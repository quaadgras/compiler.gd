// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package escape

import (
	"cmd/compile/internal/ir"
	"cmd/compile/internal/types"
)

// Handing over (doc/gd/sharing.md).
//
// A pointer sent on a channel is, to the rest of this package, in the
// hands of every goroutine: its object is shared from then on, and every
// store of a slice, a string or an interface in it takes a lock, though
// the goroutine that receives it is as a rule the only one that has it.
//
// A send hands its object over, and does not share it, if the sender
// provably has no way left to it:
//
//   - the value sent is a tracked variable (shares.go): every pointer it
//     ever holds is to an allocation of this function;
//   - neither it nor a variable that may hold the same pointer, or one
//     into the same object, is used after the send (unusedAfter);
//   - nothing else holds a pointer to the object: when the graph is
//     solved, the address of the variables' contents has flowed to those
//     variables only (holdsMoved). A callee's parameters hold it for the
//     call; what a callee keeps flows to the heap, to a result or to
//     another argument's object, which are other holders.
//
// What the object points to is shared by the send as before (the flow of
// the contents to shareLoc, and runtime.publishContents): the sender may
// well have other ways to that.
//
// If a send that was a candidate turns out not to hand over, it is an
// ordinary send, which shares the object when it runs. The variables say
// until then that the object is not shared, which is so: they are not
// used after the send.

// A moveSend is a send, or a call of (*sync.Pool).Put, that hands over
// the objects that x points to, whose contents are at c, unless
// c.moveHeld.
type moveSend struct {
	n ir.Node
	x *ir.Name
	c *location
}

// moveCandidate returns the contents of the tracked variable that n
// sends, if n may hand its object over.
func (e *escape) moveCandidate(n *ir.SendStmt) *location {
	return e.handOver(n, n.Value, n.Chan)
}

// handOver returns the contents of the tracked variable that val is, if
// the statement n, which gives it away, may hand its object over. The
// statement also evaluates other.
func (e *escape) handOver(n, val, other ir.Node) *location {
	if e.gd.Debug.NoShare != 0 || e.gd.Debug.NoMove != 0 || e.contents == nil {
		return nil
	}
	x, ok := val.(*ir.Name)
	if !ok || x.Op() != ir.ONAME || !x.Type().IsPtr() {
		return nil
	}
	c := e.contents[x]
	if c == nil || x.Curfn != e.curfn {
		return nil
	}
	class := make(map[*ir.Name]bool)
	for name, nc := range e.contents {
		if nc == c {
			if name.Curfn != e.curfn {
				return nil
			}
			class[name] = true
		}
	}
	if refers(other, class) || !unusedAfter(e.curfn, n, class) {
		return nil
	}
	e.moves = append(e.moves, moveSend{n, x, c})
	c.moveCand = true
	return c
}

// poolPut returns the contents of the variable that the call, of fns,
// puts in a sync.Pool, if the call may hand its object over.
func (e *escape) poolPut(call *ir.CallExpr, fns []*ir.Name, recv ir.Node, args []ir.Node) *location {
	if len(fns) != 1 || len(args) != 1 || !isPoolMethod(fns[0], "Put") {
		return nil
	}
	val := args[0]
	for val.Op() == ir.OCONVIFACE || val.Op() == ir.OCONVNOP || val.Op() == ir.OPAREN {
		switch v := val.(type) {
		case *ir.ConvExpr:
			val = v.X
		case *ir.ParenExpr:
			val = v.X
		}
	}
	return e.handOver(call, val, recv)
}

func isPoolMethod(fn *ir.Name, name string) bool {
	sym := fn.Sym()
	return sym != nil && sym.Pkg != nil && sym.Pkg.Path == "sync" && sym.Name == "(*Pool)."+name
}

// poolGot reports whether n is what a sync.Pool returned, as a pointer:
// p.Get().(*T).
func poolGot(n ir.Node) bool {
	for n.Op() == ir.OCONVNOP || n.Op() == ir.OPAREN {
		switch v := n.(type) {
		case *ir.ConvExpr:
			n = v.X
		case *ir.ParenExpr:
			n = v.X
		}
	}
	if n.Op() != ir.ODOTTYPE && n.Op() != ir.ODOTTYPE2 {
		return false
	}
	ta := n.(*ir.TypeAssertExpr)
	if !ta.Type().IsPtr() {
		return false
	}
	call, ok := ta.X.(*ir.CallExpr)
	if !ok || call.Op() != ir.OCALLFUNC {
		return false
	}
	fn := ir.StaticCalleeName(call.Fun)
	return fn != nil && isPoolMethod(fn, "Get")
}

// markFresh notes whether fn does nothing but return the address of an
// object that it allocates (ir.Func.FreshResult).
func markFresh(fn *ir.Func) {
	if fn.Type().NumResults() != 1 || len(fn.Body) != 1 {
		return
	}
	ret, ok := fn.Body[0].(*ir.ReturnStmt)
	if !ok || len(ret.Results) != 1 || len(ret.Init()) != 0 {
		return
	}
	n := ret.Results[0]
	for n.Op() == ir.OCONVIFACE || n.Op() == ir.OCONVNOP || n.Op() == ir.OPAREN {
		switch v := n.(type) {
		case *ir.ConvExpr:
			n = v.X
		case *ir.ParenExpr:
			n = v.X
		}
	}
	switch n.Op() {
	case ir.ONEW, ir.OPTRLIT:
		fn.FreshResult = true
	}
}

