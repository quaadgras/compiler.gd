// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

import "unsafe"

// synctest bubbles, as src/runtime/synctest.go has them, on one thread.
//
// A bubble has a clock of its own and timers of its own (time.go). It
// counts its goroutines that are not durably blocked (running): those
// parked by goparkIdle, for a reason that only another goroutine of the
// bubble can end (a channel made in the bubble, a sleep, sync.Cond, a
// WaitGroup of the bubble). When that count and active are zero, the
// bubble is durably blocked: maybeWake wakes the goroutine in
// synctest.Wait, or the caller of synctest.Run, which advances the
// clock to the next timer or reports a deadlock.
type synctestBubble struct {
	timers  []*timer
	id      uint64
	now     int64 // the fake time
	root    *g    // the caller of synctest.Run
	waiter  *g    // the caller of synctest.Wait
	main    *g    // the goroutine started by synctest.Run
	waiting bool  // a goroutine is in synctest.Wait
	done    bool  // main has exited

	total   int // the goroutines of the bubble
	running int // those not durably blocked
	active  int // other reasons not to be idle
}

var bubbleGen uint64

// goparkIdle parks the current goroutine, durably blocked if it is in a
// bubble. unlockf, if not nil, is called once the goroutine counts as
// blocked: if it returns false, the goroutine does not park.
func goparkIdle(why string, unlockf func(gp *g) bool) {
	gp := curg
	b := gp.bubble
	if b == nil {
		if unlockf == nil || unlockf(gp) {
			gopark(why)
		}
		return
	}
	b.active++
	gp.durable = true
	b.running--
	if unlockf != nil && !unlockf(gp) {
		gp.durable = false
		b.running++
		b.decActive()
		return
	}
	b.decActive()
	gopark(why)
}

// wakeDurable is called by goready: a goroutine durably blocked in a
// bubble runs again.
func wakeDurable(gp *g) {
	gp.durable = false
	gp.bubble.running++
}

// goStart and goEnd count a goroutine of the bubble that starts or
// ends.
func (b *synctestBubble) goStart() {
	b.total++
	b.running++
}

func (b *synctestBubble) goEnd(gp *g) {
	b.total--
	b.running--
	if gp == b.main {
		b.done = true
	}
	if b.total < 0 || b.running < 0 {
		fatal("synctest: bubble counts < 0")
	}
	if w := b.maybeWake(); w != nil {
		goready(w)
	}
}

func (b *synctestBubble) incActive() { b.active++ }

func (b *synctestBubble) decActive() {
	b.active--
	if b.active < 0 {
		throw("active < 0")
	}
	if w := b.maybeWake(); w != nil {
		goready(w)
	}
}

// maybeWake returns the goroutine to wake if the bubble is durably
// blocked.
func (b *synctestBubble) maybeWake() *g {
	if b.running > 0 || b.active > 0 {
		return nil
	}
	b.active++
	if next := b.wakeTime(); next > 0 && next <= b.now {
		return b.root
	}
	if b.waiter != nil {
		return b.waiter
	}
	return b.root
}

// wakeTime returns when the bubble's next timer fires, or 0.
func (b *synctestBubble) wakeTime() int64 {
	if len(b.timers) == 0 {
		return 0
	}
	return b.timers[0].when
}

// runTimers runs the bubble's timers that are due at its time.
func (b *synctestBubble) runTimers() {
	h := &b.timers
	for len(*h) > 0 && (*h)[0].when <= b.now {
		t := (*h)[0]
		t.remove()
		t.fire(b.now)
	}
}

