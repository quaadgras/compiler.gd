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
)

// wrapEscapeCandidateArgs rewrites each pointer arg of n (a closure
// call OCALLFUNC with a dynamic callee, or an OCALLINTER) whose
// Esc() == ir.EscCandidate so that, at runtime, the arg is either
// passed as a stack pointer (when the callee's escape-mask bit is
// clear) or copied to the heap first (when the bit is set).
//
// Phase D of doc/gd/escape-bits.md.
//
// The transformation, for each candidate pointer arg at index i:
//
//   orig:  cl(arg_0, ..., arg_i, ...)
//   rewr:  tmpFn := cl
//          arg_i = (*T)(runtime.maybeEscapeClosureArg(
//                     unsafe.Pointer(tmpFn), i,
//                     unsafe.Pointer(arg_i), reflect_type_ptr_of_T))
//          tmpFn(..., arg_i, ...)
//
// For OCALLINTER the helper is runtime.maybeEscapeIfaceArg, taking
// the itab pointer and the method index so it can look up the right
// slot in the itab's mask tail.
func wrapEscapeCandidateArgs(n *ir.CallExpr, init *ir.Nodes) {
	if n == nil {
		return
	}
	var isIface bool
	switch n.Op() {
	case ir.OCALLFUNC:
		if ir.StaticCalleeName(n.Fun) != nil {
			// Direct call; escape analysis used the callee's per-param
			// escape tag directly — nothing to wrap.
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
// escape analysis classified as EscCandidate — a stack-allocated
// allocation whose only escape edge is through this dynamic call.
// Escape analysis tags the underlying allocation's node, not the
// address-taking expression, so we peer through OADDR to inspect
// the target.
func argIsEscapeCandidate(arg ir.Node) bool {
	if arg == nil {
		return false
	}
	if arg.Esc() == ir.EscCandidate {
		return true
	}
	switch a := arg.(type) {
	case *ir.AddrExpr:
		if a.X != nil && a.X.Esc() == ir.EscCandidate {
			return true
		}
	}
	return false
}

func wrapClosureCallCandidates(n *ir.CallExpr, init *ir.Nodes) {
	pos := n.Pos()
	unsafePtr := types.Types[types.TUNSAFEPTR]

	// Cache the closure value so the per-arg wraps all read the same
	// mask word (and so the closure expression is evaluated only
	// once). func-to-unsafe.Pointer isn't a legal ConvExpr in the
	// language, but at the IR level the bit pattern is the same —
	// we cache the func-typed value and reinterpret with ConvNop.
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

		// runtime.maybeEscapeClosureArg(unsafe.Pointer(fnCached), i, unsafe.Pointer(arg), elemTypePtr)
		wrapCall := mkcall("maybeEscapeClosureArg", unsafePtr, init,
			typecheck.ConvNop(fnCached, unsafePtr),
			ir.NewInt(pos, int64(i)),
			typecheck.ConvNop(arg, unsafePtr),
			elemTypePtr,
		)
		// Convert the helper's unsafe.Pointer result back to the arg's
		// original pointer type.
		n.Args[i] = typecheck.ConvNop(wrapCall, argType)
	}

	// Point the call at the cached closure value instead of the
	// original expression so both Fun and Args see the same
	// materialisation.
	n.Fun = fnCached
}

func wrapIfaceCallCandidates(n *ir.CallExpr, init *ir.Nodes) {
	pos := n.Pos()
	unsafePtr := types.Types[types.TUNSAFEPTR]

	// n.Fun is a SelectorExpr on the iface value. Grab the itab
	// pointer from the iface header.
	sel, ok := n.Fun.(*ir.SelectorExpr)
	if !ok {
		base.FatalfAt(pos, "OCALLINTER with non-SelectorExpr Fun: %+v", n.Fun)
	}

	ifaceVal := cheapExpr(sel.X, init)
	// itab = iface.Tab — OITAB on the iface value.
	itabVal := ir.NewUnaryExpr(pos, ir.OITAB, ifaceVal)
	itabVal.SetType(unsafePtr)
	itabVal.SetTypecheck(1)
	itabCached := cheapExpr(itabVal, init)

	// Method index = sel.Offset() / PtrSize (the Fun slot index).
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

		// runtime.maybeEscapeIfaceArg(itab, methodIdx, i, unsafe.Pointer(arg), elemTypePtr)
		wrapCall := mkcall("maybeEscapeIfaceArg", unsafePtr, init,
			itabCached,
			ir.NewInt(pos, methodIdx),
			ir.NewInt(pos, int64(i)),
			typecheck.ConvNop(arg, unsafePtr),
			elemTypePtr,
		)
		n.Args[i] = typecheck.ConvNop(wrapCall, argType)
	}

	// Replace sel.X with the cached iface value so repeated reads
	// (itab load, receiver load) share the same evaluation.
	sel.X = ifaceVal
}
