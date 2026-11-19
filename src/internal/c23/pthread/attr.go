// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pthread

import (
	c23 "internal/c23"
	"sync"
)

// Threads' attributes and scheduling, cancellation, cleanup handlers,
// and the attributes of condition variables, read-write locks and
// barriers.

const (
	PTHREAD_CREATE_JOINABLE = 0
	PTHREAD_CREATE_DETACHED = 1

	PTHREAD_INHERIT_SCHED  = 0
	PTHREAD_EXPLICIT_SCHED = 1

	PTHREAD_SCOPE_SYSTEM  = 0
	PTHREAD_SCOPE_PROCESS = 1

	SCHED_OTHER = 0
	SCHED_FIFO  = 1
	SCHED_RR    = 2

	// SCHED_FIFO's and SCHED_RR's priorities, Linux's.
	prioMin = 1
	prioMax = 99

	// PTHREAD_STACK_MIN, glibc's.
	stackMin = 16384
)

// An attr is a pthread_attr_t: {void *__p; unsigned long __l[2]; int
// __i[6];}, the stack's address, its size and the guard's size, then the
// detach state, the scheduling inheritance, policy and priority, and the
// contention scope.
type attr struct {
	stack                   c23.Ptr
	stackSize, guardSize    uint64
	detach, inherit         int32
	policy, priority, scope int32
}

var defaultAttr = attr{stackSize: 8 << 20, guardSize: 4096} // glibc's, on Linux

func attrInt(a c23.Ptr, i int) c23.Ptr { return c23.Add(a, 3*int(c23.WordSize)+4*i) }

func loadAttr(a c23.Ptr) attr {
	return attr{
		stack:     c23.LoadPtr(a),
		stackSize: uint64(c23.LoadLong(c23.Add(a, int(c23.WordSize)))),
		guardSize: uint64(c23.LoadLong(c23.Add(a, 2*int(c23.WordSize)))),
		detach:    int32(c23.Load32(attrInt(a, 0))),
		inherit:   int32(c23.Load32(attrInt(a, 1))),
		policy:    int32(c23.Load32(attrInt(a, 2))),
		priority:  int32(c23.Load32(attrInt(a, 3))),
		scope:     int32(c23.Load32(attrInt(a, 4))),
	}
}

func storeAttr(a c23.Ptr, v attr) {
	c23.StorePtr(a, v.stack)
	c23.StoreLong(c23.Add(a, int(c23.WordSize)), int64(v.stackSize))
	c23.StoreLong(c23.Add(a, 2*int(c23.WordSize)), int64(v.guardSize))
	for i, x := range [...]int32{v.detach, v.inherit, v.policy, v.priority, v.scope, 0} {
		c23.Store32(attrInt(a, i), uint32(x))
	}
}

// PthreadAttrInit is pthread_attr_init: a joinable thread, with an 8 MiB
// stack and a page of guard, of its creator's scheduling.
func PthreadAttrInit(a c23.Ptr) int32 { storeAttr(a, defaultAttr); return 0 }

func PthreadAttrDestroy(a c23.Ptr) int32 { return 0 }

func PthreadAttrSetdetachstate(a c23.Ptr, state int32) int32 {
	if state != PTHREAD_CREATE_JOINABLE && state != PTHREAD_CREATE_DETACHED {
		return c23.EINVAL
	}
	c23.Store32(attrInt(a, 0), uint32(state))
	return 0
}

func PthreadAttrGetdetachstate(a, state c23.Ptr) int32 {
	c23.Store32(state, c23.Load32(attrInt(a, 0)))
	return 0
}

// PthreadAttrSetguardsize is pthread_attr_setguardsize: kept, a
// goroutine's stack has no guard.
func PthreadAttrSetguardsize(a c23.Ptr, size uint64) int32 {
	c23.StoreLong(c23.Add(a, 2*int(c23.WordSize)), int64(size))
	return 0
}

func PthreadAttrGetguardsize(a, size c23.Ptr) int32 {
	c23.StoreLong(size, c23.LoadLong(c23.Add(a, 2*int(c23.WordSize))))
	return 0
}

