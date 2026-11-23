// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !wasm.gc

package maps

import (
	"internal/abi"
	"internal/goarch"
	"internal/runtime/atomic"
	"unsafe"
)

// A data race on a map is memory safe (doc/gd/maps.md).
//
// Map.seq is a sequence lock: odd while a writer holds the map. A writer
// takes it with one compare-and-swap and panics if it is held: it never
// waits, and the loser has not touched the map. A reader writes nothing.
// It loads the fields of the header that go together between two loads
// of seq (readBegin), which is what makes them go together, and then
// follows only what is safe whatever a writer does meanwhile: a table's
// groups and mask never change once it is published, tables and
// directories stay alive and are never reused, and a probe is bounded.
// So the second check of seq, at the end of a lookup (readRetry), is not
// what makes the lookup safe. It makes its answer the answer of one state
// of the map, and after mapReadTries the reader takes the answer it has.

// mapReadTries is how many times a lookup that overlapped a write starts
// again before it returns what it found, a stale but safe answer. A
// writer in a loop could otherwise starve it.
const mapReadTries = 8

// mapCloneTries is how many times Clone copies a map that a writer
// overlapped before it holds the map to copy it.
const mapCloneTries = 4

// errConcurrentWrites is what a writer panics with if another holds the
// map. Pushed from runtime, to use runtime.plainError.
//
//go:linkname errConcurrentWrites
var errConcurrentWrites error

//go:linkname runtimePublicationBarrier
func runtimePublicationBarrier()

// storeBarrier orders the stores before it before those after it. It is
// an intrinsic on arm64 (DMB ISHST).
func storeBarrier() {
	if goarch.IsAmd64|goarch.Is386|goarch.IsWasm == 0 {
		runtimePublicationBarrier()
	}
}

// loadBarrier orders the loads before it before those after it. It is an
// intrinsic on arm64 (DMB ISHLD). Elsewhere an atomic operation on a word
// of its own is a barrier, where one is needed.
func loadBarrier() {
	if goarch.IsAmd64|goarch.Is386|goarch.IsWasm != 0 {
		return
	}
	var x uint32
	atomic.Xadd(&x, 1)
}

// lockWrite takes the map for a write, or panics if another writer has
// it. (Under GOEXPERIMENT=sharing the writers of a shared map wait.)
func (m *Map) lockWrite() {
	// (If a writer holds it, seq is odd and this compares it with an
	// even value.)
	if s := m.seq &^ 1; !atomic.Cas(&m.seq, s, s+1) {
		m.lockWriteSlow()
	}
	// The lock is seen held before any of the stores that follow.
	storeBarrier()
}

func (m *Map) lockWriteSlow() {
	if !isShared(unsafe.Pointer(m)) {
		panic(errConcurrentWrites)
	}
	m.lockWriteWait()
}

// lockWriteWait takes the map for a write, waiting for it.
func (m *Map) lockWriteWait() {
	for spins := 0; ; spins++ {
		s := atomic.Load(&m.seq)
		if s&1 == 0 && atomic.Cas(&m.seq, s, s+1) {
			storeBarrier()
			return
		}
		lockYield(spins)
	}
}

// unlockWrite ends a write: everything it stored is seen before the lock
// is seen free.
func (m *Map) unlockWrite() {
	atomic.StoreRel(&m.seq, m.seq+1)
}

// lockWriteHash hashes key and takes the map for a write. The hash comes
// first because Hasher may panic, on a key of a type that has no hash, and
// then the map is neither written nor held. The seed is the map's if the
// map is held: a write that came between changes it (Clear, the delete of
// the last entry), and the key is hashed again.
func (m *Map) lockWriteHash(typ *abi.MapType, key unsafe.Pointer) uintptr {
	for {
		seed := m.seed
		hash := typ.Hasher(key, seed)
		m.lockWrite()
		if m.seed == seed {
			return hash
		}
		m.unlockWrite()
	}
}

