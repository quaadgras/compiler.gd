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

const (
	stackRedZone  = 4 << 10 // cmd/internal/obj/wasm.GCStackRedZone
	stackSegSize  = 64 << 10
	stackMaxTotal = 1 << 30 // as the 1 GB of a goroutine's stack elsewhere
)

type stackSeg struct {
	base, size uintptr
	lo         uintptr // the limit to go back to
	prev       *stackSeg
}

// linsetsplim and linsplim set and return the limit of the stack. The
// compiler replaces calls to them.
func linsetsplim(lo uintptr)
func linsplim() uintptr

// morestackgc gives the frame of n bytes of the function that calls it
// a segment, and returns the top of it.
//
//go:nosplit
func morestackgc(n uintptr) uintptr {
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
		printstring("runtime: goroutine stack exceeds 1000000000-byte limit\n")
		fatal("stack overflow")
	}
	seg := gp.spareSeg
	if seg != nil && seg.size >= size {
		gp.spareSeg = nil
	} else {
		seg = &stackSeg{base: linalloc(size), size: size}
	}
	seg.prev = gp.segs
	seg.lo = gp.stacklo
	gp.segs = seg
	gp.stacklo = seg.base + stackRedZone
	linsetsplim(gp.stacklo)
	return seg.base + seg.size
}

// lessstackgc is called by the function that called morestackgc as it
// returns: its segment is no longer in use.
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
