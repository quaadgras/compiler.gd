// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

import _ "unsafe" // for go:linkname

//go:linkname main_main main.main
func main_main()

// Goroutines.
//
// Each goroutine runs on a WebAssembly stack of its own, which the
// host makes (JavaScript Promise Integration): the host calls gorun
// to start one, and a goroutine that blocks calls the import park,
// which suspends its stack. Before it suspends it, and when a
// goroutine ends, the host calls sched for the goroutine to run next,
// and starts it or resumes it. So there is one scheduler, here, and
// no stack switching code: at most one goroutine runs at a time.
//
// What a goroutine has in linear memory is its stack of variables
// without pointers; the global that points to the top of it is saved
// and restored around park.
//
// The stack of a goroutine starts with 64 KB, and grows (stack.go).

type g struct {
	coroarg *coro // the coro a coroutine goroutine runs
	id      int
	fn      func()
	started bool
	dead    bool
	stack   uintptr // the block of linear memory of its stack; 0 for main
	sp      uintptr // the top of the stack, while it is not running
	link    *g      // in the run queue

	// The segments of its stack beyond the first, and one to reuse;
	// the limit of the stack while it is not running (see stack.go).
	segs       *stackSeg
	spareSeg   *stackSeg
	stacklo    uintptr
	stackTotal uintptr

	deferred  *_defer
	deferArgp uintptr // the argp of the deferred call deferreturn runs
	panicking *_panic
	recoverSP uintptr

	// What woke the goroutine, and whether it was woken at all: of
	// the channels of a select, only the first to try does it.
	param      *sudog
	selectDone bool
	waiting    string  // why it is not running, for the deadlock report
	deferRec   uintptr // its innermost function that defers (lindefer), while it is parked

	fipsIndicator uint8 // crypto/internal/fips140's service indicator, per goroutine

	bubble  *synctestBubble // its synctest bubble (synctest.go)
	durable bool            // parked, durably blocked in its bubble
}

var (
	// maing is the goroutine that runs the initialization and main.
	maing = g{id: 1, started: true}

	curg       = &maing
	allgs      []*g // by number; maing is added by the first go statement
	runqHead   *g
	runqTail   *g
	ngoroutine = 1

	// Each goroutine that has started and not ended has a stack of
	// the engine's, and an engine may have only so many (Firefox some
	// 2,000). nstacks counts them. From stackSoft of them on, and
	// after the engine has refused one (stackFull, until a goroutine
	// ends), the goroutines not yet started wait in newq (a list by
	// link, the newest first; nnew of them), and are started only
	// when no other can run, the newest first: one that those before it wait for (a chain of channels,
	// each goroutine receiving from the next) then runs and ends
	// before the stacks are all taken by those that wait.
	nstacks    = 1
	stackFull  bool
	stackTries int
	newq       *g
	nnew       int
)

const stackSoft = 1000

const gStackSize = 64<<10 - linheader

//go:wasmimport gd park
func park(id uint32)

//go:wasmimport gd exit
func exit(code int32)

// lindefer and linsetdefer return and set the record of the innermost
// running function that defers calls, which is in its frame (see
// wasm.GCGlobalDefer in cmd/internal/obj/wasm): each goroutine has its
// own. The compiler replaces calls to them.
func lindefer() uintptr
func linsetdefer(rec uintptr)

// linsetsp sets the top of the stack in linear memory. The compiler
// replaces a call to it by the instruction.
func linsetsp(sp uintptr)

// rt0gc is the entry point of the program: the linker exports a
// function that creates the objects of the global variables, calls the
// functions that initialize the packages and then calls it.
func rt0gc() {
	main_main()
	exit(0)
}

// newproc is what a go statement calls.
func newproc(fn func()) {
	newproc1(fn)
}

// newproc1 starts a goroutine and returns it. It is in the bubble of
// the goroutine that starts it.
func newproc1(fn func()) *g {
	if len(allgs) == 0 {
		allgs = append(allgs, nil, &maing)
	}
	gp := &g{id: len(allgs), fn: fn}
	allgs = append(allgs, gp)
	ngoroutine++
	if b := curg.bubble; b != nil {
		gp.bubble = b
		b.goStart()
	}
	goready(gp)
	return gp
}

// goready makes gp runnable.
func goready(gp *g) {
	if gp.durable {
		wakeDurable(gp)
	}
	gp.link = nil
	if !gp.started && (newq != nil || stackFull) {
		pushnew(gp)
		return
	}
	if runqTail == nil {
		runqHead = gp
	} else {
		runqTail.link = gp
	}
	runqTail = gp
}

// gopark suspends the goroutine until another makes it runnable.
func gopark(why string) {
	gp := curg
	gp.waiting = why
	gp.sp = linsp()
	gp.stacklo = linsplim()
	gp.deferRec = lindefer()
	// What the host calls while the goroutine is parked (another
	// goroutine's gorun, free...) runs on the host's stack: below this
	// goroutine's frames would do, but the top of the stack the host
	// finds is whatever the last goroutine left there, which may be
	// above them (a goroutine that ends restores the top it was started
	// with).
	if hostStack == 0 {
		hostStack = linalloc(hostStackSize)
	}
	linsetsplim(hostStack + stackRedZone)
	linsetsp(hostStack + hostStackSize)
	park(uint32(gp.id))
	linsetsp(gp.sp)
	linsetsplim(gp.stacklo)
	linsetdefer(gp.deferRec)
	gp.waiting = ""
}

