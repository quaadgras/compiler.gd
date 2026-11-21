// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pthread

import (
	c23 "internal/c23"
	"runtime"
	"sync"
	"time"
)

// Read-write locks, barriers and spin locks. Read-write locks and
// barriers are kept by the address of the C object, as mutexes are; a
// spin lock is its int.

// A rwlock is a pthread_rwlock_t: its readers or its writer, and a
// channel closed (and replaced) when it is unlocked, which waiters wait
// on. Readers are preferred, as glibc's default: a thread may take a read
// lock while a writer waits, so a reader may lock it again.
type rwlock struct {
	mu      sync.Mutex
	readers int
	writer  any // the goroutine's c23.GSelf
	changed chan struct{}
}

var rwlocks sync.Map // uintptr -> *rwlock

func rwlockOf(p c23.Ptr) *rwlock {
	c23.Check(p, 4)
	a := c23.Addr(p)
	if v, ok := rwlocks.Load(a); ok {
		return v.(*rwlock)
	}
	v, _ := rwlocks.LoadOrStore(a, &rwlock{changed: make(chan struct{})})
	return v.(*rwlock)
}

// PthreadRwlockInit is pthread_rwlock_init: attr says only whether it
// is shared.
func PthreadRwlockInit(p, attr c23.Ptr) int32 {
	c23.Check(p, 4)
	rwlocks.Store(c23.Addr(p), &rwlock{changed: make(chan struct{})})
	return 0
}

func PthreadRwlockDestroy(p c23.Ptr) int32 {
	rwlocks.Delete(c23.Addr(p))
	return 0
}

// lock takes a read lock, or a write lock if write: if how is
// c23.LockTry, not waiting (EBUSY); if c23.LockUntil, until the deadline
// (ETIMEDOUT). EDEADLK if the calling thread holds the write lock (EBUSY
// if it tries), as glibc's.
func (r *rwlock) lock(write bool, how int, deadline time.Time) int32 {
	self := any(c23.GSelf())
	r.mu.Lock()
	for {
		switch {
		case r.writer == self:
			r.mu.Unlock()
			return c23.If[int32](how == c23.LockTry, c23.EBUSY, c23.EDEADLK)
		case r.writer == nil && !write:
			r.readers++
			r.mu.Unlock()
			return 0
		case r.writer == nil && r.readers == 0:
			r.writer = self
			r.mu.Unlock()
			return 0
		}
		c := r.changed
		r.mu.Unlock()
		switch how {
		case c23.LockTry:
			return c23.EBUSY
		case c23.LockWait:
			<-c
		default:
			d := time.Until(deadline)
			if d <= 0 {
				return c23.ETIMEDOUT
			}
			t := time.NewTimer(d)
			select {
			case <-c:
			case <-t.C:
			}
			t.Stop()
		}
		r.mu.Lock()
	}
}

func PthreadRwlockRdlock(p c23.Ptr) int32 { return rwlockOf(p).lock(false, c23.LockWait, time.Time{}) }

func PthreadRwlockTryrdlock(p c23.Ptr) int32 {
	return rwlockOf(p).lock(false, c23.LockTry, time.Time{})
}

func PthreadRwlockWrlock(p c23.Ptr) int32 { return rwlockOf(p).lock(true, c23.LockWait, time.Time{}) }

func PthreadRwlockTrywrlock(p c23.Ptr) int32 { return rwlockOf(p).lock(true, c23.LockTry, time.Time{}) }

// PthreadRwlockTimedrdlock is pthread_rwlock_timedrdlock, which gives up
// at the CLOCK_REALTIME time at ts.
func PthreadRwlockTimedrdlock(p, ts c23.Ptr) int32 { return timedRwlock(p, ts, false) }

// PthreadRwlockTimedwrlock is pthread_rwlock_timedwrlock.
func PthreadRwlockTimedwrlock(p, ts c23.Ptr) int32 { return timedRwlock(p, ts, true) }

func timedRwlock(p, ts c23.Ptr, write bool) int32 {
	r := rwlockOf(p)
	if e := r.lock(write, c23.LockTry, time.Time{}); e != c23.EBUSY {
		return e
	}
	d, ok := c23.LoadDeadline(ts)
	if !ok {
		return c23.EINVAL
	}
	return r.lock(write, c23.LockUntil, d)
}

// PthreadRwlockUnlock is pthread_rwlock_unlock: the calling thread's
// write lock, or a read lock. EPERM if it is not locked.
func PthreadRwlockUnlock(p c23.Ptr) int32 {
	r := rwlockOf(p)
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case r.writer != nil && r.writer == any(c23.GSelf()):
		r.writer = nil
	case r.readers > 0:
		r.readers--
		if r.readers > 0 {
			return 0
		}
	default:
		return c23.EPERM
	}
	close(r.changed)
	r.changed = make(chan struct{})
	return 0
}

// A barrier is a pthread_barrier_t: how many threads it waits for, how
// many wait, and a channel closed when they are all there.
type barrier struct {
	mu       sync.Mutex
	count, n uint32
	gen      chan struct{}
}

var barriers sync.Map // uintptr -> *barrier

// PTHREAD_BARRIER_SERIAL_THREAD
const barrierSerial = -1

// PthreadBarrierInit is pthread_barrier_init: EINVAL for a count of 0.
func PthreadBarrierInit(p, attr c23.Ptr, count uint32) int32 {
	if count == 0 {
		return c23.EINVAL
	}
	c23.Check(p, 4)
	barriers.Store(c23.Addr(p), &barrier{count: count, gen: make(chan struct{})})
	return 0
}

func PthreadBarrierDestroy(p c23.Ptr) int32 {
	barriers.Delete(c23.Addr(p))
	return 0
}

// PthreadBarrierWait is pthread_barrier_wait: the last thread to arrive
// gets PTHREAD_BARRIER_SERIAL_THREAD, the others 0. EINVAL if the barrier
// was not initialized.
func PthreadBarrierWait(p c23.Ptr) int32 {
	v, ok := barriers.Load(c23.Addr(p))
	if !ok {
		return c23.EINVAL
	}
	b := v.(*barrier)
	b.mu.Lock()
	b.n++
	if b.n == b.count {
		b.n = 0
		close(b.gen)
		b.gen = make(chan struct{})
		b.mu.Unlock()
		return barrierSerial
	}
	g := b.gen
	b.mu.Unlock()
	<-g
	return 0
}

// Spin locks: pthread_spinlock_t is an int, 0 when it is free.

func PthreadSpinInit(p c23.Ptr, pshared int32) int32 { c23.AtomicStore(p, int32(0)); return 0 }

func PthreadSpinDestroy(p c23.Ptr) int32 { return 0 }

// PthreadSpinLock is pthread_spin_lock, which spins, yielding to the
// other goroutines between tries.
func PthreadSpinLock(p c23.Ptr) int32 {
	for i := 0; !c23.AtomicCAS(p, int32(0), int32(1)); i++ {
		if i >= 64 {
			runtime.Gosched()
		}
	}
	return 0
}

func PthreadSpinTrylock(p c23.Ptr) int32 {
	return c23.If[int32](c23.AtomicCAS(p, int32(0), int32(1)), 0, c23.EBUSY)
}

func PthreadSpinUnlock(p c23.Ptr) int32 { c23.AtomicStore(p, int32(0)); return 0 }
