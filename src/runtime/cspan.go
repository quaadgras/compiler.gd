// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/goarch"
	"internal/runtime/atomic"
	"internal/runtime/gc"
	"internal/runtime/sys"
	"unsafe"
)

// The memory that C allocates (doc/gd/cc.md).
//
// What malloc gives has no type: C makes of it what it converts its
// pointers to, and may make something else of it later. So it is not
// allocated as a Go object of some type, whose pointers the collector
// would know from the type. It is allocated in spans of its own, the
// spans of the classes of C (spanClass.c), and such a span keeps for
// itself what a type would have said (cBits):
//
//   - for each word of the span, its claim (cclaim.go): what the first
//     pointer that was converted to a type over it took it for. Nothing
//     yet, something that is no pointer, a pointer to anything, or a
//     pointer of a type, which is then in the span's ids. The collector
//     skips a word that is claimed for no pointer, and looks at any
//     other as it looks at a word of a stack frame that has no map:
//     what is there is a pointer if it is the address of an object that
//     is allocated, and is nothing otherwise (scanObjectC). An object
//     begins with none of its words claimed, unless malloc is told what
//     it is for;
//   - for each object, the number of bytes that were asked for, which is
//     where the object ends for C: less than its slot, which is a size
//     class's and has a byte more besides (cPad).
//
// To everything else in the runtime these are spans of objects without
// pointers: they have no heap bitmap and no malloc header, a copy into
// one has no bitmap of the destination to go by (bulkBarrierC does it),
// and nothing of what a goroutine shares is kept for them.

// cPad is the number of bytes after every object of C's: the pointer one
// past the end of an object, which C may make and keep, points into the
// object's slot and to no other object.
const cPad = 1

// cClassPer is the number of words of memory whose classes one uint32
// of a span's cBits has, two bits each.
const cClassPer = 16

// The class of a word of C's: what it was claimed for.
const (
	cUnclaimed = 0 // nothing yet: its bytes are whatever was written there
	cScalar    = 1 // no pointer
	cUntyped   = 2 // a pointer to anything: unsafe.Pointer, C's void *
	cTyped     = 3 // a pointer of the type that the span's ids have for the word
)

// cBits is what a span of C's objects keeps for them: the classes of
// the words of the span, in uint32s, and after them, in a span of small
// objects, the uint16 sizes of the objects. It is outside the heap, as
// long as its span needs (cBitsBytes), and goes back to a free list with
// its span. The types of the words that are typed pointers are in
// another, the span's ids (cIDsOf), which a span has once it has such a
// word: a uint32 for each word of the span.
type cBits struct {
	_ sys.NotInHeap
	_ uint32
}

// cSpansUsed says that C has allocated: an integer of C's may then be
// where the collector expects a pointer (wbBufFlush1).
var cSpansUsed bool

// cBitsWords returns the number of uint32s of the classes of s.
func (s *mspan) cBitsWords() uintptr {
	return s.npages * (pageSize / goarch.PtrSize / cClassPer)
}

// cIDsBytes returns the size of the ids of s.
func (s *mspan) cIDsBytes() uintptr {
	return s.npages * (pageSize / goarch.PtrSize * 4)
}

// cIDsOf returns the ids of s, a span of C's objects: nil if no word of
// it was claimed for a typed pointer yet.
//
//go:nosplit
func (s *mspan) cIDsOf() *cBits {
	return (*cBits)(atomic.Loadp(unsafe.Pointer(&s.cids)))
}

// cBitsBytes returns the size of the cBits of s.
func (s *mspan) cBitsBytes() uintptr {
	// (And a uint32 after the classes, which compiled code loads
	// with the last of them: cmd/compile/internal/ssa's cheaders.)
	n := s.cBitsWords()*4 + 4
	if s.spanclass.sizeclass() != 0 {
		n += uintptr(s.nelems) * 2
	}
	return n
}

// cBitsOf returns the cBits of s, a span of C's objects: nil if nothing
// was allocated in it yet.
//
//go:nosplit
func (s *mspan) cBitsOf() *cBits {
	return (*cBits)(atomic.Loadp(unsafe.Pointer(&s.cbits)))
}

// word returns the i'th uint32 of the bitmap.
//
//go:nosplit
func (b *cBits) word(i uintptr) *uint32 {
	return (*uint32)(add(unsafe.Pointer(b), i*4))
}

