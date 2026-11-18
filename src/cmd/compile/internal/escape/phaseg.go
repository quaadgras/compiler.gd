// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package escape

import (
	"cmd/compile/internal/ir"
	"cmd/compile/internal/types"
)

// gd Phase G: which allocations of a function may be placed in the
// buffer its caller passes for a pointer result (outBuf_k) instead of
// on the heap. See doc/gd/escape-bits.md, Phase G.
//
// The caller passes a buffer only where the result does not outlive
// its frame, and it holds no live value when the call starts (see
// walk's phaseGCallSiteRewrite). So placing an object O there is sound
// when, during the call, O is the only object whose address reaches
// result k: nothing else can be in the buffer at the same time, and
// nothing that outlives the call can point into it. "Users" of result
// k are all the locations whose address reaches it, of any kind:
// allocations, named locals, the results of calls (which forward the
// buffer on, "chain-fold"), slice backing stores. Counting them all
// also covers an object stored inside another returned object
// (return &T{next: x}): the container is a second user.
//
// A user is placed in the buffer (tagged) when it is the only user,
// or when every user is the operand of a return statement itself (if
// c { return g() }; return &T{}), which ends the function as soon as it
// is made, so no two are ever live at once. A tagged user must be an
// allocation of the function (new, &T{...}, a local whose address is
// taken, a call whose result is forwarded) of exactly the result's
// element type, and its address must reach nothing else that outlives
// it: the heap, a dynamic call's candidate location, another result
// of any function in the batch, or a variable declared outside its
// loop (which could keep an earlier iteration's instance alive): a
// local, or result k itself when it is assigned by name rather than
// only by return statements, which end the function.
//
// The solver's own walks skip locations that already escape, so which
// roots it records leaks for depends on the order it walks them in;
// these walks don't skip.
//
// A function that tags nothing for a result says so in its export
// data (escape.finish), and its callers pass it no buffer.

// phaseGTags returns, for each location to place in its function's
// caller's buffer, the 0-based index of that buffer (outBuf ordinal).
func (b *batch) phaseGTags(fns []*ir.Func) map[*location]int {
	if b.gd.Debug.GdEscBitsOff != 0 {
		return nil
	}
	var todo []*ir.Func
	for _, fn := range fns {
		if phaseGEligibleFunc(fn) {
			todo = append(todo, fn)
		}
	}
	if len(todo) == 0 {
		return nil
	}

	// What reaches the heap: what the heap's edges lead to, and the
	// locations flow marked as escaping without an edge, and what
	// leads to them.
	heapRoots := []*location{&b.heapLoc, &b.ownHeapLoc}
	for _, l := range b.allLocs {
		if l.escapedInGraph || l.isDynRoot {
			// (A store through a pointer is a store in the heap
			// here, whatever range of addresses it is in: see
			// dynRootOf.)
			heapRoots = append(heapRoots, l)
		}
	}
	heap := b.addrReach(heapRoots...)
	for _, l := range heapRoots[1:] {
		heap[l] = true
	}
	cand := b.addrReach(&b.candidateLoc)
	results := make(map[*location]map[*location]bool)
	for _, l := range b.allLocs {
		if l.paramOut {
			results[l] = b.addrReach(l)
		}
	}

	tags := make(map[*location]int)
	for _, fn := range todo {
		b.phaseGFunc(fn, heap, cand, results, tags)
	}
	return tags
}

func phaseGEligibleFunc(fn *ir.Func) bool {
	t := fn.Type()
	if t == nil || !t.GdReturnOutBuf() || t.NumOutBufs() == 0 {
		return false
	}
	// A closure's locals can be kept by its enclosing function's,
	// which these walks don't look at; shape functions are reached
	// through dictionaries.
	return fn.OClosure == nil && !t.HasShape()
}

