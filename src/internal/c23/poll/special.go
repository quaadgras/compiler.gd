// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package poll

import (
	c23 "internal/c23"
	"time"
)

const POLLNVAL = 0x20

// Poll is poll: fds is an array of n struct pollfd {int fd; short
// events; short revents;}; timeout is in milliseconds, -1 for none.
func Poll(fds c23.Ptr, n uint64, timeout int32) int32 {
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(time.Duration(timeout) * time.Millisecond)
	}
	return pollUntil(fds, n, deadline, timeout == 0)
}

// Ppoll is ppoll: timeout is a struct timespec, or null for none. The
// signal mask is not changed while it waits.
func Ppoll(fds c23.Ptr, n uint64, ts, sigmask c23.Ptr) int32 {
	var deadline time.Time
	now := false
	if c23.Addr(ts) != 0 {
		sec, nsec := int64(c23.Load64(ts)), c23.LoadLong(c23.Add(ts, 8))
		if sec < 0 || nsec < 0 || nsec >= 1e9 {
			c23.SetErrno(c23.EINVAL)
			return -1
		}
		d := time.Duration(sec)*time.Second + time.Duration(nsec)
		now = d == 0
		deadline = time.Now().Add(d)
	}
	return pollUntil(fds, n, deadline, now)
}

// pollUntil polls until a descriptor is ready, the deadline (if not
// zero) passes, or at once if now.
func pollUntil(fds c23.Ptr, n uint64, deadline time.Time, now bool) int32 {
	for {
		ch := c23.PollWait()
		count := int32(0)
		for i := range n {
			p := c23.Add(fds, int(8*i))
			fd := int32(c23.Load32(p))
			events := int16(c23.Load16(c23.Add(p, 4)))
			var rev int16
			if fd >= 0 {
				if of := c23.FdFile(fd); of == nil {
					rev = POLLNVAL
				} else {
					rev = of.Ready() & (events | c23.POLLERR | c23.POLLHUP)
				}
			}
			c23.Store16(c23.Add(p, 6), uint16(rev))
			if rev != 0 {
				count++
			}
		}
		if count > 0 || now {
			return count
		}
		if !c23.WaitUntil(ch, deadline) {
			return 0
		}
	}
}
