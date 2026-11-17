// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc && js

package runtime

import "unsafe"

// GOOS=js: calls from JavaScript into Go (the functions syscall/js.FuncOf
// makes). See doc/gd/wasm-gc.md, stage 18.
//
// Each call runs on a goroutine of its own, which the host starts at
// once (gorun, on a stack of the engine's of its own): the call returns
// what the function returns if the goroutine ends without blocking.
// If it blocks, JavaScript cannot wait (the goroutines it waits for
// run from the event loop): the call returns a promise of the result.
// Until the call has returned, the goroutine's stack can be suspended
// only when it blocks (the host could not run it again from where it
// was otherwise), so it does without what suspends it at other times
// (jssync): memory grows at once on its stack (growmem), the runtime
// does not collect (runtime.GC returns, lincollect does nothing), and
// a stack that grows first yields (morestackgc), which makes the call
// return a promise.
//
// The call may come while a goroutine is running, from inside a call it
// made to JavaScript, or while none is: jscallback sets aside what was
// running (the goroutine and the top of its stack in linear memory),
// and jscallbackdone, which the host calls when the goroutine of the
// call has ended or blocked, puts it back.

// eventHandler is syscall/js.handleEvent, which takes the call from
// the host and makes it.
var eventHandler func() bool

//go:linkname setEventHandler syscall/js.setEventHandler
func setEventHandler(fn func() bool) {
	eventHandler = fn
}

// jsdeadlock reports that no goroutine can run and that the host has
// nothing more to do (node's event loop is empty), and exits.
//
//go:linkname jsdeadlock syscall/js.deadlock
func jsdeadlock() {
	systemstack(deadlock1)
}

// A jsCaller is what was running when a call from JavaScript came, and
// the goroutine of the call (callee).
type jsCaller struct {
	gp, callee   *g
	sp, lim, rec uintptr
}

var jsCallers []jsCaller

// jscallback is called by the host when JavaScript calls into Go: it
// makes the goroutine that makes the call, and returns its number, for
// the host to start it with gorun. (On the system stack: it allocates,
// and nothing may suspend the stack it is called on, which has the
// host's frames, nor ask the host to collect.)
//
//go:nosplit
func jscallback() int64 {
	jsCaller0 = jsCaller{curg, nil, linsp(), linsplim(), lindefer()}
	systemstack(jscallback1)
	return int64(jsCallers[len(jsCallers)-1].callee.id)
}

var jsCaller0 jsCaller

func jscallback1() {
	if len(allgs) == 0 {
		allgs = append(allgs, nil, &maing)
	}
	gp := &g{id: len(allgs), fn: jsCall}
	jsCalls++
	allgs = append(allgs, gp)
	ngoroutine++
	c := jsCaller0
	c.callee = gp
	jsCallers = append(jsCallers, c)
	gostart(gp)
}

// jsbusy tells the host that the goroutine running is about to be
// suspended where the runtime's state must be left as it is (on is 1:
// in the allocator, growing memory, or in a collection), or that it is
// no longer (0). A call from JavaScript that comes meanwhile (an event,
// node's callback of a file operation) is made later, when the
// goroutine has blocked or ended, and returns a promise: a goroutine
// that ran then could find a channel, say, half changed (a receiver
// that found nothing in the buffer and had not yet said it waits).
//
//go:wasmimport gojs runtime.busy
func jsbusy(on int32)

// jsCalls counts the calls from JavaScript since jsYield last yielded.
var jsCalls int

// jsYield is called by syscall/js after a call into JavaScript, which
// may have called into Go: after 256 such calls, a goroutine (not that
// of a call from JavaScript that has not returned) yields. Node 24's V8
// (13.6) loses about 1 KB of its stack (--stack-size) for each call from
// JavaScript into Go made while a goroutine is in a call to JavaScript,
// until that goroutine's stack is suspended: a goroutine that made a
// few thousand such calls without blocking ended with "Maximum call
// stack size exceeded". Chromium 155 and Firefox 160 do not.
//
//go:linkname jsYield syscall/js.yield
func jsYield() {
	if jsCalls >= 256 && !jssync() {
		jsCalls = 0
		Gosched()
	}
}

// jssync reports whether the goroutine running is that of a call from
// JavaScript that has not returned: the call's goroutine runs from
// jscallback to jscallbackdone, unless it blocks, which ends the call.
func jssync() bool {
	return len(jsCallers) > 0 && jsCallers[len(jsCallers)-1].callee == curg
}

func jsCall() {
	eventHandler()
}

// jscallbackdone is called by the host when the goroutine of a call from
// JavaScript has ended or blocked.
//
//go:nosplit
func jscallbackdone() {
	c := jsCallers[len(jsCallers)-1]
	jsCallers = jsCallers[:len(jsCallers)-1]
	curg = c.gp
	linsetsp(c.sp)
	linsetsplim(c.lim)
	linsetdefer(c.rec)
}

// jsIsLinear reports whether p points into linear memory, where the host
// can read and write what it points to. (With GOWASM=gc a []byte may
// also be an object of the engine's: one that new or a struct made.)
//
//go:linkname jsIsLinear syscall/js.isLinear
func jsIsLinear(p unsafe.Pointer) bool {
	return trylinaddr(p) != 0
}
