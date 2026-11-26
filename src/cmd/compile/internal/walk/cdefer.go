// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package walk

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/ssa"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
)

// Claims that wait for a store (doc/gd/cc.md).
//
// In a package of C's a conversion to a pointer to integers looks at
// the classes of the words it is over, and claims those that nothing
// claimed (runtime/cclaim.go): what is stored through the pointer is
// then an integer over nothing that is a pointer. A load asks for none
// of that, for an integer may be loaded from anything. So a pointer that
// a loop steps through an array to read it, C's
//
//	while (i->w < pivot) i++;
//
// need not be looked at in each step, as long as it is where it is
// stored through, or given to something else that may store through it.
//
// deferClaims does that for the local variables of fn that are pointers
// to integers, are not captured, and whose addresses are not taken. A
// conversion that is assigned to such a variable is only checked for
// being in its object, as one that is only loaded through is
// (ConvExpr.LoadOnly). The variable is then used as it is where it is
// loaded through, stepped, compared, or assigned to another such
// variable; any other use of it, a store through it, an argument, a
// result, a store of it, is a use of the pointer converted again, to its
// own type, with the whole check (ConvExpr.CRecheck).
func deferClaims(gd *base.Invocation, fn *ir.Func, stored map[ir.Node]bool) {
	if !gd.CObjects || gd.Flag.N != 0 || gd.Debug.CHeaders != 0 || types.PtrSize != 8 {
		return
	}
	cand := make(map[*ir.Name]bool)
	for _, n := range fn.Dcl {
		if n.Class != ir.PAUTO && n.Class != ir.PPARAM || n.Addrtaken() || n.Type() == nil || !n.Type().IsPtr() {
			continue
		}
		elem := n.Type().Elem()
		types.CalcSize(gd, elem)
		if elem.HasPointers() || elem.Alignment() == 1 || elem.Size() == 0 || elem.Size() > ssa.CConvWords*int64(types.PtrSize) {
			continue
		}
		cand[n] = true
	}
	if len(cand) == 0 {
		return
	}
	isConv := func(n ir.Node) *ir.ConvExpr {
		if c, ok := n.(*ir.ConvExpr); ok && c.Op() == ir.OCONVNOP && c.Type().IsPtr() && c.X.Type().IsUnsafePtr() && !c.CheckPtr() {
			return c
		}
		return nil
	}
	// A variable that a closure uses, or that is assigned by anything
	// but a plain assignment, is left as it is; so is one that no
	// conversion is assigned to, for which nothing would change.
	assigned := make(map[*ir.Name]bool)
	drop := func(n ir.Node) {
		if n != nil && n.Op() == ir.ONAME {
			delete(cand, n.(*ir.Name))
		}
	}
	ir.VisitFuncAndClosures(fn, func(n ir.Node) {
		switch n := n.(type) {
		case *ir.Name:
			if n.Op() == ir.ONAME && n.IsClosureVar() {
				delete(cand, n.Canonical())
			}
		case *ir.AssignStmt:
			if x, ok := n.X.(*ir.Name); ok && n.Y != nil && isConv(n.Y) != nil {
				assigned[x] = true
			}
		case *ir.AssignListStmt:
			for _, l := range n.Lhs {
				drop(l)
			}
		case *ir.AssignOpStmt:
			drop(n.X)
		case *ir.RangeStmt:
			drop(n.Key)
			drop(n.Value)
		}
	})
	for n := range cand {
		if !assigned[n] {
			delete(cand, n)
		}
	}
	if len(cand) == 0 {
		return
	}

	// held returns the variable that the value of n is, if it is one
	// of those: the variable, or a call that was inlined whose result
	// is in it.
	held := func(n ir.Node) bool {
		for n.Op() == ir.OPAREN {
			n = n.(*ir.ParenExpr).X
		}
		switch n := n.(type) {
		case *ir.Name:
			return n.Op() == ir.ONAME && cand[n]
		case *ir.InlinedCallExpr:
			if len(n.ReturnVars) == 1 {
				if v, ok := n.ReturnVars[0].(*ir.Name); ok {
					return cand[v]
				}
			}
		}
		return false
	}
	var edit func(n ir.Node) ir.Node
	// inside edits what is in n, which is used as it is.
	inside := func(n ir.Node) {
		for n.Op() == ir.OPAREN {
			n = n.(*ir.ParenExpr).X
		}
		if c, ok := n.(*ir.InlinedCallExpr); ok {
			for i, s := range c.Body {
				c.Body[i] = edit(s)
			}
		}
	}
	recheck := func(n ir.Node) ir.Node {
		c := typecheck.ConvNop(gd, typecheck.ConvNop(gd, n, types.Types[types.TUNSAFEPTR]), n.Type()).(*ir.ConvExpr)
		// (ConvNop takes a conversion it is asked for to be the
		// compiler's own, which is not checked: this one is.)
		c.SetCheckPtr(false)
		c.CRecheck = true
		return c
	}
	edit = func(n ir.Node) ir.Node {
		if n == nil {
			return n
		}
		switch n := n.(type) {
		case *ir.Name:
			if n.Op() == ir.ONAME && cand[n] {
				return recheck(n)
			}
			return n
		case *ir.Decl:
			return n
		case *ir.InlinedCallExpr:
			for i, s := range n.Body {
				n.Body[i] = edit(s)
			}
			if held(n) {
				return recheck(n)
			}
			return n
		case *ir.AssignStmt:
			x, ok := n.X.(*ir.Name)
			if !ok || x.Op() != ir.ONAME || !cand[x] {
				break
			}
			// (Its Init, if it has one, is edited with the rest.)
			saved := n.X
			ir.EditChildren(n, edit)
			n.X = saved
			if c := isConv(n.Y); c != nil {
				// Checked where the variable is used for more
				// than a load.
				c.LoadOnly, c.CRecheck = true, false
			}
			return n
		case *ir.StarExpr:
			if n.Op() == ir.ODEREF && !stored[n] && held(n.X) {
				inside(n.X)
				return n
			}
		case *ir.SelectorExpr:
			if n.Op() == ir.ODOTPTR && !stored[n] && held(n.X) {
				inside(n.X)
				return n
			}
		case *ir.ConvExpr:
			if n.Op() == ir.OCONVNOP && (n.Type().IsUnsafePtr() || n.Type().IsUintptr()) && held(n.X) {
				inside(n.X)
				return n
			}
		case *ir.BinaryExpr:
			if n.Op() == ir.OEQ || n.Op() == ir.ONE {
				if held(n.X) {
					inside(n.X)
				} else {
					n.X = edit(n.X)
				}
				if held(n.Y) {
					inside(n.Y)
				} else {
					n.Y = edit(n.Y)
				}
				return n
			}
		}
		ir.EditChildren(n, edit)
		return n
	}
	for i, s := range fn.Body {
		fn.Body[i] = edit(s)
	}
}
