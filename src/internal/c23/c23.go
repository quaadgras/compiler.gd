// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package c23 is the run-time support of C compiled by cmd/cc
// (doc/gd/cc.md): C's library, written in Go, and the few things the
// translated C needs that Go has no expression for.
//
// A C pointer is a Go pointer: *T where C's type says what it points to,
// *Void for void * and in this package, whose functions are C's
// library's and take any pointer. A pointer that the library is given is
// whatever the program made of it, so the library is checked as the C
// that calls it is (doc/gd/cc.md, "What the library checks"):
//
//   - The compiler checks this package's pointer arithmetic and its
//     conversions as it checks those of translated C: Add stays in the
//     object, and a load or a store is of bytes that are in it (in).
//   - A function that is given a pointer and a length, and hands them to
//     code that no check is in (memmove, copy, a read of a file), asks
//     the runtime how far the object goes: Check, BytesAt, Writable.
//   - What the library keeps for itself and gives C a pointer to (a
//     FILE, a va_list, a function) is an object that C's pointers
//     cannot look into, and that the library knows for its own: Handle.
//
// The few functions that make a pointer of an integer, or keep bits in
// one, are marked go:nocheckptr.
package c23

import (
	"bytes"
	"fmt"
	"internal/abi"
	"math"
	"reflect"
	"sync"
	"sync/atomic"
	"unsafe"
)

// object returns the base of the heap object that p points into and its
// size; nil, 0 if p does not point into the heap.
//
//go:noescape
//go:linkname object runtime.c23Object
func object(p unsafe.Pointer) (base unsafe.Pointer, size uintptr)

// A Fault is what C's undefined behaviour that is caught panics with.
type Fault struct {
	Msg  string
	Addr uintptr
}

func (f *Fault) Error() string { return fmt.Sprintf("c23: %s (address %#x)", f.Msg, f.Addr) }

// RaiseFault panics with a Fault.
func RaiseFault(msg string, addr uintptr) { panic(&Fault{msg, addr}) }

// Addr returns p's address. The integer is not one that a pointer is
// made of again but by FromInt and IntPtr, so p's object is not exposed
// for it (runtime.exposeptr).
//
//go:nocheckptr
func Addr(p *Void) uintptr { return uintptr(unsafe.Pointer(p)) }

// Add returns p moved by n bytes: to somewhere in what p points into,
// or to its end, which the compiler checks.
func Add(p *Void, n int) *Void { return (*Void)(unsafe.Add(unsafe.Pointer(p), n)) }

// Sub returns the difference of two pointers, in bytes.
func Sub(p, q *Void) int { return int(Addr(p) - Addr(q)) }

// extent returns the number of bytes before p and from p on in what p
// points into, if that is something that C may look at as bytes.
//
//go:linkname extent runtime.c23Extent
//go:noescape
func extent(p unsafe.Pointer) (before, after uintptr, ok bool)

// Check returns p's address for an access of the n bytes at p, which
// are in what p points into: an object of the heap as far as it was
// asked for, the stack, the program's data (runtime.c23Extent). It is
// the check of a function that is given a pointer and a length and
// loads or stores through neither by itself.
func Check(p *Void, n uintptr) unsafe.Pointer {
	if n != 0 {
		if _, after, ok := extent(unsafe.Pointer(p)); !ok || n > after {
			outside(p, n, after, ok)
		}
	}
	return unsafe.Pointer(p)
}

// Array returns p for a function that goes through the n elements of
// size bytes that p points to: all of them are in what p points into,
// which is checked once, here. The steps that the function then makes
// in the array need no check of their own (Within, Move).
func Array(p *Void, n, size uint64) *Void {
	if size != 0 && n > uint64(^uintptr(0)>>1)/size {
		outside(p, ^uintptr(0), 0, true)
	}
	return (*Void)(Check(p, uintptr(n*size)))
}

// Within returns p moved by n bytes, with no check: a step in an array
// that Array checked whole.
//
//go:nocheckptr
func Within(p *Void, n int) *Void { return (*Void)(unsafe.Add(unsafe.Pointer(p), n)) }

// Has reports whether the n bytes at p are in what p points into, as
// Check would have it.
func Has(p *Void, n uint64) bool {
	_, after, ok := extent(unsafe.Pointer(p))
	return ok && n <= uint64(after)
}

