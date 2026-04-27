// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package walk

import (
	"encoding/binary"
	"go/constant"

	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/reflectdata"
	"cmd/compile/internal/ssagen"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
	"cmd/internal/sys"
)

// walkConv walks an OCONV or OCONVNOP (but not OCONVIFACE) node.
func walkConv(gd *base.Invocation, n *ir.ConvExpr, init *ir.Nodes) ir.Node {
	n.X = walkExpr(gd, n.X, init)
	if n.Op() == ir.OCONVNOP && n.Type() == n.X.Type() {
		return n.X
	}
	if n.Op() == ir.OCONVNOP && ir.ShouldCheckPtr(gd, ir.CurFunc(gd), 1) {
		if n.Type().IsUnsafePtr() && n.X.Type().IsUintptr() { // uintptr to unsafe.Pointer
			return walkCheckPtrArithmetic(gd, n, init)
		}
	}
	param, result := rtconvfn(n.X.Type(), n.Type())
	if param == types.Txxx {
		return n
	}
	fn := types.BasicTypeNames[param] + "to" + types.BasicTypeNames[result]
	return typecheck.Conv(gd, mkcall(gd, fn, types.Types[result], init, typecheck.Conv(gd, n.X, types.Types[param])), n.Type())
}

// walkConvInterface walks an OCONVIFACE node.
func walkConvInterface(gd *base.Invocation, n *ir.ConvExpr, init *ir.Nodes) ir.Node {

	n.X = walkExpr(gd, n.X, init)

	fromType := n.X.Type()
	toType := n.Type()
	if !fromType.IsInterface() && !ir.IsBlank(ir.CurFunc(gd).Nname) {
		// skip unnamed functions (func _())
		if fromType.HasShape() {
			// Unified IR uses OCONVIFACE for converting all derived types
			// to interface type. Avoid assertion failure in
			// MarkTypeUsedInInterface, because we've marked used types
			// separately anyway.
		} else {
			reflectdata.MarkTypeUsedInInterface(gd, fromType, ir.CurFunc(gd).LSym)
		}
	}

	if !fromType.IsInterface() {
		typeWord := reflectdata.ConvIfaceTypeWord(gd, gd.Pos, n)
		var dw ir.Node
		if types.IsInlineIface(fromType) && !fromType.IsPtrShaped() {
			// gd fat-interface: hand the raw source value to OMAKEFACE as
			// n.Y. ssagen recognises a non-pointer Y and lowers into the
			// inline path (stage into two float64 halves, feed
			// OpIMake's inline slots, leave data nil). No convT* call.
			//
			// Pointer-shaped types (regular pointers, and notinheap
			// pointers like *cgo.Incomplete) are excluded: notinheap
			// pointer ifaces are built by hand in the runtime
			// (e.g. (*pollDesc).makeArg sets .data = &field) and callers
			// type-asserting them expect the stock indirect layout.
			// Forcing them through inline breaks that contract; handing
			// them to dataWord keeps the stock convT* boxing path.
			if gd.Debug.EscapeDebug > 0 {
				gd.WarnfAt(n.Pos(), "convert: using inline slot for interface value: %v", n.X)
			}
			dw = n.X
		} else if types.IsSpreadIface(fromType) {
			// gd Phase D: string and slice headers (3 words, first a
			// pointer) stored across the iface's data slot (word 0) and
			// its 16-byte inline slot (words 1+2). Hand the raw source
			// value to OMAKEFACE; ssagen emits a 4-slot OpIMake. No
			// convT*, no heap allocation.
			//
			// Works for both empty and non-empty interface targets:
			// getClosureAndRcvr reassembles the 24 B header from
			// data+inline into a stack stage before calling the method
			// (see ssa.go:getClosureAndRcvr, itab.Inline == 2).
			if gd.Debug.EscapeDebug > 0 {
				gd.WarnfAt(n.Pos(), "convert: using spread slots for interface value: %v", n.X)
			}
			dw = n.X
		} else {
			dw = dataWord(gd, n, init)
		}
		l := ir.NewBinaryExpr(gd, gd.Pos, ir.OMAKEFACE, typeWord, dw)
		l.SetType(toType)
		l.SetTypecheck(n.Typecheck())
		return l
	}
	if fromType.IsEmptyInterface() {
		gd.Fatalf("OCONVIFACE can't operate on an empty interface")
	}

	// Evaluate the input interface.
	c := typecheck.TempAt(gd, gd.Pos, ir.CurFunc(gd), fromType)
	init.Append(ir.NewAssignStmt(gd, gd.Pos, c, n.X))

	if toType.IsEmptyInterface() {
		// Implement interface to empty interface conversion:
		//
		// var res *uint8
		// res = (*uint8)(unsafe.Pointer(itab))
		// if res != nil {
		//    res = res.type
		// }

		// Grab its parts.
		itab := ir.NewUnaryExpr(gd, gd.Pos, ir.OITAB, c)
		itab.SetType(types.Types[types.TUINTPTR].PtrTo())
		itab.SetTypecheck(1)
		data := ir.NewUnaryExpr(gd, n.Pos(), ir.OIDATA, c)
		data.SetType(types.Types[types.TUINT8].PtrTo()) // Type is generic pointer - we're just passing it through.
		data.SetTypecheck(1)

		typeWord := typecheck.TempAt(gd, gd.Pos, ir.CurFunc(gd), types.NewPtr(types.Types[types.TUINT8]))
		init.Append(ir.NewAssignStmt(gd, gd.Pos, typeWord, typecheck.Conv(gd, typecheck.Conv(gd, itab, types.Types[types.TUNSAFEPTR]), typeWord.Type())))
		nif := ir.NewIfStmt(gd, gd.Pos, typecheck.Expr(gd, ir.NewBinaryExpr(gd, gd.Pos, ir.ONE, typeWord, typecheck.NodNil(gd))), nil, nil)
		nif.Body = []ir.Node{ir.NewAssignStmt(gd, gd.Pos, typeWord, itabType(gd, typeWord))}
		init.Append(nif)

		// Build the result.
		// e = iface{typeWord, data}
		e := ir.NewBinaryExpr(gd, gd.Pos, ir.OMAKEFACE, typeWord, data)
		e.SetType(toType) // assign type manually, typecheck doesn't understand OEFACE.
		e.SetTypecheck(1)
		return e
	}

	// Must be converting I2I (more specific to less specific interface).
	// Use the same code as e, _ = c.(T).
	var rhs ir.Node
	if n.TypeWord == nil || n.TypeWord.Op() == ir.OADDR && n.TypeWord.(*ir.AddrExpr).X.Op() == ir.OLINKSYMOFFSET {
		// Fixed (not loaded from a dictionary) type.
		ta := ir.NewTypeAssertExpr(gd, gd.Pos, c, toType)
		ta.SetOp(ir.ODOTTYPE2)
		// Allocate a descriptor for this conversion to pass to the runtime.
		ta.Descriptor = makeTypeAssertDescriptor(gd, toType, true)
		rhs = ta
	} else {
		ta := ir.NewDynamicTypeAssertExpr(gd, gd.Pos, ir.ODYNAMICDOTTYPE2, c, n.TypeWord)
		rhs = ta
	}
	rhs.SetType(toType)
	rhs.SetTypecheck(1)

	res := typecheck.TempAt(gd, gd.Pos, ir.CurFunc(gd), toType)
	as := ir.NewAssignListStmt(gd, gd.Pos, ir.OAS2DOTTYPE, []ir.Node{res, ir.BlankNode}, []ir.Node{rhs})
	init.Append(as)
	return res
}

