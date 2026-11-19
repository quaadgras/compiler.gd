// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"math"
	"testing"
)

func TestFenvArith(t *testing.T) {
	if !fpHardware {
		t.Skip("no rounding directions here")
	}
	third := 1.0 / 3 // rounded to nearest: down
	tests := []struct {
		name   string
		f      func(m int32) float64
		m      int32
		want   float64
		except int32
	}{
		{"1/3 nearest", func(m int32) float64 { return Arith('/', 1, 3, m) }, FeToNearest, third, FeInexact},
		{"1/3 down", func(m int32) float64 { return Arith('/', 1, 3, m) }, FeDownward, third, FeInexact},
		{"1/3 up", func(m int32) float64 { return Arith('/', 1, 3, m) }, FeUpward, math.Nextafter(third, 1), FeInexact},
		{"-1/3 zero", func(m int32) float64 { return Arith('/', -1, 3, m) }, FeTowardZero, -third, FeInexact},
		{"-1/3 down", func(m int32) float64 { return Arith('/', -1, 3, m) }, FeDownward, -math.Nextafter(third, 1), FeInexact},
		{"exact", func(m int32) float64 { return Arith('+', 1, 2, m) }, FeUpward, 3, 0},
		{"x-x down", func(m int32) float64 { return Arith('-', 1, 1, m) }, FeDownward, math.Copysign(0, -1), 0},
		{"overflow nearest", func(m int32) float64 { return Arith('*', 1e308, 10, m) }, FeToNearest, math.Inf(1), FeOverflow | FeInexact},
		{"overflow zero", func(m int32) float64 { return Arith('*', 1e308, 10, m) }, FeTowardZero, math.MaxFloat64, FeOverflow | FeInexact},
		{"underflow up", func(m int32) float64 { return Arith('*', 0x1p-1074, 0.5, m) }, FeUpward, 0x1p-1074, FeUnderflow | FeInexact},
		{"underflow nearest", func(m int32) float64 { return Arith('*', 0x1p-1074, 0.5, m) }, FeToNearest, 0, FeUnderflow | FeInexact},
		{"divbyzero", func(m int32) float64 { return Arith('/', 1, 0, m) }, FeToNearest, math.Inf(1), FeDivByZero},
		{"invalid", func(m int32) float64 { return Arith('-', math.Inf(1), math.Inf(1), m) }, FeToNearest, math.NaN(), FeInvalid},
		{"u64 down", func(m int32) float64 { return UintToDouble(1<<64-1, m) }, FeDownward, 0x1.fffffffffffffp63, FeInexact},
		{"u64 up", func(m int32) float64 { return UintToDouble(1<<64-1, m) }, FeUpward, 0x1p64, FeInexact},
		{"i64 up", func(m int32) float64 { return IntToDouble(1<<53+1, m) }, FeUpward, 1<<53 + 2, FeInexact},
		{"int float up", func(m int32) float64 { return float64(IntToFloat(1<<24+1, m)) }, FeUpward, 1<<24 + 2, FeInexact},
		{"narrow down", func(m int32) float64 { return float64(Narrow(0.1, m)) }, FeDownward, float64(math.Nextafter32(0.1, 0)), FeInexact},
		{"narrow up", func(m int32) float64 { return float64(Narrow(0.1, m)) }, FeUpward, float64(float32(0.1)), FeInexact},
		{"float add up", func(m int32) float64 { return float64(Arithf('+', 1, 0x1p-30, m)) }, FeUpward, float64(math.Nextafter32(1, 2)), FeInexact},
		{"to int", func(m int32) float64 { return float64(ToInt(-2.5, 32)) }, FeToNearest, -2, FeInexact},
		{"to int range", func(m int32) float64 { return float64(int32(ToInt(3e9, 32))) }, FeToNearest, math.MinInt32, FeInvalid},
		{"to uint", func(m int32) float64 { return float64(ToUint(-0.5, 32)) }, FeToNearest, 0, FeInexact},
		{"to uint neg", func(m int32) float64 { return float64(ToUint(-1, 8)) }, FeToNearest, math.MaxUint64, FeInvalid},
	}
	for _, tt := range tests {
		Feclearexcept(FeAllExcept)
		got := tt.f(tt.m)
		except := Fetestexcept(FeAllExcept)
		same := got == tt.want && math.Signbit(got) == math.Signbit(tt.want) || math.IsNaN(got) && math.IsNaN(tt.want)
		if !same || except != tt.except {
			t.Errorf("%s: got %x, exceptions %#x; want %x, %#x", tt.name, got, except, tt.want, tt.except)
		}
	}
	// The dynamic direction is the thread's, and new threads inherit it.
	Fesetround(FeUpward)
	defer Fesetround(FeToNearest)
	if got := Arith('/', 1, 3, FeDynamic); got != math.Nextafter(third, 1) {
		t.Errorf("dynamic upward: got %x", got)
	}
	done := make(chan float64)
	fe := inheritFenv()
	go func() {
		gSelf().fe = fe
		done <- Arith('/', 1, 3, FeDynamic)
	}()
	if got := <-done; got != math.Nextafter(third, 1) {
		t.Errorf("inherited upward: got %x", got)
	}
	if FltRounds() != 2 {
		t.Errorf("FLT_ROUNDS upward: got %d", FltRounds())
	}
	if Cmp('<', math.NaN(), 1) || Fetestexcept(FeInvalid) == 0 {
		t.Errorf("NaN < 1: no invalid")
	}
	// fma rounds once, in the thread's direction; fadd narrows once.
	var r, f [4]float64
	for i, m := range []int32{FeDownward, FeUpward, FeTowardZero, FeToNearest} {
		Fesetround(m)
		Feclearexcept(FeAllExcept)
		r[i] = Fma(0.1, 0.1, 0)
		f[i] = float64(Fadd(1, 0x1p-40))
		if Fetestexcept(FeInexact) == 0 {
			t.Errorf("fma, fadd rounding %#x: not inexact", m)
		}
	}
	Fesetround(FeToNearest)
	if r[1] != math.Nextafter(r[0], 1) || r[2] != r[0] || r[3] != r[0] && r[3] != r[1] {
		t.Errorf("fma(0.1, 0.1, 0) down, up, zero, nearest: %x", r)
	}
	if f != [4]float64{1, float64(math.Nextafter32(1, 2)), 1, 1} {
		t.Errorf("fadd(1, 0x1p-40) down, up, zero, nearest: %x", f)
	}
}
