// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package abi

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/types"
	"internal/buildcfg"
)

// gd fat-interface method calls.
//
// An interface method call passes the iface data word as the receiver
// and, after the method's own arguments, the two inline payload words
// as two float64 arguments. When those two trailing arguments land in
// registers, appending them leaves the stack layout of every other
// argument and result unchanged, so a callee that does not expect them
// (a boxed type's ordinary pointer-receiver method) simply ignores the
// registers. Inline and spread types instead put an adapter in the itab
// that rebuilds the receiver from the data word and the two payload
// words. This replaces staging the payload through a stack temporary at
// every call site and branching on itab.Inline.
//
// When the two payload words would not fit in registers (more float
// arguments than the ABI has registers for, or no register ABI at all),
// the call site keeps the staged pointer convention and the itab keeps
// the pointer-receiver wrapper. IfaceExtFits decides which, from the
// method signature alone, so the caller, the itab writer and reflect
// all agree. reflect mirrors this rule in reflect/abi.go.

// InternalRegCounts returns the number of integer and floating-point
// argument registers of ABIInternal on the target architecture. It
// matches ssa.Config's intParamRegs/floatParamRegs, which ssagen checks
// in InitConfig; it is a table here because wrappers and itabs are
// generated before the SSA configuration exists.
func InternalRegCounts() (ints, floats int) {
	switch buildcfg.GOARCH {
	case "amd64":
		return 9, 15
	case "arm64", "loong64", "riscv64":
		return 16, 16
	case "ppc64", "ppc64le":
		return 12, 12
	case "s390x":
		return 8, 16
	case "wasm":
		if buildcfg.GOWASM.GC {
			return 120, 120
		}
	}
	return 0, 0
}

// IfaceExtFits reports whether an interface call of a method with
// signature sig passes the inline payload in two trailing float
// registers. sig's receiver, if any, is ignored: the receiver word is
// always one pointer-shaped word.
func IfaceExtFits(gd *base.Invocation, sig *types.Type) bool {
	ni, nf := InternalRegCounts()
	if nf < 2 || types.PtrSize != 8 {
		return false
	}
	if buildcfg.GOWASM.GC {
		// A WebAssembly function cannot be called with arguments it
		// does not declare, as the method of a boxed type would be.
		return false
	}
	params := make([]*types.Type, 0, sig.NumParams()+3)
	params = append(params, types.Types[types.TUINTPTR])
	for _, p := range sig.Params() {
		params = append(params, p.Type)
	}
	f64 := types.Types[types.TFLOAT64]
	params = append(params, f64, f64)
	info := NewABIConfig(gd, ni, nf, 0, 1).ABIAnalyzeTypes(params, nil)
	in := info.InParams()
	return len(in[len(in)-1].Registers) == 1 && len(in[len(in)-2].Registers) == 1
}

// IfaceExtType reports whether values of type t, stored in an interface,
// are called through an inline-payload adapter.
func IfaceExtType(gd *base.Invocation, t *types.Type) bool {
	types.CalcSize(gd, t)
	return types.IsInlineIface(t) || types.IsSpreadIface(t)
}

// NumIntRegs returns the number of integer argument registers of config.
func (config *ABIConfig) NumIntRegs() int { return config.regAmounts.intRegs }

// NumFloatRegs returns the number of floating-point argument registers of config.
func (config *ABIConfig) NumFloatRegs() int { return config.regAmounts.floatRegs }
