// Copyright 2009 The Go Authors. All rights reserved.walk/bui
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package walk

import (
	"fmt"
	"go/constant"
	"go/token"
	"internal/abi"
	"strings"

	"cmd/compile/internal/base"
	"cmd/compile/internal/escape"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/reflectdata"
	"cmd/compile/internal/staticdata"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
)

// Rewrite append(src, x, y, z) so that any side effects in
// x, y, z (including runtime panics) are evaluated in
// initialization statements before the append.
// For normal code generation, stop there and leave the
// rest to ssagen.
//
// For race detector, expand append(src, a [, b]* ) to
//
//	init {
//	  s := src
//	  const argc = len(args) - 1
//	  newLen := s.len + argc
//	  if uint(newLen) <= uint(s.cap) {
//	    s = s[:newLen]
//	  } else {
//	    s = growslice(s.ptr, newLen, s.cap, argc, elemType)
//	  }
//	  s[s.len - argc] = a
//	  s[s.len - argc + 1] = b
//	  ...
//	}
//	s
func walkAppend(gd *base.Invocation, n *ir.CallExpr, init *ir.Nodes, dst ir.Node) ir.Node {
	if !ir.SameSafeExpr(dst, n.Args[0]) {
		n.Args[0] = safeExpr(gd, n.Args[0], init)
		n.Args[0] = walkExpr(gd, n.Args[0], init)
	}
	walkExprListSafe(gd, n.Args[1:], init)

	nsrc := n.Args[0]

	// walkExprListSafe will leave OINDEX (s[n]) alone if both s
	// and n are name or literal, but those may index the slice we're
	// modifying here. Fix explicitly.
	// Using cheapExpr also makes sure that the evaluation
	// of all arguments (and especially any panics) happen
	// before we begin to modify the slice in a visible way.
	ls := n.Args[1:]
	for i, n := range ls {
		n = cheapExpr(gd, n, init)
		if !types.Identical(n.Type(), nsrc.Type().Elem()) {
			n = typecheck.AssignConv(gd, n, nsrc.Type().Elem(), "append")
			n = walkExpr(gd, n, init)
		}
		ls[i] = n
	}

	argc := len(n.Args) - 1
	if argc < 1 {
		return nsrc
	}

	// General case, with no function calls left as arguments.
	// Leave for ssagen, except that instrumentation requires the old form.
	if !gd.Flag.Cfg.Instrumenting || gd.Flag.CompilingRuntime {
		return n
	}

	var l []ir.Node

	// s = slice to append to
	s := typecheck.TempAt(gd, gd.Pos, ir.CurFunc(gd), nsrc.Type())
	l = append(l, ir.NewAssignStmt(gd, gd.Pos, s, nsrc))

	// num = number of things to append
	num := ir.NewInt(gd, gd.Pos, int64(argc))

	// newLen := s.len + num
	newLen := typecheck.TempAt(gd, gd.Pos, ir.CurFunc(gd), types.Types[types.TINT])
	l = append(l, ir.NewAssignStmt(gd, gd.Pos, newLen, ir.NewBinaryExpr(gd, gd.Pos, ir.OADD, ir.NewUnaryExpr(gd, gd.Pos, ir.OLEN, s), num)))

	// if uint(newLen) <= uint(s.cap)
	nif := ir.NewIfStmt(gd, gd.Pos, nil, nil, nil)
	nif.Cond = ir.NewBinaryExpr(gd, gd.Pos, ir.OLE, typecheck.Conv(gd, newLen, types.Types[types.TUINT]), typecheck.Conv(gd, ir.NewUnaryExpr(gd, gd.Pos, ir.OCAP, s), types.Types[types.TUINT]))
	nif.Likely = true

	// then { s = s[:n] }
	slice := ir.NewSliceExpr(gd, gd.Pos, ir.OSLICE, s, nil, newLen, nil)
	slice.SetBounded(true)
	nif.Body = []ir.Node{
		ir.NewAssignStmt(gd, gd.Pos, s, slice),
	}

	// else { s = growslice(s.ptr, n, s.cap, a, T) }
	nif.Else = []ir.Node{
		ir.NewAssignStmt(gd, gd.Pos, s, walkGrowslice(gd, s, nif.PtrInit(),
			ir.NewUnaryExpr(gd, gd.Pos, ir.OSPTR, s),
			newLen,
			ir.NewUnaryExpr(gd, gd.Pos, ir.OCAP, s),
			num)),
	}

	l = append(l, nif)

	ls = n.Args[1:]
	for i, n := range ls {
		// s[s.len-argc+i] = arg
		ix := ir.NewIndexExpr(gd, gd.Pos, s, ir.NewBinaryExpr(gd, gd.Pos, ir.OSUB, newLen, ir.NewInt(gd, gd.Pos, int64(argc-i))))
		ix.SetBounded(true)
		l = append(l, ir.NewAssignStmt(gd, gd.Pos, ix, n))
	}

	typecheck.Stmts(gd, l)
	walkStmtList(gd, l)
	init.Append(l...)
	return s
}

// growslice(ptr *T, newLen, oldCap, num int, <type>) (ret []T)
func walkGrowslice(gd *base.Invocation, slice *ir.Name, init *ir.Nodes, oldPtr, newLen, oldCap, num ir.Node) *ir.CallExpr {
	elemtype := slice.Type().Elem()
	fn := typecheck.LookupRuntime(gd, "growslice", elemtype, elemtype)
	elemtypeptr := reflectdata.TypePtrAt(gd, gd.Pos, elemtype)
	return mkcall1(gd, fn, slice.Type(), init, oldPtr, newLen, oldCap, num, elemtypeptr)
}

// walkClear walks an OCLEAR node.
func walkClear(gd *base.Invocation, n *ir.UnaryExpr) ir.Node {
	typ := n.X.Type()
	switch {
	case typ.IsSlice():
		if n := arrayClear(gd, n.X.Pos(), n.X, nil); n != nil {
			return n
		}
		// If n == nil, we are clearing an array which takes zero memory, do nothing.
		return ir.NewBlockStmt(gd, n.Pos(), nil)
	case typ.IsMap():
		return mapClear(gd, n.X, reflectdata.TypePtrAt(gd, n.X.Pos(), n.X.Type()))
	}
	panic("unreachable")
}

// walkClose walks an OCLOSE node.
func walkClose(gd *base.Invocation, n *ir.UnaryExpr, init *ir.Nodes) ir.Node {
	return mkcall1(gd, chanfn(gd, "closechan", 1, n.X.Type()), nil, init, n.X)
}