// PthreadAttrSetstacksize is pthread_attr_setstacksize: kept, as a
// goroutine's stack grows. EINVAL below PTHREAD_STACK_MIN.
func PthreadAttrSetstacksize(a c23.Ptr, size uint64) int32 {
	if size < stackMin {
		return c23.EINVAL
	}
	c23.StoreLong(c23.Add(a, int(c23.WordSize)), int64(size))
	return 0
}

func PthreadAttrGetstacksize(a, size c23.Ptr) int32 {
	c23.StoreLong(size, c23.LoadLong(c23.Add(a, int(c23.WordSize))))
	return 0
}

// PthreadAttrSetstack is pthread_attr_setstack: the stack is kept, and
// not used.
func PthreadAttrSetstack(a, stack c23.Ptr, size uint64) int32 {
	if size < stackMin {
		return c23.EINVAL
	}
	c23.StorePtr(a, stack)
	c23.StoreLong(c23.Add(a, int(c23.WordSize)), int64(size))
	return 0
}

func PthreadAttrGetstack(a, stack, size c23.Ptr) int32 {
	c23.StorePtr(stack, c23.LoadPtr(a))
	c23.StoreLong(size, c23.LoadLong(c23.Add(a, int(c23.WordSize))))
	return 0
}

func PthreadAttrSetinheritsched(a c23.Ptr, inherit int32) int32 {
	if inherit != PTHREAD_INHERIT_SCHED && inherit != PTHREAD_EXPLICIT_SCHED {
		return c23.EINVAL
	}
	c23.Store32(attrInt(a, 1), uint32(inherit))
	return 0
}

func PthreadAttrGetinheritsched(a, inherit c23.Ptr) int32 {
	c23.Store32(inherit, c23.Load32(attrInt(a, 1)))
	return 0
}

// PthreadAttrSetschedpolicy is pthread_attr_setschedpolicy: SCHED_OTHER,
// SCHED_FIFO or SCHED_RR, as glibc's.
func PthreadAttrSetschedpolicy(a c23.Ptr, policy int32) int32 {
	if policy != SCHED_OTHER && policy != SCHED_FIFO && policy != SCHED_RR {
		return c23.EINVAL
	}
	c23.Store32(attrInt(a, 2), uint32(policy))
	return 0
}

func PthreadAttrGetschedpolicy(a, policy c23.Ptr) int32 {
	c23.Store32(policy, c23.Load32(attrInt(a, 2)))
	return 0
}

// prioValid reports whether prio is a priority of policy.
func prioValid(policy, prio int32) bool {
	switch policy {
	case SCHED_FIFO, SCHED_RR:
		return prio >= prioMin && prio <= prioMax
	}
	return prio == 0
}

// PthreadAttrSetschedparam is pthread_attr_setschedparam: param's
// priority (struct sched_param {int sched_priority;}), which must be one
// of the attributes' policy.
func PthreadAttrSetschedparam(a, param c23.Ptr) int32 {
	prio := int32(c23.Load32(param))
	if !prioValid(int32(c23.Load32(attrInt(a, 2))), prio) {
		return c23.EINVAL
	}
	c23.Store32(attrInt(a, 3), uint32(prio))
	return 0
}

func PthreadAttrGetschedparam(a, param c23.Ptr) int32 {
	c23.Store32(param, c23.Load32(attrInt(a, 3)))
	return 0
}

// PthreadAttrSetscope is pthread_attr_setscope: PTHREAD_SCOPE_PROCESS is
// not supported (ENOTSUP), as on Linux.
func PthreadAttrSetscope(a c23.Ptr, scope int32) int32 {
	switch scope {
	case PTHREAD_SCOPE_SYSTEM:
		c23.Store32(attrInt(a, 4), uint32(scope))
		return 0
	case PTHREAD_SCOPE_PROCESS:
		return c23.EOPNOTSUPP // ENOTSUP
	}
	return c23.EINVAL
}

func PthreadAttrGetscope(a, scope c23.Ptr) int32 {
	c23.Store32(scope, c23.Load32(attrInt(a, 4)))
	return 0
}

// Scheduling: each thread keeps a policy and priority, which change
// nothing (Go schedules goroutines). The program runs as a user without
// privileges: a real-time policy is EPERM.

