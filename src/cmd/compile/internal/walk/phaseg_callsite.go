// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package walk

import (
	"strings"

	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
)

// phaseGCallSiteRewrite scans fn for calls to Phase-G-extended
// functions and replaces each trailing nil outBuf arg with the
// address of a fresh stack-allocated buffer when the call's
// corresponding result is provably non-escaping in the caller.
//
// A call with one pointer result gets a buffer wherever it is, when
// escape analysis says the result doesn't outlive the frame (the
// call's Esc, from its spill location in escape/call.go), and fn's own
// buffer when escape analysis put the result in it (chain-fold).
// (A call with several results has no spill location, so none of its
// results gets a buffer.)
//
// Other sites keep their nil outBuf and fall through to the heap path
// inside maybeInPlace — same cost as stock.
func phaseGCallSiteRewrite(gd *base.Invocation, fn *ir.Func) {
	if !types.PhaseGActive() || gd.Debug.GdEscBitsOff != 0 {
		return
	}
	// Skip nosplit functions: adding stack autotmps here blows the
	// runtime's tight nosplit budgets (e.g. cgoSigtramp -> lockextra
	// chain). Those call sites stay on the nil-outBuf path — maybeInPlace
	// falls through to mallocgc, parity with stock.
	if fn.Pragma&ir.Nosplit != 0 {
		return
	}
	// Skip runtime + internal/runtime + syscall packages while
	// debugging: their ABI invariants (init ordering, stack
	// growth, race detector hooks) are sensitive to any extra
	// stack allocation in the caller frames. Parity with stock.
	if pkg := phaseGOwnerPkg(gd, fn); pkg == "runtime" ||
		strings.HasPrefix(pkg, "internal/runtime/") ||
		pkg == "syscall" {
		return
	}
	// Every call whose result doesn't outlive this frame gets a
	// buffer, wherever it is: f().x, use(f()), a loop condition. Its
	// zeroing goes first in the body, which dominates every use (the
	// callee zeroes the buffer again when it uses it, so what an
	// earlier iteration left there doesn't matter). An argument can't
	// point into the buffer: only an earlier result of the same call
	// could, and one still live across iterations makes the result
	// escape (escape analysis: a variable outside the loop outlives
	// it).
	//
	// A call of a function that never places anything in the buffer
	// (it returns a pointer it was passed, or found, or nil) gets
	// none: ir.CalleeOutBufUnused.
	//
	// A call whose result flows only to one of fn's own results,
	// and that escape analysis put in the buffer fn was passed for it
	// (escape/phaseg.go), is passed that buffer instead (chain-fold).
	var zero []ir.Node
	ir.VisitList(fn.Body, func(n ir.Node) {
		call, ok := n.(*ir.CallExpr)
		if !ok || !calleeIsExtended(call) {
			return
		}
		switch {
		case call.GdForwardOutBufResult > 0:
			forwardParentOutBuf(fn, call, int(call.GdForwardOutBufResult)-1)
		case call.Esc() == ir.EscNone:
			if z := giveOutBuf(gd, fn, call); z != nil {
				zero = append(zero, z)
			}
		}
	})
	if len(zero) > 0 {
		fn.Body = append(ir.Nodes(zero), fn.Body...)
	}
}

// giveOutBuf replaces the nil outBuf argument of call, a call of a
// function with one result, a pointer, with the address of a new stack
// buffer of the result's element type, and returns the statement that
// zeroes the buffer. It returns nil if call has no such argument.
func giveOutBuf(gd *base.Invocation, fn *ir.Func, call *ir.CallExpr) ir.Node {
	if !calleeIsExtended(call) {
		return nil
	}
	sig := call.Fun.Type()
	if sig.NumOutBufs() != 1 || len(sig.Results()) != 1 {
		return nil
	}
	if ir.CalleeOutBufUnused(call, 0) {
		return nil
	}
	ptrT := sig.Results()[0].Type
	if ptrT == nil || !ptrT.IsPtr() || ptrT.Elem() == nil || ptrT.Elem().Size() == 0 {
		return nil
	}
	for i, p := range sig.Params() {
		if !p.IsOutBufParam() {
			continue
		}
		if i >= len(call.Args) || call.Args[i].Op() != ir.ONIL {
			return nil
		}
		buf, zero := newOutBuf(gd, fn, call, ptrT.Elem())
		call.Args[i] = buf
		return zero
	}
	return nil
}

