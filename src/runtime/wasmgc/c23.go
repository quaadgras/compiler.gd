// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc && wasm.split

package runtime

import (
	"internal/abi"
	"unsafe"
)

// Hooks for C compiled by cmd/cc (internal/c23, doc/gd/cc.md, "On
// wasm"), with GOWASM=gc,split.
//
// What the runtime does for C's memory elsewhere (../cclaim.go) it does
// not do here, and need not. Every pointer is an object of the engine's
// that has the size of what it points into, and code that is not the
// runtime's loads and stores through it only what is inside
// (cmd/compile/internal/wasm/gcsplit.go): so malloc's memory needs no
// claims of what its words are. A word that holds a pointer is an
// element of its block's array, which bytes written over the word in
// linear memory do not change: what is loaded from there is the pointer
// that was stored, or, if the bytes are no longer its address, a
// pointer with no size, which nothing is loaded through. And an integer
// is never a pointer: one that C converts to a pointer is such a
// pointer with no size.

// c23Object returns the base of what p points into, as a pointer
// derived from p (so with p's array of pointers), and its size; nil, 0
// if p is a pointer to nothing: one made of an integer.
//
//go:linkname c23Object
func c23Object(p unsafe.Pointer) (unsafe.Pointer, uintptr) {
	lim := ptrlim(p)
	if p == nil || lim == 0 {
		return nil, 0
	}
	return unsafe.Add(p, -int(ptroff(p))), lim
}

// c23GTable returns the goroutine's slot for internal/c23's table of
// it (_Thread_local objects, errno, pthread keys, thread id).
//
//go:linkname c23GTable
func c23GTable() *unsafe.Pointer { return &curg.c23 }

// c23Malloc allocates n bytes, zeroed, for C: a block that has an
// element of its array for every word, since any of them may come to
// hold a pointer, unless scalar is set. Its size is what was asked for:
// the byte after it is not C's.
//
//go:linkname c23Malloc
func c23Malloc(n uintptr, typ unsafe.Pointer, scalar bool) unsafe.Pointer {
	if n > maxLinear {
		return nil
	}
	if n == 0 {
		// (A pointer of its own, to nothing.)
		return newblockptr(linalloc(1), 0, 0)
	}
	var nslots uintptr
	if !scalar {
		nslots = splitslots(n)
	}
	memstatsMallocs++
	return splitalloc(n, nslots)
}

// c23Extent returns the number of bytes before p, and from p on, in
// what p points into, and whether that is anything.
//
//go:linkname c23Extent
func c23Extent(p unsafe.Pointer) (before, after uintptr, ok bool) {
	if p == nil {
		return 0, 0, false
	}
	off, lim := ptroff(p), ptrlim(p)
	if lim == 0 || off > lim {
		return 0, 0, false
	}
	return off, lim - off, true
}

// c23Trim returns x, the pointer to a block that was just allocated,
// as a pointer to its first size bytes.
//
//go:linkname c23Trim
func c23Trim(x unsafe.Pointer, size uintptr) unsafe.Pointer {
	if x == nil || ptroff(x) != 0 || size >= ptrlim(x) {
		return x
	}
	return splitsetlim(x, size)
}

// c23Usable returns the number of bytes from p to the end of what it
// points into, and whether it points into a block of the heap.
//
//go:linkname c23Usable
func c23Usable(p unsafe.Pointer) (uintptr, bool) {
	if p == nil || splitbase(p) == 0 {
		return 0, false
	}
	off, lim := ptroff(p), ptrlim(p)
	if off > lim {
		return 0, true
	}
	return lim - off, true
}

// c23BytesStored is called before code that is not C's writes bytes to
// the n bytes at p. The pointers that were there stay in the elements
// of the array, which nothing reads as them any more: a word whose
// bytes are not the address of its element's pointer is loaded as a
// pointer with no size (wasm.loadptr).
//
//go:linkname c23BytesStored
func c23BytesStored(p unsafe.Pointer, n uintptr) {}

// c23Memmove copies n bytes, and the pointers of the words among them:
// all of them are in what to and from point into.
//
//go:linkname c23Memmove
func c23Memmove(to, from unsafe.Pointer, n uintptr) {
	if n != 0 {
		splitmovec(to, from, n)
	}
}

// c23Memset sets the n bytes at to, which are in what it points into,
// to c.
//
//go:linkname c23Memset
func c23Memset(to unsafe.Pointer, c byte, n uintptr) {
	if n == 0 {
		return
	}
	// (Zeroed first, with the elements of the array: no pointer is
	// left where there are bytes.)
	splitzeroc(to, n)
	if c != 0 {
		linfill(uintptr(to), c, n)
	}
}

// linfill sets the n bytes at addr to c.
func linfill(addr uintptr, c byte, n uintptr) {
	w := uint64(c) * 0x0101010101010101
	for ; n >= 8; addr, n = addr+8, n-8 {
		linstore64(addr, w)
	}
	for ; n > 0; addr, n = addr+1, n-1 {
		linstore8(addr, c)
	}
}

// c23IntPtr is the conversion of the integer v to a pointer, for C: a
// pointer that C may keep and compare, and that nothing is loaded or
// stored through.
//
//go:linkname c23IntPtr
func c23IntPtr(v uintptr) unsafe.Pointer { return splitintptr(v) }

// c23IsPointer reports whether p is a pointer to something: not nil,
// and not an integer that was converted to a pointer.
//
//go:linkname c23IsPointer
func c23IsPointer(p unsafe.Pointer) bool { return p != nil && ptrlim(p) != 0 }

