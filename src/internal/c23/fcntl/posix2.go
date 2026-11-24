// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fcntl

import (
	c23 "internal/c23"
)

const F_GETLK = 5

const F_SETLK = 6

const F_SETLKW = 7

const F_RDLCK = 0

const F_WRLCK = 1

const F_UNLCK = 2

// A flock is C's struct flock {short l_type; short l_whence; off_t
// l_start; off_t l_len; pid_t l_pid;}, which is at offsets 0, 2, 8, 16
// and 24.
type flock struct {
	typ, whence int16
	start, len  int64
	pid         int32
}

// fcntlLock is fcntl's F_GETLK, F_SETLK and F_SETLKW of the struct flock
// at p.
func fcntlLock(fd, cmd int32, p *c23.Void) int32 {
	of := c23.FdFile(fd)
	if of == nil {
		c23.SetErrno(c23.EBADF)
		return -1
	}
	l := flock{
		typ:    int16(c23.Load16(p)),
		whence: int16(c23.Load16(c23.Add(p, 2))),
		start:  int64(c23.Load64(c23.Add(p, 8))),
		len:    int64(c23.Load64(c23.Add(p, 16))),
	}
	if l.typ < F_RDLCK || l.typ > F_UNLCK || l.whence < 0 || l.whence > 2 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	acc := of.Flags & c23.O_ACCMODE
	if cmd != F_GETLK && (l.typ == F_RDLCK && acc == c23.O_WRONLY || l.typ == F_WRLCK && acc == c23.O_RDONLY) {
		c23.SetErrno(c23.EBADF)
		return -1
	}
	if err := lockFile(of.F, cmd, &l); err != nil {
		c23.SetErrnoOf(err)
		return -1
	}
	if cmd == F_GETLK {
		c23.Store16(p, uint16(l.typ))
		if l.typ != F_UNLCK {
			c23.Store16(c23.Add(p, 2), uint16(l.whence))
			c23.Store64(c23.Add(p, 8), uint64(l.start))
			c23.Store64(c23.Add(p, 16), uint64(l.len))
			c23.Store32(c23.Add(p, 24), uint32(l.pid))
		}
	}
	return 0
}
