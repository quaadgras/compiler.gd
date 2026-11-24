// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.split

package c23

// Tag marks a pointer word whose address is not a whole value of the
// type it points to. With GOWASM=gc,split an address is under 1<<31
// (runtime's linLimit), so a tagged one is past the end of linear memory,
// and Go's access through it traps.
const Tag = 1 << 31

// splitModel: GOWASM=gc,split, where a block's array of references has an
// element for every word, whatever is in it (runtime's split.go).
const splitModel = true

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
