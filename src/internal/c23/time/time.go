// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package time

import (
	c23 "internal/c23"
	"math"
	"strconv"
	"sync"
	"time"
)

var start = c23.MonoStart

// Time is time: the seconds since the epoch, also stored at t if not null.
func Time(t c23.Ptr) int64 {
	now := time.Now().Unix()
	if c23.Addr(t) != 0 {
		c23.Store64(t, uint64(now))
	}
	return now
}

// Clock is clock: the microseconds (CLOCKS_PER_SEC is a million) since
// the program started, which approximates its processor time.
func Clock() int64 { return int64(time.Since(start) / time.Microsecond) }

func Difftime(t1, t0 int64) float64 { return float64(t1 - t0) }

// tm is C's struct tm: tm_sec, tm_min, tm_hour, tm_mday, tm_mon,
// tm_year, tm_wday, tm_yday and tm_isdst.
type tm [9]int32

const (
	tmSec = iota
	tmMin
	tmHour
	tmMday
	tmMon
	tmYear
	tmWday
	tmYday
	tmIsdst
)

func tmGet(p c23.Ptr, i int) int { return int(int32(c23.Load32(c23.Add(p, 4*i)))) }

func tmSet(p c23.Ptr, i int, v int) { c23.Store32(c23.Add(p, 4*i), uint32(int32(v))) }

// tmTime is the time the struct tm at p holds, in loc. Go's time.Date
// normalizes the members as mktime does.
func tmTime(p c23.Ptr, loc *time.Location) time.Time {
	return time.Date(tmGet(p, tmYear)+1900, time.Month(tmGet(p, tmMon)+1), tmGet(p, tmMday),
		tmGet(p, tmHour), tmGet(p, tmMin), tmGet(p, tmSec), 0, loc)
}

// setTm stores t at p as a struct tm, or fails with EOVERFLOW if its
// year is not an int.
func setTm(p c23.Ptr, t time.Time) bool {
	y := int64(t.Year()) - 1900
	if y < math.MinInt32 || y > math.MaxInt32 {
		c23.SetErrno(c23.EOVERFLOW)
		return false
	}
	tmSet(p, tmSec, t.Second())
	tmSet(p, tmMin, t.Minute())
	tmSet(p, tmHour, t.Hour())
	tmSet(p, tmMday, t.Day())
	tmSet(p, tmMon, int(t.Month())-1)
	tmSet(p, tmYear, int(y))
	tmSet(p, tmWday, int(t.Weekday()))
	tmSet(p, tmYday, t.YearDay()-1)
	tmSet(p, tmIsdst, int(c23.B2I(t.IsDST())))
	return true
}

// Mktime is mktime: the time the struct tm at p holds, as local time
// (TZ's, local), whose members it normalizes. tm_isdst is not used:
// a local time that a change of offset makes ambiguous is the one Go's
// time.Date chooses.
func Mktime(p c23.Ptr) int64 {
	t := tmTime(p, local())
	if !setTm(p, t) {
		return -1
	}
	return t.Unix()
}

// Timegm is timegm: mktime in UTC.
func Timegm(p c23.Ptr) int64 {
	t := tmTime(p, time.UTC)
	if !setTm(p, t) {
		return -1
	}
	return t.Unix()
}

var tmOnce sync.Once

var tmStatic c23.Ptr // gmtime's and localtime's struct tm

var ascBuf c23.Ptr // asctime's and ctime's string

func statics() {
	tmOnce.Do(func() {
		tmStatic = c23.New[tm]()
		ascBuf = c23.Malloc(32)
	})
}

func Gmtime(t c23.Ptr) c23.Ptr {
	statics()
	return GmtimeR(t, tmStatic)
}

func Localtime(t c23.Ptr) c23.Ptr {
	statics()
	return LocaltimeR(t, tmStatic)
}

func GmtimeR(t, p c23.Ptr) c23.Ptr {
	if !setTm(p, time.Unix(int64(c23.Load64(t)), 0).UTC()) {
		return c23.Ptr(nil)
	}
	return p
}

// LocaltimeR is localtime_r, in TZ's time (local).
func LocaltimeR(t, p c23.Ptr) c23.Ptr {
	if !setTm(p, time.Unix(int64(c23.Load64(t)), 0).In(local())) {
		return c23.Ptr(nil)
	}
	return p
}

