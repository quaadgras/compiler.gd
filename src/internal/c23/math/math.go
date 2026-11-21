// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

import (
	c23 "internal/c23"
	"math"
)

// domain sets errno to EDOM for a domain error, and returns NaN.
func domain() float64 {
	c23.SetErrno(c23.EDOM)
	c23.RaiseUsed(c23.FeInvalid)
	return math.NaN()
}

// pole sets errno to ERANGE for a pole error, and returns -Inf.
func pole() float64 {
	poleErr()
	return math.Inf(-1)
}

// poleErr reports a pole error: errno ERANGE, and divide-by-zero.
func poleErr() {
	c23.SetErrno(c23.ERANGE)
	c23.RaiseUsed(c23.FeDivByZero)
}

// ranged sets errno to ERANGE if v overflowed from finite x, raising
// overflow; a subnormal v raises underflow.
func ranged(v float64, xs ...float64) float64 {
	switch {
	case math.IsInf(v, 0):
		for _, x := range xs {
			if math.IsInf(x, 0) || math.IsNaN(x) {
				return v
			}
		}
		c23.SetErrno(c23.ERANGE)
		c23.RaiseUsed(c23.FeOverflow | c23.FeInexact)
	case v != 0 && math.Abs(v) < 0x1p-1022:
		c23.RaiseUsed(c23.FeUnderflow | c23.FeInexact)
	}
	return v
}

// rangedTo0 is ranged for a function that is 0 only where an argument is:
// a 0 from finite, nonzero arguments underflowed.
func rangedTo0(v float64, xs ...float64) float64 {
	if v != 0 || !c23.FenvUsed.Load() {
		return ranged(v, xs...)
	}
	for _, x := range xs {
		if x == 0 || math.IsInf(x, 0) || math.IsNaN(x) {
			return v
		}
	}
	c23.Raise(c23.FeUnderflow | c23.FeInexact)
	return v
}

func Sin(x float64) float64 {
	if math.IsInf(x, 0) {
		return domain()
	}
	return math.Sin(x)
}

func Cos(x float64) float64 {
	if math.IsInf(x, 0) {
		return domain()
	}
	return math.Cos(x)
}

func Tan(x float64) float64 {
	if math.IsInf(x, 0) {
		return domain()
	}
	return math.Tan(x)
}

func Asin(x float64) float64 {
	if x < -1 || x > 1 {
		return domain()
	}
	return math.Asin(x)
}

func Acos(x float64) float64 {
	if x < -1 || x > 1 {
		return domain()
	}
	return math.Acos(x)
}

func Atan(x float64) float64 { return math.Atan(x) }

func Atan2(y, x float64) float64 { return math.Atan2(y, x) }

func Sinh(x float64) float64 { return ranged(math.Sinh(x), x) }

func Cosh(x float64) float64 { return ranged(math.Cosh(x), x) }

func Tanh(x float64) float64 { return math.Tanh(x) }

func Asinh(x float64) float64 { return math.Asinh(x) }

func Acosh(x float64) float64 {
	if x < 1 {
		return domain()
	}
	return math.Acosh(x)
}

func Atanh(x float64) float64 {
	switch {
	case x < -1 || x > 1:
		return domain()
	case x == 1 || x == -1:
		poleErr()
	}
	return math.Atanh(x)
}

func Exp(x float64) float64 { return rangedTo0(math.Exp(x), x) }

func Exp2(x float64) float64 { return rangedTo0(math.Exp2(x), x) }

func Expm1(x float64) float64 { return ranged(math.Expm1(x), x) }

func Log(x float64) float64 {
	switch {
	case x < 0:
		return domain()
	case x == 0:
		return pole()
	}
	return math.Log(x)
}

func Log2(x float64) float64 {
	switch {
	case x < 0:
		return domain()
	case x == 0:
		return pole()
	}
	return math.Log2(x)
}

