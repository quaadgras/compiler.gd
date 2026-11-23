// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !wasm.gc

package maps

import (
	"internal/abi"
	"internal/goexperiment"
	"unsafe"
)

// Under GOEXPERIMENT=sharing, what is written to a map that other
// goroutines can reach (runtime/shared.go) is shared before they can find
// it, and its writers wait for each other instead of panicking
// (Map.lockWriteSlow). Without the experiment no map is shared and none
// of this runs: the functions below are constants then.

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

// sharedPutSlot is the assignment of a shared map. The key, and what the
// map allocates for it, are shared before another goroutine can find them.
func sharedPutSlot(typ *abi.MapType, m *Map, key unsafe.Pointer) unsafe.Pointer {
	typ.Hasher(key, m.seed) // may panic: not with allocations marked
	shareAllocs(true)
	slot := m.PutSlot(typ, key)
	shareAllocs(false)
	return slot
}
