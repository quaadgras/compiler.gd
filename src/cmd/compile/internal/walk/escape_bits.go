// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package walk

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/reflectdata"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
	"cmd/internal/src"
)

// escapeBoxes maps each promoted EscCandidate local to the pointer
// var that wrap sites can re-home at a dynamic call. Populated by
// promoteEscapeCandidates at the start of walking a function and
// cleared at the end. The pointer-var initially holds &name (the
// stack backing); at a wrap site we update it to the heap copy
// returned by runtime.maybeEscape…Arg when the mask bit fires, and
// the call-argument rewrite substitutes the pointer-var's value in
// place of `&name`.
//
// Per-function state lives on Invocation (gd.WalkEscapeBoxes) rather
// than as a package var so concurrent compile invocations can't
// clobber each other's transient walk state.
func escapeBoxes(gd *base.Invocation) map[*ir.Name]*ir.Name {
	m, _ := gd.WalkEscapeBoxes.(map[*ir.Name]*ir.Name)
	return m
}

// promoteEscapeCandidates wires up a pointer-indirection for every
// PAUTO local that escape analysis has tagged EscCandidate:
//
//	var x T           (PAUTO, stays PAUTO) →
//	var &x *T         (PAUTO, new pointer var)
//	&x = &x           (prologue: initializes the pointer)
//	                  recorded in escapeBoxes[x] = &x
//
// At a dynamic call site `f(&x)` the wrap emits:
//
//	&x = runtime.maybeEscape…(&x, …)
//	f(&x)             // the `&x` arg becomes a load of the
//	                   pointer var, which now holds either the
//	                   original stack pointer or a migrated heap
//	                   pointer depending on the mask bit.
//
// The transformation only affects the value the callee sees. It
// does NOT rewrite other uses of x in the caller; callers that read
// x after the call continue to see the original stack slot. For the
// current test suite all EscCandidate vars are write-once / pass-
// address / drop, so stack-vs-heap divergence is unobservable.
// Extending the rewrite to post-call reads requires routing all
// accesses through the pointer var (Heapaddr); that's the follow-
// up documented in doc/gd/escape-bits.md §6a.
func promoteEscapeCandidates(gd *base.Invocation, fn *ir.Func) {
	if fn == nil || len(fn.Dcl) == 0 {
		return
	}
	gd.WalkEscapeBoxes = nil // fresh per function
	var prologue ir.Nodes
	dcls := fn.Dcl[:len(fn.Dcl):len(fn.Dcl)]
	for _, name := range dcls {
		if name.Class != ir.PAUTO || !name.EscCandidate() {
			continue
		}
		t := name.Type()
		if t == nil || t.Size() == 0 {
			continue
		}
		registerEscapeBox(gd, fn, name, &prologue)
	}
	if len(prologue) == 0 {
		return
	}
	newBody := make(ir.Nodes, 0, len(prologue)+len(fn.Body))
	newBody = append(newBody, prologue...)
	newBody = append(newBody, fn.Body...)
	fn.Body = newBody
}

