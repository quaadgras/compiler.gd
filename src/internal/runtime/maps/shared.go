// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !wasm.gc

package maps

import (
	"internal/abi"
	"internal/goexperiment"
	"internal/runtime/atomic"
	"unsafe"
)

// A map that other goroutines can reach (runtime/shared.go) is operated on
// with its lock held, by any number of readers or by one writer, so that
// goroutines that race on it see a map and not a table half grown. One
// that only its goroutine reaches takes no lock: every operation starts by
// asking which it is.
//
// The lock is held for the operation only. The slot an access or an
// assignment returns is read or written after it, as the slots an
// iteration returns are.
//
// Without the sharing experiment (GOEXPERIMENT=sharing) no map is shared
// and none of this runs: the functions below are constants then.

func isShared(p unsafe.Pointer) bool {
	return goexperiment.Sharing && runtimeIsShared(p)
}

// shareAllocs sets whether what this goroutine allocates for a map is
// shared from the start: newobject's and newarray's memory, and what is
// passed to shareAlloc.
func shareAllocs(on bool) {
	if goexperiment.Sharing {
		runtimeShareAllocs(on)
	}
}

func shareAlloc(p unsafe.Pointer) {
	if goexperiment.Sharing {
		runtimeShareAlloc(p)
	}
}

//go:linkname runtimeIsShared
func runtimeIsShared(p unsafe.Pointer) bool

//go:linkname runtimeShareAllocs
func runtimeShareAllocs(on bool)

//go:linkname runtimeShareAlloc
func runtimeShareAlloc(p unsafe.Pointer)

// lockYield waits for a lock that was not free the last spins times.
//
//go:linkname lockYield
func lockYield(spins int)

const (
	lockWriter  = 1 << 31 // a writer holds the lock
	lockPending = 1 << 30 // a writer waits for it: no new readers
	// The other bits count the readers.
)

func (m *Map) rlock() {
	for spins := 0; ; spins++ {
		v := atomic.Load(&m.lock)
		if v&(lockWriter|lockPending) == 0 {
			if atomic.Cas(&m.lock, v, v+1) {
				return
			}
			continue
		}
		lockYield(spins)
	}
}

func (m *Map) runlock() {
	atomic.Xadd(&m.lock, -1)
}

func (m *Map) wlock() {
	for spins := 0; ; spins++ {
		v := atomic.Load(&m.lock)
		if v&^lockPending == 0 {
			if atomic.Cas(&m.lock, v, lockWriter) {
				return
			}
			continue
		}
		if v&lockPending == 0 {
			atomic.Or32(&m.lock, lockPending)
		}
		lockYield(spins)
	}
}

func (m *Map) wunlock() {
	atomic.And32(&m.lock, ^uint32(lockWriter))
}

// sharedGet is the access of a shared map.
func sharedGet(typ *abi.MapType, m *Map, key unsafe.Pointer) (unsafe.Pointer, bool) {
	typ.Hasher(key, m.seed) // may panic: not with the lock held
	m.rlock()
	elem, ok := m.Get(typ, key)
	m.runlock()
	if !ok {
		return unsafe.Pointer(&zeroVal[0]), false
	}
	return elem, true
}

// sharedPutSlot is the assignment of a shared map. The key, and what the
// map allocates for it, are shared before another goroutine can find them.
func sharedPutSlot(typ *abi.MapType, m *Map, key unsafe.Pointer) unsafe.Pointer {
	typ.Hasher(key, m.seed) // may panic: not with the lock held
	m.wlock()
	shareAllocs(true)
	slot := m.PutSlot(typ, key)
	shareAllocs(false)
	m.wunlock()
	return slot
}