// PthreadGetschedparam is pthread_getschedparam.
func PthreadGetschedparam(id uint64, policy, param c23.Ptr) int32 {
	t := c23.ThreadOf(id)
	if t == nil {
		return c23.ESRCH
	}
	c23.ThreadsMu.Lock()
	p, prio := t.Policy, t.Priority
	c23.ThreadsMu.Unlock()
	c23.Store32(policy, uint32(p))
	c23.Store32(param, uint32(prio))
	return 0
}

// PthreadSetschedparam is pthread_setschedparam: SCHED_OTHER (with
// priority 0) is accepted, SCHED_FIFO and SCHED_RR are EPERM, as for a
// process without CAP_SYS_NICE.
func PthreadSetschedparam(id uint64, policy int32, param c23.Ptr) int32 {
	t := c23.ThreadOf(id)
	if t == nil {
		return c23.ESRCH
	}
	prio := int32(c23.Load32(param))
	switch {
	case policy != SCHED_OTHER && policy != SCHED_FIFO && policy != SCHED_RR, !prioValid(policy, prio):
		return c23.EINVAL
	case policy != SCHED_OTHER:
		return c23.EPERM
	}
	c23.ThreadsMu.Lock()
	t.Policy, t.Priority = policy, prio
	c23.ThreadsMu.Unlock()
	return 0
}

// PthreadSetschedprio is pthread_setschedprio: a priority of the
// thread's policy.
func PthreadSetschedprio(id uint64, prio int32) int32 {
	t := c23.ThreadOf(id)
	if t == nil {
		return c23.ESRCH
	}
	c23.ThreadsMu.Lock()
	defer c23.ThreadsMu.Unlock()
	if !prioValid(t.Policy, prio) {
		return c23.EINVAL
	}
	t.Priority = prio
	return 0
}

var concurrency int32

// PthreadSetconcurrency is pthread_setconcurrency: a hint, kept.
func PthreadSetconcurrency(n int32) int32 {
	if n < 0 {
		return c23.EINVAL
	}
	concMu.Lock()
	concurrency = n
	concMu.Unlock()
	return 0
}

// PthreadGetconcurrency is pthread_getconcurrency.
func PthreadGetconcurrency() int32 {
	concMu.Lock()
	defer concMu.Unlock()
	return concurrency
}

var concMu sync.Mutex

// PthreadGetcpuclockid is pthread_getcpuclockid: the thread's CPU-time
// clock, which counts the time since it started (Go cannot know a
// goroutine's processor time).
func PthreadGetcpuclockid(id uint64, clock c23.Ptr) int32 {
	if c23.ThreadOf(id) == nil {
		return c23.ESRCH
	}
	c23.Store32(clock, uint32(c23.ThreadCPUClock(id)))
	return 0
}

// pthread_atfork's handlers: kept, and never called, as there is no
// fork.
var (
	atforkMu sync.Mutex
	atfork   [][3]c23.Ptr
)

// PthreadAtfork is pthread_atfork.
func PthreadAtfork(prepare, parent, child c23.Ptr) int32 {
	atforkMu.Lock()
	atfork = append(atfork, [3]c23.Ptr{prepare, parent, child})
	atforkMu.Unlock()
	return 0
}

// Cancellation (internal/c23's cancel.go).

const (
	PTHREAD_CANCEL_ENABLE  = 0
	PTHREAD_CANCEL_DISABLE = 1

	PTHREAD_CANCEL_DEFERRED     = 0
	PTHREAD_CANCEL_ASYNCHRONOUS = 1
)

// PthreadCancel is pthread_cancel: the thread acts on it at its next
// cancellation point (at once, if it is the calling thread and its
// cancellation is asynchronous). ESRCH for a thread that is not one.
func PthreadCancel(id uint64) int32 {
	t := c23.ThreadOf(id)
	if t == nil {
		return c23.ESRCH
	}
	t.RequestCancel()
	if t == c23.Self() && t.CancelState().Async {
		c23.TestCancel()
	}
	return 0
}

// PthreadSetcancelstate is pthread_setcancelstate.
func PthreadSetcancelstate(state int32, old c23.Ptr) int32 {
	if state != PTHREAD_CANCEL_ENABLE && state != PTHREAD_CANCEL_DISABLE {
		return c23.EINVAL
	}
	var cs c23.Cancel
	c := &cs
	if t := c23.Self(); t != nil {
		c = t.CancelState()
	}
	if c23.Addr(old) != 0 {
		c23.Store32(old, uint32(c23.B2I(c.Disabled)))
	}
	c.Disabled = state == PTHREAD_CANCEL_DISABLE
	return 0
}