// Lower copy(a, b) to a memmove call or a runtime call.
//
//	init {
//	  n := len(a)
//	  if n > len(b) { n = len(b) }
//	  if a.ptr != b.ptr { memmove(a.ptr, b.ptr, n*sizeof(elem(a))) }
//	}
//	n;
//
// Also works if b is a string.
func walkCopy(gd *base.Invocation, n *ir.BinaryExpr, init *ir.Nodes, runtimecall bool) ir.Node {
	if n.X.Type().Elem().HasPointers() {
		ir.CurFunc(gd).SetWBPos(gd, n.Pos())
		fn := writebarrierfn(gd, "typedslicecopy", n.X.Type().Elem(), n.Y.Type().Elem())
		n.X = cheapExpr(gd, n.X, init)
		ptrL, lenL := backingArrayPtrLen(gd, n.X)
		n.Y = cheapExpr(gd, n.Y, init)
		ptrR, lenR := backingArrayPtrLen(gd, n.Y)
		return mkcall1(gd, fn, n.Type(), init, reflectdata.CopyElemRType(gd, gd.Pos, n), ptrL, lenL, ptrR, lenR)
	}

	// copy(dst []byte, src string): src may be inline-rep (24 B string,
	// word0 == nil, bytes in word1 + low 56 bits of word2). A raw
	// memmove(dst, StringPtr(src), n) would deref nil. Route through
	// runtime.stringcopy, which branches on the tag and handles both reps.
	if n.Y.Type().IsString() {
		n.X = cheapExpr(gd, n.X, init)
		ptrL, lenL := backingArrayPtrLen(gd, n.X)
		n.Y = cheapExpr(gd, n.Y, init)
		fn := typecheck.LookupRuntime(gd, "stringcopy")
		srcStr := typecheck.Conv(gd, n.Y, types.Types[types.TSTRING])
		return mkcall1(gd, fn, n.Type(), init, ptrL, lenL, srcStr)
	}

	if runtimecall {
		// rely on runtime to instrument:
		//  copy(n.Left, n.Right)
		// n.Right can be a slice or string.

		n.X = cheapExpr(gd, n.X, init)
		ptrL, lenL := backingArrayPtrLen(gd, n.X)
		n.Y = cheapExpr(gd, n.Y, init)
		ptrR, lenR := backingArrayPtrLen(gd, n.Y)

		fn := typecheck.LookupRuntime(gd, "slicecopy", ptrL.Type().Elem(), ptrR.Type().Elem())

		return mkcall1(gd, fn, n.Type(), init, ptrL, lenL, ptrR, lenR, ir.NewInt(gd, gd.Pos, n.X.Type().Elem().Size()))
	}

	n.X = walkExpr(gd, n.X, init)
	n.Y = walkExpr(gd, n.Y, init)
	nl := typecheck.TempAt(gd, gd.Pos, ir.CurFunc(gd), n.X.Type())
	nr := typecheck.TempAt(gd, gd.Pos, ir.CurFunc(gd), n.Y.Type())
	var l []ir.Node
	l = append(l, ir.NewAssignStmt(gd, gd.Pos, nl, n.X))
	l = append(l, ir.NewAssignStmt(gd, gd.Pos, nr, n.Y))

	nfrm := ir.NewUnaryExpr(gd, gd.Pos, ir.OSPTR, nr)
	nto := ir.NewUnaryExpr(gd, gd.Pos, ir.OSPTR, nl)

	nlen := typecheck.TempAt(gd, gd.Pos, ir.CurFunc(gd), types.Types[types.TINT])

	// n = len(to)
	l = append(l, ir.NewAssignStmt(gd, gd.Pos, nlen, ir.NewUnaryExpr(gd, gd.Pos, ir.OLEN, nl)))

	// if n > len(frm) { n = len(frm) }
	nif := ir.NewIfStmt(gd, gd.Pos, nil, nil, nil)

	nif.Cond = ir.NewBinaryExpr(gd, gd.Pos, ir.OGT, nlen, ir.NewUnaryExpr(gd, gd.Pos, ir.OLEN, nr))
	nif.Body.Append(ir.NewAssignStmt(gd, gd.Pos, nlen, ir.NewUnaryExpr(gd, gd.Pos, ir.OLEN, nr)))
	l = append(l, nif)

	// if to.ptr != frm.ptr { memmove( ... ) }
	ne := ir.NewIfStmt(gd, gd.Pos, ir.NewBinaryExpr(gd, gd.Pos, ir.ONE, nto, nfrm), nil, nil)
	ne.Likely = true
	l = append(l, ne)

	fn := typecheck.LookupRuntime(gd, "memmove", nl.Type().Elem(), nl.Type().Elem())
	nwid := ir.Node(typecheck.TempAt(gd, gd.Pos, ir.CurFunc(gd), types.Types[types.TUINTPTR]))
	setwid := ir.NewAssignStmt(gd, gd.Pos, nwid, typecheck.Conv(gd, nlen, types.Types[types.TUINTPTR]))
	ne.Body.Append(setwid)
	nwid = ir.NewBinaryExpr(gd, gd.Pos, ir.OMUL, nwid, ir.NewInt(gd, gd.Pos, nl.Type().Elem().Size()))
	call := mkcall1(gd, fn, nil, init, nto, nfrm, nwid)
	ne.Body.Append(call)

	typecheck.Stmts(gd, l)
	walkStmtList(gd, l)
	init.Append(l...)
	return nlen
}

// walkDelete walks an ODELETE node.
func walkDelete(gd *base.Invocation, init *ir.Nodes, n *ir.CallExpr) ir.Node {
	init.Append(ir.TakeInit(n)...)
	map_ := n.Args[0]
	key := n.Args[1]
	map_ = walkExpr(gd, map_, init)
	key = walkExpr(gd, key, init)

	t := map_.Type()
	fast := mapfast(gd, t)
	key = mapKeyArg(gd, fast, n, key, false)
	return mkcall1(gd, mapfndel(gd, mapdelete[fast], t), nil, init, reflectdata.DeleteMapRType(gd, gd.Pos, n), map_, key)
}

// walkLenCap walks an OLEN or OCAP node.
func walkLenCap(gd *base.Invocation, n *ir.UnaryExpr, init *ir.Nodes) ir.Node {
	if isRuneCount(gd, n) {
		// Replace len([]rune(string)) with runtime.countrunes(string).
		return mkcall(gd, "countrunes", n.Type(), init, typecheck.Conv(gd, n.X.(*ir.ConvExpr).X, types.Types[types.TSTRING]))
	}
	if isByteCount(gd, n) {
		conv := n.X.(*ir.ConvExpr)
		walkStmtList(gd, conv.Init())
		init.Append(ir.TakeInit(conv)...)
		_, len := backingArrayPtrLen(gd, cheapExpr(gd, conv.X, init))
		return len
	}
	if isChanLenCap(n) {
		name := "chanlen"
		if n.Op() == ir.OCAP {
			name = "chancap"
		}
		// cannot use chanfn - closechan takes any, not chan any,
		// because it accepts both send-only and recv-only channels.
		fn := typecheck.LookupRuntime(gd, name, n.X.Type())
		return mkcall1(gd, fn, n.Type(), init, n.X)
	}

	n.X = walkExpr(gd, n.X, init)

	// replace len(*[10]int) with 10.
	// delayed until now to preserve side effects.
	t := n.X.Type()
	if t.IsPtr() {
		t = t.Elem()
	}
	if t.IsArray() {
		// evaluate any side effects in n.X. See issue 72844.
		appendWalkStmt(gd, init, ir.NewAssignStmt(gd, gd.Pos, ir.BlankNode, n.X))

		con := ir.NewConstExpr(gd, constant.MakeInt64(t.NumElem()), n)
		con.SetTypecheck(1)
		return con
	}
	return n
}

