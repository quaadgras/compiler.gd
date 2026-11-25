// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"internal/abi"
	"unsafe"
)

// A union of C's that has a pointer in a member is to Go its bytes and
// one pointer, a UnionRef (doc/gd/cc.md, "Unions"). The members that
// have no pointer are in the bytes. Whatever of it has a pointer, a
// member that is one or that has one inside it, or a value that C makes
// in its bytes through a cast, is in an object of the heap of its own, a
// union value (runtime/cunion.go), which the UnionRef points to. So a
// word of the union is never an integer to one member and a pointer to
// another; and a pointer that was taken to a member points to the
// object that the member was in then, which stays what it was when the
// union is used for something else and gets another.

// A UnionRef is the pointer of a union to its value: nil, or the
// address of a union value, which begins with a unionHeader.
type UnionRef unsafe.Pointer

// A unionHeader is what a union value begins with: runtime's cUnionHdr.
type unionHeader struct {
	typ  *abi.Type      // the type of the value that follows
	back unsafe.Pointer // the UnionRef of the union that this is the value of
}

// UnionBox returns the value of type M of the union whose UnionRef is
// at slot: what it points to, if that is the union's own value of an M,
// and otherwise a new one that the union gets (runtime.c23UnionBox: a
// copy, if the value came in a copy of the union's bytes; the same
// pointer converted, if M and what the union held are pointers; zero
// otherwise).
func UnionBox[M any](slot *UnionRef) *M {
	t := abi.TypeFor[M]()
	if h := (*unionHeader)(*slot); h != nil && h.typ == t && h.back == unsafe.Pointer(slot) {
		return (*M)(unsafe.Add(unsafe.Pointer(h), unsafe.Sizeof(unionHeader{})))
	}
	return (*M)(unionBox(slot, t))
}

//go:linkname unionBox runtime.c23UnionBox
func unionBox(slot *UnionRef, t *abi.Type) unsafe.Pointer

// UnionClone is for a union that was copied whole, as part of a struct
// that was assigned: the copy, whose UnionRef is at slot, gets a value
// of its own now, as it would when it is next used.
func UnionClone(slot *UnionRef) {
	if h := (*unionHeader)(*slot); h != nil && h.back != unsafe.Pointer(slot) {
		unionClone(slot)
	}
}

//go:linkname unionClone runtime.c23UnionClone
func unionClone(slot *UnionRef)

//go:linkname unionPointer runtime.c23UnionPointer
func unionPointer(x unsafe.Pointer) (unsafe.Pointer, bool)

// UnionValuePtr returns the pointer that a union holds, of which p is
// the copy that was passed by value in variable arguments, if the
// union is one that is laid out as bytes and a UnionRef: the library's
// functions that take such a union (semctl's union semun, which the
// program declares) look there for its pointer members.
func UnionValuePtr(p *Void) (*Void, bool) {
	const word = unsafe.Sizeof(uintptr(0))
	n, ok := usable(unsafe.Pointer(p))
	if !ok || word != 8 || n < 2*word || n%word != 0 {
		return nil, false
	}
	ref := *(*unsafe.Pointer)(unsafe.Add(unsafe.Pointer(p), n-word))
	if ref == nil {
		return nil, true
	}
	q, ok := unionPointer(ref)
	return (*Void)(q), ok
}
