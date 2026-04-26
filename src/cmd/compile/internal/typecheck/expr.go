// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typecheck

import (
	"fmt"
	"go/constant"
	"internal/types/errors"
	"strings"

	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/types"
	"cmd/internal/src"
)

func tcShift(gd *base.Invocation, n, l, r ir.Node) (ir.Node, ir.Node, *types.Type) {
	if l.Type() == nil || r.Type() == nil {
		return l, r, nil
	}

	r = DefaultLit(gd, r, types.Types[types.TUINT])
	t := r.Type()
	if !t.IsInteger() {
		gd.Errorf("invalid operation: %v (shift count type %v, must be integer)", n, r.Type())
		return l, r, nil
	}
	t = l.Type()
	if t != nil && t.Kind() != types.TIDEAL && !t.IsInteger() {
		gd.Errorf("invalid operation: %v (shift of type %v)", n, t)
		return l, r, nil
	}

	// no DefaultLit for left
	// the outer context gives the type
	t = l.Type()
	if (l.Type() == types.UntypedFloat || l.Type() == types.UntypedComplex) && r.Op() == ir.OLITERAL {
		t = types.UntypedInt
	}
	return l, r, t
}

// tcArith typechecks operands of a binary arithmetic expression.
// The result of tcArith MUST be assigned back to original operands,
// t is the type of the expression, and should be set by the caller. e.g:
//
//	n.X, n.Y, t = tcArith(gd, n, op, n.X, n.Y)
//	n.SetType(t)
func tcArith(gd *base.Invocation, n ir.Node, op ir.Op, l, r ir.Node) (ir.Node, ir.Node, *types.Type) {
	l, r = defaultlit2(gd, l, r, false)
	if l.Type() == nil || r.Type() == nil {
		return l, r, nil
	}
	t := l.Type()
	if t.Kind() == types.TIDEAL {
		t = r.Type()
	}
	aop := ir.OXXX
	if n.Op().IsCmp() && t.Kind() != types.TIDEAL && !types.Identical(l.Type(), r.Type()) {
		// comparison is okay as long as one side is
		// assignable to the other.  convert so they have
		// the same type.
		//
		// the only conversion that isn't a no-op is concrete == interface.
		// in that case, check comparability of the concrete type.
		// The conversion allocates, so only do it if the concrete type is huge.
		converted := false
		if r.Type().Kind() != types.TBLANK {
			aop, _ = assignOp(gd, l.Type(), r.Type())
			if aop != ir.OXXX {
				if r.Type().IsInterface() && !l.Type().IsInterface() && !types.IsComparable(l.Type()) {
					gd.Errorf("invalid operation: %v (operator %v not defined on %s)", n, op, typekind(l.Type()))
					return l, r, nil
				}

				types.CalcSize(l.Type())
				if r.Type().IsInterface() == l.Type().IsInterface() || l.Type().Size() >= 1<<16 {
					l = ir.NewConvExpr(gd, gd.Pos, aop, r.Type(), l)
					l.SetTypecheck(1)
				}

				t = r.Type()
				converted = true
			}
		}

		if !converted && l.Type().Kind() != types.TBLANK {
			aop, _ = assignOp(gd, r.Type(), l.Type())
			if aop != ir.OXXX {
				if l.Type().IsInterface() && !r.Type().IsInterface() && !types.IsComparable(r.Type()) {
					gd.Errorf("invalid operation: %v (operator %v not defined on %s)", n, op, typekind(r.Type()))
					return l, r, nil
				}

				types.CalcSize(r.Type())
				if r.Type().IsInterface() == l.Type().IsInterface() || r.Type().Size() >= 1<<16 {
					r = ir.NewConvExpr(gd, gd.Pos, aop, l.Type(), r)
					r.SetTypecheck(1)
				}

				t = l.Type()
			}
		}
	}

	if t.Kind() != types.TIDEAL && !types.Identical(l.Type(), r.Type()) {
		l, r = defaultlit2(gd, l, r, true)
		if l.Type() == nil || r.Type() == nil {
			return l, r, nil
		}
		if l.Type().IsInterface() == r.Type().IsInterface() || aop == 0 {
			gd.Errorf("invalid operation: %v (mismatched types %v and %v)", n, l.Type(), r.Type())
			return l, r, nil
		}
	}

	if t.Kind() == types.TIDEAL {
		t = mixUntyped(l.Type(), r.Type())
	}
	if dt := defaultType(gd, t); !okfor[op][dt.Kind()] {
		gd.Errorf("invalid operation: %v (operator %v not defined on %s)", n, op, typekind(t))
		return l, r, nil
	}

	// okfor allows any array == array, map == map, func == func.
	// restrict to slice/map/func == nil and nil == slice/map/func.
	if l.Type().IsArray() && !types.IsComparable(l.Type()) {
		gd.Errorf("invalid operation: %v (%v cannot be compared)", n, l.Type())
		return l, r, nil
	}

	if l.Type().IsSlice() && !ir.IsNil(l) && !ir.IsNil(r) {
		gd.Errorf("invalid operation: %v (slice can only be compared to nil)", n)
		return l, r, nil
	}

	if l.Type().IsMap() && !ir.IsNil(l) && !ir.IsNil(r) {
		gd.Errorf("invalid operation: %v (map can only be compared to nil)", n)
		return l, r, nil
	}

	if l.Type().Kind() == types.TFUNC && !ir.IsNil(l) && !ir.IsNil(r) {
		gd.Errorf("invalid operation: %v (func can only be compared to nil)", n)
		return l, r, nil
	}

	if l.Type().IsStruct() {
		if f := types.IncomparableField(l.Type()); f != nil {
			gd.Errorf("invalid operation: %v (struct containing %v cannot be compared)", n, f.Type)
			return l, r, nil
		}
	}

	return l, r, t
}

