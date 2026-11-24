// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Thread cancellation, cleanup handlers and the end of the main thread.
//
// pthread_cancel requests a thread's cancellation, which the thread acts
// on at a cancellation point (pthread_testcancel, and the calls that
// wait: pthread_join, pthread_cond_wait and timedwait, the sleeps,
// sem_wait, sigwait and its kin, sigsuspend and pause, poll, select and
// epoll_wait, and read and write when they are called) while its
// cancellation is enabled: it runs its cleanup handlers, the last pushed
// first, then its thread-specific data's destructors, and ends with
// PTHREAD_CANCELED. Asynchronous cancellation acts at the same points: a
// goroutine cannot be interrupted elsewhere.

// Canceled is PTHREAD_CANCELED: not glibc's and musl's ((void *)-1), as
// a pointer stored in Go's memory loses Tag for want of a capability,
// but -1 without bits 31 and 55 (the tag's, whatever the target), which
// a store keeps. cmd/cc's pthread.h says the same.
var Canceled = FromInt(uintptr(canceledBits)) // truncated where pointers have 32 bits

var canceledBits = ^uint64(0) &^ (1<<31 | 1<<55)

// A Cancel is a thread's cancellation state.
type Cancel struct {
	once      sync.Once
	ch        chan struct{} // closed when cancellation is requested
	requested atomic.Bool

	// Only the thread changes these.
	Disabled bool // PTHREAD_CANCEL_DISABLE
	Async    bool // PTHREAD_CANCEL_ASYNCHRONOUS
}

// MainThread is the program's first goroutine, which runs package
// initialization and main: pthread_self's 0.
var MainThread = newThread()

func init() {
	MainThread.Start = MonoStart
	GSelf().th = MainThread
}

// Self returns the calling thread, or nil if the goroutine is not one (a
// signal's handler running on a goroutine of its own).
func Self() *Thread { return GSelf().th }

// CancelState returns t's cancellation state, which only t may change.
func (t *Thread) CancelState() *Cancel { return &t.cancel }

// RequestCancel requests t's cancellation.
func (t *Thread) RequestCancel() {
	t.cancel.once.Do(func() {
		t.cancel.requested.Store(true)
		close(t.cancel.ch)
	})
	// Wake the threads waiting on SigCond (sigwait, sigsuspend), which
	// look again.
	SigMu.Lock()
	SigCond.Broadcast()
	SigMu.Unlock()
}

// CancelChan returns a channel that is closed when the calling thread's
// cancellation is requested, or nil (which never is) if its cancellation
// is disabled or it is not a thread: a wait that is a cancellation point
// waits for it too, then calls TestCancel.
func CancelChan() <-chan struct{} {
	t := Self()
	if t == nil || t.cancel.Disabled {
		return nil
	}
	return t.cancel.ch
}

// CancelPending reports whether the calling thread should act on a
// cancellation request now: it was requested, and is enabled.
func CancelPending() bool {
	t := Self()
	return t != nil && !t.cancel.Disabled && t.cancel.requested.Load()
}

// TestCancel is pthread_testcancel: if the calling thread's cancellation
// was requested, and is enabled, the thread ends with PTHREAD_CANCELED.
func TestCancel() {
	if CancelPending() {
		Self().cancel.Disabled = true // a cleanup handler's waits are not cancellation points
		ExitThread(Canceled)
	}
}

// A cleanupHandler is what pthread_cleanup_push pushed: a void
// (*)(void *) and its argument.
type cleanupHandler struct{ f, arg *Void }

// CleanupPush pushes cleanup handler f, a void (*)(void *), with arg.
func CleanupPush(f, arg *Void) {
	FuncOf[func(*Void)](f)
	tb := GSelf()
	tb.cleanup = append(tb.cleanup, cleanupHandler{f, arg})
}

// CleanupPop pops the last cleanup handler pushed, and calls it if run.
func CleanupPop(run bool) {
	tb := GSelf()
	n := len(tb.cleanup)
	if n == 0 {
		return
	}
	h := tb.cleanup[n-1]
	tb.cleanup = tb.cleanup[:n-1]
	if run {
		FuncOf[func(*Void)](h.f)(h.arg)
	}
}

// ExitThread is pthread_exit: it runs the calling thread's cleanup
// handlers, the last pushed first, and ends it with ret (its
// thread-specific data's destructors run as it ends). The main thread
// waits for the program's other threads to end, then the program exits
// with status 0, as when the last thread ends in C.
func ExitThread(ret *Void) {
	tb := GSelf()
	for len(tb.cleanup) > 0 {
		CleanupPop(true)
	}
	if tb.th == MainThread {
		runKeyDestructors()
		liveMu.Lock()
		for live > 0 {
			liveCond.Wait()
		}
		liveMu.Unlock()
		Exit(0)
	}
	panic(ThreadExit{Ret: ret})
}

