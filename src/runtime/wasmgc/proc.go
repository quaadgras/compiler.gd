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
	waiting    string // why it is not running, for the deadlock report
}

var (
	// maing is the goroutine that runs the initialization and main.
	maing = g{id: 1, started: true}

	curg       = &maing
	allgs      []*g // by number; maing is added by the first go statement
	runqHead   *g
	runqTail   *g
	ngoroutine = 1
)

const gStackSize = 64<<10 - linheader

//go:wasmimport gd park
func park(id uint32)

//go:wasmimport gd exit
func exit(code int32)

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
	if len(allgs) == 0 {
		allgs = append(allgs, nil, &maing)
	}
	gp := &g{id: len(allgs), fn: fn}
	allgs = append(allgs, gp)
	ngoroutine++
	goready(gp)
}

// goready makes gp runnable.
func goready(gp *g) {
	gp.link = nil
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
	park(uint32(gp.id))
	linsetsp(gp.sp)
	linsetsplim(gp.stacklo)
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
	gp := runqHead
	if gp == nil {
		return -1
	}
	runqHead = gp.link
	if runqHead == nil {
		runqTail = nil
	}
	gp.link = nil
	curg = gp
	if gp.started {
		return int64(gp.id) << 1
	}
	gp.started = true
	return int64(gp.id)<<1 | 1
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
	gp.fn()
	gdone(gp)
}

// gdone records that gp has ended.
func gdone(gp *g) {
	gp.dead = true
	gp.fn = nil
	ngoroutine--
	if gp == &maing {
		return
	}
	allgs[gp.id] = nil
	freestack(gp)
	linfree(gp.stack)
}

// deadlock is called by the host when no goroutine can run and nothing
// is going to wake one.
//
//go:nosplit
func deadlock() {
	systemstack(deadlock1)
}

func deadlock1() {
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
