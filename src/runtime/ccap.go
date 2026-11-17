// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/goarch"
	"internal/runtime/gc"
	_ "unsafe"
)

// Hooks for C compiled with derived capabilities (doc/gd/cfront.md).
//
// A pointer that C holds is an address and the base of the object it was
// derived from. When C loads a pointer word from memory, or receives a
// pointer from Go, the base is not stored anywhere: it is derived from the
// address, which is valid because every untagged pointer word in the heap
// points into a live object. These functions answer the two questions such
// a derivation asks of the heap.

// ccapObject returns the base and size of the heap object that contains
// address p, or 0, 0 if p is not in an object in the heap. The base and
// size are those of the value, not of the allocation slot: an allocation
// header is excluded.
//
//go:linkname ccapObject
func ccapObject(p uintptr) (base, size uintptr) {
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

// ccapIsPtr reports whether the word at addr, which must be aligned and in
// a heap object, is one that the collector reads as a pointer.
//
//go:linkname ccapIsPtr
func ccapIsPtr(addr uintptr) bool {
	s := spanOfHeap(addr)
	if s == nil || s.spanclass.noscan() {
		return false
	}
	tp := s.typePointersOf(addr, goarch.PtrSize)
	_, p := tp.next(addr + goarch.PtrSize)
	return p == addr
}
