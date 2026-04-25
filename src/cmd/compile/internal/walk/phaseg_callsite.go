// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package walk

import (
	"strings"

	"cmd/compile/internal/base"
	"cmd/compile/internal/escape"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
)

// phaseGCallSiteRewrite scans fn for calls to Phase-G-extended
// functions and replaces each trailing nil outBuf arg with the
// address of a fresh stack-allocated buffer when the call's
// corresponding result is provably non-escaping in the caller.
//
// For now, two assignment shapes are recognised:
//
//   1. autotmp_X = callee(args...)               — single-result OAS
//   2. autotmp_A, autotmp_B = callee(args...)    — multi-result OAS2
//
// In each case, when the lhs name's escape verdict is EscNone, the
// matching outBuf nil arg is replaced with unsafe.Pointer(&tmp_k),
// where tmp_k is a freshly declared T_k autotmp. A zeroing
// assignment `tmp_k = T_k{}` is spliced into the enclosing block
// *before* the call's assignment statement — liveness requires an
// explicit write before any address-take of a pointer-bearing
// stack local.
//
// Sites whose result doesn't fit one of the recognised shapes
// (or whose result escapes) keep their nil outBuf and fall through
// to the heap path inside maybeInPlace — same cost as stock.
func phaseGCallSiteRewrite(fn *ir.Func) {
	if !types.PhaseGActive {
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
	if base.Ctxt.Pkgpath == "runtime" ||
		strings.HasPrefix(base.Ctxt.Pkgpath, "internal/runtime/") ||
		base.Ctxt.Pkgpath == "syscall" {
		return
	}
	rewriteStmts(fn, &fn.Body)
}

// rewriteStmts walks stmts in place, rewriting recognised call
// sites and splicing zero-init statements before each rewritten
// assignment. Recurses into block-bearing children so calls
// inside if/for bodies get the same treatment.
func rewriteStmts(fn *ir.Func, stmts *ir.Nodes) {
	var rebuilt ir.Nodes
	changed := false
	for _, s := range *stmts {
		var pre []ir.Node
		switch s := s.(type) {
		case *ir.AssignStmt:
			pre = tryRewriteSingleAssign(fn, s)
		case *ir.AssignListStmt:
			pre = tryRewriteListAssign(fn, s)
		}
		// Recurse into control-flow children AFTER handling this
		// statement so the containing-block rewrite is already in
		// place before descending.
		recurseIntoBlocks(fn, s)
		if len(pre) > 0 {
			changed = true
			rebuilt = append(rebuilt, pre...)
		}
		rebuilt = append(rebuilt, s)
	}
	if changed {
		*stmts = rebuilt
	}
}

// recurseIntoBlocks finds block-bearing children of n and
// recursively rewrites each block's statement list.
func recurseIntoBlocks(fn *ir.Func, n ir.Node) {
	switch n := n.(type) {
	case *ir.IfStmt:
		rewriteStmts(fn, &n.Body)
		rewriteStmts(fn, &n.Else)
	case *ir.ForStmt:
		rewriteStmts(fn, &n.Body)
	case *ir.RangeStmt:
		rewriteStmts(fn, &n.Body)
	case *ir.SwitchStmt:
		for _, cc := range n.Cases {
			rewriteStmts(fn, &cc.Body)
		}
	case *ir.SelectStmt:
		for _, cc := range n.Cases {
			rewriteStmts(fn, &cc.Body)
		}
	case *ir.BlockStmt:
		rewriteStmts(fn, &n.List)
	}
}

// tryRewriteSingleAssign handles `autotmp = callee(args...)`.
// Returns the pre-statements (zero-init) to splice into the
// enclosing block ahead of as.
func tryRewriteSingleAssign(fn *ir.Func, as *ir.AssignStmt) []ir.Node {
	call, ok := as.Y.(*ir.CallExpr)
	if !ok {
		return nil
	}
	if !calleeIsExtended(call) {
		return nil
	}
	lhs, ok := as.X.(*ir.Name)
	if !ok {
		return nil
	}
	// CRITICAL safety check: lhs.Esc() only encodes whether lhs's
	// STORAGE needs heap (e.g. if &lhs is taken and the address
	// escapes). For a `*T` local receiving a factory's result,
	// lhs.Esc() is EscNone even when the VALUE (the pointer bits)
	// later flows to a heap sink via `return lhs`, `global = lhs`,
	// etc. Stack-buffering under such uses would produce a heap
	// pointer into our soon-to-die stack frame.
	//
	// Instead, we consult the CALL expression's Esc(), populated by
	// escape analysis's Phase-G hook (see escape/call.go). The hook
	// creates a synthetic spill location at the call site that
	// stands in for the allocation the callee would normally
	// perform; attrEscapes on that loc after solve means the
	// allocation needs heap. Only a call that escape analysis has
	// explicitly confirmed non-escaping (Esc==EscNone) is safe to
	// stack-buffer — the default (EscUnknown) is treated as escape.
	if call.Esc() == ir.EscNone {
		return rewriteOutBufNils(fn, call, []*ir.Name{lhs})
	}
	// Chain-fold: result escapes only via fn's own k-th return.
	// Forward fn's outBuf_k as the call's outBuf — the caller's
	// stack buffer (or nil) propagates through.
	if call.GdForwardOutBufResult > 0 {
		return forwardParentOutBuf(fn, call, int(call.GdForwardOutBufResult)-1)
	}
	return nil
}

// tryRewriteListAssign handles `a, b = callee(args...)`.
func tryRewriteListAssign(fn *ir.Func, as *ir.AssignListStmt) []ir.Node {
	if len(as.Rhs) != 1 {
		return nil
	}
	call, ok := as.Rhs[0].(*ir.CallExpr)
	if !ok {
		return nil
	}
	if !calleeIsExtended(call) {
		return nil
	}
	lhsNames := make([]*ir.Name, len(as.Lhs))
	for i, l := range as.Lhs {
		n, ok := l.(*ir.Name)
		if !ok {
			return nil
		}
		lhsNames[i] = n
	}
	if call.Esc() == ir.EscNone {
		return rewriteOutBufNils(fn, call, lhsNames)
	}
	if call.GdForwardOutBufResult > 0 {
		return forwardParentOutBuf(fn, call, int(call.GdForwardOutBufResult)-1)
	}
	return nil
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

// sigHasPointerInputs reports whether any param or receiver of
// sig carries pointer state into the callee — pointer, slice,
// map, chan, interface, func, string, or a struct/array that
// transitively contains one. Scalar-only inputs (int, float,
// bool, …) can't alias any pointer-typed result.
func sigHasPointerInputs(sig *types.Type) bool {
	if sig == nil || sig.Kind() != types.TFUNC {
		return false
	}
	if r := sig.Recv(); r != nil && r.Type != nil && r.Type.HasPointers() {
		return true
	}
	for _, p := range sig.Params() {
		if p == nil || p.Type == nil {
			continue
		}
		if p.IsOutBufParam() {
			continue
		}
		if p.Type.HasPointers() {
			return true
		}
	}
	return false
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

// rewriteOutBufNils walks the call's args, locates trailing
// outBuf nils, and replaces each with unsafe.Pointer(&tmp_k)
// when the corresponding lhs (lhs[k] for the k-th pointer result)
// is non-escaping. Returns a list of zero-init statements that
// must be spliced into the enclosing block before the call's
// assignment — liveness requires an explicit write before any
// address-take of a pointer-bearing stack local.
func rewriteOutBufNils(fn *ir.Func, call *ir.CallExpr, lhs []*ir.Name) []ir.Node {
	sig := call.Fun.Type()
	params := sig.Params()
	results := sig.Results()
	// Map result index → lhs index. With Phase G the K-th outBuf
	// corresponds to the K-th pointer-typed result.
	resultToLhs := make(map[int]*ir.Name, len(lhs))
	for ri, r := range results {
		if ri >= len(lhs) {
			break
		}
		if r != nil && r.Type != nil && r.Type.IsPtr() {
			resultToLhs[ri] = lhs[ri]
		}
	}
	outBufToResult := make(map[int]int, sig.NumOutBufs())
	{
		k := 0
		for ri, r := range results {
			if r != nil && r.Type != nil && r.Type.IsPtr() {
				outBufToResult[k] = ri
				k++
			}
		}
	}

	var pre []ir.Node
	outBufK := 0
	for i, p := range params {
		if !p.IsOutBufParam() {
			continue
		}
		if i >= len(call.Args) {
			break
		}
		arg := call.Args[i]
		if arg.Op() != ir.ONIL {
			outBufK++
			continue
		}
		ri, ok := outBufToResult[outBufK]
		outBufK++
		if !ok {
			continue
		}
		dst, ok := resultToLhs[ri]
		if !ok || dst == nil {
			continue
		}
		if !ir.StackAllocatable(dst.Esc()) {
			continue
		}
		// Second-side check: the callee's result k must be a fresh
		// allocation, i.e. no parameter (or receiver) of sig is
		// tagged "leaks to result k" in escape analysis. Otherwise
		// the callee may return an existing input pointer whose
		// lifetime isn't tied to our stack frame, and stack-
		// buffering would either be wasted (buf unused) or unsafe
		// if the callee filled buf and *also* aliased an input.
		if escape.ResultAliasesParam(sig, ri) {
			continue
		}
		// Extra strictness (first cut): require that NO param/recv
		// of sig is pointer-containing. This rules out every form
		// of input→result aliasing by construction, even across
		// escape-tag limitations (tag rounding, Optimize(), cross-
		// package info loss). Factory patterns that take only
		// scalar args (size, flags) are still in scope — which is
		// the `func New() *T` / `func NewN(n int) *T` family.
		if sigHasPointerInputs(sig) {
			continue
		}
		ptrT := results[ri].Type
		if ptrT == nil || !ptrT.IsPtr() {
			continue
		}
		underlying := ptrT.Elem()
		if underlying == nil {
			continue
		}

		// Build the stack buffer: a fresh PAUTO of type T. Mark
		// Addrtaken so downstream passes (liveness/stack map) see
		// it as a pointer-containing stack slot if T has pointers.
		bufName := typecheck.TempAt(call.Pos(), fn, underlying)
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
		if underlying.HasPointers() {
			bufName.SetNeedzero(true)
		}

		// Emit the zero-init as a plain OAS with a nil RHS — that's
		// the internal form of "var x T" / "x = <zero>". It doesn't
		// need recursive walking; walkStmtList will lower it
		// naturally when it reaches this position.
		zeroAs := ir.NewAssignStmt(call.Pos(), bufName, nil)
		zeroAs.SetTypecheck(1)
		pre = append(pre, zeroAs)

		// Replace the nil outBuf arg with unsafe.Pointer(&bufName).
		addr := typecheck.NodAddrAt(call.Pos(), bufName)
		addr.SetType(types.NewPtr(underlying))
		addr.SetTypecheck(1)
		conv := ir.NewConvExpr(call.Pos(), ir.OCONVNOP, types.Types[types.TUNSAFEPTR], addr)
		conv.SetTypecheck(1)
		call.Args[i] = conv
	}
	return pre
}
