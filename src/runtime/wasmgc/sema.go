// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

import "unsafe"

// Semaphores and notify lists, for package sync. One goroutine runs at
// a time and none is preempted, so a semaphore is its count and the
// goroutines waiting for it, kept by its address.

type semaWaiter struct {
	gp      *g
	t       uint32 // the ticket, for a notify list
	next    *semaWaiter
	granted bool // the semaphore was handed to it
}

type semaQueue struct {
	head, tail *semaWaiter
}

func (q *semaQueue) push(w *semaWaiter) {
	if q.tail == nil {
		q.head = w
	} else {
		q.tail.next = w
	}
	q.tail = w
}

// pushFront queues w first (a waiter that has waited before).
func (q *semaQueue) pushFront(w *semaWaiter) {
	w.next = q.head
	q.head = w
	if q.tail == nil {
		q.tail = w
	}
}

func (q *semaQueue) pop() *semaWaiter {
	w := q.head
	if w != nil {
		q.head = w.next
		if q.head == nil {
			q.tail = nil
		}
	}
	return w
}

var semaWaiters map[uintptr]*semaQueue

func semaQueueOf(addr unsafe.Pointer, create bool) *semaQueue {
	k := uintptr(addr)
	q := semaWaiters[k]
	if q == nil && create {
		if semaWaiters == nil {
			semaWaiters = make(map[uintptr]*semaQueue)
		}
		q = &semaQueue{}
		semaWaiters[k] = q
	}
	return q
}

func semacquire(addr *uint32, why string) { semacquire1(addr, false, why) }

// semacquire1 is semacquire, with the waiter queued first if lifo (it
// has waited before).
func semacquire1(addr *uint32, lifo bool, why string) { semacquire2(addr, lifo, why, false) }

// semacquire2 is semacquire1, durably blocked in a synctest bubble if
// durable.
func semacquire2(addr *uint32, lifo bool, why string, durable bool) {
	for *addr == 0 {
		w := &semaWaiter{gp: curg}
		q := semaQueueOf(unsafe.Pointer(addr), true)
		if lifo {
			q.pushFront(w)
		} else {
			q.push(w)
		}
		if durable {
			goparkIdle(why, nil)
		} else {
			gopark(why)
		}
		if w.granted {
			return
		}
	}
	*addr--
}

func semrelease(addr *uint32) { semrelease1(addr, false) }

// semrelease1 is semrelease; with handoff, the semaphore goes to the
// first waiter, which runs next (sync.Mutex in starvation mode).
func semrelease1(addr *uint32, handoff bool) {
	q := semaQueueOf(unsafe.Pointer(addr), false)
	var w *semaWaiter
	if q != nil {
		w = q.pop()
		if q.head == nil {
			delete(semaWaiters, uintptr(unsafe.Pointer(addr)))
		}
	}
	if w == nil {
		*addr++
		return
	}
	if !handoff {
		*addr++
		goready(w.gp)
		return
	}
	w.granted = true
	goready(w.gp)
	Gosched()
}

//go:linkname sync_runtime_Semacquire sync.runtime_Semacquire
func sync_runtime_Semacquire(addr *uint32) { semacquire(addr, "semacquire") }

//go:linkname poll_runtime_Semacquire internal/poll.runtime_Semacquire
func poll_runtime_Semacquire(addr *uint32) { semacquire(addr, "semacquire") }

//go:linkname sync_runtime_SemacquireWaitGroup sync.runtime_SemacquireWaitGroup
func sync_runtime_SemacquireWaitGroup(addr *uint32, synctestDurable bool) {
	semacquire2(addr, false, "sync.WaitGroup.Wait", synctestDurable)
}

//go:linkname sync_runtime_SemacquireMutex internal/sync.runtime_SemacquireMutex
func sync_runtime_SemacquireMutex(addr *uint32, lifo bool, skipframes int) {
	semacquire1(addr, lifo, "sync.Mutex.Lock")
}

//go:linkname sync_runtime_SemacquireRWMutexR sync.runtime_SemacquireRWMutexR
func sync_runtime_SemacquireRWMutexR(addr *uint32, lifo bool, skipframes int) {
	semacquire(addr, "sync.RWMutex.RLock")
}

//go:linkname sync_runtime_SemacquireRWMutex sync.runtime_SemacquireRWMutex
func sync_runtime_SemacquireRWMutex(addr *uint32, lifo bool, skipframes int) {
	semacquire(addr, "sync.RWMutex.Lock")
}

