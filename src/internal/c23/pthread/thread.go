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

// PthreadCreate is pthread_create.
func PthreadCreate(thr, attr, start, arg c23.Ptr) int32 {
	f := c23.FuncOf[func(c23.Ptr) c23.Ptr](start)
	c23.Spawn(thr, func(t *c23.Thread) { t.Ret = f(arg) })
	return 0
}

// PthreadExit is pthread_exit.
func PthreadExit(ret c23.Ptr) { panic(c23.ThreadExit{Ret: ret}) }

// PthreadJoin is pthread_join.
func PthreadJoin(id uint64, ret c23.Ptr) int32 {
	t := c23.Join(id)
	if t == nil {
		return 3 // ESRCH
	}
	if c23.Addr(ret) != 0 {
		c23.StorePtr(ret, t.Ret)
	}
	return 0
}

func PthreadDetach(id uint64) int32 {
	c23.ThreadsMu.Lock()
	delete(c23.Threads, id)
	c23.ThreadsMu.Unlock()
	return 0
}

const PTHREAD_MUTEX_ERRORCHECK = 2

var conds sync.Map // uintptr -> *cond

func mutexOf(p c23.Ptr) *c23.Mutex {
	c23.Check(p, 4)
	a := c23.Addr(p)
	if m, ok := c23.Mutexes.Load(a); ok {
		return m.(*c23.Mutex)
	}
	m, _ := c23.Mutexes.LoadOrStore(a, new(c23.Mutex))
	return m.(*c23.Mutex)
}

// PthreadMutexInit is pthread_mutex_init, of the type attr has (a
// normal mutex if attr is null).
func PthreadMutexInit(m, attr c23.Ptr) int32 {
	c23.Check(m, 4)
	mu := new(c23.Mutex)
	if c23.Addr(attr) != 0 {
		mu.Kind = int32(c23.Load32(attr))
	}
	c23.Mutexes.Store(c23.Addr(m), mu)
	return 0
}

func PthreadMutexDestroy(m c23.Ptr) int32 {
	if mu, ok := c23.Mutexes.Load(c23.Addr(m)); ok && mu.(*c23.Mutex).Owner.Load() == nil && mu.(*c23.Mutex).Mu.TryLock() {
		c23.Mutexes.Delete(c23.Addr(m))
	}
	return 0
}

func PthreadMutexLock(m c23.Ptr) int32 { return mutexOf(m).Lock(c23.LockWait, time.Time{}) }

func PthreadMutexTrylock(m c23.Ptr) int32 { return mutexOf(m).Lock(c23.LockTry, time.Time{}) }

// PthreadMutexTimedlock is pthread_mutex_timedlock, which gives up at
// the CLOCK_REALTIME time at ts.
func PthreadMutexTimedlock(m, ts c23.Ptr) int32 {
	d, ok := c23.LoadDeadline(ts)
	if !ok {
		return c23.EINVAL
	}
	return mutexOf(m).Lock(c23.LockUntil, d)
}

func PthreadMutexUnlock(m c23.Ptr) int32 {
	mu := mutexOf(m)
	if mu.Kind != c23.PTHREAD_MUTEX_NORMAL {
		if mu.Owner.Load() != c23.GSelf() {
			return c23.EPERM
		}
		if mu.Count--; mu.Count > 0 {
			return 0
		}
		mu.Owner.Store(nil)
	}
	mu.Mu.Unlock()
	return 0
}

// PthreadMutexattrInit is pthread_mutexattr_init: pthread_mutexattr_t
// holds the type.
func PthreadMutexattrInit(attr c23.Ptr) int32 { c23.Store32(attr, c23.PTHREAD_MUTEX_NORMAL); return 0 }

func PthreadMutexattrDestroy(attr c23.Ptr) int32 { return 0 }

func PthreadMutexattrSettype(attr c23.Ptr, kind int32) int32 {
	if kind < c23.PTHREAD_MUTEX_NORMAL || kind > PTHREAD_MUTEX_ERRORCHECK {
		return c23.EINVAL
	}
	c23.Store32(attr, uint32(kind))
	return 0
}

