// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/goarch"
	"internal/runtime/atomic"
	"unsafe"
)

// A cPlan is what a conversion of a pointer to its type claims each word
// of a value of a type for (cClaim): the class, the id of the type of a
// pointer, and whether a union begins there or has its UnionRef there
// (cUnionID). It is the same for every conversion to the type, and
// working it out walks the type for each word, so it is worked out once
// and kept, for the types of a few words or more that have pointers.
type cPlan struct {
	elem   *_type
	c      bool // the conversion is C's (cWant)
	unions bool // cUnionRefType was set when it was made
	// For cConvFast: whether a word of it is a union's, and, if it
	// has 2*cClassPer words or less, the classes of all of them as
	// they are in a span's bits.
	hasUnion bool
	small    bool
	cls      [2]uint64
	words    [cPlanMaxWords]cPlanWord
}

type cPlanWord struct {
	want, id uint32
	union    bool
}

// cPlanMaxWords is the most words of a type that has a cPlan.
const cPlanMaxWords = 64

// cPlans is where the cPlans are kept, by the address of the type: a
// plan replaces another of the same slot, and is never freed (there are
// as many as the slots at most, which is what persistentalloc is for).
var cPlans [256]*cPlan

// cPlanOf returns the cPlan of elem, a type with pointers and at most
// cPlanMaxWords words, for a conversion that is C's if c is set: nil if
// it has no plan.
func cPlanOf(elem *_type, c bool) *cPlan {
	words := elem.Size_ / goarch.PtrSize
	if words == 0 || words > cPlanMaxWords || elem.Size_%goarch.PtrSize != 0 {
		return nil
	}
	h := uintptr(unsafe.Pointer(elem)) >> 4
	if c {
		h++
	}
	slot := &cPlans[(h^h>>8)%uintptr(len(cPlans))]
	unions := cUnionRefType != nil
	if pl := (*cPlan)(atomic.Loadp(unsafe.Pointer(slot))); pl != nil && pl.elem == elem && pl.c == c && pl.unions == unions {
		return pl
	}
	pl := (*cPlan)(persistentalloc(unsafe.Sizeof(cPlan{}), goarch.PtrSize, &memstats.other_sys))
	pl.elem, pl.c, pl.unions = elem, c, unions
	mask := getGCMask(elem)
	for k := uintptr(0); k < words; k++ {
		off := k * goarch.PtrSize
		e := &pl.words[k]
		e.want = cScalar
		if kind, leaf, in := compatLeaf(elem, off); kind != compatScalar && (compatMaskBit(elem, mask, off) || kind >= compatString) {
			e.want, e.id = cWant(kind, leaf, in, c)
		}
		if class, uid := cUnionID(elem, off); uid != 0 {
			e.want, e.id, e.union = class, uid, true
			pl.hasUnion = true
		}
		if k < 2*cClassPer {
			pl.cls[k/cClassPer] |= uint64(e.want) << (8 * (k % cClassPer))
		}
	}
	pl.small = words <= 2*cClassPer
	atomicstorep(unsafe.Pointer(slot), unsafe.Pointer(pl))
	return pl
}