//go:linkname sync_runtime_Semrelease sync.runtime_Semrelease
func sync_runtime_Semrelease(addr *uint32, handoff bool, skipframes int) {
	semrelease1(addr, handoff)
}

//go:linkname internal_sync_runtime_Semrelease internal/sync.runtime_Semrelease
func internal_sync_runtime_Semrelease(addr *uint32, handoff bool, skipframes int) {
	semrelease1(addr, handoff)
}

//go:linkname poll_runtime_Semrelease internal/poll.runtime_Semrelease
func poll_runtime_Semrelease(addr *uint32) { semrelease(addr) }

// notifyList mirrors sync.notifyList: the tickets handed out and those
// notified.
type notifyList struct {
	wait   uint32
	notify uint32
	lock   uintptr
	head   unsafe.Pointer
	tail   unsafe.Pointer
}

//go:linkname notifyListAdd sync.runtime_notifyListAdd
func notifyListAdd(l *notifyList) uint32 {
	t := l.wait
	l.wait++
	return t
}

//go:linkname notifyListWait sync.runtime_notifyListWait
func notifyListWait(l *notifyList, t uint32) {
	if int32(t-l.notify) < 0 {
		return // already notified
	}
	semaQueueOf(unsafe.Pointer(l), true).push(&semaWaiter{gp: curg, t: t})
	goparkIdle("sync.Cond.Wait", nil)
}

//go:linkname notifyListNotifyAll sync.runtime_notifyListNotifyAll
func notifyListNotifyAll(l *notifyList) {
	l.notify = l.wait
	q := semaQueueOf(unsafe.Pointer(l), false)
	if q == nil {
		return
	}
	for w := q.pop(); w != nil; w = q.pop() {
		goready(w.gp)
	}
	delete(semaWaiters, uintptr(unsafe.Pointer(l)))
}

//go:linkname notifyListNotifyOne sync.runtime_notifyListNotifyOne
func notifyListNotifyOne(l *notifyList) {
	if l.wait == l.notify {
		return
	}
	t := l.notify
	l.notify++
	q := semaQueueOf(unsafe.Pointer(l), false)
	if q == nil {
		return
	}
	var prev *semaWaiter
	for w := q.head; w != nil; prev, w = w, w.next {
		if w.t == t {
			if prev == nil {
				q.head = w.next
			} else {
				prev.next = w.next
			}
			if q.tail == w {
				q.tail = prev
			}
			goready(w.gp)
			return
		}
	}
}

//go:linkname notifyListCheck sync.runtime_notifyListCheck
func notifyListCheck(size uintptr) {}

//go:linkname sync_runtime_canSpin internal/sync.runtime_canSpin
func sync_runtime_canSpin(i int) bool { return false }

//go:linkname sync_runtime_doSpin internal/sync.runtime_doSpin
func sync_runtime_doSpin() {}

//go:linkname internal_sync_nanotime internal/sync.runtime_nanotime
func internal_sync_nanotime() int64 { return nanotime() }

//go:linkname sync_runtime_procPin sync.runtime_procPin
func sync_runtime_procPin() int { return 0 }

//go:linkname sync_runtime_procUnpin sync.runtime_procUnpin
func sync_runtime_procUnpin() {}

// Nothing is shared here, and nothing handed over (runtime/shared.go).

//go:linkname sync_runtime_poolOwned sync.runtime_poolOwned
func sync_runtime_poolOwned(x any, fresh bool) (owned, drop bool) { return false, false }

//go:linkname sync_runtime_poolStoreOwned sync.runtime_poolStoreOwned
func sync_runtime_poolStoreOwned(dst *any, x any) { *dst = x }

//go:linkname sync_runtime_poolNew sync.runtime_poolNew
func sync_runtime_poolNew(f func() any, x any) bool { return false }

//go:linkname sync_runtime_registerPoolCleanup sync.runtime_registerPoolCleanup
func sync_runtime_registerPoolCleanup(cleanup func()) { poolcleanup = cleanup }

// poolcleanup empties the sync.Pools: at each runtime.GC.
var poolcleanup func()

//go:linkname sync_fatal sync.fatal
func sync_fatal(s string) { fatal(s) }

//go:linkname internal_sync_fatal internal/sync.fatal
func internal_sync_fatal(s string) { fatal(s) }

//go:linkname internal_sync_throw internal/sync.throw
func internal_sync_throw(s string) { throw(s) }
