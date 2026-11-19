// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package times is sys/times.h's function for cmd/cc.
package times

import (
	c23 "internal/c23"
	"time"
)

// clkTck is sysconf(_SC_CLK_TCK): times counts hundredths of seconds.
const clkTck = 100

var start = time.Now()

// Times is times: the process's and its waited-for children's CPU times
// (getrusage's, where the system has it; elsewhere the time since the
// program started as its user time), and the time since the program
// started, plus one tick so that it is never 0 or -1 (clock_t is 64
// bits everywhere, so it does not wrap).
func Times(buf c23.Ptr) int64 {
	u, s, cu, cs := cpuTimes()
	if c23.Addr(buf) != 0 {
		for i, d := range []time.Duration{u, s, cu, cs} {
			c23.Store64(c23.Add(buf, 8*i), uint64(d/(time.Second/clkTck)))
		}
	}
	return int64(time.Since(start)/(time.Second/clkTck)) + 1
}