func Log10(x float64) float64 {
	switch {
	case x < 0:
		return domain()
	case x == 0:
		return pole()
	}
	return math.Log10(x)
}

func Log1p(x float64) float64 {
	switch {
	case x < -1:
		return domain()
	case x == -1:
		return pole()
	}
	return math.Log1p(x)
}

func Logb(x float64) float64 {
	if x == 0 {
		return pole()
	}
	return math.Logb(x)
}

func Ilogb(x float64) int32 {
	switch {
	case x == 0 || math.IsNaN(x):
		c23.SetErrno(c23.EDOM)
		c23.RaiseUsed(c23.FeInvalid)
		return math.MinInt32
	case math.IsInf(x, 0):
		c23.SetErrno(c23.EDOM)
		c23.RaiseUsed(c23.FeInvalid)
		return math.MaxInt32
	}
	return int32(math.Ilogb(x))
}

func Pow(x, y float64) float64 {
	switch {
	case x < 0 && !math.IsInf(x, 0) && y != math.Trunc(y) && !math.IsInf(y, 0):
		return domain()
	case x == 0 && y < 0:
		poleErr()
		return math.Pow(x, y)
	}
	return rangedTo0(math.Pow(x, y), x, y)
}

func Sqrt(x float64) float64 {
	if x < 0 {
		return domain()
	}
	if c23.FenvUsed.Load() {
		e := c23.Fenv()
		r, st := c23.FpSqrt64(c23.FpControl(e.Round), x)
		e.Except |= c23.FpExcepts(st)
		return r
	}
	return math.Sqrt(x)
}

func Cbrt(x float64) float64 { return math.Cbrt(x) }

func Hypot(x, y float64) float64 { return ranged(math.Hypot(x, y), x, y) }

func Fabs(x float64) float64 { return math.Abs(x) }

func Floor(x float64) float64 { return math.Floor(x) }

func Ceil(x float64) float64 { return math.Ceil(x) }

func Round(x float64) float64 { return math.Round(x) }

func Trunc(x float64) float64 { return math.Trunc(x) }

func Copysign(x, y float64) float64 { return math.Copysign(x, y) }

func Nextafter(x, y float64) float64 { return nextFlags(x, y, math.Nextafter(x, y), 0x1p-1022) }

func Fma(x, y, z float64) float64 {
	if c23.FenvUsed.Load() && c23.FpHasFMA {
		e := c23.Fenv()
		r, st := c23.FpFma64(c23.FpControl(e.Round), x, y, z)
		e.Except |= c23.FpExcepts(st)
		return r
	}
	return math.FMA(x, y, z)
}

func Erf(x float64) float64 { return math.Erf(x) }

func Erfc(x float64) float64 { return math.Erfc(x) }

func Tgamma(x float64) float64 { return ranged(math.Gamma(x), x) }

func Lgamma(x float64) float64 {
	v, _ := math.Lgamma(x)
	return v
}

// toLong converts x, rounded, to long: LONG_MIN if it does not fit, as
// x86 does, with errno EDOM.
func toLong(x float64) int64 {
	if math.IsNaN(x) || x >= 1<<63 || x < -(1<<63) {
		c23.SetErrno(c23.EDOM)
		c23.RaiseUsed(c23.FeInvalid)
		return math.MinInt64
	}
	return int64(x)
}

func Lround(x float64) int64 { return toLong(math.Round(x)) }

func Lrint(x float64) int64 {
	r := c23.RoundInt(x, c23.CurRound())
	v := toLong(r)
	if r != x && r >= -(1<<63) && r < 1<<63 {
		c23.RaiseUsed(c23.FeInexact)
	}
	return v
}

func Nearbyint(x float64) float64 { return c23.RintRaise(x, false) }

func Nearbyintf(x float32) float32 { return float32(c23.RintRaise(float64(x), false)) }

