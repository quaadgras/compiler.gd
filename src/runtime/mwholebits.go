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

// Which words of a small object are parts of strings, slices and
// interfaces (doc/gd/unsafe.md).
//
// An object of 512 bytes or less has only a pointer bitmap, in which a
// string looks the same as struct{p *T; n int}. So that checkptr can tell
// them apart, a span of such objects that holds a whole value has a
// wholeBits, with two bits for each word of the span: the word's state
// (wordPlain and the others, in checkptr.go), as one bit in hi and one in
// lo. A span that has never held one has a nil pointer, and a noscan span
// never has one.
//
// The bitmap is not in the GC's arenas, so the sweeper does not replace
// it: the states of the objects that survive stay where they are. Each
// allocation in a span with a bitmap writes the states of its slot, zero
// if its type holds no whole value, so a slot never keeps those of the
// object that was there. They are written before mallocgc's publication
// barrier. The bitmap goes back to a free list with its span.

// wholeBitsEnabled is false where the checks are off.
const wholeBitsEnabled = GOARCH != "wasm"

// wholeBitsLen is the length of each half of a span's bitmap. A span of
// objects without a malloc header is one page.
const wholeBitsLen = gc.PageSize / goarch.PtrSize / 64

type wholeBits struct {
	_  sys.NotInHeap
	hi [wholeBitsLen]uint64
	lo [wholeBitsLen]uint64
}

// wholeBitsFree lists the bitmaps of the spans that were freed, linked
// through their first word. It is guarded by mheap_.lock.
var wholeBitsFree *wholeBits

func newWholeBits() *wholeBits {
	var b *wholeBits
	systemstack(func() {
		lock(&mheap_.lock)
		b = wholeBitsFree
		if b != nil {
			wholeBitsFree = *(**wholeBits)(unsafe.Pointer(b))
		}
		unlock(&mheap_.lock)
		if b != nil {
			memclrNoHeapPointers(unsafe.Pointer(b), unsafe.Sizeof(wholeBits{}))
		} else {
			b = (*wholeBits)(persistentalloc(unsafe.Sizeof(wholeBits{}), 8, &memstats.gcMiscSys))
		}
	})
	return b
}

// freeWholeBits gives back the bitmap of s, which holds no objects.
//
// mheap_.lock must be held.
func (s *mspan) freeWholeBits() {
	if b := s.wholeBitsOf(); b != nil {
		atomic.StorepNoWB(unsafe.Pointer(&s.wholeBits), nil)
		*(**wholeBits)(unsafe.Pointer(b)) = wholeBitsFree
		wholeBitsFree = b
	}
}

func (s *mspan) wholeBitsOf() *wholeBits {
	return (*wholeBits)(atomic.Loadp(unsafe.Pointer(&s.wholeBits)))
}

// state returns the state of word w of the span.
func (b *wholeBits) state(w uintptr) uint8 {
	i, j := w/64, w%64
	return uint8(b.hi[i]>>j&1)<<1 | uint8(b.lo[i]>>j&1)
}

// wholeBitsGet returns the n <= 64 bits from bit o of p.
func wholeBitsGet(p *[wholeBitsLen]uint64, o, n uintptr) uint64 {
	i, j := o/64, o%64
	v := p[i] >> j
	if j+n > 64 {
		v |= p[i+1] << (64 - j)
	}
	return v & (^uint64(0) >> (64 - n))
}

// A wholeMask is the states of the words of a value of a type of at most
// 64 words that holds a whole value (abi.FieldAlignWhole). They are computed
// once from the type, so that its descriptor needs no room for them, which
// also covers the types that reflect makes. Types are never freed, and
// neither are these.
type wholeMask struct {
	_      sys.NotInHeap
	typ    *_type
	next   *wholeMask
	hi, lo uint64
}

const wholeMaskBuckets = 1 << 10

var wholeMasks [wholeMaskBuckets]*wholeMask

// wholeMaskFind returns the mask of typ, which is at most 64 words, for a
// type that is not the first of its bucket, which the callers look at
// themselves, or has no mask yet.
//
//go:noinline
func wholeMaskFind(typ *_type) *wholeMask {
	for m := (*wholeMask)(atomic.Loadp(unsafe.Pointer(&wholeMasks[typ.Hash%wholeMaskBuckets]))); m != nil; m = m.next {
		if m.typ == typ {
			return m
		}
	}
	head := &wholeMasks[typ.Hash%wholeMaskBuckets]
	var m *wholeMask
	systemstack(func() {
		m = (*wholeMask)(persistentalloc(unsafe.Sizeof(wholeMask{}), 8, &memstats.gcMiscSys))
		m.typ = typ
		for w := uintptr(0); w < typ.Size_/goarch.PtrSize; w++ {
			s := wordState(typ, w*goarch.PtrSize)
			m.hi |= uint64(s>>1) << w
			m.lo |= uint64(s&1) << w
		}
		for {
			old := atomic.Loadp(unsafe.Pointer(head))
			m.next = (*wholeMask)(old)
			if atomic.Casp1((*unsafe.Pointer)(unsafe.Pointer(head)), old, unsafe.Pointer(m)) {
				break
			}
		}
	})
	return m
}

