// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

import (
	c23 "internal/c23"
	"math"
)

// The rounding directions of fromfp and its kin (math.h's FP_INT_*).
const (
	fpIntUpward = iota
	fpIntDownward
	fpIntTowardZero
	fpIntToNearestFromZero
	fpIntToNearest
)

// reducePi returns x reduced modulo 2, exactly, for the functions of πx.
func reducePi(x float64) float64 { return math.Mod(x, 2) }

func Sinpi(x float64) float64 {
	if math.IsInf(x, 0) {
		return domain()
	}
	if math.IsNaN(x) {
		return x
	}
	r := reducePi(x) // in (-2, 2), x's sign
	switch r {
	case 0, 1, -1: // sinpi(±n) is ±0
		return math.Copysign(0, x)
	case 0.5, -1.5:
		return 1
	case -0.5, 1.5:
		return -1
	}
	return math.Sin(math.Pi * r)
}

func Cospi(x float64) float64 {
	if math.IsInf(x, 0) {
		return domain()
	}
	if math.IsNaN(x) {
		return x
	}
	r := math.Abs(reducePi(x)) // in [0, 2)
	switch r {
	case 0:
		return 1
	case 1:
		return -1
	case 0.5, 1.5:
		return 0
	}
	return math.Cos(math.Pi * r)
}

func Tanpi(x float64) float64 {
	if math.IsInf(x, 0) {
		return domain()
	}
	if math.IsNaN(x) {
		return x
	}
	r := reducePi(x)
	switch r {
	case 0: // an even integer
		return math.Copysign(0, x)
	case 1, -1: // an odd one
		return math.Copysign(0, -x)
	case 0.5, -1.5: // poles: an even integer and ½
		poleErr()
		return math.Inf(1)
	case -0.5, 1.5:
		poleErr()
		return math.Inf(-1)
	}
	return math.Tan(math.Pi * r)
}

func Acospi(x float64) float64 {
	if x < -1 || x > 1 {
		return domain()
	}
	return math.Acos(x) / math.Pi
}

func Asinpi(x float64) float64 {
	if x < -1 || x > 1 {
		return domain()
	}
	return math.Asin(x) / math.Pi
}

func Atanpi(x float64) float64 { return math.Atan(x) / math.Pi }

func Atan2pi(y, x float64) float64 { return math.Atan2(y, x) / math.Pi }

func Exp10(x float64) float64 { return rangedTo0(math.Pow(10, x), x) }

func Exp2m1(x float64) float64 { return ranged(math.Expm1(x*math.Ln2), x) }

func Exp10m1(x float64) float64 { return ranged(math.Expm1(x*math.Ln10), x) }

func Logp1(x float64) float64 { return Log1p(x) }

func Log2p1(x float64) float64 { return Log1p(x) / math.Ln2 }

func Log10p1(x float64) float64 { return Log1p(x) / math.Ln10 }

func Roundeven(x float64) float64 { return math.RoundToEven(x) }

func Nexttoward(x, y float64) float64 { return Nextafter(x, y) }

func Rsqrt(x float64) float64 {
	switch {
	case x < 0:
		return domain()
	case x == 0:
		poleErr()
		return math.Copysign(math.Inf(1), x)
	}
	return 1 / math.Sqrt(x)
}

// Pown is pown: x to the integer power n.
func Pown(x float64, n int64) float64 {
	if x == 0 && n < 0 {
		poleErr()
		if n%2 != 0 {
			return math.Copysign(math.Inf(1), x)
		}
		return math.Inf(1)
	}
	return ranged(math.Pow(x, float64(n)), x)
}

// Powr is powr: e to y log x, which is not defined for negative x.
func Powr(x, y float64) float64 {
	switch {
	case math.IsNaN(x) || math.IsNaN(y):
		return math.NaN()
	case x < 0:
		return domain()
	case x == 0 && y == 0, math.IsInf(x, 1) && y == 0, x == 1 && math.IsInf(y, 0):
		return domain()
	case x == 0 && y < 0:
		poleErr()
		return math.Inf(1)
	}
	return ranged(math.Pow(x, y), x, y)
}