// cClass returns the class of the w'th word of a span.
//
//go:nosplit
func cClass(b *cBits, w uintptr) uint32 {
	return atomic.Load(b.word(w/cClassPer)) >> (w % cClassPer * 2) & 3
}

// cClassSet makes c the class of the n words from the w'th of a span:
// other objects have classes in the same uint32s, so one is changed as
// a whole.
//
//go:nosplit
func cClassSet(b *cBits, w, n uintptr, c uint32) {
	for n > 0 {
		p := b.word(w / cClassPer)
		k := min(n, cClassPer-w%cClassPer)
		mask := uint32(uint64(1)<<(2*k)-1) << (w % cClassPer * 2)
		val := 0x55555555 * c & mask
		for {
			old := atomic.Load(p)
			if old&mask == val || atomic.Cas(p, old, old&^mask|val) {
				break
			}
		}
		w += k
		n -= k
	}
}

// cScalar reports whether the word at a in s, a span of C's objects, is
// claimed for no pointer.
//
//go:nosplit
func (s *mspan) cScalar(a uintptr) bool {
	b := s.cBitsOf()
	return b == nil || cClass(b, (a-s.base())/goarch.PtrSize) < cUntyped
}

// cSizeOf returns the number of bytes that the i'th object of s, a span
// of C's objects, was asked for.
//
//go:nosplit
func (s *mspan) cSizeOf(i uintptr) uintptr {
	if s.spanclass.sizeclass() == 0 {
		return atomic.Loaduintptr(&s.csize)
	}
	b := s.cBitsOf()
	if b == nil {
		return 0
	}
	return uintptr(*(*uint16)(add(unsafe.Pointer(b), s.cBitsWords()*4+i*2)))
}

// The cBits that were freed, by the power of two that their size was
// rounded up to, linked through their first word. Guarded by mheap_.lock.
const (
	cBitsMinOrder = 6  // 64 bytes
	cBitsMaxOrder = 15 // 32 KiB: more is the system's, and goes back to it
)

var cBitsFree [cBitsMaxOrder + 1]*cBits

func cBitsOrder(n uintptr) uint {
	o := uint(cBitsMinOrder)
	for uintptr(1)<<o < n {
		o++
	}
	return o
}

// cMetaAlloc returns n bytes outside the heap, zeroed, for the cBits or
// the ids of a span.
func cMetaAlloc(n uintptr) *cBits {
	o := cBitsOrder(n)
	var b *cBits
	systemstack(func() {
		if o > cBitsMaxOrder {
			b = (*cBits)(sysAlloc(alignUp(n, physPageSize), &memstats.gcMiscSys, "c span bits"))
			if b == nil {
				throw("runtime: cannot allocate memory")
			}
			return
		}
		lock(&mheap_.lock)
		b = cBitsFree[o]
		if b != nil {
			cBitsFree[o] = *(**cBits)(unsafe.Pointer(b))
		}
		unlock(&mheap_.lock)
		if b != nil {
			memclrNoHeapPointers(unsafe.Pointer(b), uintptr(1)<<o)
		} else {
			b = (*cBits)(persistentalloc(uintptr(1)<<o, 8, &memstats.gcMiscSys))
		}
	})
	return b
}

// cMetaFree gives back the n bytes at b that cMetaAlloc gave.
//
// mheap_.lock must be held.
func cMetaFree(b *cBits, n uintptr) {
	if o := cBitsOrder(n); o > cBitsMaxOrder {
		sysFree(unsafe.Pointer(b), alignUp(n, physPageSize), &memstats.gcMiscSys)
	} else {
		*(**cBits)(unsafe.Pointer(b)) = cBitsFree[o]
		cBitsFree[o] = b
	}
}

// newCBits gives s, in which nothing is allocated, its cBits: no word is
// claimed.
func (s *mspan) newCBits() *cBits {
	b := cMetaAlloc(s.cBitsBytes())
	atomic.StorepNoWB(unsafe.Pointer(&s.cbits), unsafe.Pointer(b))
	return b
}