//go:linkname synctest_Run internal/synctest.Run
func synctest_Run(f func()) {
	gp := curg
	if gp.bubble != nil {
		panic("synctest.Run called from within a synctest bubble")
	}
	bubbleGen++
	b := &synctestBubble{id: bubbleGen, total: 1, running: 1, root: gp}
	const synctestBaseTime = 946684800000000000 // midnight UTC 2000-01-01
	b.now = synctestBaseTime
	gp.bubble = b
	defer func() {
		gp.bubble = nil
	}()

	b.main = newproc1(f)

	b.active++
	for {
		b.runTimers()
		goparkIdle("synctest.Run", func(gp *g) bool {
			// active counts what Run holds and what goparkIdle
			// holds while it calls this.
			if b.running == 0 && b.active == 2 {
				// All the goroutines of the bubble have blocked
				// or ended.
				return false
			}
			b.active--
			return true
		})
		if b.active < 0 {
			throw("active < 0")
		}
		next := b.wakeTime()
		if next == 0 {
			break
		}
		if next < b.now {
			throw("time went backwards")
		}
		if b.done {
			// Time stops once the bubble's main goroutine has exited.
			break
		}
		b.now = next
	}

	if b.total != 1 {
		var reason string
		if b.done {
			reason = "deadlock: main bubble goroutine has exited but blocked goroutines remain"
		} else {
			reason = "deadlock: all goroutines in bubble are blocked"
		}
		panic(synctestDeadlockError{reason: reason, bubble: b})
	}
}

type synctestDeadlockError struct {
	reason string
	bubble *synctestBubble
}

var _ error = synctestDeadlockError{}

func (e synctestDeadlockError) Error() string {
	return e.reason
}

//go:linkname synctest_Wait internal/synctest.Wait
func synctest_Wait() {
	gp := curg
	b := gp.bubble
	if b == nil {
		panic("goroutine is not in a bubble")
	}
	if b.waiting {
		panic("wait already in progress")
	}
	b.waiting = true
	goparkIdle("synctest.Wait", func(gp *g) bool {
		if b.running == 0 && b.active == 0 {
			throw("running == 0 && active == 0")
		}
		b.waiter = gp
		return true
	})
	b.active--
	if b.active < 0 {
		throw("active < 0")
	}
	b.waiter = nil
	b.waiting = false
}

//go:linkname synctest_IsInBubble internal/synctest.IsInBubble
func synctest_IsInBubble() bool { return curg.bubble != nil }

//go:linkname synctest_acquire internal/synctest.acquire
func synctest_acquire() any {
	if b := curg.bubble; b != nil {
		b.incActive()
		return b
	}
	return nil
}

//go:linkname synctest_release internal/synctest.release
func synctest_release(b any) {
	b.(*synctestBubble).decActive()
}

//go:linkname synctest_inBubble internal/synctest.inBubble
func synctest_inBubble(b any, f func()) {
	gp := curg
	if gp.bubble != nil {
		panic("goroutine is already bubbled")
	}
	gp.bubble = b.(*synctestBubble)
	defer func() {
		gp.bubble = nil
	}()
	f()
}

// The objects associated with a bubble (sync.WaitGroup), by bubble id.
// Natively a special record of the object; here a map, which keeps
// the object until it is disassociated.
var bubbleAssoc map[unsafe.Pointer]uint64

// Keep these in sync with internal/synctest.
const (
	bubbleAssocUnbubbled     = iota // not associated with any bubble
	bubbleAssocCurrentBubble        // associated with the current bubble
	bubbleAssocOtherBubble          // associated with a different bubble
)

func getOrSetBubbleAssoc(p unsafe.Pointer, id uint64, add bool) int {
	if old, ok := bubbleAssoc[p]; ok {
		if old == id {
			return bubbleAssocCurrentBubble
		}
		return bubbleAssocOtherBubble
	}
	if !add {
		return bubbleAssocUnbubbled
	}
	if bubbleAssoc == nil {
		bubbleAssoc = make(map[unsafe.Pointer]uint64)
	}
	bubbleAssoc[p] = id
	return bubbleAssocCurrentBubble
}

//go:linkname synctest_associate internal/synctest.associate
func synctest_associate(p unsafe.Pointer) int {
	return getOrSetBubbleAssoc(p, curg.bubble.id, true)
}

//go:linkname synctest_disassociate internal/synctest.disassociate
func synctest_disassociate(p unsafe.Pointer) {
	delete(bubbleAssoc, p)
}

//go:linkname synctest_isAssociated internal/synctest.isAssociated
func synctest_isAssociated(p unsafe.Pointer) bool {
	return getOrSetBubbleAssoc(p, curg.bubble.id, false) == bubbleAssocCurrentBubble
}