// The result of tcCompLit MUST be assigned back to n, e.g.
//
//	n.Left = tcCompLit(gd, n.Left)
func tcCompLit(gd *base.Invocation, n *ir.CompLitExpr) (res ir.Node) {
	if base.EnableTrace && gd.Flag.LowerT {
		defer tracePrint(gd, "tcCompLit", n)(&res)
	}

	lno := gd.Pos
	defer func() {
		gd.Pos = lno
	}()

	ir.SetPos(gd, n)

	t := n.Type()
	gd.AssertfAt(t != nil, n.Pos(), "missing type in composite literal")

	switch t.Kind() {
	default:
		gd.Errorf("invalid composite literal type %v", t)
		n.SetType(nil)

	case types.TARRAY:
		typecheckarraylit(gd, t.Elem(), t.NumElem(), n.List, "array literal")
		n.SetOp(ir.OARRAYLIT)

	case types.TSLICE:
		length := typecheckarraylit(gd, t.Elem(), -1, n.List, "slice literal")
		n.SetOp(ir.OSLICELIT)
		n.Len = length

	case types.TMAP:
		for i3, l := range n.List {
			ir.SetPos(gd, l)
			if l.Op() != ir.OKEY {
				n.List[i3] = Expr(gd, l)
				gd.Errorf("missing key in map literal")
				continue
			}
			l := l.(*ir.KeyExpr)

			r := l.Key
			r = Expr(gd, r)
			l.Key = AssignConv(gd, r, t.Key(), "map key")

			r = l.Value
			r = Expr(gd, r)
			l.Value = AssignConv(gd, r, t.Elem(), "map value")
		}

		n.SetOp(ir.OMAPLIT)

	case types.TSTRUCT:
		// Need valid field offsets for Xoffset below.
		types.CalcSize(t)

		errored := false
		if len(n.List) != 0 && nokeys(n.List) {
			// simple list of variables
			ls := n.List
			for i, n1 := range ls {
				ir.SetPos(gd, n1)
				n1 = Expr(gd, n1)
				ls[i] = n1
				if i >= t.NumFields() {
					if !errored {
						gd.Errorf("too many values in %v", n)
						errored = true
					}
					continue
				}

				f := t.Field(i)
				s := f.Sym

				// Do the test for assigning to unexported fields.
				// But if this is an instantiated function, then
				// the function has already been typechecked. In
				// that case, don't do the test, since it can fail
				// for the closure structs created in
				// walkClosure(), because the instantiated
				// function is compiled as if in the source
				// package of the generic function.
				if !(ir.CurFunc(gd) != nil && strings.Contains(ir.CurFunc(gd).Nname.Sym().Name, "[")) {
					if s != nil && !types.IsExported(s.Name) && s.Pkg != types.LocalPkg(gd) {
						gd.Errorf("implicit assignment of unexported field '%s' in %v literal", s.Name, t)
					}
				}
				// No pushtype allowed here. Must name fields for that.
				n1 = AssignConv(gd, n1, f.Type, "field value")
				ls[i] = ir.NewStructKeyExpr(gd, gd.Pos, f, n1)
			}
			if len(ls) < t.NumFields() {
				gd.Errorf("too few values in %v", n)
			}
		} else {
			hash := make(map[string]bool)

			// keyed list
			ls := n.List
			for i, n := range ls {
				ir.SetPos(gd, n)

				sk, ok := n.(*ir.StructKeyExpr)
				if !ok {
					kv, ok := n.(*ir.KeyExpr)
					if !ok {
						if !errored {
							gd.Errorf("mixture of field:value and value initializers")
							errored = true
						}
						ls[i] = Expr(gd, n)
						continue
					}

					sk = tcStructLitKey(gd, t, kv)
					if sk == nil {
						continue
					}

					fielddup(gd, sk.Sym().Name, hash)
				}

				// No pushtype allowed here. Tried and rejected.
				sk.Value = Expr(gd, sk.Value)
				sk.Value = AssignConv(gd, sk.Value, sk.Field.Type, "field value")
				ls[i] = sk
			}
		}

		n.SetOp(ir.OSTRUCTLIT)
	}

	return n
}

