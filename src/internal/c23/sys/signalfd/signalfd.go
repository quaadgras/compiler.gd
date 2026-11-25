// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package signalfd is sys/signalfd.h: a signalfd is a c23.Special whose
// reads take the signals of its mask that are pending for the reading
// thread or for the process (those every thread blocks), as sigwait
// would, each as a struct signalfd_siginfo.
package signalfd

import (
	c23 "internal/c23"
	"sync"
)

const (
	SFD_NONBLOCK = c23.O_NONBLOCK
	SFD_CLOEXEC  = c23.O_CLOEXEC
)

// infoSize is sizeof(struct signalfd_siginfo).
const infoSize = 128

type signalfd struct {
	mu   sync.Mutex
	mask uint64
}

func (s *signalfd) getMask() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mask
}

// Read takes as many pending signals as b has room for, at least one,
// waiting for one unless nonblock (EAGAIN).
func (s *signalfd) Read(b []byte, nonblock bool) (int, error) {
	if len(b) < infoSize {
		return 0, c23.ErrnoError(c23.EINVAL)
	}
	t := c23.SigSelf()
	n := 0
	c23.SigMu.Lock()
	for n+infoSize <= len(b) {
		sig, info := t.Take(s.getMask())
		if sig == 0 {
			if n > 0 {
				break
			}
			if nonblock {
				c23.SigMu.Unlock()
				return 0, c23.ErrnoError(c23.EAGAIN)
			}
			c23.SigCond.Wait()
			continue
		}
		storeInfo(b[n:n+infoSize], sig, info)
		n += infoSize
	}
	c23.SigMu.Unlock()
	return n, nil
}

// storeInfo stores struct signalfd_siginfo (Linux's: ssi_signo, ssi_errno,
// ssi_code, ssi_pid, ssi_uid, ... ssi_status at 40, ssi_int at 44,
// ssi_ptr at 48).
func storeInfo(b []byte, sig int32, info c23.SigInfo) {
	clear(b)
	put32 := func(off int, v uint32) {
		for i := range 4 {
			b[off+i] = byte(v >> (8 * i))
		}
	}
	put32(0, uint32(sig))
	put32(8, uint32(info.Code))
	put32(12, uint32(info.Pid))
	put32(16, uint32(info.Uid))
	put32(40, uint32(info.Status))
	// ssi_int and ssi_ptr: the union's integer, or its pointer's
	// address if it holds one.
	v := info.Value.Int
	if a := c23.Addr(info.Value.Ptr); a != 0 {
		v = uint64(a)
	}
	put32(44, uint32(v))
	put32(48, uint32(v))
	put32(52, uint32(v>>32))
}

func (s *signalfd) Write(b []byte, nonblock bool) (int, error) {
	return 0, c23.ErrnoError(c23.EINVAL)
}

func (s *signalfd) Close() error { return nil }

func (s *signalfd) Ready() int16 {
	t := c23.SigSelf()
	m := s.getMask()
	c23.SigMu.Lock()
	defer c23.SigMu.Unlock()
	if (t.Pending|c23.SigPending)&m != 0 {
		return c23.POLLIN
	}
	return 0
}

func (s *signalfd) Mode() uint32 { return 0o600 }

// Signalfd is signalfd: a new descriptor if fd is -1, or a new mask for
// signalfd fd. SIGKILL and SIGSTOP in mask are ignored.
func Signalfd(fd int32, mask *c23.Void, flags int32) int32 {
	if flags&^(SFD_NONBLOCK|SFD_CLOEXEC) != 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	m := c23.LoadSigset(mask) &^ c23.SigUnblockable
	if fd == -1 {
		return c23.FdNewSpecial(&signalfd{mask: m}, flags&SFD_NONBLOCK, flags&SFD_CLOEXEC != 0)
	}
	sp, _ := c23.FdSpecial(fd, c23.EINVAL)
	if sp == nil {
		return -1
	}
	s, ok := sp.(*signalfd)
	if !ok {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	s.mu.Lock()
	s.mask = m
	s.mu.Unlock()
	c23.PollNotify()
	return fd
}
