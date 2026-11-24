// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"math/bits"
	"unsafe"
)

// GCC's bit builtins (__builtin_clz and its kin) and C23's stdbit.h,
// for 32- and 64-bit operands.

func Clz32(x uint32) int32      { return int32(bits.LeadingZeros32(x)) }
func Clz64(x uint64) int32      { return int32(bits.LeadingZeros64(x)) }
func Ctz32(x uint32) int32      { return int32(bits.TrailingZeros32(x)) }
func Ctz64(x uint64) int32      { return int32(bits.TrailingZeros64(x)) }
func Popcount32(x uint32) int32 { return int32(bits.OnesCount32(x)) }
func Popcount64(x uint64) int32 { return int32(bits.OnesCount64(x)) }
func Parity32(x uint32) int32   { return int32(bits.OnesCount32(x) & 1) }
func Parity64(x uint64) int32   { return int32(bits.OnesCount64(x) & 1) }
func Bswap16(x uint16) uint16   { return bits.ReverseBytes16(x) }
func Bswap32(x uint32) uint32   { return bits.ReverseBytes32(x) }
func Bswap64(x uint64) uint64   { return bits.ReverseBytes64(x) }

func Ffs32(x int32) int32 {
	if x == 0 {
		return 0
	}
	return int32(bits.TrailingZeros32(uint32(x))) + 1
}

func Ffs64(x int64) int32 {
	if x == 0 {
		return 0
	}
	return int32(bits.TrailingZeros64(uint64(x))) + 1
}

func Clrsb32(x int32) int32 {
	if x < 0 {
		x = ^x
	}
	return int32(bits.LeadingZeros32(uint32(x))) - 1
}

func Clrsb64(x int64) int32 {
	if x < 0 {
		x = ^x
	}
	return int32(bits.LeadingZeros64(uint64(x))) - 1
}

// An i128 is a 128-bit two's complement integer, for overflow checks.
type i128 struct {
	hi int64
	lo uint64
}

func toI128(v int64, unsigned bool) i128 {
	if unsigned || v >= 0 {
		return i128{0, uint64(v)}
	}
	return i128{-1, uint64(v)}
}

func (a i128) add(b i128) i128 {
	lo, c := bits.Add64(a.lo, b.lo, 0)
	return i128{a.hi + b.hi + int64(c), lo}
}

func (a i128) neg() i128 { return i128{^a.hi, ^a.lo}.add(i128{0, 1}) }

// mul returns a*b, and whether it overflowed 128 bits.
func (a i128) mul(b i128) (i128, bool) {
	neg := false
	if a.hi < 0 {
		a, neg = a.neg(), !neg
	}
	if b.hi < 0 {
		b, neg = b.neg(), !neg
	}
	if a.hi != 0 && b.hi != 0 {
		return i128{}, true
	}
	hi, lo := bits.Mul64(a.lo, b.lo)
	h2, l2 := bits.Mul64(uint64(a.hi), b.lo)
	h3, l3 := bits.Mul64(a.lo, uint64(b.hi))
	if h2 != 0 || h3 != 0 {
		return i128{}, true
	}
	hi, c := bits.Add64(hi, l2, 0)
	over := c != 0
	hi, c = bits.Add64(hi, l3, 0)
	over = over || c != 0 || int64(hi) < 0
	r := i128{int64(hi), lo}
	if neg {
		r = r.neg()
	}
	return r, over
}

// fits reports whether a is in range of a width-bit integer, unsigned or
// not.
func (a i128) fits(width int, unsigned bool) bool {
	if unsigned {
		if a.hi != 0 {
			return false
		}
		return width >= 64 || a.lo < 1<<width
	}
	if width >= 64 {
		return a.hi == 0 && int64(a.lo) >= 0 || a.hi == -1 && int64(a.lo) < 0
	}
	lim := uint64(1) << (width - 1)
	if a.hi == 0 {
		return a.lo < lim
	}
	return a.hi == -1 && a.lo >= -lim
}

// Overflow is __builtin_add_overflow and its kin (and C23's ckd_add):
// a op b, of the given signedness, in a result of width bits and the
// given signedness: the result's bits, and whether it did not fit.
func Overflow(op byte, a int64, au bool, b int64, bu bool, width int32, ru bool) (int64, bool) {
	x, y := toI128(a, au), toI128(b, bu)
	var r i128
	over := false
	switch op {
	case '+':
		r = x.add(y)
	case '-':
		r = x.add(y.neg())
	case '*':
		r, over = x.mul(y)
	}
	return int64(r.lo), over || !r.fits(int(width), ru)
}

// LoadBytes loads n (1 to 8) bytes at p as a little-endian integer: a
// packed bit-field's.
func LoadBytes(p *Void, n int) uint64 {
	b := BytesAt(p, uintptr(n))
	var v uint64
	for i := n - 1; i >= 0; i-- {
		v = v<<8 | uint64(b[i])
	}
	return v
}

// StoreBytes stores the n low bytes of v at p, little-endian.
func StoreBytes(p *Void, n int, v uint64) {
	a := p
	for i := 0; i < n; i++ {
		*(*byte)(unsafe.Add(unsafe.Pointer(a), i)) = byte(v >> (8 * i))
	}
}