// Returns the data word (the second word) used to represent conv.X in
// an interface.
func dataWord(gd *base.Invocation, conv *ir.ConvExpr, init *ir.Nodes) ir.Node {
	pos, n := conv.Pos(), conv.X
	fromType := n.Type()

	// If it's a pointer, it is its own representation.
	if types.IsDirectIface(fromType) {
		return n
	}

	isInteger := fromType.IsInteger()
	isBool := fromType.IsBoolean()
	if sc := fromType.SoleComponent(); sc != nil {
		isInteger = sc.IsInteger()
		isBool = sc.IsBoolean()
	}

	diagnose := func(msg string, n ir.Node) {
		if gd.Debug.EscapeDebug > 0 {
			// This output is most useful with -gcflags=-W=2 or similar because
			// it often prints a temp variable name.
			gd.WarnfAt(n.Pos(), "convert: %s: %v", msg, n)
		}
	}

	// Try a bunch of cases to avoid an allocation.
	var value ir.Node
	switch {
	case fromType.Size() == 0:
		// n is zero-sized. Use zerobase.
		diagnose("using global for zero-sized interface value", n)
		cheapExpr(gd, n, init) // Evaluate n for side-effects. See issue 19246.
		value = ir.NewLinksymExpr(gd, gd.Pos, ir.Syms(gd).Zerobase, types.Types[types.TUINTPTR])
	case isBool || fromType.Size() == 1 && isInteger:
		// n is a bool/byte. Use staticuint64s[n * 8] on little-endian
		// and staticuint64s[n * 8 + 7] on big-endian.
		diagnose("using global for single-byte interface value", n)
		n = cheapExpr(gd, n, init)
		n = soleComponent(gd, init, n)
		// byteindex widens n so that the multiplication doesn't overflow.
		index := ir.NewBinaryExpr(gd, gd.Pos, ir.OLSH, byteindex(gd, n), ir.NewInt(gd, gd.Pos, 3))
		if ssagen.Arch.LinkArch.ByteOrder == binary.BigEndian {
			index = ir.NewBinaryExpr(gd, gd.Pos, ir.OADD, index, ir.NewInt(gd, gd.Pos, 7))
		}
		// The actual type is [256]uint64, but we use [256*8]uint8 so we can address
		// individual bytes.
		staticuint64s := ir.NewLinksymExpr(gd, gd.Pos, ir.Syms(gd).Staticuint64s, types.NewArray(types.Types[types.TUINT8], 256*8))
		xe := ir.NewIndexExpr(gd, gd.Pos, staticuint64s, index)
		xe.SetBounded(true)
		value = xe
	case n.Op() == ir.OLINKSYMOFFSET && n.(*ir.LinksymOffsetExpr).Linksym == ir.Syms(gd).ZeroVal && n.(*ir.LinksymOffsetExpr).Offset_ == 0:
		// n is using zeroVal, so we can use n directly.
		// (Note that n does not have a proper pos in this case, so using conv for the diagnostic instead.)
		diagnose("using global for zero value interface value", conv)
		value = n
	case n.Op() == ir.ONAME && n.(*ir.Name).Class == ir.PEXTERN && n.(*ir.Name).Readonly():
		// n is a readonly global; use it directly.
		diagnose("using global for interface value", n)
		value = n
	case ir.NodeStackAllocatable(conv) && fromType.Size() <= 1024:
		// n does not escape. Use a stack temporary initialized to n.
		diagnose("using stack temporary for interface value", n)
		value = typecheck.TempAt(gd, gd.Pos, ir.CurFunc(gd), fromType)
		init.Append(typecheck.Stmt(gd, ir.NewAssignStmt(gd, gd.Pos, value, n)))
	}
	if value != nil {
		// The interface data word is &value.
		return typecheck.Expr(gd, typecheck.NodAddr(gd, value))
	}

	// Time to do an allocation. We'll call into the runtime for that.
	fnname, argType, needsaddr := dataWordFuncName(gd, fromType)
	var fn *ir.Name

	var args []ir.Node
	if needsaddr {
		// Types of large or unknown size are passed by reference.
		// Orderexpr arranged for n to be a temporary for all
		// the conversions it could see. Comparison of an interface
		// with a non-interface, especially in a switch on interface value
		// with non-interface cases, is not visible to order.stmt, so we
		// have to fall back on allocating a temp here.
		if !ir.IsAddressable(n) {
			n = copyExpr(gd, n, fromType, init)
		}
		fn = typecheck.LookupRuntime(gd, fnname, fromType)
		args = []ir.Node{reflectdata.ConvIfaceSrcRType(gd, gd.Pos, conv), typecheck.NodAddr(gd, n)}
	} else {
		// Use a specialized conversion routine that takes the type being
		// converted by value, not by pointer.
		fn = typecheck.LookupRuntime(gd, fnname)
		var arg ir.Node
		switch {
		case fromType == argType:
			// already in the right type, nothing to do
			arg = n
		case fromType.Kind() == argType.Kind(),
			fromType.IsPtrShaped() && argType.IsPtrShaped():
			// can directly convert (e.g. named type to underlying type, or one pointer to another)
			// TODO: never happens because pointers are directIface?
			arg = ir.NewConvExpr(gd, pos, ir.OCONVNOP, argType, n)
		case fromType.IsInteger() && argType.IsInteger():
			// can directly convert (e.g. int32 to uint32)
			arg = ir.NewConvExpr(gd, pos, ir.OCONV, argType, n)
		default:
			// unsafe cast through memory
			arg = copyExpr(gd, n, fromType, init)
			var addr ir.Node = typecheck.NodAddr(gd, arg)
			addr = ir.NewConvExpr(gd, pos, ir.OCONVNOP, argType.PtrTo(), addr)
			arg = ir.NewStarExpr(gd, pos, addr)
			arg.SetType(argType)
		}
		args = []ir.Node{arg}
	}
	call := ir.NewCallExpr(gd, gd.Pos, ir.OCALL, fn, nil)
	call.Args = args
	return safeExpr(gd, walkExpr(gd, typecheck.Expr(gd, call), init), init)
}

