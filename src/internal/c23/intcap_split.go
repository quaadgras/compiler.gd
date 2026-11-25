// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.split

package c23

import (
	"internal/abi"
	"unsafe"
)

// intptr_t and uintptr_t with GOWASM=gc,split (doc/gd/cc.md, "On
// wasm").
//
// A pointer is an object of the engine's there, with the size of what
// it points into, and an integer is never one: so a value of these
// types is the pointer that it was made of, as it is, or an integer,
// which is a pointer with no size and the integer for its address
// (runtime.c23IntPtr). No bit says which. Arithmetic on a value that is
// a pointer moves the pointer, wherever to: what is loaded through it
// is asked about then. Anything else gives an integer.

// Intcap is an intptr_t or uintptr_t in memory.
type Intcap struct {
	w unsafe.Pointer
}

//go:linkname setIntcap runtime.c23SetIntcap
func setIntcap(t *abi.Type)

func init() { setIntcap(abi.TypeFor[Intcap]()) }

// isPointer reports whether x is a pointer to something, and not an
// integer.
//
//go:linkname isPointer runtime.c23IsPointer
func isPointer(x unsafe.Pointer) bool

//go:linkname intPtr runtime.c23IntPtr
func intPtr(v uintptr) unsafe.Pointer

// FromInt returns the integer u as a pointer: one that nothing is
// loaded or stored through.
func FromInt(u uintptr) *Void { return (*Void)(intPtr(u)) }

// IntPtr converts an integer to a pointer.
func IntPtr(v uintptr) *Void { return (*Void)(intPtr(v)) }

func capInt(x *Void) uintptr { return Addr(x) }
func capSint(x *Void) int    { return int(Addr(x)) }

// CapUint returns the value of the uintptr_t x as an integer, and
// CapSintOf that of the intptr_t x.
func CapUint(x *Void) uintptr { return capInt(x) }
func CapSintOf(x *Void) int   { return capSint(x) }

// PtrToCap converts a pointer to an intptr_t or a uintptr_t, and
// CapToPtr one of those to a pointer: the same thing.
func PtrToCap(p *Void) *Void { return p }
func CapToPtr(x *Void) *Void { return x }

// capOf returns x op y, the integer r, as an intptr_t or a uintptr_t:
// the one of x and y that is a pointer, moved to r, if only one is.
func capOf(r uintptr, x, y *Void) *Void {
	px, py := isPointer(unsafe.Pointer(x)), isPointer(unsafe.Pointer(y))
	switch {
	case px && !py:
		return Add(x, int(r-Addr(x)))
	case py && !px:
		return Add(y, int(r-Addr(y)))
	}
	return FromInt(r)
}

// CapArith is x op y ('+', '-', '*', '/', '%', '&', '|', '^'), signed if
// s.
func CapArith(op byte, x, y *Void, s bool) *Void {
	a, b := capInt(x), capInt(y)
	var r uintptr
	switch op {
	case '+':
		r = a + b
	case '-':
		r = a - b
	case '*':
		r = a * b
	case '/':
		if s {
			r = uintptr(int(a) / int(b))
		} else {
			r = a / b
		}
	case '%':
		if s {
			r = uintptr(int(a) % int(b))
		} else {
			r = a % b
		}
	case '&':
		r = a & b
	case '|':
		r = a | b
	default:
		r = a ^ b
	}
	switch op {
	case '+', '-', '&', '|':
		return capOf(r, x, y)
	}
	return FromInt(r)
}

// CapShift is x << n ('<') or x >> n ('>'), arithmetic if s: an
// integer.
func CapShift(op byte, x *Void, n uint64, s bool) *Void {
	a := capInt(x)
	switch {
	case op == '<':
		a <<= n
	case s:
		a = uintptr(capSint(x) >> n)
	default:
		a >>= n
	}
	return FromInt(a)
}

// CapUnary is -x ('-') or ^x ('~'): an integer.
func CapUnary(op byte, x *Void) *Void {
	if op == '-' {
		return FromInt(-capInt(x))
	}
	return FromInt(^capInt(x))
}

// IntToCap and UintToCap convert an integer to an intptr_t or a
// uintptr_t.
func IntToCap(v int64) *Void   { return FromInt(uintptr(v)) }
func UintToCap(v uint64) *Void { return FromInt(uintptr(v)) }
