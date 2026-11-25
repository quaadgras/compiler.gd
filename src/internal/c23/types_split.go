// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.split

package c23

// The types of C whose size is the data model's: here long has 32 bits
// (ILP32).
type (
	Long    = int32
	ULong   = uint32
	Size    = uint32 // size_t
	SSize   = int32  // ssize_t
	Ptrdiff = int32  // ptrdiff_t
	Intptr  = int32  // intptr_t
	Uintptr = uint32 // uintptr_t
)