// tcStructLitKey typechecks an OKEY node that appeared within a
// struct literal.
func tcStructLitKey(gd *base.Invocation, typ *types.Type, kv *ir.KeyExpr) *ir.StructKeyExpr {
	key := kv.Key

	sym := key.Sym()

	// An OXDOT uses the Sym field to hold
	// the field to the right of the dot,
	// so s will be non-nil, but an OXDOT
	// is never a valid struct literal key.
	if sym == nil || sym.Pkg != types.LocalPkg(gd) || key.Op() == ir.OXDOT || sym.IsBlank() {
		gd.Errorf("invalid field name %v in struct initializer", key)
		return nil
	}

	if f := Lookdot1(gd, nil, sym, typ, typ.Fields(), 0); f != nil {
		return ir.NewStructKeyExpr(gd, kv.Pos(), f, kv.Value)
	}

	if ci := Lookdot1(gd, nil, sym, typ, typ.Fields(), 2); ci != nil { // Case-insensitive lookup.
		if visible(gd, ci.Sym) {
			gd.Errorf("unknown field '%v' in struct literal of type %v (but does have %v)", sym, typ, ci.Sym)
		} else if nonexported(sym) && sym.Name == ci.Sym.Name { // Ensure exactness before the suggestion.
			gd.Errorf("cannot refer to unexported field '%v' in struct literal of type %v", sym, typ)
		} else {
			gd.Errorf("unknown field '%v' in struct literal of type %v", sym, typ)
		}
		return nil
	}

	var f *types.Field
	p, _ := dotpath(sym, typ, &f, true)
	if p == nil || f.IsMethod() {
		gd.Errorf("unknown field '%v' in struct literal of type %v", sym, typ)
		return nil
	}

	// dotpath returns the parent embedded types in reverse order.
	var ep []string
	for ei := len(p) - 1; ei >= 0; ei-- {
		ep = append(ep, p[ei].field.Sym.Name)
	}
	ep = append(ep, sym.Name)
	gd.Errorf("cannot use promoted field %v in struct literal of type %v", strings.Join(ep, "."), typ)
	return nil
}

