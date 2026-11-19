// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sched

import (
	c23 "internal/c23"
	"runtime"
)

const SCHED_OTHER = 0

const SCHED_FIFO = 1

const SCHED_RR = 2

const SCHED_BATCH = 3

const SCHED_IDLE = 5

// SchedGetcpu is sched_getcpu, which Go cannot know (a goroutine moves
// between the system's threads, and they between processors): 0.
func SchedGetcpu() int32 { return 0 }

// SchedGetaffinity is sched_getaffinity: the processors the program may
// use, runtime.NumCPU of them, as the first bits of the cpu_set_t of n
// bytes at mask.
func SchedGetaffinity(pid int32, n uint64, mask c23.Ptr) int32 {
	cpus := runtime.NumCPU()
	if n < uint64(cpus+7)/8 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	b := c23.BytesAt(mask, uintptr(n))
	clear(b)
	for i := range cpus {
		b[i/8] |= 1 << (i % 8)
	}
	return 0
}

// SchedSetaffinity is sched_setaffinity, which is accepted and ignored
// unless the set is empty (EINVAL).
func SchedSetaffinity(pid int32, n uint64, mask c23.Ptr) int32 {
	for _, c := range c23.BytesAt(mask, uintptr(n)) {
		if c != 0 {
			return 0
		}
	}
	c23.SetErrno(c23.EINVAL)
	return -1
}

func SchedGetPriorityMax(policy int32) int32 {
	switch policy {
	case SCHED_FIFO, SCHED_RR:
		return 99
	case SCHED_OTHER, SCHED_BATCH, SCHED_IDLE:
		return 0
	}
	c23.SetErrno(c23.EINVAL)
	return -1
}

func SchedGetPriorityMin(policy int32) int32 {
	switch policy {
	case SCHED_FIFO, SCHED_RR:
		return 1
	case SCHED_OTHER, SCHED_BATCH, SCHED_IDLE:
		return 0
	}
	c23.SetErrno(c23.EINVAL)
	return -1
}

// SchedGetscheduler is sched_getscheduler: every thread is SCHED_OTHER.
func SchedGetscheduler(pid int32) int32 { return SCHED_OTHER }

// SchedGetparam is sched_getparam: priority 0, SCHED_OTHER's.
func SchedGetparam(pid int32, param c23.Ptr) int32 {
	c23.Store32(param, 0)
	return 0
}
