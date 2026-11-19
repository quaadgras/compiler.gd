// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/goarch"
	"internal/runtime/atomic"
	"internal/runtime/gc"
	"unsafe"
)

func checkptrAlignment(p unsafe.Pointer, elem *_type, n uintptr) {
	// nil pointer is always suitably aligned (#47430).
	if p == nil {
		return
	}

	// Check that (*[n]elem)(p) is appropriately aligned.
	// Note that we allow unaligned pointers if the types they point to contain
	// no pointers themselves. See issue 37298.
	// TODO(mdempsky): What about fieldAlign?
	if elem.Pointers() && uintptr(p)&(uintptr(elem.Align_)-1) != 0 {
		throw("checkptr: misaligned pointer conversion")
	}

	// gd: these checks are on in every build (doc/gd/unsafe.md), so the
	// two usual cases are found without looking for the allocation of
	// each end: the goroutine's stack, and one object of the heap.
	size := n * elem.Size_
	if gp := getg(); gp.stack.lo <= uintptr(p) && uintptr(p)+size <= gp.stack.hi && uintptr(p)+size >= uintptr(p) {
		return
	}
	if s := spanOfHeap(uintptr(p)); s != nil && !s.isUserArenaChunk {
		base := s.base() + s.objIndex(uintptr(p))*s.elemsize
		if end := uintptr(p) + size; end > base+s.elemsize || end < uintptr(p) {
			throw("checkptr: converted pointer straddles multiple allocations")
		}
		if elem.Pointers() && !checkptrSameType(s, base, uintptr(p), elem) {
			checkptrPointers(s, uintptr(p), elem, n)
		}
		return
	}

	// Not in the heap, and neither is its end: a global, or memory that
	// is not Go's, which have no bounds here.
	if size > 0 && uintptr(p)+size-1 >= uintptr(p) && spanOfHeap(uintptr(p)+size-1) == nil {
		return
	}

	// Check that (*[n]elem)(p) doesn't straddle multiple heap objects.
	// TODO(mdempsky): Fix #46938 so we don't need to worry about overflow here.
	if checkptrStraddles(p, size) {
		throw("checkptr: converted pointer straddles multiple allocations")
	}
}

// checkptrSameType reports whether p, in the object at base of the span s,
// is known to point to a value of type elem, or to one of an array of them,
// without looking at where the object's pointers are: the object was
// allocated as elems and p is at one. It is what a pointer converted to
// unsafe.Pointer and back points to.
func checkptrSameType(s *mspan, base, p uintptr, elem *_type) bool {
	if s.spanclass.noscan() || heapBitsInSpan(s.elemsize) {
		// No pointers, or no type: the object has only a bitmap.
		return false
	}
	var typ *_type
	if s.spanclass.sizeclass() != 0 {
		// The object begins with its type.
		typ = *(**_type)(unsafe.Pointer(base))
		base += gc.MallocHeaderSize
	} else {
		typ = (*_type)(atomic.Loadp(unsafe.Pointer(&s.largeType)))
	}
	return typ == elem && p >= base && (p-base)%elem.Size_ == 0
}

// checkptrSameObject reports whether a, in an object of the span s, and v
// are addresses in the same object.
func checkptrSameObject(s *mspan, a, v uintptr) bool {
	return s.base() <= v && v < s.limit && s.objIndex(a) == s.objIndex(v)
}

// checkptrPointers checks that (*[n]elem)(p), which is in one object of
// the span s, has pointers only where the object has, or where the object
// has a word that does not hold the address of anything in the heap now:
// a pointer read from a word that holds an integer is whatever the
// integer is.
//
// What it cannot see: a pointer stored later through the converted
// pointer in a word the collector does not look at, and an integer stored
// where the object has a pointer. A conversion cannot tell what will be
// stored.
func checkptrPointers(s *mspan, p uintptr, elem *_type, n uintptr) {
	limit := p + n*elem.Size_
	var tp typePointers
	var slot uintptr // the next word of the object that holds a pointer
	if !s.spanclass.noscan() {
		tp = s.typePointersOf(p, limit-p)
		tp, slot = tp.next(limit)
	}
	mask := getGCMask(elem)
	words := elem.PtrBytes / goarch.PtrSize
	for i := uintptr(0); i < n; i++ {
		for w := uintptr(0); w < words; w++ {
			if *addb(mask, w/8)>>(w%8)&1 == 0 {
				continue
			}
			a := p + i*elem.Size_ + w*goarch.PtrSize
			for slot != 0 && slot < a {
				tp, slot = tp.next(limit)
			}
			if slot != a {
				// The first word of an interface is one that the
				// collector does not look at, and is a pointer all
				// the same, to a type or a method table, which are
				// not in the heap: package reflect and others read
				// it as one. So is a pointer to a type that is
				// never in the heap. So a word that holds no
				// pointer may be seen as a pointer while what it
				// holds is not one into the heap.
				//
				// Nor is it a pointer that the collector has to see
				// if it points into the object that it is in, which
				// lives while the word is read: a buffer of bytes
				// that the system fills with a list whose links are
				// addresses in the buffer (windows).
				if v := *(*uintptr)(unsafe.Pointer(a)); v != 0 && spanOfHeap(v) != nil && !checkptrSameObject(s, a, v) {
					throw("checkptr: converted pointer type has a pointer where the memory has an integer")
				}
			}
		}
	}
}

