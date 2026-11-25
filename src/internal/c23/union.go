// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"internal/abi"
	"unsafe"
)

// A union of C's that has pointers for members, and no member with a
// pointer inside it, is to Go its bytes and one interface value
// (doc/gd/cc.md, "Unions"). The members that are not pointers are in
// the bytes. The members that are pointers are all the pointer that the
// interface value holds, whose type is the type of the member that was
// used last: so a word of the union is never an integer to one member
// and a pointer to another, and the pointer is never one of a type to
// one member and of another type to the next without a look.

// UnionPtr returns the place of the pointer of the union whose
// interface value is at slot, for a member of type P, a pointer type.
// If the value holds a pointer of another type, it is converted to P as
// a pointer of C's is, which is checked, unless one of the two is a
// pointer to anything. The place is one that a pointer of type P may be
// loaded from and stored in for as long as the union's members of other
// pointer types are not used.
func UnionPtr[P any](slot *any) *P {
	e := (*abi.EmptyInterface)(unsafe.Pointer(slot))
	if t := abi.TypeFor[P](); e.Type != t {
		unionRetype(e, t)
	}
	return (*P)(unsafe.Pointer(&e.Data))
}

// unionRetype makes t the type of the pointer that e holds.
func unionRetype(e *abi.EmptyInterface, t *abi.Type) {
	if e.Data != nil && e.Type != nil && t.Kind() == abi.Pointer {
		if elem := t.Elem(); elem.Size() != 0 {
			unionConvert(e.Data, elem, 1)
		}
	}
	e.Type = t
}

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
