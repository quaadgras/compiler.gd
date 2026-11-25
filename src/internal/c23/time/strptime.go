// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package time

import (
	c23 "internal/c23"
	"strings"
	"time"
)

// Strptime is strptime, as glibc's in the C locale: it converts the
// string at buf as format says into the struct tm at p, setting only the
// members the string gives (and, as glibc does, the day of the week and
// of the year from a date), and returns where it stopped, or null if the
// string does not match.
func Strptime(buf, format, p *c23.Void) *c23.Void {
	st := &ptime{tm: p, century: -1}
	s := c23.GoString(buf)
	rest, ok := st.parse(s, c23.GoString(format))
	if !ok {
		return (*c23.Void)(nil)
	}
	st.finish()
	return c23.Add(buf, len(s)-len(rest))
}

// ptime is strptime's state, which the conversions that are formats
// (%c, %D, %T and the like) share.
type ptime struct {
	tm                                    *c23.Void
	haveI, isPM                           bool
	century                               int
	wantCentury, wantXday                 bool
	haveWday, haveYday, haveMon, haveMday bool
	haveUweek, haveWweek                  bool
	weekNo                                int
}

func isSpace(c byte) bool { return c == ' ' || c >= '\t' && c <= '\r' }

func skipSpace(s string) string {
	for len(s) > 0 && isSpace(s[0]) {
		s = s[1:]
	}
	return s
}

// number is glibc's get_number: after spaces, digits while the value
// stays at most hi (and at most n of them), from lo to hi.
func number(s string, lo, hi, n int) (int, string, bool) {
	s = skipSpace(s)
	if len(s) == 0 || s[0] < '0' || s[0] > '9' {
		return 0, s, false
	}
	v := 0
	for {
		v = v*10 + int(s[0]-'0')
		s = s[1:]
		n--
		if n <= 0 || v*10 > hi || len(s) == 0 || s[0] < '0' || s[0] > '9' {
			break
		}
	}
	return v, s, v >= lo && v <= hi
}

// name matches one of names (full, then abbreviated to 3), ignoring case.
func name(s string, names []string) (int, string, bool) {
	for i, n := range names {
		for _, m := range []string{n, n[:3]} {
			if len(s) >= len(m) && strings.EqualFold(s[:len(m)], m) {
				return i, s[len(m):], true
			}
		}
	}
	return 0, s, false
}

func (st *ptime) set(i, v int)  { tmSet(st.tm, i, v) }
func (st *ptime) get(i int) int { return tmGet(st.tm, i) }

func (st *ptime) parse(s, f string) (string, bool) {
	for len(f) > 0 {
		if isSpace(f[0]) {
			s = skipSpace(s)
			f = f[1:]
			continue
		}
		if f[0] != '%' {
			if len(s) == 0 || s[0] != f[0] {
				return s, false
			}
			s, f = s[1:], f[1:]
			continue
		}
		f = f[1:]
		// strftime's flags and widths, and the E and O modifiers.
		for len(f) > 0 && strings.IndexByte("-_0^#", f[0]) >= 0 {
			f = f[1:]
		}
		for len(f) > 0 && f[0] >= '0' && f[0] <= '9' {
			f = f[1:]
		}
		if len(f) > 0 && (f[0] == 'E' || f[0] == 'O') {
			f = f[1:]
		}
		if len(f) == 0 {
			return s, false
		}
		c := f[0]
		f = f[1:]
		var v int
		ok := true
		sub := func(format string) {
			s, ok = st.parse(s, format)
		}
		switch c {
		case '%':
			if len(s) == 0 || s[0] != '%' {
				return s, false
			}
			s = s[1:]
		case 'a', 'A':
			v, s, ok = name(s, dayNames[:])
			st.set(tmWday, v)
			st.haveWday = true
		case 'b', 'B', 'h':
			v, s, ok = name(s, monthNames[:])
			st.set(tmMon, v)
			st.haveMon, st.wantXday = true, true
		case 'c':
			sub("%a %b %e %H:%M:%S %Y")
			st.wantXday = true
		case 'C':
			v, s, ok = number(s, 0, 99, 2)
			st.century, st.wantXday = v, true
		case 'd', 'e':
			v, s, ok = number(s, 1, 31, 2)
			st.set(tmMday, v)
			st.haveMday, st.wantXday = true, true
		case 'F':
			sub("%Y-%m-%d")
			st.wantXday = true
		case 'x', 'D':
			sub("%m/%d/%y")
			st.wantXday = true
		case 'k', 'H':
			v, s, ok = number(s, 0, 23, 2)
			st.set(tmHour, v)
			st.haveI = false
		case 'l', 'I':
			v, s, ok = number(s, 1, 12, 2)
			st.set(tmHour, v%12)
			st.haveI = true
		case 'j':
			v, s, ok = number(s, 1, 366, 3)
			st.set(tmYday, v-1)
			st.haveYday = true
		case 'm':
			v, s, ok = number(s, 1, 12, 2)
			st.set(tmMon, v-1)
			st.haveMon, st.wantXday = true, true
		case 'M':
			v, s, ok = number(s, 0, 59, 2)
			st.set(tmMin, v)
		case 'n', 't':
			s = skipSpace(s)
		case 'p':
			switch {
			case len(s) >= 2 && strings.EqualFold(s[:2], "AM"):
				st.isPM = false
			case len(s) >= 2 && strings.EqualFold(s[:2], "PM"):
				st.isPM = true
			default:
				return s, false
			}
			s = s[2:]
		case 'r':
			sub("%I:%M:%S %p")
		case 'R':
			sub("%H:%M")
		case 's':
			neg := len(s) > 0 && s[0] == '-'
			if neg {
				s = s[1:]
			}
			if len(s) == 0 || s[0] < '0' || s[0] > '9' {
				return s, false
			}
			var secs int64
			for len(s) > 0 && s[0] >= '0' && s[0] <= '9' {
				secs = secs*10 + int64(s[0]-'0')
				s = s[1:]
			}
			if neg {
				secs = -secs
			}
			ok = setTm(st.tm, time.Unix(secs, 0).In(local()))
		case 'S':
			v, s, ok = number(s, 0, 61, 2)
			st.set(tmSec, v)
		case 'X', 'T':
			sub("%H:%M:%S")
		case 'u':
			v, s, ok = number(s, 1, 7, 1)
			st.set(tmWday, v%7)
			st.haveWday = true
		case 'g':
			_, s, ok = number(s, 0, 99, 2)
		case 'G':
			if len(s) == 0 || s[0] < '0' || s[0] > '9' {
				return s, false
			}
			for len(s) > 0 && s[0] >= '0' && s[0] <= '9' {
				s = s[1:]
			}
		case 'U', 'W':
			v, s, ok = number(s, 0, 53, 2)
			st.weekNo = v
			if c == 'U' {
				st.haveUweek = true
			} else {
				st.haveWweek = true
			}
		case 'V':
			_, s, ok = number(s, 0, 53, 2)
		case 'w':
			v, s, ok = number(s, 0, 6, 1)
			st.set(tmWday, v)
			st.haveWday = true
		case 'y':
			v, s, ok = number(s, 0, 99, 2)
			if v < 69 {
				v += 100
			}
			st.set(tmYear, v)
			st.wantCentury, st.wantXday = true, true
		case 'Y':
			v, s, ok = number(s, 0, 9999, 4)
			st.set(tmYear, v-1900)
			st.wantCentury, st.wantXday = false, true
		case 'Z': // a zone's name, which is not used
			s = skipSpace(s)
			for len(s) > 0 && !isSpace(s[0]) {
				s = s[1:]
			}
		case 'z': // an offset, which struct tm has no member for
			s = skipSpace(s)
			if len(s) > 0 && s[0] == 'Z' {
				s = s[1:]
				break
			}
			if len(s) == 0 || s[0] != '+' && s[0] != '-' {
				return s, false
			}
			s = s[1:]
			n := 0
			for n < 4 && len(s) > 0 && (s[0] >= '0' && s[0] <= '9' || s[0] == ':' && n == 2) {
				if s[0] != ':' {
					n++
				}
				s = s[1:]
			}
			if n != 2 && n != 4 {
				return s, false
			}
		default:
			return s, false
		}
		if !ok {
			return s, false
		}
	}
	return s, true
}

