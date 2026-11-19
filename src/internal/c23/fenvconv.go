// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"math"
	"strconv"
	"strings"
)

// Conversions between binary and decimal honor the thread's rounding
// direction (C23 F.5): strconv's round to nearest; in the others, the exact
// value is rounded here. Exact digits come from strconv too (a double has
// at most 767 significant decimal digits, 1074 after the point), so the
// library needs no math/big.

// fmtFloat is strconv.FormatFloat(v, fmt, prec, 64), for fmt 'e', 'f' or
// 'x', rounded in the thread's direction: as a negative number's if
// negated (printf formats the magnitude).
func fmtFloat(v float64, fmt byte, prec int, negated bool) string {
	d := CurRound()
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
	sign := ""
	if math.Signbit(v) {
		sign = "-"
	}
	if fmt == 'f' {
		s := strconv.FormatFloat(math.Abs(v), 'f', 1100, 64) // exact
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
	s := strconv.FormatFloat(math.Abs(v), 'e', 800, 64) // exact
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
// direction: strconv's result, rounded to nearest, or the number next to
// it on the side of s's exact value.
func ParseFloat(s string, bits int) (float64, error) {
	f, err := strconv.ParseFloat(s, bits)
	overflow := err != nil && err.(*strconv.NumError).Err == strconv.ErrRange
	if err != nil && !overflow || math.IsNaN(f) {
		return f, err
	}
	d := CurRound()
	if d == FeToNearest || math.IsInf(f, 0) && !overflow {
		return f, err // an infinity spelled out is exact
	}
	next := func(toward float64) float64 {
		if bits == 32 {
			return float64(math.Nextafter32(float32(f), float32(toward)))
		}
		return math.Nextafter(f, toward)
	}
	neg := strings.HasPrefix(s, "-")
	if overflow {
		// Beyond the largest finite: toward zero, the largest.
		if d == FeTowardZero || d == FeDownward && !neg || d == FeUpward && neg {
			return next(0), err
		}
		return f, err
	}
	switch c := cmpExact(s, f); {
	case c > 0 && (d == FeUpward || d == FeTowardZero && neg):
		return next(math.Inf(1)), err
	case c < 0 && (d == FeDownward || d == FeTowardZero && !neg):
		return next(math.Inf(-1)), err
	}
	return f, err
}

// cmpExact compares the number s spells, decimal or hexadecimal as
// strconv.ParseFloat takes it, with f, which has its sign: -1, 0 or +1 as
// it is less than f, the same, or greater.
func cmpExact(s string, f float64) int {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimLeft(s, "+-")
	var c int
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		// In binary: f's 53 bits against s's.
		m, e := math.Frexp(math.Abs(f))
		fd := strconv.FormatUint(uint64(math.Ldexp(m, 53)), 2)
		sd, se := digitsOf(s[2:], 16, 'p')
		c = cmpDigits(sd, se, fd, e)
	} else {
		sd, se := digitsOf(s, 10, 'e')
		ed := strconv.FormatFloat(math.Abs(f), 'e', 800, 64) // exact
		i := strings.IndexByte(ed, 'e')
		fe, _ := strconv.Atoi(ed[i+1:])
		c = cmpDigits(sd, se, ed[:1]+ed[2:i], fe+1)
	}
	if neg {
		return -c
	}
	return c
}

// digitsOf reads the mantissa and exponent of a number in base 10 or 16
// (whose exponent, after expChar, is of 2), as digits of 0.ddd... (base 10)
// or bits of 0.bbb... (base 16) times base to the power exp.
func digitsOf(s string, base int, expChar byte) (digits string, exp int) {
	mant := s
	if i := strings.IndexAny(s, string(expChar)+strings.ToUpper(string(expChar))); i >= 0 {
		mant = s[:i]
		exp, _ = strconv.Atoi(s[i+1:])
	}
	var b strings.Builder
	point := -1
	for i := 0; i < len(mant); i++ {
		c := mant[i]
		if c == '.' {
			point = b.Len()
			continue
		}
		if base == 16 {
			v, _ := strconv.ParseUint(string(c), 16, 8)
			b.WriteString(strconv.FormatUint(v|16, 2)[1:]) // four bits
		} else {
			b.WriteByte(c)
		}
	}
	ds := b.String()
	if point < 0 {
		point = len(ds)
	}
	// 0.ddd... times base**(point): the point is before the first digit.
	exp += point
	t := strings.TrimLeft(ds, "0")
	exp -= len(ds) - len(t)
	return strings.TrimRight(t, "0"), exp
}

// cmpDigits compares 0.a × B^ae with 0.b × B^be, a and b digits (or bits)
// with no zeros leading or trailing.
func cmpDigits(a string, ae int, b string, be int) int {
	b = strings.TrimRight(strings.TrimLeft(b, "0"), "0")
	switch {
	case a == "" && b == "":
		return 0
	case a == "":
		return -1
	case b == "":
		return 1
	case ae != be:
		if ae > be {
			return 1
		}
		return -1
	}
	return strings.Compare(a, b) // the same length's prefix first, then longer
}
