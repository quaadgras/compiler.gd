// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fcntl

import (
	c23 "internal/c23"
	"io/fs"
	"os"
)

const AT_FDCWD = c23.AT_FDCWD

// Openat is openat: open of path relative to the directory of dirfd
// (c23.AtPath).
func Openat(dirfd int32, path *c23.Void, flags int32, va ...any) int32 {
	name, ok := c23.AtName(dirfd, path)
	if !ok {
		return -1
	}
	var mode uint32
	if flags&c23.O_CREAT != 0 && len(va) > 0 {
		mode = uint32(c23.ToInt64(va[0]))
	}
	return c23.Open(name, flags, mode)
}

// posix_fadvise's advice.
const (
	POSIX_FADV_NORMAL     = 0
	POSIX_FADV_RANDOM     = 1
	POSIX_FADV_SEQUENTIAL = 2
	POSIX_FADV_WILLNEED   = 3
	POSIX_FADV_DONTNEED   = 4
	POSIX_FADV_NOREUSE    = 5
)

// PosixFadvise is posix_fadvise, which takes no advice: it checks its
// arguments and returns an error number (EBADF, ESPIPE, EINVAL) or 0.
func PosixFadvise(fd int32, off, n int64, advice int32) int32 {
	of := c23.FdFile(fd)
	switch {
	case of == nil:
		return c23.EBADF
	case of.Sp != nil:
		return c23.ESPIPE
	case advice < POSIX_FADV_NORMAL || advice > POSIX_FADV_NOREUSE || n < 0:
		return c23.EINVAL
	}
	if fi, err := of.F.Stat(); err == nil && fi.Mode()&fs.ModeNamedPipe != 0 {
		return c23.ESPIPE
	}
	return 0
}

// PosixFallocate is posix_fallocate: the file is made at least off+n
// bytes long (by truncate, so the space is not reserved).
func PosixFallocate(fd int32, off, n int64) int32 {
	of := c23.FdFile(fd)
	switch {
	case of == nil:
		return c23.EBADF
	case of.Sp != nil:
		return c23.ESPIPE
	case off < 0 || n <= 0:
		return c23.EINVAL
	case of.Flags&c23.O_ACCMODE == c23.O_RDONLY:
		return c23.EBADF
	}
	fi, err := of.F.Stat()
	if err != nil {
		return c23.ErrnoOf(err)
	}
	if !fi.Mode().IsRegular() {
		return c23.ENODEV
	}
	if fi.Size() < off+n {
		if err := of.F.Truncate(off + n); err != nil {
			return c23.ErrnoOf(err)
		}
	}
	return 0
}

// lockf's commands.
const (
	F_ULOCK = 0
	F_LOCK  = 1
	F_TLOCK = 2
	F_TEST  = 3
)

// Lockf is lockf (unistd.h), on fcntl's record locks: a write lock of n
// bytes from the file's offset (to its end and beyond if n is 0, before
// the offset if n is negative). F_TEST fails with EACCES if another
// process holds a lock there.
func Lockf(fd, cmd int32, n int64) int32 {
	of := c23.FdFile(fd)
	if of == nil || of.F == nil {
		c23.SetErrno(c23.EBADF)
		return -1
	}
	l := flock{typ: F_WRLCK, whence: 1, len: n}
	if n < 0 {
		l.start, l.len = n, -n
	}
	var lcmd int32
	switch cmd {
	case F_ULOCK:
		l.typ, lcmd = F_UNLCK, F_SETLK
	case F_LOCK:
		lcmd = F_SETLKW
	case F_TLOCK:
		lcmd = F_SETLK
	case F_TEST:
		lcmd = F_GETLK
	default:
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	if cmd != F_ULOCK && cmd != F_TEST && of.Flags&c23.O_ACCMODE == c23.O_RDONLY {
		c23.SetErrno(c23.EBADF)
		return -1
	}
	if err := lockFile(of.F, lcmd, &l); err != nil {
		c23.SetErrnoOf(err)
		return -1
	}
	if cmd == F_TEST && l.typ != F_UNLCK && l.pid != int32(os.Getpid()) {
		c23.SetErrno(c23.EACCES)
		return -1
	}
	return 0
}
