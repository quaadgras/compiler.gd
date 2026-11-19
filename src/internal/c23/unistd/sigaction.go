// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package unistd

import (
	c23 "internal/c23"
	"time"
)

// Pause is pause.
func Pause() int32 {
	c23.Suspend(c23.SigSelf().Mask)
	c23.SetErrno(c23.EINTR)
	return -1
}

// Alarm is alarm: ITIMER_REAL (c23.SetITimer) set to seconds, which
// sends SIGALRM to the process; the result is the seconds left until the
// previous alarm, rounded as Linux does.
func Alarm(seconds uint32) uint32 {
	d, _ := c23.SetITimer(0, time.Duration(seconds)*time.Second, 0)
	if d <= 0 {
		return 0
	}
	left := uint32(d / time.Second)
	if d%time.Second >= time.Second/2 || left == 0 {
		left++
	}
	return left
}