// walkMakeChan walks an OMAKECHAN node.
func walkMakeChan(gd *base.Invocation, n *ir.MakeExpr, init *ir.Nodes) ir.Node {
	// When size fits into int, use makechan instead of
	// makechan64, which is faster and shorter on 32 bit platforms.
	size := n.Len
	fnname := "makechan64"
	argtype := types.Types[types.TINT64]

	// Type checking guarantees that TIDEAL size is positive and fits in an int.
	// The case of size overflow when converting TUINT or TUINTPTR to TINT
	// will be handled by the negative range checks in makechan during runtime.
	if size.Type().IsKind(types.TIDEAL) || size.Type().Size() <= types.Types[types.TUINT].Size() {
		fnname = "makechan"
		argtype = types.Types[types.TINT]
	}

	return mkcall1(gd, chanfn(gd, fnname, 1, n.Type()), n.Type(), init, reflectdata.MakeChanRType(gd, gd.Pos, n), typecheck.Conv(gd, size, argtype))
}

// walkMakeMap walks an OMAKEMAP node.
func walkMakeMap(gd *base.Invocation, n *ir.MakeExpr, init *ir.Nodes) ir.Node {
	t := n.Type()
	mapType := reflectdata.MapType(gd)
	hint := n.Len

	// var m *Map
	var m ir.Node
	if ir.NodeStackAllocatable(n) {
		// Allocate hmap on stack.

		// var mv Map
		// m = &mv
		m = stackTempAddr(gd, init, mapType)

		// Allocate one group pointed to by m.dirPtr on stack if hint
		// is not larger than MapGroupSlots. In case hint is
		// larger, runtime.makemap will allocate on the heap.
		// Maximum key and elem size is 128 bytes, larger objects
		// are stored with an indirection. So max bucket size is 2048+eps.
		if !ir.IsConst(hint, constant.Int) ||
			constant.Compare(hint.Val(), token.LEQ, constant.MakeInt64(abi.MapGroupSlots)) {

			// In case hint is larger than MapGroupSlots
			// runtime.makemap will allocate on the heap, see
			// #20184
			//
			// if hint <= abi.MapGroupSlots {
			//     var gv group
			//     g = &gv
			//     g.ctrl = abi.MapCtrlEmpty
			//     m.dirPtr = g
			// }

			nif := ir.NewIfStmt(gd, gd.Pos, ir.NewBinaryExpr(gd, gd.Pos, ir.OLE, hint, ir.NewInt(gd, gd.Pos, abi.MapGroupSlots)), nil, nil)
			nif.Likely = true

			groupType := reflectdata.MapGroupType(gd, t)

			// var gv group
			// g = &gv
			g := stackTempAddr(gd, &nif.Body, groupType)

			// Can't use ir.NewInt because bit 63 is set, which
			// makes conversion to uint64 upset.
			empty := ir.NewBasicLit(gd, gd.Pos, types.UntypedInt, constant.MakeUint64(abi.MapCtrlEmpty))

			// g.ctrl = abi.MapCtrlEmpty
			csym := groupType.Field(0).Sym // g.ctrl see reflectdata/map.go
			ca := ir.NewAssignStmt(gd, gd.Pos, ir.NewSelectorExpr(gd, gd.Pos, ir.ODOT, g, csym), empty)
			nif.Body.Append(ca)

			// m.dirPtr = g
			dsym := mapType.Field(2).Sym // m.dirPtr see reflectdata/map.go
			na := ir.NewAssignStmt(gd, gd.Pos, ir.NewSelectorExpr(gd, gd.Pos, ir.ODOT, m, dsym), typecheck.ConvNop(gd, g, types.Types[types.TUNSAFEPTR]))
			nif.Body.Append(na)
			appendWalkStmt(gd, init, nif)
		}
	}

	if ir.IsConst(hint, constant.Int) && constant.Compare(hint.Val(), token.LEQ, constant.MakeInt64(abi.MapGroupSlots)) {
		// Handling make(map[any]any) and
		// make(map[any]any, hint) where hint <= abi.MapGroupSlots
		// specially allows for faster map initialization and
		// improves binary size by using calls with fewer arguments.
		// For hint <= abi.MapGroupSlots no groups will be
		// allocated by makemap. Therefore, no groups need to be
		// allocated in this code path.
		if ir.NodeStackAllocatable(n) {
			// Only need to initialize m.seed since
			// m map has been allocated on the stack already.
			// m.seed = uintptr(rand())
			rand := mkcall(gd, "rand", types.Types[types.TUINT64], init)
			seedSym := mapType.Field(1).Sym // m.seed see reflectdata/map.go
			appendWalkStmt(gd, init, ir.NewAssignStmt(gd, gd.Pos, ir.NewSelectorExpr(gd, gd.Pos, ir.ODOT, m, seedSym), typecheck.Conv(gd, rand, types.Types[types.TUINTPTR])))
			return typecheck.ConvNop(gd, m, t)
		}
		// Call runtime.makemap_small to allocate a
		// map on the heap and initialize the map's seed field.
		fn := typecheck.LookupRuntime(gd, "makemap_small", t.Key(), t.Elem())
		return mkcall1(gd, fn, n.Type(), init)
	}

	if n.Esc() != ir.EscNone {
		m = typecheck.NodNil(gd)
	}

	// Map initialization with a variable or large hint is
	// more complicated. We therefore generate a call to
	// runtime.makemap to initialize hmap and allocate the
	// map buckets.

	// When hint fits into int, use makemap instead of
	// makemap64, which is faster and shorter on 32 bit platforms.
	fnname := "makemap64"
	argtype := types.Types[types.TINT64]

	// Type checking guarantees that TIDEAL hint is positive and fits in an int.
	// See checkmake call in TMAP case of OMAKE case in OpSwitch in typecheck1 function.
	// The case of hint overflow when converting TUINT or TUINTPTR to TINT
	// will be handled by the negative range checks in makemap during runtime.
	if hint.Type().IsKind(types.TIDEAL) || hint.Type().Size() <= types.Types[types.TUINT].Size() {
		fnname = "makemap"
		argtype = types.Types[types.TINT]
	}

	fn := typecheck.LookupRuntime(gd, fnname, mapType, t.Key(), t.Elem())
	return mkcall1(gd, fn, n.Type(), init, reflectdata.MakeMapRType(gd, gd.Pos, n), typecheck.Conv(gd, hint, argtype), m)
}

