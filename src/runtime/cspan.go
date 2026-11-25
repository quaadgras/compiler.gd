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
// What malloc gives has no type: C makes of it what it stores there, and
// may make something else of it later. So it is not allocated as a Go
// object of some type, whose pointers the collector would know from the
// type. It is allocated in spans of its own, the spans of the classes of
// C (spanClass.c), and such a span keeps for itself what a type would
// have said (cBits):
//
//   - for each word of the span, a bit that says that the word holds no
//     pointer. A word whose bit is clear may hold one, and the collector
//     looks at it as it looks at a word of a stack frame that has no map:
//     what is there is a pointer if it is the address of an object that
//     is allocated, and is nothing otherwise (scanObjectC). An object
//     begins with all of its bits clear;
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

// cBitsWord is the number of words of memory that one word of the bitmap
// is for.
const cBitsWord = 32

// cBits is what a span of C's objects keeps for them: a bitmap of the
// words of the span, in uint32s, and after it, in a span of small
// objects, the uint16 sizes of the objects. It is outside the heap, as
// long as its span needs (cBitsBytes), and goes back to a free list with
// its span.
type cBits struct {
	_ sys.NotInHeap
	_ uint32
}

// cSpansUsed says that C has allocated: an integer of C's may then be
// where the collector expects a pointer (wbBufFlush1).
var cSpansUsed bool

// usesInlineMarkBits reports whether the span has its mark bits in
// itself (gcUsesSpanInlineMarkBits): a span of C's objects never has,
// and is not scanned as a span.
//
//go:nosplit
func (s *mspan) usesInlineMarkBits() bool {
	return gcUsesSpanInlineMarkBits(s.elemsize) && !s.spanclass.c()
}

// cBitsWords returns the number of uint32s of the bitmap of s.
func (s *mspan) cBitsWords() uintptr {
	return s.npages * (pageSize / goarch.PtrSize / cBitsWord)
}

