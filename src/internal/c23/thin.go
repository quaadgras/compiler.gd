// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"fmt"
	"unsafe"
)

// What cmd/cc's 1:1 translation (cc -thin, doc/gd/cc.md) uses: there a C
// pointer is Go's, *T or unsafe.Pointer, a C object is a Go variable of
// the type that mirrors it, and the checks are the ones the compiler
// makes of unsafe.Pointer (doc/gd/unsafe.md), not this package's. The
// library is still this package's, which takes a Ptr: Thin makes one of
// a Go pointer where the library is called.

const wordBytes = uint64(WordSize)

// Lit is the array A whose bytes are s: a string literal's object.
func Lit[A any](s string) (a A) {
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&a)), unsafe.Sizeof(a)), s)
	return a
}

// AllocOf is malloc(n) converted to *T: an array of T's of at least n
// bytes, which the collector knows the pointers of.
func AllocOf[T any](n uint64) *T {
	var z T
	size := uint64(unsafe.Sizeof(z))
	if size == 0 {
		return new(T)
	}
	return unsafe.SliceData(make([]T, max((n+size-1)/size, 1)))
}

// AllocRaw is malloc(n) that is given no type: memory in which any word
// may come to hold a pointer, which the collector therefore looks at
// word by word.
func AllocRaw(n uint64) unsafe.Pointer {
	return unsafe.Pointer(unsafe.SliceData(make([]unsafe.Pointer, max((n+wordBytes-1)/wordBytes, 1))))
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
func ReallocRaw(p unsafe.Pointer, n uint64) unsafe.Pointer {
	q := AllocRaw(n)
	if p != nil {
		old := sizeOf(p) / WordSize
		copy(unsafe.Slice((*unsafe.Pointer)(q), max((uintptr(n)+WordSize-1)/WordSize, 1)), unsafe.Slice((*unsafe.Pointer)(p), old))
	}
	return q
}

// Free1 is free: the collector frees.
func Free1(p unsafe.Pointer) {}

var byteInfo = TinfoOf[byte]()

// Thin is the Ptr for a Go pointer that the library is given: the bytes
// from it to the end of its heap object, or as far as the library goes
// if it points to a variable of the stack or of the program's data.
func Thin(p unsafe.Pointer) Ptr {
	if p == nil {
		return Ptr{}
	}
	if c, ok := cobject(p); ok {
		return c
	}
	if base, size := object(p); base != nil {
		return Ptr{Obj: base, Size: size, Off: int(uintptr(p) - uintptr(base)), ti: tref(byteInfo)}
	}
	return Ptr{Obj: p, Size: ^uintptr(0) >> 2, ti: tref(byteInfo)}
}

// Unthin is the Go pointer for a Ptr the library returns.
func Unthin(p Ptr) unsafe.Pointer {
	if p.Obj == nil {
		return unsafe.Pointer(uintptr(p.Off))
	}
	return unsafe.Add(p.Obj, p.Off)
}

// The library's functions that 1:1 code calls most, on Go's pointers.

// Strlen1 is strlen.
func Strlen1(s unsafe.Pointer) uint64 {
	n := uint64(0)
	for *(*byte)(unsafe.Add(s, n)) != 0 {
		n++
	}
	return n
}

// Strcmp1 is strcmp.
func Strcmp1(a, b unsafe.Pointer) int32 {
	for i := 0; ; i++ {
		x, y := *(*byte)(unsafe.Add(a, i)), *(*byte)(unsafe.Add(b, i))
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
func Memcpy1(dst, src unsafe.Pointer, n uint64) unsafe.Pointer {
	if words(n, dst, src) {
		copy(unsafe.Slice((*unsafe.Pointer)(dst), n/wordBytes), unsafe.Slice((*unsafe.Pointer)(src), n/wordBytes))
		return dst
	}
	copy(unsafe.Slice((*byte)(dst), n), unsafe.Slice((*byte)(src), n))
	return dst
}

// Memset1 is memset.
func Memset1(p unsafe.Pointer, c int32, n uint64) unsafe.Pointer {
	if c == 0 && words(n, p) {
		clear(unsafe.Slice((*unsafe.Pointer)(p), n/wordBytes))
		return p
	}
	b := unsafe.Slice((*byte)(p), n)
	if c == 0 {
		clear(b)
	} else {
		for i := range b {
			b[i] = byte(c)
		}
	}
	return p
}

// NewFunc1 is the pointer to the function f: its descriptor.
func NewFunc1(f any) *Func { return &Func{f: f} }

// FuncOf1 is the Go function of type F that p points to, which a call
// through a C function pointer calls.
func FuncOf1[F any](p *Func) F {
	if p == nil {
		RaiseFault("call through a null pointer to a function", 0)
	}
	f, ok := p.f.(F)
	if !ok {
		if g, ok := adapt[F](p.f); ok {
			return g
		}
		var z F
		RaiseFault(fmt.Sprintf("call of a %T through a pointer to a function of type %T", p.f, z), uintptr(unsafe.Pointer(p)))
	}
	return f
}

// SizeOf1 is the number of bytes from p to the end of the object it
// points into, as the collector knows it: the object's size class, which
// may be more than what was asked for.
func SizeOf1(p unsafe.Pointer) uint64 { return uint64(sizeOf(p)) }
