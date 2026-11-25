// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"internal/goarch"
	"internal/runtime/atomic"
	"unsafe"
)

// The values of C's unions (doc/gd/cc.md, "Unions").
//
// A union of C's that has a pointer in a member is, where it is, its
// bytes, in which its members without pointers are, and one pointer.
// Whatever of it has a pointer is in an object of the heap of its own,
// a union value, which that pointer points to: so no word is an integer
// to one member and a pointer to another, and a pointer that was taken
// to a member is to an object that stays what it was when the union is
// used for something else and gets another value.
//
// A union value is an object of C's spans (cspan.go). It begins with a
// header of two words, what it is the value of:
//
//	typ  *_type          // the type of the value that follows
//	back unsafe.Pointer  // the pointer of the union that it is the value of
//
// and the value follows, its words claimed for what the type has there
// (cclaim.go), as malloc that is told the type claims them. The first
// word's claim says that the object is a union value (cUnionValueID).
// The back reference is how an access through a union knows that the
// value is its own, and not one that came in a copy of the union's
// bytes; and how a pointer to the value finds its union again.

// cUnionHeader is the size of the header of a union value.
const cUnionHeader = 2 * goarch.PtrSize

// cUnionValueID is the id of the claim of the first word of a union
// value, which holds its type: no type has it.
const cUnionValueID = ^uint32(0)

// cUnionHdr is the header of a union value.
type cUnionHdr struct {
	typ  *_type
	back unsafe.Pointer
}

// cUnionNew allocates a union value of typ, zero, for the union whose
// pointer is at back.
func cUnionNew(typ *_type, back unsafe.Pointer) unsafe.Pointer {
	const word = goarch.PtrSize
	x := mallocC(cUnionHeader + typ.Size_)
	if x == nil {
		throw("out of memory")
	}
	s := spanOfUnchecked(uintptr(x))
	b := s.cBitsOf()
	w := (uintptr(x) - s.base()) / word
	ids := s.cIDsOf()
	if ids == nil {
		ids = s.newCIDs()
	}
	atomic.Store(ids.word(w), cUnionValueID)
	cClassSet(b, w, 1, cTyped)
	cClassSet(b, w+1, 1, cUntyped)
	cClaimValues(s, b, w+cUnionHeader/word, typ, typ.Size_)
	h := (*cUnionHdr)(x)
	h.typ, h.back = typ, back
	publicationBarrier()
	return x
}

// cUnionValue returns the header of the union value that p points to
// the beginning of, or nil if it points to none.
func cUnionValue(p unsafe.Pointer) *cUnionHdr {
	s := cSpanOf(uintptr(p))
	if s == nil {
		return nil
	}
	b, ids := s.cBitsOf(), s.cIDsOf()
	if b == nil || ids == nil {
		return nil
	}
	i := s.objIndex(uintptr(p))
	if uintptr(p) != s.base()+i*s.elemsize || s.isFree(i) {
		return nil
	}
	w := (uintptr(p) - s.base()) / goarch.PtrSize
	if cClass(b, w) != cTyped || atomic.Load(ids.word(w)) != cUnionValueID {
		return nil
	}
	return (*cUnionHdr)(p)
}

// c23UnionBox returns the value of type typ of the union whose pointer
// is at slot: what the pointer points to, if that is the union's own
// value of that type, and otherwise a new value that the union gets:
//
//   - a copy of the one it has, if that is of typ and another union's:
//     it came in a copy of the union's bytes (memcpy, realloc);
//   - the pointer that it has, converted as a pointer of C's is, if it
//     has a pointer and typ is one;
//   - and otherwise zero.
//
// internal/c23's UnionBox makes the first comparison itself.
//
//go:linkname c23UnionBox
func c23UnionBox(slot *unsafe.Pointer, typ *_type) unsafe.Pointer {
	old := cUnionValue(*slot)
	if old != nil && old.typ == typ && old.back == unsafe.Pointer(slot) {
		return add(unsafe.Pointer(old), cUnionHeader)
	}
	x := cUnionNew(typ, unsafe.Pointer(slot))
	v := add(x, cUnionHeader)
	if old != nil {
		ov := add(unsafe.Pointer(old), cUnionHeader)
		switch {
		case old.typ == typ:
			c23Memmove(v, ov, typ.Size_)
		case cIsPointer(old.typ) && cIsPointer(typ):
			if p := *(*unsafe.Pointer)(ov); p != nil {
				if typ.Kind() == abi.Pointer {
					if elem := (*ptrtype)(unsafe.Pointer(typ)).Elem; elem.Size_ != 0 {
						checkptrAlignmentC(p, elem, 1)
					}
				}
				*(*unsafe.Pointer)(v) = p
			}
		}
	}
	*slot = x
	return v
}

// cIsPointer reports whether t is a pointer type.
func cIsPointer(t *_type) bool {
	return t.Kind() == abi.Pointer || t.Kind() == abi.UnsafePointer
}

// c23UnionClone is for a union that was copied whole, as part of a
// struct that was assigned: the copy, whose pointer is at slot, gets a
// value of its own now, as it would when it is next used.
//
//go:linkname c23UnionClone
func c23UnionClone(slot *unsafe.Pointer) {
	if old := cUnionValue(*slot); old != nil && old.back != unsafe.Pointer(slot) {
		c23UnionBox(slot, old.typ)
	}
}

// c23UnionPointer returns the pointer that the union value at x holds,
// if it is the value of a pointer: for internal/c23's functions that are
// given a union by value.
//
//go:linkname c23UnionPointer
func c23UnionPointer(x unsafe.Pointer) (unsafe.Pointer, bool) {
	h := cUnionValue(x)
	if h == nil || !cIsPointer(h.typ) {
		return nil, false
	}
	return *(*unsafe.Pointer)(add(x, cUnionHeader)), true
}
