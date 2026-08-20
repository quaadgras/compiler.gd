// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package walk

import (
	"fmt"
	"internal/abi"

	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/rttype"
	"cmd/compile/internal/ssagen"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
	"cmd/internal/src"
)

// The constant is known to runtime.
const tmpstringbufsize = 32

func Walk(gd *base.Invocation, fn *ir.Func) {
	gd.CurFunc = fn

	// Build pre-walk analysis caches with a single AST traversal and
	// hang them off the Invocation for the duration of this fn.
	// (At some point, it might be worthwhile to have a walkState structure
	// that gets passed everywhere where things like this can go.)
	gd.WalkStaticValues = analyzePreWalk(fn)
	defer func() { gd.WalkStaticValues = nil }()

	errorsBefore := gd.Errors()
	order(gd, fn)
	if gd.Errors() > errorsBefore {
		return
	}

	// gd escape-bits: promote EscCandidate PAUTOs to PAUTOHEAP with
	// a stack-initial backing slot so the wrap at indirect call sites
	// can re-home the backing to heap on demand. Must run before
	// walkStmtList so SSA gen sees Heapaddr wired up.
	promoteEscapeCandidates(gd, fn)

	// gd Phase G: recognize return-of-allocation shapes and
	// retag the underlying local with an outBuf result index so
	// ssagen routes its heap allocation through runtime.maybeInPlace.
	// Must run before walkStmtList lowers ONEW/OPTRLIT and before
	// the body is otherwise transformed.
	phaseGReturnRewrite(gd, fn)

	// gd Phase G: at each call site to a Phase-G-extended callee,
	// replace trailing nil outBuf args with the address of a fresh
	// stack-allocated buffer when (a) the corresponding result's
	// destination doesn't escape the caller's frame, AND (b) the
	// callee's result k is a fresh allocation per escape tags (no
	// input aliases result k). Pairs with the callee-side rewrite
	// above to deliver the zero-alloc path end-to-end.
	phaseGCallSiteRewrite(gd, fn)

	if gd.Flag.W != 0 {
		s := fmt.Sprintf("\nbefore walk %v", ir.CurFunc(gd).Sym())
		ir.DumpList(gd, s, ir.CurFunc(gd).Body)
	}

	walkStmtList(gd, ir.CurFunc(gd).Body)
	if gd.Flag.W != 0 {
		s := fmt.Sprintf("after walk %v", ir.CurFunc(gd).Sym())
		ir.DumpList(gd, s, ir.CurFunc(gd).Body)
	}

	// Eagerly compute sizes of all variables for SSA.
	for _, n := range fn.Dcl {
		types.CalcSize(gd, n.Type())
	}
}

// walkRecv walks an ORECV node.
func walkRecv(gd *base.Invocation, n *ir.UnaryExpr) ir.Node {
	if n.Typecheck() == 0 {
		gd.Fatalf("missing typecheck: %+v", n)
	}
	init := ir.TakeInit(n)

	n.X = walkExpr(gd, n.X, &init)
	call := walkExpr(gd, mkcall1(gd, chanfn(gd, "chanrecv1", 2, n.X.Type()), nil, &init, n.X, typecheck.NodNil(gd)), &init)
	return ir.InitExpr(gd, init, call)
}

func convas(gd *base.Invocation, n *ir.AssignStmt, init *ir.Nodes) *ir.AssignStmt {
	if n.Op() != ir.OAS {
		gd.Fatalf("convas: not OAS %v", n.Op())
	}
	n.SetTypecheck(1)

	if n.X == nil || n.Y == nil {
		return n
	}

	lt := n.X.Type()
	rt := n.Y.Type()
	if lt == nil || rt == nil {
		return n
	}

	if ir.IsBlank(n.X) {
		n.Y = typecheck.DefaultLit(gd, n.Y, nil)
		return n
	}

	if !types.Identical(lt, rt) {
		n.Y = typecheck.AssignConv(gd, n.Y, lt, "assignment")
		n.Y = walkExpr(gd, n.Y, init)
	}
	types.CalcSize(gd, n.Y.Type())

	return n
}

