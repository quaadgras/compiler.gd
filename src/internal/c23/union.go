// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"internal/abi"
	"unsafe"
)

// A union of C's that has pointers in its members is to Go its bytes and
// one interface value (doc/gd/cc.md, "Unions"). The members that have
// no pointer are in the bytes. Whatever of it has a pointer is in the
// interface value, whose type says what that is: so a word of the union
// is never an integer to one member and a pointer to another, and what
// the value holds is never one thing to one member and another to the
// next without a look.

// What the interface value of a union holds is one of three things, and
// its type says which:
//
//   - a pointer, for a member that is one (UnionPtr): the type is the
//     member's pointer type;
//   - a value of one pointer and nothing else, a struct of C's that is
//     one, where it is, for a cast of the address of the union's bytes
//     to a pointer to such a type (UnionVal): the type is the struct's;
//   - anything else that has a pointer in it, a member that is a struct
//     or an array with pointers or the target of such a cast, in an
//     object of its own that the value points to (UnionBox): the type is
//     Box's of it. A pointer to such a member is a pointer to that
//     object, which stays what it is when the union is used for
//     something else and gets another.
//
// A union that is used as another of the three, or as a value or a box
// of another type, begins as zero for it: only a pointer that is read as
// a pointer of another type is the same pointer, converted.

// UnionPtr returns the place of the pointer of the union whose
// interface value is at slot, for a member of type P, a pointer type.
// If the value holds a pointer of another type, it is converted to P as
// a pointer of C's is, which is checked. The place is one that a
// pointer of type P may be loaded from and stored in for as long as the
// union is not used for something else.
func UnionPtr[P any](slot *any) *P {
	e := (*abi.EmptyInterface)(unsafe.Pointer(slot))
	if t := abi.TypeFor[P](); e.Type != t {
		unionRetype(e, t)
	}
	return (*P)(unsafe.Pointer(&e.Data))
}

// unionRetype makes t, a pointer type, the type of what e holds.
func unionRetype(e *abi.EmptyInterface, t *abi.Type) {
	if e.Data != nil && e.Type != nil {
		switch e.Type.Kind() {
		case abi.Pointer, abi.UnsafePointer:
			if t.Kind() == abi.Pointer {
				if elem := t.Elem(); elem.Size() != 0 {
					unionConvert(e.Data, elem, 1)
				}
			}
		default:
			// A value, or a box: not a pointer of the member's.
			e.Data = nil
		}
	}
	e.Type = t
}

// UnionVal returns the place of the value of type T, a struct that is
// one pointer, of the union whose interface value is at slot: the
// pointer that the value holds, as a T.
func UnionVal[T any](slot *any) *T {
	e := (*abi.EmptyInterface)(unsafe.Pointer(slot))
	if t := abi.TypeFor[T](); e.Type != t {
		e.Data = nil
		e.Type = t
	}
	return (*T)(unsafe.Pointer(&e.Data))
}

// A Box is the object that a union's member of type M is in, if M has
// pointers and is more than one: with a byte after it, as every object
// of C's on the heap has, for the pointer to its end.
type Box[M any] struct {
	V M
	_ byte
}

// UnionBox returns the member of type M of the union whose interface
// value is at slot: the object that the value points to, a new one if
// the union was used for something else.
func UnionBox[M any](slot *any) *M {
	e := (*abi.EmptyInterface)(unsafe.Pointer(slot))
	if t := abi.TypeFor[Box[M]](); e.Type != t || e.Data == nil {
		e.Data = unsafe.Pointer(new(Box[M]))
		e.Type = t
	}
	return &(*Box[M])(e.Data).V
}

// UnionClone is for a union that was copied whole, as part of a struct
// that was assigned: if the interface value at slot points to a box,
// the copy gets a box of its own.
func UnionClone(slot *any) {
	e := (*abi.EmptyInterface)(unsafe.Pointer(slot))
	if t := e.Type; t != nil && e.Data != nil && t.Kind() == abi.Struct && t.TFlag&abi.TFlagDirectIface == 0 {
		p := unionNew(t)
		unionMove(t, p, e.Data)
		e.Data = p
	}
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
// union is one that is laid out as bytes and an interface value: the
// library's functions that take such a union (semctl's union semun,
// which the program declares) look there for its pointer members.
func UnionValuePtr(p *Void) (*Void, bool) {
	const iface = unsafe.Sizeof(any(nil))
	n, ok := usable(unsafe.Pointer(p))
	if !ok || unsafe.Sizeof(uintptr(0)) != 8 || n < 8+iface || n%8 != 0 {
		return nil, false
	}
	e := (*abi.EmptyInterface)(unsafe.Add(unsafe.Pointer(p), n-iface))
	return (*Void)(e.Data), true
}