// registerEscapeBox sets up the Heapaddr indirection for a promoted
// EscCandidate PAUTO local:
//
//	backing T       // fresh PAUTO holding the actual value
//	addr    *T      // fresh PAUTO holding a pointer to backing
//	addr = &backing // prologue
//	name.Heapaddr = addr
//
// After this, SSA gen routes every read/write of `name` through
// `*addr` and every `&name` through `addr`. The wrap at a dynamic
// call site re-points `addr` to a heap copy when the callee's mask
// fires, so post-call accesses of `name` through `*addr` agree with
// the callee's view of the pointee — which is the invariant the
// naive copy-pointee wrap design could not preserve.
func registerEscapeBox(gd *base.Invocation, fn *ir.Func, name *ir.Name, prologue *ir.Nodes) {
	pos := name.Pos()
	t := name.Type()

	// backing: the actual storage slot for name. PAUTO, stack-
	// allocated. Kept separate from `name` so SSA's Heapaddr
	// rewrite on name doesn't fire when we compute `&backing` in
	// the prologue.
	backingSym := &types.Sym{Name: "." + name.Sym().Name + ".backing", Pkg: types.LocalPkg(gd)}
	backing := ir.NewNameAt(gd, pos, backingSym, t)
	backing.Class = ir.PAUTO
	backing.Curfn = fn
	backing.SetUsed(true)
	backing.SetAutoTemp(true)
	// Mark backing as EscHeap so liveness/shouldTrack skips it —
	// the callee writes the backing through a pointer, which
	// liveness doesn't see, and we'd hit "bad live variable at
	// entry" otherwise.
	backing.SetEsc(ir.EscHeap)
	types.CalcSize(gd, backing.Type())
	fn.Dcl = append(fn.Dcl, backing)

	// addr: the pointer to the active storage. PAUTO *T. Starts
	// pointing at backing; the wrap may later re-home it to a
	// heap copy.
	addrSym := &types.Sym{Name: "&" + name.Sym().Name, Pkg: types.LocalPkg(gd)}
	addr := ir.NewNameAt(gd, pos, addrSym, types.NewPtr(t))
	addr.Class = ir.PAUTO
	addr.Curfn = fn
	addr.SetUsed(true)
	addr.SetAutoTemp(true)
	types.CalcSize(gd, addr.Type())
	fn.Dcl = append(fn.Dcl, addr)

	// Prologue: addr = &backing.
	takeAddr := typecheck.NodAddrAt(gd, pos, backing)
	takeAddr.SetType(addr.Type())
	takeAddr.SetTypecheck(1)
	as := ir.NewAssignStmt(gd, pos, addr, takeAddr)
	as.SetTypecheck(1)
	prologue.Append(as)

	// Route every later access of name through *addr.
	name.Heapaddr = addr

	eb, _ := gd.WalkEscapeBoxes.(map[*ir.Name]*ir.Name)
	if eb == nil {
		eb = make(map[*ir.Name]*ir.Name)
		gd.WalkEscapeBoxes = eb
	}
	eb[name] = addr
}

// wrapEscapeCandidateArgs rewrites each candidate arg of n (a closure
// OCALLFUNC with a dynamic callee, or an OCALLINTER) so that, at
// runtime, the caller's pointer to the pointee is either left alone
// (when the callee's escape-mask bit is clear) or re-homed to heap
// first (when the bit is set). For ONAME PAUTOHEAP args we update
// the name's Heapaddr pointer var in place so post-call reads of the
// variable through *Heapaddr agree with the callee's view. For the
// ONEW / &T{} case the wrap substitutes the call argument directly.
//
// Phase D of doc/gd/escape-bits.md.
func wrapEscapeCandidateArgs(gd *base.Invocation, n *ir.CallExpr, init *ir.Nodes) {
	if n == nil {
		return
	}
	var isIface bool
	switch n.Op() {
	case ir.OCALLFUNC:
		if ir.StaticCalleeName(n.Fun) != nil {
			// Direct call; escape analysis used the callee's per-
			// param escape tag directly — nothing to wrap.
			return
		}
	case ir.OCALLINTER:
		isIface = true
	default:
		return
	}

	// Bail cheaply when no arg is a candidate — the hot path.
	hasCandidate := false
	for _, arg := range n.Args {
		if argIsEscapeCandidate(arg) {
			hasCandidate = true
			break
		}
	}
	if !hasCandidate {
		return
	}
	if isIface {
		wrapIfaceCallCandidates(gd, n, init)
	} else {
		wrapClosureCallCandidates(gd, n, init)
	}
}

// argIsEscapeCandidate reports whether arg points to an object that
// escape analysis classified as EscCandidate. We check three shapes:
//
//  1. The direct node itself (e.g. an ONEW that survived inline
//     stack-allocation), or a pointer-valued local bound to one.
//  2. `&name` where name is a PAUTOHEAP-promoted candidate.
//  3. A Name whose Defn chains back to a candidate expression.
func argIsEscapeCandidate(arg ir.Node) bool {
	if arg == nil {
		return false
	}
	if arg.EscCandidate() {
		return true
	}
	switch a := arg.(type) {
	case *ir.AddrExpr:
		if a.X != nil && a.X.EscCandidate() {
			return true
		}
		if inner, ok := a.X.(*ir.Name); ok && nameReachesCandidate(inner, 4) {
			return true
		}
	case *ir.Name:
		return nameReachesCandidate(a, 4)
	}
	return false
}