// walkMakeSlice walks an OMAKESLICE node.
func walkMakeSlice(gd *base.Invocation, n *ir.MakeExpr, init *ir.Nodes) ir.Node {
	len := n.Len
	cap := n.Cap
	len = safeExpr(gd, len, init)
	if cap != nil {
		cap = safeExpr(gd, cap, init)
	} else {
		cap = len
	}
	t := n.Type()
	if t.Elem().NotInHeap() {
		gd.Errorf("%v can't be allocated in Go; it is incomplete (or unallocatable)", t.Elem())
	}

	tryStack := false
	if ir.NodeStackAllocatable(n) {
		if why := escape.HeapAllocReason(gd, n); why != "" {
			gd.Fatalf("%v has EscNone, but %v", n, why)
		}
		if ir.IsSmallIntConst(cap) {
			// Constant backing array - allocate it and slice it.
			cap := typecheck.IndexConst(gd, cap)
			// Note that len might not be constant. If it isn't, check for panics.
			// cap is constrained to [0,2^31) or [0,2^63) depending on whether
			// we're in 32-bit or 64-bit systems. So it's safe to do:
			//
			// if uint64(len) > cap {
			//     if len < 0 { panicmakeslicelen() }
			//     panicmakeslicecap()
			// }
			nif := ir.NewIfStmt(gd, gd.Pos, ir.NewBinaryExpr(gd, gd.Pos, ir.OGT, typecheck.Conv(gd, len, types.Types[types.TUINT64]), ir.NewInt(gd, gd.Pos, cap)), nil, nil)
			niflen := ir.NewIfStmt(gd, gd.Pos, ir.NewBinaryExpr(gd, gd.Pos, ir.OLT, len, ir.NewInt(gd, gd.Pos, 0)), nil, nil)
			niflen.Body = []ir.Node{mkcall(gd, "panicmakeslicelen", nil, init)}
			nif.Body.Append(niflen, mkcall(gd, "panicmakeslicecap", nil, init))
			init.Append(typecheck.Stmt(gd, nif))

			// var arr [cap]E
			// s = arr[:len]
			t := types.NewArray(t.Elem(), cap) // [cap]E
			arr := typecheck.TempAt(gd, gd.Pos, ir.CurFunc(gd), t)
			appendWalkStmt(gd, init, ir.NewAssignStmt(gd, gd.Pos, arr, nil)) // zero temp
			s := ir.NewSliceExpr(gd, gd.Pos, ir.OSLICE, arr, nil, len, nil)  // arr[:len]
			// The conv is necessary in case n.Type is named.
			return walkExpr(gd, typecheck.Expr(gd, typecheck.Conv(gd, s, n.Type())), init)
		}
		// Check that this optimization is enabled in general and for this node.
		tryStack = gd.Flag.N == 0 && base.VariableMakeHash.MatchPos(n.Pos(), nil)
	}

	// The final result is assigned to this variable.
	slice := typecheck.TempAt(gd, gd.Pos, ir.CurFunc(gd), n.Type()) // []E result (possibly named)

	if tryStack {
		// K := maxStackSize/sizeof(E)
		// if cap <= K {
		//     var arr [K]E
		//     slice = arr[:len:cap]
		// } else {
		//     slice = makeslice(elemType, len, cap)
		// }
		maxStackSize := int64(gd.Debug.VariableMakeThreshold)
		K := maxStackSize / t.Elem().Size() // rounds down
		if K > 0 {                          // skip if elem size is too big.
			nif := ir.NewIfStmt(gd, gd.Pos, ir.NewBinaryExpr(gd, gd.Pos, ir.OLE, typecheck.Conv(gd, cap, types.Types[types.TUINT64]), ir.NewInt(gd, gd.Pos, K)), nil, nil)

			// cap is in bounds after the K check, but len might not be.
			// (Note that the slicing below would generate a panic for
			// the same bad cases, but we want makeslice panics, not
			// regular slicing panics.)
			lenCap := ir.NewIfStmt(gd, gd.Pos, ir.NewBinaryExpr(gd, gd.Pos, ir.OGT, typecheck.Conv(gd, len, types.Types[types.TUINT64]), typecheck.Conv(gd, cap, types.Types[types.TUINT64])), nil, nil)
			lenZero := ir.NewIfStmt(gd, gd.Pos, ir.NewBinaryExpr(gd, gd.Pos, ir.OLT, len, ir.NewInt(gd, gd.Pos, 0)), nil, nil)
			lenZero.Body.Append(mkcall(gd, "panicmakeslicelen", nil, &lenZero.Body))
			lenCap.Body.Append(lenZero)
			lenCap.Body.Append(mkcall(gd, "panicmakeslicecap", nil, &lenCap.Body))
			nif.Body.Append(lenCap)

			t := types.NewArray(t.Elem(), K) // [K]E
			// Wrap in a struct containing a [0]uintptr field to force
			// pointer alignment. Some user code expects higher alignment
			// than what is guaranteed by the element type, because that's
			// the behavior they observed of mallocgc, and then relied upon.
			// See issue 73199.
			field := typecheck.Lookup(gd, "arr")
			t = types.NewStruct([]*types.Field{
				{Sym: types.BlankSym(gd), Type: types.NewArray(types.Types[types.TUINTPTR], 0)},
				{Sym: field, Type: t},
			})
			t.SetNoalg(true)
			store := typecheck.TempAt(gd, gd.Pos, ir.CurFunc(gd), t)        // var store struct{_ uintptr[0]; arr [K]E}
			nif.Body.Append(ir.NewAssignStmt(gd, gd.Pos, store, nil))       // store = {} (zero it)
			arr := ir.NewSelectorExpr(gd, gd.Pos, ir.ODOT, store, field)    // arr = store.arr
			s := ir.NewSliceExpr(gd, gd.Pos, ir.OSLICE, arr, nil, len, cap) // store.arr[:len:cap]
			nif.Body.Append(ir.NewAssignStmt(gd, gd.Pos, slice, s))         // slice = store.arr[:len:cap]

			appendWalkStmt(gd, init, typecheck.Stmt(gd, nif))

			// Put makeslice call below in the else branch.
			init = &nif.Else
		}
	}

	// Set up a call to makeslice.
	// When len and cap can fit into int, use makeslice instead of
	// makeslice64, which is faster and shorter on 32 bit platforms.
	fnname := "makeslice64"
	argtype := types.Types[types.TINT64]

	// Type checking guarantees that TIDEAL len/cap are positive and fit in an int.
	// The case of len or cap overflow when converting TUINT or TUINTPTR to TINT
	// will be handled by the negative range checks in makeslice during runtime.
	if (len.Type().IsKind(types.TIDEAL) || len.Type().Size() <= types.Types[types.TUINT].Size()) &&
		(cap.Type().IsKind(types.TIDEAL) || cap.Type().Size() <= types.Types[types.TUINT].Size()) {
		fnname = "makeslice"
		argtype = types.Types[types.TINT]
	}
	fn := typecheck.LookupRuntime(gd, fnname)
	ptr := mkcall1(gd, fn, types.Types[types.TUNSAFEPTR], init, reflectdata.MakeSliceElemRType(gd, gd.Pos, n), typecheck.Conv(gd, len, argtype), typecheck.Conv(gd, cap, argtype))
	ptr.MarkNonNil()
	len = typecheck.Conv(gd, len, types.Types[types.TINT])
	cap = typecheck.Conv(gd, cap, types.Types[types.TINT])
	s := ir.NewSliceHeaderExpr(gd, gd.Pos, t, ptr, len, cap)
	appendWalkStmt(gd, init, ir.NewAssignStmt(gd, gd.Pos, slice, s))

	return slice
}

