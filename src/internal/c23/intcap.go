// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

// intptr_t and uintptr_t, which cmd/cc still gives a kind of their own
// from when they carried a pointer's capability: their value is a *Void
// that holds the integer, and their arithmetic is the integer's. They
// are to become plain integers.

// Intcap is an intptr_t or uintptr_t in memory.
type Intcap struct {
	w uintptr
}

// CapArith is x op y ('+', '-', '*', '/', '%', '&', '|', '^'), signed if
// s.
func CapArith(op byte, x, y *Void, s bool) *Void {
	a, b := Addr(x), Addr(y)
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
	return FromInt(r)
}

// CapShift is x << n ('<') or x >> n ('>'), arithmetic if s.
func CapShift(op byte, x *Void, n uint64, s bool) *Void {
	a := Addr(x)
	switch {
	case op == '<':
		a <<= n
	case s:
		a = uintptr(int(a) >> n)
	default:
		a >>= n
	}
	return FromInt(a)
}

// CapUnary is -x ('-') or ^x ('~').
func CapUnary(op byte, x *Void) *Void {
	if op == '-' {
		return FromInt(-Addr(x))
	}
	return FromInt(^Addr(x))
}

// IntToCap and UintToCap convert an integer to an intptr_t or a
// uintptr_t.
func IntToCap(v int64) *Void   { return FromInt(uintptr(v)) }
func UintToCap(v uint64) *Void { return FromInt(uintptr(v)) }
