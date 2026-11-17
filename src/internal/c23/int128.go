// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import "math/bits"

// GCC's __int128 and unsigned __int128, which Go has no type for: two
// 64-bit halves, two's complement, with C's operators as methods.

type Int128 struct {
	Lo uint64
	Hi int64
}

type Uint128 struct {
	Lo, Hi uint64
}

func I128(v int64) Int128     { return Int128{uint64(v), v >> 63} }
func I128U(v uint64) Int128   { return Int128{v, 0} }
func U128(v int64) Uint128    { return Uint128{uint64(v), uint64(v >> 63)} }
func U128U(v uint64) Uint128  { return Uint128{v, 0} }
func (x Int128) U() Uint128   { return Uint128{x.Lo, uint64(x.Hi)} }
func (x Uint128) I() Int128   { return Int128{x.Lo, int64(x.Hi)} }
func (x Int128) IsZero() bool { return x.Lo == 0 && x.Hi == 0 }
func (x Uint128) IsZero() bool {
	return x.Lo == 0 && x.Hi == 0
}

func (x Uint128) Add(y Uint128) Uint128 {
	lo, c := bits.Add64(x.Lo, y.Lo, 0)
	hi, _ := bits.Add64(x.Hi, y.Hi, c)
	return Uint128{lo, hi}
}

func (x Uint128) Sub(y Uint128) Uint128 {
	lo, b := bits.Sub64(x.Lo, y.Lo, 0)
	hi, _ := bits.Sub64(x.Hi, y.Hi, b)
	return Uint128{lo, hi}
}

func (x Uint128) Mul(y Uint128) Uint128 {
	hi, lo := bits.Mul64(x.Lo, y.Lo)
	hi += x.Hi*y.Lo + x.Lo*y.Hi
	return Uint128{lo, hi}
}

func (x Uint128) And(y Uint128) Uint128 { return Uint128{x.Lo & y.Lo, x.Hi & y.Hi} }
func (x Uint128) Or(y Uint128) Uint128  { return Uint128{x.Lo | y.Lo, x.Hi | y.Hi} }
func (x Uint128) Xor(y Uint128) Uint128 { return Uint128{x.Lo ^ y.Lo, x.Hi ^ y.Hi} }
func (x Uint128) Not() Uint128          { return Uint128{^x.Lo, ^x.Hi} }
func (x Uint128) Neg() Uint128          { return Uint128{}.Sub(x) }

func (x Uint128) Shl(n uint) Uint128 {
	switch {
	case n >= 128:
		return Uint128{}
	case n >= 64:
		return Uint128{0, x.Lo << (n - 64)}
	case n == 0:
		return x
	}
	return Uint128{x.Lo << n, x.Hi<<n | x.Lo>>(64-n)}
}

func (x Uint128) Shr(n uint) Uint128 {
	switch {
	case n >= 128:
		return Uint128{}
	case n >= 64:
		return Uint128{x.Hi >> (n - 64), 0}
	case n == 0:
		return x
	}
	return Uint128{x.Lo>>n | x.Hi<<(64-n), x.Hi >> n}
}

func (x Uint128) Cmp(y Uint128) int {
	switch {
	case x.Hi < y.Hi || x.Hi == y.Hi && x.Lo < y.Lo:
		return -1
	case x == y:
		return 0
	}
	return 1
}

// divmod returns x/y and x%y.
func (x Uint128) divmod(y Uint128) (Uint128, Uint128) {
	if y.IsZero() {
		panic("integer divide by zero")
	}
	if y.Hi == 0 && x.Hi < y.Lo {
		q, r := bits.Div64(x.Hi, x.Lo, y.Lo)
		return Uint128{q, 0}, Uint128{r, 0}
	}
	// Bit by bit.
	var q, r Uint128
	for i := 127; i >= 0; i-- {
		r = r.Shl(1)
		r.Lo |= x.Shr(uint(i)).Lo & 1
		if r.Cmp(y) >= 0 {
			r = r.Sub(y)
			q = q.Or(Uint128{1, 0}.Shl(uint(i)))
		}
	}
	return q, r
}

func (x Uint128) Div(y Uint128) Uint128 { q, _ := x.divmod(y); return q }
func (x Uint128) Rem(y Uint128) Uint128 { _, r := x.divmod(y); return r }

func (x Int128) Add(y Int128) Int128 { return x.U().Add(y.U()).I() }
func (x Int128) Sub(y Int128) Int128 { return x.U().Sub(y.U()).I() }
func (x Int128) Mul(y Int128) Int128 { return x.U().Mul(y.U()).I() }
func (x Int128) And(y Int128) Int128 { return x.U().And(y.U()).I() }
func (x Int128) Or(y Int128) Int128  { return x.U().Or(y.U()).I() }
func (x Int128) Xor(y Int128) Int128 { return x.U().Xor(y.U()).I() }
func (x Int128) Not() Int128         { return x.U().Not().I() }
func (x Int128) Neg() Int128         { return x.U().Neg().I() }
func (x Int128) Shl(n uint) Int128   { return x.U().Shl(n).I() }

func (x Int128) Shr(n uint) Int128 {
	if x.Hi >= 0 {
		return x.U().Shr(n).I()
	}
	return x.Not().U().Shr(n).Not().I()
}

func (x Int128) abs() Uint128 {
	if x.Hi < 0 {
		return x.Neg().U()
	}
	return x.U()
}

func (x Int128) Div(y Int128) Int128 {
	q := x.abs().Div(y.abs()).I()
	if (x.Hi < 0) != (y.Hi < 0) {
		q = q.Neg()
	}
	return q
}

func (x Int128) Rem(y Int128) Int128 {
	r := x.abs().Rem(y.abs()).I()
	if x.Hi < 0 {
		r = r.Neg()
	}
	return r
}

func (x Int128) Cmp(y Int128) int {
	switch {
	case x.Hi < y.Hi || x.Hi == y.Hi && x.Lo < y.Lo:
		return -1
	case x == y:
		return 0
	}
	return 1
}

func (x Int128) Float64() float64 {
	if x.Hi < 0 {
		return -x.Neg().U().Float64()
	}
	return x.U().Float64()
}

func (x Uint128) Float64() float64 { return float64(x.Hi)*(1<<64) + float64(x.Lo) }

func I128F(f float64) Int128 {
	if f < 0 {
		return U128F(-f).I().Neg()
	}
	return U128F(f).I()
}

func U128F(f float64) Uint128 {
	hi := uint64(f / (1 << 64))
	return Uint128{uint64(f - float64(hi)*(1<<64)), hi}
}

func LoadI128(p Ptr) Int128 { return Int128{Load64(p), int64(Load64(Add(p, 8)))} }
func LoadU128(p Ptr) Uint128 {
	return Uint128{Load64(p), Load64(Add(p, 8))}
}
func StoreI128(p Ptr, v Int128) {
	Store64(p, v.Lo)
	Store64(Add(p, 8), uint64(v.Hi))
}
func StoreU128(p Ptr, v Uint128) {
	Store64(p, v.Lo)
	Store64(Add(p, 8), v.Hi)
}
