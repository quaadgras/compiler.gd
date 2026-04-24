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
//
// CURRENTLY OFF — the per-function flag approach has a fundamental
// ABI mismatch bug with function-value assignments:
//
//   func NewFoo() *Foo { ... }       // flagged, body expects N+K args
//   var f func() *Foo = NewFoo        // f's Type is a fresh FuncType, flag NOT set
//   _ = f()                           // call site uses f's Type → passes N args
//                                     // but NewFoo's body expects N+K → garbage in reg N+1
//
// In a fork-compiled compile tool this manifests as corrupted map
// pointers (the garbage in the shifted register ends up looking like
// a *hmap with m.writing ≠ 0) — symptom is a concurrent-map-fatal
// on a single goroutine deep in backend code. all.bash trip Apr 24
// 2026 (full trace in race.txt) is the canonical repro.
//
// Fix requires propagating the flag through compatible FuncTypes so
// caller/callee can't disagree, or moving the ABI decision out of the
// type altogether (hidden ABI arg-count bit). Tracked for Phase G.2.1.
const PhaseGActive = false

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