//go:noinline
func outside(p *Void, n, after uintptr, ok bool) {
	if !ok {
		RaiseFault(fmt.Sprintf("access of %d bytes of what is no object of C's", n), Addr(p))
	}
	RaiseFault(fmt.Sprintf("access of %d bytes where the object has %d", n, after), Addr(p))
}

// slice returns the n bytes at p, which the caller checked.
//
//go:nocheckptr
func slice(p unsafe.Pointer, n uintptr) []byte { return unsafe.Slice((*byte)(p), n) }

// BytesAt returns the n bytes at p, to read: Check's.
func BytesAt(p *Void, n uintptr) []byte {
	if n == 0 {
		return nil
	}
	return slice(Check(p, n), n)
}

// BytesFrom returns the bytes from p to the end of what it points into,
// to read.
func BytesFrom(p *Void) []byte {
	_, after, ok := extent(unsafe.Pointer(p))
	if !ok {
		outside(p, 1, 0, false)
	}
	return slice(unsafe.Pointer(p), after)
}

// Writable returns the n bytes at p, to write: Check's. Every store in
// C's memory that the compiler does not see as a store of C's is
// through its result: what copy writes, what the system reads from a
// file or a socket, what a Go package fills in. So this is the one
// place where the runtime is told of bytes that are written over words
// which may be claimed for pointers (bytesStored).
func Writable(p *Void, n uintptr) []byte {
	if n == 0 {
		return nil
	}
	a := Check(p, n)
	bytesStored(a, n)
	return slice(a, n)
}

// bytesStored tells the runtime that bytes are about to be written to
// the n bytes at p, which Writable checked, by code that the compiler
// does not see as storing in C's memory.
//
//go:linkname bytesStored runtime.c23BytesStored
//go:noescape
func bytesStored(p unsafe.Pointer, n uintptr)

// StringAt returns the bytes of the string at p, to read, without the 0
// that ends it, which is in what p points into.
func StringAt(p *Void) []byte {
	b := BytesFrom(p)
	i := bytes.IndexByte(b, 0)
	if i < 0 {
		RaiseFault(fmt.Sprintf("string of %d bytes with no end in its object", len(b)), Addr(p))
	}
	return b[:i]
}

// in returns p's address for a load or a store of the n bytes at p. The
// compiler checks that p moved by n bytes is in what p points into, or
// at its end, as it checks any arithmetic of C's (doc/gd/cc.md): so the
// n bytes are in it. The conversion that follows would by itself let p
// be the end of its object, which a pointer of C's may be.
func in(p *Void, n int) unsafe.Pointer {
	return unsafe.Add(unsafe.Add(unsafe.Pointer(p), n), -n)
}

// Cap describes p, for messages.
func Cap(p *Void) string { return fmt.Sprintf("%#x", Addr(p)) }

// A Type is a Go type that memory is allocated as.
type Type struct {
	t    reflect.Type
	size uintptr
}

type tinfo = Type

var tinfos sync.Map // reflect.Type -> *Type

func infoOf(t reflect.Type) *Type {
	if ti, ok := tinfos.Load(t); ok {
		return ti.(*Type)
	}
	ti, _ := tinfos.LoadOrStore(t, &Type{t: t, size: t.Size()})
	return ti.(*Type)
}

// TinfoOf returns T's Type.
func TinfoOf[T any]() *Type { return infoOf(reflect.TypeFor[T]()) }

// TypeOf returns T's Type.
func TypeOf[T any]() *Type { return TinfoOf[T]() }

// An Obj is a C object of type T.
type Obj[T any] struct {
	V T
}

// Local returns the pointer to the object o.
func Local[T any](o *Obj[T]) *Void { return (*Void)(unsafe.Pointer(&o.V)) }

// LocalT is Local.
func LocalT[T any](o *Obj[T], t *Type) *Void { return (*Void)(unsafe.Pointer(&o.V)) }

// New allocates an object of type T.
func New[T any]() *Void { return (*Void)(unsafe.Pointer(&new(padded[T]).v)) }

// A Handle is what a pointer of C's to something that the library keeps
// for itself points to: a FILE, a DIR, a jmp_buf's frame. It is an
// interface and nothing else, so C cannot look into it or change it:
// the compiler's checks refuse a pointer of C's converted over part of
// an interface, and the runtime refuses the library's functions an
// object of Go's that holds one (runtime.c23Extent). And it is always
// an object of the heap that was allocated as a Handle, which the
// runtime knows from the object and C cannot make.
type Handle struct {
	v any
}