// walkBytesRunesToString walks an OBYTES2STR or ORUNES2STR node.
func walkBytesRunesToString(gd *base.Invocation, n *ir.ConvExpr, init *ir.Nodes) ir.Node {
	a := typecheck.NodNil(gd)
	if ir.NodeStackAllocatable(n) {
		// Create temporary buffer for string on stack.
		a = stackBufAddr(gd, tmpstringbufsize, types.Types[types.TUINT8])
	}
	if n.Op() == ir.ORUNES2STR {
		// slicerunetostring(*[32]byte, []rune) string
		return mkcall(gd, "slicerunetostring", n.Type(), init, a, n.X)
	}
	// slicebytetostring(*[32]byte, ptr *byte, n int) string
	n.X = cheapExpr(gd, n.X, init)
	ptr, len := backingArrayPtrLen(gd, n.X)
	return mkcall(gd, "slicebytetostring", n.Type(), init, a, ptr, len)
}

// walkBytesToStringTemp walks an OBYTES2STRTMP node.
func walkBytesToStringTemp(gd *base.Invocation, n *ir.ConvExpr, init *ir.Nodes) ir.Node {
	n.X = walkExpr(gd, n.X, init)
	if !gd.Flag.Cfg.Instrumenting {
		// Let the backend handle OBYTES2STRTMP directly
		// to avoid a function call to slicebytetostringtmp.
		return n
	}
	// slicebytetostringtmp(ptr *byte, n int) string
	n.X = cheapExpr(gd, n.X, init)
	ptr, len := backingArrayPtrLen(gd, n.X)
	return mkcall(gd, "slicebytetostringtmp", n.Type(), init, ptr, len)
}

