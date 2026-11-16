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

	bubble *synctestBubble // a timer of a synctest bubble, on its clock
	rand   uint32          // orders the timers of a bubble due at once
}

// heap returns the heap the timer is in when it is in one: the
// bubble's, for a timer of a synctest bubble.
func (t *timer) heap() *[]*timer {
	if t.bubble != nil {
		return &t.bubble.timers
	}
	return &timers
}

// now returns the time on the timer's clock.
func (t *timer) now() int64 {
	if t.bubble != nil {
		return t.bubble.now
	}
	return nanotime()
}

// curNow returns the time on the clock of the current goroutine.
func curNow() int64 {
	if b := curg.bubble; b != nil {
		return b.now
	}
	return nanotime()
}

// timeTimer is what package time knows as a Timer: its channel and
// whether it was made by newTimer, then the runtime's part.
type timeTimer struct {
	c    unsafe.Pointer
	init bool
	t    *timer
}

var timers []*timer

func (t *timer) less(u *timer) bool {
	if t.when == u.when && t.bubble != nil {
		// Timers of a bubble due at the same instant fire in a
		// random order, as natively.
		return t.rand < u.rand
	}
	return t.when < u.when
}

func timerUp(h []*timer, i int) {
	for i > 0 {
		p := (i - 1) / 2
		if !h[i].less(h[p]) {
			break
		}
		timerSwap(h, i, p)
		i = p
	}
}

func timerDown(h []*timer, i int) {
	n := len(h)
	for {
		l := 2*i + 1
		if l >= n {
			return
		}
		m := l
		if r := l + 1; r < n && h[r].less(h[l]) {
			m = r
		}
		if !h[m].less(h[i]) {
			return
		}
		timerSwap(h, i, m)
		i = m
	}
}

func timerSwap(h []*timer, i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].idx = i
	h[j].idx = j
}

func (t *timer) add() {
	hp := t.heap()
	if t.bubble != nil {
		t.rand = uint32(rand())
	}
	t.idx = len(*hp)
	*hp = append(*hp, t)
	timerUp(*hp, t.idx)
}

// remove takes t out of the heap and reports whether it was in it.
func (t *timer) remove() bool {
	i := t.idx
	if i < 0 {
		return false
	}
	hp := t.heap()
	h := *hp
	last := len(h) - 1
	if i != last {
		timerSwap(h, i, last)
	}
	h[last] = nil
	h = h[:last]
	*hp = h
	if i != last {
		timerDown(h, i)
		timerUp(h, i)
	}
	t.idx = -1
	return true
}

// fakeDue runs the timer t of a synctest bubble, which is not in a
// heap, if it is due now: it is run by the goroutine that sets it, as
// time does not advance until the bubble blocks.
func (t *timer) fakeDue() bool {
	if t.bubble == nil || t.idx >= 0 || t.when == 0 || t.when > t.bubble.now {
		return false
	}
	t.fire(t.bubble.now)
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
	now := t.now()
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
	t := &timeTimer{init: true, t: &timer{when: when, period: period, f: f, arg: arg, ch: c, idx: -1, bubble: curg.bubble}}
	if c != nil {
		// Lazy: see timer.blocked.
		c.timer = true
		c.tm = t.t
		return t
	}
	if !t.t.fakeDue() {
		t.t.add()
	}
	return t
}

//go:linkname stopTimer time.stopTimer
func stopTimer(t *timeTimer) bool {
	tt := t.t
	if tt.bubble != nil && curg.bubble != tt.bubble {
		fatal("stop of synctest timer from outside bubble")
	}
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
	tt := t.t
	if tt.bubble != nil && curg.bubble != tt.bubble {
		fatal("reset of synctest timer from outside bubble")
	}
	pending := stopTimer(t)
	tt.when = when
	tt.period = period
	if tt.ch == nil && tt.fakeDue() {
		return pending
	}
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
	when := curNow() + ns
	if when < 0 { // overflow
		when = 1<<63 - 1
	}
	t := &timer{when: when, f: goreadyTimer, arg: curg, idx: -1, bubble: curg.bubble}
	t.add()
	goparkIdle("sleep", nil)
}

func goreadyTimer(arg any, seq uintptr, delay int64) {
	goready(arg.(*g))
}

//go:linkname time_runtimeNano time.runtimeNano
func time_runtimeNano() int64 { return curNow() }

//go:linkname time_runtimeNow time.runtimeNow
func time_runtimeNow() (sec int64, nsec int32, mono int64) {
	if b := curg.bubble; b != nil {
		// No monotonic time in a bubble, as natively.
		return b.now / 1e9, int32(b.now % 1e9), 0
	}
	ns := walltime()
	return ns / 1e9, int32(ns % 1e9), nanotime()
}

//go:linkname time_runtimeIsBubbled time.runtimeIsBubbled
func time_runtimeIsBubbled() bool { return curg.bubble != nil }

//go:wasmimport gd walltime
func walltime() int64

// haveHighResSleep: the host's timers have millisecond resolution.
var haveHighResSleep = true
