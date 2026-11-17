// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

// The stack in linear memory, where the variables whose address is
// taken and that have no pointers live, is in segments. A function
// whose frame would go below the bottom of the segment in use (plus a
// red zone, which these functions run in) calls morestackgc for a
// segment of its own, and lessstackgc when it returns. The frames
// are not moved: a pointer to a variable on the stack is its address.
// (See cmd/compile/internal/wasm/gc.go and its GCGlobalStackLo.)
//
// The segments are also how the stack of the engine grows, which a
// goroutine's frames are really on (each goroutine has one, from the
// host), and which is of a size the engine fixes: about 1 MB in a
// browser. The frame of every function that calls others takes of the
// stack in linear memory in proportion to what it may take of the
// engine's (wasm.GCStackRatio), so a segment is full before the
// engine's stack is; and a function that gets a new segment runs, with
// it, on a new stack of the engine's: it calls itself again through
// the host (wasm.GCHop), which starts the stack, and returns what that
// call returns. A goroutine's stack is then as deep as its segments
// can be, as Go's is.

const (
	stackRedZone = 4 << 10 // cmd/internal/obj/wasm.GCStackRedZone
	stackSegSize = 64 << 10
	// A goroutine's stack elsewhere is at most 1 GB. Here that is what
	// the engine's stacks may take: wasm.GCStackRatio times as much as
	// the segments, which are what is counted.
	stackMaxTotal = 128 << 20
)

type stackSeg struct {
	base, size uintptr
	lo, sp     uintptr // the limit and the top of the stack to go back to
	prev       *stackSeg
}

// lincallersp returns where the stack in linear memory was when the
// function that calls it was called, and linsetcallersp sets where it
// is to be when that function returns. The compiler replaces calls to
// them.
func lincallersp() uintptr
func linsetcallersp(sp uintptr)

// linsetsplim and linsplim set and return the limit of the stack. The
// compiler replaces calls to them.
func linsetsplim(lo uintptr)
func linsplim() uintptr

// morestackgc makes a segment for the frame of n bytes of the function
// that calls it, and leaves the top of the stack at the top of it: the
// function then calls itself on a new stack of the engine's, where its
// frame is there.
//
//go:nosplit
func morestackgc(n uintptr) {
	// The function that calls this was perhaps called as a closure,
	// and takes the closure, when it calls itself, from where the
	// call left it: the allocation below may collect, and the
	// goroutines that run meanwhile call closures of theirs.
	ctxt := closurectxt()
	lo := linsplim()
	linsetsplim(0) // no checks while this runs, in the red zone
	if onSysStack {
		fatal("system stack overflow")
	}
	gp := curg
	size := uintptr(stackSegSize)
	if need := n + 2*stackRedZone; need > size {
		size = (need + 4095) &^ 4095
	}
	gp.stackTotal += size
	if gp.stackTotal > stackMaxTotal {
		printstring("runtime: goroutine stack exceeds 1000000000-byte limit\n") // (of the engine's stack)
		fatal("stack overflow")
	}
	seg := gp.spareSeg
	if seg != nil && seg.size >= size {
		gp.spareSeg = nil
	} else {
		seg = &stackSeg{base: linalloc(size), size: size}
	}
	seg.prev = gp.segs
	seg.lo = lo
	seg.sp = lincallersp()
	gp.segs = seg
	gp.stacklo = seg.base + stackRedZone
	linsetsplim(gp.stacklo)
	linsetcallersp(seg.base + seg.size)
	setclosurectxt(ctxt)
}

// lessstackgc is called by the function that called morestackgc when
// its call of itself returns: the segment is no longer in use.
//
//go:nosplit
func lessstackgc() {
	gp := curg
	seg := gp.segs
	gp.segs = seg.prev
	gp.stacklo = seg.lo
	gp.stackTotal -= seg.size
	if gp.spareSeg == nil {
		gp.spareSeg = seg
	} else {
		linfree(seg.base)
	}
	linsetsplim(gp.stacklo)
	linsetcallersp(seg.sp)
}

// stackunwindgc is called by a function that a recovered panic returns
// to, whose frame is at fp: the segments of the frames below it are no
// longer in use.
//
//go:nosplit
func stackunwindgc(fp uintptr) {
	gp := curg
	for seg := gp.segs; seg != nil && !(seg.base <= fp && fp < seg.base+seg.size); seg = gp.segs {
		lessstackgc()
	}
}

// freestack frees the segments of the stack of gp, which has ended.
func freestack(gp *g) {
	for seg := gp.segs; seg != nil; seg = seg.prev {
		linfree(seg.base)
	}
	if gp.spareSeg != nil {
		linfree(gp.spareSeg.base)
	}
	gp.segs, gp.spareSeg = nil, nil
}

// The stack that what the host calls (sched, timerwait...) runs on: not
// that of a goroutine, whose segments it would otherwise use.
var (
	sysStack   uintptr
	onSysStack bool
)

const sysStackSize = 256 << 10

// systemstack calls fn on the system stack.
//
//go:nosplit
func systemstack(fn func()) {
	if onSysStack {
		fn()
		return
	}
	sp, lo := linsp(), linsplim()
	linsetsplim(0)
	onSysStack = true
	if sysStack == 0 {
		sysStack = linalloc(sysStackSize)
	}
	linsetsplim(sysStack + stackRedZone)
	linsetsp(sysStack + sysStackSize)
	fn()
	onSysStack = false
	linsetsp(sp)
	linsetsplim(lo)
}
