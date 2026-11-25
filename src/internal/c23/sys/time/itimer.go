// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package time

import (
	c23 "internal/c23"
	"time"
)

const (
	ITIMER_REAL    = 0
	ITIMER_VIRTUAL = 1
	ITIMER_PROF    = 2
)

// loadTimeval reads a struct timeval {time_t tv_sec; suseconds_t
// tv_usec;}, 8 bytes each on every target.
func loadTimeval(p *c23.Void) (time.Duration, bool) {
	sec, usec := int64(c23.Load64(p)), int64(c23.Load64(c23.Add(p, 8)))
	if sec < 0 || usec < 0 || usec >= 1e6 {
		return 0, false
	}
	if sec >= (1<<63-1)/int64(time.Second) {
		return 1<<63 - 1, true
	}
	return time.Duration(sec)*time.Second + time.Duration(usec)*time.Microsecond, true
}

func storeTimeval(p *c23.Void, d time.Duration) {
	// Round up, so that an armed timer does not read as disarmed.
	us := (d + time.Microsecond - 1) / time.Microsecond
	c23.Store64(p, uint64(us/1e6))
	c23.Store64(c23.Add(p, 8), uint64(us%1e6))
}

// storeItimerval stores struct itimerval {struct timeval it_interval,
// it_value;}.
func storeItimerval(p *c23.Void, value, interval time.Duration) {
	storeTimeval(p, interval)
	storeTimeval(c23.Add(p, 16), value)
}

// Getitimer is getitimer.
func Getitimer(which int32, curr *c23.Void) int32 {
	if which < ITIMER_REAL || which > ITIMER_PROF {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	v, i := c23.ITimer(int(which))
	storeItimerval(curr, v, i)
	return 0
}

// Setitimer is setitimer. ITIMER_REAL is alarm's timer, as on Linux, and
// sends SIGALRM. ITIMER_VIRTUAL (SIGVTALRM) and ITIMER_PROF (SIGPROF)
// count wall time too, as Go has no timers of a process's processor
// time: they expire no later than Linux's would.
func Setitimer(which int32, newValue, oldValue *c23.Void) int32 {
	if which < ITIMER_REAL || which > ITIMER_PROF {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	var value, interval time.Duration
	if c23.Addr(newValue) != 0 { // null disarms, as Linux allows
		var ok1, ok2 bool
		interval, ok1 = loadTimeval(newValue)
		value, ok2 = loadTimeval(c23.Add(newValue, 16))
		if !ok1 || !ok2 {
			c23.SetErrno(c23.EINVAL)
			return -1
		}
	}
	v, i := c23.SetITimer(int(which), value, interval)
	if c23.Addr(oldValue) != 0 {
		storeItimerval(oldValue, v, i)
	}
	return 0
}
