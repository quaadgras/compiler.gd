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
		if count > 0 || timeout == 0 {
			return count
		}
		if !c23.WaitUntil(ch, deadline) {
			return 0
		}
	}
}