func PthreadMutexattrGettype(attr, kind c23.Ptr) int32 {
	c23.Store32(kind, c23.Load32(attr))
	return 0
}

// A cond is a pthread_cond_t or cnd_t: the channels of the threads
// waiting for it, which a signal closes (sync.Cond has no timed wait).
type cond struct {
	mu      sync.Mutex
	waiters []chan struct{}
}

func condOf(c c23.Ptr) *cond {
	c23.Check(c, 4)
	a := c23.Addr(c)
	if v, ok := conds.Load(a); ok {
		return v.(*cond)
	}
	v, _ := conds.LoadOrStore(a, new(cond))
	return v.(*cond)
}

func PthreadCondInit(c, attr c23.Ptr) int32 { return 0 }

func PthreadCondDestroy(c c23.Ptr) int32 {
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

// PthreadCondWait is pthread_cond_wait.
func PthreadCondWait(c, m c23.Ptr) int32 { return condOf(c).wait(mutexOf(m), time.Time{}) }

// PthreadCondTimedwait is pthread_cond_timedwait, which gives up at the
// CLOCK_REALTIME time at ts.
func PthreadCondTimedwait(c, m, ts c23.Ptr) int32 {
	d, ok := c23.LoadDeadline(ts)
	if !ok {
		return c23.EINVAL
	}
	return condOf(c).wait(mutexOf(m), d)
}

// wait unlocks mu, waits for a signal (or the deadline, if it is not
// zero: ETIMEDOUT) and locks mu again. A recursive mutex is unlocked
// however many times its owner locked it, and locked as many again.
func (cv *cond) wait(mu *c23.Mutex, deadline time.Time) int32 {
	self := c23.GSelf()
	if mu.Kind != c23.PTHREAD_MUTEX_NORMAL && mu.Owner.Load() != self {
		return c23.EPERM
	}
	ch := make(chan struct{})
	cv.mu.Lock()
	cv.waiters = append(cv.waiters, ch)
	cv.mu.Unlock()

	n := mu.Count
	if mu.Kind != c23.PTHREAD_MUTEX_NORMAL {
		mu.Owner.Store(nil)
	}
	mu.Mu.Unlock()
	var e int32
	if deadline.IsZero() {
		<-ch
	} else {
		t := time.NewTimer(time.Until(deadline))
		select {
		case <-ch:
		case <-t.C:
			// Timed out, unless a signal took ch from the waiters
			// meanwhile: then it is that signal's wakeup.
			cv.mu.Lock()
			if i := slices.Index(cv.waiters, ch); i >= 0 {
				cv.waiters = slices.Delete(cv.waiters, i, i+1)
				e = c23.ETIMEDOUT
			}
			cv.mu.Unlock()
		}
		t.Stop()
	}
	mu.Mu.Lock()
	if mu.Kind != c23.PTHREAD_MUTEX_NORMAL {
		mu.Owner.Store(self)
		mu.Count = n
	}
	return e
}

func PthreadCondSignal(c c23.Ptr) int32 {
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

func PthreadCondBroadcast(c c23.Ptr) int32 {
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
func PthreadOnce(once, f c23.Ptr) int32 {
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
func PthreadKeyCreate(k, destructor c23.Ptr) int32 {
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

func PthreadGetspecific(k uint32) c23.Ptr {
	return c23.GSelf().Keys[k]
}

func PthreadSetspecific(k uint32, v c23.Ptr) int32 {
	c23.KeysMu.Lock()
	ok := int(k) < len(c23.Keys) && c23.Keys[k].Live
	c23.KeysMu.Unlock()
	if !ok {
		return c23.EINVAL
	}
	tb := c23.GSelf()
	if tb.Keys == nil {
		tb.Keys = map[uint32]c23.Ptr{}
	}
	tb.Keys[k] = v
	return 0
}

func SchedYield() int32 { runtime.Gosched(); return 0 }
