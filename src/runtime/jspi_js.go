// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js && wasm.jspi

package runtime

import (
	"internal/abi"
	"internal/goarch"
	"internal/runtime/sys"
	"unsafe"
)

// On js/wasm with JSPI (JavaScript Promise Integration) every goroutine
// runs on its own WebAssembly stack, which the engine suspends and
// resumes, instead of the stack being unwound and rewound at every
// goroutine switch:
//
//   - A goroutine starts in wasm_export_gorun, which wasm_exec.js calls
//     through WebAssembly.promising.
//   - mcall runs its fn and the scheduler on the g0 stack, still on the
//     calling goroutine's WebAssembly stack. The scheduler's gogo
//     records the goroutine to run next and returns. If that is another
//     goroutine, mcall calls jspark, a WebAssembly.Suspending import:
//     JavaScript resumes the chosen goroutine (or starts it) and the
//     parked one stays suspended until it is chosen again.
//   - When nothing is runnable the scheduler chooses no goroutine and
//     control goes back to the JavaScript event loop, whose timers and
//     events run the scheduler again through wasm_export_sched.
//
// The Go stack stays in linear memory, so tracebacks, stack scanning
// and stack copying are unchanged. Compiled code keeps no WebAssembly
// local alive across a call and reloads SP after every call, so a
// suspended frame survives its goroutine's stack being moved.
const jspi = true

// jsnextg is the goroutine gogo chose to run next, for JavaScript.
// jspending is that goroutine until it actually runs.
// Both are *g, kept as uintptr because assembly writes them.
var (
	jsnextg   uintptr
	jspending uintptr
)

// jsgdead is set when the goroutine whose WebAssembly stack is running
// was destroyed (gdestroy), by a function mcall called: a coroutine
// exiting through coroswitch_m. mcall then unwinds the stack to
// wasm_export_gorun instead of parking it.
var jsgdead uintptr

// jspark suspends gp's WebAssembly stack and lets next run, if it is not
// nil. It returns when the scheduler chooses gp again.
//
//go:wasmimport gojs runtime.park
func jspark(gp, next *g)

// jssched is called on g0 by wasm_export_sched, from a JavaScript timer
// or event while no goroutine runs. It runs the scheduler.
func jssched() {
	if getg().m.curg != nil || jsCallerDepth != 0 {
		throw("jssched: a goroutine is running")
	}
	jsbusy()
	jsnextg = 0
	checkTimeouts()
	schedule()
}

// jsexit is called on g0 by wasm_export_gorun when gp's function has
// returned or called Goexit.
func jsexit(gp *g) {
	jsnextg = 0
	goexit0(gp)
	jsgdead = 0
}

// jsgoexit calls goexitDefers(p) for Goexit, and again each time a
// recovered panic's throw (jsthrowrecover) selects Goexit's frame.
//
//go:noescape
func jsgoexit(p *_panic)

// jsthrowgoexit unwinds the WebAssembly stack to wasm_export_gorun.
func jsthrowgoexit()

// jsthrowrecover unwinds the WebAssembly stack to the frame recovery
// selected in g.sched, which re-enters at its deferreturn call.
func jsthrowrecover()

// Calls from JavaScript into Go (functions made by syscall/js.FuncOf)
// each run on a new goroutine, which wasm_exec.js starts at once, so
// the call is synchronous for as long as the goroutine does not park.
// If it parks, JavaScript gets a promise for the result.
//
// JavaScript can make such a call while a goroutine is running, from
// inside a call that goroutine made to JavaScript, or while the
// goroutine the scheduler chose has not started running yet. That
// goroutine is set aside on jsCallers, and it is what the scheduler goes
// back to when the callback's goroutine parks or exits: nothing else
// can run until JavaScript returns.

type jsCaller struct {
	gp      guintptr
	pending bool // gp had been chosen to run, but was not running yet
}

var (
	jsCallers     [64]jsCaller
	jsCallerDepth int

	// jsinplace is the goroutine, if any, that continues inside its
	// call to JavaScript when the current callback returns. A *g.
	jsinplace uintptr

	jsCallbackFn = jsCallbackMain
)

// jsCallbackMain is the function a callback's goroutine runs.
func jsCallbackMain() {
	if eventHandler == nil {
		// The program does not use syscall/js, so this can only be
		// wasm_exec_node.js reporting a deadlock.
		jsdeadlock()
	}
	eventHandler()
}

// jscallback is called on g0 by wasm_export_callback when JavaScript
// calls into Go. sp is the Go stack pointer at that time. It leaves a
// new goroutine in jsnextg for JavaScript to start.
func jscallback(sp uintptr) {
	jsbusy()
	jsnextg = 0
	mp := getg().m
	if cur := mp.curg; cur != nil {
		pending := jspending == uintptr(unsafe.Pointer(cur))
		if !pending {
			// cur is inside a call to JavaScript, made with
			// stack pointer sp. Describe it as waiting in the
			// caller of that import, for stack scans.
			cur.sched.pc = *(*uintptr)(unsafe.Pointer(sp))
			cur.sched.sp = sp + goarch.PtrSize
		}
		if jsCallerDepth == len(jsCallers) {
			throw("too many nested calls from JavaScript")
		}
		jsCallers[jsCallerDepth] = jsCaller{gp: guintptr(unsafe.Pointer(cur)), pending: pending}
		jsCallerDepth++
		casGToWaiting(cur, _Grunning, waitReasonZero)
		dropg()
	}
	fn := *(**funcval)(unsafe.Pointer(&jsCallbackFn))
	newg := newproc1(fn, mp.g0, abi.FuncPCABIInternal(jscallback)+sys.PCQuantum, false, waitReasonZero)
	execute(newg, false)
}

// jsreturn is called by the scheduler when a callback's goroutine has
// parked or exited and a goroutine was set aside for it: that
// goroutine runs again.
func jsreturn() {
	jsCallerDepth--
	c := jsCallers[jsCallerDepth]
	jsCallers[jsCallerDepth] = jsCaller{}
	gp := c.gp.ptr()
	casgstatus(gp, _Gwaiting, _Grunnable)
	execute(gp, true)
	// JavaScript has nothing to wake: gp either continues inside its
	// call to JavaScript, or is already about to run.
	jsnextg = 0
	if !c.pending {
		jspending = 0
		jsinplace = uintptr(unsafe.Pointer(gp))
	}
}

// jsidlestart is when the scheduler last returned to the JavaScript
// event loop with nothing to run, or 0 if Go code has run since.
var jsidlestart int64

// jsidle is called by the scheduler when nothing is runnable.
func jsidle() {
	jsidlestart = nanotime()
}

// jsbusy is called when JavaScript calls into Go.
func jsbusy() {
	if jsidlestart != 0 {
		sched.idleTime.Add(nanotime() - jsidlestart)
		jsidlestart = 0
	}
}
