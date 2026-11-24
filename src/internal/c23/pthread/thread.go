// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pthread

import (
	c23 "internal/c23"
	"runtime"
	"slices"
	"sync"
	"time"
)

// PthreadCreate is pthread_create: of attr (if not null), the thread
// honors the detach state, and the scheduling policy and priority (kept,
// changing nothing) if it says PTHREAD_EXPLICIT_SCHED; the stack's size
// and address are not used (a goroutine's stack grows).
func PthreadCreate(thr, attr, start, arg *c23.Void) int32 {
	f := c23.FuncOf[func(*c23.Void) *c23.Void](start)
	a := defaultAttr
	if c23.Addr(attr) != 0 {
		a = loadAttr(attr)
	}
	t := c23.SpawnThread(thr, a.detach == PTHREAD_CREATE_DETACHED, func(t *c23.Thread) { t.Ret = f(arg) })
	if a.inherit == PTHREAD_EXPLICIT_SCHED {
		c23.ThreadsMu.Lock()
		t.Policy, t.Priority = a.policy, a.priority
		c23.ThreadsMu.Unlock()
	}
	return 0
}

// PthreadExit is pthread_exit: the thread's cleanup handlers run, the
// last pushed first, then its thread-specific data's destructors.
func PthreadExit(ret *c23.Void) { c23.ExitThread(ret) }

// PthreadJoin is pthread_join, a cancellation point.
func PthreadJoin(id uint64, ret *c23.Void) int32 {
	t, e := c23.JoinErr(id)
	if e != 0 {
		return e
	}
	if c23.Addr(ret) != 0 {
		c23.StorePtr(ret, t.Ret)
	}
	return 0
}

// PthreadDetach is pthread_detach: ESRCH for a thread that is not one,
// EINVAL for one already detached.
func PthreadDetach(id uint64) int32 { return c23.Detach(id) }

const PTHREAD_MUTEX_ERRORCHECK = 2

var conds sync.Map // uintptr -> *cond

func mutexOf(p *c23.Void) *c23.Mutex {
	c23.Check(p, 4)
	a := c23.Addr(p)
	if m, ok := c23.Mutexes.Load(a); ok {
		return m.(*c23.Mutex)
	}
	m, _ := c23.Mutexes.LoadOrStore(a, new(c23.Mutex))
	return m.(*c23.Mutex)
}

// PthreadMutexInit is pthread_mutex_init, of the type, robustness,
// protocol and priority ceiling attr has (a normal mutex if attr is
// null).
func PthreadMutexInit(m, attr *c23.Void) int32 {
	c23.Check(m, 4)
	mu := new(c23.Mutex)
	if c23.Addr(attr) != 0 {
		a := c23.Load32(attr)
		mu.Kind = int32(a & maTypeMask)
		mu.Robust = a&maRobust != 0
		mu.Protocol = int32(a>>maProtocolShift) & 3
		mu.Ceiling = maCeiling(a)
	}
	c23.Mutexes.Store(c23.Addr(m), mu)
	return 0
}

func PthreadMutexDestroy(m *c23.Void) int32 {
	if mu, ok := c23.Mutexes.Load(c23.Addr(m)); ok && mu.(*c23.Mutex).Owner.Load() == nil && mu.(*c23.Mutex).Mu.TryLock() {
		c23.Mutexes.Delete(c23.Addr(m))
	}
	return 0
}

func PthreadMutexLock(m *c23.Void) int32 { return mutexOf(m).Lock(c23.LockWait, time.Time{}) }

func PthreadMutexTrylock(m *c23.Void) int32 { return mutexOf(m).Lock(c23.LockTry, time.Time{}) }

// PthreadMutexTimedlock is pthread_mutex_timedlock, which gives up at
// the CLOCK_REALTIME time at ts.
func PthreadMutexTimedlock(m, ts *c23.Void) int32 {
	d, ok := c23.LoadDeadline(ts)
	if !ok {
		return c23.EINVAL
	}
	return mutexOf(m).Lock(c23.LockUntil, d)
}

func PthreadMutexUnlock(m *c23.Void) int32 { return mutexOf(m).Unlock() }

