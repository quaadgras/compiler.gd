// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typecheck

import (
	"strings"

	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/types"
)

// PhaseGActive gates the return-value outBuf rewrite. When false,
// the noder's signature append is a no-op and call-site nil-fill is
// skipped; signatures pass through unchanged. When true, every
// pointer-returning signature gains one trailing `outBufK *T`
// parameter per pointer-typed result, and every call to such a
// function gets nil args auto-appended for the outBufs a later walk
// pass hasn't yet filled with `&buf`. See
// doc/gd/escape-bits-phase-g-plan.md.
//
// V1 keeps this off while the rest of Phase G lands in pieces. Flip
// for local testing via a compile-tool rebuild.
const PhaseGActive = false

// AppendReturnOutBufs extends params with one synthesized
// `.outBufK *T` field for each pointer-typed result in results.
// No-op when disabled, when results have no pointers, when the
// signature is ineligible (see phaseGEligible), or when params
// already ends in a matching outBuf tail.
//
// Gate recv arg is the receiver field of the signature being built
// (nil for bare functions) so we can skip methods in V1.
func AppendReturnOutBufs(recv *types.Field, params, results []*types.Field) []*types.Field {
	if !phaseGEligible(recv, params, results) {
		return params
	}
	if paramsAlreadyHaveOutBufs(params, results) {
		return params
	}

	extended := make([]*types.Field, len(params), len(params)+numPtrResults(results))
	copy(extended, params)

	k := 0
	for _, r := range results {
		if r.Type == nil || !r.Type.IsPtr() {
			continue
		}
		sym := types.LocalPkg.LookupNum(types.OutBufNamePrefix, k)
		field := types.NewField(r.Pos, sym, r.Type)
		extended = append(extended, field)
		k++
	}
	return extended
}

// phaseGEligible decides whether a freshly-decoded signature gets
// outBufs appended. Centralises every V1 gate so the rule set is in
// one place.
func phaseGEligible(recv *types.Field, params, results []*types.Field) bool {
	if !PhaseGActive {
		return false
	}
	if base.Flag.CompilingRuntime {
		// Runtime has asm entry points that expect stock ABI, and
		// its Go funcs are interleaved with those asm routines in
		// ways that make a universal rewrite fragile (tests hang on
		// cgo/preemption paths even though the code compiles). Skip
		// entirely. User packages that import runtime funcs run
		// through a separate strip step at funcExt/obj time.
		return false
	}
	if !hasPtrResult(results) {
		return false
	}
	if hasDictParam(params) {
		// Generic instantiation carries a runtime-dict param we
		// don't want to reshape around yet. Defer.
		return false
	}
	if len(params) > 0 && params[len(params)-1].IsDDD() {
		// Variadic: the last param is a slice with trailing-arg
		// semantics. Appending an outBuf after the variadic would
		// place it in the slice; placing it before requires ABI
		// rework. Defer.
		return false
	}
	return true
}

// FillOutBufArgs ensures n.Args has a trailing nil for each outBuf
// param of the callee's signature, so typecheckaste's arity check
// passes. Called from tcCall after the callee is typechecked but
// before typecheckaste. No-op when callee has no outBufs.
//
// The nil we insert is a typed NilExpr matching the outBuf's *T
// type. A later walk-time pass (G.4) will replace selected nils
// with &stackBuf where the caller's escape analyser proved result
// locality; non-candidate sites stay nil and the callee falls
// through to heap alloc in runtime.maybeInPlace.
func FillOutBufArgs(n *ir.CallExpr, callee *types.Type) {
	if !PhaseGActive {
		return
	}
	nOut := callee.NumOutBufs()
	if nOut == 0 {
		return
	}
	userArgs := callee.NumParams() - nOut
	if len(n.Args) != userArgs {
		// Arity already mismatches user-visible expectations; let
		// typecheckaste produce its normal error. Don't mask.
		return
	}
	outBufs := callee.OutBufs()
	for _, field := range outBufs {
		nilArg := ir.NewNilExpr(n.Pos(), field.Type)
		nilArg.SetTypecheck(1)
		n.Args = append(n.Args, nilArg)
	}
}

// IsOutBufParam is a thin forwarding helper for callers that don't
// have a *types.Type in hand (e.g. the noder's filter loops).
// Prefer the receiver-method form (f.IsOutBufParam()) when possible.
func IsOutBufParam(f *types.Field) bool {
	return f.IsOutBufParam()
}

func hasPtrResult(results []*types.Field) bool {
	for _, r := range results {
		if r.Type != nil && r.Type.IsPtr() {
			return true
		}
	}
	return false
}

func numPtrResults(results []*types.Field) int {
	n := 0
	for _, r := range results {
		if r.Type != nil && r.Type.IsPtr() {
			n++
		}
	}
	return n
}

// hasDictParam reports whether params contains a runtime-dictionary
// param (`.dict` prefix). Generics instantiation inserts such a
// param between the receiver and the user-declared params; while we
// defer generic-method support, skipping the rewrite for any
// signature that has a dict keeps those callees stock.
func hasDictParam(params []*types.Field) bool {
	for _, p := range params {
		if p != nil && p.Sym != nil && strings.HasPrefix(p.Sym.Name, ".dict") {
			return true
		}
	}
	return false
}

// paramsAlreadyHaveOutBufs reports whether params already ends in
// one synthesised outBuf slot per pointer result — i.e. the
// signature was serialised after a prior rewrite and reading it
// again would double the slots.
func paramsAlreadyHaveOutBufs(params, results []*types.Field) bool {
	need := numPtrResults(results)
	if len(params) < need {
		return false
	}
	tail := params[len(params)-need:]
	for _, p := range tail {
		if !p.IsOutBufParam() {
			return false
		}
	}
	return true
}
