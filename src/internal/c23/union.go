// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"internal/abi"
	"unsafe"
)

// A union of C's that has a pointer in a member is to Go its bytes and
// one pointer (doc/gd/cc.md, "Unions"). The members that have no
// pointer are in the bytes. Whatever of it has a pointer, a member that
// is one or that has one inside it, or a value that C makes in its
// bytes through a cast, is in an object of the heap of its own, a Box,
// which the pointer points to. So a word of the union is never an
// integer to one member and a pointer to another; and a pointer that
// was taken to a member points to the object that the member was in
// then, which stays what it was when the union is used for something
// else and gets another.
//
// A Box begins with what it is, the type of the Box itself, and with
// where the union is that it is the value of, its back reference. An
// access through a union looks at both: a Box of another type is not
// the member's, and a Box of another union is one that came here in a
// copy of the union's bytes (memcpy, realloc), which the union then
// gets a copy of for itself.

// A Box is the object that a union's value of type M is in, with a byte
// after it, as every object of C's on the heap has, for the pointer to
// its end.
type Box[M any] struct {
	boxHeader
	V M
	_ byte
}

// A boxHeader is what every Box begins with.
type boxHeader struct {
	typ  *abi.Type      // the Box's own type
	back unsafe.Pointer // the pointer of the union that this is the value of
}

// UnionBox returns the value of type M of the union whose pointer is at
// slot: the object that the pointer points to, if it is the union's own
// Box of an M, and otherwise a new one that the union gets.
func UnionBox[M any](slot *unsafe.Pointer) *M {
	h := (*boxHeader)(*slot)
	if t := abi.TypeFor[Box[M]](); h == nil || h.typ != t || h.back != unsafe.Pointer(slot) {
		h = unionRebox[M](slot, h, t)
	}
	return &(*Box[M])(unsafe.Pointer(h)).V
}

// unionRebox gives the union whose pointer is at slot a Box of an M of
// its own: a copy of the one it has, h, if that is an M's and another
// union's; the pointer that it holds converted, if it holds a pointer
// and M is one; and otherwise one that is zero.
func unionRebox[M any](slot *unsafe.Pointer, h *boxHeader, t *abi.Type) *boxHeader {
	b := new(Box[M])
	b.typ, b.back = t, unsafe.Pointer(slot)
	switch {
	case h == nil:
	case h.typ == t:
		b.V = (*Box[M])(unsafe.Pointer(h)).V
	default:
		// A pointer member that is read as another: the same
		// pointer, converted as a pointer of C's is, which is
		// checked.
		if mt := abi.TypeFor[M](); mt.Kind() == abi.Pointer || mt.Kind() == abi.UnsafePointer {
			if p, ok := boxPointer(h); ok && p != nil {
				if mt.Kind() == abi.Pointer {
					if elem := mt.Elem(); elem.Size() != 0 {
						unionConvert(p, elem, 1)
					}
				}
				*(*unsafe.Pointer)(unsafe.Pointer(&b.V)) = p
			}
		}
	}
	*slot = unsafe.Pointer(b)
	return &b.boxHeader
}

// boxPointer returns the pointer that the Box at h holds, if it is the
// Box of a pointer.
func boxPointer(h *boxHeader) (unsafe.Pointer, bool) {
	st := h.typ.StructType()
	if st == nil || len(st.Fields) < 2 {
		return nil, false
	}
	if k := st.Fields[1].Typ.Kind(); k != abi.Pointer && k != abi.UnsafePointer {
		return nil, false
	}
	return *(*unsafe.Pointer)(unsafe.Add(unsafe.Pointer(h), st.Fields[1].Offset)), true
}

// UnionClone is for a union that was copied whole, as part of a struct
// that was assigned: the copy, whose pointer is at slot, gets a Box of
// its own now, as it would when it is next used.
func UnionClone(slot *unsafe.Pointer) {
	h := (*boxHeader)(*slot)
	if h == nil || h.back == unsafe.Pointer(slot) {
		return
	}
	p := unionNew(h.typ)
	unionMove(h.typ, p, unsafe.Pointer(h))
	(*boxHeader)(p).back = unsafe.Pointer(slot)
	*slot = p
}

//go:linkname unionNew runtime.c23New
func unionNew(t *abi.Type) unsafe.Pointer

//go:linkname unionMove runtime.c23TypedMove
func unionMove(t *abi.Type, dst, src unsafe.Pointer)

// unionConvert is the check of the conversion of p to a pointer to
// elem that a package of C's makes.
//
//go:linkname unionConvert runtime.c23Convert
func unionConvert(p unsafe.Pointer, elem *abi.Type, n uintptr)

// UnionValuePtr returns the pointer that a union holds, of which p is
// the copy that was passed by value in variable arguments, if the
// union is one that is laid out as bytes and a pointer to a Box: the
// library's functions that take such a union (semctl's union semun,
// which the program declares) look there for its pointer members.
func UnionValuePtr(p *Void) (*Void, bool) {
	const word = unsafe.Sizeof(uintptr(0))
	n, ok := usable(unsafe.Pointer(p))
	if !ok || word != 8 || n < 2*word || n%word != 0 {
		return nil, false
	}
	h := (*boxHeader)(*(*unsafe.Pointer)(unsafe.Add(unsafe.Pointer(p), n-word)))
	if h == nil {
		return nil, true
	}
	q, ok := boxPointer(h)
	return (*Void)(q), ok
}