// tcConv typechecks an OCONV node.
func tcConv(gd *base.Invocation, n *ir.ConvExpr) ir.Node {
	types.CheckSize(n.Type()) // ensure width is calculated for backend
	n.X = Expr(gd, n.X)
	n.X = convlit1(gd, n.X, n.Type(), true, nil)
	t := n.X.Type()
	if t == nil || n.Type() == nil {
		n.SetType(nil)
		return n
	}
	op, why := convertOp(gd, n.X.Op() == ir.OLITERAL, t, n.Type())
	if op == ir.OXXX {
		// Due to //go:nointerface, we may be stricter than types2 here (#63333).
		gd.ErrorfAt(n.Pos(), errors.InvalidConversion, "cannot convert %L to type %v%s", n.X, n.Type(), why)
		n.SetType(nil)
		return n
	}

	n.SetOp(op)
	switch n.Op() {
	case ir.OCONVNOP:
		if t.Kind() == n.Type().Kind() {
			switch t.Kind() {
			case types.TFLOAT32, types.TFLOAT64, types.TCOMPLEX64, types.TCOMPLEX128:
				// Floating point casts imply rounding and
				// so the conversion must be kept.
				n.SetOp(ir.OCONV)
			}
		}

	// do not convert to []byte literal. See CL 125796.
	// generated code and compiler memory footprint is better without it.
	case ir.OSTR2BYTES:
		// ok

	case ir.OSTR2RUNES:
		if n.X.Op() == ir.OLITERAL {
			return stringtoruneslit(gd, n)
		}

	case ir.OBYTES2STR:
		if t.Elem() != types.ByteType && t.Elem() != types.Types[types.TUINT8] {
			// If t is a slice of a user-defined byte type B (not uint8
			// or byte), then add an extra CONVNOP from []B to []byte, so
			// that the call to slicebytetostring() added in walk will
			// typecheck correctly.
			n.X = ir.NewConvExpr(gd, n.X.Pos(), ir.OCONVNOP, types.NewSlice(types.ByteType), n.X)
			n.X.SetTypecheck(1)
		}

	case ir.ORUNES2STR:
		if t.Elem() != types.RuneType && t.Elem() != types.Types[types.TINT32] {
			// If t is a slice of a user-defined rune type B (not uint32
			// or rune), then add an extra CONVNOP from []B to []rune, so
			// that the call to slicerunetostring() added in walk will
			// typecheck correctly.
			n.X = ir.NewConvExpr(gd, n.X.Pos(), ir.OCONVNOP, types.NewSlice(types.RuneType), n.X)
			n.X.SetTypecheck(1)
		}

	}
	return n
}

// DotField returns a field selector expression that selects the
// index'th field of the given expression, which must be of struct or
// pointer-to-struct type.
func DotField(gd *base.Invocation, pos src.XPos, x ir.Node, index int) *ir.SelectorExpr {
	op, typ := ir.ODOT, x.Type()
	if typ.IsPtr() {
		op, typ = ir.ODOTPTR, typ.Elem()
	}
	if !typ.IsStruct() {
		gd.FatalfAt(pos, "DotField of non-struct: %L", x)
	}

	// TODO(mdempsky): This is the backend's responsibility.
	types.CalcSize(typ)

	field := typ.Field(index)
	return dot(gd, pos, field.Type, op, x, field)
}

func dot(gd *base.Invocation, pos src.XPos, typ *types.Type, op ir.Op, x ir.Node, selection *types.Field) *ir.SelectorExpr {
	n := ir.NewSelectorExpr(gd, pos, op, x, selection.Sym)
	n.Selection = selection
	n.SetType(typ)
	n.SetTypecheck(1)
	return n
}

