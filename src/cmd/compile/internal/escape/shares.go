// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package escape

import (
	"fmt"

	"cmd/compile/internal/ir"
)

// Goroutine sharing, statically (doc/gd/sharing.md, runtime/shared.go).
//
// The solver marks with attrShares every location whose address may flow
// to where another goroutine can reach it. An allocation without the mark
// is reached, while the function that made it runs, by that function's
// goroutine only: what the function returns is the caller's, which is the
// same goroutine.
//
// markUnshared turns that into facts about variables, which is what the
// code generator has when it stores or loads through one:
//
//   - ir.UnsharedStorage: the variable's own memory is not shared (it
//     matters for one that is moved to the heap);
//   - ir.UnsharedPointee: every pointer the variable ever holds is to
//     such an allocation, or nil.
//
// The stores and loads in unshared memory need no lock, no publication
// and no validation.
//
// The graph knows where an address flows, but not everything a variable
// may be assigned (a call's result, a load from the heap). So the
// variables that count are the tracked ones: those whose every
// assignment, found in the syntax, has a source that is plainly a local
// allocation or another tracked variable (trackPointers).
//
// A store through a pointer is, to escape analysis, a store in the heap,
// which would make everything that a function links together shared. For
// a tracked variable x the graph has instead a location for what x's
// allocations hold, with x = &contents: a store through x flows there, a
// load through x flows from there, and the contents are shared exactly
// when x's value is. For escaping, the contents are the heap, as before.

// trackPointers finds fn's tracked variables and gives each the location
// of its contents. It is called before fn's graph is built.
func (b *batch) trackPointers(fn *ir.Func) {
	if b.gd.Debug.NoShare != 0 || b.gd.Debug.NoLocal != 0 {
		return
	}
	cand, srcs := b.pointerSources(fn)
	if len(cand) == 0 {
		return
	}
	dropUnfresh(cand, srcs, func(n ir.Node) bool { return true })
	if b.contents == nil {
		b.contents = make(map[*ir.Name]*location)
		b.sources = make(map[*ir.Name][]ir.Node)
	}

	// Two variables that may hold the same pointer have the same
	// contents: a store through one is loaded through the other. A
	// tracked variable gets a pointer another holds only from it.
	class := make(map[*ir.Name]*ir.Name)
	var find func(n *ir.Name) *ir.Name
	find = func(n *ir.Name) *ir.Name {
		if p, ok := class[n]; ok && p != n {
			r := find(p)
			class[n] = r
			return r
		}
		return n
	}
	for name := range cand {
		for _, src := range srcs[name] {
			pointerVars(src, func(v *ir.Name) {
				if cand[v] {
					class[find(name)] = find(v)
				}
			})
		}
	}
	for name := range cand {
		root := find(name)
		c := b.contents[root]
		if c == nil {
			c = &location{
				curfn:     fn,
				loopDepth: 1,
				attrs:     attrEscapes | attrPersists | attrMutates | attrCalls,
			}
			b.allLocs = append(b.allLocs, c)
			b.contents[root] = c
		}
		b.contents[name] = c
		b.sources[name] = srcs[name]
		// name = &contents
		x := b.oldLoc(name)
		x.edges = append(x.edges, edge{src: c, derefs: -1, share: true})
	}
}

// pointerVars calls f for the variables whose pointer the fresh
// expression n may be: see dropUnfresh.
func pointerVars(n ir.Node, f func(v *ir.Name)) {
	switch n.Op() {
	case ir.OCONVNOP:
		pointerVars(n.(*ir.ConvExpr).X, f)
	case ir.OPAREN:
		pointerVars(n.(*ir.ParenExpr).X, f)
	case ir.OADDR:
		if p := addrThrough(n.(*ir.AddrExpr)); p != nil {
			pointerVars(p, f)
		}
	case ir.OAPPEND:
		pointerVars(n.(*ir.CallExpr).Args[0], f)
	case ir.OSLICE, ir.OSLICE3:
		pointerVars(n.(*ir.SliceExpr).X, f)
	case ir.ONAME:
		f(n.(*ir.Name))
	case ir.OINLCALL:
		if n := n.(*ir.InlinedCallExpr); len(n.ReturnVars) == 1 {
			pointerVars(n.ReturnVars[0], f)
		}
	}
}

