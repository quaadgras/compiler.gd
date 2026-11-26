// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package walk

import (
	"go/constant"

	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/types"
)

// Views of strings, slices and interfaces (doc/gd/unsafe.md).
//
// (*T)(p), p an unsafe.Pointer, must not make a view of part of a string,
// a slice or an interface: they are loaded and stored as a unit, and a
// conversion cannot tell a load from a store. Before walk, while the
// program's assignments are as written, each such conversion is looked
// at: if what p points into is known and the view is partial, the
// conversion fails at run time (walkConv); if it is known and the view is
// whole, the conversion needs no check of this; if it is not known the
// runtime checks it against the type of the object, if the object has one.

// markWholeViews sets the View of the conversions of fn.
func markWholeViews(gd *base.Invocation, fn *ir.Func, ro *ir.ReassignOracle) {
	if !ir.ShouldCheckPtr(gd, fn, 1) {
		return
	}
	markLoadOnly(fn)
	clipped := make(map[*ir.ConvExpr]bool)
	ir.Visit(fn, func(n ir.Node) {
		switch n.Op() {
		case ir.OSLICE3ARR:
			// (*[N]T)(p)[:l:m] views m elements, not N.
			n := n.(*ir.SliceExpr)
			if n.X.Op() != ir.OCONVNOP {
				return
			}
			c := n.X.(*ir.ConvExpr)
			if !isViewConv(c) {
				return
			}
			clipped[c] = true
			arr := c.Type().Elem()
			types.CalcSize(gd, arr)
			size := arr.Elem().Size() // at least one, if m is not constant
			if m, ok := viewConst(n.Max); ok && m >= 0 && m <= arr.NumElem() {
				size *= m
			}
			markWholeView(gd, fn, ro, c, size)
		case ir.OCONVNOP:
			c := n.(*ir.ConvExpr)
			if isViewConv(c) && !clipped[c] {
				types.CalcSize(gd, c.Type().Elem())
				markWholeView(gd, fn, ro, c, c.Type().Elem().Size())
			}
		}
	})
}

// markLoadOnly sets the LoadOnly of the conversions of fn from
// unsafe.Pointer to a pointer whose result is loaded through and
// nothing else: *(*T)(p), (*T)(p).f and (*[N]T)(p)[i] where they are
// values, and not what is assigned to, or whose address is taken.
func markLoadOnly(fn *ir.Func) {
	// The places that are stored in, or whose addresses are taken:
	// what is dereferenced for one of them is not only loaded.
	stored := make(map[ir.Node]bool)
	var place func(n ir.Node)
	place = func(n ir.Node) {
		for n != nil {
			stored[n] = true
			switch n.Op() {
			case ir.OPAREN:
				n = n.(*ir.ParenExpr).X
			case ir.ODOT:
				n = n.(*ir.SelectorExpr).X
			case ir.OINDEX:
				x := n.(*ir.IndexExpr).X
				if !x.Type().IsArray() {
					return
				}
				n = x
			default:
				return
			}
		}
	}
	ir.Visit(fn, func(n ir.Node) {
		switch n := n.(type) {
		case *ir.AssignStmt:
			place(n.X)
		case *ir.AssignListStmt:
			for _, l := range n.Lhs {
				place(l)
			}
		case *ir.AssignOpStmt:
			place(n.X)
		case *ir.RangeStmt:
			place(n.Key)
			place(n.Value)
		case *ir.AddrExpr:
			place(n.X)
		case *ir.SliceExpr:
			// A slice of an array is of the array's address.
			place(n.X)
		}
	})
	ir.Visit(fn, func(n ir.Node) {
		var x ir.Node
		switch n.Op() {
		case ir.ODEREF:
			x = n.(*ir.StarExpr).X
		case ir.ODOTPTR:
			x = n.(*ir.SelectorExpr).X
		default:
			return
		}
		if stored[n] {
			return
		}
		for x.Op() == ir.OPAREN {
			x = x.(*ir.ParenExpr).X
		}
		if c, ok := x.(*ir.ConvExpr); ok && c.Op() == ir.OCONVNOP && c.Type().IsPtr() && c.X.Type().IsUnsafePtr() {
			c.LoadOnly = true
		}
	})
}

// isViewConv reports whether n is a conversion from unsafe.Pointer to a
// pointer that is to be checked.
func isViewConv(n *ir.ConvExpr) bool {
	return n.View == ir.ViewUnknown && n.Type().IsPtr() && n.X.Type().IsUnsafePtr() && !n.CheckPtr()
}

