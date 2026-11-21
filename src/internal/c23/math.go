// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import "math"

// math.h. long double is double. A float function computes in double
// and rounds its result to float.

// raiseUsed raises excepts if a thread has used the floating-point
// environment: no one else can see them (Annex F's exceptions for the
// library's special cases, besides errno).
func RaiseUsed(excepts int32) {
	if FenvUsed.Load() {
		Raise(excepts)
	}
}

func Rint(x float64) float64 { return RintRaise(x, true) }

// rint rounds x to an integer in the thread's direction, raising inexact
// if it was not one and if raise (rint does, nearbyint does not).
func RintRaise(x float64, raise bool) float64 {
	r := RoundInt(x, CurRound())
	if raise && r != x && !math.IsNaN(x) {
		RaiseUsed(FeInexact)
	}
	return r
}

// roundInt rounds x to an integer in direction d.
func RoundInt(x float64, d int32) float64 {
	switch d {
	case FeDownward:
		return math.Floor(x)
	case FeUpward:
		return math.Ceil(x)
	case FeTowardZero:
		return math.Trunc(x)
	}
	return math.RoundToEven(x)
}

// The classification of math.h's macros. A float is classified as the
// double it converts to: a subnormal float is a normal double, so
// float's macros classify a float by its own range.

const ()

// float variants

func rintf(x float32) float32 { return float32(Rint(float64(x))) }