// addrThrough returns p if n is the address of a part of what the
// pointer or slice p points to (&p.f, &p[i], &*p), and nil otherwise.
func addrThrough(n *ir.AddrExpr) ir.Node {
	switch x := ir.OuterValue(n.X).(type) {
	case *ir.SelectorExpr:
		if x.Op() == ir.ODOTPTR {
			return x.X
		}
	case *ir.StarExpr:
		return x.X
	case *ir.IndexExpr:
		if x.Op() == ir.OINDEX && x.X.Type().IsSlice() {
			return x.X
		}
	}
	return nil
}

// contentsOf returns the location of what the pointer or slice n's
// allocations hold, if n is a tracked variable, and nil otherwise.
func (b *batch) contentsOf(n ir.Node) *location {
	if b.contents == nil {
		return nil
	}
	for {
		switch x := n.(type) {
		case *ir.ParenExpr:
			n = x.X
			continue
		case *ir.ConvExpr:
			if x.Op() == ir.OCONVNOP && x.X.Type().HasPointers() {
				n = x.X
				continue
			}
		case *ir.Name:
			if x.Op() == ir.ONAME {
				return b.contents[x.Canonical()]
			}
		}
		return nil
	}
}

// pointerSources returns the local variables of fn that hold pointers,
// whose address is not taken, that no closure has, and that are only
// assigned by plain assignments, with the sources of those.
func (b *batch) pointerSources(fn *ir.Func) (cand map[*ir.Name]bool, srcs map[*ir.Name][]ir.Node) {
	cand = make(map[*ir.Name]bool)
	srcs = make(map[*ir.Name][]ir.Node)
	for _, n := range fn.Dcl {
		if n.Class != ir.PAUTO || n.IsClosureVar() || n.Addrtaken() || !n.Type().HasPointers() {
			continue
		}
		if !n.Type().IsPtr() && !n.Type().IsSlice() && !n.Type().IsUnsafePtr() {
			continue
		}
		switch n.Defn.(type) {
		case nil, *ir.AssignStmt, *ir.AssignListStmt:
			cand[n] = true
		}
	}
	if len(cand) == 0 {
		return
	}
	drop := func(n ir.Node) {
		if name, ok := n.(*ir.Name); ok {
			delete(cand, name)
		}
	}
	ir.VisitFuncAndClosures(fn, func(n ir.Node) {
		switch n := n.(type) {
		case *ir.AssignStmt:
			if n.Op() != ir.OAS {
				drop(n.X)
				break
			}
			if name, ok := n.X.(*ir.Name); ok && cand[name] && n.Y != nil {
				srcs[name] = append(srcs[name], n.Y)
			}
		case *ir.AssignListStmt:
			if n.Op() == ir.OAS2 && len(n.Lhs) == len(n.Rhs) {
				for i, l := range n.Lhs {
					if name, ok := l.(*ir.Name); ok && cand[name] {
						srcs[name] = append(srcs[name], n.Rhs[i])
					}
				}
				break
			}
			for _, l := range n.Lhs {
				drop(l)
			}
		case *ir.AssignOpStmt:
			drop(n.X)
		case *ir.RangeStmt:
			drop(n.Key)
			drop(n.Value)
		case *ir.CaseClause:
			if n.Var != nil {
				drop(n.Var)
			}
		case *ir.ClosureExpr:
			for _, cv := range n.Func.ClosureVars {
				drop(cv.Canonical())
			}
		}
	})
	return
}