// walkMakeSliceCopy walks an OMAKESLICECOPY node.
func walkMakeSliceCopy(gd *base.Invocation, n *ir.MakeExpr, init *ir.Nodes) ir.Node {
	if ir.NodeStackAllocatable(n) {
		gd.Fatalf("OMAKESLICECOPY with EscNone: %v", n)
	}

	t := n.Type()
	if t.Elem().NotInHeap() {
		gd.Errorf("%v can't be allocated in Go; it is incomplete (or unallocatable)", t.Elem())
	}

	length := typecheck.Conv(gd, n.Len, types.Types[types.TINT])
	copylen := ir.NewUnaryExpr(gd, gd.Pos, ir.OLEN, n.Cap)
	copyptr := ir.NewUnaryExpr(gd, gd.Pos, ir.OSPTR, n.Cap)

	if !t.Elem().HasPointers() && n.Bounded() {
		// When len(to)==len(from) and elements have no pointers:
		// replace make+copy with runtime.mallocgc+runtime.memmove.

		// We do not check for overflow of len(to)*elem.Width here
		// since len(from) is an existing checked slice capacity
		// with same elem.Width for the from slice.
		size := ir.NewBinaryExpr(gd, gd.Pos, ir.OMUL, typecheck.Conv(gd, length, types.Types[types.TUINTPTR]), typecheck.Conv(gd, ir.NewInt(gd, gd.Pos, t.Elem().Size()), types.Types[types.TUINTPTR]))

		// instantiate mallocgc(size uintptr, typ *byte, needszero bool) unsafe.Pointer
		fn := typecheck.LookupRuntime(gd, "mallocgc")
		ptr := mkcall1(gd, fn, types.Types[types.TUNSAFEPTR], init, size, typecheck.NodNil(gd), ir.NewBool(gd, gd.Pos, false))
		ptr.MarkNonNil()
		sh := ir.NewSliceHeaderExpr(gd, gd.Pos, t, ptr, length, length)

		s := typecheck.TempAt(gd, gd.Pos, ir.CurFunc(gd), t)
		r := typecheck.Stmt(gd, ir.NewAssignStmt(gd, gd.Pos, s, sh))
		r = walkExpr(gd, r, init)
		init.Append(r)

		// instantiate memmove(to *any, frm *any, size uintptr)
		fn = typecheck.LookupRuntime(gd, "memmove", t.Elem(), t.Elem())
		ncopy := mkcall1(gd, fn, nil, init, ir.NewUnaryExpr(gd, gd.Pos, ir.OSPTR, s), copyptr, size)
		init.Append(walkExpr(gd, typecheck.Stmt(gd, ncopy), init))

		return s
	}
	// Replace make+copy with runtime.makeslicecopy.
	// instantiate makeslicecopy(typ *byte, tolen int, fromlen int, from unsafe.Pointer) unsafe.Pointer
	fn := typecheck.LookupRuntime(gd, "makeslicecopy")
	ptr := mkcall1(gd, fn, types.Types[types.TUNSAFEPTR], init, reflectdata.MakeSliceElemRType(gd, gd.Pos, n), length, copylen, typecheck.Conv(gd, copyptr, types.Types[types.TUNSAFEPTR]))
	ptr.MarkNonNil()
	sh := ir.NewSliceHeaderExpr(gd, gd.Pos, t, ptr, length, length)
	return walkExpr(gd, typecheck.Expr(gd, sh), init)
}

// walkNew walks an ONEW node.
func walkNew(gd *base.Invocation, n *ir.UnaryExpr, init *ir.Nodes) ir.Node {
	t := n.Type().Elem()
	if t.NotInHeap() {
		gd.Errorf("%v can't be allocated in Go; it is incomplete (or unallocatable)", n.Type().Elem())
	}
	if ir.NodeStackAllocatable(n) {
		if t.Size() > ir.MaxImplicitStackVarSize {
			gd.Fatalf("large ONEW with EscNone: %v", n)
		}
		addr := stackTempAddr(gd, init, t)
		// gd escape-bits: propagate the EscCandidate bit from the
		// ONEW to the resulting &stackTemp expression so the
		// call-site wrap can still detect the candidate at the
		// call site (ONEW is the node tagged by escape; walk
		// rewrites it in place and the bit would otherwise be
		// lost).
		if n.EscCandidate() {
			addr.SetEscCandidate(true)
		}
		return addr
	}
	types.CalcSize(gd, t)
	n.MarkNonNil()
	return n
}

func walkMinMax(gd *base.Invocation, n *ir.CallExpr, init *ir.Nodes) ir.Node {
	init.Append(ir.TakeInit(n)...)
	walkExprList(gd, n.Args, init)
	return n
}

