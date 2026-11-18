// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

// Intcaps: intptr_t and uintptr_t are integers that carry a capability, as
// CHERI C's __intcap types are. A pointer converted to one keeps its
// capability and gets it back converted to a pointer again; converted to
// another integer type, a pointer or an intcap is its address only.
// Arithmetic is on the address, keeping a capability, so that
// (T *)(((uintptr_t)p + 7) & ~7) is still p's.
//
// A value is a Ptr. In memory an intcap is an Intcap, a split word: its
// bytes are the address, which scalar stores may write (memcpy, scanf,
// unions), and its capability is kept in the object's side table, as for a
// pointer in a union; accesses through it are checked against that
// capability, whatever the address has become.

// Intcap is an intcap in memory.
type Intcap struct {
	w uintptr `c23:"union"`
}

// rebase is p moved to address a, its capability kept.
func rebase(p Ptr, a uintptr) Ptr { return Add(p, int(a-Addr(p))) }

// CapArith is x op y ('+', '-', '*', '/', '%', '&', '|', '^') on intcaps:
// on their addresses, signed if s, keeping x's capability, or y's if x has
// none (CHERI C's rule).
func CapArith(op byte, x, y Ptr, s bool) Ptr {
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
	if x.obj == nil && y.obj != nil {
		return rebase(y, r)
	}
	return rebase(x, r)
}

// CapShift is x << n ('<') or x >> n ('>'), arithmetic if s.
func CapShift(op byte, x Ptr, n uint64, s bool) Ptr {
	a := Addr(x)
	switch {
	case op == '<':
		a <<= n
	case s:
		a = uintptr(int(a) >> n)
	default:
		a >>= n
	}
	return rebase(x, a)
}

// CapUnary is -x ('-') or ^x ('~').
func CapUnary(op byte, x Ptr) Ptr {
	if op == '-' {
		return rebase(x, -Addr(x))
	}
	return rebase(x, ^Addr(x))
}

// IntToCap and UintToCap convert an integer to an intcap, which has no
// capability.
func IntToCap(v int64) Ptr   { return FromInt(uintptr(v)) }
func UintToCap(v uint64) Ptr { return FromInt(uintptr(v)) }