// walkRuneToString walks an ORUNESTR node.
func walkRuneToString(gd *base.Invocation, n *ir.ConvExpr, init *ir.Nodes) ir.Node {
	a := typecheck.NodNil(gd)
	if ir.NodeStackAllocatable(n) {
		a = stackBufAddr(gd, 4, types.Types[types.TUINT8])
	}
	// intstring(*[4]byte, rune)
	return mkcall(gd, "intstring", n.Type(), init, a, typecheck.Conv(gd, n.X, types.Types[types.TINT64]))
}

// walkStringToBytes walks an OSTR2BYTES node.
func walkStringToBytes(gd *base.Invocation, n *ir.ConvExpr, init *ir.Nodes) ir.Node {
	s := n.X

	if expr, ok := s.(*ir.AddStringExpr); ok {
		return walkAddString(gd, expr, init, n)
	}

	if ir.IsConst(s, constant.String) {
		sc := ir.StringVal(s)

		// Allocate a [n]byte of the right size.
		t := types.NewArray(types.Types[types.TUINT8], int64(len(sc)))
		var a ir.Node
		if ir.NodeStackAllocatable(n) && len(sc) <= int(ir.MaxImplicitStackVarSize) {
			a = stackBufAddr(gd, t.NumElem(), t.Elem())
		} else {
			types.CalcSize(gd, t)
			a = ir.NewUnaryExpr(gd, gd.Pos, ir.ONEW, nil)
			a.SetType(types.NewPtr(t))
			a.SetTypecheck(1)
			a.MarkNonNil()
		}
		p := typecheck.TempAt(gd, gd.Pos, ir.CurFunc(gd), t.PtrTo()) // *[n]byte
		init.Append(typecheck.Stmt(gd, ir.NewAssignStmt(gd, gd.Pos, p, a)))

		// Copy from the static string data to the [n]byte.
		if len(sc) > 0 {
			sptr := ir.NewUnaryExpr(gd, gd.Pos, ir.OSPTR, s)
			sptr.SetBounded(true)
			as := ir.NewAssignStmt(gd, gd.Pos, ir.NewStarExpr(gd, gd.Pos, p), ir.NewStarExpr(gd, gd.Pos, typecheck.ConvNop(gd, sptr, t.PtrTo())))
			appendWalkStmt(gd, init, as)
		}

		// Slice the [n]byte to a []byte.
		slice := ir.NewSliceExpr(gd, n.Pos(), ir.OSLICEARR, p, nil, nil, nil)
		slice.SetType(n.Type())
		slice.SetTypecheck(1)
		return walkExpr(gd, slice, init)
	}

	a := typecheck.NodNil(gd)
	if ir.NodeStackAllocatable(n) {
		// Create temporary buffer for slice on stack.
		a = stackBufAddr(gd, tmpstringbufsize, types.Types[types.TUINT8])
	}
	// stringtoslicebyte(*32[byte], string) []byte
	return mkcall(gd, "stringtoslicebyte", n.Type(), init, a, typecheck.Conv(gd, s, types.Types[types.TSTRING]))
}

