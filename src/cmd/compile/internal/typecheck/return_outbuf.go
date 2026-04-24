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

// outBufNamePrefix is the leading substring every synthesized
// outBufK param's Sym name shares. Consumers that need to recognise
// synthesised params (reflect.Type.In, call-site walk) match on this
// prefix. The leading `.` keeps the name unexportable so it can't
// shadow user identifiers.
const outBufNamePrefix = ".outBuf"

// AppendReturnOutBufs extends params with one synthesized `.outBufK *T`
// field for each pointer-typed result in results, in the order they
// appear. Returns the (possibly extended) params slice unchanged if
// the rewrite is disabled, the input has no pointer results, or the
// trailing params are already synthesized outBuf slots (idempotency
// guard for signatures read back from pkgbits that were serialised
// post-rewrite).
//
// The rewrite is universal: applied to every callable the noder
// decodes, including interface method declarations, so iface
// satisfaction continues to hold by construction.
//
// Package boundaries: runtime is intentionally skipped. Its
// asm-backed routines follow stock ABI, and even though a trailing
// outBuf arg would be silently ignored by asm that doesn't read it,
// the conservative default for V1 is to skip.
func AppendReturnOutBufs(params, results []*types.Field) []*types.Field {
	if !PhaseGActive {
		return params
	}
	if base.Flag.CompilingRuntime {
		return params
	}
	if !HasPtrResult(results) {
		return params
	}
	if paramsAlreadyHaveOutBufs(params, results) {
		return params
	}

	extended := make([]*types.Field, len(params), len(params)+NumPtrResults(results))
	copy(extended, params)

	k := 0
	for _, r := range results {
		if r.Type == nil || !r.Type.IsPtr() {
			continue
		}
		sym := types.LocalPkg.LookupNum(outBufNamePrefix, k)
		field := types.NewField(r.Pos, sym, r.Type)
		extended = append(extended, field)
		k++
	}
	return extended
}

// IsOutBufParam reports whether f was synthesised by
// AppendReturnOutBufs. Used by reflect and walk to distinguish
// rewritten-by-gd params from user-declared ones.
func IsOutBufParam(f *types.Field) bool {
	if f == nil || f.Sym == nil {
		return false
	}
	return strings.HasPrefix(f.Sym.Name, outBufNamePrefix)
}

// HasPtrResult reports whether any result is a direct pointer type.
func HasPtrResult(results []*types.Field) bool {
	for _, r := range results {
		if r.Type != nil && r.Type.IsPtr() {
			return true
		}
	}
	return false
}

// NumPtrResults counts direct pointer results.
func NumPtrResults(results []*types.Field) int {
	n := 0
	for _, r := range results {
		if r.Type != nil && r.Type.IsPtr() {
			n++
		}
	}
	return n
}

// NumTrailingOutBufParams counts the trailing .outBufK params on
// sigParams, i.e. the ones AppendReturnOutBufs added. Returns 0
// when the signature has no synthesised outBufs (or when PhaseGActive
// is false).
func NumTrailingOutBufParams(sigParams []*types.Field) int {
	n := 0
	for i := len(sigParams) - 1; i >= 0; i-- {
		if !IsOutBufParam(sigParams[i]) {
			break
		}
		n++
	}
	return n
}

// FillOutBufArgs ensures n.Args contains a trailing nil for each
// outBuf param of the callee's type. Called from tcCall after the
// callee is typechecked but before typecheckaste compares arg counts.
// No-op when the callee has no outBufs or args already match.
//
// The nil we insert is a typed ConstExpr matching the outBuf's *T
// type so downstream passes (escape analysis, SSA) treat it as a
// normal nil pointer. A later walk-time pass (G.4) will rewrite
// individual call sites to replace the nils with &stackBuf where the
// caller's escape analyzer proved result-locality.
func FillOutBufArgs(n *ir.CallExpr, callee *types.Type) {
	if !PhaseGActive {
		return
	}
	if callee == nil || callee.Kind() != types.TFUNC {
		return
	}
	sigParams := callee.Params()
	outBufs := NumTrailingOutBufParams(sigParams)
	if outBufs == 0 {
		return
	}
	userArgs := len(sigParams) - outBufs
	if len(n.Args) != userArgs {
		// Arity already mismatches user-visible expectations;
		// let typecheckaste produce its normal error. Don't mask.
		return
	}
	for i := 0; i < outBufs; i++ {
		field := sigParams[userArgs+i]
		nilArg := ir.NewNilExpr(n.Pos(), field.Type)
		n.Args = append(n.Args, nilArg)
	}
}

// paramsAlreadyHaveOutBufs reports whether params already ends in
// one synthesized outBuf slot per pointer result — i.e. the signature
// was serialised after a prior rewrite and reading it again would
// double the slots.
//
// Match is exact on both the count of trailing outBuf-named params
// and their position (contiguous tail). A user who somehow named a
// param `.outBufK` would fail that position check; no collision.
func paramsAlreadyHaveOutBufs(params, results []*types.Field) bool {
	need := NumPtrResults(results)
	if len(params) < need {
		return false
	}
	tail := params[len(params)-need:]
	for _, p := range tail {
		if !IsOutBufParam(p) {
			return false
		}
	}
	return true
}