// Gosched yields the processor, allowing other goroutines to run.
func Gosched() {
	goready(curg)
	gopark("yield")
}

// sched is called by the host: it returns the goroutine to run next,
// as twice its number, plus one if it is to be started with gorun
// rather than resumed, or -1 if there is none.
//
//go:nosplit
func sched() int64 {
	var r int64
	systemstack(func() { r = sched1() })
	return r
}

func sched1() int64 {
	runtimers()
	for {
		gp := runqHead
		if gp == nil {
			break
		}
		runqHead = gp.link
		if runqHead == nil {
			runqTail = nil
		}
		gp.link = nil
		if gp.started {
			curg = gp
			return int64(gp.id) << 1
		}
		if nstacks < stackSoft && !stackFull {
			return gostart(gp)
		}
		// Stacks are short: it waits in newq.
		pushnew(gp)
	}
	if gp := newq; gp != nil && !stackFull {
		newq = gp.link
		gp.link = nil
		nnew--
		return gostart(gp)
	}
	return -1
}

// gostart is sched's answer that gp, which has not started, is to be.
func gostart(gp *g) int64 {
	curg = gp
	gp.started = true
	nstacks++
	return int64(gp.id)<<1 | 1
}

// pushnew adds gp, which has not started, to newq.
func pushnew(gp *g) {
	gp.link = newq
	newq = gp
	nnew++
}

// unstart is called by the host when it could not run the goroutine
// that sched told it to start: the engine has no stack for it. The
// goroutine waits for one (see newq). It reports whether that is so:
// not if the goroutine ran (the failure is then its own).
//
//go:nosplit
func unstart(id int64) int64 {
	if id <= 0 || id >= int64(len(allgs)) {
		return 0
	}
	gp := allgs[id]
	if gp == nil || gp.dead || !gp.started || gp.stack != 0 || gp == &maing {
		return 0
	}
	gp.started = false
	nstacks--
	stackFull = true
	pushnew(gp)
	return 1
}

// gorun is called by the host, on a new stack, to run a goroutine.
//
//go:nosplit
func gorun(id int64) {
	gp := allgs[id]
	gp.stack = linalloc(gStackSize)
	gp.stacklo = gp.stack + stackRedZone
	linsetsplim(gp.stacklo)
	linsetsp(gp.stack + gStackSize)
	linsetdefer(0)
	gp.fn()
	linsetdefer(0)
	// The goroutine's stack is freed: what the host calls next (sched)
	// must not keep its frame there, where an allocation could
	// overwrite it. It runs on a stack of the host's calls.
	if hostStack == 0 {
		hostStack = linalloc(hostStackSize)
	}
	linsetsplim(hostStack + stackRedZone)
	linsetsp(hostStack + hostStackSize)
	gdone(gp)
}

// hostStack is the stack in linear memory that the functions the host
// calls use once a goroutine has ended (while one is parked, they use
// what is free of its stack).
var hostStack uintptr

const hostStackSize = 64 << 10

// gdone records that gp has ended.
func gdone(gp *g) {
	gp.dead = true
	gp.fn = nil
	ngoroutine--
	// Its stack is the engine's to give again.
	nstacks--
	stackFull = false
	stackTries = 0
	if gp.bubble != nil {
		bubbleGoEnd(gp)
	}
	if gp == &maing {
		return
	}
	allgs[gp.id] = nil
	freestack(gp)
	linfree(gp.stack)
}

// deadlock is called by the host when no goroutine can run and nothing
// is going to wake one. It returns only if the host is to schedule
// again (see deadlock1).
//
//go:nosplit
func deadlock() {
	systemstack(deadlock1)
}

func deadlock1() {
	if newq != nil {
		// Goroutines wait for a stack of the engine's, which it may
		// have again by now (it refused one just after a goroutine
		// ended, say): the host schedules again.
		if stackTries < 3 {
			stackTries++
			stackFull = false
			return
		}
		printstring("fatal error: too many goroutines: the WebAssembly engine has stacks for ")
		printint(int64(nstacks))
		printstring(", all of them blocked, and ")
		printint(int64(nnew))
		printstring(" more goroutines wait to start\n")
		exit(2)
	}
	if maing.dead && ngoroutine == 0 {
		printstring("fatal error: no goroutines (main called runtime.Goexit) - deadlock!\n")
		exit(2)
	}
	printstring("fatal error: all goroutines are asleep - deadlock!\n\n")
	if len(allgs) == 0 {
		allgs = append(allgs, nil, &maing)
	}
	for _, gp := range allgs {
		if gp != nil && !gp.dead {
			printstring("goroutine ")
			printint(int64(gp.id))
			printstring(" [")
			printstring(gp.waiting)
			printstring("]:\n")
		}
	}
	exit(2)
}

// mapinitnoop is a no-op function the linker redirects the
// initialization of unused maps to.
func mapinitnoop() {}

// unreachableMethod is the target of the method table entries of
// methods the linker found unreachable.
func unreachableMethod() {
	fatal("unreachable method called. linker bug?")
}

var zerobase uintptr // the address of every zero-sized object
