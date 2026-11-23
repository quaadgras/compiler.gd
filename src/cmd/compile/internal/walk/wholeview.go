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
			markWholeView(gd, ro, c, size)
		case ir.OCONVNOP:
			c := n.(*ir.ConvExpr)
			if isViewConv(c) && !clipped[c] {
				types.CalcSize(gd, c.Type().Elem())
				markWholeView(gd, ro, c, c.Type().Elem().Size())
			}
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
func markWholeView(gd *base.Invocation, ro *ir.ReassignOracle, n *ir.ConvExpr, size int64) {
	src, off, ok := viewSource(ro, n.X, 0)
	if !ok {
		if gd.Debug.UnsafeWhole >= 2 {
			gd.WarnfAt(n.Pos(), "unsafewhole: unknown source: %v", n.Type())
		}
		return
	}
	types.CalcSize(gd, src)
	k := types.PartialView(src, off, n.Type().Elem(), size)
	if k == types.NotWhole {
		n.View = ir.ViewWhole
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
	if depth > 32 {
		return nil, 0, false
	}
	depth++
	switch n.Op() {
	case ir.OPAREN:
		return viewSource(ro, n.(*ir.ParenExpr).X, depth)
	case ir.OCONVNOP:
		if t, off, ok := viewSource(ro, n.(*ir.ConvExpr).X, depth); ok {
			return t, off, true
		}
	case ir.OADDR:
		return viewPlace(ro, n.(*ir.AddrExpr).X, depth)
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
		t, off, ok := viewSource(ro, x, depth)
		return t, off + c, ok
	case ir.OSUB:
		n := n.(*ir.BinaryExpr)
		c, ok := viewConst(n.Y)
		if !ok {
			return nil, 0, false
		}
		t, off, ok := viewSource(ro, n.X, depth)
		return t, off - c, ok
	case ir.ONAME:
		if v := ro.StaticValue(n); v != n {
			if t, off, ok := viewSource(ro, v, depth); ok {
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
func viewPlace(ro *ir.ReassignOracle, n ir.Node, depth int) (*types.Type, int64, bool) {
	switch n.Op() {
	case ir.OPAREN:
		return viewPlace(ro, n.(*ir.ParenExpr).X, depth)
	case ir.ODOT:
		n := n.(*ir.SelectorExpr)
		t, off, ok := viewPlace(ro, n.X, depth)
		return t, off + n.Offset(), ok
	case ir.ODOTPTR:
		n := n.(*ir.SelectorExpr)
		t, off, ok := viewSource(ro, n.X, depth)
		return t, off + n.Offset(), ok
	case ir.ODEREF:
		return viewSource(ro, n.(*ir.StarExpr).X, depth)
	case ir.OINDEX:
		n := n.(*ir.IndexExpr)
		if i, ok := viewConst(n.Index); ok && n.X.Type().IsArray() {
			t, off, ok := viewPlace(ro, n.X, depth)
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