func isLeap(y int) bool { return y%4 == 0 && (y%100 != 0 || y%400 == 0) }

// monYday are the days before each month (and the year's).
var monYday = [2][13]int{
	{0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334, 365},
	{0, 31, 60, 91, 121, 152, 182, 213, 244, 274, 305, 335, 366},
}

func (st *ptime) leap() int { return int(c23.B2I(isLeap(st.get(tmYear) + 1900))) }

// dayOfWeek sets tm_wday from the date.
func (st *ptime) dayOfWeek() {
	t := time.Date(st.get(tmYear)+1900, time.Month(st.get(tmMon)+1), st.get(tmMday), 0, 0, 0, 0, time.UTC)
	st.set(tmWday, int(t.Weekday()))
}

// dayOfYear sets tm_yday from the date.
func (st *ptime) dayOfYear() {
	m := st.get(tmMon)
	if m < 0 || m > 11 {
		return
	}
	st.set(tmYday, monYday[st.leap()][m]+st.get(tmMday)-1)
}

// monthDay sets tm_mon and tm_mday (those not given) from tm_yday.
func (st *ptime) monthDay() {
	yd := st.get(tmYday)
	l := st.leap()
	m := 0
	for m < 13 && monYday[l][m] <= yd {
		m++
	}
	if m == 0 {
		return
	}
	if !st.haveMon {
		st.set(tmMon, m-1)
	}
	if !st.haveMday {
		st.set(tmMday, yd-monYday[l][m-1]+1)
	}
}

// finish is what glibc does after the conversions: the hour of %p, the
// century, and the days of the week and the year of a date.
func (st *ptime) finish() {
	if st.haveI && st.isPM {
		st.set(tmHour, st.get(tmHour)+12)
	}
	if st.century != -1 {
		if st.wantCentury {
			st.set(tmYear, st.get(tmYear)%100+(st.century-19)*100)
		} else {
			st.set(tmYear, (st.century-19)*100)
		}
	}
	if st.wantXday && !st.haveWday {
		if !(st.haveMon && st.haveMday) && st.haveYday {
			st.monthDay()
			st.haveMon, st.haveMday = true, true
		}
		if st.get(tmYear) >= -1900 {
			st.dayOfWeek()
		}
	}
	if st.wantXday && !st.haveYday {
		st.dayOfYear()
	}
	if (st.haveUweek || st.haveWweek) && st.haveWday {
		saveWday, saveMday, saveMon := st.get(tmWday), st.get(tmMday), st.get(tmMon)
		off := 1
		if st.haveUweek {
			off = 0
		}
		st.set(tmMday, 1)
		st.set(tmMon, 0)
		st.dayOfWeek()
		if st.haveMday {
			st.set(tmMday, saveMday)
		}
		if st.haveMon {
			st.set(tmMon, saveMon)
		}
		if !st.haveYday {
			st.set(tmYday, (7-(st.get(tmWday)-off))%7+(st.weekNo-1)*7+(saveWday-off+7)%7)
		}
		if !st.haveMday || !st.haveMon {
			st.monthDay()
		}
		st.set(tmWday, saveWday)
	}
}