// wholeRepeat returns the l-bit pattern v repeated for w bits, w <= 64.
func wholeRepeat(v uint64, l, w uintptr) uint64 {
	for s := l; s < w; s *= 2 {
		v |= v << (s & 63)
	}
	if w < 64 {
		v &= 1<<(w&63) - 1
	}
	return v
}

// needWholeBits reports whether an allocation of typ in span has states
// to write: its type holds a whole value, or the span has a bitmap, where
// the slot may have the states of another object.
func needWholeBits(span *mspan, typ *_type) bool {
	return wholeBitsEnabled && (typ.HoldsWhole() || span.wholeBits != nil)
}

// writeWholeBits writes the states of the new object at x, which holds
// values of type typ in its first dataSize bytes.
func (span *mspan) writeWholeBits(x, dataSize uintptr, typ *_type) {
	var hi, lo uint64
	if typ.HoldsWhole() {
		m := (*wholeMask)(atomic.Loadp(unsafe.Pointer(&wholeMasks[typ.Hash%wholeMaskBuckets])))
		if m == nil || m.typ != typ {
			m = wholeMaskFind(typ)
		}
		hi, lo = m.hi, m.lo
		if dataSize != typ.Size_ {
			l, w := typ.Size_/goarch.PtrSize, dataSize/goarch.PtrSize
			hi, lo = wholeRepeat(hi, l, w), wholeRepeat(lo, l, w)
		}
	}
	bits := span.wholeBitsOf()
	if bits == nil {
		if hi|lo == 0 {
			return
		}
		bits = newWholeBits()
		// (After the bitmap is clear: a reader of another object of
		// the span may load it at once.)
		atomic.StorepNoWB(unsafe.Pointer(&span.wholeBits), unsafe.Pointer(bits))
	}
	// Both halves at once. Other objects' bits in the same words stay as
	// they are at every moment: another goroutine may be reading them.
	// (n is 1 to 64 and j below 64, so the shifts need no test.)
	o, n := (x-span.base())/goarch.PtrSize, span.elemsize/goarch.PtrSize
	i, j := o/64, o%64
	mask := ^uint64(0) >> ((64 - n) & 63)
	ph := (*uint64)(add(unsafe.Pointer(&bits.hi), i*8))
	pl := (*uint64)(add(unsafe.Pointer(&bits.lo), i*8))
	*ph = *ph&^(mask<<(j&63)) | hi<<(j&63)
	*pl = *pl&^(mask<<(j&63)) | lo<<(j&63)
	if j+n > 64 {
		k := (64 - j) & 63
		ph, pl = (*uint64)(add(unsafe.Pointer(ph), 8)), (*uint64)(add(unsafe.Pointer(pl), 8))
		*ph = *ph&^(mask>>k) | hi>>k
		*pl = *pl&^(mask>>k) | lo>>k
	}
}

// checkptrWholeSmall is checkptrWhole for the object at base of s, of 512
// bytes or less, which has no recorded type.
func checkptrWholeSmall(s *mspan, base, p uintptr, elem *_type, size uintptr) {
	bits := s.wholeBitsOf()
	limit := base + s.elemsize
	if bits == nil || size == 0 || limit > s.limit {
		// (Past the last object of the span there is no object, and
		// the words of one might not be in the bitmap.)
		return
	}
	const word = goarch.PtrSize
	// The usual cases, without looking at the view's type word by word:
	// the words it touches are plain, or it is of whole words and they
	// are in the states of its values.
	end := p + min(size, limit-p)
	o, n := (p-s.base())/word, (end-1)/word-p/word+1
	have1, have0 := wholeBitsGet(&bits.hi, o, n), wholeBitsGet(&bits.lo, o, n)
	var want1, want0 uint64
	ok := true
	if have1|have0 != 0 && elem.HoldsWhole() {
		ok = p&(word-1) == 0 && size <= limit-p && size%elem.Size_ == 0
		if ok {
			m := (*wholeMask)(atomic.Loadp(unsafe.Pointer(&wholeMasks[elem.Hash%wholeMaskBuckets])))
			if m == nil || m.typ != elem {
				m = wholeMaskFind(elem)
			}
			want1, want0 = m.hi, m.lo
			if size != elem.Size_ {
				l := elem.Size_ / word
				want1, want0 = wholeRepeat(want1, l, n), wholeRepeat(want0, l, n)
			}
		}
	}
	// Where the object's word is not plain the view's must be the same,
	// and a later word of a value of the view must be over the same sort
	// of memory as the word before it.
	used := have1 | have0
	differ := ((have1 ^ want1) | (have0 ^ want0)) & used
	mixed := (used ^ used<<1) & want1 &^ want0
	if ok && differ|mixed == 0 {
		past := alignUp(end, word)
		if past >= limit || bits.state((past-s.base())/word) != wordCont {
			return
		}
	}
	if s.isFree(s.objIndex(base)) {
		// A pointer kept from before: these are not its states.
		return
	}
	obj := wholeObject{bits: bits, tbase: base, sbase: s.base()}
	checkptrWholeIn(&obj, limit, p, elem, size)
}
