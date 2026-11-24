// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !wasm.split && !386 && !arm && !mips && !mipsle

package c23

// Tag marks a pointer word whose address is not a whole value of the
// type it points to: not canonical, so it faults where Go dereferences it
// (on wasm, where addresses are 32 bits, the compiler's nil checks catch
// it), and the collector does not take it for a pointer.
const Tag = 1 << 55

// splitModel: GOWASM=gc,split (tag_split.go).
const splitModel = false

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