// nextFlags raises nextafter's exceptions (Annex F) for r, the number
// after x toward y in a type whose least normal is tiny: overflow to an
// infinity, underflow to a subnormal or zero.
func nextFlags[T float32 | float64](x, y, r T, tiny float64) T {
	switch {
	case !c23.FenvUsed.Load() || x == y || x != x || y != y:
	case math.IsInf(float64(r), 0):
		c23.Raise(c23.FeOverflow | c23.FeInexact)
	case math.Abs(float64(r)) < tiny:
		c23.Raise(c23.FeUnderflow | c23.FeInexact)
	}
	return r
}

func Fmod(x, y float64) float64 {
	if math.IsInf(x, 0) || y == 0 {
		if math.IsNaN(x) || math.IsNaN(y) {
			return math.NaN()
		}
		return domain()
	}
	return math.Mod(x, y)
}

func Remainder(x, y float64) float64 {
	if math.IsInf(x, 0) || y == 0 {
		if math.IsNaN(x) || math.IsNaN(y) {
			return math.NaN()
		}
		return domain()
	}
	return math.Remainder(x, y)
}

// Fmax is fmax: a NaN operand is ignored.
func Fmax(x, y float64) float64 {
	switch {
	case math.IsNaN(x):
		return y
	case math.IsNaN(y):
		return x
	}
	return math.Max(x, y)
}

// Fmin is fmin: a NaN operand is ignored.
func Fmin(x, y float64) float64 {
	switch {
	case math.IsNaN(x):
		return y
	case math.IsNaN(y):
		return x
	}
	return math.Min(x, y)
}

func Fdim(x, y float64) float64 {
	switch {
	case math.IsNaN(x) || math.IsNaN(y):
		return math.NaN()
	case x > y:
		return ranged(x-y, x, y)
	}
	return 0
}

func Ldexp(x float64, e int32) float64 { return ranged(math.Ldexp(x, int(e)), x) }

// Scalbln is scalbln, and scalbn whose int converts to long.
func Scalbln(x float64, e int64) float64 {
	e = max(min(e, 1<<20), -1<<20)
	return ranged(math.Ldexp(x, int(e)), x)
}

func Frexp(x float64, e c23.Ptr) float64 {
	f, n := math.Frexp(x)
	if math.IsInf(x, 0) || math.IsNaN(x) {
		f, n = x, 0
	}
	c23.Store32(e, uint32(int32(n)))
	return f
}

func Modf(x float64, ip c23.Ptr) float64 {
	if math.IsInf(x, 0) {
		c23.StoreF64(ip, x)
		return math.Copysign(0, x)
	}
	i, f := math.Modf(x)
	c23.StoreF64(ip, i)
	return f
}

// Nan is nan: a quiet NaN, whatever the tag.
func Nan(tag c23.Ptr) float64 { return math.NaN() }

const fpNaN = 0

const fpInfinite = 1

const fpZero = 2

const fpSubnormal = 3

const fpNormal = 4

// Fpclassify is fpclassify of a double.
func Fpclassify(x float64) int32 {
	switch {
	case math.IsNaN(x):
		return fpNaN
	case math.IsInf(x, 0):
		return fpInfinite
	case x == 0:
		return fpZero
	case math.Abs(x) < 0x1p-1022:
		return fpSubnormal
	}
	return fpNormal
}

// Fpclassifyf is fpclassify of a float.
func Fpclassifyf(x float32) int32 {
	if c := Fpclassify(float64(x)); c != fpNormal {
		return c
	}
	if math.Abs(float64(x)) < 0x1p-126 {
		return fpSubnormal
	}
	return fpNormal
}

// Signbit is signbit: whether x's sign is negative.
func Signbit(x float64) int32 { return c23.B2I(math.Signbit(x)) }