// holdsMoved reports whether root, to which the address of the contents
// c of a class of tracked variables flows, is another holder of their
// objects than those variables.
func (b *batch) holdsMoved(root, c *location) bool {
	switch root {
	case c, &b.moveLoc, &b.mutatorLoc, &b.calleeLoc, &b.blankLoc:
		// (The object may point to itself.)
		return false
	case &b.heapLoc, &b.ownHeapLoc, &b.candidateLoc, &b.shareLoc:
		return true
	}
	if root.transient || root.curfn != c.curfn {
		// A temporary that forwards; a callee's parameter or
		// variable, which has it for the call.
		return false
	}
	if name, ok := root.n.(*ir.Name); ok && name != nil && root.n.Op() == ir.ONAME && b.contents[name] == c {
		return false
	}
	return true
}

// markMoves records the sends that hand over.
func (b *batch) markMoves() {
	for _, m := range b.moves {
		if m.c.moveHeld {
			continue
		}
		switch n := m.n.(type) {
		case *ir.SendStmt:
			n.Move = true
			if b.gd.Flag.LowerM != 0 {
				b.gd.WarnfAt(n.Pos(), "%v is handed over by the send", m.x)
			}
		case *ir.CallExpr:
			n.HandOver = m.x
			if b.gd.Flag.LowerM != 0 {
				b.gd.WarnfAt(n.Pos(), "%v is handed over to the pool", m.x)
			}
		}
	}
}

// refers reports whether n mentions a variable of class.
func refers(n ir.Node, class map[*ir.Name]bool) bool {
	if n == nil {
		return false
	}
	return ir.Any(n, func(n ir.Node) bool {
		name, ok := n.(*ir.Name)
		return ok && n.Op() == ir.ONAME && class[name]
	})
}

// unusedAfter reports whether no variable of class is used once the
// statement send of fn has run, in the syntax: no statement that may
// follow it, up to a return, mentions one. It is false whenever that is not plain.
func unusedAfter(fn *ir.Func, send ir.Node, class map[*ir.Name]bool) bool {
	// A jump may lead anywhere, unless it is the return of an inlined
	// call, to the label that ends it; a deferred call runs after the
	// send.
	ends := make(map[*types.Sym]bool)
	anyBody(fn, func(n ir.Node) bool {
		if call, ok := n.(*ir.InlinedCallExpr); ok && len(call.Body) > 0 {
			if l, ok := call.Body[len(call.Body)-1].(*ir.LabelStmt); ok {
				ends[l.Label] = true
			}
		}
		return false
	})
	odd := anyBody(fn, func(n ir.Node) bool {
		switch n.Op() {
		case ir.OGOTO:
			return !ends[n.(*ir.BranchStmt).Label]
		case ir.OFALL:
			return true
		case ir.ODEFER, ir.OGO:
			return refers(n, class)
		}
		return false
	})
	if odd {
		return false
	}

	declaredIn := func(body ir.Nodes) bool {
		n := 0
		for _, stmt := range body {
			ir.Any(stmt, func(x ir.Node) bool {
				if d, ok := x.(*ir.Decl); ok && x.Op() == ir.ODCL && class[d.X] {
					n++
				}
				return false
			})
		}
		return n == len(class)
	}

	// list reports whether send is in the list, whether what may run
	// after it there is free of the variables, and whether the function
	// returns before anything after the list runs.
	var list func(l ir.Nodes) (found, ok, done bool)
	stmt := func(n ir.Node) (found, ok, done bool) {
		if n == nil {
			return false, true, false
		}
		if n == send {
			return true, true, false
		}
		switch n := n.(type) {
		case *ir.BlockStmt:
			return list(n.List)
		case *ir.IfStmt:
			if found, ok, done := list(n.Body); found {
				return true, ok, done
			}
			return list(n.Else)
		case *ir.ForStmt:
			// The loop runs again: its variables are those of
			// one turn only if they are declared in its body.
			if found, ok, done := list(n.Body); found {
				return true, ok && (done || declaredIn(n.Body)), done
			}
		case *ir.RangeStmt:
			if found, ok, done := list(n.Body); found {
				return true, ok && (done || declaredIn(n.Body)), done
			}
		case *ir.SwitchStmt:
			for _, cas := range n.Cases {
				if found, ok, done := list(cas.Body); found {
					return true, ok, done
				}
			}
		case *ir.SelectStmt:
			for _, cas := range n.Cases {
				if found, ok, done := list(cas.Body); found {
					return true, ok, done
				}
			}
		case *ir.InlinedCallExpr:
			// (Its returns are jumps to its end.)
			return list(n.Body)
		}
		return false, true, false
	}
	list = func(l ir.Nodes) (bool, bool, bool) {
		for i, n := range l {
			found, ok, done := stmt(n)
			if !found {
				continue
			}
			if !ok || done {
				return true, ok, done
			}
			for _, rest := range l[i+1:] {
				if refers(rest, class) {
					return true, false, false
				}
				if rest.Op() == ir.ORETURN {
					return true, true, true
				}
			}
			return true, true, false
		}
		return false, true, false
	}
	found, ok, _ := list(fn.Body)
	return found && ok
}

// anyBody is ir.Any for the statements of fn.
func anyBody(fn *ir.Func, cond func(ir.Node) bool) bool {
	for _, n := range fn.Body {
		if ir.Any(n, cond) {
			return true
		}
	}
	return false
}