// walkStringToBytesTemp walks an OSTR2BYTESTMP node.
func walkStringToBytesTemp(gd *base.Invocation, n *ir.ConvExpr, init *ir.Nodes) ir.Node {
	// []byte(string) conversion that creates a slice
	// referring to the actual string bytes.
	// This conversion is handled later by the backend and
	// is only for use by internal compiler optimizations
	// that know that the slice won't be mutated.
	// The only such case today is:
	// for i, c := range []byte(string)
	n.X = walkExpr(gd, n.X, init)
	return n
}

// walkStringToRunes walks an OSTR2RUNES node.
func walkStringToRunes(gd *base.Invocation, n *ir.ConvExpr, init *ir.Nodes) ir.Node {
	a := typecheck.NodNil(gd)
	if ir.NodeStackAllocatable(n) {
		// Create temporary buffer for slice on stack.
		a = stackBufAddr(gd, tmpstringbufsize, types.Types[types.TINT32])
	}
	// stringtoslicerune(*[32]rune, string) []rune
	return mkcall(gd, "stringtoslicerune", n.Type(), init, a, typecheck.Conv(gd, n.X, types.Types[types.TSTRING]))
}

// dataWordFuncName returns the name of the function used to convert a value of type "from"
// to the data word of an interface.
// argType is the type the argument needs to be coerced to.
// needsaddr reports whether the value should be passed (needaddr==false) or its address (needsaddr==true).
func dataWordFuncName(gd *base.Invocation, from *types.Type) (fnname string, argType *types.Type, needsaddr bool) {
	if from.IsInterface() {
		gd.Fatalf("can only handle non-interfaces")
	}
	switch {
	case from.Size() == 2 && uint8(from.Alignment()) == 2:
		return "convT16", types.Types[types.TUINT16], false
	case from.Size() == 4 && uint8(from.Alignment()) == 4 && !from.HasPointers():
		return "convT32", types.Types[types.TUINT32], false
	case from.Size() == 8 && uint8(from.Alignment()) == uint8(types.Types[types.TUINT64].Alignment()) && !from.HasPointers():
		return "convT64", types.Types[types.TUINT64], false
	}
	if sc := from.SoleComponent(); sc != nil {
		switch {
		case sc.IsString():
			return "convTstring", types.Types[types.TSTRING], false
		case sc.IsSlice():
			return "convTslice", types.NewSlice(types.Types[types.TUINT8]), false // the element type doesn't matter
		}
	}

	if from.HasPointers() {
		return "convT", types.Types[types.TUNSAFEPTR], true
	}
	return "convTnoptr", types.Types[types.TUNSAFEPTR], true
}

