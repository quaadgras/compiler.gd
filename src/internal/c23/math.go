// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import "math"

// math.h. long double is double. A float function computes in double
// and rounds its result to float.

// domain sets errno to EDOM for a domain error, and returns NaN.
func domain() float64 {
	SetErrno(EDOM)
	return math.NaN()
}

// pole sets errno to ERANGE for a pole error, and returns -Inf.
func pole() float64 {
	SetErrno(ERANGE)
	return math.Inf(-1)
}

// ranged sets errno to ERANGE if v overflowed from finite x.
func ranged(v float64, xs ...float64) float64 {
	if math.IsInf(v, 0) {
		for _, x := range xs {
			if math.IsInf(x, 0) || math.IsNaN(x) {
				return v
			}
		}
		SetErrno(ERANGE)
	}
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

func Atan(x float64) float64     { return math.Atan(x) }
func Atan2(y, x float64) float64 { return math.Atan2(y, x) }
func Sinh(x float64) float64     { return ranged(math.Sinh(x), x) }
func Cosh(x float64) float64     { return ranged(math.Cosh(x), x) }
func Tanh(x float64) float64     { return math.Tanh(x) }
func Asinh(x float64) float64    { return math.Asinh(x) }

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
		SetErrno(ERANGE)
	}
	return math.Atanh(x)
}

func Exp(x float64) float64   { return ranged(math.Exp(x), x) }
func Exp2(x float64) float64  { return ranged(math.Exp2(x), x) }
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
		SetErrno(EDOM)
		return math.MinInt32
	case math.IsInf(x, 0):
		SetErrno(EDOM)
		return math.MaxInt32
	}
	return int32(math.Ilogb(x))
}

func Pow(x, y float64) float64 {
	switch {
	case x < 0 && !math.IsInf(x, 0) && y != math.Trunc(y) && !math.IsInf(y, 0):
		return domain()
	case x == 0 && y < 0:
		SetErrno(ERANGE)
	}
	return ranged(math.Pow(x, y), x, y)
}

func Sqrt(x float64) float64 {
	if x < 0 {
		return domain()
	}
	return math.Sqrt(x)
}

func Cbrt(x float64) float64         { return math.Cbrt(x) }
func Hypot(x, y float64) float64     { return ranged(math.Hypot(x, y), x, y) }
func Fabs(x float64) float64         { return math.Abs(x) }
func Floor(x float64) float64        { return math.Floor(x) }
func Ceil(x float64) float64         { return math.Ceil(x) }
func Round(x float64) float64        { return math.Round(x) }
func Trunc(x float64) float64        { return math.Trunc(x) }
func Rint(x float64) float64         { return math.RoundToEven(x) }
func Copysign(x, y float64) float64  { return math.Copysign(x, y) }
func Nextafter(x, y float64) float64 { return math.Nextafter(x, y) }
func Fma(x, y, z float64) float64    { return math.FMA(x, y, z) }
func Erf(x float64) float64          { return math.Erf(x) }
func Erfc(x float64) float64         { return math.Erfc(x) }
func Tgamma(x float64) float64       { return ranged(math.Gamma(x), x) }

func Lgamma(x float64) float64 {
	v, _ := math.Lgamma(x)
	return v
}

// toLong converts x, rounded, to long: LONG_MIN if it does not fit, as
// x86 does, with errno EDOM.
func toLong(x float64) int64 {
	if math.IsNaN(x) || x >= 1<<63 || x < -(1<<63) {
		SetErrno(EDOM)
		return math.MinInt64
	}
	return int64(x)
}

func Lround(x float64) int64 { return toLong(math.Round(x)) }
func Lrint(x float64) int64  { return toLong(math.RoundToEven(x)) }

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

func Frexp(x float64, e Ptr) float64 {
	f, n := math.Frexp(x)
	if math.IsInf(x, 0) || math.IsNaN(x) {
		f, n = x, 0
	}
	Store32(e, uint32(int32(n)))
	return f
}

func Modf(x float64, ip Ptr) float64 {
	if math.IsInf(x, 0) {
		StoreF64(ip, x)
		return math.Copysign(0, x)
	}
	i, f := math.Modf(x)
	StoreF64(ip, i)
	return f
}

// Nan is nan: a quiet NaN, whatever the tag.
func Nan(tag Ptr) float64 { return math.NaN() }

// The classification of math.h's macros. A float is classified as the
// double it converts to: a subnormal float is a normal double, so
// float's macros classify a float by its own range.

const (
	fpNaN       = 0
	fpInfinite  = 1
	fpZero      = 2
	fpSubnormal = 3
	fpNormal    = 4
)

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
func Signbit(x float64) int32 { return B2I(math.Signbit(x)) }

// float variants

func f32(f func(float64) float64) func(float32) float32 {
	return func(x float32) float32 { return float32(f(float64(x))) }
}

func f32f32(f func(float64, float64) float64) func(float32, float32) float32 {
	return func(x, y float32) float32 { return float32(f(float64(x), float64(y))) }
}

// rangedf sets errno to ERANGE if v, rounded to float, overflowed.
func rangedf(v float64, xs ...float32) float32 {
	r := float32(v)
	if math.IsInf(float64(r), 0) && !math.IsInf(v, 0) {
		for _, x := range xs {
			if math.IsInf(float64(x), 0) || math.IsNaN(float64(x)) {
				return r
			}
		}
		SetErrno(ERANGE)
	}
	return r
}

