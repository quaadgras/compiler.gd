// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

import (
	"internal/abi"
	"unsafe"
)

// Maps. The maps of internal/runtime/maps are made of memory; this is
// a table with open addressing whose keys and elements are either in a
// block of linear memory, if they have no pointers, or objects, one
// each.
//
// The compiler knows two things about a map: that its length is its
// first word, and the first two fields of an iterator. It gives
// mapaccess1, mapaccess2 and mapassign one more argument than it
// declares: the zero value of an element that is an object, or nil.
//
// TODO: random iteration order; NaN keys; the hash of a string is not
// cached in the string; maps of more than 2^31 bytes of keys.

type gcmap struct {
	used uint64 // the number of entries: must be first
	seed uintptr
	tab  *maptable

	onStack bool // Go would have it on the stack: see makemap
}

type maptable struct {
	cap    int      // the number of slots: a power of two
	full   int      // the slots that are not empty: live or deleted
	hashes []uint64 // of each slot: slotEmpty, slotDeleted, or the hash of the key
	keys   mapstore
	elems  mapstore
}

const (
	slotEmpty   = 0
	slotDeleted = 1
	slotLive    = 2 // or'ed into every hash
)

// A mapstore holds the keys or the elements of a table.
type mapstore struct {
	typ  *abi.Type
	size uintptr
	mem  unsafe.Pointer // without pointers: the block of linear memory
	addr uintptr        // and its address
	objs unsafe.Pointer // with pointers: an array of objects
}

func newmapstore(t *abi.Type, n int) mapstore {
	s := mapstore{typ: t, size: t.Size_}
	if splitMode {
		s.mem = newarray(t, n)
		return s
	}
	if t.Pointers() {
		s.objs = newobjarray(n)
	} else {
		s.mem = linnew(uintptr(n) * t.Size_)
		s.addr = linaddr(s.mem)
	}
	return s
}

// ptr returns a pointer to entry i.
func (s *mapstore) ptr(i int) unsafe.Pointer {
	if splitMode {
		return add(s.mem, uintptr(i)*s.size)
	}
	if s.objs != nil {
		return objarrayget(s.objs, i)
	}
	return unsafe.Pointer(s.addr + uintptr(i)*s.size)
}

// put sets entry i to a copy of what p points to.
func (s *mapstore) put(i int, p unsafe.Pointer) {
	if splitMode {
		memmove(s.ptr(i), p, s.size)
		return
	}
	if s.objs != nil {
		if boxobj(p) != p {
			// A pointer into an object (a field, say: m[a.f] = v):
			// gcclone copies whole objects and would return it, and
			// the entry would change with the object.
			o := newobjectT(s.typ)
			memstatsMallocs-- // (a copy, as gcclone's)
			typedmemmove(s.typ, o, p)
			objarrayset(s.objs, i, o)
			return
		}
		objarrayset(s.objs, i, gcclone(p))
		return
	}
	memmove(s.ptr(i), p, s.size)
}

// move sets entry i to entry j of old.
func (s *mapstore) move(i int, old *mapstore, j int) {
	if splitMode {
		memmove(s.ptr(i), old.ptr(j), s.size)
		return
	}
	if s.objs != nil {
		objarrayset(s.objs, i, objarrayget(old.objs, j))
		return
	}
	memmove(s.ptr(i), old.ptr(j), s.size)
}

func newmaptable(t *abi.MapType, n int) *maptable {
	return &maptable{
		cap:    n,
		hashes: make([]uint64, n),
		keys:   newmapstore(t.Key, n),
		elems:  newmapstore(t.Elem, n),
	}
}

func makemap_small() *gcmap {
	return &gcmap{seed: uintptr(nanotime())}
}

func makemap(t *abi.MapType, hint int, m *gcmap) *gcmap {
	if hint < 0 {
		// A small map that does not escape (see walkMakeMap): not an
		// allocation in Go, nor its first eight entries.
		m = makemap_small()
		m.onStack = true
		return m
	}
	memstatsMallocs++
	m = makemap_small()
	if hint > 0 {
		n := 8
		for n*7 < hint*8 {
			n *= 2
		}
		m.tab = newmaptable(t, n)
	}
	return m
}

