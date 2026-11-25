// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stdlib

import (
	c23 "internal/c23"
	"os"
)

// Pseudo-terminals: the system's, on Linux (/dev/ptmx and devpts) and
// macOS; elsewhere posix_openpt fails with ENOSYS, and no descriptor is
// a pseudo-terminal's master (EINVAL; ENOTTY for ptsname_r).

// PosixOpenpt is posix_openpt: a descriptor of a new pseudo-terminal's
// master, of flags O_RDWR and O_NOCTTY (and O_CLOEXEC).
func PosixOpenpt(flags int32) int32 {
	if flags&^(c23.O_ACCMODE|c23.O_NOCTTY|c23.O_CLOEXEC) != 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	if !havePty {
		c23.SetErrno(c23.ENOSYS)
		return -1
	}
	return c23.Open("/dev/ptmx", flags, 0)
}

// ptyFile is fd's file, or nil with errno EBADF (or EINVAL if it is not
// a file).
func ptyFile(fd int32) *os.File {
	of := c23.FdFile(fd)
	if of == nil {
		c23.SetErrno(c23.EBADF)
		return nil
	}
	if of.F == nil {
		c23.SetErrno(c23.EINVAL)
		return nil
	}
	return of.F
}

// Grantpt is grantpt: the slave of the master fd is the user's (as
// devpts makes it on Linux).
func Grantpt(fd int32) int32 {
	f := ptyFile(fd)
	if f == nil {
		return -1
	}
	if err := grantpt(f); err != nil {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	return 0
}

// Unlockpt is unlockpt: the slave of the master fd may be opened.
func Unlockpt(fd int32) int32 {
	f := ptyFile(fd)
	if f == nil {
		return -1
	}
	if err := unlockpt(f); err != nil {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	return 0
}

var ptsBuf *c23.Void

// Ptsname is ptsname: the name of the slave of the master fd, in an
// array the next call reuses, or null (errno EBADF, ENOTTY).
func Ptsname(fd int32) *c23.Void {
	if c23.Addr(ptsBuf) == 0 {
		ptsBuf = c23.Malloc(128)
	}
	if e := PtsnameR(fd, ptsBuf, 128); e != 0 {
		c23.SetErrno(e)
		return (*c23.Void)(nil)
	}
	return ptsBuf
}

// PtsnameR is ptsname_r: the name in the n bytes at buf; an errno value
// (EBADF, ENOTTY, ERANGE) or 0.
func PtsnameR(fd int32, buf *c23.Void, n uint64) int32 {
	of := c23.FdFile(fd)
	if of == nil {
		return c23.EBADF
	}
	if of.F == nil {
		return c23.ENOTTY
	}
	name, err := ptsname(of.F)
	if err != nil {
		return c23.ENOTTY
	}
	if uint64(len(name)) >= n {
		return c23.ERANGE
	}
	copy(c23.Writable(buf, uintptr(len(name)+1)), name+"\x00")
	return 0
}
