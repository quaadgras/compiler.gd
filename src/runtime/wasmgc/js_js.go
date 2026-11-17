// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

import _ "unsafe" // for go:linkname

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
// the host to start it with gorun.
//
//go:nosplit
func jscallback() int64 {
	if len(allgs) == 0 {
		allgs = append(allgs, nil, &maing)
	}
	gp := &g{id: len(allgs), fn: jsCall}
	jsCallers = append(jsCallers, jsCaller{curg, gp, linsp(), linsplim(), lindefer()})
	allgs = append(allgs, gp)
	ngoroutine++
	gostart(gp)
	return int64(gp.id)
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
