// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

import "unsafe"

// Timers. They are in a heap ordered by when they fire. sched runs
// those that are due before it picks a goroutine; when no goroutine
// can run, the host asks timerwait how long to wait before calling
// sched again.

type timer struct {
	when   int64
	period int64
	f      func(arg any, seq uintptr, delay int64)
	arg    any
	seq    uintptr
	ch     *hchan // a timer channel to empty when it is stopped or reset
	idx    int    // in timers, or -1

	// A timer with a channel is lazy (as Go 1.23's): it is in timers
	// only while goroutines are blocked on its channel (blocked of
	// them), so that one nothing refers to can be collected. A receive
	// delivers the value of one that is due (maybeRunChan). when is 0
	// once it has fired, or when it is stopped.
	blocked int32
}

// timeTimer is what package time knows as a Timer: its channel and
// whether it was made by newTimer, then the runtime's part.
type timeTimer struct {
	c    unsafe.Pointer
	init bool
	t    *timer
}

var timers []*timer

func (t *timer) less(u *timer) bool { return t.when < u.when }

func timerUp(i int) {
	for i > 0 {
		p := (i - 1) / 2
		if !timers[i].less(timers[p]) {
			break
		}
		timerSwap(i, p)
		i = p
	}
}

func timerDown(i int) {
	n := len(timers)
	for {
		l := 2*i + 1
		if l >= n {
			return
		}
		m := l
		if r := l + 1; r < n && timers[r].less(timers[l]) {
			m = r
		}
		if !timers[m].less(timers[i]) {
			return
		}
		timerSwap(i, m)
		i = m
	}
}

func timerSwap(i, j int) {
	timers[i], timers[j] = timers[j], timers[i]
	timers[i].idx = i
	timers[j].idx = j
}

func (t *timer) add() {
	t.idx = len(timers)
	timers = append(timers, t)
	timerUp(t.idx)
}

// remove takes t out of the heap and reports whether it was in it.
func (t *timer) remove() bool {
	i := t.idx
	if i < 0 {
		return false
	}
	last := len(timers) - 1
	if i != last {
		timerSwap(i, last)
	}
	timers[last] = nil
	timers = timers[:last]
	if i != last {
		timerDown(i)
		timerUp(i)
	}
	t.idx = -1
	return true
}

// runtimers runs the timers that are due.
func runtimers() {
	if len(timers) == 0 {
		return
	}
	now := nanotime()
	for len(timers) > 0 && timers[0].when <= now {
		t := timers[0]
		t.remove()
		t.fire(now)
	}
}

// fire runs the timer t, which is due and not in timers, and sets its
// next time: in timers again if it is periodic and in use.
func (t *timer) fire(now int64) {
	delay := now - t.when
	if t.period > 0 {
		t.when += t.period * (1 + delay/t.period)
		if t.when < 0 { // overflow
			t.when = 1<<63 - 1
		}
		if t.ch == nil || t.blocked > 0 {
			t.add()
		}
	} else if t.ch != nil {
		t.when = 0
	}
	t.f(t.arg, t.seq, delay)
}

// maybeRunChan fires the timer of the timer channel c, if it is due, as
// a receive from c begins.
func maybeRunChan(c *hchan) {
	t := c.tm
	if t == nil || t.when == 0 {
		return
	}
	now := nanotime()
	if t.when > now {
		return
	}
	t.remove()
	t.fire(now)
}

// blockTimerChan and unblockTimerChan are called as a goroutine blocks
// on the timer channel c, and as it wakes: the timer is in timers while
// one is.
func blockTimerChan(c *hchan) {
	t := c.tm
	if t == nil {
		return
	}
	t.blocked++
	if t.when != 0 && t.idx < 0 {
		t.add()
	}
}

func unblockTimerChan(c *hchan) {
	t := c.tm
	if t == nil {
		return
	}
	t.blocked--
	if t.blocked == 0 && t.idx >= 0 {
		t.remove()
	}
}

// timerwait is called by the host when no goroutine can run: it
// returns the nanoseconds until the next timer fires, or -1 if there
// is none.
//
//go:nosplit
func timerwait() int64 {
	var r int64
	systemstack(func() { r = timerwait1() })
	return r
}

func timerwait1() int64 {
	if len(timers) == 0 {
		return -1
	}
	d := timers[0].when - nanotime()
	if d < 0 {
		d = 0
	}
	return d
}

//go:linkname newTimer time.newTimer
func newTimer(when, period int64, f func(arg any, seq uintptr, delay int64), arg any, c *hchan) *timeTimer {
	t := &timeTimer{init: true, t: &timer{when: when, period: period, f: f, arg: arg, ch: c, idx: -1}}
	if c != nil {
		// Lazy: see timer.blocked.
		c.timer = true
		c.tm = t.t
		return t
	}
	t.t.add()
	return t
}

//go:linkname stopTimer time.stopTimer
func stopTimer(t *timeTimer) bool {
	tt := t.t
	pending := tt.remove()
	if tt.ch != nil {
		// Armed: not fired, or fired lazily, which is the same.
		pending = tt.when != 0
		tt.when = 0
	}
	// A value sent but not received counts as not sent: the timer's
	// channel is synchronous (Go 1.23).
	if tt.drain() {
		pending = true
	}
	return pending
}

//go:linkname resetTimer time.resetTimer
func resetTimer(t *timeTimer, when, period int64) bool {
	pending := stopTimer(t)
	tt := t.t
	tt.when = when
	tt.period = period
	if tt.ch == nil || tt.blocked > 0 {
		tt.add()
	}
	return pending
}

// drain empties the channel of a timer, so that no value sent before
// it was stopped or reset is received after.
func (t *timer) drain() bool {
	drained := false
	if t.ch != nil {
		for t.ch.qcount > 0 {
			chanrecv(t.ch, nil, false)
			drained = true
		}
	}
	return drained
}

//go:linkname timeSleep time.Sleep
func timeSleep(ns int64) {
	if ns <= 0 {
		return
	}
	when := nanotime() + ns
	if when < 0 { // overflow
		when = 1<<63 - 1
	}
	t := &timer{when: when, f: goreadyTimer, arg: curg, idx: -1}
	t.add()
	gopark("sleep")
}

func goreadyTimer(arg any, seq uintptr, delay int64) {
	goready(arg.(*g))
}

//go:linkname time_runtimeNano time.runtimeNano
func time_runtimeNano() int64 { return nanotime() }

//go:linkname time_runtimeNow time.runtimeNow
func time_runtimeNow() (sec int64, nsec int32, mono int64) {
	ns := walltime()
	return ns / 1e9, int32(ns % 1e9), nanotime()
}

//go:linkname time_runtimeIsBubbled time.runtimeIsBubbled
func time_runtimeIsBubbled() bool { return false }

//go:wasmimport gd walltime
func walltime() int64

// haveHighResSleep: the host's timers have millisecond resolution.
var haveHighResSleep = true
