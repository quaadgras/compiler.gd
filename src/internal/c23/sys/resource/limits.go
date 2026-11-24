// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package resource

import (
	c23 "internal/c23"
)

// getpriority's and setpriority's which.
const (
	PRIO_PROCESS = 0
	PRIO_PGRP    = 1
	PRIO_USER    = 2
)

// Getpriority is getpriority: the nice value (from -20 to 19) of a
// process, a process group or a user's processes (the system's; 0 where
// it has none). errno tells -1 from a failure.
func Getpriority(which int32, who uint32) int32 {
	if which < PRIO_PROCESS || which > PRIO_USER {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	v, err := getpriority(int(which), int(int32(who)))
	if err != nil {
		c23.SetErrnoOf(err)
		return -1
	}
	return int32(v)
}

// Setpriority is setpriority: the system's (EPERM where it has none).
func Setpriority(which int32, who uint32, prio int32) int32 {
	if which < PRIO_PROCESS || which > PRIO_USER {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	return c23.ErrRet(setpriority(int(which), int(int32(who)), int(min(max(prio, -20), 19))))
}

// getrlimit's resources: Linux's.
const (
	RLIMIT_CPU        = 0
	RLIMIT_FSIZE      = 1
	RLIMIT_DATA       = 2
	RLIMIT_STACK      = 3
	RLIMIT_CORE       = 4
	RLIMIT_RSS        = 5
	RLIMIT_NPROC      = 6
	RLIMIT_NOFILE     = 7
	RLIMIT_MEMLOCK    = 8
	RLIMIT_AS         = 9
	RLIMIT_LOCKS      = 10
	RLIMIT_SIGPENDING = 11
	RLIMIT_MSGQUEUE   = 12
	RLIMIT_NICE       = 13
	RLIMIT_RTPRIO     = 14
	RLIMIT_RTTIME     = 15
	RLIMIT_NLIMITS    = 16
)

// RLIM_INFINITY is no limit.
const RLIM_INFINITY = ^uint64(0)

// Getrlimit is getrlimit: the system's limits, where it has them, of
// struct rlimit {rlim_t rlim_cur, rlim_max;} (RLIM_INFINITY where there is
// none, or the system has no limits C can see). Go raises the limit of
// open files (RLIMIT_NOFILE) to its maximum when the program starts, so
// that one is not what the program was started with.
func Getrlimit(res int32, rl *c23.Void) int32 {
	if res < 0 || res >= RLIMIT_NLIMITS {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	cur, max, err := getrlimit(res)
	if err != nil {
		c23.SetErrnoOf(err)
		return -1
	}
	c23.Store64(rl, cur)
	c23.Store64(c23.Add(rl, 8), max)
	return 0
}

// Setrlimit is setrlimit: the system's (EPERM where it has none, or
// the system has not the resource).
func Setrlimit(res int32, rl *c23.Void) int32 {
	if res < 0 || res >= RLIMIT_NLIMITS {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	cur, max := c23.Load64(rl), c23.Load64(c23.Add(rl, 8))
	if cur > max {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	return c23.ErrRet(setrlimit(res, cur, max))
}
