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
	words  [cPlanMaxWords]cPlanWord
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
		}
	}
	atomicstorep(unsafe.Pointer(slot), unsafe.Pointer(pl))
	return pl
}