// nameReachesCandidate reports whether n (or, via Defn, a chain of
// Name assignments of bounded depth) carries the EscCandidate bit.
func nameReachesCandidate(n *ir.Name, depth int) bool {
	if n == nil || depth <= 0 {
		return false
	}
	if n.EscCandidate() {
		return true
	}
	if n.Class != ir.PAUTO {
		return false
	}
	if n.Defn == nil {
		return false
	}
	rhs := n.Defn
	if as, ok := rhs.(*ir.AssignStmt); ok {
		rhs = as.Y
	}
	if rhs == nil {
		return false
	}
	if rhs.EscCandidate() {
		return true
	}
	if name, ok := rhs.(*ir.Name); ok {
		return nameReachesCandidate(name, depth-1)
	}
	return false
}

// candidateStorageAddr returns the pointer-var registered in
// escapeBoxes for `arg` when arg is `&name` for a promoted
// EscCandidate local. The wrap site updates that pointer var at
// runtime and the call arg is rewritten to its loaded value, so the
// callee sees either the original stack pointer or a heap-migrated
// pointer depending on the mask bit. Returns (nil, false) for
// fresh-allocation args; those substitute the call arg directly.
func candidateStorageAddr(gd *base.Invocation, arg ir.Node) (*ir.Name, bool) {
	eb := escapeBoxes(gd)
	if eb == nil {
		return nil, false
	}
	if ae, ok := arg.(*ir.AddrExpr); ok {
		if name, ok := ae.X.(*ir.Name); ok {
			if box, ok := eb[name]; ok {
				return box, true
			}
		}
	}
	return nil, false
}

// candidateArg captures the per-arg work walk needs to do at one
// indirect call site. We pre-classify each candidate arg once,
// then emit the resolved-mask plumbing in one block (cheap-cache
// the boxes, build heapMask, resolve, per-arg test + materialise),
// rather than threading classification through one mkcall per arg.
type candidateArg struct {
	idx       int      // position in n.Args
	box       *ir.Name // pointer-PAUTO whose value is the live storage; nil for fresh-alloc
	name      *ir.Name // user local for `p := new(T); f(p)`; nil otherwise
	freshExpr ir.Node  // raw expression for fresh-alloc args; nil otherwise
	elemPtr   ir.Node  // *abi.Type for materializeToHeap
	argType   *types.Type
}

