// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

import "unsafe"

// From ../float.go and ../complex.go.

const (
	float64Mask  = 0x7FF
	float64Shift = 64 - 11 - 1
	float64Bias  = 1023
)

var inf = float64frombits(0x7FF0000000000000)

// isNaN reports whether f is an IEEE 754 “not-a-number” value.
func isNaN(f float64) (is bool) {
	// IEEE 754 says that only NaNs satisfy f != f.
	return f != f
}

// isFinite reports whether f is neither NaN nor an infinity.
func isFinite(f float64) bool {
	return !isNaN(f - f)
}

// isInf reports whether f is an infinity.
func isInf(f float64) bool {
	return !isNaN(f) && !isFinite(f)
}

// abs returns the absolute value of x.
//
// Special cases are:
//
//	abs(±Inf) = +Inf
//	abs(NaN) = NaN
func abs(x float64) float64 {
	const sign = 1 << 63
	return float64frombits(float64bits(x) &^ sign)
}

// copysign returns a value with the magnitude
// of x and the sign of y.
func copysign(x, y float64) float64 {
	const sign = 1 << 63
	return float64frombits(float64bits(x)&^sign | float64bits(y)&sign)
}

// float64frombits returns the floating point number corresponding
// the IEEE 754 binary representation b.
func float64frombits(b uint64) float64 {
	return *(*float64)(unsafe.Pointer(&b))
}

// floor returns the greatest integer value less than or equal to x.
//
// Special cases are:
//
//	floor(±0) = ±0
//	floor(±Inf) = ±Inf
//	floor(NaN) = NaN
//
// N.B. Portable floor copied from math. math also has optimized arch-specific
// implementations.
func floor(x float64) float64 {
	if x == 0 || isNaN(x) || isInf(x) {
		return x
	}
	if x < 0 {
		d, fract := modf(-x)
		if fract != 0.0 {
			d = d + 1
		}
		return -d
	}
	d, _ := modf(x)
	return d
}

// ceil returns the least integer value greater than or equal to x.
//
// Special cases are:
//
//	Ceil(±0) = ±0
//	Ceil(±Inf) = ±Inf
//	Ceil(NaN) = NaN
//
// N.B. Portable ceil copied from math. math also has optimized arch-specific
// implementations.
func ceil(x float64) float64 {
	return -floor(-x)
}

// modf returns integer and fractional floating-point numbers
// that sum to f. Both values have the same sign as f.
//
// Special cases are:
//
//	Modf(±Inf) = ±Inf, NaN
//	Modf(NaN) = NaN, NaN
//
// N.B. Portable modf copied from math. math also has optimized arch-specific
// implementations.
func modf(f float64) (int float64, frac float64) {
	if f < 1 {
		switch {
		case f < 0:
			int, frac = modf(-f)
			return -int, -frac
		case f == 0:
			return f, f // Return -0, -0 when f == -0
		}
		return 0, f
	}

	x := float64bits(f)
	e := uint(x>>float64Shift)&float64Mask - float64Bias

	// Keep the top 12+e bits, the integer part; clear the rest.
	if e < 64-12 {
		x &^= 1<<(64-12-e) - 1
	}
	int = float64frombits(x)
	frac = f - int
	return
}
func complex128div(n complex128, m complex128) complex128 {
	var e, f float64 // complex(e, f) = n/m

	// Algorithm for robust complex division as described in
	// Robert L. Smith: Algorithm 116: Complex division. Commun. ACM 5(8): 435 (1962).
	if abs(real(m)) >= abs(imag(m)) {
		ratio := imag(m) / real(m)
		denom := real(m) + ratio*imag(m)
		e = (real(n) + imag(n)*ratio) / denom
		f = (imag(n) - real(n)*ratio) / denom
	} else {
		ratio := real(m) / imag(m)
		denom := imag(m) + ratio*real(m)
		e = (real(n)*ratio + imag(n)) / denom
		f = (imag(n)*ratio - real(n)) / denom
	}

	if isNaN(e) && isNaN(f) {
		// Correct final result to infinities and zeros if applicable.
		// Matches C99: ISO/IEC 9899:1999 - G.5.1  Multiplicative operators.

		a, b := real(n), imag(n)
		c, d := real(m), imag(m)

		switch {
		case m == 0 && (!isNaN(a) || !isNaN(b)):
			e = copysign(inf, c) * a
			f = copysign(inf, c) * b

		case (isInf(a) || isInf(b)) && isFinite(c) && isFinite(d):
			a = inf2one(a)
			b = inf2one(b)
			e = inf * (a*c + b*d)
			f = inf * (b*c - a*d)

		case (isInf(c) || isInf(d)) && isFinite(a) && isFinite(b):
			c = inf2one(c)
			d = inf2one(d)
			e = 0 * (a*c + b*d)
			f = 0 * (b*c - a*d)
		}
	}

	return complex(e, f)
}
func inf2one(f float64) float64 {
	g := 0.0
	if isInf(f) {
		g = 1.0
	}
	return copysign(g, f)
}
