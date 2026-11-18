// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Conversions between binary and decimal honor the thread's rounding
// direction (C23 F.5): strconv's round to nearest; in the others, the exact
// value is rounded here.

// fmtFloat is strconv.FormatFloat(v, fmt, prec, 64), for fmt 'e', 'f' or
// 'x', rounded in the thread's direction: as a negative number's if
// negated (printf formats the magnitude).
func fmtFloat(v float64, fmt byte, prec int, negated bool) string {
	d := curRound()
	if d == FeToNearest || prec < 0 || math.IsInf(v, 0) || math.IsNaN(v) {
		return strconv.FormatFloat(v, fmt, prec, 64)
	}
	neg := math.Signbit(v) != negated
	up := d == FeUpward && !neg || d == FeDownward && neg // away from zero
	if fmt == 'x' {
		// To 1+4*prec bits, the hexadecimal digits, then exactly.
		if n := 1 + 4*prec; n < 53 && v != 0 {
			m, e := math.Frexp(math.Abs(v))
			s := math.Ldexp(m, n)
			r := math.Trunc(s)
			if up && r != s {
				r++
			}
			v = math.Copysign(math.Ldexp(r, e-n), v)
		}
		return strconv.FormatFloat(v, 'x', prec, 64)
	}
	// v's exact digits: it has at most 1074 after the point, 767
	// significant.
	b := new(big.Float).SetFloat64(math.Abs(v))
	sign := ""
	if math.Signbit(v) {
		sign = "-"
	}
	if fmt == 'f' {
		s := b.Text('f', 1100)
		dot := strings.IndexByte(s, '.')
		ds, carry := roundDigits(s[:dot]+s[dot+1:], dot+prec, up)
		if carry {
			ds, dot = "1"+ds, dot+1
		}
		if prec == 0 {
			return sign + ds
		}
		return sign + ds[:dot] + "." + ds[dot:]
	}
	s := b.Text('e', 800)
	i := strings.IndexByte(s, 'e')
	exp, _ := strconv.Atoi(s[i+1:])
	ds, carry := roundDigits(s[:1]+s[2:i], 1+prec, up)
	if carry {
		ds, exp = ("1" + ds)[:1+prec], exp+1
	}
	m := ds[:1]
	if prec > 0 {
		m += "." + ds[1:]
	}
	es := "+"
	if exp < 0 {
		es, exp = "-", -exp
	}
	if exp < 10 {
		es += "0"
	}
	return sign + m + "e" + es + strconv.Itoa(exp)
}

// roundDigits keeps the first n digits of ds, adding one in the last place
// if up and any digit after is not 0; carry reports that all were 9s, now
// 0s, with a 1 to go before them.
func roundDigits(ds string, n int, up bool) (string, bool) {
	keep := []byte(ds[:n])
	if !up || strings.Trim(ds[n:], "0") == "" {
		return string(keep), false
	}
	for i := n - 1; i >= 0; i-- {
		if keep[i] != '9' {
			keep[i]++
			return string(keep), false
		}
		keep[i] = '0'
	}
	return string(keep), true
}

// parseFloat is strconv.ParseFloat(s, bits) rounded in the thread's
// direction.
func parseFloat(s string, bits int) (float64, error) {
	f, err := strconv.ParseFloat(s, bits)
	if err != nil && err.(*strconv.NumError).Err != strconv.ErrRange || math.IsNaN(f) {
		return f, err
	}
	d := curRound()
	if d == FeToNearest {
		return f, err
	}
	x, ok := new(big.Rat).SetString(s)
	if !ok {
		return f, err // "inf"
	}
	return roundTo(f, x, bits, d), err
}

// roundTo is x rounded in direction d to a float of bits bits, given f,
// which is x rounded to nearest: the number before or after f, or f.
func roundTo(f float64, x *big.Rat, bits int, d int32) float64 {
	next := func(toward float64) float64 {
		if bits == 32 {
			return float64(math.Nextafter32(float32(f), float32(toward)))
		}
		return math.Nextafter(f, toward)
	}
	neg := x.Sign() < 0
	if math.IsInf(f, 0) {
		// x is beyond the largest finite: toward zero, the largest.
		if d == FeTowardZero || d == FeDownward && !neg || d == FeUpward && neg {
			return next(0)
		}
		return f
	}
	switch c := new(big.Rat).SetFloat64(f).Cmp(x); {
	case c < 0 && (d == FeUpward || d == FeTowardZero && neg):
		return next(math.Inf(1))
	case c > 0 && (d == FeDownward || d == FeTowardZero && !neg):
		return next(math.Inf(-1))
	}
	return f
}