// XDotField returns an expression representing the field selection
// x.sym. If any implicit field selection are necessary, those are
// inserted too.
func XDotField(gd *base.Invocation, pos src.XPos, x ir.Node, sym *types.Sym) *ir.SelectorExpr {
	n := Expr(gd, ir.NewSelectorExpr(gd, pos, ir.OXDOT, x, sym)).(*ir.SelectorExpr)
	if n.Op() != ir.ODOT && n.Op() != ir.ODOTPTR {
		gd.FatalfAt(pos, "unexpected result op: %v (%v)", n.Op(), n)
	}
	return n
}

// XDotMethod returns an expression representing the method value
// x.sym (i.e., x is a value, not a type). If any implicit field
// selection are necessary, those are inserted too.
//
// If callee is true, the result is an ODOTMETH/ODOTINTER, otherwise
// an OMETHVALUE.
func XDotMethod(gd *base.Invocation, pos src.XPos, x ir.Node, sym *types.Sym, callee bool) *ir.SelectorExpr {
	n := ir.NewSelectorExpr(gd, pos, ir.OXDOT, x, sym)
	if callee {
		n = Callee(gd, n).(*ir.SelectorExpr)
		if n.Op() != ir.ODOTMETH && n.Op() != ir.ODOTINTER {
			gd.FatalfAt(pos, "unexpected result op: %v (%v)", n.Op(), n)
		}
	} else {
		n = Expr(gd, n).(*ir.SelectorExpr)
		if n.Op() != ir.OMETHVALUE {
			gd.FatalfAt(pos, "unexpected result op: %v (%v)", n.Op(), n)
		}
	}
	return n
}

// tcDot typechecks an OXDOT or ODOT node.
func tcDot(gd *base.Invocation, n *ir.SelectorExpr, top int) ir.Node {
	if n.Op() == ir.OXDOT {
		n = AddImplicitDots(gd, n)
		n.SetOp(ir.ODOT)
		if n.X == nil {
			n.SetType(nil)
			return n
		}
	}

	n.X = Expr(gd, n.X)
	n.X = DefaultLit(gd, n.X, nil)

	t := n.X.Type()
	if t == nil {
		gd.UpdateErrorDot(ir.Line(gd, n), fmt.Sprint(n.X), fmt.Sprint(n))
		n.SetType(nil)
		return n
	}

	if n.X.Op() == ir.OTYPE {
		gd.FatalfAt(n.Pos(), "use NewMethodExpr to construct OMETHEXPR")
	}

	if t.IsPtr() && !t.Elem().IsInterface() {
		t = t.Elem()
		if t == nil {
			n.SetType(nil)
			return n
		}
		n.SetOp(ir.ODOTPTR)
		types.CheckSize(t)
	}

	if n.Sel.IsBlank() {
		gd.Errorf("cannot refer to blank field or method")
		n.SetType(nil)
		return n
	}

	if Lookdot(gd, n, t, 0) == nil {
		// Legitimate field or method lookup failed, try to explain the error
		switch {
		case t.IsEmptyInterface():
			gd.Errorf("%v undefined (type %v is interface with no methods)", n, n.X.Type())

		case t.IsPtr() && t.Elem().IsInterface():
			// Pointer to interface is almost always a mistake.
			gd.Errorf("%v undefined (type %v is pointer to interface, not interface)", n, n.X.Type())

		case Lookdot(gd, n, t, 1) != nil:
			// Field or method matches by name, but it is not exported.
			gd.Errorf("%v undefined (cannot refer to unexported field or method %v)", n, n.Sel)

		default:
			if mt := Lookdot(gd, n, t, 2); mt != nil && visible(gd, mt.Sym) { // Case-insensitive lookup.
				gd.Errorf("%v undefined (type %v has no field or method %v, but does have %v)", n, n.X.Type(), n.Sel, mt.Sym)
			} else {
				gd.Errorf("%v undefined (type %v has no field or method %v)", n, n.X.Type(), n.Sel)
			}
		}
		n.SetType(nil)
		return n
	}

	if (n.Op() == ir.ODOTINTER || n.Op() == ir.ODOTMETH) && top&ctxCallee == 0 {
		n.SetOp(ir.OMETHVALUE)
		n.SetType(NewMethodType(gd, n.Type(), nil))
	}
	return n
}

