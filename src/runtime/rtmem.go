// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/runtime/atomic"
	"unsafe"
)

// The memory that is the runtime's own (doc/gd/unsafe.md).
//
// A pointer that a program makes of an integer, with nothing that says
// where the integer came from, is one to memory that is not Go's: what
// the system returned, a file that was mapped, a device. That is what
// such a conversion is for, and nothing bounds it. It is not for the
// memory that the runtime keeps for itself outside the objects of the
// heap: the bitmaps and the spans of the heap, the stacks of the
// goroutines and of the threads, the tables of the program. A pointer
// to those would be a pointer to everything.
//
// So the runtime knows what is its own: the address space of the heap
// (heapArenaOf), the modules of the program, and what it asked the
// system for besides (rtMem, which sysAlloc and sysMap add to and
// sysFree takes from: an address that is only reserved is no memory).

// rtMem is the memory that the runtime asked the system for outside the
// heap's address space. A range is counted and then written, and one
// that is given back is made empty: a reader needs no lock. If there are more than it has room
// for, every address that is not known otherwise is taken for one.
var rtMem struct {
	n    atomic.Uint32
	full atomic.Bool
	r    [1 << 12]struct{ lo, hi atomic.Uintptr }
}

// rtMemAdd says that the n bytes at p are the runtime's.
//
//go:nosplit
func rtMemAdd(p unsafe.Pointer, n uintptr) {
	if p == nil || n == 0 {
		return
	}
	i := rtMem.n.Add(1) - 1
	if i >= uint32(len(rtMem.r)) {
		rtMem.full.Store(true)
		return
	}
	rtMem.r[i].lo.Store(uintptr(p))
	rtMem.r[i].hi.Store(uintptr(p) + n)
}

// rtMemRemove says that the n bytes at p were given back to the system:
// what the system maps there next is not the runtime's. A range that
// they are the start or the end of is made shorter, and any other that
// they are part of is forgotten whole.
//
//go:nosplit
func rtMemRemove(p unsafe.Pointer, n uintptr) {
	lo, hi := uintptr(p), uintptr(p)+n
	cnt := min(rtMem.n.Load(), uint32(len(rtMem.r)))
	for i := uint32(0); i < cnt; i++ {
		r := &rtMem.r[i]
		rlo, rhi := r.lo.Load(), r.hi.Load()
		switch {
		case rhi <= lo || hi <= rlo:
		case lo <= rlo && rhi <= hi, rlo < lo && hi < rhi:
			r.hi.Store(0)
			r.lo.Store(0)
		case lo <= rlo:
			r.lo.Store(hi)
		default:
			r.hi.Store(lo)
		}
	}
}

// rtMemOwned reports whether a is an address of memory that the runtime
// keeps for itself, or of the program's own code and tables: not of an
// object of the heap that is allocated, nor of a variable, which the
// caller looked for first.
func rtMemOwned(a uintptr) bool {
	if heapArenaOf(a) != nil {
		// The heap's address space: a span that is not of objects
		// (a stack, what the allocator keeps), or pages that are
		// free.
		return true
	}
	for datap := &firstmoduledata; datap != nil; datap = datap.next {
		if datap.text <= a && a < datap.end || datap.minpc <= a && a < datap.maxpc {
			return true
		}
	}
	n := min(rtMem.n.Load(), uint32(len(rtMem.r)))
	for i := uint32(0); i < n; i++ {
		if hi := rtMem.r[i].hi.Load(); rtMem.r[i].lo.Load() <= a && a < hi {
			return true
		}
	}
	return rtMem.full.Load()
}