// generate code for print.
func walkPrint(gd *base.Invocation, nn *ir.CallExpr, init *ir.Nodes) ir.Node {
	// Hoist all the argument evaluation up before the lock.
	walkExprListCheap(gd, nn.Args, init)

	// For println, add " " between elements and "\n" at the end.
	if nn.Op() == ir.OPRINTLN {
		s := nn.Args
		t := make([]ir.Node, 0, len(s)*2)
		for i, n := range s {
			if i != 0 {
				t = append(t, ir.NewString(gd, gd.Pos, " "))
			}
			t = append(t, n)
		}
		t = append(t, ir.NewString(gd, gd.Pos, "\n"))
		nn.Args = t
	}

	// Collapse runs of constant strings.
	s := nn.Args
	t := make([]ir.Node, 0, len(s))
	for i := 0; i < len(s); {
		var strs []string
		for i < len(s) && ir.IsConst(s[i], constant.String) {
			strs = append(strs, ir.StringVal(s[i]))
			i++
		}
		if len(strs) > 0 {
			t = append(t, ir.NewString(gd, gd.Pos, strings.Join(strs, "")))
		}
		if i < len(s) {
			t = append(t, s[i])
			i++
		}
	}
	nn.Args = t

	calls := []ir.Node{mkcall(gd, "printlock", nil, init)}
	for i, n := range nn.Args {
		if n.Op() == ir.OLITERAL {
			if n.Type() == types.UntypedRune {
				n = typecheck.DefaultLit(gd, n, types.RuneType)
			}

			switch n.Val().Kind() {
			case constant.Int:
				n = typecheck.DefaultLit(gd, n, types.Types[types.TINT64])

			case constant.Float:
				n = typecheck.DefaultLit(gd, n, types.Types[types.TFLOAT64])
			}
		}

		if n.Op() != ir.OLITERAL && n.Type() != nil && n.Type().Kind() == types.TIDEAL {
			n = typecheck.DefaultLit(gd, n, types.Types[types.TINT64])
		}
		n = typecheck.DefaultLit(gd, n, nil)
		nn.Args[i] = n
		if n.Type() == nil || n.Type().Kind() == types.TFORW {
			continue
		}

		var on *ir.Name
		switch n.Type().Kind() {
		case types.TINTER:
			if n.Type().IsEmptyInterface() {
				on = typecheck.LookupRuntime(gd, "printeface", n.Type())
			} else {
				on = typecheck.LookupRuntime(gd, "printiface", n.Type())
			}
		case types.TPTR:
			if n.Type().Elem().NotInHeap() {
				on = typecheck.LookupRuntime(gd, "printuintptr")
				n = ir.NewConvExpr(gd, gd.Pos, ir.OCONV, nil, n)
				n.SetType(types.Types[types.TUNSAFEPTR])
				n = ir.NewConvExpr(gd, gd.Pos, ir.OCONV, nil, n)
				n.SetType(types.Types[types.TUINTPTR])
				break
			}
			fallthrough
		case types.TCHAN, types.TMAP, types.TFUNC, types.TUNSAFEPTR:
			on = typecheck.LookupRuntime(gd, "printpointer", n.Type())
		case types.TSLICE:
			on = typecheck.LookupRuntime(gd, "printslice", n.Type())
		case types.TUINT, types.TUINT8, types.TUINT16, types.TUINT32, types.TUINT64, types.TUINTPTR:
			if types.RuntimeSymName(n.Type().Sym()) == "hex" {
				on = typecheck.LookupRuntime(gd, "printhex")
			} else {
				on = typecheck.LookupRuntime(gd, "printuint")
			}
		case types.TINT, types.TINT8, types.TINT16, types.TINT32, types.TINT64:
			on = typecheck.LookupRuntime(gd, "printint")
		case types.TFLOAT32:
			on = typecheck.LookupRuntime(gd, "printfloat32")
		case types.TFLOAT64:
			on = typecheck.LookupRuntime(gd, "printfloat64")
		case types.TCOMPLEX64:
			on = typecheck.LookupRuntime(gd, "printcomplex64")
		case types.TCOMPLEX128:
			on = typecheck.LookupRuntime(gd, "printcomplex128")
		case types.TBOOL:
			on = typecheck.LookupRuntime(gd, "printbool")
		case types.TSTRING:
			cs := ""
			if ir.IsConst(n, constant.String) {
				cs = ir.StringVal(n)
			}
			// Print values of the named type `quoted` using printquoted.
			if types.RuntimeSymName(n.Type().Sym()) == "quoted" {
				on = typecheck.LookupRuntime(gd, "printquoted")
			} else {
				switch cs {
				case " ":
					on = typecheck.LookupRuntime(gd, "printsp")
				case "\n":
					on = typecheck.LookupRuntime(gd, "printnl")
				default:
					on = typecheck.LookupRuntime(gd, "printstring")
				}
			}
		default:
			badtype(gd, ir.OPRINT, n.Type(), nil)
			continue
		}

		r := ir.NewCallExpr(gd, gd.Pos, ir.OCALL, on, nil)
		if params := on.Type().Params(); len(params) > 0 {
			t := params[0].Type
			n = typecheck.Conv(gd, n, t)
			r.Args.Append(n)
		}
		calls = append(calls, r)
	}

	calls = append(calls, mkcall(gd, "printunlock", nil, init))

	typecheck.Stmts(gd, calls)
	walkExprList(gd, calls, init)

	r := ir.NewBlockStmt(gd, gd.Pos, nil)
	r.List = calls
	return walkStmt(gd, typecheck.Stmt(gd, r))
}

// walkRecover walks an ORECOVER node.
func walkRecover(gd *base.Invocation, nn *ir.CallExpr, init *ir.Nodes) ir.Node {
	return mkcall(gd, "gorecover", nn.Type(), init)
}

// walkUnsafeData walks an OUNSAFESLICEDATA or OUNSAFESTRINGDATA expression.
func walkUnsafeData(gd *base.Invocation, n *ir.UnaryExpr, init *ir.Nodes) ir.Node {
	if n.X.Type().IsString() {
		// gd: unsafe.StringData must return a stable pointer for the
		// lifetime of the string value. For inline-rep inputs the
		// bytes live in the header (stack/register), so a plain OSPTR
		// would dangle once the caller's frame exits.
		//
		// Constant-string fast path: when the argument is a compile-time
		// string literal, point directly at its rodata symbol. Stock Go
		// does this implicitly via OSPTR+ConstString's heap form; under
		// Phase C's inline rewrite we would otherwise pay a per-call
		// heap copy in hot zero-alloc code (e.g. log/slog's StringValue).
		// Look through ONAME / OCONVNOP to the underlying literal for
		// inliner-created parameter bindings (e.g. `value := "foo"` from
		// inlining log/slog.StringValue("foo")). ir.StaticValue walks
		// single-assignment chains back to the root.
		x := ir.StaticValue(n.X)
		if x.Op() == ir.OLITERAL && x.Val().Kind() == constant.String {
			s := constant.StringVal(x.Val())
			if len(s) == 0 {
				return typecheck.ConvNop(gd, typecheck.NodNil(gd), n.Type())
			}
			sym := staticdata.StringSym(gd, n.Pos(), s)
			addr := typecheck.NodAddr(gd, ir.NewLinksymExpr(gd, n.Pos(), sym, types.Types[types.TUINT8]))
			addr.SetType(n.Type())
			return typecheck.Expr(gd, addr)
		}
		// Non-escaping fast path: escape analysis has marked n EscNone,
		// so the pointer is consumed within the caller's frame. OSPTR
		// lowers through ssagen.stringBytesTransient — heap-rep returns
		// word 0 directly, inline-rep spills to an addrtaken autotmp
		// that lives for the function's duration. No heap alloc.
		//
		// The escape pass's OUNSAFESTRINGDATA case spills via a location
		// whose escape status is reflected in n.Esc(); see
		// cmd/compile/internal/escape/expr.go.
		if ir.NodeStackAllocatable(n) {
			res := typecheck.Expr(gd, ir.NewUnaryExpr(gd, n.Pos(), ir.OSPTR, n.X))
			res.SetType(n.Type())
			return walkExpr(gd, res, init)
		}
		// Otherwise route through runtime.stringDataHeap, which returns
		// word0 unchanged for heap-rep (zero overhead) and heap-copies
		// inline-rep bytes so the pointer survives the caller's frame.
		s := walkExpr(gd, n.X, init)
		res := mkcall(gd, "stringDataHeap", n.Type(), init, s)
		return res
	}
	// unsafe.SliceData: slices always have a stable backing pointer,
	// OSPTR is sufficient.
	slice := walkExpr(gd, n.X, init)
	res := typecheck.Expr(gd, ir.NewUnaryExpr(gd, n.Pos(), ir.OSPTR, slice))
	res.SetType(n.Type())
	return walkExpr(gd, res, init)
}