// cClaimFresh claims the words of a value of plan's type at a, the w'th
// word of s, if nothing claimed any of them and the words that are to
// be pointers hold nothing: what malloc gave, converted for the first
// time, which is most conversions that claim anything. It reports
// whether it did; if not, nothing is changed that cClaim's loop over
// the words does not expect.
//
// It does for all the words what cClaimWord does for each: the ids
// first, then the classes, a uint64 of them at once. Only while the
// collector is not marking, which cannot begin before this is done: a
// word that becomes one it looks at holds nothing, so there is nothing
// to show it.
func cClaimFresh(s *mspan, b *cBits, w, a uintptr, plan *cPlan) bool {
	const word = goarch.PtrSize
	k := plan.elem.Size_ / word
	mp := acquirem()
	if writeBarrier.enabled || mp.cprobe {
		releasem(mp)
		return false
	}
	for i := uintptr(0); i < k; {
		c := min(k-i, cClassPer-(w+i)%cClassPer)
		mask := cClassesOf(c) << ((w + i) % cClassPer * 8)
		if atomic.Load64(b.group((w+i)/cClassPer))&mask != 0 {
			releasem(mp)
			return false
		}
		i += c
	}
	var ids *cBits
	for i := uintptr(0); i < k; i++ {
		e := &plan.words[i]
		if e.want < cUntyped && !e.union {
			continue
		}
		if e.want >= cUntyped && *(*uintptr)(unsafe.Pointer(a + i*word)) != 0 {
			releasem(mp)
			return false
		}
		if e.id != 0 {
			if ids == nil {
				if ids = s.cIDsOf(); ids == nil {
					ids = s.newCIDs()
				}
			}
			atomic.Store(ids.word(w+i), e.id)
		}
	}
	for i := uintptr(0); i < k; {
		c := min(k-i, cClassPer-(w+i)%cClassPer)
		shift := (w + i) % cClassPer * 8
		var val uint64
		for j := uintptr(0); j < c; j++ {
			val |= uint64(plan.words[i+j].want) << (shift + 8*j)
		}
		p := b.group((w + i) / cClassPer)
		mask := cClassesOf(c) << shift
		for {
			old := atomic.Load64(p)
			if old&mask != 0 {
				// Another thread claimed a word meanwhile: the
				// rest is cClaim's to compare, word by word.
				// (The ids that were stored are of words whose
				// classes say that nothing claimed them.)
				if cClaimCounting || debug.cclaims != 0 {
					cClaimStats.claims.Add(int64(i))
				}
				releasem(mp)
				return false
			}
			if atomic.Cas64(p, old, old|val) {
				break
			}
		}
		i += c
	}
	if cClaimCounting || debug.cclaims != 0 {
		cClaimStats.claims.Add(int64(k))
	}
	releasem(mp)
	return true
}

// cConvFast is the conversion of a, the address of a pointer of C's
// with none of the bits that say more of it, to a pointer to elem, for
// the two cases that most conversions to a type with pointers are, and
// reports whether it was one of them; if not, nothing was done, and
// checkptrConvC1 goes on.
//
// The words that the value is over, in a small or large object of C's
// that the P remembers (cHeaderOf), are either claimed for just what
// elem has there, the same classes and the same types of pointers: then
// the conversion claims nothing and changes nothing, as each one after
// the first of a pointer to an element of an array of C++'s does. Or
// nothing claimed any of them: then they are claimed at once
// (cClaimFresh), the first conversion of what malloc gave.
//
// No union is in it: elem has none, the word at a is not one that says
// where a union begins or has its pointer, and a is not where the value
// of a union would begin in its object (cUnionConv).
func cConvFast(a uintptr, elem *_type, load bool) bool {
	const word = goarch.PtrSize
	if a&(word-1) != 0 || getg().m.cprobe {
		return false
	}
	plan := cPlanOf(elem, true)
	if plan == nil || plan.hasUnion || !plan.small {
		return false
	}
	h, base, size, ok := cHeaderOf(a)
	if !ok || a-base > size || elem.Size_ > size-(a-base) || a-base == cUnionHeader {
		return false
	}
	b := (*cBits)(unsafe.Pointer(h.bits))
	w, k := (a-h.base)/word, elem.Size_/word
	have := cClassWindow(b, w) & cClassesOf(k)
	var more uint64
	if k > cClassPer {
		more = cClassWindow(b, w+cClassPer) & cClassesOf(k-cClassPer)
	}
	s := (*mspan)(unsafe.Pointer(h.span))
	if have|more == 0 {
		return !load && cClaimFresh(s, b, w, a, plan)
	}
	if have != plan.cls[0] || more != plan.cls[1] {
		return false
	}
	ids := s.cIDsOf()
	for j := uintptr(0); j < k; j++ {
		if e := &plan.words[j]; e.want == cTyped && (ids == nil || atomic.Load(ids.word(w+j)) != e.id) {
			return false
		}
	}
	if plan.words[0].want != cTyped && ids != nil && atomic.Load(ids.word(w)) >= cUnionStartID {
		return false
	}
	return true
}