//go:linkname newObject runtime.c23NewObject
func newObject(t *abi.Type) unsafe.Pointer

//go:linkname isObject runtime.c23IsObject
//go:noescape
func isObject(p unsafe.Pointer, t *abi.Type) bool

var handleType = abi.TypeFor[Handle]()

// NewHandle returns the pointer for C to v.
//
//go:nocheckptr
func NewHandle(v any) *Void {
	h := (*Handle)(newObject(handleType))
	h.v = v
	return seal(unsafe.Pointer(h))
}

// A seen is a set of addresses that were found to be of objects of one
// type that the library made (isObject), each where its address puts
// it: an address that was asked about before is a load and a
// comparison. What is in the set stays allocated, so an address in it
// is of nothing else.
type seen [1 << 8]unsafe.Pointer

// is reports whether p is the address of an object of the heap that
// was allocated as a t.
//
//go:nocheckptr
func (s *seen) is(p unsafe.Pointer, t *abi.Type) bool {
	slot := &s[uintptr(p)>>4%uintptr(len(s))]
	if p != nil && atomic.LoadPointer(slot) == p {
		return true
	}
	if !isObject(p, t) {
		return false
	}
	atomic.StorePointer(slot, p)
	return true
}

var handles seen

// handleAt returns the Handle at p, or nil if p is not the address of
// one that NewHandle made.
//
//go:nocheckptr
func handleAt(p *Void) *Handle {
	return (*Handle)(unseal(&handles, p, handleType))
}

// HandleOf returns the T that p is the pointer for C to, and whether it
// is one: p is what NewHandle returned for a T.
func HandleOf[T any](p *Void) (T, bool) {
	h := handleAt(p)
	if h == nil {
		var z T
		return z, false
	}
	v, ok := h.v.(T)
	return v, ok
}

// padded is T as it is allocated: with a byte after it (see AllocOf).
type padded[T any] struct {
	v T
	_ byte
}

// NewT is New.
func NewT[T any](t *Type) *Void { return New[T]() }

// NewArray allocates n bytes as an array of T's.
func NewArray[T any](n uintptr) *Void { return (*Void)(unsafe.Pointer(AllocOf[T](uint64(n)))) }

// NewArrayT allocates n bytes as an array of t's.
func NewArrayT(t *Type, n uintptr) *Void { return newOf(t, n) }

// newOf allocates n bytes as an array of ti's type.
func newOf(ti *Type, n uintptr) *Void {
	if ti.size == 0 {
		return (*Void)(unsafe.Pointer(new(byte)))
	}
	count := int(n/ti.size + 1) // a byte after it at least (see AllocOf)
	return (*Void)(reflect.MakeSlice(reflect.SliceOf(ti.t), count, count).UnsafePointer())
}

// Raw is the element of C's memory that has no type.
type Raw struct {
	b byte
}

var rawInfo = TinfoOf[Raw]()

// Malloc is malloc: memory in which any word may come to hold a pointer.
func Malloc(n uint64) *Void { return AllocRaw(n) }

// Free is free: the collector frees.
func Free(p *Void) {}

// FromGo returns the C pointer for a Go pointer: itself.
func FromGo[T any](p *T) *Void { return (*Void)(unsafe.Pointer(p)) }

// FromSlice returns the C pointer to s's first element.
func FromSlice[T any](s []T) *Void { return (*Void)(unsafe.Pointer(unsafe.SliceData(s))) }

// FromGoUnsafe returns the C pointer for an unsafe.Pointer: itself.
func FromGoUnsafe(p unsafe.Pointer) *Void { return (*Void)(p) }

// ToGo returns the Go pointer for a C pointer: itself.
func ToGo[T any](p *Void) *T { return (*T)(unsafe.Pointer(p)) }

// ToGoUnsafe returns the Go pointer for a C pointer: itself.
func ToGoUnsafe(p *Void) unsafe.Pointer { return unsafe.Pointer(p) }

// LoadPtr loads the pointer at p: a word that is claimed for a pointer,
// or is from here on (runtime/cclaim.go).
func LoadPtr(p *Void) *Void { return *(**Void)(in(p, int(WordSize))) }

