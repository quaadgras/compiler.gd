// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package complex

import (
	"math"
	"math/cmplx"
)

func Creal(z complex128) float64 { return real(z) }

func Cimag(z complex128) float64 { return imag(z) }

func Conj(z complex128) complex128 { return cmplx.Conj(z) }

func Cabs(z complex128) float64 { return cmplx.Abs(z) }

func Carg(z complex128) float64 { return cmplx.Phase(z) }

func Cexp(z complex128) complex128 { return cmplx.Exp(z) }

func Clog(z complex128) complex128 { return cmplx.Log(z) }

func Csqrt(z complex128) complex128 { return cmplx.Sqrt(z) }

func Cpow(z, w complex128) complex128 { return cmplx.Pow(z, w) }

func Csin(z complex128) complex128 { return cmplx.Sin(z) }

func Ccos(z complex128) complex128 { return cmplx.Cos(z) }

func Ctan(z complex128) complex128 { return cmplx.Tan(z) }

func Casin(z complex128) complex128 { return cmplx.Asin(z) }

func Cacos(z complex128) complex128 { return cmplx.Acos(z) }

func Catan(z complex128) complex128 { return cmplx.Atan(z) }

func Csinh(z complex128) complex128 { return cmplx.Sinh(z) }

func Ccosh(z complex128) complex128 { return cmplx.Cosh(z) }

func Ctanh(z complex128) complex128 { return cmplx.Tanh(z) }

func Casinh(z complex128) complex128 { return cmplx.Asinh(z) }

func Cacosh(z complex128) complex128 { return cmplx.Acosh(z) }

func Catanh(z complex128) complex128 { return cmplx.Atanh(z) }

func Cproj(z complex128) complex128 {
	if math.IsInf(real(z), 0) || math.IsInf(imag(z), 0) {
		return complex(math.Inf(1), math.Copysign(0, imag(z)))
	}
	return z
}

func Crealf(z complex64) float32 { return real(z) }

func Cimagf(z complex64) float32 { return imag(z) }

func Conjf(z complex64) complex64 { return complex(real(z), -imag(z)) }

func Cabsf(z complex64) float32 { return float32(cmplx.Abs(complex128(z))) }

func Cargf(z complex64) float32 { return float32(cmplx.Phase(complex128(z))) }

func Cexpf(z complex64) complex64 { return complex64(cmplx.Exp(complex128(z))) }

func Clogf(z complex64) complex64 { return complex64(cmplx.Log(complex128(z))) }

func Csqrtf(z complex64) complex64 { return complex64(cmplx.Sqrt(complex128(z))) }

func Cpowf(z, w complex64) complex64 { return complex64(cmplx.Pow(complex128(z), complex128(w))) }

func Csinf(z complex64) complex64 { return complex64(cmplx.Sin(complex128(z))) }

func Ccosf(z complex64) complex64 { return complex64(cmplx.Cos(complex128(z))) }

func Ctanf(z complex64) complex64 { return complex64(Ctan(complex128(z))) }

func Casinf(z complex64) complex64 { return complex64(Casin(complex128(z))) }

func Cacosf(z complex64) complex64 { return complex64(Cacos(complex128(z))) }

func Catanf(z complex64) complex64 { return complex64(Catan(complex128(z))) }

func Csinhf(z complex64) complex64 { return complex64(Csinh(complex128(z))) }

func Ccoshf(z complex64) complex64 { return complex64(Ccosh(complex128(z))) }

func Ctanhf(z complex64) complex64 { return complex64(Ctanh(complex128(z))) }

func Casinhf(z complex64) complex64 { return complex64(Casinh(complex128(z))) }

func Cacoshf(z complex64) complex64 { return complex64(Cacosh(complex128(z))) }

func Catanhf(z complex64) complex64 { return complex64(Catanh(complex128(z))) }

func Cprojf(z complex64) complex64 { return complex64(Cproj(complex128(z))) }
