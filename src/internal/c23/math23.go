// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import "math"

// The functions C23 adds to math.h (7.12), and remquo and nexttoward. As
// in math.go, long double is double, and a float function computes in
// double and rounds its result to float.

// Llogb is llogb: ilogb, in a long.
func Llogb(x float64) int64 {
	switch {
	case x == 0:
		SetErrno(EDOM)
		return math.MinInt64 // FP_LLOGB0
	case math.IsNaN(x):
		SetErrno(EDOM)
		return math.MinInt64 // FP_LLOGBNAN
	case math.IsInf(x, 0):
		SetErrno(EDOM)
		return math.MaxInt64
	}
	return int64(math.Ilogb(x))
}

// The payload of a NaN is its significand's low bits, below the quiet bit.
const ()

// Llogb32 is llogb where long has 32 bits.
func Llogb32(x float64) int64 { return max(min(Llogb(x), math.MaxInt32), math.MinInt32) }

func Llogbf32(x float32) int64 { return Llogb32(float64(x)) }
