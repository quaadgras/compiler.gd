// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

import (
	"internal/c23"
	"math"
	"testing"
)

// TestNarrowRounding checks fma and fadd in each rounding direction.
func TestNarrowRounding(t *testing.T) {
	if !c23.FpHardware {
		t.Skip("no rounding directions here")
	}
	// fma rounds once, in the thread's direction; fadd narrows once.
	var r, f [4]float64
	for i, m := range []int32{c23.FeDownward, c23.FeUpward, c23.FeTowardZero, c23.FeToNearest} {
		c23.Fesetround(m)
		c23.Feclearexcept(c23.FeAllExcept)
		r[i] = Fma(0.1, 0.1, 0)
		f[i] = float64(Fadd(1, 0x1p-40))
		if c23.Fetestexcept(c23.FeInexact) == 0 {
			t.Errorf("fma, fadd rounding %#x: not inexact", m)
		}
	}
	c23.Fesetround(c23.FeToNearest)
	if r[1] != math.Nextafter(r[0], 1) || r[2] != r[0] || r[3] != r[0] && r[3] != r[1] {
		t.Errorf("fma(0.1, 0.1, 0) down, up, zero, nearest: %x", r)
	}
	if f != [4]float64{1, float64(math.Nextafter32(1, 2)), 1, 1} {
		t.Errorf("fadd(1, 0x1p-40) down, up, zero, nearest: %x", f)
	}
}
