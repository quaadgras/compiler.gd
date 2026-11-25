// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !wasm.split && !386 && !arm && !mips && !mipsle

package c23

// The types of C whose size is the data model's: here long has 64 bits
// (LP64).
type (
	Long    = int64
	ULong   = uint64
	Size    = uint64 // size_t
	SSize   = int64  // ssize_t
	Ptrdiff = int64  // ptrdiff_t
	Intptr  = int64  // intptr_t
	Uintptr = uint64 // uintptr_t
)
