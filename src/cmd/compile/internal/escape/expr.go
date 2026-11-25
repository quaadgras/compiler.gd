// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package escape

import (
	"slices"

	"cmd/compile/internal/ir"
	"cmd/compile/internal/types"
)

// expr models evaluating an expression n and flowing the result into
// hole k.
func (e *escape) expr(k hole, n ir.Node) {
	if n == nil {
		return
	}
	e.stmts(n.Init())
	e.exprSkipInit(k, n)
}

func (e *escape) exprSkipInit(k hole, n ir.Node) {
	if n == nil {
		return
	}

	lno := ir.SetPos(e.gd, n)
	defer func() {
		e.gd.Pos = lno
	}()

	if k.derefs >= 0 && !n.Type().IsUntyped() && !n.Type().HasPointers() {
		k.dst = &e.blankLoc
	}
	if k.derefs >= 0 && n.Type().IsString() && e.readOnlyOn() {
		// (Not the address of a string variable, which is written.)
		// Only a string comes of this expression: what it points
		// to is never written. (Where a string is made of memory
		// that was not allocated for one, that memory is shared
		// from then on: aliasHole.)
		k.ro = true
	}

	switch n.Op() {
	default:
		e.gd.Fatalf("unexpected expr: %s %v", n.Op().String(), n)

	case ir.OLITERAL, ir.ONIL, ir.OGETG, ir.OGETCALLERSP, ir.OTYPE, ir.OMETHEXPR, ir.OLINKSYMOFFSET:
		// nop

	case ir.ONAME:
		n := n.(*ir.Name)
		if n.Class == ir.PFUNC || n.Class == ir.PEXTERN {
			if n.Class == ir.PEXTERN {
				e.unknown(k, n)
			}
			return
		}
		e.flow(k, e.oldLoc(n))

	case ir.OPLUS, ir.ONEG, ir.OBITNOT, ir.ONOT:
		n := n.(*ir.UnaryExpr)
		e.discard(n.X)
	case ir.OADD, ir.OSUB, ir.OOR, ir.OXOR, ir.OMUL, ir.ODIV, ir.OMOD, ir.OLSH, ir.ORSH, ir.OAND, ir.OANDNOT, ir.OEQ, ir.ONE, ir.OLT, ir.OLE, ir.OGT, ir.OGE:
		n := n.(*ir.BinaryExpr)
		// (An address that is only compared, or that another is
		// subtracted from, is not kept as an integer: below.)
		unkept := ir.UnkeptAddrs(n)
		for _, x := range []ir.Node{n.X, n.Y} {
			if c := ir.AddrConv(x); c != nil && slices.Contains(unkept, c) {
				for ; x != c; x = x.(*ir.ConvExpr).X {
					e.stmts(x.Init())
				}
				e.stmts(c.Init())
				e.discard(c.X)
				continue
			}
			e.discard(x)
		}
	case ir.OANDAND, ir.OOROR:
		n := n.(*ir.LogicalExpr)
		e.discard(n.X)
		e.discard(n.Y)
	case ir.OADDR:
		n := n.(*ir.AddrExpr)
		e.expr(k.addr(e.gd, n, "address-of"), n.X) // "address-of"
	case ir.ODEREF:
		n := n.(*ir.StarExpr)
		if punnedString(n) && !e.typedByConstruction() {
			// What the pointer points to holds the pointer to
			// the bytes.
			k = e.aliasHole(k)
		}
		e.expr(k.deref(e.gd, n, "indirection"), n.X) // "indirection"
	case ir.ODOT, ir.ODOTMETH, ir.ODOTINTER:
		n := n.(*ir.SelectorExpr)
		e.expr(k.note(e.gd, n, "dot"), n.X)
	case ir.ODOTPTR:
		n := n.(*ir.SelectorExpr)
		e.expr(k.deref(e.gd, n, "dot of pointer"), n.X) // "dot of pointer"
	case ir.ODOTTYPE, ir.ODOTTYPE2:
		n := n.(*ir.TypeAssertExpr)
		e.expr(e.assertHole(k, n.Type(), n.X.Type()).dotType(e.gd, n.Type(), n, "dot"), n.X)
	case ir.ODYNAMICDOTTYPE, ir.ODYNAMICDOTTYPE2:
		n := n.(*ir.DynamicTypeAssertExpr)
		e.expr(e.assertHole(k, n.Type(), n.X.Type()).dotType(e.gd, n.Type(), n, "dot"), n.X)
		// n.T doesn't need to be tracked; it always points to read-only storage.
	case ir.OINDEX:
		n := n.(*ir.IndexExpr)
		if n.X.Type().IsArray() {
			e.expr(k.note(e.gd, n, "fixed-array-index-of"), n.X)
		} else {
			// TODO(mdempsky): Fix why reason text.
			e.expr(k.deref(e.gd, n, "dot of pointer"), n.X)
		}
		e.discard(n.Index)
	case ir.OINDEXMAP:
		n := n.(*ir.IndexExpr)
		if e.mapsOn() {
			// What is loaded from a map is reached from it.
			e.expr(k.deref(e.gd, n, "map index"), n.X)
		} else {
			e.discard(n.X)
		}
		// Keys used in map lookups do not need to escape.
		// See "Hashing Pointers" doc in internal/runtime/maps/map.go.
		e.discard(n.Index)
	case ir.OSLICE, ir.OSLICEARR, ir.OSLICE3, ir.OSLICE3ARR, ir.OSLICESTR:
		n := n.(*ir.SliceExpr)
		e.expr(k.note(e.gd, n, "slice"), n.X)
		e.discard(n.Low)
		e.discard(n.High)
		e.discard(n.Max)

	case ir.OCONV, ir.OCONVNOP:
		n := n.(*ir.ConvExpr)
		if (ir.ShouldCheckPtr(e.gd, e.curfn, 2) || ir.ShouldAsanCheckPtr(e.gd, e.curfn)) && n.Type().IsUnsafePtr() && n.X.Type().IsPtr() {
			// When -d=checkptr=2 or -asan is enabled,
			// treat conversions to unsafe.Pointer as an
			// escaping operation. This allows better
			// runtime instrumentation, since we can more
			// easily detect object boundaries on the heap
			// than the stack.
			e.assignHeap(n.X, "conversion to unsafe.Pointer", n)
		} else if n.Type().IsUnsafePtr() && n.X.Type().IsUintptr() {
			e.unsafeValue(k, n.X)
		} else if n.Type().IsUintptr() && n.X.Type().IsUnsafePtr() {
			// An address that is kept as an integer is a pointer
			// again wherever the integer goes (runtime.exposeptr).
			e.expr(e.shareHole(), n.X)
		} else {
			e.expr(k, n.X)
		}
	case ir.OCONVIFACE:
		n := n.(*ir.ConvExpr)
		if !n.X.Type().IsInterface() && !types.IsDirectIface(n.X.Type()) {
			k = e.spill(k, n)
		}
		if !n.X.Type().IsInterface() {
			k = e.convHole(k, n.X.Type(), n.Type())
		}
		e.expr(k.note(e.gd, n, "interface-converted"), n.X)
	case ir.OMAKEFACE:
		n := n.(*ir.BinaryExpr)
		// Note: n.X is not needed because it can never point to memory that might escape.
		e.expr(k, n.Y)
	case ir.OITAB, ir.OIDATA, ir.OSPTR:
		n := n.(*ir.UnaryExpr)
		e.expr(k, n.X)
	case ir.OSLICE2ARR:
		// Converting a slice to array is effectively a deref.
		n := n.(*ir.ConvExpr)
		e.expr(k.deref(e.gd, n, "slice-to-array"), n.X)
	case ir.OSLICE2ARRPTR:
		// the slice pointer flows directly to the result
		n := n.(*ir.ConvExpr)
		e.expr(k, n.X)
	case ir.ORECV:
		n := n.(*ir.UnaryExpr)
		e.discard(n.X)
		e.unknown(k, n)

	case ir.OUNSAFESTRINGDATA:
		// gd: under the SSO fork unsafe.StringData may heap-allocate a
		// 1..15 B buffer when its input is an inline-rep string and
		// the returned *byte escapes the caller's frame. Spill here
		// so the escape solver records an EscHeap/EscNone bit on n
		// that walk can read to pick between runtime.stringDataHeap
		// and a cheap stack-spill via OSPTR. The result is that
		// buffer, which holds bytes and no pointers, or the string's
		// data pointer, which flows to the result as in stock (to
		// the result, not through the buffer to the heap).
		n := n.(*ir.UnaryExpr)
		e.spill(k, n)
		e.expr(k, n.X)

	case ir.OCALLMETH, ir.OCALLFUNC, ir.OCALLINTER, ir.OINLCALL,
		ir.OLEN, ir.OCAP, ir.OMIN, ir.OMAX, ir.OCOMPLEX, ir.OREAL, ir.OIMAG, ir.OAPPEND, ir.OCOPY, ir.ORECOVER,
		ir.OUNSAFEADD, ir.OUNSAFESLICE, ir.OUNSAFESTRING, ir.OUNSAFESLICEDATA:
		e.call([]hole{k}, n)

	case ir.ONEW:
		n := n.(*ir.UnaryExpr)
		e.spill(k, n)

	case ir.OMAKESLICE:
		n := n.(*ir.MakeExpr)
		e.spill(k, n)
		e.discard(n.Len)
		e.discard(n.Cap)
	case ir.OMAKECHAN:
		n := n.(*ir.MakeExpr)
		e.discard(n.Len)
	case ir.OMAKEMAP:
		n := n.(*ir.MakeExpr)
		e.spill(k, n)
		e.discard(n.Len)

	case ir.OMETHVALUE:
		// Flow the receiver argument to both the closure and
		// to the receiver parameter.

		n := n.(*ir.SelectorExpr)
		closureK := e.spill(k, n)

		m := n.Selection

		// We don't know how the method value will be called
		// later, so conservatively assume the result
		// parameters all flow to the heap.
		//
		// TODO(mdempsky): Change ks into a callback, so that
		// we don't have to create this slice?
		//
		// Which goroutines reach what it returns, the call says:
		// what a function value's call returns may be what the
		// value holds, or what that leads to (call.go). The heap
		// here said that all which the receiver holds is shared,
		// as soon as a method that returns some of it was a value.
		rk := e.heapHole()
		if e.shareTagsOn() && e.gd.Debug.NoLocalMethVal == 0 {
			rk = e.ownHeapHole()
		}
		var ks []hole
		for i := m.Type.NumResults(); i > 0; i-- {
			ks = append(ks, rk)
		}
		name, _ := m.Nname.(*ir.Name)
		// What the method says it stores through its receiver, or
		// in what that reaches, is of the receiver that the value
		// binds: the method's first argument whenever it is called.
		// (Of its other parameters nothing is known here, and those
		// go to the heap.)
		saved, savedGroups, savedMix := e.callArgs, e.argGroups, e.mixGroup
		if e.shareTagsOn() && e.gd.Debug.NoLocalMethVal == 0 {
			e.callArgs, e.argGroups, e.mixGroup = []ir.Node{n.X}, nil, nil
		}
		paramK := e.tagHole(ks, name, m.Type.Recv())
		e.callArgs, e.argGroups, e.mixGroup = saved, savedGroups, savedMix

		e.expr(e.teeHole(paramK, closureK), n.X)

	case ir.OPTRLIT:
		n := n.(*ir.AddrExpr)
		e.expr(e.spill(k, n), n.X)

	case ir.OARRAYLIT:
		n := n.(*ir.CompLitExpr)
		for _, elt := range n.List {
			if elt.Op() == ir.OKEY {
				elt = elt.(*ir.KeyExpr).Value
			}
			e.expr(k.note(e.gd, n, "array literal element"), elt)
		}

	case ir.OSLICELIT:
		n := n.(*ir.CompLitExpr)
		k = e.spill(k, n)

		for _, elt := range n.List {
			if elt.Op() == ir.OKEY {
				elt = elt.(*ir.KeyExpr).Value
			}
			e.expr(k.note(e.gd, n, "slice-literal-element"), elt)
		}

	case ir.OSTRUCTLIT:
		n := n.(*ir.CompLitExpr)
		for _, elt := range n.List {
			e.expr(k.note(e.gd, n, "struct literal element"), elt.(*ir.StructKeyExpr).Value)
		}

	case ir.OMAPLIT:
		n := n.(*ir.CompLitExpr)
		mk := e.spill(k, n)

		// Map keys and values are always stored in the heap.
		for _, elt := range n.List {
			elt := elt.(*ir.KeyExpr)
			if e.mapsOn() {
				// In the heap that is where the map is: what
				// the map holds, as the elements of a slice.
				hk := e.teeHole(mk.note(e.gd, n, "map literal element"), e.ownHeapHole())
				e.expr(hk, elt.Key)
				e.expr(hk, elt.Value)
				continue
			}
			e.assignHeap(elt.Key, "map literal key", n)
			e.assignHeap(elt.Value, "map literal value", n)
		}

	case ir.OCLOSURE:
		n := n.(*ir.ClosureExpr)
		k = e.spill(k, n)
		e.closures = append(e.closures, closure{k, n})

		if fn := n.Func; fn.IsClosure() {
			for _, cv := range fn.ClosureVars {
				if loc := e.oldLoc(cv); !loc.captured {
					loc.captured = true

					// Ignore reassignments to the variable in straightline code
					// preceding the first capture by a closure.
					if loc.loopDepth == e.loopDepth {
						loc.reassigned = false
					}
				}
			}

			for _, n := range fn.Dcl {
				// Add locations for local variables of the
				// closure, if needed, in case we're not including
				// the closure func in the batch for escape
				// analysis (happens for escape analysis called
				// from reflectdata.methodWrapper)
				if n.Op() == ir.ONAME && n.Opt == nil {
					e.with(fn).newLoc(n, true)
				}
			}
			e.walkFunc(fn)
		}

	case ir.ORUNES2STR, ir.OBYTES2STR, ir.OSTR2RUNES, ir.OSTR2BYTES, ir.ORUNESTR:
		n := n.(*ir.ConvExpr)
		e.spill(k, n)
		e.discard(n.X)

	case ir.OADDSTR:
		n := n.(*ir.AddStringExpr)
		e.spill(k, n)

		// Arguments of OADDSTR never escape;
		// runtime.concatstrings makes sure of that.
		e.discards(n.List)

	case ir.ODYNAMICTYPE:
		// Nothing to do - argument is a *runtime._type (+ maybe a *runtime.itab) pointing to static data section
	}
}

