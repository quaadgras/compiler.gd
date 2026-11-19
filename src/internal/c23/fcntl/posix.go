// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fcntl

import (
	c23 "internal/c23"
)

const F_DUPFD = 0

const F_GETFD = 1

const F_SETFD = 2

const F_GETFL = 3

const F_SETFL = 4

const F_DUPFD_CLOEXEC = 1030

const FD_CLOEXEC = 1

// Open is open: the mode, if flags has O_CREAT, is its third argument.
func Open(path c23.Ptr, flags int32, va ...any) int32 {
	var mode uint32
	if flags&c23.O_CREAT != 0 && len(va) > 0 {
		mode = uint32(c23.ToInt64(va[0]))
	}
	return c23.Open(c23.GoString(path), flags, mode)
}

func Creat(path c23.Ptr, mode uint32) int32 {
	return c23.Open(c23.GoString(path), c23.O_WRONLY|c23.O_CREAT|c23.O_TRUNC, mode)
}

// Fcntl is fcntl, for F_DUPFD, F_DUPFD_CLOEXEC, F_GETFD, F_SETFD (FD_CLOEXEC,
// which only F_GETFD reads back), F_GETFL and F_SETFL (O_APPEND and
// O_NONBLOCK, which only F_GETFL reads back), and the advisory record locks
// F_GETLK, F_SETLK and F_SETLKW (the system's, where it has them).
func Fcntl(fd, cmd int32, va ...any) int32 {
	var arg int32
	if len(va) > 0 && cmd != F_GETLK && cmd != F_SETLK && cmd != F_SETLKW {
		arg = int32(c23.ToInt64(va[0]))
	}
	switch cmd {
	case F_DUPFD, F_DUPFD_CLOEXEC:
		return c23.DupFrom(fd, arg, cmd == F_DUPFD_CLOEXEC)
	case F_GETLK, F_SETLK, F_SETLKW:
		return fcntlLock(fd, cmd, c23.VaArg[c23.Ptr](c23.VaStart(va)))
	}
	c23.FdMu.Lock()
	defer c23.FdMu.Unlock()
	e := c23.FdEnt(fd)
	if e == nil {
		c23.SetErrno(c23.EBADF)
		return -1
	}
	switch cmd {
	case F_GETFD:
		return c23.B2I(e.Cloexec)
	case F_SETFD:
		e.Cloexec = arg&FD_CLOEXEC != 0
		return 0
	case F_GETFL:
		return e.Of.Flags
	case F_SETFL:
		const settable = c23.O_APPEND | c23.O_NONBLOCK
		e.Of.Flags = e.Of.Flags&^settable | arg&settable
		return 0
	}
	c23.SetErrno(c23.EINVAL)
	return -1
}