func vmkcall(gd *base.Invocation, fn ir.Node, t *types.Type, init *ir.Nodes, va []ir.Node) *ir.CallExpr {
	if init == nil {
		gd.Fatalf("mkcall with nil init: %v", fn)
	}
	if fn.Type() == nil || fn.Type().Kind() != types.TFUNC {
		gd.Fatalf("mkcall %v %v", fn, fn.Type())
	}

	// gd Phase G (universal extension): the callee's sig may have
	// trailing outBufK unsafe.Pointer params appended by
	// NewSignature. Walk-level mk*call helpers only receive the
	// user args, so pad va with nil unsafe.Pointer per synthesised
	// outBuf. typecheck.Call → FillOutBufArgs handles the typical
	// path but matches by user-visible arity; mkcall can't rely on
	// that because the runtime sigs in builtin.go are already
	// extended.
	if nOut := fn.Type().NumOutBufs(); nOut > 0 {
		unsafePtr := types.Types[types.TUNSAFEPTR]
		for i := 0; i < nOut; i++ {
			nilArg := ir.NewNilExpr(gd, gd.Pos, unsafePtr)
			nilArg.SetTypecheck(1)
			va = append(va, nilArg)
		}
	}

	n := fn.Type().NumParams()
	if n != len(va) {
		gd.Fatalf("vmkcall %v needs %v args got %v", fn, n, len(va))
	}

	call := typecheck.Call(gd, gd.Pos, fn, va, false).(*ir.CallExpr)
	call.SetType(t)
	return walkExpr(gd, call, init).(*ir.CallExpr)
}

func mkcall(gd *base.Invocation, name string, t *types.Type, init *ir.Nodes, args ...ir.Node) *ir.CallExpr {
	return vmkcall(gd, typecheck.LookupRuntime(gd, name), t, init, args)
}

func mkcallstmt(gd *base.Invocation, name string, args ...ir.Node) ir.Node {
	return mkcallstmt1(gd, typecheck.LookupRuntime(gd, name), args...)
}

func mkcall1(gd *base.Invocation, fn ir.Node, t *types.Type, init *ir.Nodes, args ...ir.Node) *ir.CallExpr {
	return vmkcall(gd, fn, t, init, args)
}

func mkcallstmt1(gd *base.Invocation, fn ir.Node, args ...ir.Node) ir.Node {
	var init ir.Nodes
	n := vmkcall(gd, fn, nil, &init, args)
	if len(init) == 0 {
		return n
	}
	init.Append(n)
	return ir.NewBlockStmt(gd, n.Pos(), init)
}

func chanfn(gd *base.Invocation, name string, n int, t *types.Type) ir.Node {
	if !t.IsChan() {
		gd.Fatalf("chanfn %v", t)
	}
	switch n {
	case 1:
		return typecheck.LookupRuntime(gd, name, t.Elem())
	case 2:
		return typecheck.LookupRuntime(gd, name, t.Elem(), t.Elem())
	}
	gd.Fatalf("chanfn %d", n)
	return nil
}

func mapfn(gd *base.Invocation, name string, t *types.Type, isfat bool) ir.Node {
	if !t.IsMap() {
		gd.Fatalf("mapfn %v", t)
	}
	if mapfast(gd, t) == mapslow || isfat {
		return typecheck.LookupRuntime(gd, name, t.Key(), t.Elem(), t.Key(), t.Elem())
	}
	return typecheck.LookupRuntime(gd, name, t.Key(), t.Elem(), t.Elem())
}

func mapfndel(gd *base.Invocation, name string, t *types.Type) ir.Node {
	if !t.IsMap() {
		gd.Fatalf("mapfn %v", t)
	}
	if mapfast(gd, t) == mapslow {
		return typecheck.LookupRuntime(gd, name, t.Key(), t.Elem(), t.Key())
	}
	return typecheck.LookupRuntime(gd, name, t.Key(), t.Elem())
}

const (
	mapslow = iota
	mapfast32
	mapfast32ptr
	mapfast64
	mapfast64ptr
	mapfaststr
	nmapfast
)

type mapnames [nmapfast]string

func mkmapnames(base string, ptr string) mapnames {
	return mapnames{base, base + "_fast32", base + "_fast32" + ptr, base + "_fast64", base + "_fast64" + ptr, base + "_faststr"}
}

var mapaccess1 = mkmapnames("mapaccess1", "")
var mapaccess2 = mkmapnames("mapaccess2", "")
var mapassign = mkmapnames("mapassign", "ptr")
var mapdelete = mkmapnames("mapdelete", "")