// newCIDs gives s its ids, or returns the ones that it was given
// meanwhile.
func (s *mspan) newCIDs() *cBits {
	n := s.cIDsBytes()
	b := cMetaAlloc(n)
	if !atomic.Casp1((*unsafe.Pointer)(unsafe.Pointer(&s.cids)), nil, unsafe.Pointer(b)) {
		systemstack(func() {
			lock(&mheap_.lock)
			cMetaFree(b, n)
			unlock(&mheap_.lock)
		})
		b = s.cIDsOf()
	}
	return b
}

// freeCBits gives back the cBits and the ids of s, which holds no
// objects.
//
// mheap_.lock must be held.
func (s *mspan) freeCBits() {
	if b := s.cBitsOf(); b != nil {
		n := s.cBitsBytes()
		// (What the Ps remember of the span is no longer so.)
		cHeaderEpoch.Add(1)
		atomic.StorepNoWB(unsafe.Pointer(&s.cbits), nil)
		cMetaFree(b, n)
	}
	if b := s.cIDsOf(); b != nil {
		n := s.cIDsBytes()
		atomic.StorepNoWB(unsafe.Pointer(&s.cids), nil)
		cMetaFree(b, n)
	}
}

// cInitObject makes the slot at x of s, which is zero, an object that
// was asked for size bytes: no word of it is claimed. The caller is
// allocating it, and has not published it.
func (s *mspan) cInitObject(x, size uintptr) {
	b := s.cBitsOf()
	if b == nil {
		b = s.newCBits()
	}
	if s.spanclass.sizeclass() == 0 {
		atomic.Storeuintptr(&s.csize, size)
	} else {
		*(*uint16)(add(unsafe.Pointer(b), s.cBitsWords()*4+s.objIndex(x)*2)) = uint16(size)
	}
	cClassSet(b, (x-s.base())/goarch.PtrSize, s.elemsize/goarch.PtrSize, cUnclaimed)
}

// mallocC allocates size bytes for C, zeroed: malloc. The object is one
// of a span of C's objects. It returns nil if there is no object of
// that size.
func mallocC(size uintptr) unsafe.Pointer {
	if size > maxAlloc-cPad-pageSize {
		return nil
	}
	if doubleCheckMalloc && gcphase == _GCmarktermination {
		throw("mallocC called with gcphase == _GCmarktermination")
	}
	if !cSpansUsed {
		cSpansUsed = true
	}

	// It's possible for any malloc to trigger sweeping, which may in
	// turn queue finalizers. Record this dynamic lock edge.
	lockRankMayQueueFinalizer()

	// Assist the GC if needed.
	if gcBlackenEnabled != 0 {
		deductAssistCredit(size + cPad)
	}

	var x unsafe.Pointer
	var elemsize uintptr
	if size+cPad <= maxSmallSize {
		x, elemsize = mallocCSmall(size)
	} else {
		x, elemsize = mallocCLarge(size)
	}

	if raceenabled {
		racemalloc(x, size)
	}
	if msanenabled {
		msanmalloc(x, size)
	}
	if valgrindenabled {
		valgrindMalloc(x, size)
	}

	// Adjust our GC assist debt to account for internal fragmentation.
	if gcBlackenEnabled != 0 {
		if assistG := getg().m.curg; assistG != nil {
			assistG.gcAssistBytes -= int64(elemsize - size - cPad)
		}
	}
	if debug.malloc {
		postMallocgcDebug(x, elemsize, nil)
	}
	return x
}