// dropUnfresh removes from cand the variables with a source that is not
// fresh: an allocation that alloc accepts, nil, a variable that stays in
// cand, or a slice or the address of a part of what one of those points
// to.
func dropUnfresh(cand map[*ir.Name]bool, srcs map[*ir.Name][]ir.Node, alloc func(n ir.Node) bool) {
	var fresh func(n ir.Node) bool
	fresh = func(n ir.Node) bool {
		switch n.Op() {
		case ir.ONIL:
			return true
		case ir.OCONVNOP, ir.OPAREN:
			switch n := n.(type) {
			case *ir.ConvExpr:
				// Not an integer made a pointer.
				return n.X.Type().HasPointers() && fresh(n.X)
			case *ir.ParenExpr:
				return fresh(n.X)
			}
		case ir.ONEW, ir.OPTRLIT, ir.OMAKESLICE, ir.OSLICELIT:
			return alloc(n)
		case ir.OADDR:
			n := n.(*ir.AddrExpr)
			// A part of what a fresh pointer points to. (Not the
			// address of a variable: what is stored through it
			// would be loaded from the variable.)
			if p := addrThrough(n); p != nil {
				return fresh(p)
			}
		case ir.OAPPEND:
			n := n.(*ir.CallExpr)
			return alloc(n) && fresh(n.Args[0])
		case ir.OSLICE, ir.OSLICE3:
			n := n.(*ir.SliceExpr)
			return n.X.Type().IsSlice() && fresh(n.X)
		case ir.ONAME:
			name := n.(*ir.Name)
			return cand[name]
		case ir.OINLCALL:
			n := n.(*ir.InlinedCallExpr)
			if len(n.ReturnVars) == 1 {
				return fresh(n.ReturnVars[0])
			}
		}
		return false
	}
	for changed := true; changed; {
		changed = false
		for name := range cand {
			for _, src := range srcs[name] {
				if !fresh(src) {
					delete(cand, name)
					changed = true
					break
				}
			}
		}
	}
}

// markUnshared records on fns' variables what the solved graph says no
// other goroutine can reach.
func (b *batch) markUnshared(fns []*ir.Func) {
	if b.gd.Debug.NoShare != 0 || b.gd.Debug.NoLocal != 0 {
		return
	}
	// The allocations that are shared, by the expression or the variable
	// that makes them.
	shared := make(map[ir.Node]bool)
	alloc := make(map[ir.Node]bool)
	for _, loc := range b.allLocs {
		if loc.n == nil {
			continue
		}
		alloc[loc.n] = true
		if loc.hasAttr(attrShares) {
			shared[loc.n] = true
		}
	}
	unshared := func(n ir.Node) bool { return alloc[n] && !shared[n] }

	for _, fn := range fns {
		cand := make(map[*ir.Name]bool)
		for _, n := range fn.Dcl {
			if n.Class != ir.PAUTO || n.IsClosureVar() {
				continue
			}
			loc, ok := n.Opt.(*location)
			if !ok || loc == nil || loc.hasAttr(attrShares) {
				continue
			}
			n.Unshared |= ir.UnsharedStorage
			if b.contents[n] != nil && !loc.captured && !loc.addrtaken && !b.contents[n].hasAttr(attrShares) {
				cand[n] = true
			}
		}
		dropUnfresh(cand, b.sources, unshared)
		for name := range cand {
			name.Unshared |= ir.UnsharedPointee
		}
		if b.gd.Flag.LowerM >= 3 {
			for _, n := range fn.Dcl {
				if n.Class == ir.PAUTO && n.Type().HasPointers() {
					loc, _ := n.Opt.(*location)
					fmt.Printf("%s: %v unshared=%d shares=%v tracked=%v\n", b.gd.FmtPos(n.Pos()), n, n.Unshared, loc != nil && loc.hasAttr(attrShares), b.contents[n] != nil)
				}
			}
		}
	}
}
