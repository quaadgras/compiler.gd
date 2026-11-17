// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"math"
	"math/cmplx"
)

// C's complex types, as Go's: float _Complex is complex64, double and
// long double _Complex complex128 (long double's in x86-64's 32 bytes).

func LoadC64(p Ptr) complex64 {
	return complex(LoadF32(p), LoadF32(Add(p, 4)))
}

func LoadC128(p Ptr) complex128 {
	return complex(LoadF64(p), LoadF64(Add(p, 8)))
}

func LoadCL(p Ptr) complex128 {
	return complex(LoadF64(p), LoadF64(Add(p, 16)))
}

func StoreC64(p Ptr, v complex64) {
	StoreF32(p, real(v))
	StoreF32(Add(p, 4), imag(v))
}

func StoreC128(p Ptr, v complex128) {
	StoreF64(p, real(v))
	StoreF64(Add(p, 8), imag(v))
}

func StoreCL(p Ptr, v complex128) {
	StoreF64(p, real(v))
	StoreF64(Add(p, 16), imag(v))
}

// complex.h

func Creal(z complex128) float64      { return real(z) }
func Cimag(z complex128) float64      { return imag(z) }
func Conj(z complex128) complex128    { return cmplx.Conj(z) }
func Cabs(z complex128) float64       { return cmplx.Abs(z) }
func Carg(z complex128) float64       { return cmplx.Phase(z) }
func Cexp(z complex128) complex128    { return cmplx.Exp(z) }
func Clog(z complex128) complex128    { return cmplx.Log(z) }
func Csqrt(z complex128) complex128   { return cmplx.Sqrt(z) }
func Cpow(z, w complex128) complex128 { return cmplx.Pow(z, w) }
func Csin(z complex128) complex128    { return cmplx.Sin(z) }
func Ccos(z complex128) complex128    { return cmplx.Cos(z) }
func Ctan(z complex128) complex128    { return cmplx.Tan(z) }
func Casin(z complex128) complex128   { return cmplx.Asin(z) }
func Cacos(z complex128) complex128   { return cmplx.Acos(z) }
func Catan(z complex128) complex128   { return cmplx.Atan(z) }
func Csinh(z complex128) complex128   { return cmplx.Sinh(z) }
func Ccosh(z complex128) complex128   { return cmplx.Cosh(z) }
func Ctanh(z complex128) complex128   { return cmplx.Tanh(z) }
func Casinh(z complex128) complex128  { return cmplx.Asinh(z) }
func Cacosh(z complex128) complex128  { return cmplx.Acosh(z) }
func Catanh(z complex128) complex128  { return cmplx.Atanh(z) }

func Cproj(z complex128) complex128 {
	if math.IsInf(real(z), 0) || math.IsInf(imag(z), 0) {
		return complex(math.Inf(1), math.Copysign(0, imag(z)))
	}
	return z
}

func Crealf(z complex64) float32     { return real(z) }
func Cimagf(z complex64) float32     { return imag(z) }
func Conjf(z complex64) complex64    { return complex(real(z), -imag(z)) }
func Cabsf(z complex64) float32      { return float32(cmplx.Abs(complex128(z))) }
func Cargf(z complex64) float32      { return float32(cmplx.Phase(complex128(z))) }
func Cexpf(z complex64) complex64    { return complex64(cmplx.Exp(complex128(z))) }
func Clogf(z complex64) complex64    { return complex64(cmplx.Log(complex128(z))) }
func Csqrtf(z complex64) complex64   { return complex64(cmplx.Sqrt(complex128(z))) }
func Cpowf(z, w complex64) complex64 { return complex64(cmplx.Pow(complex128(z), complex128(w))) }
func Csinf(z complex64) complex64    { return complex64(cmplx.Sin(complex128(z))) }
func Ccosf(z complex64) complex64    { return complex64(cmplx.Cos(complex128(z))) }
