// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package resource

import (
	c23 "internal/c23"
)

const RUSAGE_SELF = 0

const RUSAGE_CHILDREN = -1

const RUSAGE_THREAD = 1

// Getrusage is getrusage: the system's, where it has it (RUSAGE_THREAD is
// the process's); elsewhere, the time since the program started as its
// user time. usage is a struct rusage: two struct timevals and 14 longs.
func Getrusage(who int32, usage *c23.Void) int32 {
	if who != RUSAGE_SELF && who != RUSAGE_CHILDREN && who != RUSAGE_THREAD {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	ru, err := rusage(who)
	if err != nil {
		c23.SetErrnoOf(err)
		return -1
	}
	for i, v := range ru {
		if i < 4 {
			c23.Store64(c23.Add(usage, 8*i), uint64(v)) // time_t and suseconds_t
		} else {
			c23.StoreLong(c23.Add(usage, 32+int(c23.LongSize)*(i-4)), v)
		}
	}
	return 0
}