// cBitsBytes returns the size of the cBits of s.
func (s *mspan) cBitsBytes() uintptr {
	n := s.cBitsWords() * 4
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

// cScalar reports whether the word at a in s, a span of C's objects, is
// known to hold no pointer.
//
//go:nosplit
func (s *mspan) cScalar(a uintptr) bool {
	b := s.cBitsOf()
	if b == nil {
		return false
	}
	w := (a - s.base()) / goarch.PtrSize
	return atomic.Load(b.word(w/cBitsWord))>>(w%cBitsWord)&1 != 0
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

// newCBits gives s, in which nothing is allocated, its cBits: every word
// may hold a pointer.
func (s *mspan) newCBits() *cBits {
	n := s.cBitsBytes()
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
	atomic.StorepNoWB(unsafe.Pointer(&s.cbits), unsafe.Pointer(b))
	return b
}

// freeCBits gives back the cBits of s, which holds no objects.
//
// mheap_.lock must be held.
func (s *mspan) freeCBits() {
	b := s.cBitsOf()
	if b == nil {
		return
	}
	n := s.cBitsBytes()
	atomic.StorepNoWB(unsafe.Pointer(&s.cbits), nil)
	if o := cBitsOrder(n); o > cBitsMaxOrder {
		sysFree(unsafe.Pointer(b), alignUp(n, physPageSize), &memstats.gcMiscSys)
	} else {
		*(**cBits)(unsafe.Pointer(b)) = cBitsFree[o]
		cBitsFree[o] = b
	}
}

// cInitObject makes the slot at x of s, which is zero, an object that
// was asked for size bytes: every word of it may hold a pointer. The
// caller is allocating it, and has not published it.
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
	cBitsSet(b, (x-s.base())/goarch.PtrSize, s.elemsize/goarch.PtrSize, false)
}

// cBitsSet sets or clears the bits of the n words from the w'th of a
// span: other objects have bits in the same uint32s, so one is changed
// as a whole.
func cBitsSet(b *cBits, w, n uintptr, scalar bool) {
	for n > 0 {
		p := b.word(w / cBitsWord)
		k := min(n, cBitsWord-w%cBitsWord)
		mask := uint32(1)<<k - 1
		if k == cBitsWord {
			mask = ^uint32(0)
		}
		mask <<= w % cBitsWord
		if have := atomic.Load(p); scalar && have&mask != mask {
			atomic.Or(p, mask)
		} else if !scalar && have&mask != 0 {
			atomic.And(p, ^mask)
		}
		w += k
		n -= k
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
	for i := uintptr(0); i < n; i, w = i+goarch.PtrSize, w+1 {
		if bits != nil {
			scalar := atomic.Load(bits.word(w / cBitsWord))
			if scalar == ^uint32(0) && w%cBitsWord == 0 {
				// Skip the words of this uint32 (the loop
				// increment will do the last).
				i += (cBitsWord - 1) * goarch.PtrSize
				w += cBitsWord - 1
				continue
			}
			if scalar>>(w%cBitsWord)&1 != 0 {
				continue
			}
		}
		val := *(*uintptr)(unsafe.Pointer(b + i))
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
		if bits != nil && atomic.Load(bits.word(w/cBitsWord))>>(w%cBitsWord)&1 != 0 {
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

// The bits of a span of C's objects change as C stores.
//
// A word whose bit is set holds no pointer: the collector does not look
// at it, and a conversion to a pointer type that has a pointer there is
// refused if what the word holds is an address (checkptrPointers). So:
//
//   - a pointer is stored in a word of C's with the bit clear. A store of
//     a pointer that the compiler makes in a package of C's goes through
//     c23StorePtr, and the runtime's atomic stores of pointers look as it
//     does: the bit is cleared before the store, and again after it if
//     it was set meanwhile;
//   - a value of a type with pointers is stored whole by typedmemmove,
//     which the compiler calls for it in a package of C's: the bits of
//     all of its words become what the type says, at once (cTyped);
//   - a bit is set by what stores no pointers and knows it: memset, a
//     copy of words that hold none, an allocation that says what its
//     words are, and a store of integers that the compiler makes in a
//     package of C's through a pointer that it knows to be into what
//     malloc gave with no type (c23Scalars). The bit is set before the
//     words are written, and what the words held is the collector's to
//     see first, as for any pointer that is written over while it marks.
//
// A bit that changes does so under the span's sequence (cseq), which is
// odd meanwhile, as a seqlock's is. Stores do not read it. What reads
// bits together with the words they are for does: a copy from a span of
// C's takes the source's bits for the destination's, and if the sequence
// moved while it copied, a word it took for an integer may be a pointer
// by now, so it takes all of them for possible pointers instead.

// cLock makes the sequence of s odd: the caller changes bits of s. It
// cannot be preempted until cUnlock.
//
//go:nosplit
func (s *mspan) cLock() {
	for i := 0; ; i++ {
		if v := s.cseq.Load(); v&1 == 0 && s.cseq.CompareAndSwap(v, v+1) {
			return
		}
		if i < 64 {
			procyield(16)
		} else {
			osyield()
		}
	}
}

//go:nosplit
func (s *mspan) cUnlock() { s.cseq.Add(1) }

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

// cBitsGet returns the bits of the k words from the w'th of a span, at
// most cBitsWord of them.
//
//go:nosplit
func cBitsGet(b *cBits, w, k uintptr) uint32 {
	v := uint64(atomic.Load(b.word(w / cBitsWord)))
	if w%cBitsWord+k > cBitsWord {
		v |= uint64(atomic.Load(b.word(w/cBitsWord+1))) << cBitsWord
	}
	return uint32(v>>(w%cBitsWord)) & uint32(uint64(1)<<k-1)
}

// cPointers clears the bits of the n words from the w'th of s: they may
// hold pointers.
//
//go:nosplit
func (s *mspan) cPointers(b *cBits, w, n uintptr) {
	locked := false
	for n > 0 {
		p := b.word(w / cBitsWord)
		k := min(n, cBitsWord-w%cBitsWord)
		mask := uint32(uint64(1)<<k-1) << (w % cBitsWord)
		if atomic.Load(p)&mask != 0 {
			if !locked {
				s.cLock()
				locked = true
			}
			atomic.And(p, ^mask)
		}
		w += k
		n -= k
	}
	if locked {
		s.cUnlock()
	}
}

// cPointerSlot is for a store of a pointer at slot, before it and after
// it: if slot is a word of C's, the word may hold a pointer.
//
//go:nosplit
func cPointerSlot(slot uintptr) {
	if s := cSpanOf(slot); s != nil {
		if b := s.cBitsOf(); b != nil {
			w := (slot - s.base()) / goarch.PtrSize
			if atomic.Load(b.word(w/cBitsWord))>>(w%cBitsWord)&1 != 0 {
				s.cPointers(b, w, 1)
			}
		}
	}
}

// c23StorePtr is *slot = val for a store that the compiler makes in a
// package of C translated by cmd/cc, where slot is not known to be a
// variable's: it may be a word of C's that holds no pointer.
//
//go:nosplit
func c23StorePtr(slot *unsafe.Pointer, val unsafe.Pointer) {
	if val != nil {
		if s := cSpanOf(uintptr(unsafe.Pointer(slot))); s != nil {
			if b := s.cBitsOf(); b != nil {
				w := (uintptr(unsafe.Pointer(slot)) - s.base()) / goarch.PtrSize
				p, shift := b.word(w/cBitsWord), w%cBitsWord
				if atomic.Load(p)>>shift&1 != 0 {
					s.cPointers(b, w, 1)
				}
				*slot = val
				if atomic.Load(p)>>shift&1 != 0 {
					// A store of an integer here set it
					// between the two.
					s.cPointers(b, w, 1)
				}
				return
			}
		}
	}
	*slot = val
}

// cTyped is for a copy of n values of typ to dst, before it: if dst is
// words of C's, they are what typ says from here on, possible pointers
// where it has pointers and words that hold none elsewhere, whatever
// they were. A value's bits are set as a whole, a uint32 of the bitmap
// at a time, and not for each pointer of it. The collector sees what
// the words held first, if it is marking.
//
// It reports whether dst is C's, and returns the span if it left it
// locked: the caller unlocks it when the words are written, and then
// calls cTypedPointers, for a word that something else set meanwhile.
//
//go:nosplit
func cTyped(dst uintptr, typ *_type, n uintptr) (inC bool, locked *mspan) {
	s := cSpanOf(dst)
	if s == nil {
		return false, nil
	}
	b := s.cBitsOf()
	if b == nil {
		return true, nil
	}
	if dst%goarch.PtrSize != 0 || typ.Size_%goarch.PtrSize != 0 {
		// Not whole words: only the pointers are known.
		cTypedPointers(dst, typ, n)
		return true, nil
	}
	mask := getGCMask(typ)
	ptrs := typ.PtrBytes / goarch.PtrSize
	each := typ.Size_ / goarch.PtrSize
	w0 := (dst - s.base()) / goarch.PtrSize
	// (No further than the object's slot, whatever is copied.)
	total := min(n*each, (s.objIndex(dst)+1)*s.elemsize/goarch.PtrSize-w0)
	if total == 0 {
		return true, nil
	}
	cShade(b, w0, dst, 0, total)
	var want, ours uint32 // the bits of this uint32 that are set, and that are the value's
	for i := uintptr(0); i < total; i++ {
		bit := uint32(1) << ((w0 + i) % cBitsWord)
		ours |= bit
		if f := i % each; f >= ptrs || *addb(mask, f/8)>>(f%8)&1 == 0 {
			want |= bit
		}
		if (w0+i)%cBitsWord == cBitsWord-1 || i == total-1 {
			p := b.word((w0 + i) / cBitsWord)
			if have := atomic.Load(p) & ours; have != want {
				if locked == nil {
					s.cLock()
					locked = s
				}
				if set := want &^ have; set != 0 {
					atomic.Or(p, set)
				}
				if clear := have &^ want; clear != 0 {
					atomic.And(p, ^clear)
				}
			}
			want, ours = 0, 0
		}
	}
	return true, locked
}

// cTypedPointers is for a copy of n values of typ to dst, after it:
// where typ has a pointer, a word of C's at dst may hold one. It
// reports whether dst is C's.
//
//go:nosplit
func cTypedPointers(dst uintptr, typ *_type, n uintptr) bool {
	s := cSpanOf(dst)
	if s == nil {
		return false
	}
	b := s.cBitsOf()
	if b == nil {
		return true
	}
	mask := getGCMask(typ)
	words := typ.PtrBytes / goarch.PtrSize
	limit := (s.objIndex(dst) + 1) * s.elemsize / goarch.PtrSize // of the object's slot
	for i := uintptr(0); i < n; i++ {
		w0 := (dst + i*typ.Size_ - s.base()) / goarch.PtrSize
		for w := uintptr(0); w < words && w0+w < limit; w++ {
			if *addb(mask, w/8)>>(w%8)&1 != 0 && atomic.Load(b.word((w0+w)/cBitsWord))>>((w0+w)%cBitsWord)&1 != 0 {
				s.cPointers(b, w0+w, 1)
			}
		}
	}
	return true
}

// cShade is the collector's barrier for the n words of C's from the w'th
// of a span, at dst, that may hold pointers, if the collector is
// marking: it sees what they hold, and what the words at src do, which
// are stored there next, if src is not 0.
//
//go:nosplit
func cShade(b *cBits, w, dst, src, n uintptr) {
	if !writeBarrier.enabled {
		return
	}
	buf := &getg().m.p.ptr().wbBuf
	for i := uintptr(0); i < n; i++ {
		if atomic.Load(b.word((w+i)/cBitsWord))>>((w+i)%cBitsWord)&1 == 0 {
			p := buf.get1()
			p[0] = *(*uintptr)(unsafe.Pointer(dst + i*goarch.PtrSize))
		}
		if src != 0 {
			p := buf.get1()
			p[0] = *(*uintptr)(unsafe.Pointer(src + i*goarch.PtrSize))
		}
	}
}

// cScalars sets the bits of the n words from the w'th of s, which the
// caller is about to write integers in, and has shaded. It returns
// with s locked if any was clear: the caller unlocks it when the words
// are written.
//
//go:nosplit
func (s *mspan) cScalars(b *cBits, w, n uintptr) (locked bool) {
	for n > 0 {
		p := b.word(w / cBitsWord)
		k := min(n, cBitsWord-w%cBitsWord)
		mask := uint32(uint64(1)<<k-1) << (w % cBitsWord)
		if atomic.Load(p)&mask != mask {
			if !locked {
				s.cLock()
				locked = true
			}
			atomic.Or(p, mask)
		}
		w += k
		n -= k
	}
	return locked
}

// c23Scalars is for a store of n bytes at p that holds no pointer, before
// it: one that the compiler makes in a package of C's through a pointer
// that it knows to be into what malloc gave with no type
// (cmd/compile/internal/ssa/cscalars.go), which is the memory whose
// words nothing has said anything of yet. The words that the store
// writes in, whole or in part, hold no pointer from here on, and what
// they held is the collector's to see first. It does nothing for a
// word of anything else.
//
// The bits are set without the span's sequence, which memset has
// until it has written: the store comes after this returns, so the
// sequence would not be for it. A copy of these words meanwhile is one
// that C does not define, and it takes what it finds there for an
// integer.
//
//go:nosplit
func c23Scalars(p unsafe.Pointer, n uintptr) {
	s := cSpanOf(uintptr(p))
	if s == nil {
		return
	}
	b := s.cBitsOf()
	if b == nil {
		return
	}
	base := s.base()
	w := (uintptr(p) - base) / goarch.PtrSize
	if (uintptr(p)+n-1-base)/goarch.PtrSize == w {
		// One word, as most stores are.
		bits, bit := b.word(w/cBitsWord), uint32(1)<<(w%cBitsWord)
		if atomic.Load(bits)&bit == 0 {
			cShade(b, w, base+w*goarch.PtrSize, 0, 1)
			atomic.Or(bits, bit)
		}
		return
	}
	// (No further than the object's slot, whatever is stored.)
	end := min(uintptr(p)+n, base+(s.objIndex(uintptr(p))+1)*s.elemsize)
	k := (alignUp(end, goarch.PtrSize)-base)/goarch.PtrSize - w
	cShade(b, w, base+w*goarch.PtrSize, 0, k)
	cBitsSet(b, w, k, true)
}

// c23Malloc is malloc for C translated by cmd/cc (internal/c23): size
// bytes, zeroed. If typ is not nil the object is for values of typ: its
// words hold pointers where typ has them and nowhere else, until C
// stores otherwise. If scalar is set, no word of it holds a pointer,
// until C stores one.
//
//go:linkname c23Malloc
func c23Malloc(size uintptr, typ *_type, scalar bool) unsafe.Pointer {
	x := mallocC(size)
	if x == nil || !scalar && typ == nil {
		return x
	}
	// The object is not reached yet by anything that looks at bits.
	s := spanOfUnchecked(uintptr(x))
	b := s.cBitsOf()
	w := (uintptr(x) - s.base()) / goarch.PtrSize
	n := s.elemsize / goarch.PtrSize
	if scalar || !typ.Pointers() {
		cBitsSet(b, w, n, true)
		return x
	}
	if typ.Size_%goarch.PtrSize != 0 || typ.Size_ == 0 {
		return x
	}
	mask := getGCMask(typ)
	ptrs := typ.PtrBytes / goarch.PtrSize
	each := typ.Size_ / goarch.PtrSize
	// The whole values of typ that fit in size, and no more: what is
	// left of the slot is anything. One uint32 of the bitmap at a
	// time: other objects have bits in the same ones.
	total := size / typ.Size_ * each
	var set uint32
	for i := uintptr(0); i < total; i++ {
		if f := i % each; f >= ptrs || *addb(mask, f/8)>>(f%8)&1 == 0 {
			set |= 1 << ((w + i) % cBitsWord)
		}
		if (w+i)%cBitsWord == cBitsWord-1 || i == total-1 {
			if set != 0 {
				atomic.Or(b.word((w+i)/cBitsWord), set)
			}
			set = 0
		}
	}
	publicationBarrier()
	return x
}

// c23Memmove is memmove for C: the words of it that may be whole
// pointers are copied as pointers are, with the collector's barrier, and
// the bytes before and after them as bytes. If it copies to an object
// of C's, the words there are then what the words they are copied from
// were: possible pointers, or none.
//
//go:linkname c23Memmove
//go:nosplit
func c23Memmove(dst, src unsafe.Pointer, n uintptr) {
	if dst == src || n == 0 {
		return
	}
	d, sr := uintptr(dst), uintptr(src)
	lo, hi := alignDown(d, goarch.PtrSize), alignUp(d+n, goarch.PtrSize)
	wlo, whi := alignUp(d, goarch.PtrSize), alignDown(d+n, goarch.PtrSize)
	aligned := (d^sr)&(goarch.PtrSize-1) == 0
	ds := cSpanOf(d)
	var b *cBits
	if ds != nil {
		b = ds.cBitsOf()
	}
	if b == nil {
		// A destination of Go's: its own bitmap says where its
		// pointers are.
		if writeBarrier.enabled && aligned && wlo < whi {
			bulkBarrierPreWrite(wlo, sr+(wlo-d), whi-wlo, nil)
		}
		memmove(dst, src, n)
		return
	}
	base := ds.base()
	// What is written over, in whole words or in part.
	cShade(b, (lo-base)/goarch.PtrSize, lo, 0, (hi-lo)/goarch.PtrSize)
	if wlo >= whi {
		memmove(dst, src, n)
		return
	}
	w, words := (wlo-base)/goarch.PtrSize, (whi-wlo)/goarch.PtrSize
	if !aligned {
		// No word arrives whole: none is a pointer.
		locked := ds.cScalars(b, w, words)
		memmove(dst, src, n)
		if locked {
			ds.cUnlock()
		}
		return
	}
	sw := sr + (wlo - d)
	// The bits of the source, if it has any.
	var sb *cBits
	var sbase uintptr
	var seq uint32
	ss := spanOfHeap(sw)
	switch {
	case ss == nil:
		// A variable's, or not Go's: any word may be a pointer.
	case ss.spanclass.c():
		sb = ss.cBitsOf()
		sbase = ss.base()
		if ss == ds {
			// One object's, or two of one span: the bits
			// of both are read and written under its
			// sequence.
			ds.cLock()
		} else if seq = ss.cseq.Load(); seq&1 != 0 {
			sb = nil
		}
	case ss.spanclass.noscan():
		// An object of Go's without pointers.
		locked := ds.cScalars(b, w, words)
		memmove(dst, src, n)
		if locked {
			ds.cUnlock()
		}
		return
	}
	if sb == nil {
		// Possible pointers, all of them.
		if ss == ds {
			ds.cUnlock()
		}
		if writeBarrier.enabled {
			buf := &getg().m.p.ptr().wbBuf
			for i := uintptr(0); i < whi-wlo; i += goarch.PtrSize {
				p := buf.get1()
				p[0] = *(*uintptr)(unsafe.Pointer(sw + i))
			}
		}
		ds.cPointers(b, w, words)
		memmove(dst, src, n)
		ds.cPointers(b, w, words)
		return
	}
	// The destination's bits become the source's before the words
	// are copied, and the collector sees the pointers that come. In
	// one span the bits are copied as the words are, from the end
	// that leaves what is still to be read as it was.
	sw0 := (sw - sbase) / goarch.PtrSize
	locked := ss == ds
	back := ss == ds && sw0 < w
	for done := uintptr(0); done < words; {
		var i, k uintptr
		if back {
			// The chunk that ends where the last one began.
			end := words - done
			k = (w+end-1)%cBitsWord + 1
			k = min(k, end)
			i = end - k
		} else {
			i = done
			k = min(words-i, cBitsWord-(w+i)%cBitsWord)
		}
		scalar := cBitsGet(sb, sw0+i, k)
		q := b.word((w + i) / cBitsWord)
		shift := (w + i) % cBitsWord
		all := uint32(uint64(1)<<k - 1)
		if have := atomic.Load(q) >> shift & all; have != scalar {
			if !locked {
				ds.cLock()
				locked = true
			}
			if clear := have &^ scalar; clear != 0 {
				atomic.And(q, ^(clear << shift))
			}
			if set := scalar &^ have; set != 0 {
				atomic.Or(q, set<<shift)
			}
		}
		if writeBarrier.enabled && scalar != all {
			buf := &getg().m.p.ptr().wbBuf
			for j := uintptr(0); j < k; j++ {
				if scalar>>j&1 == 0 {
					x := buf.get1()
					x[0] = *(*uintptr)(unsafe.Pointer(sw + (i+j)*goarch.PtrSize))
				}
			}
		}
		done += k
	}
	memmove(dst, src, n)
	if locked {
		ds.cUnlock()
	}
	if ss != ds && ss.cseq.Load() != seq {
		// A bit of the source changed as it was copied: a word
		// taken for an integer may have been a pointer by then.
		ds.cPointers(b, w, words)
	}
}

// c23Memset is memset for C: the pointers that it writes over are the
// collector's to see first, and the whole words that it writes in an
// object of C's hold no pointers after.
//
//go:linkname c23Memset
//go:nosplit
func c23Memset(dst unsafe.Pointer, c byte, n uintptr) {
	if n == 0 {
		return
	}
	d := uintptr(dst)
	lo, hi := alignDown(d, goarch.PtrSize), alignUp(d+n, goarch.PtrSize)
	locked := false
	s := cSpanOf(d)
	if b := s.cBitsOfOrNil(); b != nil {
		// Every word that is written in, whole or not.
		cShade(b, (lo-s.base())/goarch.PtrSize, lo, 0, (hi-lo)/goarch.PtrSize)
		if wlo, whi := alignUp(d, goarch.PtrSize), alignDown(d+n, goarch.PtrSize); wlo < whi {
			locked = s.cScalars(b, (wlo-s.base())/goarch.PtrSize, (whi-wlo)/goarch.PtrSize)
		}
	} else if writeBarrier.enabled {
		bulkBarrierPreWrite(lo, 0, hi-lo, nil)
	}
	if c == 0 {
		memclrNoHeapPointers(dst, n)
	} else {
		// The first bytes, and then twice what is there until it is all.
		k := min(n, 16)
		for i := uintptr(0); i < k; i++ {
			*(*byte)(add(dst, i)) = c
		}
		for k < n {
			m := min(k, n-k)
			memmove(add(dst, k), dst, m)
			k += m
		}
	}
	if locked {
		s.cUnlock()
	}
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
