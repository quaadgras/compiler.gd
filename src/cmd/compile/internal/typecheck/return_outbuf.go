// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typecheck

import (
	"cmd/compile/internal/ir"
	"cmd/compile/internal/types"
)

// PhaseGActive gates the return-value outBuf rewrite at the
// writer/reader level. When true, the writer sets ir.GdReturnOutBuf
// on eligible functions; the reader, on seeing the pragma, flips
// typeGdReturnOutBuf on the function's signature type. All downstream
// consumers that need the extended view consult the sig's
// GdReturnOutBuf() flag and the VirtualParams / VirtualRecvParams
// helpers; stock consumers (reflect, identity, shape, iface match)
// continue to use sig.Params()/Results() and see the unextended form.
const PhaseGActive = true

// FillOutBufArgs ensures n.Args has a trailing nil for each virtual
// outBuf param of the callee's signature. Called from tcCall after
// the callee is typechecked but before typecheckaste compares arg
// counts. No-op when the callee has no outBufs or when the caller's
// arg list already matches the extended arity.
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
	if callee == nil || callee.Kind() != types.TFUNC {
		return
	}
	nOut := callee.NumOutBufs()
	if nOut == 0 {
		return
	}
	userArgs := callee.NumParams()
	if len(n.Args) != userArgs {
		// Arity already mismatches user-visible expectations; let
		// typecheckaste produce its normal error. Don't mask.
		return
	}
	virtual := callee.VirtualParams()
	for i := 0; i < nOut; i++ {
		field := virtual[userArgs+i]
		nilArg := ir.NewNilExpr(n.Pos(), field.Type)
		nilArg.SetTypecheck(1)
		n.Args = append(n.Args, nilArg)
	}
}