// checkptrStraddles reports whether the first size-bytes of memory
// addressed by ptr is known to straddle more than one Go allocation.
func checkptrStraddles(ptr unsafe.Pointer, size uintptr) bool {
	if size <= 1 {
		return false
	}

	// Check that add(ptr, size-1) won't overflow. This avoids the risk
	// of producing an illegal pointer value (assuming ptr is legal).
	if uintptr(ptr) >= -(size - 1) {
		return true
	}
	end := add(ptr, size-1)

	// TODO(mdempsky): Detect when [ptr, end] contains Go allocations,
	// but neither ptr nor end point into one themselves.

	return checkptrBase(ptr) != checkptrBase(end)
}

func checkptrArithmetic(p unsafe.Pointer, originals []unsafe.Pointer) {
	if 0 < uintptr(p) && uintptr(p) < minLegalPointer {
		throw("checkptr: pointer arithmetic computed bad pointer value")
	}

	// Check that if the computed pointer p points into a heap
	// object, then one of the original pointers must have pointed
	// into the same object.
	base := checkptrBase(p)
	if base == 0 {
		return
	}

	for _, original := range originals {
		if base == checkptrBase(original) {
			return
		}
	}

	// gd: an integer that was kept is a pointer again if it is in an
	// object of the heap whose address the program took as an integer:
	// see exposeptr.
	if checkptrExposed(uintptr(p)) {
		return
	}

	throw("checkptr: pointer arithmetic result points to invalid allocation")
}

// Exposed objects.
//
// Go's rules for unsafe.Pointer let an integer become a pointer only in
// the expression that made it of one. Code written as C is keeps
// addresses in integers: a handle in a uintptr field, an argument of a
// callback. So a pointer may also be made of an integer that is the
// address of something in an object of the heap that is exposed: one whose
// address the program made an integer of, and kept. Compiled code calls
// exposeptr where it converts a pointer to a uintptr that outlives the
// expression. The object is no more alive for it: when it is freed the
// integer is the address of nothing, and of nothing exposed if the memory
// is used again.
//
// This is what C calls an exposed address (PNVI-ae): an integer gives no
// access to an object that never gave its address away.

// exposeptr marks the object of the heap that p points into as exposed.
func exposeptr(p unsafe.Pointer) {
	if p == nil {
		return
	}
	s := spanOfHeap(uintptr(p))
	if s == nil || s.isUserArenaChunk {
		return
	}
	i := s.objIndex(uintptr(p))
	mask := uint8(1) << (i % 8)
	if bits := (*gcBits)(atomic.Loadp(unsafe.Pointer(&s.exposedBits))); bits != nil && atomic.Load8(bits.bytep(i/8))&mask != 0 {
		return
	}
	// The sweeper replaces the bitmap, so the span must be swept and
	// stay so: as for the bits of shared.go.
	mp := acquirem()
	s.ensureSwept()
	lock(&s.speciallock)
	bits := (*gcBits)(atomic.Loadp(unsafe.Pointer(&s.exposedBits)))
	if bits == nil {
		bits = newMarkBits(uintptr(s.nelems))
		atomic.StorepNoWB(unsafe.Pointer(&s.exposedBits), unsafe.Pointer(bits))
	}
	if !s.isFree(i) {
		atomic.Or8(bits.bytep(i/8), mask)
	}
	unlock(&s.speciallock)
	releasem(mp)
	KeepAlive(p)
}

// checkptrExposed reports whether p is in an exposed object of the heap.
func checkptrExposed(p uintptr) bool {
	s := spanOfHeap(p)
	if s == nil || s.isUserArenaChunk {
		return false
	}
	bits := (*gcBits)(atomic.Loadp(unsafe.Pointer(&s.exposedBits)))
	if bits == nil {
		return false
	}
	i := s.objIndex(p)
	return !s.isFree(i) && atomic.Load8(bits.bytep(i/8))&(1<<(i%8)) != 0
}

// checkptrBase returns the base address for the allocation containing
// the address p.
//
// Importantly, if p1 and p2 point into the same variable, then
// checkptrBase(p1) == checkptrBase(p2). However, the converse/inverse
// is not necessarily true as allocations can have trailing padding,
// and multiple variables may be packed into a single allocation.
//
// checkptrBase should be an internal detail,
// but widely used packages access it using linkname.
// Notable members of the hall of shame include:
//   - github.com/bytedance/sonic
//
// Do not remove or change the type signature.
// See go.dev/issue/67401.
//
//go:linkname checkptrBase
func checkptrBase(p unsafe.Pointer) uintptr {
	// stack
	if gp := getg(); gp.stack.lo <= uintptr(p) && uintptr(p) < gp.stack.hi {
		// TODO(mdempsky): Walk the stack to identify the
		// specific stack frame or even stack object that p
		// points into.
		//
		// In the mean time, use "1" as a pseudo-address to
		// represent the stack. This is an invalid address on
		// all platforms, so it's guaranteed to be distinct
		// from any of the addresses we might return below.
		return 1
	}

	// heap (must check after stack because of #35068)
	// (Not findObject: it throws for a pointer into a span that was
	// freed, which an integer kept from before may well be; that is
	// for this check to say, or not, as for any other address.)
	if s := spanOfHeap(uintptr(p)); s != nil {
		return s.base() + s.objIndex(uintptr(p))*s.elemsize
	}

	// data or bss
	for _, datap := range activeModules() {
		if datap.data <= uintptr(p) && uintptr(p) < datap.edata {
			return datap.data
		}
		if datap.bss <= uintptr(p) && uintptr(p) < datap.ebss {
			return datap.bss
		}
	}

	return 0
}

// reflect_checkptr is the check of the conversion (*t)(p) for
// reflect.NewAt.
//
//go:linkname reflect_checkptr reflect.checkptr
func reflect_checkptr(p unsafe.Pointer, t *_type) {
	if sharingEnabled { // not wasm: see cmd/compile/internal/base/flag.go
		checkptrAlignment(p, t, 1)
	}
}