// c23InObject says where the address r is in what the pointer of
// address p points into. (It is not asked with split: a pointer that
// arithmetic moves keeps its bounds, whatever the result.)
//
//go:linkname c23InObject
func c23InObject(p, r uintptr) int { return 0 }

//go:linkname c23AllowIntegers
func c23AllowIntegers() {}

//go:linkname c23SetIntcap
func c23SetIntcap(t *_type) {}

// The objects of the library's own.
//
// internal/c23 gives C pointers to objects of Go's that it made (a
// FILE, a va_list, a function), and is given them back: c23IsObject
// says that a pointer is to the start of one that c23NewObject
// allocated as that type. C cannot make one: what says so is not in
// memory that a pointer of C's reaches.
var c23Objects map[uintptr]*_type

// c23NewObject allocates an object of type t, zeroed.
//
//go:linkname c23NewObject
func c23NewObject(t *_type) unsafe.Pointer {
	p := newobject(t)
	if c23Objects == nil {
		c23Objects = make(map[uintptr]*_type)
	}
	c23Objects[uintptr(p)] = t
	return p
}

// c23IsObject reports whether p is the address of an object that
// c23NewObject allocated as a value of type t: a pointer to the start
// of the block, with the block's array, which is not something that
// arithmetic on another block's pointer or an integer gives.
//
//go:linkname c23IsObject
func c23IsObject(p unsafe.Pointer, t *_type) bool {
	if p == nil || c23Objects == nil || ptroff(p) != 0 || ptrnslots(p) == 0 {
		return false
	}
	return c23Objects[uintptr(p)] == t
}

// c23Seal returns p, a pointer to an object of the library's, as the
// pointer that C is given to it: one with no size, which nothing is
// loaded or stored through. C has it to keep, to compare and to give
// back, and cannot look at what it points to or change it.
//
//go:linkname c23Seal
func c23Seal(p unsafe.Pointer) unsafe.Pointer { return splitsetlim(p, 0) }

// c23Unseal returns the pointer to the object of type t that p is the
// sealed pointer to (c23IsObject), or nil.
//
//go:linkname c23Unseal
func c23Unseal(p unsafe.Pointer, t *_type) unsafe.Pointer {
	if !c23IsObject(p, t) {
		return nil
	}
	return splitsetlim(p, t.Size_)
}

// c23ObjectFreed is called when the block at addr is freed.
func c23ObjectFreed(addr uintptr) {
	if c23Objects != nil {
		delete(c23Objects, addr)
	}
}

// Unions.
//
// With 4-byte words cmd/cc lays a union out as C does, a pointer where
// an integer may be: the two are not the same thing here (see above).
// What is left is the unions of the library's own headers, which it
// keeps as it does elsewhere (internal/c23's union.go): a pointer to a
// value of the heap that has what it is the value of before it.

type cUnionHdr struct {
	typ  *_type
	back unsafe.Pointer
}

// cUnionMark is the type that c23Objects has for a union value.
var cUnionMark _type

func cUnionValue(p unsafe.Pointer) *cUnionHdr {
	if p == nil || c23Objects == nil || ptroff(p) != 0 || c23Objects[uintptr(p)] != &cUnionMark {
		return nil
	}
	return (*cUnionHdr)(p)
}

//go:linkname c23SetUnionRef
func c23SetUnionRef(t *_type) {}

//go:linkname c23HasUnions
func c23HasUnions() bool { return true }

//go:linkname c23UnionGlobal
func c23UnionGlobal(p unsafe.Pointer, t *_type) {}

// c23UnionBox returns the value of type typ of the union whose pointer
// is at slot: what the pointer points to, if that is the union's own
// value of that type, and otherwise a new value that the union gets (a
// copy of the one it has, if that is of typ and another union's; the
// pointer that it has, if it has one and typ is a pointer; or zero).
//
//go:linkname c23UnionBox
func c23UnionBox(slot *unsafe.Pointer, typ *_type) unsafe.Pointer {
	const hdr = unsafe.Sizeof(cUnionHdr{})
	old := cUnionValue(*slot)
	if old != nil && old.typ == typ && old.back == unsafe.Pointer(slot) {
		return add(unsafe.Pointer(old), hdr)
	}
	size := hdr + typ.Size_
	x := splitalloc(size, splitslots(size))
	if c23Objects == nil {
		c23Objects = make(map[uintptr]*_type)
	}
	c23Objects[uintptr(x)] = &cUnionMark
	h := (*cUnionHdr)(x)
	h.typ, h.back = typ, unsafe.Pointer(slot)
	v := add(x, hdr)
	if old != nil {
		ov := add(unsafe.Pointer(old), hdr)
		switch {
		case old.typ == typ:
			splitmove(v, ov, typ.Size_)
		case cIsPointer(old.typ) && cIsPointer(typ):
			*(*unsafe.Pointer)(v) = *(*unsafe.Pointer)(ov)
		}
	}
	*slot = x
	return v
}

func cIsPointer(t *_type) bool {
	return t.Kind() == abi.Pointer || t.Kind() == abi.UnsafePointer
}

//go:linkname c23UnionClone
func c23UnionClone(slot *unsafe.Pointer) {
	if old := cUnionValue(*slot); old != nil && old.back != unsafe.Pointer(slot) {
		c23UnionBox(slot, old.typ)
	}
}

//go:linkname c23UnionPointer
func c23UnionPointer(x unsafe.Pointer) (unsafe.Pointer, bool) {
	h := cUnionValue(x)
	if h == nil || !cIsPointer(h.typ) {
		return nil, false
	}
	return *(*unsafe.Pointer)(add(x, unsafe.Sizeof(cUnionHdr{}))), true
}
