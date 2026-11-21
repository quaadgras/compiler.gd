// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

import "math"

// The Bessel functions of the first and second kinds (XSI), Go's; y0, y1
// and yn of a negative number are domain errors and of zero pole errors,
// as glibc's.

func J0(x float64) float64 { return math.J0(x) }

func J1(x float64) float64 { return math.J1(x) }

func Jn(n int32, x float64) float64 { return math.Jn(int(n), x) }

// yDomain reports y's errors at x, returning y.
func yDomain(x, y float64) float64 {
	switch {
	case x < 0:
		return domain()
	case x == 0:
		return pole()
	}
	return y
}

func Y0(x float64) float64 { return yDomain(x, math.Y0(x)) }

func Y1(x float64) float64 { return yDomain(x, math.Y1(x)) }

func Yn(n int32, x float64) float64 { return yDomain(x, math.Yn(int(n), x)) }

func J0f(x float32) float32 { return float32(J0(float64(x))) }

func J1f(x float32) float32 { return float32(J1(float64(x))) }

func Jnf(n int32, x float32) float32 { return float32(Jn(n, float64(x))) }

func Y0f(x float32) float32 { return float32(Y0(float64(x))) }

func Y1f(x float32) float32 { return float32(Y1(float64(x))) }

func Ynf(n int32, x float32) float32 { return float32(Yn(n, float64(x))) }
