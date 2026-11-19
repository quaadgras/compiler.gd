// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"sync"
	"time"
)

// MonoStart is CLOCK_MONOTONIC's origin: the program's start.
var MonoStart = time.Now()

// Interval timers: setitimer's three, of which ITIMER_REAL is alarm's
// too, as on Linux. Each counts wall time (Go has no timers of processor
// time) and sends its signal to the process when it expires, then again
// every interval if that is not zero.

// The timers' signals: ITIMER_REAL's, ITIMER_VIRTUAL's and ITIMER_PROF's.
var itimerSigs = [3]int32{14, 26, 27} // SIGALRM, SIGVTALRM, SIGPROF

type itimer struct {
	t        *time.Timer
	at       time.Time // when it expires next, if armed
	interval time.Duration
	seq      int // which arming the timer's function is of
}

var (
	itimerMu sync.Mutex
	itimers  [3]itimer
)

// SetITimer arms timer which (ITIMER_REAL, ITIMER_VIRTUAL or ITIMER_PROF)
// to expire after value, then every interval; a zero value disarms it.
// It returns what was left of the previous arming and its interval.
func SetITimer(which int, value, interval time.Duration) (oldValue, oldInterval time.Duration) {
	itimerMu.Lock()
	defer itimerMu.Unlock()
	it := &itimers[which]
	oldValue, oldInterval = it.left(), it.interval
	if it.t != nil {
		it.t.Stop()
		it.t = nil
	}
	it.seq++
	it.at, it.interval = time.Time{}, 0
	if value > 0 {
		it.at, it.interval = time.Now().Add(value), interval
		it.arm(which, value)
	}
	return oldValue, oldInterval
}

// ITimer returns what is left of timer which and its interval.
func ITimer(which int) (value, interval time.Duration) {
	itimerMu.Lock()
	defer itimerMu.Unlock()
	return itimers[which].left(), itimers[which].interval
}

// left is the time until it expires; itimerMu is held.
func (it *itimer) left() time.Duration {
	if it.at.IsZero() {
		return 0
	}
	return max(time.Until(it.at), time.Microsecond) // armed: not zero
}

// arm starts it's timer to expire after d; itimerMu is held.
func (it *itimer) arm(which int, d time.Duration) {
	seq := it.seq
	it.t = time.AfterFunc(d, func() {
		itimerMu.Lock()
		if it.seq != seq {
			itimerMu.Unlock()
			return
		}
		if it.interval > 0 {
			// The next expiry, skipping those already past as Linux
			// counts them overruns.
			now := time.Now()
			it.at = it.at.Add(it.interval)
			if it.at.Before(now) {
				it.at = it.at.Add((now.Sub(it.at)/it.interval + 1) * it.interval)
			}
			it.arm(which, time.Until(it.at))
		} else {
			it.at, it.t = time.Time{}, nil
		}
		itimerMu.Unlock()
		SigSendProcess(itimerSigs[which], SigInfo{Code: SI_KERNEL}, nil)
	})
}

// SI_KERNEL is siginfo_t's si_code of a signal the system sends.
const SI_KERNEL = 0x80
