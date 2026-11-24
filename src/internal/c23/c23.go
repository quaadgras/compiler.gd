// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package c23 is the run-time support of C compiled by cmd/cc
// (doc/gd/cc.md): C's library, written in Go, and the few things the
// translated C needs that Go has no expression for.
//
// A C pointer is a Go pointer: *T where C's type says what it points to,
// unsafe.Pointer for void * and in this package, whose functions are
// C's library's and take any pointer. Nothing here checks one: what is
// checked is what the compiler checks of unsafe.Pointer where translated
// C converts a pointer or does arithmetic on it (doc/gd/unsafe.md). This
// package is compiled without those checks, as the runtime is.
//
// Ptr is the name C's pointers had when they carried capabilities: it is
// unsafe.Pointer, and is going.
package c23

import (
	"fmt"
	"math"
	"reflect"
	"sync"
	"unsafe"
)

// object returns the base of the heap object that p points into and its
// size; nil, 0 if p does not point into the heap.
//
//go:noescape
//go:linkname object runtime.c23Object
func object(p unsafe.Pointer) (base unsafe.Pointer, size uintptr)

// Ptr is a C pointer.
type Ptr = unsafe.Pointer

// A Fault is what C's undefined behaviour that is caught panics with.
type Fault struct {
	Msg  string
	Addr uintptr
}

func (f *Fault) Error() string { return fmt.Sprintf("c23: %s (address %#x)", f.Msg, f.Addr) }

// RaiseFault panics with a Fault.
func RaiseFault(msg string, addr uintptr) { panic(&Fault{msg, addr}) }

// Addr returns p's address.
func Addr(p Ptr) uintptr { return uintptr(p) }

// FromInt returns the pointer whose address is u.
func FromInt(u uintptr) Ptr { return unsafe.Pointer(u) }

// Add returns p moved by n bytes.
func Add(p Ptr, n int) Ptr { return unsafe.Add(p, n) }

// Sub returns the difference of two pointers, in bytes.
func Sub(p, q Ptr) int { return int(uintptr(p) - uintptr(q)) }

// Cap describes p, for messages.
func Cap(p Ptr) string { return fmt.Sprintf("%#x", uintptr(p)) }

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
func Local[T any](o *Obj[T]) Ptr { return unsafe.Pointer(&o.V) }

// LocalT is Local.
func LocalT[T any](o *Obj[T], t *Type) Ptr { return unsafe.Pointer(&o.V) }

// New allocates an object of type T.
func New[T any]() Ptr { return unsafe.Pointer(new(T)) }

// NewT is New.
func NewT[T any](t *Type) Ptr { return unsafe.Pointer(new(T)) }

// NewArray allocates n bytes as an array of T's.
func NewArray[T any](n uintptr) Ptr { return unsafe.Pointer(AllocOf[T](uint64(n))) }

// NewArrayT allocates n bytes as an array of t's.
func NewArrayT(t *Type, n uintptr) Ptr { return newOf(t, n) }

// newOf allocates n bytes as an array of ti's type.
func newOf(ti *Type, n uintptr) Ptr {
	if ti.size == 0 {
		return unsafe.Pointer(new(byte))
	}
	count := int(max((n+ti.size-1)/ti.size, 1))
	return reflect.MakeSlice(reflect.SliceOf(ti.t), count, count).UnsafePointer()
}

// Raw is the element of C's memory that has no type.
type Raw struct {
	b byte
}

var rawInfo = TinfoOf[Raw]()

// Malloc is malloc: memory in which any word may come to hold a pointer.
func Malloc(n uint64) Ptr { return AllocRaw(n) }

// Free is free: the collector frees.
func Free(p Ptr) {}

// FromGo returns the C pointer for a Go pointer: itself.
func FromGo[T any](p *T) Ptr { return unsafe.Pointer(p) }

// FromSlice returns the C pointer to s's first element.
func FromSlice[T any](s []T) Ptr { return unsafe.Pointer(unsafe.SliceData(s)) }

// FromGoUnsafe returns the C pointer for an unsafe.Pointer: itself.
func FromGoUnsafe(p unsafe.Pointer) Ptr { return p }

// ToGo returns the Go pointer for a C pointer: itself.
func ToGo[T any](p Ptr) *T { return (*T)(p) }

// ToGoUnsafe returns the Go pointer for a C pointer: itself.
func ToGoUnsafe(p Ptr) unsafe.Pointer { return p }

// Check returns p's address, for an access of n bytes.
func Check(p Ptr, n uintptr) unsafe.Pointer { return p }

// LoadPtr loads the pointer at p.
func LoadPtr(p Ptr) Ptr { return *(*unsafe.Pointer)(p) }

// StorePtr stores the pointer v at p.
func StorePtr(p, v Ptr) { *(*unsafe.Pointer)(p) = v }

func Load8(p Ptr) uint8   { return *(*uint8)(p) }
func Load16(p Ptr) uint16 { return *(*uint16)(p) }
func Load32(p Ptr) uint32 { return *(*uint32)(p) }
func Load64(p Ptr) uint64 { return *(*uint64)(p) }

func LoadF32(p Ptr) float32 { return math.Float32frombits(Load32(p)) }
func LoadF64(p Ptr) float64 { return math.Float64frombits(Load64(p)) }

func Store8(p Ptr, v uint8)   { *(*uint8)(p) = v }
func Store16(p Ptr, v uint16) { *(*uint16)(p) = v }
func Store32(p Ptr, v uint32) { *(*uint32)(p) = v }
func Store64(p Ptr, v uint64) { *(*uint64)(p) = v }

func StoreF32(p Ptr, v float32) { Store32(p, math.Float32bits(v)) }
func StoreF64(p Ptr, v float64) { Store64(p, math.Float64bits(v)) }

// Memmove copies n bytes from src to dst, which may overlap.
func Memmove(dst, src Ptr, n uintptr) { Memcpy1(dst, src, uint64(n)) }

// MoveWord copies one word, which may be a pointer, from src to dst.
func MoveWord(dst, src Ptr) { *(*unsafe.Pointer)(dst) = *(*unsafe.Pointer)(src) }

// Memset sets size bytes at p to c.
func Memset(p Ptr, c int32, size uint64) Ptr { return Memset1(p, c, size) }

// typeName is a type's name for messages: C's types by cgo's names.
func typeName(ti *Type) string { return ti.t.String() }
