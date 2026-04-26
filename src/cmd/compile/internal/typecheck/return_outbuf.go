// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typecheck

import (
	"cmd/compile/internal/base"
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
func FillOutBufArgs(gd *base.Invocation, n *ir.CallExpr, callee *types.Type) {
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
	// sig.Params() layout:
	//   non-variadic: [user0..user{N-1}, outBuf0..outBuf{K-1}]
	//   variadic:     [user0..user{N-2}, outBuf0..outBuf{K-1},
	//                  variadic_slice]
	//
	// Find the first outBuf position — that's where we splice in
	// the matching nil args.
	params := callee.Params()
	outBufStart := -1
	for i, p := range params {
		if p != nil && p.IsOutBufParam() {
			outBufStart = i
			break
		}
	}
	if outBufStart < 0 {
		return // unexpected: flagged but no outBuf fields
	}
	// User-visible mandatory arg count: params BEFORE the outBufs.
	userMandatory := outBufStart
	variadic := len(params) > 0 && params[len(params)-1].IsDDD()
	if variadic {
		if len(n.Args) < userMandatory {
			// Too few args; let typecheckaste error.
			return
		}
	} else {
		if len(n.Args) != userMandatory {
			// Arity already mismatches user-visible expectations;
			// let typecheckaste error without masking.
			return
		}
	}
	// Splice K nils into n.Args at position userMandatory.
	nils := make([]ir.Node, nOut)
	for i := range nils {
		field := params[outBufStart+i]
		nilArg := ir.NewNilExpr(gd, n.Pos(), field.Type)
		nilArg.SetTypecheck(1)
		nils[i] = nilArg
	}
	newArgs := make([]ir.Node, 0, len(n.Args)+nOut)
	newArgs = append(newArgs, n.Args[:userMandatory]...)
	newArgs = append(newArgs, nils...)
	newArgs = append(newArgs, n.Args[userMandatory:]...)
	n.Args = newArgs
}
