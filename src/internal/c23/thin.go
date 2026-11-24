// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"internal/abi"
	"unsafe"
)

// What cmd/cc's translation (doc/gd/cc.md) uses, which is 1:1: there a C
// pointer is Go's, *T or unsafe.Pointer, a C object is a Go variable of
// the type that mirrors it, and the checks are the ones the compiler
// makes of unsafe.Pointer (doc/gd/unsafe.md), not this package's. The
// library is still this package's, which takes a *Void: Thin makes one of
// a Go pointer where the library is called.

// Lit is the array A whose bytes are s: a string literal's object.
func Lit[A any](s string) (a A) {
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&a)), unsafe.Sizeof(a)), s)
	return a
}

// What malloc gives is an object of the runtime's spans of C's objects
// (runtime/cspan.go). It is n bytes to every check of a pointer, with a
// byte after it that the pointer to its end points to. For each of its
// words the runtime knows whether it holds a pointer or none, from what
// C stores there, and the collector looks at a word that may hold one
// as it looks at a word that it has no type for.

// AllocOf is malloc(n) converted to *T: its words begin as those of an
// array of T's, pointers where T has them.
func AllocOf[T any](n uint64) *T {
	if n != uint64(uintptr(n)) {
		return nil
	}
	return (*T)(malloc(uintptr(n), unsafe.Pointer(abi.TypeFor[T]()), false))
}

// AllocRaw is malloc(n) that is given no type: any word of it may hold
// a pointer, until C stores something else there. NULL if there is no
// object of n bytes.
func AllocRaw(n uint64) *Void {
	if n != uint64(uintptr(n)) {
		return nil
	}
	return (*Void)(malloc(uintptr(n), nil, false))
}

// malloc allocates n bytes, zeroed: for values of typ, a *abi.Type, if
// it is not nil, and for no pointers if scalar is set.
//
//go:linkname malloc runtime.c23Malloc
func malloc(n uintptr, typ unsafe.Pointer, scalar bool) unsafe.Pointer

// memmove copies n bytes, the words of them that may be pointers as
// pointers are copied.
//
//go:linkname memmove runtime.c23Memmove
//go:noescape
func memmove(to, from unsafe.Pointer, n uintptr)

//go:linkname memset runtime.c23Memset
//go:noescape
func memset(to unsafe.Pointer, c byte, n uintptr)

// usable returns the number of bytes from p to the end of the object of
// C's that it points into, and whether it points into one.
//
//go:linkname usable runtime.c23Usable
//go:noescape
func usable(p unsafe.Pointer) (uintptr, bool)

// sizeOf is the number of bytes from p to the end of the heap object it
// points into: 0 if it points to none.
func sizeOf(p unsafe.Pointer) uintptr {
	base, size := object(p)
	if base == nil {
		return 0
	}
	return size - (uintptr(p) - uintptr(base))
}

// ReallocOf1 is realloc(p, n) converted to *T.
func ReallocOf1[T any](p *T, n uint64) *T {
	q := AllocOf[T](n)
	if p != nil && q != nil {
		memmove(unsafe.Pointer(q), unsafe.Pointer(p), min(uintptr(n), sizeOf(unsafe.Pointer(p))))
	}
	return q
}

// ReallocRaw is realloc(p, n) that is given no type.
func ReallocRaw(p *Void, n uint64) *Void {
	q := AllocRaw(n)
	if p != nil && q != nil {
		memmove(unsafe.Pointer(q), unsafe.Pointer(p), min(uintptr(n), sizeOf(unsafe.Pointer(p))))
	}
	return q
}

// Free1 is free: the collector frees.
func Free1(p *Void) {}

// MallocAligned is aligned_alloc: n bytes at a multiple of align.
func MallocAligned(align, n uintptr) *Void {
	p := AllocRaw(uint64(n + align))
	return Add(p, int((align-Addr(p)%align)%align))
}

// NewAligned allocates an object of type T at a multiple of align.
func NewAligned[T any](align uintptr) *Void { return NewAlignedT(TinfoOf[T](), align) }

// NewAlignedT allocates an object of type t at a multiple of align.
func NewAlignedT(t *Type, align uintptr) *Void { return NewArrayAlignedT(t, t.size, align) }

// NewArrayAlignedT allocates n bytes as an array of t's at a multiple
// of align: in memory that has no type, since Go aligns no type so.
func NewArrayAlignedT(t *Type, n, align uintptr) *Void { return MallocAligned(align, max(n, 1)) }

// The library's functions that 1:1 code calls most, on Go's pointers.

// Strlen1 is strlen.
func Strlen1(s *Void) uint64 {
	n := uint64(0)
	for *(*byte)(unsafe.Add(unsafe.Pointer(s), n)) != 0 {
		n++
	}
	return n
}

// Strcmp1 is strcmp.
func Strcmp1(a, b *Void) int32 {
	for i := 0; ; i++ {
		x, y := *(*byte)(unsafe.Add(unsafe.Pointer(a), i)), *(*byte)(unsafe.Add(unsafe.Pointer(b), i))
		if x != y {
			return int32(x) - int32(y)
		}
		if x == 0 {
			return 0
		}
	}
}

// Memcpy1 is memcpy and memmove.
func Memcpy1(to, from *Void, n uint64) *Void {
	memmove(unsafe.Pointer(to), unsafe.Pointer(from), uintptr(n))
	return to
}

// Memset1 is memset.
func Memset1(to *Void, c int32, n uint64) *Void {
	memset(unsafe.Pointer(to), byte(c), uintptr(n))
	return to
}

// SizeOf1 is the number of bytes from p to the end of the object it
// points into (malloc_usable_size): what was asked for, if malloc gave
// it with no type, and otherwise what the collector knows, the object's
// size class, less the byte that stays after every object (see AllocOf).
func SizeOf1(p *Void) uint64 {
	if n, ok := usable(unsafe.Pointer(p)); ok {
		return uint64(n)
	}
	return uint64(max(sizeOf(unsafe.Pointer(p)), 1) - 1)
}

// labels are what the addresses of labels (GNU's &&label) point to: one
// byte for each label of a function, by its number.
var labels [1 << 16]byte

// LabelAddr is the address of the label numbered id.
func LabelAddr(id int) *Void { return (*Void)(unsafe.Pointer(&labels[id])) }

// LabelOf is the number of the label whose address is p: -1 if p is not
// one's.
func LabelOf(p *Void) int {
	d := Addr(p) - uintptr(unsafe.Pointer(&labels[0]))
	if d >= uintptr(len(labels)) {
		return -1
	}
	return int(d)
}

//go:linkname allowIntegers runtime.c23AllowIntegers
func allowIntegers()

func init() { allowIntegers() }
