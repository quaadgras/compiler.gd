// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import "time"

// time.h

var start = time.Now()

// Time is time: the seconds since the epoch, also stored at t if not null.
func Time(t Ptr) int64 {
	now := time.Now().Unix()
	if Addr(t) != 0 {
		Store64(t, uint64(now))
	}
	return now
}

// Clock is clock: the microseconds (CLOCKS_PER_SEC is a million) since
// the program started, which approximates its processor time.
func Clock() int64 { return int64(time.Since(start) / time.Microsecond) }

func Difftime(t1, t0 int64) float64 { return float64(t1 - t0) }
