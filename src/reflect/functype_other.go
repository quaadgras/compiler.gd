// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !wasm.gc

package reflect

import (
	"internal/abi"
	"unsafe"
)

func newFuncType(n int) (*funcType, []*rtype) { return newFuncTypeStruct(n) }

const wasmGCMode = false

func callWasmGC(op string, t *funcType, fn unsafe.Pointer, rcvr Value, rcvrtype *abi.Type, in []Value) []Value {
	panic("unreachable")
}

func makeFuncWasmGC(t *abi.Type, ftyp *funcType, fn func([]Value) []Value) Value {
	panic("unreachable")
}

func linearType[T any](t *T) *T { return t }

func gcMethodFunc(t *rtype, i int, tfn unsafe.Pointer) unsafe.Pointer { panic("unreachable") }

func gcTypeEqual(t *abi.Type, p, q unsafe.Pointer) bool { return t.Equal(p, q) }

// (See functype_wasmgc.go.)
var gcDeferred, gcPassDeferred bool