// unsafeValue evaluates a uintptr-typed arithmetic expression looking
// for conversions from an unsafe.Pointer.
func (e *escape) unsafeValue(k hole, n ir.Node) {
	if n.Type().Kind() != types.TUINTPTR {
		e.gd.Fatalf("unexpected type %v for %v", n.Type(), n)
	}
	if k.addrtaken {
		e.gd.Fatalf("unexpected addrtaken")
	}

	e.stmts(n.Init())

	switch n.Op() {
	case ir.OCONV, ir.OCONVNOP:
		n := n.(*ir.ConvExpr)
		if n.X.Type().IsUnsafePtr() {
			e.expr(k, n.X)
		} else {
			e.discard(n.X)
		}
	case ir.ODOTPTR:
		n := n.(*ir.SelectorExpr)
		if ir.IsReflectHeaderDataField(n) {
			e.expr(k.deref(e.gd, n, "reflect.Header.Data"), n.X)
		} else {
			e.discard(n.X)
		}
	case ir.OPLUS, ir.ONEG, ir.OBITNOT:
		n := n.(*ir.UnaryExpr)
		e.unsafeValue(k, n.X)
	case ir.OADD, ir.OSUB, ir.OOR, ir.OXOR, ir.OMUL, ir.ODIV, ir.OMOD, ir.OAND, ir.OANDNOT:
		n := n.(*ir.BinaryExpr)
		e.unsafeValue(k, n.X)
		e.unsafeValue(k, n.Y)
	case ir.OLSH, ir.ORSH:
		n := n.(*ir.BinaryExpr)
		e.unsafeValue(k, n.X)
		// RHS need not be uintptr-typed (#32959) and can't meaningfully
		// flow pointers anyway.
		e.discard(n.Y)
	default:
		e.exprSkipInit(e.discardHole(), n)
	}
}

