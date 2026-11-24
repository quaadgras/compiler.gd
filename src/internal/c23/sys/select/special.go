// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sysselect

import (
	c23 "internal/c23"
	"time"
)

const POLLPRI = 0x2

// Select is select: the fd_sets are bit arrays (descriptor i is bit i%8
// of byte i/8, on a little-endian target, whatever the word), timeout a
// struct timeval or null for none.
func Select(nfds int32, rd, wr, ex, timeout *c23.Void) int32 {
	var deadline time.Time
	poll := true
	if c23.Addr(timeout) != 0 {
		sec, usec := int64(c23.Load64(timeout)), int64(c23.Load64(c23.Add(timeout, 8)))
		if sec < 0 || usec < 0 || usec >= 1e6 {
			c23.SetErrno(c23.EINVAL)
			return -1
		}
		d := time.Duration(sec)*time.Second + time.Duration(usec)*time.Microsecond
		poll = d == 0
		deadline = time.Now().Add(d)
	}
	return selectFds(nfds, rd, wr, ex, deadline, poll)
}

// Pselect is pselect: timeout is a struct timespec; the signal mask is
// not changed.
func Pselect(nfds int32, rd, wr, ex, timeout, sigmask *c23.Void) int32 {
	var deadline time.Time
	poll := true
	if c23.Addr(timeout) != 0 {
		sec, nsec := int64(c23.Load64(timeout)), c23.LoadLong(c23.Add(timeout, 8))
		if sec < 0 || nsec < 0 || nsec >= 1e9 {
			c23.SetErrno(c23.EINVAL)
			return -1
		}
		d := time.Duration(sec)*time.Second + time.Duration(nsec)
		poll = d == 0
		deadline = time.Now().Add(d)
	}
	return selectFds(nfds, rd, wr, ex, deadline, poll)
}

func selectFds(nfds int32, rd, wr, ex *c23.Void, deadline time.Time, poll bool) int32 {
	if nfds < 0 || nfds > 1024 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	nb := uintptr(nfds+7) / 8
	sets := [3][]byte{}
	want := [3][]byte{}
	for i, p := range [3]*c23.Void{rd, wr, ex} {
		if c23.Addr(p) != 0 {
			sets[i] = c23.BytesAt(p, nb)
			want[i] = append([]byte(nil), sets[i]...)
		}
	}
	isSet := func(s []byte, fd int32) bool { return s != nil && s[fd/8]&(1<<(fd%8)) != 0 }
	for fd := range nfds {
		if (isSet(want[0], fd) || isSet(want[1], fd) || isSet(want[2], fd)) && c23.FdFile(fd) == nil {
			c23.SetErrno(c23.EBADF)
			return -1
		}
	}
	for {
		ch := c23.PollWait()
		count := int32(0)
		var got [3][]byte
		for i := range got {
			if want[i] != nil {
				got[i] = make([]byte, nb)
			}
		}
		for fd := range nfds {
			if !isSet(want[0], fd) && !isSet(want[1], fd) && !isSet(want[2], fd) {
				continue
			}
			of := c23.FdFile(fd)
			if of == nil {
				continue
			}
			r := of.Ready()
			for i, ev := range [3]int16{c23.POLLIN | c23.POLLHUP | c23.POLLERR, c23.POLLOUT | c23.POLLERR, POLLPRI} {
				if isSet(want[i], fd) && r&ev != 0 {
					got[i][fd/8] |= 1 << (fd % 8)
					count++
				}
			}
		}
		if count > 0 || poll || !deadline.IsZero() && !time.Now().Before(deadline) {
			for i := range sets {
				if sets[i] != nil {
					copy(sets[i], got[i])
				}
			}
			return count
		}
		c23.WaitUntil(ch, deadline)
	}
}