func walkUnsafeSlice(gd *base.Invocation, n *ir.BinaryExpr, init *ir.Nodes) ir.Node {
	ptr := safeExpr(gd, n.X, init)
	len := safeExpr(gd, n.Y, init)
	sliceType := n.Type()

	lenType := types.Types[types.TINT64]
	unsafePtr := typecheck.Conv(gd, ptr, types.Types[types.TUNSAFEPTR])

	// If checkptr enabled, call runtime.unsafeslicecheckptr to check ptr and len.
	// for simplicity, unsafeslicecheckptr always uses int64.
	// Type checking guarantees that TIDEAL len/cap are positive and fit in an int.
	// The case of len or cap overflow when converting TUINT or TUINTPTR to TINT
	// will be handled by the negative range checks in unsafeslice during runtime.
	if ir.ShouldCheckPtr(gd, ir.CurFunc(gd), 1) {
		fnname := "unsafeslicecheckptr"
		fn := typecheck.LookupRuntime(gd, fnname)
		init.Append(mkcall1(gd, fn, nil, init, reflectdata.UnsafeSliceElemRType(gd, gd.Pos, n), unsafePtr, typecheck.Conv(gd, len, lenType)))
	} else {
		// Otherwise, open code unsafe.Slice to prevent runtime call overhead.
		// Keep this code in sync with runtime.unsafeslice{,64}
		if len.Type().IsKind(types.TIDEAL) || len.Type().Size() <= types.Types[types.TUINT].Size() {
			lenType = types.Types[types.TINT]
		} else {
			// len64 := int64(len)
			// if int64(int(len64)) != len64 {
			//     panicunsafeslicelen()
			// }
			len64 := typecheck.Conv(gd, len, lenType)
			nif := ir.NewIfStmt(gd, gd.Pos, nil, nil, nil)
			nif.Cond = ir.NewBinaryExpr(gd, gd.Pos, ir.ONE, typecheck.Conv(gd, typecheck.Conv(gd, len64, types.Types[types.TINT]), lenType), len64)
			nif.Body.Append(mkcall(gd, "panicunsafeslicelen", nil, &nif.Body))
			appendWalkStmt(gd, init, nif)
		}

		// if len < 0 { panicunsafeslicelen() }
		nif := ir.NewIfStmt(gd, gd.Pos, nil, nil, nil)
		nif.Cond = ir.NewBinaryExpr(gd, gd.Pos, ir.OLT, typecheck.Conv(gd, len, lenType), ir.NewInt(gd, gd.Pos, 0))
		nif.Body.Append(mkcall(gd, "panicunsafeslicelen", nil, &nif.Body))
		appendWalkStmt(gd, init, nif)

		if sliceType.Elem().Size() == 0 {
			// if ptr == nil && len > 0  {
			//      panicunsafesliceptrnil()
			// }
			nifPtr := ir.NewIfStmt(gd, gd.Pos, nil, nil, nil)
			isNil := ir.NewBinaryExpr(gd, gd.Pos, ir.OEQ, unsafePtr, typecheck.NodNil(gd))
			gtZero := ir.NewBinaryExpr(gd, gd.Pos, ir.OGT, typecheck.Conv(gd, len, lenType), ir.NewInt(gd, gd.Pos, 0))
			nifPtr.Cond =
				ir.NewLogicalExpr(gd, gd.Pos, ir.OANDAND, isNil, gtZero)
			nifPtr.Body.Append(mkcall(gd, "panicunsafeslicenilptr", nil, &nifPtr.Body))
			appendWalkStmt(gd, init, nifPtr)

			h := ir.NewSliceHeaderExpr(gd, n.Pos(), sliceType,
				typecheck.Conv(gd, ptr, types.Types[types.TUNSAFEPTR]),
				typecheck.Conv(gd, len, types.Types[types.TINT]),
				typecheck.Conv(gd, len, types.Types[types.TINT]))
			return walkExpr(gd, typecheck.Expr(gd, h), init)
		}

		// mem, overflow := math.mulUintptr(et.size, len)
		mem := typecheck.TempAt(gd, gd.Pos, ir.CurFunc(gd), types.Types[types.TUINTPTR])
		overflow := typecheck.TempAt(gd, gd.Pos, ir.CurFunc(gd), types.Types[types.TBOOL])

		decl := types.NewSignature(gd, nil,
			[]*types.Field{
				types.NewField(gd.Pos, nil, types.Types[types.TUINTPTR]),
				types.NewField(gd.Pos, nil, types.Types[types.TUINTPTR]),
			},
			[]*types.Field{
				types.NewField(gd.Pos, nil, types.Types[types.TUINTPTR]),
				types.NewField(gd.Pos, nil, types.Types[types.TBOOL]),
			})

		fn := ir.NewFunc(gd, n.Pos(), n.Pos(), mathMulUintptrSym(gd), decl)

		call := mkcall1(gd, fn.Nname, fn.Type().ResultsTuple(), init, ir.NewInt(gd, gd.Pos, sliceType.Elem().Size()), typecheck.Conv(gd, typecheck.Conv(gd, len, lenType), types.Types[types.TUINTPTR]))
		appendWalkStmt(gd, init, ir.NewAssignListStmt(gd, gd.Pos, ir.OAS2, []ir.Node{mem, overflow}, []ir.Node{call}))

		// if overflow || mem > -uintptr(ptr) {
		//     if ptr == nil {
		//         panicunsafesliceptrnil()
		//     }
		//     panicunsafeslicelen()
		// }
		nif = ir.NewIfStmt(gd, gd.Pos, nil, nil, nil)
		memCond := ir.NewBinaryExpr(gd, gd.Pos, ir.OGT, mem, ir.NewUnaryExpr(gd, gd.Pos, ir.ONEG, typecheck.Conv(gd, unsafePtr, types.Types[types.TUINTPTR])))
		nif.Cond = ir.NewLogicalExpr(gd, gd.Pos, ir.OOROR, overflow, memCond)
		nifPtr := ir.NewIfStmt(gd, gd.Pos, nil, nil, nil)
		nifPtr.Cond = ir.NewBinaryExpr(gd, gd.Pos, ir.OEQ, unsafePtr, typecheck.NodNil(gd))
		nifPtr.Body.Append(mkcall(gd, "panicunsafeslicenilptr", nil, &nifPtr.Body))
		nif.Body.Append(nifPtr, mkcall(gd, "panicunsafeslicelen", nil, &nif.Body))
		appendWalkStmt(gd, init, nif)
	}

	h := ir.NewSliceHeaderExpr(gd, n.Pos(), sliceType,
		typecheck.Conv(gd, ptr, types.Types[types.TUNSAFEPTR]),
		typecheck.Conv(gd, len, types.Types[types.TINT]),
		typecheck.Conv(gd, len, types.Types[types.TINT]))
	return walkExpr(gd, typecheck.Expr(gd, h), init)
}

