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

// endTag and poisonTag are the runtime's cEnd and cPoison: the bits of a
// pointer to the end of its object, and of one that the program was not
// given. A load or a store through either faults.
const (
	endTag    = uintptr((1 << 54) * (unsafe.Sizeof(uintptr(0)) / 8))
	poisonTag = uintptr((1 << 55) * (unsafe.Sizeof(uintptr(0)) / 8))
)

// capInt returns the integer that x is: its address without the bits,
// if it is a pointer.
func capInt(x *Void) uintptr {
	v := Addr(x)
	if v&intTag != 0 {
		return v &^ (intTag | endTag)
	}
	return v
}

// capSint is capInt for an intptr_t: the 63 bits are a signed integer's.
func capSint(x *Void) int {
	v := Addr(x)
	if v&intTag != 0 {
		return int(v &^ (intTag | endTag))
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
	if a := Addr(p); a&poisonTag != 0 {
		// What was made of an integer is the integer again.
		return FromInt(a &^ (poisonTag | intTag))
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
// the address of something of the program's is a pointer that cannot be
// used, one that faults: nothing says that the program was given that
// address. (GODEBUG=cintptr=0 lets it be, and it is counted.)
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
	switch inObject(p&^intTag, r) {
	case 1:
		return FromInt(r | intTag)
	case 2:
		return FromInt(r | intTag | endTag)
	}
	return FromInt(r &^ intTag)
}

// inObject says where r is in what the pointer p, which may have endTag,
// points into: 0 if out of it, 1 if in it, 2 if at its end.
//
//go:linkname inObject runtime.c23InObject
func inObject(p, r uintptr) int

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