func makemap64(t *abi.MapType, hint int64, m *gcmap) *gcmap {
	return makemap(t, int(hint), m)
}

func maphash(t *abi.MapType, m *gcmap, key unsafe.Pointer) uint64 {
	if t.Hasher == nil {
		// A map type made by reflect.MapOf.
		return uint64(typehash(t.Key, key, m.seed)) | slotLive
	}
	return uint64(t.Hasher(key, m.seed)) | slotLive
}

// find returns the slot of key in m, or -1.
func (m *gcmap) find(t *abi.MapType, key unsafe.Pointer) int {
	tab := m.tab
	if tab == nil || m.used == 0 {
		mapKeyCheck(t, key)
		return -1
	}
	h := maphash(t, m, key)
	mask := tab.cap - 1
	for i := int(h) & mask; ; i = (i + 1) & mask {
		switch c := tab.hashes[i]; {
		case c == slotEmpty:
			return -1
		case c == h && typeEqual(t.Key, key, tab.keys.ptr(i)):
			return i
		}
	}
}

// mapKeyCheck panics if key, of a map that has no entries to compare
// it with, could not be hashed: as if it were looked for.
func mapKeyCheck(t *abi.MapType, key unsafe.Pointer) {
	if t.HashMightPanic() {
		if t.Hasher == nil {
			typehash(t.Key, key, 0)
		} else {
			t.Hasher(key, 0)
		}
	}
}

func mapaccess1(t *abi.MapType, m *gcmap, key, zero unsafe.Pointer) unsafe.Pointer {
	if m == nil {
		mapKeyCheck(t, key)
	}
	if m != nil {
		if i := m.find(t, key); i >= 0 {
			return m.tab.elems.ptr(i)
		}
	}
	if zero != nil {
		return zero
	}
	return unsafe.Pointer(&zeroVal[0])
}

func mapaccess2(t *abi.MapType, m *gcmap, key, zero unsafe.Pointer) (unsafe.Pointer, bool) {
	if m == nil {
		mapKeyCheck(t, key)
	}
	if m != nil {
		if i := m.find(t, key); i >= 0 {
			return m.tab.elems.ptr(i), true
		}
	}
	if zero != nil {
		return zero, false
	}
	return unsafe.Pointer(&zeroVal[0]), false
}

func mapaccess1_fat(t *abi.MapType, m *gcmap, key, zero unsafe.Pointer) unsafe.Pointer {
	if m != nil {
		if i := m.find(t, key); i >= 0 {
			return m.tab.elems.ptr(i)
		}
	}
	return zero
}

func mapaccess2_fat(t *abi.MapType, m *gcmap, key, zero unsafe.Pointer) (unsafe.Pointer, bool) {
	if m != nil {
		if i := m.find(t, key); i >= 0 {
			return m.tab.elems.ptr(i), true
		}
	}
	return zero, false
}

var zeroVal [abi.ZeroValSize]byte

// grow gives m a table of n slots.
func (m *gcmap) grow(t *abi.MapType, n int) {
	old := m.tab
	tab := newmaptable(t, n)
	m.tab = tab
	if old == nil {
		return
	}
	mask := n - 1
	for j, h := range old.hashes {
		if h < slotLive {
			continue
		}
		i := int(h) & mask
		for tab.hashes[i] != slotEmpty {
			i = (i + 1) & mask
		}
		tab.hashes[i] = h
		tab.keys.move(i, &old.keys, j)
		tab.elems.move(i, &old.elems, j)
		tab.full++
	}
}