var (
	sinf       = f32(Sin)
	cosf       = f32(Cos)
	tanf       = f32(Tan)
	asinf      = f32(Asin)
	acosf      = f32(Acos)
	atanf      = f32(Atan)
	atan2f     = f32f32(Atan2)
	tanhf      = f32(Tanh)
	asinhf     = f32(Asinh)
	acoshf     = f32(Acosh)
	atanhf     = f32(Atanh)
	logf       = f32(Log)
	log2f      = f32(Log2)
	log10f     = f32(Log10)
	log1pf     = f32(Log1p)
	logbf      = f32(Logb)
	sqrtf      = f32(Sqrt)
	cbrtf      = f32(Cbrt)
	fabsf      = f32(Fabs)
	floorf     = f32(Floor)
	ceilf      = f32(Ceil)
	roundf     = f32(Round)
	truncf     = f32(Trunc)
	rintf      = f32(Rint)
	copysignf  = f32f32(Copysign)
	fmodf      = f32f32(Fmod)
	remainderf = f32f32(Remainder)
	fmaxf      = f32f32(Fmax)
	fminf      = f32f32(Fmin)
	erff       = f32(Erf)
	erfcf      = f32(Erfc)
)

func Sinf(x float32) float32         { return sinf(x) }
func Cosf(x float32) float32         { return cosf(x) }
func Tanf(x float32) float32         { return tanf(x) }
func Asinf(x float32) float32        { return asinf(x) }
func Acosf(x float32) float32        { return acosf(x) }
func Atanf(x float32) float32        { return atanf(x) }
func Atan2f(y, x float32) float32    { return atan2f(y, x) }
func Sinhf(x float32) float32        { return rangedf(math.Sinh(float64(x)), x) }
func Coshf(x float32) float32        { return rangedf(math.Cosh(float64(x)), x) }
func Tanhf(x float32) float32        { return tanhf(x) }
func Asinhf(x float32) float32       { return asinhf(x) }
func Acoshf(x float32) float32       { return acoshf(x) }
func Atanhf(x float32) float32       { return atanhf(x) }
func Expf(x float32) float32         { return rangedf(math.Exp(float64(x)), x) }
func Exp2f(x float32) float32        { return rangedf(math.Exp2(float64(x)), x) }
func Expm1f(x float32) float32       { return rangedf(math.Expm1(float64(x)), x) }
func Logf(x float32) float32         { return logf(x) }
func Log2f(x float32) float32        { return log2f(x) }
func Log10f(x float32) float32       { return log10f(x) }
func Log1pf(x float32) float32       { return log1pf(x) }
func Logbf(x float32) float32        { return logbf(x) }
func Ilogbf(x float32) int32         { return Ilogb(float64(x)) }
func Powf(x, y float32) float32      { return rangedf(Pow(float64(x), float64(y)), x, y) }
func Sqrtf(x float32) float32        { return sqrtf(x) }
func Cbrtf(x float32) float32        { return cbrtf(x) }
func Hypotf(x, y float32) float32    { return rangedf(math.Hypot(float64(x), float64(y)), x, y) }
func Fabsf(x float32) float32        { return fabsf(x) }
func Floorf(x float32) float32       { return floorf(x) }
func Ceilf(x float32) float32        { return ceilf(x) }
func Roundf(x float32) float32       { return roundf(x) }
func Truncf(x float32) float32       { return truncf(x) }
func Rintf(x float32) float32        { return rintf(x) }
func Lroundf(x float32) int64        { return Lround(float64(x)) }
func Lrintf(x float32) int64         { return Lrint(float64(x)) }
func Copysignf(x, y float32) float32 { return copysignf(x, y) }
func Fmodf(x, y float32) float32     { return fmodf(x, y) }
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
	return float32(math.FMA(float64(x), float64(y), float64(z)))
}
func Erff(x float32) float32  { return erff(x) }
func Erfcf(x float32) float32 { return erfcf(x) }
func Tgammaf(x float32) float32 {
	return rangedf(math.Gamma(float64(x)), x)
}
func Lgammaf(x float32) float32 { return float32(Lgamma(float64(x))) }
func Nextafterf(x, y float32) float32 {
	return math.Nextafter32(x, y)
}
func Ldexpf(x float32, e int32) float32 {
	return rangedf(math.Ldexp(float64(x), int(max(min(e, 1000), -1000))), x)
}
func Scalblnf(x float32, e int64) float32 {
	return rangedf(math.Ldexp(float64(x), int(max(min(e, 1000), -1000))), x)
}

func Frexpf(x float32, e Ptr) float32 { return float32(Frexp(float64(x), e)) }

func Modff(x float32, ip Ptr) float32 {
	if math.IsInf(float64(x), 0) {
		StoreF32(ip, x)
		return float32(math.Copysign(0, float64(x)))
	}
	i, f := math.Modf(float64(x))
	StoreF32(ip, float32(i))
	return float32(f)
}

func Nanf(tag Ptr) float32 { return float32(math.NaN()) }

func Scalbn(x float64, e int32) float64  { return Scalbln(x, int64(e)) }
func Scalbnf(x float32, e int32) float32 { return Scalblnf(x, int64(e)) }
