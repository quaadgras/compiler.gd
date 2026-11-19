// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package monetary is monetary.h's functions for cmd/cc, in the C
// locale, the only one the library has.
package monetary

import (
	c23 "internal/c23"
	"strconv"
	"strings"
)

type Ptr = c23.Ptr

func toFloat(a any) float64 {
	switch v := a.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	}
	return float64(c23.ToInt64(a))
}

// strfmon formats the doubles of va as format says, in the C locale: no
// currency symbol and no grouping, a period for the decimal point and
// two decimals unless the precision says otherwise, a - before a
// negative value or, with the ( flag, parentheses around it (and a space
// either side of a positive one).
func strfmon(format string, va []any) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(format); i++ {
		c := format[i]
		if c != '%' {
			b.WriteByte(c)
			continue
		}
		i++
		if i < len(format) && format[i] == '%' {
			b.WriteByte('%')
			continue
		}
		fill, paren, left := byte(' '), false, false
	flags:
		for ; i < len(format); i++ {
			switch format[i] {
			case '=':
				if i+1 >= len(format) {
					return "", false
				}
				i++
				fill = format[i]
			case '^', '+', '!':
			case '(':
				paren = true
			case '-':
				left = true
			default:
				break flags
			}
		}
		num := func() int {
			n := -1
			for i < len(format) && '0' <= format[i] && format[i] <= '9' {
				if n < 0 {
					n = 0
				}
				n = n*10 + int(format[i]-'0')
				i++
			}
			return n
		}
		width := num()
		lprec, rprec := -1, 2
		if i < len(format) && format[i] == '#' {
			i++
			if lprec = num(); lprec < 0 {
				return "", false
			}
		}
		if i < len(format) && format[i] == '.' {
			i++
			if rprec = num(); rprec < 0 {
				return "", false
			}
		}
		if i >= len(format) || format[i] != 'i' && format[i] != 'n' || len(va) == 0 {
			return "", false
		}
		v := toFloat(va[0])
		va = va[1:]
		neg := v < 0 || v == 0 && 1/v < 0
		if neg {
			v = -v
		}
		s := strconv.FormatFloat(v, 'f', rprec, 64)
		if lprec >= 0 {
			ip := s
			if k := strings.IndexByte(s, '.'); k >= 0 {
				ip = s[:k]
			}
			if n := lprec - len(ip); n > 0 {
				s = strings.Repeat(string(fill), n) + s
			}
		}
		switch {
		case paren && neg:
			s = "(" + s + ")"
		case paren:
			s = " " + s + " "
		case neg:
			s = "-" + s
		case lprec >= 0:
			// Room for a sign, so that the values line up.
			s = " " + s
		}
		if n := width - len(s); n > 0 {
			if left {
				s += strings.Repeat(" ", n)
			} else {
				s = strings.Repeat(" ", n) + s
			}
		}
		b.WriteString(s)
	}
	return b.String(), true
}

func store(s Ptr, maxsize uint64, format Ptr, va []any) int64 {
	out, ok := strfmon(c23.GoString(format), va)
	if !ok {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	if uint64(len(out)) >= maxsize {
		c23.SetErrno(c23.E2BIG)
		return -1
	}
	copy(c23.BytesAt(s, uintptr(len(out)+1)), out+"\x00")
	return int64(len(out))
}

// Strfmon is strfmon, in the C locale.
func Strfmon(s Ptr, maxsize uint64, format Ptr, va ...any) int64 {
	return store(s, maxsize, format, va)
}

// StrfmonL is strfmon_l, which is strfmon: the library's locales have
// the same conventions (internal/c23/locale).
func StrfmonL(s Ptr, maxsize uint64, _ Ptr, format Ptr, va ...any) int64 {
	return store(s, maxsize, format, va)
}
