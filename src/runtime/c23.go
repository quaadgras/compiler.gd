// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/runtime/gc"
	"unsafe"
)

// Hooks for C translated by cmd/cc (internal/c23, doc/gd/cc.md).

// c23Object returns the base and size of the heap object that p points
// into, the base as a pointer derived from p; nil, 0 if p is not into the
// heap. The base and size are those of the value, not of the allocation
// slot: an allocation header is excluded.
//
//go:linkname c23Object
func c23Object(p unsafe.Pointer) (unsafe.Pointer, uintptr) {
	a := uintptr(p)
	s := spanOfHeap(a)
	if s == nil {
		return nil, 0
	}
	base := s.base() + s.objIndex(a)*s.elemsize
	size := s.elemsize
	if s.spanclass.c() {
		// What malloc gave ends where it was asked to.
		size = s.cSizeOf(s.objIndex(a))
	}
	if !s.spanclass.noscan() && !heapBitsInSpan(size) && s.spanclass.sizeclass() != 0 {
		base += gc.MallocHeaderSize
		size -= gc.MallocHeaderSize
	}
	if a < base {
		return nil, 0
	}
	return unsafe.Add(p, -int(a-base)), size
}

// c23GTable returns the calling goroutine's slot for what internal/c23
// keeps for it.
//
//go:linkname c23GTable
func c23GTable() *unsafe.Pointer { return &getg().c23 }

// c23AllowIntegers lets a pointer-typed word hold an integer that is no
// address: C keeps integers in pointers ((void *)1, a union's int read as
// its pointer), which the collector and the stack copier otherwise stop
// the program for (debug.invalidptr). A program that has C in it runs
// with that off; such a word is then skipped, as GODEBUG=invalidptr=0
// has it.
//
//go:linkname c23AllowIntegers
func c23AllowIntegers() { debug.invalidptr = 0 }