func mapfast(gd *base.Invocation, t *types.Type) int {
	if t.Elem().Size() > abi.MapMaxElemBytes {
		return mapslow
	}
	switch algType(gd, t.Key()) {
	case types.AMEM32:
		if !t.Key().HasPointers() {
			return mapfast32
		}
		if types.PtrSize == 4 {
			return mapfast32ptr
		}
		gd.Fatalf("small pointer %v", t.Key())
	case types.AMEM64:
		if !t.Key().HasPointers() {
			return mapfast64
		}
		if types.PtrSize == 8 {
			return mapfast64ptr
		}
		// Two-word object, at least one of which is a pointer.
		// Use the slow path.
	case types.ASTRING:
		return mapfaststr
	}
	return mapslow
}

// algType returns the fixed-width AMEMxx variants instead of the general
// AMEM kind when possible.
func algType(gd *base.Invocation, t *types.Type) types.AlgKind {
	a := types.AlgType(gd, t)
	if a == types.AMEM {
		if t.Alignment() < int64(gd.Ctxt.Arch.Alignment) && t.Alignment() < t.Size() {
			// For example, we can't treat [2]int16 as an int32 if int32s require
			// 4-byte alignment. See issue 46283.
			return a
		}
		switch t.Size() {
		case 0:
			return types.AMEM0
		case 1:
			return types.AMEM8
		case 2:
			return types.AMEM16
		case 4:
			return types.AMEM32
		case 8:
			return types.AMEM64
		case 16:
			return types.AMEM128
		}
	}

	return a
}

func walkAppendArgs(gd *base.Invocation, n *ir.CallExpr, init *ir.Nodes) {
	walkExprListSafe(gd, n.Args, init)

	// walkExprListSafe will leave OINDEX (s[n]) alone if both s
	// and n are name or literal, but those may index the slice we're
	// modifying here. Fix explicitly.
	ls := n.Args
	for i1, n1 := range ls {
		ls[i1] = cheapExpr(gd, n1, init)
	}
}

// appendWalkStmt typechecks and walks stmt and then appends it to init.
func appendWalkStmt(gd *base.Invocation, init *ir.Nodes, stmt ir.Node) {
	op := stmt.Op()
	n := typecheck.Stmt(gd, stmt)
	if op == ir.OAS || op == ir.OAS2 {
		// If the assignment has side effects, walkExpr will append them
		// directly to init for us, while walkStmt will wrap it in an OBLOCK.
		// We need to append them directly.
		// TODO(rsc): Clean this up.
		n = walkExpr(gd, n, init)
	} else {
		n = walkStmt(gd, n)
	}
	init.Append(n)
}

// The max number of defers in a function using open-coded defers. We enforce this
// limit because the deferBits bitmask is currently a single byte (to minimize code size)
const maxOpenDefers = 8

// backingArrayPtrLen extracts the pointer and length from a slice or string.
// This constructs two nodes referring to n, so n must be a cheapExpr.
func backingArrayPtrLen(gd *base.Invocation, n ir.Node) (ptr, length ir.Node) {
	var init ir.Nodes
	c := cheapExpr(gd, n, &init)
	if c != n || len(init) != 0 {
		gd.Fatalf("backingArrayPtrLen not cheap: %v", n)
	}
	ptr = ir.NewUnaryExpr(gd, gd.Pos, ir.OSPTR, n)
	if n.Type().IsString() {
		ptr.SetType(types.Types[types.TUINT8].PtrTo())
	} else {
		ptr.SetType(n.Type().Elem().PtrTo())
	}
	ptr.SetTypecheck(1)
	length = ir.NewUnaryExpr(gd, gd.Pos, ir.OLEN, n)
	length.SetType(types.Types[types.TINT])
	length.SetTypecheck(1)
	return ptr, length
}