func mapassign(t *abi.MapType, m *gcmap, key, zero unsafe.Pointer) unsafe.Pointer {
	if m == nil {
		panic(plainError("assignment to entry in nil map"))
	}
	if i := m.find(t, key); i >= 0 {
		if t.NeedKeyUpdate() {
			// +0 replaced by -0, a string by an equal one.
			m.tab.keys.put(i, key)
		}
		return m.tab.elems.ptr(i)
	}
	switch tab := m.tab; {
	case tab == nil:
		n := memstatsMallocs
		m.grow(t, 8)
		if m.onStack {
			memstatsMallocs = n
		}
	case (tab.full+1)*8 > tab.cap*7:
		// Too full: twice the size, unless deleting has left most
		// of it free.
		n := tab.cap
		if int(m.used+1)*2 > n {
			n *= 2
		}
		m.grow(t, n)
	}
	tab := m.tab
	h := maphash(t, m, key)
	mask := tab.cap - 1
	i := int(h) & mask
	for tab.hashes[i] >= slotLive {
		i = (i + 1) & mask
	}
	if tab.hashes[i] == slotEmpty {
		tab.full++
	}
	tab.hashes[i] = h
	tab.keys.put(i, key)
	switch {
	case zero != nil:
		tab.elems.put(i, zero)
	case tab.elems.objs != nil && !splitMode:
		// (From reflect, which has no zero object to copy.)
		objarrayset(tab.elems.objs, i, newobjectT(t.Elem))
	default:
		memclrNoHeapPointers(tab.elems.ptr(i), tab.elems.size)
	}
	m.used++
	return tab.elems.ptr(i)
}

func mapdelete(t *abi.MapType, m *gcmap, key unsafe.Pointer) {
	if m == nil {
		mapKeyCheck(t, key)
		return
	}
	i := m.find(t, key)
	if i < 0 {
		return
	}
	tab := m.tab
	tab.hashes[i] = slotDeleted
	if splitMode && t.Key.Pointers() {
		memclrNoHeapPointers(tab.keys.ptr(i), tab.keys.size)
	}
	if splitMode && t.Elem.Pointers() {
		memclrNoHeapPointers(tab.elems.ptr(i), tab.elems.size)
	}
	if tab.keys.objs != nil && !splitMode {
		objarrayset(tab.keys.objs, i, nil)
	}
	if tab.elems.objs != nil && !splitMode {
		objarrayset(tab.elems.objs, i, nil)
	}
	m.used--
}

func mapclear(t *abi.MapType, m *gcmap) {
	if m == nil {
		return
	}
	m.tab = nil
	m.used = 0
}

// mapIter is the iterator of a range over a map. The compiler
// allocates it, as an internal/runtime/maps.Iter, of which this has the
// fields: only some are used.
type mapIter struct {
	key  unsafe.Pointer // must be first: nil at the end
	elem unsafe.Pointer // must be second
	typ  *abi.MapType
	m    *gcmap

	entryOffset uint64
	dirOffset   uint64

	clearSeq uint64

	globalDepth uint8

	dirIdx int

	tab *maptable // the table when the iteration started

	group unsafe.Pointer

	entryIdx uint64 // the next slot of tab
}

func mapIterStart(t *abi.MapType, m *gcmap, it *mapIter) {
	it.typ = t
	it.m = m
	it.key = nil
	it.elem = nil
	if m == nil || m.used == 0 {
		return
	}
	it.tab = m.tab
	it.entryIdx = 0
	mapIterNext(it)
}

func mapIterNext(it *mapIter) {
	tab := it.tab
	for i := int(it.entryIdx); i < tab.cap; i++ {
		if tab.hashes[i] < slotLive {
			continue
		}
		it.entryIdx = uint64(i + 1)
		if tab == it.m.tab {
			it.key = tab.keys.ptr(i)
			it.elem = tab.elems.ptr(i)
			return
		}
		// The map has grown or was cleared since: the entry is
		// where it is now, if it is still there.
		if j := it.m.find(it.typ, tab.keys.ptr(i)); j >= 0 {
			it.key = it.m.tab.keys.ptr(j)
			it.elem = it.m.tab.elems.ptr(j)
			return
		}
	}
	it.key = nil
	it.elem = nil
}
