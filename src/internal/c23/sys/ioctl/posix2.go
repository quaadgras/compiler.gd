// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ioctl

import (
	c23 "internal/c23"
	"io"
)

const TIOCGWINSZ = 0x5413

const FIONREAD = 0x541B

const FIONBIO = 0x5421

const FIONCLEX = 0x5450

const FIOCLEX = 0x5451

// Ioctl is ioctl, for FIONREAD (the bytes a read would return: what is
// left of a regular file, or what the system says of a pipe or terminal),
// TIOCGWINSZ (a terminal's struct winsize), FIONBIO, FIOCLEX and
// FIONCLEX. Other requests fail with ENOTTY.
func Ioctl(fd int32, req uint64, va ...any) int32 {
	var arg *c23.Void
	if len(va) > 0 {
		arg = c23.VaArg[*c23.Void](c23.VaStart(va))
	}
	of := c23.FdFile(fd)
	if of == nil {
		c23.SetErrno(c23.EBADF)
		return -1
	}
	switch req {
	case FIONREAD:
		var n int64
		ahead, all := of.Buffered()
		if all {
			n = int64(ahead)
		} else if fi, err := of.F.Stat(); err == nil && fi.Mode().IsRegular() {
			pos, err := of.F.Seek(0, io.SeekCurrent)
			if err != nil {
				c23.SetErrnoOf(err)
				return -1
			}
			n = max(fi.Size()-pos, 0)
		} else {
			var err error
			if n, err = ioctlInRead(of.F); err != nil {
				c23.SetErrnoOf(err)
				return -1
			}
			n += int64(ahead) // what was read ahead of C, for poll
		}
		c23.Store32(arg, uint32(n))
		return 0
	case TIOCGWINSZ:
		ws, err := ioctlWinsize(of.F)
		if err != nil {
			c23.SetErrnoOf(err)
			return -1
		}
		for i, v := range ws {
			c23.Store16(c23.Add(arg, 2*i), v)
		}
		return 0
	case FIONBIO:
		c23.FdMu.Lock()
		defer c23.FdMu.Unlock()
		if c23.Load32(arg) != 0 {
			of.Flags |= c23.O_NONBLOCK
		} else {
			of.Flags &^= c23.O_NONBLOCK
		}
		return 0
	case FIOCLEX, FIONCLEX:
		c23.FdMu.Lock()
		defer c23.FdMu.Unlock()
		if e := c23.FdEnt(fd); e != nil {
			e.Cloexec = req == FIOCLEX
		}
		return 0
	}
	c23.SetErrno(c23.ENOTTY)
	return -1
}