// mathMulUintptrSym returns gd's per-Invocation Sym for
// internal/runtime/math.MulUintptr. Was a package-level
// `var math_MulUintptr` that pinned a process-global Pkg pointer
// (and therefore a Sym whose Pkg differed from per-Invocation
// Syms). Made into a function so the Pkg is interned in gd's
// TypesPkgMap.
//
// Passes "" for the package name on purpose: the importer may have
// already created the Pkg (with the real "math" name); NewPkg with
// empty name accepts whatever name the existing Pkg carries instead
// of asserting a match.
func mathMulUintptrSym(gd *base.Invocation) *types.Sym {
	return types.NewPkg(gd, "internal/runtime/math", "").Lookup("MulUintptr")
}

func walkUnsafeString(gd *base.Invocation, n *ir.BinaryExpr, init *ir.Nodes) ir.Node {
	ptr := safeExpr(gd, n.X, init)
	len := safeExpr(gd, n.Y, init)

	lenType := types.Types[types.TINT64]
	unsafePtr := typecheck.Conv(gd, ptr, types.Types[types.TUNSAFEPTR])

	// If checkptr enabled, call runtime.unsafestringcheckptr to check ptr and len.
	// for simplicity, unsafestringcheckptr always uses int64.
	// Type checking guarantees that TIDEAL len are positive and fit in an int.
	if ir.ShouldCheckPtr(gd, ir.CurFunc(gd), 1) {
		fnname := "unsafestringcheckptr"
		fn := typecheck.LookupRuntime(gd, fnname)
		init.Append(mkcall1(gd, fn, nil, init, unsafePtr, typecheck.Conv(gd, len, lenType)))
	} else {
		// Otherwise, open code unsafe.String to prevent runtime call overhead.
		// Keep this code in sync with runtime.unsafestring{,64}
		if len.Type().IsKind(types.TIDEAL) || len.Type().Size() <= types.Types[types.TUINT].Size() {
			lenType = types.Types[types.TINT]
		} else {
			// len64 := int64(len)
			// if int64(int(len64)) != len64 {
			//     panicunsafestringlen()
			// }
			len64 := typecheck.Conv(gd, len, lenType)
			nif := ir.NewIfStmt(gd, gd.Pos, nil, nil, nil)
			nif.Cond = ir.NewBinaryExpr(gd, gd.Pos, ir.ONE, typecheck.Conv(gd, typecheck.Conv(gd, len64, types.Types[types.TINT]), lenType), len64)
			nif.Body.Append(mkcall(gd, "panicunsafestringlen", nil, &nif.Body))
			appendWalkStmt(gd, init, nif)
		}

		// if len < 0 { panicunsafestringlen() }
		nif := ir.NewIfStmt(gd, gd.Pos, nil, nil, nil)
		nif.Cond = ir.NewBinaryExpr(gd, gd.Pos, ir.OLT, typecheck.Conv(gd, len, lenType), ir.NewInt(gd, gd.Pos, 0))
		nif.Body.Append(mkcall(gd, "panicunsafestringlen", nil, &nif.Body))
		appendWalkStmt(gd, init, nif)

		// if uintpr(len) > -uintptr(ptr) {
		//    if ptr == nil {
		//       panicunsafestringnilptr()
		//    }
		//    panicunsafeslicelen()
		// }
		nifLen := ir.NewIfStmt(gd, gd.Pos, nil, nil, nil)
		nifLen.Cond = ir.NewBinaryExpr(gd, gd.Pos, ir.OGT, typecheck.Conv(gd, len, types.Types[types.TUINTPTR]), ir.NewUnaryExpr(gd, gd.Pos, ir.ONEG, typecheck.Conv(gd, unsafePtr, types.Types[types.TUINTPTR])))
		nifPtr := ir.NewIfStmt(gd, gd.Pos, nil, nil, nil)
		nifPtr.Cond = ir.NewBinaryExpr(gd, gd.Pos, ir.OEQ, unsafePtr, typecheck.NodNil(gd))
		nifPtr.Body.Append(mkcall(gd, "panicunsafestringnilptr", nil, &nifPtr.Body))
		nifLen.Body.Append(nifPtr, mkcall(gd, "panicunsafestringlen", nil, &nifLen.Body))
		appendWalkStmt(gd, init, nifLen)
	}
	h := ir.NewStringHeaderExpr(gd, n.Pos(),
		typecheck.Conv(gd, ptr, types.Types[types.TUNSAFEPTR]),
		typecheck.Conv(gd, len, types.Types[types.TINT]),
	)
	return walkExpr(gd, typecheck.Expr(gd, h), init)
}

func badtype(gd *base.Invocation, op ir.Op, tl, tr *types.Type) {
	var s string
	if tl != nil {
		s += fmt.Sprintf("\n\t%v", tl)
	}
	if tr != nil {
		s += fmt.Sprintf("\n\t%v", tr)
	}

	// common mistake: *struct and *interface.
	if tl != nil && tr != nil && tl.IsPtr() && tr.IsPtr() {
		if tl.Elem().IsStruct() && tr.Elem().IsInterface() {
			s += "\n\t(*struct vs *interface)"
		} else if tl.Elem().IsInterface() && tr.Elem().IsStruct() {
			s += "\n\t(*interface vs *struct)"
		}
	}

	gd.Errorf("illegal types for operand: %v%s", op, s)
}

func writebarrierfn(gd *base.Invocation, name string, l *types.Type, r *types.Type) ir.Node {
	return typecheck.LookupRuntime(gd, name, l, r)
}

// isRuneCount reports whether n is of the form len([]rune(string)).
// These are optimized into a call to runtime.countrunes.
func isRuneCount(gd *base.Invocation, n ir.Node) bool {
	return gd.Flag.N == 0 && !gd.Flag.Cfg.Instrumenting && n.Op() == ir.OLEN && n.(*ir.UnaryExpr).X.Op() == ir.OSTR2RUNES
}

// isByteCount reports whether n is of the form len(string([]byte)).
func isByteCount(gd *base.Invocation, n ir.Node) bool {
	return gd.Flag.N == 0 && !gd.Flag.Cfg.Instrumenting && n.Op() == ir.OLEN &&
		(n.(*ir.UnaryExpr).X.Op() == ir.OBYTES2STR || n.(*ir.UnaryExpr).X.Op() == ir.OBYTES2STRTMP)
}

// isChanLenCap reports whether n is of the form len(c) or cap(c) for a channel c.
// Note that this does not check for -n or instrumenting because this
// is a correctness rewrite, not an optimization.
func isChanLenCap(n ir.Node) bool {
	return (n.Op() == ir.OLEN || n.Op() == ir.OCAP) && n.(*ir.UnaryExpr).X.Type().IsChan()
}