// Rootn is rootn: x to the power 1/n.
func Rootn(x float64, n int64) float64 {
	switch {
	case n == 0 || x < 0 && n%2 == 0:
		return domain()
	case x == 0 && n < 0:
		poleErr()
		return math.Copysign(math.Inf(1), x)
	case n == 1:
		return x
	case n == -1:
		return 1 / x
	case n == 2:
		return math.Sqrt(x)
	case n == 3:
		return math.Cbrt(x)
	}
	r := math.Pow(math.Abs(x), 1/float64(n))
	return math.Copysign(r, x)
}

// Compoundn is compoundn: (1 + x) to the integer power n.
func Compoundn(x float64, n int64) float64 {
	switch {
	case x < -1:
		return domain()
	case x == -1 && n < 0:
		poleErr()
		return math.Inf(1)
	case n == 0:
		return 1
	}
	return ranged(math.Exp(float64(n)*math.Log1p(x)), x)
}

// Remquo is remquo: remainder, and in *quo the low bits of the quotient
// with its sign.
func Remquo(x, y float64, quo c23.Ptr) float64 {
	r := Remainder(x, y)
	q := int64(0)
	if !math.IsNaN(r) && !math.IsInf(x, 0) && y != 0 {
		n := math.RoundToEven((x - r) / y)
		q = int64(math.Mod(math.Abs(n), 1<<30))
		if n < 0 {
			q = -q
		}
	}
	c23.Store32(quo, uint32(int32(q)))
	return r
}

func Nextup(x float64) float64 { return math.Nextafter(x, math.Inf(1)) }

func Nextdown(x float64) float64 { return math.Nextafter(x, math.Inf(-1)) }

// fromfp rounds x in direction round to an integer of width bits, signed
// or not; a NaN, an infinity, or an integer out of range is a domain
// error (C23 7.12.9.10).
func fromfp(x float64, round int32, width uint32, unsigned bool) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) || width == 0 {
		return domain()
	}
	var r float64
	switch round {
	case fpIntUpward:
		r = math.Ceil(x)
	case fpIntDownward:
		r = math.Floor(x)
	case fpIntTowardZero:
		r = math.Trunc(x)
	case fpIntToNearestFromZero:
		r = math.Round(x)
	case fpIntToNearest:
		r = math.RoundToEven(x)
	default:
		return domain()
	}
	w := float64(min(width, 64))
	if unsigned {
		if r < 0 || r >= math.Exp2(w) {
			return domain()
		}
	} else if r < -math.Exp2(w-1) || r >= math.Exp2(w-1) {
		return domain()
	}
	return r + 0 // no -0
}

func Fromfp(x float64, round int32, width uint32) float64 { return fromfp(x, round, width, false) }

func Ufromfp(x float64, round int32, width uint32) float64 { return fromfp(x, round, width, true) }

func Fromfpx(x float64, round int32, width uint32) float64 { return fromfp(x, round, width, false) }

func Ufromfpx(x float64, round int32, width uint32) float64 { return fromfp(x, round, width, true) }

// maxmin is fmaximum and its kin: the larger (max) or smaller of x and
// y, of their magnitudes if mag (then of their values if equal), -0
// below +0; a NaN is the result (if num, the other operand is).
func maxmin(x, y float64, max, mag, num bool) float64 {
	switch {
	case math.IsNaN(x) && math.IsNaN(y):
		return x + y
	case math.IsNaN(x):
		if num {
			return y
		}
		return x
	case math.IsNaN(y):
		if num {
			return x
		}
		return y
	}
	if mag {
		ax, ay := math.Abs(x), math.Abs(y)
		switch {
		case ax > ay:
			if max {
				return x
			}
			return y
		case ax < ay:
			if max {
				return y
			}
			return x
		}
	}
	switch {
	case x == y && x == 0:
		if math.Signbit(x) != max {
			return x
		}
		return y
	case x > y == max:
		return x
	}
	return y
}