// rtconvfn returns the parameter and result types that will be used by a
// runtime function to convert from type src to type dst. The runtime function
// name can be derived from the names of the returned types.
//
// If no such function is necessary, it returns (Txxx, Txxx).
func rtconvfn(src, dst *types.Type) (param, result types.Kind) {
	if ssagen.Arch.SoftFloat {
		return types.Txxx, types.Txxx
	}

	switch ssagen.Arch.LinkArch.Family {
	case sys.ARM, sys.MIPS:
		if src.IsFloat() {
			switch dst.Kind() {
			case types.TINT64, types.TUINT64:
				return types.TFLOAT64, dst.Kind()
			}
		}
		if dst.IsFloat() {
			switch src.Kind() {
			case types.TINT64, types.TUINT64:
				return src.Kind(), dst.Kind()
			}
		}

	case sys.I386:
		if src.IsFloat() {
			switch dst.Kind() {
			case types.TINT64, types.TUINT64:
				return types.TFLOAT64, dst.Kind()
			case types.TUINT32, types.TUINT, types.TUINTPTR:
				return types.TFLOAT64, types.TUINT32
			}
		}
		if dst.IsFloat() {
			switch src.Kind() {
			case types.TINT64, types.TUINT64:
				return src.Kind(), dst.Kind()
			case types.TUINT32, types.TUINT, types.TUINTPTR:
				return types.TUINT32, types.TFLOAT64
			}
		}
	}
	return types.Txxx, types.Txxx
}

func soleComponent(gd *base.Invocation, init *ir.Nodes, n ir.Node) ir.Node {
	if n.Type().SoleComponent() == nil {
		return n
	}
	// Keep in sync with cmd/compile/internal/types/type.go:Type.SoleComponent.
	for {
		switch {
		case n.Type().IsStruct():
			if n.Type().Field(0).Sym.IsBlank() {
				// Treat blank fields as the zero value as the Go language requires.
				n = typecheck.TempAt(gd, gd.Pos, ir.CurFunc(gd), n.Type().Field(0).Type)
				appendWalkStmt(gd, init, ir.NewAssignStmt(gd, gd.Pos, n, nil))
				continue
			}
			n = typecheck.DotField(gd, n.Pos(), n, 0)
		case n.Type().IsArray():
			n = typecheck.Expr(gd, ir.NewIndexExpr(gd, n.Pos(), n, ir.NewInt(gd, gd.Pos, 0)))
		default:
			return n
		}
	}
}