// PthreadMutexConsistent is pthread_mutex_consistent.
func PthreadMutexConsistent(m *c23.Void) int32 { return mutexOf(m).Consistent() }

// PthreadMutexGetprioceiling is pthread_mutex_getprioceiling: EINVAL
// unless the mutex's protocol is PTHREAD_PRIO_PROTECT, as glibc's.
func PthreadMutexGetprioceiling(m, ceiling *c23.Void) int32 {
	mu := mutexOf(m)
	if mu.Protocol != PTHREAD_PRIO_PROTECT {
		return c23.EINVAL
	}
	c23.Store32(ceiling, uint32(mu.Ceiling))
	return 0
}

// PthreadMutexSetprioceiling is pthread_mutex_setprioceiling, which
// locks the mutex to change its ceiling, and stores the old one at old
// (if not null). Priorities change nothing.
func PthreadMutexSetprioceiling(m *c23.Void, ceiling int32, old *c23.Void) int32 {
	mu := mutexOf(m)
	if mu.Protocol != PTHREAD_PRIO_PROTECT || ceiling < prioMin || ceiling > prioMax {
		return c23.EINVAL
	}
	if e := mu.Lock(c23.LockWait, time.Time{}); e != 0 && e != c23.EDEADLK && e != c23.EOWNERDEAD {
		return e
	} else if e != c23.EDEADLK {
		defer mu.Unlock()
	}
	if c23.Addr(old) != 0 {
		c23.Store32(old, uint32(mu.Ceiling))
	}
	mu.Ceiling = ceiling
	return 0
}

// pthread_mutexattr_t holds the type (the low 2 bits), the robustness,
// the protocol, whether it is shared and the priority ceiling.
const (
	maTypeMask      = 3
	maRobust        = 1 << 2
	maProtocolShift = 3
	maPshared       = 1 << 7
	maCeilingShift  = 8
)

// maCeiling is the priority ceiling of mutex attributes a: the least
// SCHED_FIFO priority, as glibc's, until one is set.
func maCeiling(a uint32) int32 {
	if c := int32(a>>maCeilingShift) & 0xff; c != 0 {
		return c
	}
	return prioMin
}

// PthreadMutexattrInit is pthread_mutexattr_init.
func PthreadMutexattrInit(attr *c23.Void) int32 {
	c23.Store32(attr, c23.PTHREAD_MUTEX_NORMAL)
	return 0
}

func PthreadMutexattrDestroy(attr *c23.Void) int32 { return 0 }

func PthreadMutexattrSettype(attr *c23.Void, kind int32) int32 {
	if kind < c23.PTHREAD_MUTEX_NORMAL || kind > PTHREAD_MUTEX_ERRORCHECK {
		return c23.EINVAL
	}
	c23.Store32(attr, c23.Load32(attr)&^maTypeMask|uint32(kind))
	return 0
}

func PthreadMutexattrGettype(attr, kind *c23.Void) int32 {
	c23.Store32(kind, c23.Load32(attr)&maTypeMask)
	return 0
}

const (
	PTHREAD_MUTEX_STALLED = 0
	PTHREAD_MUTEX_ROBUST  = 1

	PTHREAD_PRIO_NONE    = 0
	PTHREAD_PRIO_INHERIT = 1
	PTHREAD_PRIO_PROTECT = 2

	PTHREAD_PROCESS_PRIVATE = 0
	PTHREAD_PROCESS_SHARED  = 1
)

// setBits sets the bits of mask in the int at attr to v.
func setBits(attr *c23.Void, mask, v uint32) {
	c23.Store32(attr, c23.Load32(attr)&^mask|v)
}

func PthreadMutexattrSetrobust(attr *c23.Void, robust int32) int32 {
	if robust != PTHREAD_MUTEX_STALLED && robust != PTHREAD_MUTEX_ROBUST {
		return c23.EINVAL
	}
	setBits(attr, maRobust, uint32(robust)*maRobust)
	return 0
}

func PthreadMutexattrGetrobust(attr, robust *c23.Void) int32 {
	c23.Store32(robust, c23.Load32(attr)&maRobust/maRobust)
	return 0
}

