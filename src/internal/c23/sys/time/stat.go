// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package time

import (
	c23 "internal/c23"
	"time"
)

// Gettimeofday is gettimeofday, which stores the time as a struct timeval
// {time_t tv_sec; suseconds_t tv_usec;} at tv.
func Gettimeofday(tv, tz *c23.Void) int32 {
	if c23.Addr(tv) != 0 {
		now := time.Now()
		c23.Store64(tv, uint64(now.Unix()))
		c23.Store64(c23.Add(tv, 8), uint64(now.Nanosecond()/1000))
	}
	if c23.Addr(tz) != 0 {
		// struct timezone {int tz_minuteswest; int tz_dsttime;}: UTC, as glibc.
		c23.Store32(tz, 0)
		c23.Store32(c23.Add(tz, 4), 0)
	}
	return 0
}
