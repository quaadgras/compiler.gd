// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/runtime/atomic"
	"unsafe"
)

// The types of small objects (doc/gd/unsafe.md).
//
// An object with pointers of 512 bytes or less has no malloc header:
// where its pointers are is in its span's bitmap, and what it was
// allocated as is nowhere. A conversion of an unsafe.Pointer into it is
// compared with that, as it is for an object that has a header
// (checkptr_compat.go): a struct{ p *[1]int } is not to become a
// struct{ p *[1000]int }. So a span of such objects keeps it: a uint32
// for each object, the number of the type it was allocated for
// (objTypeID), or 0 for a type that has none, which is one that the
// program made as it ran. The object holds values of the type from its
// base on, as an object with a header does after it.
//
// The numbers are outside the heap, so the sweeper does not touch them.
// Each allocation in the span writes its own, before mallocgc's
// publication barrier, so a slot never keeps the type of the object
// that was there. They go back to a free list with their span. A span
// of objects without pointers has none.

// objTypesEnabled is false where the checks are off.
const objTypesEnabled = wholeBitsEnabled

// objTypesOf returns the numbers of the types of the objects of s: nil
// if nothing with pointers was allocated in it.
func (s *mspan) objTypesOf() *cBits {
	return (*cBits)(atomic.Loadp(unsafe.Pointer(&s.otypes)))
}

// objTypesBytes returns the size of the numbers of s.
func (s *mspan) objTypesBytes() uintptr {
	return uintptr(s.nelems) * 4
}

func (s *mspan) newObjTypes() *cBits {
	b := cMetaAlloc(s.objTypesBytes())
	atomic.StorepNoWB(unsafe.Pointer(&s.otypes), unsafe.Pointer(b))
	return b
}

// freeObjTypes gives back the numbers of s, which holds no objects.
//
// mheap_.lock must be held.
func (s *mspan) freeObjTypes() {
	if b := s.objTypesOf(); b != nil {
		n := s.objTypesBytes()
		atomic.StorepNoWB(unsafe.Pointer(&s.otypes), nil)
		cMetaFree(b, n)
	}
}

// objTypeID is cTypeID, the number that stands for the type t or 0, with
// one comparison: every allocation of a small object with pointers
// makes it.
//
//go:nosplit
func objTypeID(t *_type) uint32 {
	md := &firstmoduledata
	if d := uintptr(unsafe.Pointer(t)) - md.types; d < md.etypes-md.types {
		return uint32(d) + 1
	}
	return 0
}

// setObjType records that the i'th object of s, which has pointers and
// no malloc header, is allocated for values of typ. The caller is
// allocating it, and has not published it. (The allocation does it
// itself once the span has its numbers: only the allocation writes
// s.otypes, and it has the span.)
//
//go:noinline
func (s *mspan) setObjType(i uintptr, typ *_type) {
	t := s.otypes
	if t == nil {
		t = s.newObjTypes()
	}
	*t.word(i) = objTypeID(typ)
}

// objType returns the type that the i'th object of s, which has no
// malloc header, was allocated for, or nil if it is not known.
func (s *mspan) objType(i uintptr) *_type {
	if t := s.objTypesOf(); t != nil {
		if id := atomic.Load(t.word(i)); id != 0 {
			return cTypeOf(id)
		}
	}
	return nil
}
