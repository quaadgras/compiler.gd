// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sched

import (
	c23 "internal/c23"
	"os"
	"runtime"
	"sync/atomic"
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

// The program's scheduling policy, as sched_setscheduler set it: which
// changes nothing (Go schedules goroutines). A real-time policy is EPERM,
// as for a process without CAP_SYS_NICE (glibc's; musl's
// sched_setscheduler and sched_setparam are always ENOSYS).
var policy atomic.Int32

// SchedGetscheduler is sched_getscheduler: the program's policy,
// SCHED_OTHER unless sched_setscheduler chose SCHED_BATCH or SCHED_IDLE.
func SchedGetscheduler(pid int32) int32 {
	if !selfPid(pid) {
		return -1
	}
	return policy.Load()
}

// SchedGetparam is sched_getparam: priority 0, that of every policy the
// program may have.
func SchedGetparam(pid int32, param c23.Ptr) int32 {
	if !selfPid(pid) {
		return -1
	}
	c23.Store32(param, 0)
	return 0
}

// selfPid reports whether pid is the program's process (0 is) or one of
// its children, which have its scheduling: ESRCH if not, as the program
// cannot know another's.
func selfPid(pid int32) bool {
	if pid < 0 {
		c23.SetErrno(c23.EINVAL)
		return false
	}
	if pid == 0 || pid == int32(os.Getpid()) {
		return true
	}
	c23.ChildMu.Lock()
	c := c23.Children[pid]
	c23.ChildMu.Unlock()
	if c == nil {
		c23.SetErrno(c23.ESRCH)
		return false
	}
	return true
}

// checkParam checks priority prio for policy p: EINVAL if it is not one
// of p's, EPERM for a real-time policy.
func checkParam(p, prio int32) bool {
	lo, hi := SchedGetPriorityMin(p), SchedGetPriorityMax(p)
	switch {
	case lo < 0 || prio < lo || prio > hi:
		c23.SetErrno(c23.EINVAL)
		return false
	case p == SCHED_FIFO || p == SCHED_RR:
		c23.SetErrno(c23.EPERM)
		return false
	}
	return true
}

// SchedSetscheduler is sched_setscheduler: SCHED_OTHER, SCHED_BATCH and
// SCHED_IDLE (with priority 0) are kept, real-time policies are EPERM.
// It returns 0, as Linux's.
func SchedSetscheduler(pid, p int32, param c23.Ptr) int32 {
	if !selfPid(pid) || !checkParam(p, int32(c23.Load32(param))) {
		return -1
	}
	policy.Store(p)
	return 0
}

// SchedSetparam is sched_setparam: a priority of the program's policy.
func SchedSetparam(pid int32, param c23.Ptr) int32 {
	if !selfPid(pid) || !checkParam(policy.Load(), int32(c23.Load32(param))) {
		return -1
	}
	return 0
}

// SchedRrGetInterval is sched_rr_get_interval: 100ms, SCHED_RR's time
// slice on Linux, whatever the policy.
func SchedRrGetInterval(pid int32, ts c23.Ptr) int32 {
	if !selfPid(pid) {
		return -1
	}
	c23.Store64(ts, 0)
	c23.StoreLong(c23.Add(ts, 8), 100e6)
	return 0
}