var dayNames = [7]string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

var monthNames = [12]string{"January", "February", "March", "April", "May", "June", "July",
	"August", "September", "October", "November", "December"}

func dayName(p c23.Ptr) string {
	if d := tmGet(p, tmWday); d >= 0 && d < 7 {
		return dayNames[d]
	}
	return "?"
}

func monthName(p c23.Ptr) string {
	if m := tmGet(p, tmMon); m >= 0 && m < 12 {
		return monthNames[m]
	}
	return "?"
}

func abbr(s string) string { return s[:min(3, len(s))] }

// Asctime is asctime: as "Sun Sep 16 01:03:52 1973\n", in an array the
// next call of asctime or ctime reuses.
func Asctime(p c23.Ptr) c23.Ptr {
	statics()
	b := asctime(p)
	if len(b) > 32 {
		c23.SetErrno(c23.EOVERFLOW)
		return c23.Ptr(nil)
	}
	copy(c23.BytesAt(ascBuf, uintptr(len(b))), b)
	return ascBuf
}

// asctime is asctime's string, with its null.
func asctime(p c23.Ptr) []byte {
	b := append(strftime("%a %b", p), pad(tmGet(p, tmMday), 3, ' ')...)
	b = append(b, ' ')
	b = append(b, strftime("%H:%M:%S ", p)...)
	b = strconv.AppendInt(b, int64(tmGet(p, tmYear))+1900, 10)
	return append(b, '\n', 0)
}

// Ctime is ctime: asctime(localtime(t)).
func Ctime(t c23.Ptr) c23.Ptr {
	p := Localtime(t)
	if c23.Addr(p) == 0 {
		return c23.Ptr(nil)
	}
	return Asctime(p)
}

// Strftime is strftime, in the C locale: the E and O modifiers have no
// effect, and %z and %Z are those of TZ's zone (local) at the time p holds
// (none if tm_isdst is negative): struct tm has no zone.
func Strftime(s c23.Ptr, max uint64, format, p c23.Ptr) uint64 {
	b := strftime(c23.GoString(format), p)
	if uint64(len(b)) >= max {
		return 0
	}
	copy(c23.BytesAt(s, uintptr(len(b)+1)), append(b, 0))
	return uint64(len(b))
}