// markWholeView sets the View of n, which uses size bytes of what it
// points to.
func markWholeView(gd *base.Invocation, fn *ir.Func, ro *ir.ReassignOracle, n *ir.ConvExpr, size int64) {
	src, off, ok := viewSourceIn(ro, n.X, 0, gd.CObjects)
	if !ok {
		if gd.Debug.UnsafeWhole >= 2 {
			gd.WarnfAt(n.Pos(), "unsafewhole: unknown source: %v", n.Type())
		}
		return
	}
	types.CalcSize(gd, src)
	if src.Size() == 0 {
		// A pointer to a type of no size says nothing of what it
		// points to: C's void * is one (internal/c23's Void), and
		// what it is converted to is the runtime's to compare with
		// the object, as for an unsafe.Pointer.
		return
	}
	if off < 0 || size > src.Size()-off {
		// The view goes past what the pointer is known to point
		// to, which may be one element of many: whether it fits
		// in what is there is for the runtime to say.
		return
	}
	k := types.PartialView(src, off, n.Type().Elem(), size)
	if k == types.NotWhole {
		n.View = ir.ViewWhole
		if gd.CObjects && !n.Type().Elem().HasPointers() && (!src.HasPointers() || off >= types.PtrDataSize(gd, src)) {
			// In a package of C's, a view with no pointer of
			// what has none there: the pointer it is made of
			// lets through the same loads and stores, for it
			// points to src (its conversion was checked).
			n.CUnchecked = true
		}
		// C's types are not compared yet: a union is words that
		// its members are converted over. What the compiler writes
		// itself is not: the adapter of a method for a value that
		// is in an interface's own words makes the value of them.
		if !gd.CObjects && !fn.Wrapper() {
			switch why := types.CompatView(gd, src, off, n.Type().Elem(), size, n.LoadOnly); {
			case why == types.ViewOK:
			case gd.Debug.UnsafeWhole != 0:
				gd.WarnfAt(n.Pos(), "unsafewhole: incompatible view (%d): %v at offset %d of %v", why, n.Type(), off, src)
			default:
				n.View = ir.ViewPtrInt + ir.ViewKind(why-types.ViewPtrInt)
			}
		}
		return
	}
	if gd.Debug.UnsafeWhole != 0 {
		gd.WarnfAt(n.Pos(), "unsafewhole: partial view of %v: %v at offset %d of %v", k, n.Type(), off, src)
		return
	}
	n.View = ir.ViewPartial
}

// viewSource returns the type of what the pointer n points into, and the
// offset in it, if they are known.
func viewSource(ro *ir.ReassignOracle, n ir.Node, depth int) (*types.Type, int64, bool) {
	return viewSourceIn(ro, n, depth, false)
}

// viewSourceIn is viewSource, for a package of C's if c is set: there a
// conversion of an unsafe.Pointer to a pointer may give another pointer
// than it is given (runtime.checkptrConvC: a union's member), so what it
// gives is only known to point to its type.
func viewSourceIn(ro *ir.ReassignOracle, n ir.Node, depth int, inC bool) (*types.Type, int64, bool) {
	if depth > 32 {
		return nil, 0, false
	}
	depth++
	switch n.Op() {
	case ir.OPAREN:
		return viewSourceIn(ro, n.(*ir.ParenExpr).X, depth, inC)
	case ir.OCONVNOP:
		if cv := n.(*ir.ConvExpr); inC && cv.X.Type().IsUnsafePtr() && cv.Type().IsPtr() {
			break
		}
		if t, off, ok := viewSourceIn(ro, n.(*ir.ConvExpr).X, depth, inC); ok {
			return t, off, true
		}
	case ir.OADDR:
		return viewPlace(ro, n.(*ir.AddrExpr).X, depth, inC)
	case ir.OUNSAFEADD, ir.OADD:
		n := n.(*ir.BinaryExpr)
		x, y := n.X, n.Y
		if n.Op() == ir.OADD && ir.IsConst(x, constant.Int) {
			x, y = y, x
		}
		c, ok := viewConst(y)
		if !ok {
			return nil, 0, false
		}
		t, off, ok := viewSourceIn(ro, x, depth, inC)
		return t, off + c, ok
	case ir.OSUB:
		n := n.(*ir.BinaryExpr)
		c, ok := viewConst(n.Y)
		if !ok {
			return nil, 0, false
		}
		t, off, ok := viewSourceIn(ro, n.X, depth, inC)
		return t, off - c, ok
	case ir.ONAME:
		if ro == nil {
			break
		}
		if v := ro.StaticValue(n); v != n {
			if t, off, ok := viewSourceIn(ro, v, depth, inC); ok {
				return t, off, true
			}
		}
	}
	if t := n.Type(); t != nil && t.IsPtr() {
		return t.Elem(), 0, true
	}
	return nil, 0, false
}

// viewPlace is viewSource for the address of the variable or part of one n.
func viewPlace(ro *ir.ReassignOracle, n ir.Node, depth int, inC bool) (*types.Type, int64, bool) {
	switch n.Op() {
	case ir.OPAREN:
		return viewPlace(ro, n.(*ir.ParenExpr).X, depth, inC)
	case ir.ODOT:
		n := n.(*ir.SelectorExpr)
		t, off, ok := viewPlace(ro, n.X, depth, inC)
		return t, off + n.Offset(), ok
	case ir.ODOTPTR:
		n := n.(*ir.SelectorExpr)
		t, off, ok := viewSourceIn(ro, n.X, depth, inC)
		return t, off + n.Offset(), ok
	case ir.ODEREF:
		return viewSourceIn(ro, n.(*ir.StarExpr).X, depth, inC)
	case ir.OINDEX:
		n := n.(*ir.IndexExpr)
		if i, ok := viewConst(n.Index); ok && n.X.Type().IsArray() {
			t, off, ok := viewPlace(ro, n.X, depth, inC)
			return t, off + i*n.Type().Size(), ok
		}
	}
	return n.Type(), 0, true
}

// viewConst returns the value of n if it is an integer constant that fits
// in an int64.
func viewConst(n ir.Node) (int64, bool) {
	if n == nil || !ir.IsConst(n, constant.Int) {
		return 0, false
	}
	return constant.Int64Val(n.Val())
}

// cAddInside reports whether n, unsafe.Add(p, c) in a package of C's
// with c a constant, is inside what p is known to point to (viewSource),
// and not at its end: in the object that p is in, which is all that the
// check of the addition asks (runtime.checkptrArithmeticC).
func cAddInside(gd *base.Invocation, n *ir.BinaryExpr) bool {
	c, ok := viewConst(n.Y)
	if !ok {
		return false
	}
	src, off, ok := viewSourceIn(nil, n.X, 0, true)
	if !ok {
		return false
	}
	types.CalcSize(gd, src)
	return src.Size() > 0 && off+c >= 0 && off+c < src.Size()
}
