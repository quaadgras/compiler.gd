// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package unistd

import (
	c23 "internal/c23"
	"sync"
	"time"
)

const SIGALRM = 14

const SI_KERNEL = 0x80

// Pause is pause.
func Pause() int32 {
	c23.Suspend(c23.SigSelf().Mask)
	c23.SetErrno(c23.EINTR)
	return -1
}

var alarmMu sync.Mutex

var alarmTimer *time.Timer

var alarmAt time.Time

// Alarm is alarm: SIGALRM is sent to the process seconds from now
// (time.AfterFunc), and the result is the seconds left until the
// previous alarm, rounded as Linux does.
func Alarm(seconds uint32) uint32 {
	alarmMu.Lock()
	defer alarmMu.Unlock()
	var left uint32
	if alarmTimer != nil && alarmTimer.Stop() {
		d := alarmAt.Sub(time.Now())
		if d > 0 {
			left = uint32(d / 1e9)
			if d%1e9 >= 5e8 || left == 0 {
				left++
			}
		}
	}
	alarmTimer = nil
	if seconds > 0 {
		alarmAt = time.Now().Add(time.Duration(seconds) * time.Second)
		alarmTimer = time.AfterFunc(time.Duration(seconds)*time.Second, func() {
			c23.SigSendProcess(SIGALRM, c23.SigInfo{Code: SI_KERNEL}, nil)
		})
	}
	return left
}
