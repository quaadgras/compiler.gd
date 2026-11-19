// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package eventfd is sys/eventfd.h: an eventfd is a c23.Special that
// holds a 64-bit counter.
package eventfd

import (
	c23 "internal/c23"
	"sync"
)

const (
	EFD_SEMAPHORE = 1
	EFD_NONBLOCK  = c23.O_NONBLOCK
	EFD_CLOEXEC   = c23.O_CLOEXEC
)

// counterMax is the most the counter holds.
const counterMax = 1<<64 - 2

type eventfd struct {
	mu       sync.Mutex
	cond     sync.Cond
	count    uint64
	sem      bool
	arrivals uint64 // the writes, for epoll's edge trigger
}

func le64(b []byte) uint64 {
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
}

func putLE64(b []byte, v uint64) {
	for i := range 8 {
		b[i] = byte(v >> (8 * i))
	}
}

// Read takes the counter (or 1 of it, as a semaphore), waiting while it
// is zero unless nonblock (EAGAIN).
func (e *eventfd) Read(b []byte, nonblock bool) (int, error) {
	if len(b) < 8 {
		return 0, c23.ErrnoError(c23.EINVAL)
	}
	e.mu.Lock()
	for e.count == 0 {
		if nonblock {
			e.mu.Unlock()
			return 0, c23.ErrnoError(c23.EAGAIN)
		}
		e.cond.Wait()
	}
	v := e.count
	if e.sem {
		v = 1
	}
	e.count -= v
	e.cond.Broadcast()
	e.mu.Unlock()
	putLE64(b, v)
	c23.PollNotify()
	return 8, nil
}

// Write adds to the counter, waiting while that would pass its maximum
// unless nonblock (EAGAIN).
func (e *eventfd) Write(b []byte, nonblock bool) (int, error) {
	if len(b) < 8 {
		return 0, c23.ErrnoError(c23.EINVAL)
	}
	v := le64(b)
	if v > counterMax {
		return 0, c23.ErrnoError(c23.EINVAL)
	}
	e.mu.Lock()
	for counterMax-e.count < v {
		if nonblock {
			e.mu.Unlock()
			return 0, c23.ErrnoError(c23.EAGAIN)
		}
		e.cond.Wait()
	}
	e.count += v
	e.arrivals++
	e.cond.Broadcast()
	e.mu.Unlock()
	c23.PollNotify()
	return 8, nil
}

func (e *eventfd) Close() error { return nil }

func (e *eventfd) Ready() int16 {
	e.mu.Lock()
	defer e.mu.Unlock()
	var r int16
	if e.count > 0 {
		r |= c23.POLLIN
	}
	if e.count < counterMax {
		r |= c23.POLLOUT
	}
	return r
}

func (e *eventfd) Mode() uint32 { return 0o600 }

func (e *eventfd) Arrivals() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.arrivals
}

// Eventfd is eventfd.
func Eventfd(initval uint32, flags int32) int32 {
	if flags&^(EFD_SEMAPHORE|EFD_NONBLOCK|EFD_CLOEXEC) != 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	e := &eventfd{count: uint64(initval), sem: flags&EFD_SEMAPHORE != 0}
	e.cond.L = &e.mu
	return c23.FdNewSpecial(e, flags&EFD_NONBLOCK, flags&EFD_CLOEXEC != 0)
}

// EventfdRead is eventfd_read: a read of the counter into *value.
func EventfdRead(fd int32, value c23.Ptr) int32 {
	of := c23.FdFile(fd)
	if of == nil {
		c23.SetErrno(c23.EBADF)
		return -1
	}
	var b [8]byte
	if n, err := of.Read(b[:]); err != nil || n != 8 {
		if err != nil {
			c23.SetErrnoOf(err)
		}
		return -1
	}
	c23.Store64(value, le64(b[:]))
	return 0
}

// EventfdWrite is eventfd_write: a write of value.
func EventfdWrite(fd int32, value uint64) int32 {
	of := c23.FdFile(fd)
	if of == nil {
		c23.SetErrno(c23.EBADF)
		return -1
	}
	var b [8]byte
	putLE64(b[:], value)
	if n, err := of.Write(b[:]); err != nil || n != 8 {
		if err != nil {
			c23.SetErrnoOf(err)
		}
		return -1
	}
	return 0
}
