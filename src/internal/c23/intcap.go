// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import "unsafe"

// intptr_t and uintptr_t (doc/gd/cc.md, "Integers that are pointers").
//
// C keeps pointers in integers, and makes pointers of integers. A
// pointer that is made of an integer that the program computed is one
// that it may have made up, so only these two types carry a pointer
// through: a value of theirs is a word that the collector looks at, a
// *Void here and an Intcap in memory, with a bit that no address has
// (intTag, bit 63, the runtime's cIntTag) which says that the value was
// made of a pointer and is one still. A value that was made of an
// integer has the integer's low 63 bits and not the bit.
//
// Arithmetic on a value that is a pointer gives one that is a pointer
// still if the result is in the object that the pointer was in, or at
// its end: p + 4, p & ^7. Anything else gives an integer: p * 2, p ^ key,
// p - q. A value that is converted to another integer type is its
// address, or its integer: the bit is not part of it.
//
// A value that is a pointer converts to that pointer. One that is an
// integer converts to a pointer as any integer does (IntPtr): to the
// integer, which is no address that the program may use.

// Intcap is an intptr_t or uintptr_t in memory.
type Intcap struct {
	w unsafe.Pointer
}

// intTag is the bit of an intptr_t or a uintptr_t that is a pointer.
const intTag = uintptr(1) << (8*unsafe.Sizeof(uintptr(0)) - 1)

// capInt returns the integer that x is: its address without the bit.
func capInt(x *Void) uintptr { return Addr(x) &^ intTag }

// capSint is capInt for an intptr_t: the 63 bits are a signed integer's.
func capSint(x *Void) int {
	v := Addr(x)
	if v&intTag != 0 {
		return int(v &^ intTag)
	}
	return int(v<<1) >> 1
}

// CapUint returns the value of the uintptr_t x as an integer, and
// CapSintOf that of the intptr_t x.
func CapUint(x *Void) uintptr { return capInt(x) }
func CapSintOf(x *Void) int   { return capSint(x) }

// PtrToCap converts a pointer to an intptr_t or a uintptr_t: its
// address, which is a pointer still.
func PtrToCap(p *Void) *Void {
	if p == nil {
		return nil
	}
	return FromInt(Addr(p)&^ptrTags | intTag)
}

// ptrTags are the bits that a pointer may have and no address has: the
// runtime's cPtrBits.
const ptrTags = uintptr(3) << (8*unsafe.Sizeof(uintptr(0)) - 2)

// CapToPtr converts an intptr_t or a uintptr_t to a pointer: the pointer
// that it was made of, or the integer as a pointer (IntPtr).
func CapToPtr(x *Void) *Void {
	if v := Addr(x); v&intTag != 0 {
		return FromInt(v &^ intTag)
	}
	return IntPtr(Addr(x))
}

// IntPtr converts an integer to a pointer. The pointer is one that C may
// keep and compare: (void *)1, a handler's SIG_IGN. An integer that is
// the address of something of the program's is counted, and with
// GODEBUG=cintptr=1 is a pointer that cannot be used: nothing says that
// the program was given that address.
func IntPtr(v uintptr) *Void { return (*Void)(intPtr(v)) }

//go:linkname intPtr runtime.c23IntPtr
func intPtr(v uintptr) unsafe.Pointer

// capOf returns x op y, an integer, as an intptr_t or a uintptr_t: a
// pointer still if one of x and y is one and r is in its object.
func capOf(r uintptr, x, y *Void) *Void {
	a, b := Addr(x), Addr(y)
	if (a|b)&intTag == 0 {
		return FromInt(r &^ intTag)
	}
	if a&intTag != 0 && b&intTag != 0 {
		// Of two pointers: a distance, a comparison.
		return FromInt(r &^ intTag)
	}
	p := a
	if b&intTag != 0 {
		p = b
	}
	if base, size := object(unsafe.Pointer(p &^ intTag)); base != nil && r-uintptr(base) <= size {
		return FromInt(r | intTag)
	}
	return FromInt(r &^ intTag)
}

// CapArith is x op y ('+', '-', '*', '/', '%', '&', '|', '^'), signed if
// s.
func CapArith(op byte, x, y *Void, s bool) *Void {
	a, b := capInt(x), capInt(y)
	if s {
		a, b = uintptr(capSint(x)), uintptr(capSint(y))
	}
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
	return FromInt(r &^ intTag)
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
	return FromInt(a &^ intTag)
}

// CapUnary is -x ('-') or ^x ('~'): an integer.
func CapUnary(op byte, x *Void) *Void {
	if op == '-' {
		return FromInt(-capInt(x) &^ intTag)
	}
	return FromInt(^capInt(x) &^ intTag)
}

// IntToCap and UintToCap convert an integer to an intptr_t or a
// uintptr_t: its low 63 bits.
func IntToCap(v int64) *Void   { return FromInt(uintptr(v) &^ intTag) }
func UintToCap(v uint64) *Void { return FromInt(uintptr(v) &^ intTag) }