// rangedf sets errno to ERANGE if v, rounded to float, overflowed, raising
// overflow; underflow if it is subnormal, or 0 from a nonzero v.
func rangedf(v float64, xs ...float32) float32 {
	r := float32(v)
	switch {
	case math.IsInf(float64(r), 0) && !math.IsInf(v, 0):
		for _, x := range xs {
			if math.IsInf(float64(x), 0) || math.IsNaN(float64(x)) {
				return r
			}
		}
		c23.SetErrno(c23.ERANGE)
		c23.RaiseUsed(c23.FeOverflow | c23.FeInexact)
	case r != 0 && math.Abs(float64(r)) < 0x1p-126 || r == 0 && v != 0:
		c23.RaiseUsed(c23.FeUnderflow | c23.FeInexact)
	}
	return r
}

// The float versions of functions that only round the double result.
func sinf(x float32) float32 { return float32(Sin(float64(x))) }

func cosf(x float32) float32 { return float32(Cos(float64(x))) }

func tanf(x float32) float32 { return float32(Tan(float64(x))) }

func asinf(x float32) float32 { return float32(Asin(float64(x))) }

func acosf(x float32) float32 { return float32(Acos(float64(x))) }

func atanf(x float32) float32 { return float32(Atan(float64(x))) }

func atan2f(x, y float32) float32 { return float32(Atan2(float64(x), float64(y))) }

func tanhf(x float32) float32 { return float32(Tanh(float64(x))) }

func asinhf(x float32) float32 { return float32(Asinh(float64(x))) }

func acoshf(x float32) float32 { return float32(Acosh(float64(x))) }

func atanhf(x float32) float32 { return float32(Atanh(float64(x))) }

func logf(x float32) float32 { return float32(Log(float64(x))) }

func log2f(x float32) float32 { return float32(Log2(float64(x))) }

func log10f(x float32) float32 { return float32(Log10(float64(x))) }

func log1pf(x float32) float32 { return float32(Log1p(float64(x))) }

func logbf(x float32) float32 { return float32(Logb(float64(x))) }

func sqrtf(x float32) float32 { return float32(Sqrt(float64(x))) }

func cbrtf(x float32) float32 { return float32(Cbrt(float64(x))) }

func fabsf(x float32) float32 { return float32(Fabs(float64(x))) }

func floorf(x float32) float32 { return float32(Floor(float64(x))) }

func ceilf(x float32) float32 { return float32(Ceil(float64(x))) }

func roundf(x float32) float32 { return float32(Round(float64(x))) }

func truncf(x float32) float32 { return float32(Trunc(float64(x))) }

func copysignf(x, y float32) float32 { return float32(Copysign(float64(x), float64(y))) }

func fmodf(x, y float32) float32 { return float32(Fmod(float64(x), float64(y))) }

func remainderf(x, y float32) float32 { return float32(Remainder(float64(x), float64(y))) }

func fmaxf(x, y float32) float32 { return float32(Fmax(float64(x), float64(y))) }

func fminf(x, y float32) float32 { return float32(Fmin(float64(x), float64(y))) }

func erff(x float32) float32 { return float32(Erf(float64(x))) }

func erfcf(x float32) float32 { return float32(Erfc(float64(x))) }

func Sinf(x float32) float32 { return sinf(x) }

func Cosf(x float32) float32 { return cosf(x) }

func Tanf(x float32) float32 { return tanf(x) }

func Asinf(x float32) float32 { return asinf(x) }

func Acosf(x float32) float32 { return acosf(x) }

func Atanf(x float32) float32 { return atanf(x) }

func Atan2f(y, x float32) float32 { return atan2f(y, x) }

func Sinhf(x float32) float32 { return rangedf(math.Sinh(float64(x)), x) }

func Coshf(x float32) float32 { return rangedf(math.Cosh(float64(x)), x) }

func Tanhf(x float32) float32 { return tanhf(x) }

func Asinhf(x float32) float32 { return asinhf(x) }

func Acoshf(x float32) float32 { return acoshf(x) }

func Atanhf(x float32) float32 { return atanhf(x) }

func Expf(x float32) float32 { return rangedf(math.Exp(float64(x)), x) }