// tcDotType typechecks an ODOTTYPE node.
func tcDotType(gd *base.Invocation, n *ir.TypeAssertExpr) ir.Node {
	n.X = Expr(gd, n.X)
	n.X = DefaultLit(gd, n.X, nil)
	l := n.X
	t := l.Type()
	if t == nil {
		n.SetType(nil)
		return n
	}
	if !t.IsInterface() {
		gd.Errorf("invalid type assertion: %v (non-interface type %v on left)", n, t)
		n.SetType(nil)
		return n
	}

	gd.AssertfAt(n.Type() != nil, n.Pos(), "missing type: %v", n)

	if n.Type() != nil && !n.Type().IsInterface() {
		why := ImplementsExplain(gd, n.Type(), t)
		if why != "" {
			gd.Fatalf("impossible type assertion:\n\t%s", why)
			n.SetType(nil)
			return n
		}
	}
	return n
}

// tcITab typechecks an OITAB node.
func tcITab(gd *base.Invocation, n *ir.UnaryExpr) ir.Node {
	n.X = Expr(gd, n.X)
	t := n.X.Type()
	if t == nil {
		n.SetType(nil)
		return n
	}
	if !t.IsInterface() {
		gd.Fatalf("OITAB of %v", t)
	}
	n.SetType(types.NewPtr(types.Types[types.TUINTPTR]))
	return n
}

// tcIndex typechecks an OINDEX node.
func tcIndex(gd *base.Invocation, n *ir.IndexExpr) ir.Node {
	n.X = Expr(gd, n.X)
	n.X = DefaultLit(gd, n.X, nil)
	n.X = implicitstar(gd, n.X)
	l := n.X
	n.Index = Expr(gd, n.Index)
	r := n.Index
	t := l.Type()
	if t == nil || r.Type() == nil {
		n.SetType(nil)
		return n
	}
	switch t.Kind() {
	default:
		gd.Errorf("invalid operation: %v (type %v does not support indexing)", n, t)
		n.SetType(nil)
		return n

	case types.TSTRING, types.TARRAY, types.TSLICE:
		n.Index = indexlit(gd, n.Index)
		if t.IsString() {
			n.SetType(types.ByteType)
		} else {
			n.SetType(t.Elem())
		}
		why := "string"
		if t.IsArray() {
			why = "array"
		} else if t.IsSlice() {
			why = "slice"
		}

		if n.Index.Type() != nil && !n.Index.Type().IsInteger() {
			gd.Errorf("non-integer %s index %v", why, n.Index)
			return n
		}

	case types.TMAP:
		n.Index = AssignConv(gd, n.Index, t.Key(), "map index")
		n.SetType(t.Elem())
		n.SetOp(ir.OINDEXMAP)
		n.Assigned = false
	}
	return n
}

// tcLenCap typechecks an OLEN or OCAP node.
func tcLenCap(gd *base.Invocation, n *ir.UnaryExpr) ir.Node {
	n.X = Expr(gd, n.X)
	n.X = DefaultLit(gd, n.X, nil)
	l := n.X
	t := l.Type()
	if t == nil {
		n.SetType(nil)
		return n
	}
	var ok bool
	if t.IsPtr() && t.Elem().IsArray() {
		ok = true
	} else if n.Op() == ir.OLEN {
		ok = okforlen[t.Kind()]
	} else {
		ok = okforcap[t.Kind()]
	}
	if !ok {
		gd.Errorf("invalid argument %L for %v", l, n.Op())
		n.SetType(nil)
		return n
	}

	n.SetType(types.Types[types.TINT])
	return n
}