// mayCall reports whether evaluating expression n may require
// function calls, which could clobber function call arguments/results
// currently on the stack.
func mayCall(gd *base.Invocation, n ir.Node) bool {
	// This is intended to avoid putting constants
	// into temporaries with the race detector (or other
	// instrumentation) which interferes with simple
	// "this is a constant" tests in ssagen.
	// Also, it will generally lead to better code.
	if n.Op() == ir.OLITERAL {
		return false
	}

	// When instrumenting, any expression might require function calls.
	if gd.Flag.Cfg.Instrumenting {
		return true
	}

	isSoftFloat := func(typ *types.Type) bool {
		return types.IsFloat[typ.Kind()] || types.IsComplex[typ.Kind()]
	}

	return ir.Any(n, func(n ir.Node) bool {
		// walk should have already moved any Init blocks off of
		// expressions.
		if len(n.Init()) != 0 {
			gd.FatalfAt(n.Pos(), "mayCall %+v", n)
		}

		switch n.Op() {
		default:
			gd.FatalfAt(n.Pos(), "mayCall %+v", n)

		case ir.OCALLFUNC, ir.OCALLINTER,
			ir.OUNSAFEADD, ir.OUNSAFESLICE:
			return true

		case ir.OINDEX, ir.OSLICE, ir.OSLICEARR, ir.OSLICE3, ir.OSLICE3ARR, ir.OSLICESTR,
			ir.ODEREF, ir.ODOTPTR, ir.ODOTTYPE, ir.ODYNAMICDOTTYPE, ir.ODIV, ir.OMOD,
			ir.OSLICE2ARR, ir.OSLICE2ARRPTR:
			// These ops might panic, make sure they are done
			// before we start marshaling args for a call. See issue 16760.
			return true

		case ir.OANDAND, ir.OOROR:
			n := n.(*ir.LogicalExpr)
			// The RHS expression may have init statements that
			// should only execute conditionally, and so cannot be
			// pulled out to the top-level init list. We could try
			// to be more precise here.
			return len(n.Y.Init()) != 0

		// When using soft-float, these ops might be rewritten to function calls
		// so we ensure they are evaluated first.
		case ir.OADD, ir.OSUB, ir.OMUL, ir.ONEG:
			return ssagen.Arch.SoftFloat && isSoftFloat(n.Type())
		case ir.OLT, ir.OEQ, ir.ONE, ir.OLE, ir.OGE, ir.OGT:
			n := n.(*ir.BinaryExpr)
			return ssagen.Arch.SoftFloat && isSoftFloat(n.X.Type())
		case ir.OCONV:
			n := n.(*ir.ConvExpr)
			return ssagen.Arch.SoftFloat && (isSoftFloat(n.Type()) || isSoftFloat(n.X.Type()))

		case ir.OMIN, ir.OMAX:
			// string or float requires runtime call, see (*ssagen.state).minmax method.
			return n.Type().IsString() || n.Type().IsFloat()

		case ir.OLITERAL, ir.ONIL, ir.ONAME, ir.OLINKSYMOFFSET, ir.OMETHEXPR,
			ir.OAND, ir.OANDNOT, ir.OLSH, ir.OOR, ir.ORSH, ir.OXOR, ir.OCOMPLEX, ir.OMAKEFACE,
			ir.OADDR, ir.OBITNOT, ir.ONOT, ir.OPLUS,
			ir.OCAP, ir.OIMAG, ir.OLEN, ir.OREAL,
			ir.OCONVNOP, ir.ODOT,
			ir.OCFUNC, ir.OIDATA, ir.OITAB, ir.OSPTR,
			ir.OBYTES2STRTMP, ir.OGETG, ir.OGETCALLERSP, ir.OSLICEHEADER, ir.OSTRINGHEADER:
			// ok: operations that don't require function calls.
			// Expand as needed.
		}

		return false
	})
}

// itabType loads the _type field from a runtime.itab struct. The
// package-level cache was removed because runtimeField now allocates
// a Sym from the per-Invocation runtime Pkg; the recomputation is
// cheap and avoids first-Invocation Pkg-pointer pinning.
func itabType(gd *base.Invocation, itab ir.Node) ir.Node {
	itabTypeField := runtimeField(gd, "Type", rttype.ITab.OffsetOf("Type"), types.NewPtr(types.Types[types.TUINT8]))
	return boundedDotPtr(gd, gd.Pos, itab, itabTypeField)
}

// boundedDotPtr returns a selector expression representing ptr.field
// and omits nil-pointer checks for ptr.
func boundedDotPtr(gd *base.Invocation, pos src.XPos, ptr ir.Node, field *types.Field) *ir.SelectorExpr {
	sel := ir.NewSelectorExpr(gd, pos, ir.ODOTPTR, ptr, field.Sym)
	sel.Selection = field
	sel.SetType(field.Type)
	sel.SetTypecheck(1)
	sel.SetBounded(true) // guaranteed not to fault
	return sel
}

