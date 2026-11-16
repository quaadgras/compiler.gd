// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

import _ "unsafe"

// Coroutines (for iter.Pull): a goroutine that runs only when another
// switches to it, which then waits until it is switched back to.

type coro struct {
	gp *g // the goroutine blocked on the coro
	f  func(*coro)
}

//go:linkname newcoro
func newcoro(f func(*coro)) *coro {
	c := &coro{f: f}
	if len(allgs) == 0 {
		allgs = append(allgs, nil, &maing)
	}
	gp := &g{id: len(allgs), fn: corostart, coroarg: c}
	allgs = append(allgs, gp)
	ngoroutine++
	if b := curg.bubble; b != nil {
		// In the bubble, durably blocked until it is switched to.
		gp.bubble = b
		b.total++
		gp.durable = true
	}
	c.gp = gp // created, not runnable
	return c
}

//go:linkname coroswitch
func coroswitch(c *coro) {
	next := c.gp
	if next == nil {
		fatal("coroswitch on an exited coro")
	}
	c.gp = curg
	// next is readied first: the bubble, if any, has a goroutine
	// running throughout.
	goready(next)
	goparkIdle("coroutine", nil)
}

// corostart runs the function of a coro, then wakes the goroutine
// blocked on it, and its own goroutine ends.
func corostart() {
	c := curg.coroarg
	curg.coroarg = nil
	// (Deferred: also when f calls runtime.Goexit.)
	defer coroexit(c)
	c.f(c)
}

// coroexit wakes the goroutine blocked on c, as the coroutine's
// goroutine ends.
func coroexit(c *coro) {
	next := c.gp
	c.gp = nil
	if next != nil {
		goready(next)
	}
}