// tcUnsafeData typechecks an OUNSAFESLICEDATA or OUNSAFESTRINGDATA node.
func tcUnsafeData(gd *base.Invocation, n *ir.UnaryExpr) ir.Node {
	n.X = Expr(gd, n.X)
	n.X = DefaultLit(gd, n.X, nil)
	l := n.X
	t := l.Type()
	if t == nil {
		n.SetType(nil)
		return n
	}

	var kind types.Kind
	if n.Op() == ir.OUNSAFESLICEDATA {
		kind = types.TSLICE
	} else {
		/* kind is string */
		kind = types.TSTRING
	}

	if t.Kind() != kind {
		gd.Errorf("invalid argument %L for %v", l, n.Op())
		n.SetType(nil)
		return n
	}

	if kind == types.TSTRING {
		t = types.ByteType
	} else {
		t = t.Elem()
	}
	n.SetType(types.NewPtr(t))
	return n
}

// tcRecv typechecks an ORECV node.
func tcRecv(gd *base.Invocation, n *ir.UnaryExpr) ir.Node {
	n.X = Expr(gd, n.X)
	n.X = DefaultLit(gd, n.X, nil)
	l := n.X
	t := l.Type()
	if t == nil {
		n.SetType(nil)
		return n
	}
	if !t.IsChan() {
		gd.Errorf("invalid operation: %v (receive from non-chan type %v)", n, t)
		n.SetType(nil)
		return n
	}

	if !t.ChanDir().CanRecv() {
		gd.Errorf("invalid operation: %v (receive from send-only type %v)", n, t)
		n.SetType(nil)
		return n
	}

	n.SetType(t.Elem())
	return n
}

// tcSPtr typechecks an OSPTR node.
func tcSPtr(gd *base.Invocation, n *ir.UnaryExpr) ir.Node {
	n.X = Expr(gd, n.X)
	t := n.X.Type()
	if t == nil {
		n.SetType(nil)
		return n
	}
	if !t.IsSlice() && !t.IsString() {
		gd.Fatalf("OSPTR of %v", t)
	}
	if t.IsString() {
		n.SetType(types.NewPtr(types.Types[types.TUINT8]))
	} else {
		n.SetType(types.NewPtr(t.Elem()))
	}
	return n
}

// tcSlice typechecks an OSLICE or OSLICE3 node.
func tcSlice(gd *base.Invocation, n *ir.SliceExpr) ir.Node {
	n.X = DefaultLit(gd, Expr(gd, n.X), nil)
	n.Low = indexlit(gd, Expr(gd, n.Low))
	n.High = indexlit(gd, Expr(gd, n.High))
	n.Max = indexlit(gd, Expr(gd, n.Max))
	hasmax := n.Op().IsSlice3(gd)
	l := n.X
	if l.Type() == nil {
		n.SetType(nil)
		return n
	}
	if l.Type().IsArray() {
		if !ir.IsAddressable(n.X) {
			gd.Errorf("invalid operation %v (slice of unaddressable value)", n)
			n.SetType(nil)
			return n
		}

		addr := NodAddr(gd, n.X)
		addr.SetImplicit(true)
		n.X = Expr(gd, addr)
		l = n.X
	}
	t := l.Type()
	var tp *types.Type
	if t.IsString() {
		if hasmax {
			gd.Errorf("invalid operation %v (3-index slice of string)", n)
			n.SetType(nil)
			return n
		}
		n.SetType(t)
		n.SetOp(ir.OSLICESTR)
	} else if t.IsPtr() && t.Elem().IsArray() {
		tp = t.Elem()
		n.SetType(types.NewSlice(tp.Elem()))
		types.CalcSize(n.Type())
		if hasmax {
			n.SetOp(ir.OSLICE3ARR)
		} else {
			n.SetOp(ir.OSLICEARR)
		}
	} else if t.IsSlice() {
		n.SetType(t)
	} else {
		gd.Errorf("cannot slice %v (type %v)", l, t)
		n.SetType(nil)
		return n
	}

	if n.Low != nil && !checksliceindex(gd, n.Low) {
		n.SetType(nil)
		return n
	}
	if n.High != nil && !checksliceindex(gd, n.High) {
		n.SetType(nil)
		return n
	}
	if n.Max != nil && !checksliceindex(gd, n.Max) {
		n.SetType(nil)
		return n
	}
	return n
}