func Exp2f(x float32) float32 { return rangedf(math.Exp2(float64(x)), x) }

func Expm1f(x float32) float32 { return rangedf(math.Expm1(float64(x)), x) }

func Logf(x float32) float32 { return logf(x) }

func Log2f(x float32) float32 { return log2f(x) }

func Log10f(x float32) float32 { return log10f(x) }

func Log1pf(x float32) float32 { return log1pf(x) }

func Logbf(x float32) float32 { return logbf(x) }

func Ilogbf(x float32) int32 { return Ilogb(float64(x)) }

func Powf(x, y float32) float32 { return rangedf(Pow(float64(x), float64(y)), x, y) }

func Sqrtf(x float32) float32 { return sqrtf(x) }

func Cbrtf(x float32) float32 { return cbrtf(x) }

func Hypotf(x, y float32) float32 { return rangedf(math.Hypot(float64(x), float64(y)), x, y) }

func Fabsf(x float32) float32 { return fabsf(x) }

func Floorf(x float32) float32 { return floorf(x) }

func Ceilf(x float32) float32 { return ceilf(x) }

func Roundf(x float32) float32 { return roundf(x) }

func Truncf(x float32) float32 { return truncf(x) }

func Rintf(x float32) float32 { return float32(c23.RintRaise(float64(x), true)) }

func Lroundf(x float32) int64 { return Lround(float64(x)) }

func Lrintf(x float32) int64 { return Lrint(float64(x)) }

func Copysignf(x, y float32) float32 { return copysignf(x, y) }

func Fmodf(x, y float32) float32 { return fmodf(x, y) }

func Remainderf(x, y float32) float32 {
	return remainderf(x, y)
}

func Fmaxf(x, y float32) float32 { return fmaxf(x, y) }

func Fminf(x, y float32) float32 { return fminf(x, y) }

func Fdimf(x, y float32) float32 {
	return rangedf(Fdim(float64(x), float64(y)), x, y)
}

// Fmaf is fmaf: exact in double, but for double rounding.
func Fmaf(x, y, z float32) float32 {
	if c23.FenvUsed.Load() && c23.FpHasFMA {
		e := c23.Fenv()
		r, st := c23.FpFma32(c23.FpControl(e.Round), x, y, z)
		e.Except |= c23.FpExcepts(st)
		return r
	}
	return float32(math.FMA(float64(x), float64(y), float64(z)))
}

func Erff(x float32) float32 { return erff(x) }

func Erfcf(x float32) float32 { return erfcf(x) }

func Tgammaf(x float32) float32 {
	return rangedf(math.Gamma(float64(x)), x)
}

func Lgammaf(x float32) float32 { return float32(Lgamma(float64(x))) }

func Nextafterf(x, y float32) float32 {
	return nextFlags(x, y, math.Nextafter32(x, y), 0x1p-126)
}

func Ldexpf(x float32, e int32) float32 {
	return rangedf(math.Ldexp(float64(x), int(max(min(e, 1000), -1000))), x)
}

func Scalblnf(x float32, e int64) float32 {
	return rangedf(math.Ldexp(float64(x), int(max(min(e, 1000), -1000))), x)
}

func Frexpf(x float32, e c23.Ptr) float32 { return float32(Frexp(float64(x), e)) }

func Modff(x float32, ip c23.Ptr) float32 {
	if math.IsInf(float64(x), 0) {
		c23.StoreF32(ip, x)
		return float32(math.Copysign(0, float64(x)))
	}
	i, f := math.Modf(float64(x))
	c23.StoreF32(ip, float32(i))
	return float32(f)
}

func Nanf(tag c23.Ptr) float32 { return float32(math.NaN()) }

func Scalbn(x float64, e int32) float64 { return Scalbln(x, int64(e)) }

func Scalbnf(x float32, e int32) float32 { return Scalblnf(x, int64(e)) }
