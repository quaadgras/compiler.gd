// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/goarch"
	"internal/runtime/sys"
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
//   - the goroutine's stack from the caller's frame up, as far as the
//     frame that p is in goes (cFrameEnd);
//   - the section of the program's data that p is in, or what the
//     program has that is read-only, each whole.
//
// For the stack and the data that is what the checks of pointer
// arithmetic go by too (c23Header): where those come to know the
// bounds of a variable, this is the one function to tell.
//
//go:linkname c23Extent
func c23Extent(p unsafe.Pointer) (before, after uintptr, ok bool) {
	a := uintptr(p)
	if gp := getg(); gp.stack.lo <= a && a < gp.stack.hi {
		// Nothing that is alive is below the frame of the function
		// that asks: what is there is of functions that returned, or
		// will be the frames of the ones that the library goes on
		// to call.
		sp := sys.GetCallerSP()
		if a < sp {
			return 0, 0, false
		}
		return a - sp, cFrameEnd(gp, a) - a, true
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
	for _, datap := range activeModules() {
		if a < datap.etext || a >= datap.end {
			continue
		}
		var lo, hi uintptr
		switch {
		case a < datap.noptrdata:
			// What is between the code and the data is
			// read-only: the bytes of string literals are there.
			lo, hi = datap.etext, datap.noptrdata
		case a < datap.enoptrdata:
			lo, hi = datap.noptrdata, datap.enoptrdata
		case datap.data <= a && a < datap.edata:
			lo, hi = datap.data, datap.edata
		case datap.bss <= a && a < datap.ebss:
			lo, hi = datap.bss, datap.ebss
		case datap.noptrbss <= a && a < datap.enoptrbss:
			lo, hi = datap.noptrbss, datap.enoptrbss
		default:
			return 0, 0, false
		}
		return a - lo, hi - a, true
	}
	return 0, 0, false
}

// cFrameEnd returns where what C may write from a, an address in gp's
// stack that is in or above the frame of the caller's caller, ends: the
// first place above a where a function that is running keeps its
// caller's frame pointer. The address that the function returns to is
// kept next to it, and neither is the variable's that a is in: without
// this a string that is copied to a variable of a function, and is
// longer than it, is where that function returns to.
//
// The frame pointers are a list, each the address of the one before it,
// from the function that is running up to the goroutine's first. Where
// there are none, or the list is not as it should be, the end is the
// stack's.
func cFrameEnd(gp *g, a uintptr) uintptr {
	if !framepointer_enabled {
		return gp.stack.hi
	}
	fp := getfp()
	for n := 0; n < 1<<12; n++ {
		if fp < gp.stack.lo || fp >= gp.stack.hi || fp&(goarch.PtrSize-1) != 0 {
			break
		}
		if fp > a {
			return fp
		}
		next := *(*uintptr)(unsafe.Pointer(fp))
		if next <= fp {
			break
		}
		fp = next
	}
	return gp.stack.hi
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