// PthreadMutexattrSetpshared is pthread_mutexattr_setpshared: a shared
// mutex is the program's, as there are no other processes to share it.
func PthreadMutexattrSetpshared(attr *c23.Void, pshared int32) int32 {
	if pshared != PTHREAD_PROCESS_PRIVATE && pshared != PTHREAD_PROCESS_SHARED {
		return c23.EINVAL
	}
	setBits(attr, maPshared, uint32(pshared)*maPshared)
	return 0
}

func PthreadMutexattrGetpshared(attr, pshared *c23.Void) int32 {
	c23.Store32(pshared, c23.Load32(attr)&maPshared/maPshared)
	return 0
}

// PthreadMutexattrSetprotocol is pthread_mutexattr_setprotocol: the
// protocol is kept, and priorities change nothing.
func PthreadMutexattrSetprotocol(attr *c23.Void, protocol int32) int32 {
	if protocol < PTHREAD_PRIO_NONE || protocol > PTHREAD_PRIO_PROTECT {
		return c23.EINVAL
	}
	setBits(attr, 3<<maProtocolShift, uint32(protocol)<<maProtocolShift)
	return 0
}

func PthreadMutexattrGetprotocol(attr, protocol *c23.Void) int32 {
	c23.Store32(protocol, c23.Load32(attr)>>maProtocolShift&3)
	return 0
}

// PthreadMutexattrSetprioceiling is pthread_mutexattr_setprioceiling: a
// SCHED_FIFO priority.
func PthreadMutexattrSetprioceiling(attr *c23.Void, ceiling int32) int32 {
	if ceiling < prioMin || ceiling > prioMax {
		return c23.EINVAL
	}
	setBits(attr, 0xff<<maCeilingShift, uint32(ceiling)<<maCeilingShift)
	return 0
}

func PthreadMutexattrGetprioceiling(attr, ceiling *c23.Void) int32 {
	c23.Store32(ceiling, uint32(maCeiling(c23.Load32(attr))))
	return 0
}

// A cond is a pthread_cond_t or cnd_t: the channels of the threads
// waiting for it, which a signal closes (sync.Cond has no timed wait),
// and the clock of its timed waits.
type cond struct {
	mu      sync.Mutex
	waiters []chan struct{}
	clock   int32
}

func condOf(c *c23.Void) *cond {
	c23.Check(c, 4)
	a := c23.Addr(c)
	if v, ok := conds.Load(a); ok {
		return v.(*cond)
	}
	v, _ := conds.LoadOrStore(a, new(cond))
	return v.(*cond)
}

// PthreadCondInit is pthread_cond_init, with the clock attr has (if not
// null) for pthread_cond_timedwait.
func PthreadCondInit(c, attr *c23.Void) int32 {
	c23.Check(c, 4)
	cv := new(cond)
	if c23.Addr(attr) != 0 {
		cv.clock = int32(c23.Load32(attr) &^ caPshared)
	}
	conds.Store(c23.Addr(c), cv)
	return 0
}

func PthreadCondDestroy(c *c23.Void) int32 {
	if v, ok := conds.Load(c23.Addr(c)); ok {
		cv := v.(*cond)
		cv.mu.Lock()
		if len(cv.waiters) == 0 {
			conds.Delete(c23.Addr(c))
		}
		cv.mu.Unlock()
	}
	return 0
}

// PthreadCondWait is pthread_cond_wait, a cancellation point: a
// canceled thread locks the mutex again before its cleanup handlers run.
func PthreadCondWait(c, m *c23.Void) int32 { return condOf(c).wait(mutexOf(m), time.Time{}) }

// PthreadCondTimedwait is pthread_cond_timedwait, which gives up at the
// time at ts on the condition variable's clock (CLOCK_REALTIME unless
// pthread_condattr_setclock chose CLOCK_MONOTONIC).
func PthreadCondTimedwait(c, m, ts *c23.Void) int32 {
	cv := condOf(c)
	d, ok := c23.LoadClockDeadline(cv.clock, ts)
	if !ok {
		return c23.EINVAL
	}
	return cv.wait(mutexOf(m), d)
}