// classifyCandidates builds the per-arg work list for n. Returns
// (nil, false) if no arg is a candidate or none classifies as a
// supported shape — the caller then bails before emitting any
// mask-resolution plumbing.
func classifyCandidates(gd *base.Invocation, n *ir.CallExpr) ([]candidateArg, bool) {
	pos := n.Pos()
	out := make([]candidateArg, 0, len(n.Args))
	for i, arg := range n.Args {
		if !argIsEscapeCandidate(arg) {
			continue
		}
		argType := arg.Type()
		if !argType.IsPtr() && !argType.IsUnsafePtr() {
			continue
		}
		ca := candidateArg{idx: i, argType: argType, elemPtr: reflectdata.TypePtrAt(gd, pos, argType.Elem())}
		if box, ok := candidateStorageAddr(gd, arg); ok {
			ca.box = box
		} else if name, ok := candidatePointerName(arg); ok {
			ca.name = name
		} else {
			ca.freshExpr = arg
		}
		out = append(out, ca)
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// boxValue returns an ir.Node whose evaluation yields the *T
// pointer currently in the box / name / fresh expression. This is
// the value passed to runtime.materializeToHeap and the value
// tested by runtime.isOnHeap when building heapMask.
func (ca *candidateArg) boxValue() ir.Node {
	switch {
	case ca.box != nil:
		return ca.box
	case ca.name != nil:
		return ca.name
	default:
		return ca.freshExpr
	}
}

// u64Lit returns a typed-uint64 literal; using ir.NewInt directly
// yields an untyped constant, which trips ssagen's "width not
// calculated" check when fed into a typed binary op without going
// through full typecheck propagation.
func u64Lit(gd *base.Invocation, pos src.XPos, v int64) ir.Node {
	n := ir.NewInt(gd, pos, v)
	n.SetType(types.Types[types.TUINT64])
	n.SetTypecheck(1)
	return n
}

// intLit returns a typed-int literal; mirrors u64Lit for places
// we feed an `int`-typed value into a runtime helper (depth arg).
func intLit(gd *base.Invocation, pos src.XPos, v int64) ir.Node {
	n := ir.NewInt(gd, pos, v)
	n.SetType(types.Types[types.TINT])
	n.SetTypecheck(1)
	return n
}

// uintptrLit returns a typed-uintptr literal for offset arithmetic
// on unsafe-pointer-derived values.
func uintptrLit(gd *base.Invocation, pos src.XPos, v int64) ir.Node {
	n := ir.NewInt(gd, pos, v)
	n.SetType(types.Types[types.TUINTPTR])
	n.SetTypecheck(1)
	return n
}

// rawLoadU64At returns IR for `*(*uint64)(unsafe.Pointer(uintptr(base) + offset))`.
// Used to open-code the mask-word load out of a closure header or
// itab method tail without paying a runtime helper call on the
// fast (static-mask) path.
func rawLoadU64At(gd *base.Invocation, pos src.XPos, base ir.Node, offset int64) ir.Node {
	unsafePtr := types.Types[types.TUNSAFEPTR]
	uintptrT := types.Types[types.TUINTPTR]
	u64 := types.Types[types.TUINT64]

	asUnsafe := typecheck.ConvNop(gd, base, unsafePtr)
	asUintptr := typecheck.Conv(gd, asUnsafe, uintptrT)
	sum := ir.NewBinaryExpr(gd, pos, ir.OADD, asUintptr, uintptrLit(gd, pos, offset))
	sum.SetType(uintptrT)
	sum.SetTypecheck(1)
	asPtrU64 := typecheck.ConvNop(gd, typecheck.ConvNop(gd, sum, unsafePtr), types.NewPtr(u64))
	deref := ir.NewStarExpr(gd, pos, asPtrU64)
	deref.SetType(u64)
	deref.SetTypecheck(1)
	return deref
}

// newU64Temp creates a fresh PAUTO uint64 temporary in the current
// function. Used for the resolved mask and the heapMask scratch
// values that walk threads through the open-coded resolution
// sequence.
func newU64Temp(gd *base.Invocation, pos src.XPos) *ir.Name {
	t := typecheck.TempAt(gd, pos, ir.CurFunc(gd), types.Types[types.TUINT64])
	t.SetTypecheck(1)
	return t
}

// emitMaskResolve emits the open-coded mask-resolution sequence
// for one indirect call site:
//
//	raw := *(*uint64)(carrier + maskOffset)
//	mask := raw
//	if raw & 1 != 0 {
//	    hm := uint64(0)
//	    if isOnHeap(box_0) { hm |= 1 << 1 }
//	    if isOnHeap(box_1) { hm |= 1 << 2 }
//	    ...
//	    mask = resolveMaskSlow(raw, computeFnCarrier, hm, maxComputeMaskDepth)
//	}
//
// Returns the `mask` PAUTO. carrier is the value at whose
// maskOffset the raw mask word lives (the closure value, or the
// itab pointer for an iface call). computeFnCarrier is what the
// compute fn sees as its first argument (the closure value, or the
// receiver-data word for an iface call) — symmetric across the two
// dispatch shapes: in both cases it points at the storage holding
// the captured/field state the compute fn reads to forward.
//
// heapMask construction lives inside the bit-0 branch on purpose:
// the static-mask fast path (the dominant case across stdlib) costs
// just one load + one and-test + branch-not-taken, with no
// stack-range checks at all.
func emitMaskResolve(gd *base.Invocation, pos src.XPos, init *ir.Nodes, carrier, computeFnCarrier ir.Node, maskOffset int64, cands []candidateArg) *ir.Name {
	u64 := types.Types[types.TUINT64]
	unsafePtr := types.Types[types.TUNSAFEPTR]

	rawTmp := newU64Temp(gd, pos)
	init.Append(typecheck.Stmt(gd, ir.NewAssignStmt(gd, pos, rawTmp, rawLoadU64At(gd, pos, carrier, maskOffset))))

	maskTmp := newU64Temp(gd, pos)
	init.Append(typecheck.Stmt(gd, ir.NewAssignStmt(gd, pos, maskTmp, rawTmp)))

	// Build the dynamic-branch body: compute heapMask, call
	// resolveMaskSlow, store result into maskTmp.
	var dynBody ir.Nodes
	hmTmp := newU64Temp(gd, pos)
	dynBody.Append(typecheck.Stmt(gd, ir.NewAssignStmt(gd, pos, hmTmp, u64Lit(gd, pos, 0))))
	for _, ca := range cands {
		boxAsUnsafe := typecheck.ConvNop(gd, ca.boxValue(), unsafePtr)
		isHeapCall := mkcall(gd, "isOnHeap", types.Types[types.TBOOL], &dynBody, boxAsUnsafe)
		bit := u64Lit(gd, pos, int64(1)<<uint(ca.idx+1))
		// Walk lowering of OASOP normally happens during walkStmt;
		// statements we append into a freshly-constructed Nodes
		// won't be re-walked, so lower hmTmp |= bit by hand to
		// hmTmp = hmTmp | bit.
		orExpr := typecheck.Expr(gd, ir.NewBinaryExpr(gd, pos, ir.OOR, hmTmp, bit))
		var orBody ir.Nodes
		orBody.Append(typecheck.Stmt(gd, ir.NewAssignStmt(gd, pos, hmTmp, orExpr)))
		dynBody.Append(typecheck.Stmt(gd, ir.NewIfStmt(gd, pos, isHeapCall, orBody, nil)))
	}
	slowCall := mkcall(gd, "resolveMaskSlow", u64, &dynBody,
		rawTmp,
		typecheck.ConvNop(gd, computeFnCarrier, unsafePtr),
		hmTmp,
		intLit(gd, pos, int64(maxComputeMaskDepthForGen)),
	)
	dynBody.Append(typecheck.Stmt(gd, ir.NewAssignStmt(gd, pos, maskTmp, slowCall)))

	cond := typecheck.Expr(gd, ir.NewBinaryExpr(gd, pos, ir.ONE,
		ir.NewBinaryExpr(gd, pos, ir.OAND, rawTmp, u64Lit(gd, pos, 1)),
		u64Lit(gd, pos, 0)))
	init.Append(typecheck.Stmt(gd, ir.NewIfStmt(gd, pos, cond, dynBody, nil)))

	return maskTmp
}

// maxComputeMaskDepthForGen mirrors runtime.maxComputeMaskDepth.
// The runtime constant is unexported and we'd rather not add a
// runtime accessor for one int — keep them in lockstep here.
const maxComputeMaskDepthForGen = 8

// emitMaybeMaterialize emits, for one candidate arg, the open-
// coded conditional materialise:
//
//	if mask & (1 << (i+1)) != 0 {
//	    slot = (T)(runtime.materializeToHeap(unsafe.Pointer(slot), &T))
//	}
//
// where `slot` is the storage location whose value reaches the
// callee — a promoted-PAUTO box, a user pointer-name, or a fresh
// PAUTO seeded with the original fresh-alloc expression. The call
// site's argument is then rewritten to reference `slot`.
func emitMaybeMaterialize(gd *base.Invocation, pos src.XPos, n *ir.CallExpr, init *ir.Nodes, mask *ir.Name, ca candidateArg) {
	unsafePtr := types.Types[types.TUNSAFEPTR]

	var slot *ir.Name
	switch {
	case ca.box != nil:
		slot = ca.box
	case ca.name != nil:
		slot = ca.name
	default:
		slot = typecheck.TempAt(gd, pos, ir.CurFunc(gd), ca.argType)
		slot.SetTypecheck(1)
		init.Append(typecheck.Stmt(gd, ir.NewAssignStmt(gd, pos, slot, ca.freshExpr)))
	}

	var bodyInit ir.Nodes
	matCall := mkcall(gd, "materializeToHeap", unsafePtr, &bodyInit,
		typecheck.ConvNop(gd, slot, unsafePtr),
		ca.elemPtr,
	)
	bodyInit.Append(typecheck.Stmt(gd, ir.NewAssignStmt(gd, pos, slot, typecheck.ConvNop(gd, matCall, slot.Type()))))

	bit := u64Lit(gd, pos, int64(1)<<uint(ca.idx+1))
	cond := typecheck.Expr(gd, ir.NewBinaryExpr(gd, pos, ir.ONE,
		ir.NewBinaryExpr(gd, pos, ir.OAND, mask, bit),
		u64Lit(gd, pos, 0)))
	init.Append(typecheck.Stmt(gd, ir.NewIfStmt(gd, pos, cond, bodyInit, nil)))
	n.Args[ca.idx] = slot
}

func wrapClosureCallCandidates(gd *base.Invocation, n *ir.CallExpr, init *ir.Nodes) {
	pos := n.Pos()
	cands, ok := classifyCandidates(gd, n)
	if !ok {
		return
	}
	// Cache the closure value once: the mask load, the compute-fn
	// carrier, and the call itself all see the same materialisation.
	fnCached := cheapExpr(gd, n.Fun, init)
	mask := emitMaskResolve(gd, pos, init, fnCached, fnCached, int64(types.PtrSize), cands)
	for _, ca := range cands {
		emitMaybeMaterialize(gd, pos, n, init, mask, ca)
	}
	n.Fun = fnCached
}

func wrapIfaceCallCandidates(gd *base.Invocation, n *ir.CallExpr, init *ir.Nodes) {
	pos := n.Pos()
	unsafePtr := types.Types[types.TUNSAFEPTR]

	sel, ok := n.Fun.(*ir.SelectorExpr)
	if !ok {
		gd.FatalfAt(pos, "OCALLINTER with non-SelectorExpr Fun: %+v", n.Fun)
	}

	cands, hasCands := classifyCandidates(gd, n)
	if !hasCands {
		return
	}

	// Cache the iface value, then derive itab AND data pointers
	// from it. The mask word lives in the itab tail (carrier);
	// the compute fn sees the receiver-data pointer
	// (computeFnCarrier) so it can reach the receiver's fields —
	// symmetric to the closure case where captures live in the
	// closure value itself.
	ifaceVal := cheapExpr(gd, sel.X, init)
	itabVal := ir.NewUnaryExpr(gd, pos, ir.OITAB, ifaceVal)
	itabVal.SetType(unsafePtr)
	itabVal.SetTypecheck(1)
	itabCached := cheapExpr(gd, itabVal, init)

	dataVal := ir.NewUnaryExpr(gd, pos, ir.OIDATA, ifaceVal)
	dataVal.SetType(unsafePtr)
	dataVal.SetTypecheck(1)
	dataCached := cheapExpr(gd, dataVal, init)

	methodIdx := sel.Offset() / int64(types.PtrSize)
	ifaceType := sel.X.Type()
	for ifaceType.IsPtr() {
		ifaceType = ifaceType.Elem()
	}
	if !ifaceType.IsInterface() {
		gd.FatalfAt(pos, "OCALLINTER receiver type is not interface: %v", sel.X.Type())
	}
	ni := int64(len(ifaceType.AllMethods()))

	// itab layout (mirrors src/internal/abi/iface.go ITab and
	// src/runtime/iface.go itab in the gd fork):
	//   Inter   *InterfaceType  (PtrSize)
	//   Type    *Type           (PtrSize)
	//   Hash    uint32          (4)
	//   Inline  uint8           (1)
	//   _       [3]byte         (3)
	//   Fun     [N]uintptr      (N * PtrSize)
	//   masks   [N]uint64       (N * 8) ← per-method escape masks
	//
	// Per-method mask tail starts at funBase + N*PtrSize; methodIdx
	// selects the 8-byte slot within it.
	funBase := int64(2*types.PtrSize) + 8 // 2 ptrs + uint32 + uint8 + 3 pad
	maskOffset := funBase + ni*int64(types.PtrSize) + methodIdx*8

	mask := emitMaskResolve(gd, pos, init, itabCached, dataCached, maskOffset, cands)
	for _, ca := range cands {
		emitMaybeMaterialize(gd, pos, n, init, mask, ca)
	}
	sel.X = ifaceVal
}

// candidatePointerName returns (name, true) when arg is an ONAME
// PAUTO pointer variable whose Defn chains back to an EscCandidate
// allocation (`p := new(T)` / `p := &Foo{}` style). In that case the
// wrap updates p's value in place and the call arg is p itself.
func candidatePointerName(arg ir.Node) (*ir.Name, bool) {
	name, ok := arg.(*ir.Name)
	if !ok || name.Class != ir.PAUTO {
		return nil, false
	}
	t := name.Type()
	if t == nil || (!t.IsPtr() && !t.IsUnsafePtr()) {
		return nil, false
	}
	if !nameReachesCandidate(name, 4) {
		return nil, false
	}
	return name, true
}