// tcSliceHeader typechecks an OSLICEHEADER node.
func tcSliceHeader(gd *base.Invocation, n *ir.SliceHeaderExpr) ir.Node {
	// Errors here are Fatalf instead of Errorf because only the compiler
	// can construct an OSLICEHEADER node.
	// Components used in OSLICEHEADER that are supplied by parsed source code
	// have already been typechecked in e.g. OMAKESLICE earlier.
	t := n.Type()
	if t == nil {
		gd.Fatalf("no type specified for OSLICEHEADER")
	}

	if !t.IsSlice() {
		gd.Fatalf("invalid type %v for OSLICEHEADER", n.Type())
	}

	if n.Ptr == nil || n.Ptr.Type() == nil || !n.Ptr.Type().IsUnsafePtr() {
		gd.Fatalf("need unsafe.Pointer for OSLICEHEADER")
	}

	n.Ptr = Expr(gd, n.Ptr)
	n.Len = DefaultLit(gd, Expr(gd, n.Len), types.Types[types.TINT])
	n.Cap = DefaultLit(gd, Expr(gd, n.Cap), types.Types[types.TINT])

	return n
}

// tcStringHeader typechecks an OSTRINGHEADER node.
func tcStringHeader(gd *base.Invocation, n *ir.StringHeaderExpr) ir.Node {
	t := n.Type()
	if t == nil {
		gd.Fatalf("no type specified for OSTRINGHEADER")
	}

	if !t.IsString() {
		gd.Fatalf("invalid type %v for OSTRINGHEADER", n.Type())
	}

	if n.Ptr == nil || n.Ptr.Type() == nil || !n.Ptr.Type().IsUnsafePtr() {
		gd.Fatalf("need unsafe.Pointer for OSTRINGHEADER")
	}

	n.Ptr = Expr(gd, n.Ptr)
	n.Len = DefaultLit(gd, Expr(gd, n.Len), types.Types[types.TINT])

	if ir.IsConst(n.Len, constant.Int) && ir.Int64Val(n.Len) < 0 {
		gd.Fatalf("len for OSTRINGHEADER must be non-negative")
	}

	return n
}

// tcStar typechecks an ODEREF node, which may be an expression or a type.
func tcStar(gd *base.Invocation, n *ir.StarExpr, top int) ir.Node {
	n.X = typecheck(gd, n.X, ctxExpr|ctxType)
	l := n.X
	t := l.Type()
	if t == nil {
		n.SetType(nil)
		return n
	}

	// TODO(mdempsky): Remove (along with ctxType above) once I'm
	// confident this code path isn't needed any more.
	if l.Op() == ir.OTYPE {
		gd.Fatalf("unexpected type in deref expression: %v", l)
	}

	if !t.IsPtr() {
		if top&(ctxExpr|ctxStmt) != 0 {
			gd.Errorf("invalid indirect of %L", n.X)
			n.SetType(nil)
			return n
		}
		gd.Errorf("%v is not a type", l)
		return n
	}

	n.SetType(t.Elem())
	return n
}

// tcUnaryArith typechecks a unary arithmetic expression.
func tcUnaryArith(gd *base.Invocation, n *ir.UnaryExpr) ir.Node {
	n.X = Expr(gd, n.X)
	l := n.X
	t := l.Type()
	if t == nil {
		n.SetType(nil)
		return n
	}
	if !okfor[n.Op()][defaultType(gd, t).Kind()] {
		gd.Errorf("invalid operation: %v (operator %v not defined on %s)", n, n.Op(), typekind(t))
		n.SetType(nil)
		return n
	}

	n.SetType(t)
	return n
}