// pad formats v in at least n digits, filled with c.
func pad(v, n int, c byte) []byte {
	s := strconv.Itoa(v)
	var b []byte
	if v < 0 {
		b, s = append(b, '-'), s[1:]
	}
	for i := len(s); i < n; i++ {
		b = append(b, c)
	}
	return append(b, s...)
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// isoWeeks is how many weeks ISO 8601 year y has.
func isoWeeks(y int) int {
	p := func(y int) int { return ((y+floorDiv(y, 4)-floorDiv(y, 100)+floorDiv(y, 400))%7 + 7) % 7 }
	if p(y) == 4 || p(y-1) == 3 {
		return 53
	}
	return 52
}

// isoWeek is the ISO 8601 year and week of the struct tm at p.
func isoWeek(p c23.Ptr) (year, week int) {
	year = tmGet(p, tmYear) + 1900
	wdm := (tmGet(p, tmWday) + 6) % 7 // Monday is 0
	week = floorDiv(tmGet(p, tmYday)-wdm+10, 7)
	switch {
	case week < 1:
		year--
		week = isoWeeks(year)
	case week > isoWeeks(year):
		year++
		week = 1
	}
	return year, week
}

func strftime(f string, p c23.Ptr) []byte {
	var b []byte
	for i := 0; i < len(f); i++ {
		if f[i] != '%' || i+1 >= len(f) {
			b = append(b, f[i])
			continue
		}
		i++
		if (f[i] == 'E' || f[i] == 'O') && i+1 < len(f) {
			i++
		}
		year := tmGet(p, tmYear) + 1900
		hour := tmGet(p, tmHour)
		switch f[i] {
		case 'a':
			b = append(b, abbr(dayName(p))...)
		case 'A':
			b = append(b, dayName(p)...)
		case 'b', 'h':
			b = append(b, abbr(monthName(p))...)
		case 'B':
			b = append(b, monthName(p)...)
		case 'c':
			b = append(b, strftime("%a %b %e %H:%M:%S %Y", p)...)
		case 'C':
			b = append(b, pad(floorDiv(year, 100), 2, '0')...)
		case 'd':
			b = append(b, pad(tmGet(p, tmMday), 2, '0')...)
		case 'D', 'x':
			b = append(b, strftime("%m/%d/%y", p)...)
		case 'e':
			b = append(b, pad(tmGet(p, tmMday), 2, ' ')...)
		case 'F':
			b = append(b, strftime("%Y-%m-%d", p)...)
		case 'g':
			y, _ := isoWeek(p)
			b = append(b, pad((y%100+100)%100, 2, '0')...)
		case 'G':
			y, _ := isoWeek(p)
			b = strconv.AppendInt(b, int64(y), 10)
		case 'H':
			b = append(b, pad(hour, 2, '0')...)
		case 'I':
			h := (hour%12 + 12) % 12
			if h == 0 {
				h = 12
			}
			b = append(b, pad(h, 2, '0')...)
		case 'j':
			b = append(b, pad(tmGet(p, tmYday)+1, 3, '0')...)
		case 'm':
			b = append(b, pad(tmGet(p, tmMon)+1, 2, '0')...)
		case 'M':
			b = append(b, pad(tmGet(p, tmMin), 2, '0')...)
		case 'n':
			b = append(b, '\n')
		case 'p':
			if hour < 12 {
				b = append(b, "AM"...)
			} else {
				b = append(b, "PM"...)
			}
		case 'r':
			b = append(b, strftime("%I:%M:%S %p", p)...)
		case 'R':
			b = append(b, strftime("%H:%M", p)...)
		case 'S':
			b = append(b, pad(tmGet(p, tmSec), 2, '0')...)
		case 't':
			b = append(b, '\t')
		case 'T', 'X':
			b = append(b, strftime("%H:%M:%S", p)...)
		case 'u':
			d := tmGet(p, tmWday)
			if d == 0 {
				d = 7
			}
			b = strconv.AppendInt(b, int64(d), 10)
		case 'U':
			b = append(b, pad(floorDiv(tmGet(p, tmYday)+7-tmGet(p, tmWday), 7), 2, '0')...)
		case 'V':
			_, w := isoWeek(p)
			b = append(b, pad(w, 2, '0')...)
		case 'w':
			b = strconv.AppendInt(b, int64(tmGet(p, tmWday)), 10)
		case 'W':
			b = append(b, pad(floorDiv(tmGet(p, tmYday)+7-(tmGet(p, tmWday)+6)%7, 7), 2, '0')...)
		case 'y':
			b = append(b, pad((year%100+100)%100, 2, '0')...)
		case 'Y':
			b = strconv.AppendInt(b, int64(year), 10)
		case 'z':
			if tmGet(p, tmIsdst) >= 0 {
				_, off := tmTime(p, local()).Zone()
				sign := byte('+')
				if off < 0 {
					sign, off = '-', -off
				}
				b = append(b, sign)
				b = append(b, pad(off/3600*100+off/60%60, 4, '0')...)
			}
		case 'Z':
			if tmGet(p, tmIsdst) >= 0 {
				name, _ := tmTime(p, local()).Zone()
				b = append(b, name...)
			}
		case '%':
			b = append(b, '%')
		default:
			b = append(b, '%', f[i])
		}
	}
	return b
}

// TimespecGet is timespec_get: the time of base TIME_UTC (1), as a
// struct timespec (tv_sec, tv_nsec).
func TimespecGet(ts c23.Ptr, base int32) int32 {
	if base != 1 {
		return 0
	}
	now := time.Now()
	c23.Store64(ts, uint64(now.Unix()))
	c23.StoreLong(c23.Add(ts, 8), int64(now.Nanosecond()))
	return base
}

// TimespecGetres is timespec_getres: TIME_UTC's resolution, which Go
// gives in nanoseconds.
func TimespecGetres(ts c23.Ptr, base int32) int32 {
	if base != 1 {
		return 0
	}
	if c23.Addr(ts) != 0 {
		c23.Store64(ts, 0)
		c23.StoreLong(c23.Add(ts, 8), 1)
	}
	return base
}
