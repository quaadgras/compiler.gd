// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package ulimit is ulimit.h's function for cmd/cc.
package ulimit

import (
	c23 "internal/c23"
)

const (
	UL_GETFSIZE = 1
	UL_SETFSIZE = 2
)

// longMax is LONG_MAX.
var longMax = int64(uint64(1)<<(8*c23.LongSize-1) - 1)

// Ulimit is ulimit: the limit on a file's size, in 512-byte blocks (the
// system's RLIMIT_FSIZE where it has one; else no limit, LONG_MAX, as
// glibc says of RLIM_INFINITY), which UL_SETFSIZE sets.
func Ulimit(cmd int32, va ...any) int64 {
	switch cmd {
	case UL_GETFSIZE:
		v, err := getFsize()
		if err != nil {
			c23.SetErrnoOf(err)
			return -1
		}
		return v
	case UL_SETFSIZE:
		if len(va) == 0 {
			c23.SetErrno(c23.EINVAL)
			return -1
		}
		n := c23.ToInt64(va[0])
		if n < 0 {
			c23.SetErrno(c23.EINVAL)
			return -1
		}
		if err := setFsize(n); err != nil {
			c23.SetErrnoOf(err)
			return -1
		}
		return n
	}
	c23.SetErrno(c23.EINVAL)
	return -1
}
