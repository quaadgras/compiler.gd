// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/runtime/gc"
	_ "unsafe"
)

// Hooks for C compiled with derived capabilities (doc/gd/cc.md).
//
// C's heap objects begin with a header that holds their type and size.
// When C loads a pointer word that points into one, c23Object finds the
// object, so that the pointer gets the capability of the whole object back.

// c23Object returns the base and size of the heap object that contains
// address p, or 0, 0 if p is not in an object in the heap. The base and
// size are those of the value, not of the allocation slot: an allocation
// header is excluded.
//
//go:linkname c23Object
func c23Object(p uintptr) (base, size uintptr) {
	s := spanOfHeap(p)
	if s == nil {
		return 0, 0
	}
	base = s.base() + s.objIndex(p)*s.elemsize
	size = s.elemsize
	if !s.spanclass.noscan() && !heapBitsInSpan(size) && s.spanclass.sizeclass() != 0 {
		base += gc.MallocHeaderSize
		size -= gc.MallocHeaderSize
	}
	if p < base {
		return 0, 0
	}
	return base, size
}
