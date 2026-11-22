// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build 386 || amd64 || s390x || arm || arm64 || loong64 || ppc64 || ppc64le || mips || mipsle || (wasm && !wasm.gc) || mips64 || mips64le || riscv64

package bytealg

import "unsafe"

// (No race instrumentation, as the assembly has none: the runtime calls
// it before it may.)
//
//go:norace
func Compare(a, b []byte) int {
	return compareSlices(unsafe.SliceData(a), len(a), cap(a), unsafe.SliceData(b), len(b), cap(b))
}

// The assembly takes the words of stock Go's slices, the array, the
// length and the capacity: with 8-byte words a slice is packed, or is a
// huge one's header (doc/gd/slice16.md), which it would not read.
//
//go:noescape
func compareSlices(a *byte, alen, acap int, b *byte, blen, bcap int) int

func CompareString(a, b string) int {
	return abigen_runtime_cmpstring(a, b)
}

// The declaration below generates ABI wrappers for functions
// implemented in assembly in this package but declared in another
// package.

//go:linkname abigen_runtime_cmpstring runtime.cmpstring
func abigen_runtime_cmpstring(a, b string) int