// byteindex converts n, which is byte-sized, to an int used to index into an array.
// We cannot use conv, because we allow converting bool to int here,
// which is forbidden in user code.
func byteindex(gd *base.Invocation, n ir.Node) ir.Node {
	// We cannot convert from bool to int directly.
	// While converting from int8 to int is possible, it would yield
	// the wrong result for negative values.
	// Reinterpreting the value as an unsigned byte solves both cases.
	if !types.Identical(n.Type(), types.Types[types.TUINT8]) {
		n = ir.NewConvExpr(gd, gd.Pos, ir.OCONV, nil, n)
		n.SetType(types.Types[types.TUINT8])
		n.SetTypecheck(1)
	}
	n = ir.NewConvExpr(gd, gd.Pos, ir.OCONV, nil, n)
	n.SetType(types.Types[types.TINT])
	n.SetTypecheck(1)
	return n
}

func walkCheckPtrArithmetic(gd *base.Invocation, n *ir.ConvExpr, init *ir.Nodes) ir.Node {
	// Calling cheapExpr(gd, n, init) below leads to a recursive call to
	// walkExpr, which leads us back here again. Use n.Checkptr to
	// prevent infinite loops.
	if n.CheckPtr() {
		return n
	}
	n.SetCheckPtr(true)
	defer n.SetCheckPtr(false)

	// TODO(mdempsky): Make stricter. We only need to exempt
	// reflect.Value.Pointer and reflect.Value.UnsafeAddr.
	switch n.X.Op() {
	case ir.OCALLMETH:
		gd.FatalfAt(n.X.Pos(), "OCALLMETH missed by typecheck")
	case ir.OCALLFUNC, ir.OCALLINTER:
		return n
	}

	if n.X.Op() == ir.ODOTPTR && ir.IsReflectHeaderDataField(n.X) {
		return n
	}

	// Find original unsafe.Pointer operands involved in this
	// arithmetic expression.
	//
	// "It is valid both to add and to subtract offsets from a
	// pointer in this way. It is also valid to use &^ to round
	// pointers, usually for alignment."
	var originals []ir.Node
	var walk func(n ir.Node)
	walk = func(n ir.Node) {
		switch n.Op() {
		case ir.OADD:
			n := n.(*ir.BinaryExpr)
			walk(n.X)
			walk(n.Y)
		case ir.OSUB, ir.OANDNOT:
			n := n.(*ir.BinaryExpr)
			walk(n.X)
		case ir.OCONVNOP:
			n := n.(*ir.ConvExpr)
			if n.X.Type().IsUnsafePtr() {
				n.X = cheapExpr(gd, n.X, init)
				originals = append(originals, typecheck.ConvNop(gd, n.X, types.Types[types.TUNSAFEPTR]))
			}
		}
	}
	walk(n.X)

	cheap := cheapExpr(gd, n, init)

	slice := typecheck.MakeDotArgs(gd, gd.Pos, types.NewSlice(types.Types[types.TUNSAFEPTR]), originals)
	slice.SetEsc(ir.EscNone)

	init.Append(mkcall(gd, "checkptrArithmetic", nil, init, typecheck.ConvNop(gd, cheap, types.Types[types.TUNSAFEPTR]), slice))
	// TODO(khr): Mark backing store of slice as dead. This will allow us to reuse
	// the backing store for multiple calls to checkptrArithmetic.

	return cheap
}

// walkSliceToArray walks an OSLICE2ARR expression.
func walkSliceToArray(gd *base.Invocation, n *ir.ConvExpr, init *ir.Nodes) ir.Node {
	// Replace T(x) with *(*T)(x).
	conv := typecheck.Expr(gd, ir.NewConvExpr(gd, gd.Pos, ir.OCONV, types.NewPtr(n.Type()), n.X)).(*ir.ConvExpr)
	deref := typecheck.Expr(gd, ir.NewStarExpr(gd, gd.Pos, conv)).(*ir.StarExpr)

	// The OSLICE2ARRPTR conversion handles checking the slice length,
	// so the dereference can't fail.
	//
	// However, this is more than just an optimization: if T is a
	// zero-length array, then x (and thus (*T)(x)) can be nil, but T(x)
	// should *not* panic. So suppressing the nil check here is
	// necessary for correctness in that case.
	deref.SetBounded(true)

	return walkExpr(gd, deref, init)
}
