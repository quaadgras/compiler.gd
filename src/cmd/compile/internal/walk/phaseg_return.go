// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package walk

import (
	"cmd/compile/internal/ir"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
)

// phaseGReturnRewrite recognizes pointer-result return shapes that
// can be redirected through runtime.maybeInPlace, replacing the
// per-call heap allocation with the caller's stack buffer when one
// was supplied at the call site (or the heap when not — the helper
// handles both).
//
// First-cut shapes recognised (no escape-graph dependency):
//
//   1. return new(T)              — single ONEW result
//   2. return &T{...}             — single OPTRLIT result
//
// In each case the returned pointer's underlying storage is
// provably local: there is no other reference to it inside the
// callee, so binding it to outBuf_k is safe by construction.
//
// Transform: synthesise a heap-promoted T-typed temp tagged with
// OutBufResultIdx=k+1, hoist the (zero-valued for new(T), or
// composite-literal-valued for OPTRLIT) initialisation as an
// assignment, and replace the return value with &temp. The
// existing newHeapaddr→maybeInPlace hook in ssagen then routes
// the temp's allocation through the runtime helper.
//
// Constraint: at most one tagged temp per outBuf — addressed by
// only recognising single-return functions in this first cut.
// Multi-return + return &local + return p land in later cuts.
func phaseGReturnRewrite(fn *ir.Func) {
	sig := fn.Type()
	if sig == nil || !sig.GdReturnOutBuf() {
		return
	}
	results := sig.Results()
	nOutBufs := sig.NumOutBufs()
	if nOutBufs == 0 || len(results) == 0 {
		return
	}

	body := fn.Body
	if len(body) == 0 {
		return
	}
	last := body[len(body)-1]
	ret, ok := last.(*ir.ReturnStmt)
	if !ok {
		return
	}
	if len(ret.Results) != len(results) {
		return
	}
	for i := 0; i < len(body)-1; i++ {
		if containsReturn(body[i]) {
			return
		}
	}

	var pre ir.Nodes
	// `order` may have hoisted `new(T)` into `autotmp = new(T); return autotmp`.
	// Track the indices of body statements that supplied a result expression
	// via such a hoisted assignment so we can drop them from the new body.
	dropIdx := make(map[int]bool)
	resultIdx := 0
	rewriteAny := false
	for i, r := range results {
		if r == nil || r.Type == nil || !r.Type.IsPtr() {
			continue
		}
		resultIdx++
		retExpr := ret.Results[i]
		// If the return expression is just an autotmp Name, look for the
		// immediately-preceding `autotmp = <alloc>` and fold it.
		retExpr, foldedIdx := unwrapHoistedAlloc(body, retExpr)
		if foldedIdx >= 0 {
			dropIdx[foldedIdx] = true
		}

		tmp, init := phaseGAllocPattern(fn, retExpr, r.Type.Elem())
		if tmp == nil {
			continue
		}
		tmp.OutBufResultIdx = uint8(resultIdx)
		tmp.SetEsc(ir.EscHeap)
		tmp.SetEscCandidate(false)
		tmp.SetAddrtaken(true)

		decl := ir.NewDecl(retExpr.Pos(), ir.ODCL, tmp)
		decl.SetTypecheck(1)
		pre.Append(decl)
		if init != nil {
			pre.Append(init)
		}
		addr := typecheck.NodAddrAt(retExpr.Pos(), tmp)
		addr.SetType(r.Type)
		addr.SetTypecheck(1)
		ret.Results[i] = addr
		rewriteAny = true
	}
	if !rewriteAny {
		return
	}

	newBody := make([]ir.Node, 0, len(body)-1+len(pre)+1)
	for i := 0; i < len(body)-1; i++ {
		if dropIdx[i] {
			continue
		}
		newBody = append(newBody, body[i])
	}
	newBody = append(newBody, pre...)
	newBody = append(newBody, ret)
	fn.Body = newBody
}

// unwrapHoistedAlloc detects the pattern produced by `order` for
// `return new(T)` / `return &Lit{}` outside an OAS context: a
// preceding statement of the form `autotmp = <alloc>` whose lhs
// name is what the return references. When found, returns the
// underlying allocation expression and the body index of the
// hoisted assignment (so the caller can splice it out). Otherwise
// returns (retExpr, -1) unchanged.
func unwrapHoistedAlloc(body ir.Nodes, retExpr ir.Node) (ir.Node, int) {
	name, ok := retExpr.(*ir.Name)
	if !ok || !name.AutoTemp() {
		return retExpr, -1
	}
	// Walk backwards from the return through the body; the hoisted
	// alloc is typically the last assignment to name.
	for i := len(body) - 2; i >= 0; i-- {
		as, ok := body[i].(*ir.AssignStmt)
		if !ok {
			continue
		}
		if lhs, ok := as.X.(*ir.Name); !ok || lhs != name {
			continue
		}
		switch as.Y.Op() {
		case ir.ONEW, ir.OPTRLIT:
			return as.Y, i
		}
		return retExpr, -1
	}
	return retExpr, -1
}

// phaseGAllocPattern returns (tmp, init) when retExpr matches one
// of the first-cut allocation patterns, where tmp is a freshly
// declared T-typed local and init (if non-nil) is the assignment
// statement that fills it from a composite literal. Returns
// (nil, nil) when no pattern matches.
func phaseGAllocPattern(fn *ir.Func, retExpr ir.Node, elemT *types.Type) (*ir.Name, ir.Node) {
	switch retExpr.Op() {
	case ir.ONEW:
		nw := retExpr.(*ir.UnaryExpr)
		if nw.X == nil || nw.X.Type() == nil {
			return nil, nil
		}
		if !types.Identical(nw.X.Type(), elemT) {
			return nil, nil
		}
		tmp := typecheck.TempAt(retExpr.Pos(), fn, elemT)
		return tmp, nil

	case ir.OPTRLIT:
		ad := retExpr.(*ir.AddrExpr)
		if ad.X == nil || ad.X.Type() == nil {
			return nil, nil
		}
		if !types.Identical(ad.X.Type(), elemT) {
			return nil, nil
		}
		tmp := typecheck.TempAt(retExpr.Pos(), fn, elemT)
		as := ir.NewAssignStmt(retExpr.Pos(), tmp, ad.X)
		as.SetTypecheck(1)
		return tmp, as
	}
	return nil, nil
}

// containsReturn reports whether n contains an ORETURN anywhere
// in its subtree. Used by the first-cut rewriter to bail out on
// any function with embedded returns until multi-exit support
// lands.
func containsReturn(n ir.Node) bool {
	found := false
	ir.Visit(n, func(x ir.Node) {
		if !found && x.Op() == ir.ORETURN {
			found = true
		}
	})
	return found
}