func mallocCSmall(size uintptr) (unsafe.Pointer, uintptr) {
	// Set mp.mallocing to keep from being preempted by GC.
	mp := acquirem()
	if doubleCheckMalloc {
		if mp.mallocing != 0 {
			throw("malloc deadlock")
		}
		if mp.gsignal == getg() {
			throw("malloc during signal")
		}
	}
	mp.mallocing = 1

	checkGCTrigger := false
	c := getMCache(mp)
	var sizeclass uint8
	if need := size + cPad; need <= gc.SmallSizeMax-8 {
		sizeclass = gc.SizeToSizeClass8[divRoundUp(need, gc.SmallSizeDiv)]
	} else {
		sizeclass = gc.SizeToSizeClass128[divRoundUp(need-gc.SmallSizeMax, gc.LargeSizeDiv)]
	}
	if size >= 16 && gc.SizeClassToSize[sizeclass]&15 != 0 {
		// What holds 16 bytes is aligned for anything of C's.
		sizeclass++
	}
	elemsize := uintptr(gc.SizeClassToSize[sizeclass])
	spc := makeCSpanClass(sizeclass)
	span := c.alloc[spc]

	v := nextFreeFast(span)
	if v == 0 {
		v, span, checkGCTrigger = c.nextFree(spc)
	}
	x := unsafe.Pointer(v)
	if span.needzero != 0 {
		memclrNoHeapPointers(x, elemsize)
	}
	span.cInitObject(uintptr(x), size)

	// Ensure that the stores above that initialize x and its bits
	// occur before the caller can make x observable to the garbage
	// collector.
	publicationBarrier()

	if writeBarrier.enabled {
		// Allocate black during GC.
		// All slots hold nil so no scanning is needed.
		gcmarknewobject(span, uintptr(x))
	} else {
		// Track the last free index before the mark phase (see
		// mallocgcSmallNoscan).
		span.freeIndexForScan = span.freeindex
	}

	c.nextSample -= int64(elemsize)
	if c.nextSample < 0 || MemProfileRate != c.memProfRate {
		profilealloc(mp, x, elemsize)
	}
	mp.mallocing = 0
	releasem(mp)

	if checkGCTrigger {
		if t := (gcTrigger{kind: gcTriggerHeap}); t.test() {
			gcStart(t)
		}
	}
	return x, elemsize
}

func mallocCLarge(size uintptr) (unsafe.Pointer, uintptr) {
	// Set mp.mallocing to keep from being preempted by GC.
	mp := acquirem()
	if doubleCheckMalloc {
		if mp.mallocing != 0 {
			throw("malloc deadlock")
		}
		if mp.gsignal == getg() {
			throw("malloc during signal")
		}
	}
	mp.mallocing = 1

	c := getMCache(mp)
	span := c.allocLarge(size+cPad, makeCSpanClass(0))
	span.freeindex = 1
	span.allocCount = 1
	elemsize := span.elemsize
	x := unsafe.Pointer(span.base())
	span.cInitObject(uintptr(x), size)

	publicationBarrier()

	if writeBarrier.enabled {
		// Allocate black during GC.
		gcmarknewobject(span, uintptr(x))
	} else {
		span.freeIndexForScan = span.freeindex
	}

	c.nextSample -= int64(elemsize)
	if c.nextSample < 0 || MemProfileRate != c.memProfRate {
		profilealloc(mp, x, elemsize)
	}
	mp.mallocing = 0
	releasem(mp)

	// Check to see if we need to trigger the GC.
	if t := (gcTrigger{kind: gcTriggerHeap}); t.test() {
		gcStart(t)
	}

	// The object is zeroed late, where preemption can occur: what
	// the collector finds in it before is no pointer, or one that it
	// takes for one as it would any integer.
	if span.needzero != 0 {
		memclrNoHeapPointersChunked(elemsize, x)
	}
	mp = acquirem()
	publicationBarrier()
	releasem(mp)
	return x, elemsize
}

// greyConservative marks the object that val is an address in, if it is
// one that is allocated: val is a word that may hold a pointer or
// anything else. base and off are for debugging only.
//
//go:nowritebarrierrec
func greyConservative(val, base, off uintptr, gcw *gcWork) {
	val &^= cPtrBits // of a pointer to a union (cclaim.go)
	span := spanOfHeap(val)
	if span == nil {
		return
	}
	// Ignore objects allocated during the mark phase, they've been
	// allocated black.
	idx := span.objIndex(val)
	if span.isFreeOrNewlyAllocated(idx) {
		return
	}
	obj := span.base() + idx*span.elemsize
	if !tryDeferToSpanScan(obj, gcw) {
		greyobject(obj, base, off, span, gcw, idx)
	}
}