func Fmaximum(x, y float64) float64 { return maxmin(x, y, true, false, false) }

func Fminimum(x, y float64) float64 { return maxmin(x, y, false, false, false) }

func FmaximumMag(x, y float64) float64 { return maxmin(x, y, true, true, false) }

func FminimumMag(x, y float64) float64 { return maxmin(x, y, false, true, false) }

func FmaximumNum(x, y float64) float64 { return maxmin(x, y, true, false, true) }

func FminimumNum(x, y float64) float64 { return maxmin(x, y, false, false, true) }

func FmaximumMagNum(x, y float64) float64 { return maxmin(x, y, true, true, true) }

func FminimumMagNum(x, y float64) float64 { return maxmin(x, y, false, true, true) }

func Fmaximumf(x, y float32) float32 { return float32(Fmaximum(float64(x), float64(y))) }

func Fminimumf(x, y float32) float32 { return float32(Fminimum(float64(x), float64(y))) }

func FmaximumMagf(x, y float32) float32 { return float32(FmaximumMag(float64(x), float64(y))) }

func FminimumMagf(x, y float32) float32 { return float32(FminimumMag(float64(x), float64(y))) }

func FmaximumNumf(x, y float32) float32 { return float32(FmaximumNum(float64(x), float64(y))) }

func FminimumNumf(x, y float32) float32 { return float32(FminimumNum(float64(x), float64(y))) }

func FmaximumMagNumf(x, y float32) float32 { return float32(FmaximumMagNum(float64(x), float64(y))) }

func FminimumMagNumf(x, y float32) float32 { return float32(FminimumMagNum(float64(x), float64(y))) }

// The narrowing operations: float results of double operations (and
// double of long double's, which is double).
func Fadd(x, y float64) float32 {
	if !c23.FpHardware {
		return rangedf(x+y, float32(x), float32(y))
	}
	return narrowOdd(c23.FpAdd64(c23.FpControl(c23.FeTowardZero), x, y))
}

func Fsub(x, y float64) float32 {
	if !c23.FpHardware {
		return rangedf(x-y, float32(x), float32(y))
	}
	return narrowOdd(c23.FpSub64(c23.FpControl(c23.FeTowardZero), x, y))
}

func Fmul(x, y float64) float32 {
	if !c23.FpHardware {
		return rangedf(x*y, float32(x), float32(y))
	}
	return narrowOdd(c23.FpMul64(c23.FpControl(c23.FeTowardZero), x, y))
}

func Fdiv(x, y float64) float32 {
	if !c23.FpHardware {
		return rangedf(x/y, float32(x), float32(y))
	}
	return narrowOdd(c23.FpDiv64(c23.FpControl(c23.FeTowardZero), x, y))
}

func Ffma(x, y, z float64) float32 {
	if !c23.FpHasFMA {
		return float32(math.FMA(x, y, z))
	}
	return narrowOdd(c23.FpFma64(c23.FpControl(c23.FeTowardZero), x, y, z))
}

func Fsqrt(x float64) float32 {
	if !c23.FpHardware || x < 0 {
		return float32(Sqrt(x))
	}
	return narrowOdd(c23.FpSqrt64(c23.FpControl(c23.FeTowardZero), x))
}

// narrowOdd rounds r, a double computed toward zero with MXCSR or FPSR
// st after, to float in the thread's direction, once: r with its last bit
// set if it was inexact ("round to odd") has 29 bits more than float,
// which then round as the exact value's would. The exceptions are the
// operation's, but for inexact, overflow and underflow, which the
// narrowing raises.
func narrowOdd(r float64, st uint64) float32 {
	ex := c23.FpExcepts(st)
	if ex&c23.FeInexact != 0 {
		r = math.Float64frombits(math.Float64bits(r) | 1)
	}
	f, nst := c23.FpNarrow(c23.FpControl(c23.CurRound()), r)
	ex = ex&^(c23.FeInexact|c23.FeOverflow|c23.FeUnderflow) | c23.FpExcepts(nst)
	if ex&c23.FeOverflow != 0 {
		c23.SetErrno(c23.ERANGE)
	}
	c23.RaiseUsed(ex)
	return f
}