func (b *batch) phaseGFunc(fn *ir.Func, heap, cand map[*location]bool, results map[*location]map[*location]bool, tags map[*location]int) {
	sig := fn.Type()

	// The operands of fn's return statements, by result index.
	terminal := make(map[ir.Node]int)
	nres := len(sig.Results())
	ir.VisitList(fn.Body, func(n ir.Node) {
		ret, ok := n.(*ir.ReturnStmt)
		if !ok || len(ret.Results) != nres {
			return
		}
		for k, r := range ret.Results {
			terminal[r] = k
		}
	})

	// fn's result locations, by result index.
	resLocs := make(map[int]*location)
	for _, n := range fn.Dcl {
		if n.Class == ir.PPARAMOUT && n.Canonical().Opt != nil {
			l := b.oldLoc(n)
			resLocs[l.resultIndex-1] = l
		}
	}

	// Locals that outlive a user in a loop: walked on demand.
	var outer map[*location]map[*location]bool
	keptAcrossIterations := func(u *location) bool {
		if u.loopDepth <= 1 {
			return false
		}
		if outer == nil {
			outer = make(map[*location]map[*location]bool)
		}
		for _, l := range b.allLocs {
			if l.curfn != fn || l.loopDepth >= u.loopDepth || l.paramOut {
				continue
			}
			r, ok := outer[l]
			if !ok {
				r = b.addrReach(l)
				outer[l] = r
			}
			if r[u] {
				return true
			}
		}
		return false
	}

	for ri, r := range sig.Results() {
		if r.Type == nil || !r.Type.IsPtr() {
			continue
		}
		root := resLocs[ri]
		if root == nil {
			continue
		}
		elem := r.Type.Elem()
		if elem.Size() == 0 {
			continue
		}
		// A call of a function that never uses its buffer makes its
		// result elsewhere: it is never in this one.
		var users []*location
		for u := range results[root] {
			if call, ok := u.n.(*ir.CallExpr); ok && u.curfn == fn && ir.CalleeOutBufUnused(call, 0) {
				continue
			}
			users = append(users, u)
		}
		if len(users) == 0 {
			continue
		}
		ok := true
		allTerminal := true
		for _, u := range users {
			if !phaseGTaggable(u, fn, elem) || heap[u] || cand[u] {
				ok = false
				break
			}
			for other, reach := range results {
				if other != root && reach[u] {
					ok = false
					break
				}
			}
			if !ok || keptAcrossIterations(u) {
				ok = false
				break
			}
			if u.loopDepth > root.loopDepth && root.assignedInBody {
				// for ... { p = &T{next: p} }: the result
				// itself keeps an earlier iteration's u.
				ok = false
				break
			}
			if k, isTerm := terminal[u.n]; !isTerm || k != ri {
				allTerminal = false
			}
		}
		if !ok || len(users) > 1 && !allTerminal {
			continue
		}
		ord := types.OutBufOrdinal(sig, ri)
		if ord < 0 {
			continue
		}
		for _, u := range users {
			tags[u] = ord
		}
	}
}

// phaseGTaggable reports whether u is an allocation of fn that can be
// made in a buffer for a result of type *elem.
func phaseGTaggable(u *location, fn *ir.Func, elem *types.Type) bool {
	if u.curfn != fn || u.n == nil {
		return false
	}
	switch n := u.n.(type) {
	case *ir.Name:
		return n.Op() == ir.ONAME && n.Class == ir.PAUTO && !n.EscCandidate() &&
			types.Identical(n.Type(), elem)
	case *ir.UnaryExpr:
		return n.Op() == ir.ONEW && types.Identical(n.Type().Elem(), elem)
	case *ir.AddrExpr:
		return n.Op() == ir.OPTRLIT && types.Identical(n.Type().Elem(), elem)
	case *ir.CallExpr:
		// The result of a call to a function with an outBuf for it,
		// forwarded fn's buffer (walk's forwardParentOutBuf).
		if n.Op() != ir.OCALLFUNC && n.Op() != ir.OCALLINTER || n.Fun == nil {
			return false
		}
		t := n.Fun.Type()
		if t == nil || t.Kind() != types.TFUNC || !t.GdReturnOutBuf() || t.NumOutBufs() != 1 || len(t.Results()) != 1 {
			return false
		}
		r := t.Results()[0].Type
		return r != nil && r.IsPtr() && types.Identical(r.Elem(), elem)
	}
	return false
}

// addrReach returns the locations whose address flows to one of
// roots: those the solver's walkOne from it reaches with derefs < 0.
// Unlike walkOne it doesn't stop at locations that escape. It doesn't
// see flows to the heap that flow recorded no edge for
// (escapedInGraph).
func (b *batch) addrReach(roots ...*location) map[*location]bool {
	best := make(map[*location]int)
	addr := make(map[*location]bool)
	var todo []*location
	for _, r := range roots {
		best[r] = 0
		todo = append(todo, r)
	}
	for len(todo) > 0 {
		l := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		derefs := best[l]
		if derefs < 0 {
			// l's address flows to root; what flows into l is a
			// value stored in l, whose address does not (walkOne's
			// "root = &l; l = x").
			addr[l] = true
			derefs = 0
		}
		for _, e := range l.edges {
			if e.share {
				continue
			}
			d := derefs + e.derefs
			if old, seen := best[e.src]; !seen || d < old {
				best[e.src] = d
				todo = append(todo, e.src)
			}
		}
	}
	for _, r := range roots {
		delete(addr, r)
	}
	return addr
}