// scanObjectC is scanObject for b in s, a span of C's objects: the words
// of the object, or of the oblet at b, that may hold pointers are looked
// at as scanConservative looks at a frame's.
//
//go:nowritebarrierrec
func scanObjectC(b uintptr, s *mspan, gcw *gcWork) {
	n := s.elemsize
	if n > maxObletBytes {
		// Large object. Break into oblets for better
		// parallelism and lower latency.
		if b == s.base() {
			for oblet := b + maxObletBytes; oblet < s.base()+s.elemsize; oblet += maxObletBytes {
				if !gcw.putObjFast(oblet) {
					gcw.putObj(oblet)
				}
			}
		}
		n = min(s.base()+s.elemsize-b, maxObletBytes)
	}
	bits := s.cBitsOf()
	w := (b - s.base()) / goarch.PtrSize
	for i := uintptr(0); bits != nil && i < n; i, w = i+goarch.PtrSize, w+1 {
		// Only a word that is claimed for a pointer holds one: what
		// nothing claimed is bytes, and a pointer that was written
		// there as bytes is one that cannot be used when the word
		// is claimed for it (cClaimWord).
		classes := atomic.Load(bits.word(w / cClassPer))
		if classes&(0x55555555*cUntyped) == 0 && w%cClassPer == 0 {
			// Skip the words of this uint32 (the loop
			// increment will do the last).
			i += (cClassPer - 1) * goarch.PtrSize
			w += cClassPer - 1
			continue
		}
		if classes>>(w%cClassPer*2)&3 < cUntyped {
			continue
		}
		val := *(*uintptr)(unsafe.Pointer(b + i)) &^ cPtrBits
		// Quickly filter out nil and pointers back to the object.
		if val >= minLegalPointer && val-b >= n {
			greyConservative(val, b, i, gcw)
		}
	}
	gcw.bytesMarked += uint64(n)
	gcw.heapScanWork += int64(n)
	if debug.gctrace > 1 {
		gcw.stats[s.spanclass.sizeclass()].sparseObjsScanned++
	}
}

// bulkBarrierC is bulkBarrierPreWrite for a destination in s, a span of
// C's objects: the barrier for every word of it that may hold a pointer.
// What it shades may be an integer (wbBufFlush1).
//
//go:nosplit
func bulkBarrierC(s *mspan, dst, src, size uintptr, old bool) {
	bits := s.cBitsOf()
	buf := &getg().m.p.ptr().wbBuf
	w := (dst - s.base()) / goarch.PtrSize
	for i := uintptr(0); i < size; i, w = i+goarch.PtrSize, w+1 {
		if bits == nil || cClass(bits, w) < cUntyped {
			continue
		}
		if old {
			p := buf.get1()
			p[0] = *(*uintptr)(unsafe.Pointer(dst + i))
		}
		if src != 0 {
			p := buf.get1()
			p[0] = *(*uintptr)(unsafe.Pointer(src + i))
		}
	}
}

// cSpanOf returns the span of C's objects that p points into, if it
// does.
//
//go:nosplit
func cSpanOf(p uintptr) *mspan {
	if s := spanOfHeap(p); s != nil && s.spanclass.c() {
		return s
	}
	return nil
}

// cBitsOfOrNil is cBitsOf for a span that may be nil.
//
//go:nosplit
func (s *mspan) cBitsOfOrNil() *cBits {
	if s == nil {
		return nil
	}
	return s.cBitsOf()
}

// c23Trim makes the object of C's at x one that was asked for size
// bytes, fewer than it was, and returns x. The caller allocated it and
// has not published it: aligned_alloc, whose object ends where the
// bytes that were asked for do.
//
//go:linkname c23Trim
func c23Trim(x unsafe.Pointer, size uintptr) unsafe.Pointer {
	s := spanOfHeap(uintptr(x))
	if s == nil || !s.spanclass.c() {
		return x
	}
	i := s.objIndex(uintptr(x))
	if uintptr(x) != s.base()+i*s.elemsize || size >= s.cSizeOf(i) {
		return x
	}
	if s.spanclass.sizeclass() == 0 {
		atomic.Storeuintptr(&s.csize, size)
	} else {
		*(*uint16)(add(unsafe.Pointer(s.cBitsOf()), s.cBitsWords()*4+i*2)) = uint16(size)
	}
	return x
}

// c23Usable returns the number of bytes from p to the end of the object
// of C's that it points into, as it was asked for, and whether p points
// into one.
//
//go:linkname c23Usable
func c23Usable(p unsafe.Pointer) (uintptr, bool) {
	s := spanOfHeap(uintptr(p))
	if s == nil || !s.spanclass.c() {
		return 0, false
	}
	i := s.objIndex(uintptr(p))
	off := uintptr(p) - (s.base() + i*s.elemsize)
	if size := s.cSizeOf(i); off <= size {
		return size - off, true
	}
	return 0, true
}