// discard evaluates an expression n for side-effects, but discards
// its value.
func (e *escape) discard(n ir.Node) {
	e.expr(e.discardHole(), n)
}

func (e *escape) discards(l ir.Nodes) {
	for _, n := range l {
		e.discard(n)
	}
}

// spill allocates a new location associated with expression n, flows
// its address to k, and returns a hole that flows values to it. It's
// intended for use with most expressions that allocate storage.
func (e *escape) spill(k hole, n ir.Node) hole {
	loc := e.newLoc(n, false)
	e.flow(k.addr(e.gd, n, "spill"), loc)
	return loc.asHole()
}

// aliasHole returns the hole for the memory that a string is made of
// without a copy (unsafe.String, a load through a pointer converted from
// another type), where the string goes to k: the memory is that of
// something that may be written, a buffer, and the flows of strings are
// not followed for who reaches what they point to (edge.ro). So it counts
// as shared from here on.
func (e *escape) aliasHole(k hole) hole {
	if !e.readOnlyOn() || k.dst == &e.blankLoc || k.derefs < 0 {
		return k
	}
	return e.teeHole(k, e.shareHole())
}

// typedByConstruction reports whether the current function is reflect's:
// its pointers are to values of the types it says they are (ir.UncheckedPkg),
// so a string that it loads through a converted pointer was allocated
// as a string.
func (e *escape) typedByConstruction() bool {
	if e.curfn == nil || e.curfn.Sym() == nil || e.curfn.Sym().Pkg == nil {
		return false
	}
	switch e.curfn.Sym().Pkg.Path {
	case "reflect", "internal/reflectlite":
		return true
	}
	return false
}

// punnedString reports whether n loads a string through a pointer that
// was converted from another type.
func punnedString(n *ir.StarExpr) bool {
	if n.Type() == nil || !n.Type().IsString() {
		return false
	}
	x := n.X
	for x.Op() == ir.OPAREN {
		x = x.(*ir.ParenExpr).X
	}
	c, ok := x.(*ir.ConvExpr)
	return ok && c.Op() == ir.OCONVNOP && c.X.Type() != nil && c.X.Type().IsUnsafePtr()
}