// StorePtr stores the pointer v at p.
func StorePtr(p, v *Void) { *(**Void)(in(p, int(WordSize))) = v }

func Load8(p *Void) uint8   { return *(*uint8)(in(p, 1)) }
func Load16(p *Void) uint16 { return *(*uint16)(in(p, 2)) }
func Load32(p *Void) uint32 { return *(*uint32)(in(p, 4)) }
func Load64(p *Void) uint64 { return *(*uint64)(in(p, 8)) }

func LoadF32(p *Void) float32 { return math.Float32frombits(Load32(p)) }
func LoadF64(p *Void) float64 { return math.Float64frombits(Load64(p)) }

func Store8(p *Void, v uint8)   { *(*uint8)(in(p, 1)) = v }
func Store16(p *Void, v uint16) { *(*uint16)(in(p, 2)) = v }
func Store32(p *Void, v uint32) { *(*uint32)(in(p, 4)) = v }
func Store64(p *Void, v uint64) { *(*uint64)(in(p, 8)) = v }

func StoreF32(p *Void, v float32) { Store32(p, math.Float32bits(v)) }
func StoreF64(p *Void, v float64) { Store64(p, math.Float64bits(v)) }

// Memmove copies n bytes from src to dst, which may overlap.
func Memmove(dst, src *Void, n uintptr) { Memcpy1(dst, src, uint64(n)) }

// Peek and Poke are the loads and stores of translated C where Go has
// no variable for what C loads or stores (doc/gd/cc.md): the unit of a
// bit-field, a member of a struct that is laid out as bytes. The
// address is that of an lvalue of C's, made of a pointer that the
// compiler checked where C made it. So they check nothing, as a load or
// a store through a typed pointer of C's checks nothing; the library,
// which is given pointers that it knows nothing of, uses Load and Store.

//go:nocheckptr
func Peek8(p *Void) uint8 { return *(*uint8)(unsafe.Pointer(p)) }

//go:nocheckptr
func Peek16(p *Void) uint16 { return *(*uint16)(unsafe.Pointer(p)) }

//go:nocheckptr
func Peek32(p *Void) uint32 { return *(*uint32)(unsafe.Pointer(p)) }

//go:nocheckptr
func Peek64(p *Void) uint64 { return *(*uint64)(unsafe.Pointer(p)) }

func PeekPtr(p *Void) *Void { return *(**Void)(unsafe.Pointer(p)) }

func PeekF32(p *Void) float32 { return math.Float32frombits(Peek32(p)) }
func PeekF64(p *Void) float64 { return math.Float64frombits(Peek64(p)) }

func Poke8(p *Void, v uint8) { *(*uint8)(unsafe.Pointer(p)) = v }

func Poke16(p *Void, v uint16) { *(*uint16)(unsafe.Pointer(p)) = v }

func Poke32(p *Void, v uint32) { *(*uint32)(unsafe.Pointer(p)) = v }

func Poke64(p *Void, v uint64) { *(*uint64)(unsafe.Pointer(p)) = v }

func PokePtr(p, v *Void) { *(**Void)(unsafe.Pointer(p)) = v }

// PeekCap and PokeCap load and store the intptr_t or uintptr_t at p: a
// word that says whether it is a pointer (intcap.go), which the
// conversion claims for that.
func PeekCap(p *Void) *Void { return (*Void)((*Intcap)(unsafe.Pointer(p)).w) }
func PokeCap(p, v *Void)    { (*Intcap)(unsafe.Pointer(p)).w = unsafe.Pointer(v) }

func PokeF32(p *Void, v float32) { Poke32(p, math.Float32bits(v)) }
func PokeF64(p *Void, v float64) { Poke64(p, math.Float64bits(v)) }

// Move copies the n bytes of a struct or an array that translated C
// assigns, from src to dst: the size is the type's and the addresses
// are of lvalues of C's, so it checks nothing, as Peek and Poke.
func Move(dst, src *Void, n uintptr) { memmove(unsafe.Pointer(dst), unsafe.Pointer(src), n) }

// Memset sets size bytes at p to c.
func Memset(p *Void, c int32, size uint64) *Void { return Memset1(p, c, size) }

// typeName is a type's name for messages: C's types by cgo's names.
func typeName(ti *Type) string { return ti.t.String() }