// PthreadSetcanceltype is pthread_setcanceltype: asynchronous
// cancellation acts at the same points as deferred cancellation, and
// when it is chosen.
func PthreadSetcanceltype(typ int32, old c23.Ptr) int32 {
	if typ != PTHREAD_CANCEL_DEFERRED && typ != PTHREAD_CANCEL_ASYNCHRONOUS {
		return c23.EINVAL
	}
	var cs c23.Cancel
	c := &cs
	if t := c23.Self(); t != nil {
		c = t.CancelState()
	}
	if c23.Addr(old) != 0 {
		c23.Store32(old, uint32(c23.B2I(c.Async)))
	}
	c.Async = typ == PTHREAD_CANCEL_ASYNCHRONOUS
	if c.Async {
		c23.TestCancel()
	}
	return 0
}

// PthreadTestcancel is pthread_testcancel.
func PthreadTestcancel() { c23.TestCancel() }

// PthreadCleanupPush is musl's _pthread_cleanup_push, which
// pthread_cleanup_push calls with a struct __ptcb of its block (not
// used: the handlers are the thread's).
func PthreadCleanupPush(cb, f, arg c23.Ptr) { c23.CleanupPush(f, arg) }

// PthreadCleanupPop is musl's _pthread_cleanup_pop.
func PthreadCleanupPop(cb c23.Ptr, run int32) { c23.CleanupPop(run != 0) }

// pthread_condattr_t holds the clock, and whether the condition variable
// is shared in its top bit.
const caPshared = 1 << 31

func PthreadCondattrInit(a c23.Ptr) int32 { c23.Store32(a, 0); return 0 }

func PthreadCondattrDestroy(a c23.Ptr) int32 { return 0 }

// PthreadCondattrSetclock is pthread_condattr_setclock: CLOCK_REALTIME
// or CLOCK_MONOTONIC.
func PthreadCondattrSetclock(a c23.Ptr, clock int32) int32 {
	if clock != c23.CLOCK_REALTIME && clock != c23.CLOCK_MONOTONIC {
		return c23.EINVAL
	}
	setBits(a, ^uint32(caPshared), uint32(clock))
	return 0
}

func PthreadCondattrGetclock(a, clock c23.Ptr) int32 {
	c23.Store32(clock, c23.Load32(a)&^caPshared)
	return 0
}

func PthreadCondattrSetpshared(a c23.Ptr, pshared int32) int32 {
	if pshared != PTHREAD_PROCESS_PRIVATE && pshared != PTHREAD_PROCESS_SHARED {
		return c23.EINVAL
	}
	setBits(a, caPshared, uint32(pshared)<<31)
	return 0
}

func PthreadCondattrGetpshared(a, pshared c23.Ptr) int32 {
	c23.Store32(pshared, c23.Load32(a)>>31)
	return 0
}

// The attributes of read-write locks and barriers: whether they are
// shared, which changes nothing (the program's threads share them).

func PthreadRwlockattrInit(a c23.Ptr) int32 { c23.Store32(a, 0); return 0 }

func PthreadRwlockattrDestroy(a c23.Ptr) int32 { return 0 }

func PthreadRwlockattrSetpshared(a c23.Ptr, pshared int32) int32 { return setPshared(a, pshared) }

func PthreadRwlockattrGetpshared(a, pshared c23.Ptr) int32 {
	c23.Store32(pshared, c23.Load32(a))
	return 0
}

func PthreadBarrierattrInit(a c23.Ptr) int32 { c23.Store32(a, 0); return 0 }

func PthreadBarrierattrDestroy(a c23.Ptr) int32 { return 0 }

func PthreadBarrierattrSetpshared(a c23.Ptr, pshared int32) int32 { return setPshared(a, pshared) }

func PthreadBarrierattrGetpshared(a, pshared c23.Ptr) int32 {
	c23.Store32(pshared, c23.Load32(a))
	return 0
}

func setPshared(a c23.Ptr, pshared int32) int32 {
	if pshared != PTHREAD_PROCESS_PRIVATE && pshared != PTHREAD_PROCESS_SHARED {
		return c23.EINVAL
	}
	c23.Store32(a, uint32(pshared))
	return 0
}
