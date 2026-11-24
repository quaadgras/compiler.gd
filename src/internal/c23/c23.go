// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package c23 is the run-time support of C compiled by cmd/cc
// (doc/gd/cc.md): C's library, written in Go, and the few things the
// translated C needs that Go has no expression for.
//
// A C pointer is a Go pointer: *T where C's type says what it points to,
// *Void for void * and in this package, whose functions are C's
// library's and take any pointer. Nothing here checks one: what is
// checked is what the compiler checks of unsafe.Pointer where translated
// C converts a pointer or does arithmetic on it (doc/gd/unsafe.md). This
// package is compiled without those checks, as the runtime is.
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

// A Fault is what C's undefined behaviour that is caught panics with.
type Fault struct {
	Msg  string
	Addr uintptr
}

func (f *Fault) Error() string { return fmt.Sprintf("c23: %s (address %#x)", f.Msg, f.Addr) }

// RaiseFault panics with a Fault.
func RaiseFault(msg string, addr uintptr) { panic(&Fault{msg, addr}) }

// Addr returns p's address.
func Addr(p *Void) uintptr { return uintptr(unsafe.Pointer(p)) }

// FromInt returns the pointer whose address is u.
func FromInt(u uintptr) *Void { return (*Void)(unsafe.Pointer(u)) }

// Add returns p moved by n bytes.
func Add(p *Void, n int) *Void { return (*Void)(unsafe.Add(unsafe.Pointer(p), n)) }

// Sub returns the difference of two pointers, in bytes.
func Sub(p, q *Void) int { return int(Addr(p) - Addr(q)) }

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
func New[T any]() *Void { return (*Void)(unsafe.Pointer(new(T))) }

// NewT is New.
func NewT[T any](t *Type) *Void { return (*Void)(unsafe.Pointer(new(T))) }

// NewArray allocates n bytes as an array of T's.
func NewArray[T any](n uintptr) *Void { return (*Void)(unsafe.Pointer(AllocOf[T](uint64(n)))) }

// NewArrayT allocates n bytes as an array of t's.
func NewArrayT(t *Type, n uintptr) *Void { return newOf(t, n) }

// newOf allocates n bytes as an array of ti's type.
func newOf(ti *Type, n uintptr) *Void {
	if ti.size == 0 {
		return (*Void)(unsafe.Pointer(new(byte)))
	}
	count := int(max((n+ti.size-1)/ti.size, 1))
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

// Check returns p's address, for an access of n bytes.
func Check(p *Void, n uintptr) unsafe.Pointer { return unsafe.Pointer(p) }

// LoadPtr loads the pointer at p.
func LoadPtr(p *Void) *Void { return *(**Void)(unsafe.Pointer(p)) }

// StorePtr stores the pointer v at p.
func StorePtr(p, v *Void) { *(**Void)(unsafe.Pointer(p)) = v }

func Load8(p *Void) uint8   { return *(*uint8)(unsafe.Pointer(p)) }
func Load16(p *Void) uint16 { return *(*uint16)(unsafe.Pointer(p)) }
func Load32(p *Void) uint32 { return *(*uint32)(unsafe.Pointer(p)) }
func Load64(p *Void) uint64 { return *(*uint64)(unsafe.Pointer(p)) }

func LoadF32(p *Void) float32 { return math.Float32frombits(Load32(p)) }
func LoadF64(p *Void) float64 { return math.Float64frombits(Load64(p)) }

func Store8(p *Void, v uint8)   { *(*uint8)(unsafe.Pointer(p)) = v }
func Store16(p *Void, v uint16) { *(*uint16)(unsafe.Pointer(p)) = v }
func Store32(p *Void, v uint32) { *(*uint32)(unsafe.Pointer(p)) = v }
func Store64(p *Void, v uint64) { *(*uint64)(unsafe.Pointer(p)) = v }

func StoreF32(p *Void, v float32) { Store32(p, math.Float32bits(v)) }
func StoreF64(p *Void, v float64) { Store64(p, math.Float64bits(v)) }

// Memmove copies n bytes from src to dst, which may overlap.
func Memmove(dst, src *Void, n uintptr) { Memcpy1(dst, src, uint64(n)) }

// MoveWord copies one word, which may be a pointer, from src to dst.
func MoveWord(dst, src *Void) { *(**Void)(unsafe.Pointer(dst)) = *(**Void)(unsafe.Pointer(src)) }

// Memset sets size bytes at p to c.
func Memset(p *Void, c int32, size uint64) *Void { return Memset1(p, c, size) }

// typeName is a type's name for messages: C's types by cgo's names.
func typeName(ti *Type) string { return ti.t.String() }
