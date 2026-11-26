// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"math"
	"math/rand"
	"testing"
)

// TestAppendFloat compares appendFloat, which converts a number once,
// with formatFloat, which is how printf's conversions are defined here.
func TestAppendFloat(t *testing.T) {
	vals := []float64{0, 1, 0.5, 9.5, 9.9999995, 99999.95, 999999.5, 1e5, 1e6, 123456, 1234567, 0.0001, 0.00001, 0.000099999995,
		1.5e-5, 1e-300, 1e300, math.MaxFloat64, math.SmallestNonzeroFloat64, 3.25, 0.1, 100, 1e15, 1e16, 1e17, 123456789012345678,
		2.5, 0.125, 1.0000001, 0.99999999, 5e-324, 4.35, 1e-4, 9.99995e-5, 1e22, 1e23}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 1500; i++ {
		vals = append(vals, math.Float64frombits(r.Uint64()&^(1<<63)), r.Float64(), r.Float64()*math.Pow(10, float64(r.Intn(40)-20)), float64(r.Intn(100000))/100)
	}
	for _, v := range vals {
		if math.IsInf(v, 0) || math.IsNaN(v) {
			continue
		}
		for _, conv := range []byte("fFeEgG") {
			for _, prec := range []int{-1, 0, 1, 2, 3, 6, 9, 15, 17, 20} {
				for _, alt := range []bool{false, true} {
					want := string(formatFloat(v, conv, prec, alt, false))
					var buf [8]byte
					got := string(appendFloat(buf[:0], v, conv, prec, alt, false))
					if got != want {
						t.Fatalf("appendFloat(%v, %c, %d, alt=%v) = %q, want %q", v, conv, prec, alt, got, want)
					}
				}
			}
		}
	}
}
