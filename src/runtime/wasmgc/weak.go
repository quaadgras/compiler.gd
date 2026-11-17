// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

import "unsafe"

// Weak pointers are the host's WeakRefs: gd.weakmake returns the same
// number for the same object, and gd.weakget the object, or nil once it
// is collected. A pointer into an object (a box, which is made anew for
// each pointer) is weak on the object, with the offset kept; a pointer
// to linear memory is held strongly.

type weakHandle struct {
	id     int32
	off    uintptr
	strong unsafe.Pointer
	addr   uintptr // split: the address
}

type weakKey struct {
	id  int32
	off uintptr
}

var (
	weakHandles map[weakKey]*weakHandle
	weakLinear  map[uintptr]*weakHandle
)

//go:linkname weak_runtime_registerWeakPointer weak.runtime_registerWeakPointer
func weak_runtime_registerWeakPointer(p unsafe.Pointer) unsafe.Pointer {
	if splitMode && p != nil && uintptr(p) >= uintptr(heapstart()) && uintptr(p) < linbrk {
		// A block: weak on its array, which the host keeps a
		// WeakRef to.
		k := weakKey{weakmake(p), uintptr(p)}
		if weakHandles == nil {
			weakHandles = make(map[weakKey]*weakHandle)
		}
		h := weakHandles[k]
		if h == nil {
			h = &weakHandle{id: k.id, off: ptroff(p), addr: uintptr(p)}
			weakHandles[k] = h
		}
		return unsafe.Pointer(h)
	}
	if a := trylinaddr(p); a != 0 || splitMode {
		// (Data or a stack: never freed.)
		if weakLinear == nil {
			weakLinear = make(map[uintptr]*weakHandle)
		}
		h := weakLinear[a]
		if h == nil {
			h = &weakHandle{strong: p}
			weakLinear[a] = h
		}
		return unsafe.Pointer(h)
	}
	k := weakKey{weakmake(boxobj(p)), boxoff(p)}
	if weakHandles == nil {
		weakHandles = make(map[weakKey]*weakHandle)
	}
	h := weakHandles[k]
	if h == nil {
		h = &weakHandle{id: k.id, off: k.off}
		weakHandles[k] = h
	}
	return unsafe.Pointer(h)
}

//go:linkname weak_runtime_makeStrongFromWeak weak.runtime_makeStrongFromWeak
func weak_runtime_makeStrongFromWeak(u unsafe.Pointer) unsafe.Pointer {
	h := (*weakHandle)(u)
	if h.strong != nil {
		return h.strong
	}
	if splitMode {
		return weakgetat(h.id, uint32(h.addr), uint32(h.off))
	}
	p := weakget(h.id)
	if p == nil || h.off == 0 {
		return p
	}
	return add(p, h.off)
}

// The cleanup queue is always empty: cleanups do not run.
//
//go:linkname unique_runtime_blockUntilEmptyCleanupQueue unique.runtime_blockUntilEmptyCleanupQueue
func unique_runtime_blockUntilEmptyCleanupQueue(timeout int64) bool {
	return blockUntilEmptyCleanupQueue(timeout)
}

// blockUntilEmptyCleanupQueue waits, up to timeout nanoseconds, until
// no cleanup is queued or running.
func blockUntilEmptyCleanupQueue(timeout int64) bool {
	end := nanotime() + timeout
	for cleanupG {
		if nanotime() > end {
			return false
		}
		Gosched()
	}
	return true
}

//go:wasmimport gd weakmake
func weakmake(p unsafe.Pointer) int32

//go:wasmimport gd weakget
func weakget(id int32) unsafe.Pointer

//go:wasmimport gd weakgetat
func weakgetat(id int32, addr, off uint32) unsafe.Pointer

//go:linkname sync_test_runtime_blockUntilEmptyCleanupQueue sync_test.runtime_blockUntilEmptyCleanupQueue
func sync_test_runtime_blockUntilEmptyCleanupQueue(timeout int64) bool {
	return blockUntilEmptyCleanupQueue(timeout)
}
