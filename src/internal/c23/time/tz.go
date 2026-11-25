// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package time

import (
	c23 "internal/c23"
	"os"
	"strings"
	"sync"
	"time"
)

// The local time zone is TZ's, read again whenever TZ changes (glibc's
// localtime calls tzset): unset, the system's (/etc/localtime, or Go's
// time.Local where there is none); empty, UTC; otherwise the zone of
// that name (Go's time.LoadLocation, after a leading ':'), or else a
// POSIX TZ string ("EST5EDT,M3.2.0,M11.1.0"), which Go's zone files
// interpret as their footer; or UTC if it is neither.

var (
	tzMu    sync.Mutex
	tzDone  bool
	tzSet   bool   // whether TZ was set
	tzEnv   string // and its value
	tzLoc   *time.Location
	tzNames = map[string]*c23.Void{}

	// tzname, timezone and daylight, the library's variables (cmd/cc's
	// libcVars).
	tznameP, timezoneP, daylightP *c23.Void
)

// local returns the local time zone, as TZ says now.
func local() *time.Location {
	tzMu.Lock()
	defer tzMu.Unlock()
	update()
	return tzLoc
}

// update reads TZ, and if it changed sets the zone and tzname, timezone
// and daylight. tzMu is held.
func update() {
	env, set := os.LookupEnv("TZ")
	if tzDone && set == tzSet && env == tzEnv {
		return
	}
	tzDone, tzSet, tzEnv = true, set, env
	tzLoc = loadZone(env, set)
	setVars()
}

func loadZone(env string, set bool) *time.Location {
	if !set {
		if data, err := os.ReadFile("/etc/localtime"); err == nil {
			if loc, err := time.LoadLocationFromTZData("Local", data); err == nil {
				return loc
			}
		}
		return time.Local
	}
	name := strings.TrimPrefix(env, ":")
	if name == "" {
		return time.UTC
	}
	if name != "Local" {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc
		}
	}
	if loc, ok := posixZone(env); ok {
		return loc
	}
	return time.UTC
}

// badZone is the zone a POSIX TZ string Go cannot read leaves.
const badZone = "?bad"

// posixZone is the zone of a POSIX TZ string: a zone file with no
// transitions, whose footer the string is.
func posixZone(s string) (*time.Location, bool) {
	if s == "" || strings.ContainsAny(s, "\n\x00") {
		return nil, false
	}
	var b []byte
	header := func() {
		b = append(b, "TZif2"...)
		b = append(b, make([]byte, 15)...)
		// isutcnt, isstdcnt, leapcnt, timecnt, typecnt, charcnt
		for _, n := range [6]uint32{0, 0, 0, 0, 1, uint32(len(badZone) + 1)} {
			b = append(b, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
		}
	}
	data := func() {
		b = append(b, 0, 0, 0, 0, 0, 0) // utoff 0, not DST, abbreviation 0
		b = append(b, badZone+"\x00"...)
	}
	header()
	data()
	header()
	data()
	b = append(b, '\n')
	b = append(b, s...)
	b = append(b, '\n')
	loc, err := time.LoadLocationFromTZData(s, b)
	if err != nil {
		return nil, false
	}
	if name, _ := time.Unix(0, 0).In(loc).Zone(); name == badZone {
		return nil, false
	}
	return loc, true
}

// setVars sets tzname, timezone and daylight to the zone's: the
// standard time's and the daylight saving time's of this year, which has
// daylight saving time if January or July has (tzname[1] is the
// standard time's if neither has). tzMu is held.
func setVars() {
	if c23.Addr(tznameP) == 0 {
		tznameP = c23.Malloc(2 * uint64(c23.WordSize))
		timezoneP = c23.Malloc(uint64(c23.LongSize))
		daylightP = c23.Malloc(4)
	}
	y := time.Now().Year()
	std, dst := "", ""
	stdOff := 0
	for _, m := range []time.Month{time.January, time.July} {
		t := time.Date(y, m, 1, 12, 0, 0, 0, time.UTC).In(tzLoc)
		name, off := t.Zone()
		if t.IsDST() {
			dst = name
		} else if std == "" {
			std, stdOff = name, off
		}
	}
	if std == "" { // daylight saving time all year
		std = dst
		_, stdOff = time.Date(y, time.January, 1, 12, 0, 0, 0, time.UTC).In(tzLoc).Zone()
	}
	daylight := dst != ""
	if !daylight {
		dst = std
	}
	ws := int(c23.WordSize)
	c23.StorePtr(tznameP, tzName(std))
	c23.StorePtr(c23.Add(tznameP, ws), tzName(dst))
	c23.StoreLong(timezoneP, int64(-stdOff))
	c23.Store32(daylightP, uint32(c23.B2I(daylight)))
}

// tzName is the C string of a zone's name, which it keeps.
func tzName(s string) *c23.Void {
	p, ok := tzNames[s]
	if !ok {
		p = c23.CString(s)
		tzNames[s] = p
	}
	return p
}

// vars returns where the variables are, set as TZ says.
func vars() {
	tzMu.Lock()
	defer tzMu.Unlock()
	update()
}

// Tzset is tzset: it sets the local time zone, and tzname, timezone and
// daylight, as TZ says.
func Tzset() { vars() }

// Tzname, Timezone and Daylight return where tzname, timezone and
// daylight are (cmd/cc's libcVars).
func Tzname() *c23.Void { vars(); return tznameP }

func Timezone() *c23.Void { vars(); return timezoneP }

func Daylight() *c23.Void { vars(); return daylightP }

// AsctimeR is asctime_r: asctime at buf (at least 26 bytes).
func AsctimeR(p, buf *c23.Void) *c23.Void {
	b := asctime(p)
	if len(b) > 26 {
		c23.SetErrno(c23.EOVERFLOW)
		return (*c23.Void)(nil)
	}
	copy(c23.Writable(buf, uintptr(len(b))), b)
	return buf
}

// CtimeR is ctime_r: asctime_r(localtime_r(t), buf).
func CtimeR(t, buf *c23.Void) *c23.Void {
	p := c23.New[tm]()
	if c23.Addr(LocaltimeR(t, p)) == 0 {
		return (*c23.Void)(nil)
	}
	return AsctimeR(p, buf)
}

// StrftimeL is strftime_l, which is strftime: the library's locales
// format times alike.
func StrftimeL(s *c23.Void, max uint64, format, p, _ *c23.Void) uint64 {
	return Strftime(s, max, format, p)
}
