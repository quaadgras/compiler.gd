// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package file

import (
	c23 "internal/c23"
)

const LOCK_SH = 1

const LOCK_EX = 2

const LOCK_NB = 4

const LOCK_UN = 8

// Flock is flock: a shared (LOCK_SH) or exclusive (LOCK_EX) lock of fd's
// whole file, or its release (LOCK_UN); with LOCK_NB, errno EWOULDBLOCK
// (EAGAIN) rather than waiting for another process's lock.
func Flock(fd int32, op int32) int32 {
	switch op &^ LOCK_NB {
	case LOCK_SH, LOCK_EX, LOCK_UN:
	default:
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	f := c23.FdOS(fd)
	if f == nil {
		return -1
	}
	return c23.ErrRet(flockFile(f, op))
}
