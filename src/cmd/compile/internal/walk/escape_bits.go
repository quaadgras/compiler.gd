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
var escapeBoxes map[*ir.Name]*ir.Name

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
func promoteEscapeCandidates(fn *ir.Func) {
	if fn == nil || len(fn.Dcl) == 0 {
		return
	}
	escapeBoxes = nil // fresh per function
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
		registerEscapeBox(fn, name, &prologue)
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
func registerEscapeBox(fn *ir.Func, name *ir.Name, prologue *ir.Nodes) {
	pos := name.Pos()
	t := name.Type()

	// backing: the actual storage slot for name. PAUTO, stack-
	// allocated. Kept separate from `name` so SSA's Heapaddr
	// rewrite on name doesn't fire when we compute `&backing` in
	// the prologue.
	backingSym := &types.Sym{Name: "." + name.Sym().Name + ".backing", Pkg: types.LocalPkg}
	backing := ir.NewNameAt(pos, backingSym, t)
	backing.Class = ir.PAUTO
	backing.Curfn = fn
	backing.SetUsed(true)
	backing.SetAutoTemp(true)
	// Mark backing as EscHeap so liveness/shouldTrack skips it —
	// the callee writes the backing through a pointer, which
	// liveness doesn't see, and we'd hit "bad live variable at
	// entry" otherwise.
	backing.SetEsc(ir.EscHeap)
	types.CalcSize(backing.Type())
	fn.Dcl = append(fn.Dcl, backing)

	// addr: the pointer to the active storage. PAUTO *T. Starts
	// pointing at backing; the wrap may later re-home it to a
	// heap copy.
	addrSym := &types.Sym{Name: "&" + name.Sym().Name, Pkg: types.LocalPkg}
	addr := ir.NewNameAt(pos, addrSym, types.NewPtr(t))
	addr.Class = ir.PAUTO
	addr.Curfn = fn
	addr.SetUsed(true)
	addr.SetAutoTemp(true)
	types.CalcSize(addr.Type())
	fn.Dcl = append(fn.Dcl, addr)

	// Prologue: addr = &backing.
	takeAddr := typecheck.NodAddrAt(pos, backing)
	takeAddr.SetType(addr.Type())
	takeAddr.SetTypecheck(1)
	as := ir.NewAssignStmt(pos, addr, takeAddr)
	as.SetTypecheck(1)
	prologue.Append(as)

	// Route every later access of name through *addr.
	name.Heapaddr = addr

	if escapeBoxes == nil {
		escapeBoxes = make(map[*ir.Name]*ir.Name)
	}
	escapeBoxes[name] = addr
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
func wrapEscapeCandidateArgs(n *ir.CallExpr, init *ir.Nodes) {
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
		wrapIfaceCallCandidates(n, init)
	} else {
		wrapClosureCallCandidates(n, init)
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
func candidateStorageAddr(arg ir.Node) (*ir.Name, bool) {
	if escapeBoxes == nil {
		return nil, false
	}
	if ae, ok := arg.(*ir.AddrExpr); ok {
		if name, ok := ae.X.(*ir.Name); ok {
			if box, ok := escapeBoxes[name]; ok {
				return box, true
			}
		}
	}
	return nil, false
}

func wrapClosureCallCandidates(n *ir.CallExpr, init *ir.Nodes) {
	pos := n.Pos()
	unsafePtr := types.Types[types.TUNSAFEPTR]

	// Cache the closure value once so every per-arg wrap sees the
	// same mask word.
	fnCached := cheapExpr(n.Fun, init)

	for i, arg := range n.Args {
		if !argIsEscapeCandidate(arg) {
			continue
		}
		argType := arg.Type()
		if !argType.IsPtr() && !argType.IsUnsafePtr() {
			continue
		}
		elemType := argType.Elem()
		elemTypePtr := reflectdata.TypePtrAt(pos, elemType)

		if box, ok := candidateStorageAddr(arg); ok {
			emitBoxUpdate(pos, box, elemTypePtr, init, fnCached, int64(i), "maybeEscapeClosureArg", nil)
			n.Args[i] = box
			continue
		}
		if name, ok := candidatePointerName(arg); ok {
			// `p := new(T); f(p)` — update the var in place so
			// post-call reads of p see the migrated pointer.
			// The call argument is the var itself.
			updateNameViaWrap(pos, name, elemTypePtr, init, fnCached, int64(i), "maybeEscapeClosureArg", nil)
			n.Args[i] = name
			continue
		}

		// Fresh-allocation path: substitute the call argument with
		// the wrap's return value (no var to update).
		wrapCall := mkcall("maybeEscapeClosureArg", unsafePtr, init,
			typecheck.ConvNop(fnCached, unsafePtr),
			ir.NewInt(pos, int64(i)),
			typecheck.ConvNop(arg, unsafePtr),
			elemTypePtr,
		)
		n.Args[i] = typecheck.ConvNop(wrapCall, argType)
	}

	// Point the call at the cached closure value so Fun and Args
	// see the same materialisation.
	n.Fun = fnCached
}

func wrapIfaceCallCandidates(n *ir.CallExpr, init *ir.Nodes) {
	pos := n.Pos()
	unsafePtr := types.Types[types.TUNSAFEPTR]

	sel, ok := n.Fun.(*ir.SelectorExpr)
	if !ok {
		base.FatalfAt(pos, "OCALLINTER with non-SelectorExpr Fun: %+v", n.Fun)
	}

	ifaceVal := cheapExpr(sel.X, init)
	itabVal := ir.NewUnaryExpr(pos, ir.OITAB, ifaceVal)
	itabVal.SetType(unsafePtr)
	itabVal.SetTypecheck(1)
	itabCached := cheapExpr(itabVal, init)

	methodIdx := sel.Offset() / int64(types.PtrSize)

	for i, arg := range n.Args {
		if !argIsEscapeCandidate(arg) {
			continue
		}
		argType := arg.Type()
		if !argType.IsPtr() && !argType.IsUnsafePtr() {
			continue
		}
		elemType := argType.Elem()
		elemTypePtr := reflectdata.TypePtrAt(pos, elemType)

		if box, ok := candidateStorageAddr(arg); ok {
			emitBoxUpdate(pos, box, elemTypePtr, init, itabCached, int64(i), "maybeEscapeIfaceArg", ir.NewInt(pos, methodIdx))
			n.Args[i] = box
			continue
		}
		if name, ok := candidatePointerName(arg); ok {
			updateNameViaWrap(pos, name, elemTypePtr, init, itabCached, int64(i), "maybeEscapeIfaceArg", ir.NewInt(pos, methodIdx))
			n.Args[i] = name
			continue
		}

		wrapCall := mkcall("maybeEscapeIfaceArg", unsafePtr, init,
			itabCached,
			ir.NewInt(pos, methodIdx),
			ir.NewInt(pos, int64(i)),
			typecheck.ConvNop(arg, unsafePtr),
			elemTypePtr,
		)
		n.Args[i] = typecheck.ConvNop(wrapCall, argType)
	}

	sel.X = ifaceVal
}

// emitBoxUpdate emits `box = cast(helper(carrier, …,
// unsafe.Pointer(box), elemTypePtr))` into init.
func emitBoxUpdate(pos src.XPos, box *ir.Name, elemTypePtr ir.Node, init *ir.Nodes, carrier ir.Node, argIdx int64, helperName string, methodIdxArg ir.Node) {
	updateNameViaWrap(pos, box, elemTypePtr, init, carrier, argIdx, helperName, methodIdxArg)
}

// updateNameViaWrap emits `name = cast(helper(carrier, …,
// unsafe.Pointer(name), elemTypePtr))`. Works for both the box
// pointer case (where name is the synthesized `&x` PAUTO whose
// value is the active storage pointer) and the Name-of-pointer
// case (where name is a user local whose value is a pointer to a
// fresh allocation). helperName is "maybeEscapeClosureArg" or
// "maybeEscapeIfaceArg"; methodIdxArg is nil for closure calls.
func updateNameViaWrap(pos src.XPos, name *ir.Name, elemTypePtr ir.Node, init *ir.Nodes, carrier ir.Node, argIdx int64, helperName string, methodIdxArg ir.Node) {
	unsafePtr := types.Types[types.TUNSAFEPTR]
	args := []ir.Node{
		typecheck.ConvNop(carrier, unsafePtr),
	}
	if methodIdxArg != nil {
		args = append(args, methodIdxArg)
	}
	args = append(args,
		ir.NewInt(pos, argIdx),
		typecheck.ConvNop(name, unsafePtr),
		elemTypePtr,
	)
	wrapCall := mkcall(helperName, unsafePtr, init, args...)
	newPtr := typecheck.ConvNop(wrapCall, name.Type())
	as := ir.NewAssignStmt(pos, name, newPtr)
	init.Append(typecheck.Stmt(as))
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