// readBegin returns the map's sequence and the fields of the header that
// say where its tables are, all from one state of the map: no write was
// in progress or came between the two loads of seq. These loads are
// atomic so that they stay in order where loads may be reordered.
//
// The hot paths call readFields and readOK themselves, which inline.
func (m *Map) readBegin() (seq uint32, dir unsafe.Pointer, dirLen int, shift uint8) {
	seq, dir, dirLen, shift = m.readFields()
	if !m.readOK(seq) {
		return m.readBeginSlow()
	}
	return
}

// readFields is the first half of readBegin.
func (m *Map) readFields() (seq uint32, dir unsafe.Pointer, dirLen int, shift uint8) {
	seq = atomic.Load(&m.seq)
	dirLen = int(atomic.Loaduintptr((*uintptr)(unsafe.Pointer(&m.dirLen))))
	dir = atomic.Loadp(unsafe.Pointer(&m.dirPtr))
	shift = atomic.Load8(&m.globalShift)
	return
}

// readOK is the second half of readBegin: it reports whether what
// readFields returned with seq is from one state of the map.
func (m *Map) readOK(seq uint32) bool {
	return seq&1 == 0 && atomic.Load(&m.seq) == seq
}

// readBeginSlow is readBegin when a write is in progress: it waits for
// one state of the header. A writer holds the map for a short time and
// runs no user code meanwhile.
func (m *Map) readBeginSlow() (seq uint32, dir unsafe.Pointer, dirLen int, shift uint8) {
	for spins := 0; ; spins++ {
		seq = atomic.Load(&m.seq)
		if seq&1 == 0 {
			dirLen = int(atomic.Loaduintptr((*uintptr)(unsafe.Pointer(&m.dirLen))))
			dir = atomic.Loadp(unsafe.Pointer(&m.dirPtr))
			shift = atomic.Load8(&m.globalShift)
			if atomic.Load(&m.seq) == seq {
				return
			}
		}
		lockYield(spins)
	}
}

// readChanged reports whether a write began since readBegin returned seq.
func (m *Map) readChanged(seq uint32) bool {
	loadBarrier()
	return atomic.Load(&m.seq) != seq
}

// readRetry reports whether a lookup that began at seq should start
// again: a write overlapped it, and it has not tried mapReadTries times.
func (m *Map) readRetry(seq uint32, tries *int) bool {
	if !m.readChanged(seq) {
		return false
	}
	*tries++
	return *tries < mapReadTries
}

// updateKey stores key, which compares equal to the key in the slot at
// slot (+0 and -0, or a string with another array), as an assignment
// does. The key slotKey points to is the slot's, or if the key is
// indirect, its own object: that object is never written once it is in a
// slot, since a reader may be comparing it, so the new key is a new one.
func updateKey(typ *abi.MapType, slot, slotKey, key unsafe.Pointer, local bool) {
	if typ.IndirectKey() {
		kmem := newobjectIn(typ.Key, local)
		typedmemmove(typ.Key, kmem, key)
		*(*unsafe.Pointer)(slot) = kmem
		return
	}
	typedmemmove(typ.Key, slotKey, key)
}

// dirIndex is the index in a directory of dirLen tables of a hash.
func dirIndex(hash uintptr, dirLen int, shift uint8) uintptr {
	if dirLen == 1 {
		return 0
	}
	return hash >> (shift & 63)
}

// dirAt is the table at index i of the directory dir.
func dirAt(dir unsafe.Pointer, i uintptr) *table {
	return *(**table)(unsafe.Pointer(uintptr(dir) + goarch.PtrSize*i))
}

// depthOf is the global depth of a directory of dirLen tables whose
// shift is shift. (A small map has no directory, and depth 0.)
func depthOf(dirLen int, shift uint8) uint8 {
	if dirLen <= 0 {
		return 0
	}
	return depthToShift(0) - shift
}
