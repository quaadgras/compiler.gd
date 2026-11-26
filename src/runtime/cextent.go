// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/goarch"
	"unsafe"
)

// What C's library asks of the pointers that it is given (internal/c23,
// doc/gd/cc.md).
//
// The compiler checks the arithmetic and the conversions of the library
// as it checks those of the C that calls it. That is not enough for a
// function that is given a pointer and a length and hands both to code
// that no check is in: to memmove, to copy, to a read of a file. Such a
// function asks here how far the object goes.

// c23Extent returns the number of bytes before p, and from p on, in what
// p points into, and whether that is something that C may look at as
// bytes:
//
//   - an object of C's of the heap (cspan.go), as far as it was asked
//     for: not the byte after it;
//   - an object of Go's of the heap, its slot, if it holds no string,
//     slice or interface: those are not bytes to C, and what the
//     library keeps for itself (internal/c23's Handle) is made of them;
//   - a variable of a frame of the goroutine's stack, or of the
//     program's data, or the bytes of a string of the program's
//     (stackvar.go).
//
//go:linkname c23Extent
func c23Extent(p unsafe.Pointer) (before, after uintptr, ok bool) {
	// (A pointer that nothing is loaded through as what its type says
	// is one to bytes all the same: cRevive.)
	a := uintptr(cRevive(p))
	if gp := getg(); gp.stack.lo <= a && a < gp.stack.hi {
		// A variable of a frame (stackvar.go): what is in none that
		// is known is not C's to look at.
		base, size, _ := cObjectOf(a)
		if size == 0 {
			return 0, 0, false
		}
		return a - base, size - (a - base), true
	}
	if cInData(a) {
		// A variable of the program's data, or the bytes of a
		// string: a format, a name. (It is in no span.)
		if base, size, _, _, state := checkptrVar(a); state == varFound && size != 0 {
			return a - base, size - (a - base), true
		}
		return 0, 0, false
	}
	if _, base, size, ok := cHeaderOf(a); ok {
		// An object of C's, of a span that the P remembers.
		if a-base > size {
			return 0, 0, false
		}
		return a - base, size - (a - base), true
	}
	if s := spanOfHeap(a); s != nil {
		if s.isUserArenaChunk {
			return 0, 0, false
		}
		i := s.objIndex(a)
		base := s.base() + i*s.elemsize
		if s.spanclass.c() {
			size := s.cSizeOf(i)
			if a-base > size {
				return 0, 0, false
			}
			return a - base, size - (a - base), true
		}
		if cGoObjectWhole(s, base) {
			return 0, 0, false
		}
		return a - base, s.elemsize - (a - base), true
	}
	// A variable of the program's data, or the bytes of a string.
	if base, size, kind := cObjectOf(a); kind == cObjData && size != 0 {
		return a - base, size - (a - base), true
	}
	return 0, 0, false
}

// c23BytesStored is called before code that the compiler does not see
// as storing in C's memory writes bytes to the n bytes at p, which are
// in one object (c23Extent): what copy writes there, what the system
// reads there from a file. It is the one place that is told of such
// stores (internal/c23's Writable).
//
// The pointers that are written over are the collector's to see first,
// as c23Memset's are. (That holds if the write is made before the
// collector next starts to mark: a read that blocks first is not held
// to it. And nothing is done yet about the claims of the words, which
// may be for pointers that the bytes now are not: cclaim.go.)
//
//go:linkname c23BytesStored
//go:nosplit
func c23BytesStored(p unsafe.Pointer, n uintptr) {
	if n == 0 {
		return
	}
	// Bytes are not written over a word that holds a pointer.
	c23ByteStore(p, n)
	if !writeBarrier.enabled {
		return
	}
	const word = goarch.PtrSize
	d := uintptr(p)
	lo, hi := alignDown(d, word), alignUp(d+n, word)
	if s := cSpanOf(d); s != nil {
		if b := s.cBitsOf(); b != nil {
			cShade(b, (lo-s.base())/word, lo, 0, (min(hi, s.limit)-lo)/word)
		}
		return
	}
	bulkBarrierPreWrite(lo, 0, hi-lo, nil)
}

// cGoObjectWhole reports whether the object at base of s, a span of Go's
// objects, holds a string, a slice or an interface, or is free.
func cGoObjectWhole(s *mspan, base uintptr) bool {
	if s.spanclass.noscan() {
		return false
	}
	if s.isFree(s.objIndex(base)) {
		return true
	}
	if typ, _ := checkptrObjectType(s, base); typ != nil {
		return typ.HoldsWhole()
	}
	// A small object of a type that has no number: the states of its
	// words (mwholebits.go).
	if bits := s.wholeBitsOf(); wholeBitsEnabled && bits != nil && base+s.elemsize <= s.limit {
		const word = goarch.PtrSize
		o, n := (base-s.base())/word, s.elemsize/word
		for n > 0 {
			k := min(n, 64)
			if wholeBitsGet(&bits.hi, o, k)|wholeBitsGet(&bits.lo, o, k) != 0 {
				return true
			}
			o, n = o+k, n-k
		}
	}
	return false
}

// c23IsObject reports whether p is the address of an object of Go's of
// the heap that was allocated as a value of type t, which has a pointer
// in it: the object itself, not something inside it or after it. It is
// how C's library knows what it made (internal/c23's Handle, Func and
// VaList) from what C made to look like it: memory of C's has no type,
// and neither have the stack and the data.
//
//go:linkname c23IsObject
func c23IsObject(p unsafe.Pointer, t *_type) bool {
	s := spanOfHeap(uintptr(p))
	if s == nil || s.isUserArenaChunk || s.spanclass.c() || s.spanclass.noscan() {
		return false
	}
	base := s.base() + s.objIndex(uintptr(p))*s.elemsize
	typ, tbase := checkptrObjectType(s, base)
	return typ == t && uintptr(p) == tbase
}

// c23NewObject allocates an object of type t on the heap, zeroed: the
// compiler does not see the allocation, and so does not put the object
// in a frame, where c23IsObject would not know it.
//
//go:linkname c23NewObject
func c23NewObject(t *_type) unsafe.Pointer {
	return mallocgc(t.Size_, t, true)
}

// cInData reports whether a is in the data of the program's first
// module, from its read-only data to its end: an address that is of no
// span and of no stack.
//
//go:nosplit
func cInData(a uintptr) bool {
	return firstmoduledata.rodata <= a && a < firstmoduledata.end
}