// The count of the threads running, other than the main thread.
var (
	liveMu   sync.Mutex
	liveCond = sync.NewCond(&liveMu)
	live     int
)

func liveAdd(n int) {
	liveMu.Lock()
	live += n
	if live == 0 {
		liveCond.Broadcast()
	}
	liveMu.Unlock()
}

// CancelSleep sleeps for d, as a cancellation point.
func CancelSleep(d time.Duration) {
	TestCancel()
	if d <= 0 {
		return
	}
	c := CancelChan()
	if c == nil {
		time.Sleep(d)
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-c:
		TestCancel()
	}
}

// CancelSleepUntil sleeps until deadline, as a cancellation point.
func CancelSleepUntil(deadline time.Time) { CancelSleep(time.Until(deadline)) }

// The clocks of clock_gettime: CLOCK_REALTIME, CLOCK_MONOTONIC,
// CLOCK_PROCESS_CPUTIME_ID, CLOCK_THREAD_CPUTIME_ID and CLOCK_BOOTTIME
// (Linux's), and the CPU-time clocks of processes and threads
// (clock_getcpuclockid's and pthread_getcpuclockid's).
const (
	CLOCK_REALTIME           = 0
	CLOCK_MONOTONIC          = 1
	CLOCK_PROCESS_CPUTIME_ID = 2
	CLOCK_THREAD_CPUTIME_ID  = 3
	CLOCK_BOOTTIME           = 7
)

// ThreadCPUClock is pthread_getcpuclockid's clock of thread id, encoded as
// glibc and musl encode a thread's (as Linux's for its thread id).
func ThreadCPUClock(id uint64) int32 { return int32(-int64(id)-1)*8 + 6 }

// ProcessCPUClock is clock_getcpuclockid's clock of process pid.
func ProcessCPUClock(pid int32) int32 { return (-pid-1)*8 + 2 }

// ClockOrigin returns the origin of clock id: the time its value is
// counted from (the Unix epoch, as time.Time's zero is not, is
// time.Unix(0, 0)). A CPU-time clock counts the time since its process
// or thread started (Go cannot know the processor time of a goroutine,
// nor, portably, the program's): the program's start for the process's,
// and the thread's start for a thread's. false if id is not a clock
// (EINVAL).
func ClockOrigin(id int32) (time.Time, bool) {
	switch id {
	case CLOCK_REALTIME:
		return time.Unix(0, 0), true
	case CLOCK_MONOTONIC, CLOCK_BOOTTIME, CLOCK_PROCESS_CPUTIME_ID:
		return MonoStart, true
	case CLOCK_THREAD_CPUTIME_ID:
		if t := Self(); t != nil {
			return t.Start, true
		}
		return MonoStart, true
	}
	if id >= 0 {
		return time.Time{}, false
	}
	switch id & 7 {
	case 2: // a process's
		if id == ProcessCPUClock(int32(os.Getpid())) || id == ProcessCPUClock(0) {
			return MonoStart, true
		}
	case 6: // a thread's
		if t := ThreadOf(uint64(-(int64(id>>3) + 1))); t != nil {
			return t.Start, true
		}
	}
	return time.Time{}, false
}

// ClockNow returns the time on clock id, as a duration since its origin.
func ClockNow(id int32) (time.Duration, bool) {
	o, ok := ClockOrigin(id)
	if !ok {
		return 0, false
	}
	if id == CLOCK_REALTIME {
		return time.Duration(time.Now().UnixNano()), true
	}
	return time.Since(o), true
}

// LoadTimespec reads the struct timespec at ts as a duration: false if
// its nanoseconds are not between 0 and 999999999 or its seconds are
// negative.
func LoadTimespec(ts *Void) (time.Duration, bool) {
	sec, nsec := int64(Load64(ts)), LoadLong(Add(ts, 8))
	if sec < 0 || nsec < 0 || nsec >= 1e9 {
		return 0, false
	}
	if sec >= (1<<63-1)/int64(time.Second) {
		return 1<<63 - 1, true
	}
	return time.Duration(sec)*time.Second + time.Duration(nsec), true
}

// StoreTimespec stores d as the struct timespec at ts.
func StoreTimespec(ts *Void, d time.Duration) {
	Store64(ts, uint64(d/time.Second))
	StoreLong(Add(ts, 8), int64(d%time.Second))
}

// LoadClockDeadline reads the struct timespec at ts, a time on clock id:
// false if it is not one.
func LoadClockDeadline(id int32, ts *Void) (time.Time, bool) {
	if id == CLOCK_REALTIME {
		return LoadDeadline(ts)
	}
	d, ok := LoadTimespec(ts)
	o, ok2 := ClockOrigin(id)
	if !ok || !ok2 {
		return time.Time{}, false
	}
	return o.Add(d), true
}
