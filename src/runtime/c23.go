// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/runtime/gc"
	"unsafe"
)

// Hooks for C compiled with derived capabilities (doc/gd/cc.md).
//
// C's heap objects begin with a header that holds their type and size.
// When C loads a pointer word that points into one, c23Object finds the
// object, so that the pointer gets the capability of the whole object back.

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
	if !s.spanclass.noscan() && !heapBitsInSpan(size) && s.spanclass.sizeclass() != 0 {
		base += gc.MallocHeaderSize
		size -= gc.MallocHeaderSize
	}
	if a < base {
		return nil, 0
	}
	return unsafe.Add(p, -int(a-base)), size
}

// c23Stack returns the bounds of the calling goroutine's stack.
//
//go:linkname c23Stack
func c23Stack() (lo, hi uintptr) {
	gp := getg()
	return gp.stack.lo, gp.stack.hi
}

// c23GTable returns the calling goroutine's side table slot.
//
//go:linkname c23GTable
func c23GTable() *unsafe.Pointer { return &getg().c23 }

// c23StackMove re-keys a goroutine's side table when copystack moves its
// stack: every address in [lo, hi) moves by delta. It runs on the system
// stack with the goroutine stopped, and must not allocate or write
// pointers.
var c23StackMove func(t unsafe.Pointer, lo, hi, delta uintptr)

//go:linkname c23SetStackMove
func c23SetStackMove(f func(t unsafe.Pointer, lo, hi, delta uintptr)) { c23StackMove = f }

// c23AllowIntegers lets a pointer-typed word hold an integer that is no
// address: C keeps integers in pointers ((void *)1, a union's int read as
// its pointer), which the collector and the stack copier otherwise stop
// the program for (debug.invalidptr). A program that has C in it runs
// with that off; such a word is then skipped, as GODEBUG=invalidptr=0
// has it.
//
//go:linkname c23AllowIntegers
func c23AllowIntegers() { debug.invalidptr = 0 }