func runtimeField(gd *base.Invocation, name string, offset int64, typ *types.Type) *types.Field {
	f := types.NewField(src.NoXPos, ir.Pkgs(gd).Runtime.Lookup(name), typ)
	f.Offset = offset
	return f
}

// ifaceData loads the data field from an interface.
// The concrete type must be known to have type t.
// It follows the pointer if !IsDirectIface(t).
func ifaceData(gd *base.Invocation, pos src.XPos, n ir.Node, t *types.Type) ir.Node {
	if t.IsInterface() {
		gd.Fatalf("ifaceData interface: %v", t)
	}
	ptr := ir.NewUnaryExpr(gd, pos, ir.OIDATA, n)
	if types.IsDirectIface(t) {
		ptr.SetType(t)
		ptr.SetTypecheck(1)
		return ptr
	}
	ptr.SetType(types.NewPtr(t))
	ptr.SetTypecheck(1)
	ind := ir.NewStarExpr(gd, pos, ptr)
	ind.SetType(t)
	ind.SetTypecheck(1)
	ind.SetBounded(true)
	return ind
}

// staticValue returns the earliest expression it can find that always
// evaluates to n, with similar semantics to [ir.StaticValue].
//
// It only returns results for the ir.CurFunc(gd) being processed in [Walk],
// including its closures, and uses a cache to reduce duplicative work.
// It can return n or nil if it does not find an earlier expression.
//
// The current use case is reducing OCONVIFACE allocations, and hence
// staticValue is currently only useful when given an *ir.ConvExpr.X as n.
func staticValue(gd *base.Invocation, n ir.Node) ir.Node {
	wa := curWalkAnalysis(gd)
	if wa == nil {
		gd.Fatalf("WalkStaticValues is nil. staticValue called outside of walk.Walk?")
	}
	return wa.staticValues[n]
}

// walkAnalysis holds the pre-walk analysis caches for the function
// currently being walked. It is stored in gd.WalkStaticValues so that
// concurrent in-process compile invocations don't share it.
type walkAnalysis struct {
	// staticValues is a cache of static values for use by staticValue.
	staticValues map[ir.Node]ir.Node

	// shapeConvSources maps an *ir.Name (a PAUTO interface variable) to
	// the shape type of the OCONVIFACE expression that is its single
	// static value, if any.
	shapeConvSources map[*ir.Name]*types.Type
}

// curWalkAnalysis returns the walkAnalysis for the function currently
// being walked, or nil outside of walk.Walk.
func curWalkAnalysis(gd *base.Invocation) *walkAnalysis {
	wa, _ := gd.WalkStaticValues.(*walkAnalysis)
	return wa
}

// analyzePreWalk populates staticValues and shapeConvSources using a
// single AST traversal. We can't use an ir.ReassignOracle or
// ir.StaticValue in the middle of walk because they don't currently
// handle transformed assignments (e.g., will complain about
// 'RHS == nil'). So we build these maps before walk begins.
func analyzePreWalk(fn *ir.Func) *walkAnalysis {
	ro := &ir.ReassignOracle{}
	ro.Init(fn)
	sv := make(map[ir.Node]ir.Node)
	scs := make(map[*ir.Name]*types.Type)
	ir.Visit(fn, func(n ir.Node) {
		switch n.Op() {
		case ir.OCONVIFACE:
			x := n.(*ir.ConvExpr).X
			v := ro.StaticValue(x)
			if v != nil && v != x {
				sv[x] = v
			}
		case ir.ONAME:
			name := n.(*ir.Name).Canonical()
			if name.Class != ir.PAUTO || name.Type() == nil || !name.Type().IsInterface() {
				return
			}
			val := ro.StaticValue(name)
			if val == nil || val.Op() != ir.OCONVIFACE {
				return
			}
			srcType := val.(*ir.ConvExpr).X.Type()
			if srcType != nil && !srcType.IsInterface() && srcType.IsShape() {
				scs[name] = srcType
			}
		}
	})
	return &walkAnalysis{staticValues: sv, shapeConvSources: scs}
}