// wait unlocks mu, waits for a signal (or the deadline, if it is not
// zero: ETIMEDOUT) and locks mu again. A recursive mutex is unlocked
// however many times its owner locked it, and locked as many again.
func (cv *cond) wait(mu *c23.Mutex, deadline time.Time) int32 {
	c23.TestCancel()
	ch := make(chan struct{})
	cv.mu.Lock()
	cv.waiters = append(cv.waiters, ch)
	cv.mu.Unlock()

	n, e := mu.Release()
	if e != 0 {
		cv.forget(ch)
		return e
	}
	var tc <-chan time.Time
	if !deadline.IsZero() {
		t := time.NewTimer(time.Until(deadline))
		defer t.Stop()
		tc = t.C
	}
	canceled := false
	select {
	case <-ch:
	case <-tc:
		// Timed out, unless a signal took ch from the waiters
		// meanwhile: then it is that signal's wakeup.
		if cv.forget(ch) {
			e = c23.ETIMEDOUT
		}
	case <-c23.CancelChan():
		canceled = cv.forget(ch)
	}
	if e2 := mu.Reacquire(n); e2 != 0 {
		e = e2
	}
	if canceled {
		c23.TestCancel()
	}
	return e
}

// forget removes ch from the waiters, if a signal did not take it: it
// reports whether it was there.
func (cv *cond) forget(ch chan struct{}) bool {
	cv.mu.Lock()
	defer cv.mu.Unlock()
	if i := slices.Index(cv.waiters, ch); i >= 0 {
		cv.waiters = slices.Delete(cv.waiters, i, i+1)
		return true
	}
	return false
}

func PthreadCondSignal(c *c23.Void) int32 {
	if v, ok := conds.Load(c23.Addr(c)); ok {
		cv := v.(*cond)
		cv.mu.Lock()
		if len(cv.waiters) > 0 {
			close(cv.waiters[0])
			cv.waiters = cv.waiters[1:]
		}
		cv.mu.Unlock()
	}
	return 0
}

func PthreadCondBroadcast(c *c23.Void) int32 {
	if v, ok := conds.Load(c23.Addr(c)); ok {
		cv := v.(*cond)
		cv.mu.Lock()
		for _, ch := range cv.waiters {
			close(ch)
		}
		cv.waiters = nil
		cv.mu.Unlock()
	}
	return 0
}

var onces sync.Map

// PthreadOnce is pthread_once.
func PthreadOnce(once, f *c23.Void) int32 {
	if c23.AtomicLoad[int32](once) != 0 {
		return 0
	}
	v, _ := onces.LoadOrStore(c23.Addr(once), new(sync.Once))
	v.(*sync.Once).Do(func() {
		c23.FuncOf[func()](f)()
		c23.AtomicStore[int32](once, 1)
	})
	return 0
}

// PthreadKeyCreate is pthread_key_create.
func PthreadKeyCreate(k, destructor *c23.Void) int32 {
	c23.KeysMu.Lock()
	defer c23.KeysMu.Unlock()
	if len(c23.Keys) >= 1024 { // PTHREAD_KEYS_MAX
		return c23.EAGAIN
	}
	c23.Keys = append(c23.Keys, c23.Key{Destructor: destructor, Live: true})
	c23.Store32(k, uint32(len(c23.Keys)-1))
	return 0
}

func PthreadKeyDelete(k uint32) int32 {
	c23.KeysMu.Lock()
	defer c23.KeysMu.Unlock()
	if int(k) >= len(c23.Keys) || !c23.Keys[k].Live {
		return c23.EINVAL
	}
	c23.Keys[k] = c23.Key{}
	return 0
}

func PthreadGetspecific(k uint32) *c23.Void {
	return c23.GSelf().Keys[k]
}

func PthreadSetspecific(k uint32, v *c23.Void) int32 {
	c23.KeysMu.Lock()
	ok := int(k) < len(c23.Keys) && c23.Keys[k].Live
	c23.KeysMu.Unlock()
	if !ok {
		return c23.EINVAL
	}
	tb := c23.GSelf()
	if tb.Keys == nil {
		tb.Keys = map[uint32]*c23.Void{}
	}
	tb.Keys[k] = v
	return 0
}

func SchedYield() int32 { runtime.Gosched(); return 0 }
