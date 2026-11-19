// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package time

import (
	c23 "internal/c23"
	"time"
)

const CLOCK_REALTIME = 0

const CLOCK_MONOTONIC = 1

const CLOCK_PROCESS_CPUTIME_ID = 2

const CLOCK_THREAD_CPUTIME_ID = 3

func storeTimespec(ts c23.Ptr, sec, nsec int64) {
	c23.Store64(ts, uint64(sec))
	c23.StoreLong(c23.Add(ts, 8), nsec)
}

// ClockGettime is clock_gettime. The CPU-time clocks give the time since
// the program started (CLOCK_PROCESS_CPUTIME_ID) or the thread did
// (CLOCK_THREAD_CPUTIME_ID, and pthread_getcpuclockid's), as Go cannot
// know their processor time.
func ClockGettime(id int32, ts c23.Ptr) int32 {
	d, ok := c23.ClockNow(id)
	if !ok {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	storeTimespec(ts, int64(d/time.Second), int64(d%time.Second))
	return 0
}

// ClockGetres is clock_getres: a nanosecond, for each clock.
func ClockGetres(id int32, ts c23.Ptr) int32 {
	if _, ok := c23.ClockOrigin(id); !ok {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	if c23.Addr(ts) != 0 {
		storeTimespec(ts, 0, 1)
	}
	return 0
}

// Nanosleep is nanosleep, a cancellation point, which is not interrupted
// by signals, so it does not store what is left at rem.
func Nanosleep(req, rem c23.Ptr) int32 {
	sec, nsec := int64(c23.Load64(req)), c23.LoadLong(c23.Add(req, 8))
	if sec < 0 || nsec < 0 || nsec >= 1e9 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	c23.CancelSleep(time.Duration(sec)*time.Second + time.Duration(nsec))
	return 0
}
