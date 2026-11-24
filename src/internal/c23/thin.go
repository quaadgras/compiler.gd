// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import "unsafe"

// What cmd/cc's 1:1 translation (cc -thin, doc/gd/cc.md) uses: there a C
// pointer is Go's, *T or unsafe.Pointer, a C object is a Go variable of
// the type that mirrors it, and the checks are the ones the compiler
// makes of unsafe.Pointer (doc/gd/unsafe.md), not this package's. The
// library is still this package's, which takes a *Void: Thin makes one of
// a Go pointer where the library is called.

const wordBytes = uint64(WordSize)

// Lit is the array A whose bytes are s: a string literal's object.
func Lit[A any](s string) (a A) {
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&a)), unsafe.Sizeof(a)), s)
	return a
}

// Every object of C's on the heap has at least a byte after it, so that
// the pointer one past its end, which C may make and keep, points into
// it and to no other object: the collector would keep the other alive
// in its place, or stop on a pointer to one that is free. The compiler
// does the same for the variables of translated C (cmd/compile's
// ir.CObjType).

// AllocOf is malloc(n) converted to *T: an array of T's of more than n
// bytes, which the collector knows the pointers of.
func AllocOf[T any](n uint64) *T {
	var z T
	size := uint64(unsafe.Sizeof(z))
	if size == 0 {
		return new(T)
	}
	return unsafe.SliceData(make([]T, n/size+1))
}

// AllocRaw is malloc(n) that is given no type: memory in which any word
// may come to hold a pointer, which the collector therefore looks at
// word by word.
func AllocRaw(n uint64) *Void {
	return (*Void)(unsafe.Pointer(unsafe.SliceData(make([]unsafe.Pointer, n/wordBytes+1))))
}

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
	if p != nil {
		var z T
		if size := unsafe.Sizeof(z); size != 0 {
			old := sizeOf(unsafe.Pointer(p)) / size
			copy(unsafe.Slice(q, max((uintptr(n)+size-1)/size, 1)), unsafe.Slice(p, old))
		}
	}
	return q
}

// ReallocRaw is realloc(p, n) that is given no type.
func ReallocRaw(p *Void, n uint64) *Void {
	q := AllocRaw(n)
	if p != nil {
		old := sizeOf(unsafe.Pointer(p)) / WordSize
		copy(unsafe.Slice((*unsafe.Pointer)(unsafe.Pointer(q)), max((uintptr(n)+WordSize-1)/WordSize, 1)), unsafe.Slice((*unsafe.Pointer)(unsafe.Pointer(p)), old))
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

// words reports whether the n bytes at each of ps are whole words, which
// may hold pointers: the collector must see them copied and cleared.
func words(n uint64, ps ...unsafe.Pointer) bool {
	if n%wordBytes != 0 || n == 0 {
		return false
	}
	for _, p := range ps {
		if uintptr(p)%WordSize != 0 {
			return false
		}
	}
	return true
}

// Memcpy1 is memcpy and memmove.
func Memcpy1(to, from *Void, n uint64) *Void {
	dst, src := unsafe.Pointer(to), unsafe.Pointer(from)
	if words(n, dst, src) {
		copy(unsafe.Slice((*unsafe.Pointer)(dst), n/wordBytes), unsafe.Slice((*unsafe.Pointer)(src), n/wordBytes))
		return to
	}
	copy(unsafe.Slice((*byte)(dst), n), unsafe.Slice((*byte)(src), n))
	return to
}

// Memset1 is memset.
func Memset1(to *Void, c int32, n uint64) *Void {
	p := unsafe.Pointer(to)
	if c == 0 && words(n, p) {
		clear(unsafe.Slice((*unsafe.Pointer)(p), n/wordBytes))
		return to
	}
	b := unsafe.Slice((*byte)(p), n)
	if c == 0 {
		clear(b)
	} else {
		for i := range b {
			b[i] = byte(c)
		}
	}
	return to
}

// SizeOf1 is the number of bytes from p to the end of the object it
// points into, as the collector knows it: the object's size class, which
// is more than what was asked for (malloc_usable_size), less the byte that
// stays after every object (see AllocOf).
func SizeOf1(p *Void) uint64 { return uint64(max(sizeOf(unsafe.Pointer(p)), 1) - 1) }

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
