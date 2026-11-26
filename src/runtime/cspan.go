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

// cClassPer is the number of words of memory whose classes one uint64
// of a span's cBits has, a byte each: the class is the byte's low two
// bits.
const cClassPer = 8

const (
	cClassLows  = 0x0101010101010101 // the low bit of each class of a uint64
	cClassHighs = 0x0202020202020202 // the high bit of each: a pointer
	cClassAll   = 0x0303030303030303
)

// cClassesOf returns the bits of the first k classes of a uint64.
//
//go:nosplit
func cClassesOf(k uintptr) uint64 {
	if k >= cClassPer {
		return cClassAll
	}
	return cClassAll & (uint64(1)<<(8*k) - 1)
}

// The class of a word of C's: what it was claimed for.
const (
	cUnclaimed = 0 // nothing yet: its bytes are whatever was written there
	cScalar    = 1 // no pointer
	cUntyped   = 2 // a pointer to anything: unsafe.Pointer, C's void *
	cTyped     = 3 // a pointer of the type that the span's ids have for the word
)

// cBits is what a span of C's objects keeps for them: the classes of
// the words of the span, a byte each, in uint64s, and after them, in a
// span of small objects, the uint16 sizes of the objects. It is outside the heap, as
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

// cBitsWords returns the number of bytes of the classes of s, in
// uint32s: a byte for each word of the span, and a uint64 more, which
// is loaded with the last of them (cClassWindow, and
// cmd/compile/internal/ssa's cheaders).
func (s *mspan) cBitsWords() uintptr {
	return s.npages*(pageSize/goarch.PtrSize/4) + 2
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

// cBitsBytes returns the size of what s.cbits is: the classes, unless
// they are in the shadow (cshadow.go), and the sizes.
func (s *mspan) cBitsBytes() uintptr {
	n := uintptr(8)
	if !cShadowOn {
		n = s.cBitsWords() * 4
	}
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
	b := (*cBits)(atomic.Loadp(unsafe.Pointer(&s.cbits)))
	if cShadowOn && b != nil {
		return (*cBits)(unsafe.Pointer(cShadowOf(s.base())))
	}
	return b
}

// cSizesOf returns the uint16 sizes of the objects of s, a span of small
// objects of C's that has cBits.
//
//go:nosplit
func (s *mspan) cSizesOf() unsafe.Pointer {
	b := atomic.Loadp(unsafe.Pointer(&s.cbits))
	if cShadowOn {
		return add(b, 8)
	}
	return add(b, s.cBitsWords()*4)
}

// word returns the i'th uint32 of the ids.
//
//go:nosplit
func (b *cBits) word(i uintptr) *uint32 {
	return (*uint32)(add(unsafe.Pointer(b), i*4))
}

// cClass returns the class of the w'th word of a span.
//
//go:nosplit
func cClass(b *cBits, w uintptr) uint32 {
	return uint32(atomic.Load8((*uint8)(add(unsafe.Pointer(b), w)))) & 3
}

// group returns the i'th uint64 of the classes.
//
//go:nosplit
func (b *cBits) group(i uintptr) *uint64 {
	return (*uint64)(add(unsafe.Pointer(b), i*8))
}

// cClassWindow returns the classes of the cClassPer words from the
// w'th, the w'th lowest.
//
//go:nosplit
func cClassWindow(b *cBits, w uintptr) uint64 {
	v := atomic.Load64(b.group(w / cClassPer))
	if sh := w % cClassPer * 8; sh != 0 {
		v = v>>sh | atomic.Load64(b.group(w/cClassPer+1))<<(64-sh)
	}
	return v
}

// cClassSet makes c the class of the n words from the w'th of a span:
// other objects have classes in the same uint64s, so one is changed as
// a whole.
//
//go:nosplit
func cClassSet(b *cBits, w, n uintptr, c uint32) {
	for n > 0 {
		p := b.group(w / cClassPer)
		k := min(n, cClassPer-w%cClassPer)
		mask := cClassesOf(k) << (w % cClassPer * 8)
		val := cClassLows * uint64(c) & mask
		for {
			old := atomic.Load64(p)
			if old&mask == val || atomic.Cas64(p, old, old&^mask|val) {
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
	if s.cBitsOf() == nil {
		return 0
	}
	return uintptr(*(*uint16)(add(s.cSizesOf(), i*2)))
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
	return s.cBitsOf()
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
	if b := (*cBits)(atomic.Loadp(unsafe.Pointer(&s.cbits))); b != nil {
		n := s.cBitsBytes()
		// (What the Ps remember of the span is no longer so.)
		cHeaderEpoch.Add(1)
		atomic.StorepNoWB(unsafe.Pointer(&s.cbits), nil)
		cMetaFree(b, n)
		if cShadowOn {
			// The memory is in no object of C's any more.
			cShadowClear(s.base(), s.npages*pageSize)
		}
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
		*(*uint16)(add(s.cSizesOf(), s.objIndex(x)*2)) = uint16(size)
	}
	cObjectBytes(b, (x-s.base())/goarch.PtrSize, s.elemsize/goarch.PtrSize, size, false)
}

// What the byte of a word of C's says beside its class.
const (
	cWordIn    = 0x80 // the whole word is in what its object was asked for
	cWordStart = 0x40 // the word is the first of its object's slot
	cWordRO    = 0x20 // nothing is stored in the word: a constant of the program
	// The number of bytes of a word that are in its object, if not
	// all of them are and the object ends in the word.
	cWordPart      = 0x1c
	cWordPartShift = 2
)

// cObjectBytes writes the bytes of the n words of a slot, from the
// w'th of b, for an object that was asked for size bytes: which of the
// words are whole in it, and that the first is the first. The words'
// classes are as they were if keep is set, and nothing's otherwise. The
// bytes are the slot's own: nothing else writes them now, though the
// uint64s that they are in may be changed for the bytes of other
// objects (cClassSet), which leaves these as they are.
//
//go:nosplit
func cObjectBytes(b *cBits, w, n, size uintptr, keep bool) {
	whole := size / goarch.PtrSize
	p := add(unsafe.Pointer(b), w)
	if keep {
		for i := uintptr(0); i < n; i++ {
			q := (*uint8)(add(p, i))
			v := *q & 3
			if i < whole {
				v |= cWordIn
			} else if i == whole {
				v |= uint8(size%goarch.PtrSize) << cWordPartShift
			}
			if i == 0 {
				v |= cWordStart
			}
			*q = v
		}
		return
	}
	i := uintptr(0)
	for ; i+8 <= whole; i += 8 {
		*(*uint64)(add(p, i)) = cClassLows * cWordIn
	}
	for ; i < whole; i++ {
		*(*uint8)(add(p, i)) = cWordIn
	}
	for ; i < n && i%8 != 0; i++ {
		*(*uint8)(add(p, i)) = 0
	}
	for ; i+8 <= n; i += 8 {
		*(*uint64)(add(p, i)) = 0
	}
	for ; i < n; i++ {
		*(*uint8)(add(p, i)) = 0
	}
	*(*uint8)(p) |= cWordStart
	if whole < n {
		*(*uint8)(add(p, whole)) |= uint8(size%goarch.PtrSize) << cWordPartShift
	}
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
		classes := atomic.Load64(bits.group(w / cClassPer))
		if classes&cClassHighs == 0 && w%cClassPer == 0 {
			// Skip the words of this uint64 (the loop
			// increment will do the last).
			i += (cClassPer - 1) * goarch.PtrSize
			w += cClassPer - 1
			continue
		}
		if classes>>(w%cClassPer*8)&3 < cUntyped {
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
		*(*uint16)(add(s.cSizesOf(), i*2)) = uint16(size)
	}
	cObjectBytes(s.cBitsOf(), (uintptr(x)-s.base())/goarch.PtrSize, s.elemsize/goarch.PtrSize, size, true)
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