// newOutBuf returns unsafe.Pointer(&buf) for a new stack buffer buf of
// type t for call's result, and the statement that zeroes it.
func newOutBuf(gd *base.Invocation, fn *ir.Func, call *ir.CallExpr, t *types.Type) (ir.Node, ir.Node) {
	// A fresh PAUTO of type T. Mark Addrtaken so downstream passes
	// (liveness/stack map) see it as a pointer-containing stack
	// slot if T has pointers.
	bufName := typecheck.TempAt(gd, call.Pos(), fn, t)
	bufName.SetEsc(ir.EscNone)
	bufName.SetAddrtaken(true)
	// CRITICAL: the returned pointer aliases this buffer, so its
	// lifetime extends past the call. The compiler's AllocFrame
	// pass can't see that aliasing (the pointer flows through
	// runtime.maybeInPlace and back), so it would happily reuse
	// this slot for a later autotmp — which would zero-overwrite
	// the callee's fill. Mark non-mergeable to pin the slot for
	// the full remainder of the function frame.
	bufName.SetNonMergeable(true)
	if t.HasPointers() {
		bufName.SetNeedzero(true)
	}

	// The zero-init as a plain OAS with a nil RHS — the internal
	// form of "var x T". Liveness requires an explicit write before
	// any address-take of a pointer-bearing stack local.
	zeroAs := ir.NewAssignStmt(gd, call.Pos(), bufName, nil)
	zeroAs.SetTypecheck(1)

	addr := typecheck.NodAddrAt(gd, call.Pos(), bufName)
	addr.SetType(types.NewPtr(t))
	addr.SetTypecheck(1)
	conv := ir.NewConvExpr(gd, call.Pos(), ir.OCONVNOP, types.Types[types.TUNSAFEPTR], addr)
	conv.SetTypecheck(1)
	return conv, zeroAs
}

// phaseGOwnerPkg returns the path of the package whose code fn is,
// for deciding whether the call-site rewrite applies. A DUPOK
// function (a generic instantiation, or a wrapper) is compiled in
// every package that needs it, and the linker keeps only one copy,
// so every copy must be the same: the decision has to follow the
// function's own package, the one its symbol belongs to, not the
// package being compiled. Otherwise, say, internal/sync's
// (*HashTrieMap[any,any]).expand forwards its outBuf when compiled
// in reflect but passes nil when compiled in syscall.
func phaseGOwnerPkg(gd *base.Invocation, fn *ir.Func) string {
	if fn.Dupok() {
		if s := fn.Sym(); s != nil && s.Pkg != nil {
			return s.Pkg.Path
		}
	}
	return gd.Ctxt.Pkgpath
}

// forwardParentOutBuf rewrites the call's nil outBuf arg (for the
// call's first pointer result) to fn's own outBuf parameter for
// fn's k-th pointer result. This realises Phase G's chain-fold:
// when the call's result flows directly into fn's k-th return,
// fn's caller already supplied (or didn't) a buffer for that slot,
// and threading it through avoids any allocation here.
//
// Returns nil pre-statements (no stack buffer is synthesised; we
// just substitute one Name reference for an ONIL).
func forwardParentOutBuf(fn *ir.Func, call *ir.CallExpr, parentResultIdx int) []ir.Node {
	parentSig := fn.Type()
	if parentSig == nil || !parentSig.GdReturnOutBuf() {
		return nil
	}
	// Find fn's outBuf param for the parentResultIdx-th pointer result.
	parentOutBuf := findOutBufParamName(fn, parentResultIdx)
	if parentOutBuf == nil {
		return nil
	}
	calleeSig := call.Fun.Type()
	params := calleeSig.Params()
	for i, p := range params {
		if !p.IsOutBufParam() {
			continue
		}
		if i >= len(call.Args) {
			break
		}
		if call.Args[i].Op() != ir.ONIL {
			continue
		}
		// Replace nil with parent's outBuf Name. The Name is
		// already an unsafe.Pointer typed PPARAM, so no conv
		// needed.
		call.Args[i] = parentOutBuf
		// Only the first pointer-typed result is wired by the
		// escape spill, so stop after the first replacement.
		return nil
	}
	return nil
}

// findOutBufParamName returns fn's outBuf *ir.Name for the k-th
// pointer-typed result, or nil if fn doesn't carry that param.
// outBuf params are recognised by their Sym name prefix
// (types.OutBufNamePrefix) since IsOutBufParam is a *types.Field
// helper, not exposed on *ir.Name.
func findOutBufParamName(fn *ir.Func, k int) *ir.Name {
	if fn == nil {
		return nil
	}
	idx := 0
	for _, n := range fn.Dcl {
		if n == nil || n.Class != ir.PPARAM {
			continue
		}
		if n.Sym() == nil {
			continue
		}
		if !strings.HasPrefix(n.Sym().Name, types.OutBufNamePrefix) {
			continue
		}
		if idx == k {
			return n
		}
		idx++
	}
	return nil
}

// calleeIsExtended reports whether call's callee has Phase G's
// extended ABI (i.e., trailing outBuf params).
func calleeIsExtended(call *ir.CallExpr) bool {
	if call == nil || call.Fun == nil {
		return false
	}
	t := call.Fun.Type()
	if t == nil || t.Kind() != types.TFUNC {
		return false
	}
	return t.GdReturnOutBuf() && t.NumOutBufs() > 0
}