// C23's narrowing operations from long double to double: long double is
// double, and these are its operations.
func Daddl(x, y float64) float64 { return dop('+', x, y) }

func Dsubl(x, y float64) float64 { return dop('-', x, y) }

func Dmull(x, y float64) float64 { return dop('*', x, y) }

func Ddivl(x, y float64) float64 { return dop('/', x, y) }

func dop(op byte, x, y float64) float64 {
	if c23.FenvUsed.Load() {
		return c23.Arith(op, x, y, c23.FeDynamic)
	}
	switch op {
	case '+':
		return x + y
	case '-':
		return x - y
	case '*':
		return x * y
	}
	return x / y
}

func Dfmal(x, y, z float64) float64 { return Fma(x, y, z) }

func Dsqrtl(x float64) float64 { return Sqrt(x) }

// Canonicalize is canonicalize: every encoding is canonical.
func Canonicalize(cx, x c23.Ptr) int32 {
	c23.StoreF64(cx, c23.LoadF64(x))
	return 0
}

func Canonicalizef(cx, x c23.Ptr) int32 {
	c23.StoreF32(cx, c23.LoadF32(x))
	return 0
}

const payload64 = 1<<51 - 1

const payload32 = 1<<22 - 1

func Getpayload(x c23.Ptr) float64 {
	v := c23.LoadF64(x)
	if !math.IsNaN(v) {
		return -1
	}
	return float64(math.Float64bits(v) & payload64)
}

func Getpayloadf(x c23.Ptr) float32 {
	v := c23.LoadF32(x)
	if v == v {
		return -1
	}
	return float32(math.Float32bits(v) & payload32)
}

func setpayload(res c23.Ptr, pl float64, quiet bool) int32 {
	if pl < 0 || pl != math.Trunc(pl) || pl > payload64 || !quiet && pl == 0 {
		c23.StoreF64(res, 0)
		return 1
	}
	bits := uint64(0x7ff)<<52 | uint64(pl)
	if quiet {
		bits |= 1 << 51
	}
	c23.StoreF64(res, math.Float64frombits(bits))
	return 0
}

func setpayloadf(res c23.Ptr, pl float32, quiet bool) int32 {
	if pl < 0 || pl != float32(math.Trunc(float64(pl))) || pl > payload32 || !quiet && pl == 0 {
		c23.StoreF32(res, 0)
		return 1
	}
	bits := uint32(0xff)<<23 | uint32(pl)
	if quiet {
		bits |= 1 << 22
	}
	c23.StoreF32(res, math.Float32frombits(bits))
	return 0
}

func Setpayload(res c23.Ptr, pl float64) int32 { return setpayload(res, pl, true) }

func Setpayloadsig(res c23.Ptr, pl float64) int32 { return setpayload(res, pl, false) }

func Setpayloadf(res c23.Ptr, pl float32) int32 { return setpayloadf(res, pl, true) }

func Setpayloadsigf(res c23.Ptr, pl float32) int32 { return setpayloadf(res, pl, false) }

// totalKey orders float64s as IEEE 754's totalOrder does, as integers.
func totalKey(v float64, mag bool) int64 {
	b := math.Float64bits(v)
	if mag {
		b &^= 1 << 63
	}
	if b>>63 != 0 {
		return -int64(b &^ (1 << 63))
	}
	return int64(b)
}

func totalKeyf(v float32, mag bool) int64 {
	b := math.Float32bits(v)
	if mag {
		b &^= 1 << 31
	}
	if b>>31 != 0 {
		return -int64(b &^ (1 << 31))
	}
	return int64(b)
}

func Totalorder(x, y c23.Ptr) int32 {
	return c23.B2I(totalKey(c23.LoadF64(x), false) <= totalKey(c23.LoadF64(y), false))
}

func Totalordermag(x, y c23.Ptr) int32 {
	return c23.B2I(totalKey(c23.LoadF64(x), true) <= totalKey(c23.LoadF64(y), true))
}

