// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typecheck

import (
	"cmd/compile/internal/ir"
	"cmd/compile/internal/types"
)

// PhaseGActive aliases types.PhaseGActive so consumers in this
// package keep their familiar reference point without importing
// state across the types/typecheck boundary.
const PhaseGActive = types.PhaseGActive

// FillOutBufArgs ensures n.Args has a trailing nil for each
// synthesised outBuf param of the callee's signature. Called from
// tcCall after the callee is typechecked but before typecheckaste
// compares arg counts.
//
// With Phase G.2.1 the outBuf fields live in the real sig.Params()
// slice (NewSignature bakes them in). The user writes N args; we
// append K nils so typecheckaste sees N+K args for N+K params.
//
// A later walk-time pass (G.4) will replace selected nils with
// &stackBuf where the caller's escape analyser proved result
// locality; non-candidate sites stay nil and the callee falls
// through to heap alloc in runtime.maybeInPlace.
func FillOutBufArgs(n *ir.CallExpr, callee *types.Type) {
	if !PhaseGActive {
		return
	}
	if callee == nil || callee.Kind() != types.TFUNC {
		return
	}
	if !callee.GdReturnOutBuf() {
		return
	}
	nOut := callee.NumOutBufs()
	if nOut == 0 {
		return
	}
	// sig.Params() is [user0..user{N-1}, outBuf0..outBuf{K-1}].
	// User wrote N args; append K nils.
	params := callee.Params()
	userArgs := len(params) - nOut
	if len(n.Args) != userArgs {
		// Arity already mismatches user-visible expectations; let
		// typecheckaste produce its normal error. Don't mask.
		return
	}
	for i := 0; i < nOut; i++ {
		field := params[userArgs+i]
		nilArg := ir.NewNilExpr(n.Pos(), field.Type)
		nilArg.SetTypecheck(1)
		n.Args = append(n.Args, nilArg)
	}
}