func Totalorderf(x, y c23.Ptr) int32 {
	return c23.B2I(totalKeyf(c23.LoadF32(x), false) <= totalKeyf(c23.LoadF32(y), false))
}

func Totalordermagf(x, y c23.Ptr) int32 {
	return c23.B2I(totalKeyf(c23.LoadF32(x), true) <= totalKeyf(c23.LoadF32(y), true))
}

// Issignaling is issignaling's: a NaN with its quiet bit clear.
func Issignaling(x float64) int32 {
	b := math.Float64bits(x)
	return c23.B2I(math.IsNaN(x) && b&(1<<51) == 0)
}

// Iseqsig is iseqsig: x == y, but a NaN is a domain error (errno EDOM
// and invalid), as in glibc. float and long double arguments are
// converted to double exactly.
func Iseqsig(x, y float64) int32 {
	if x != x || y != y {
		c23.SetErrno(c23.EDOM)
		c23.RaiseUsed(c23.FeInvalid)
		return 0
	}
	return c23.B2I(x == y)
}

func Issignalingf(x float32) int32 {
	b := math.Float32bits(x)
	return c23.B2I(x != x && b&(1<<22) == 0)
}

// The float variants of the rest.
func Sinpif(x float32) float32 { return float32(Sinpi(float64(x))) }

func Cospif(x float32) float32 { return float32(Cospi(float64(x))) }

func Tanpif(x float32) float32 { return float32(Tanpi(float64(x))) }

func Acospif(x float32) float32 { return float32(Acospi(float64(x))) }

func Asinpif(x float32) float32 { return float32(Asinpi(float64(x))) }

func Atanpif(x float32) float32 { return float32(Atanpi(float64(x))) }

func Atan2pif(y, x float32) float32 { return float32(Atan2pi(float64(y), float64(x))) }

func Exp10f(x float32) float32 { return rangedf(math.Pow(10, float64(x)), x) }

func Exp2m1f(x float32) float32 { return rangedf(math.Expm1(float64(x)*math.Ln2), x) }

func Exp10m1f(x float32) float32 { return rangedf(math.Expm1(float64(x)*math.Ln10), x) }

func Logp1f(x float32) float32 { return float32(Logp1(float64(x))) }

func Log2p1f(x float32) float32 { return float32(Log2p1(float64(x))) }

func Log10p1f(x float32) float32 { return float32(Log10p1(float64(x))) }

func Roundevenf(x float32) float32 { return float32(math.RoundToEven(float64(x))) }

func Llogbf(x float32) int64 { return c23.Llogb(float64(x)) }

func Rsqrtf(x float32) float32 { return float32(Rsqrt(float64(x))) }

func Pownf(x float32, n int64) float32 { return rangedf(Pown(float64(x), n), x) }

func Powrf(x, y float32) float32 { return rangedf(Powr(float64(x), float64(y)), x, y) }

func Rootnf(x float32, n int64) float32 { return float32(Rootn(float64(x), n)) }

func Compoundnf(x float32, n int64) float32 {
	return rangedf(Compoundn(float64(x), n), x)
}

func Nextupf(x float32) float32 { return math.Nextafter32(x, float32(math.Inf(1))) }

func Nextdownf(x float32) float32 { return math.Nextafter32(x, float32(math.Inf(-1))) }

func Nexttowardf(x float32, y float64) float32 {
	switch {
	case math.IsNaN(y):
		return float32(y)
	case float64(x) < y:
		return Nextupf(x)
	case float64(x) > y:
		return Nextdownf(x)
	}
	return float32(y)
}

func Remquof(x, y float32, quo c23.Ptr) float32 {
	return float32(Remquo(float64(x), float64(y), quo))
}

func Fromfpf(x float32, round int32, width uint32) float32 {
	return float32(fromfp(float64(x), round, width, false))
}

func Ufromfpf(x float32, round int32, width uint32) float32 {
	return float32(fromfp(float64(x), round, width, true))
}
