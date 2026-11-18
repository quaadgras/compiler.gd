// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Goroutine sharing.
//
// A heap object is unshared while only the goroutine that allocated it can
// reach it, and shared from the moment a second goroutine might: when it is
// handed to a new goroutine, sent on a channel other goroutines can reach,
// or stored in a global or in an object that is already shared. Sharing is
// for the life of the object, and goes to everything the object points to.
//
// The goroutine that hands an object over marks it, and everything
// reachable from it, before the pointer leaves its hands (publish). Until
// then no other goroutine has a pointer to any of it, so the graph does not
// change under the walk, and a goroutine that finds one of its own objects
// unshared knows that it still is: only it could have shared it.
//
// What the bit is for: operations that are not memory safe when two
// goroutines race on them (a map's, the load and store of a value of more
// than one word) take a lock on shared memory, and none on the rest.
//
// The bits are one per object, in a bitmap of the span, as the pinner's
// are, allocated when a span gets its first shared object. Memory that is
// not the heap's is shared if it is not a goroutine's stack: globals are,
// and whatever else the runtime does not know.

package runtime

import (
	"internal/abi"
	"internal/goarch"
	"internal/runtime/atomic"
	"unsafe"
)

// sharingEnabled is whether objects are marked shared at all. Where
// goroutines run one at a time and are never preempted between two
// instructions (wasm), no operation can race.
const sharingEnabled = GOARCH != "wasm"

func (s *mspan) getSharedBits() *gcBits {
	return (*gcBits)(atomic.Loadp(unsafe.Pointer(&s.sharedBits)))
}

// isShared reports whether another goroutine may reach the memory p points
// to. A nil p is not shared.
//
// Bits a concurrent sweep unlinks stay valid until the next cycle, as the
// pinner's do, and this function has no safe point.
//
//go:nosplit
func isShared(p unsafe.Pointer) bool {
	if !sharingEnabled || p == nil {
		return false
	}
	s := spanOf(uintptr(p))
	if s == nil {
		return true
	}
	if s.state.get() != mSpanInUse || uintptr(p) < s.base() || uintptr(p) >= s.limit {
		// A stack, or memory the runtime allocates for itself.
		return false
	}
	if s.isUserArenaChunk {
		return true
	}
	bits := s.getSharedBits()
	if bits == nil {
		return false
	}
	i := s.objIndex(uintptr(p))
	shared := atomic.Load8(bits.bytep(i/8))&(1<<(i%8)) != 0
	KeepAlive(p)
	return shared
}

// markShared marks the heap object p points into as shared. If it was not,
// it returns the object's base and span; otherwise, or if p does not point
// into a heap object, zero.
func markShared(p uintptr) (base uintptr, s *mspan) {
	s = spanOf(p)
	if s == nil || s.state.get() != mSpanInUse || p < s.base() || p >= s.limit || s.isUserArenaChunk {
		return 0, nil
	}
	i := s.objIndex(p)
	mask := uint8(1) << (i % 8)
	if bits := s.getSharedBits(); bits != nil && atomic.Load8(bits.bytep(i/8))&mask != 0 {
		return 0, nil
	}

	// The sweeper replaces the bitmap, so the span must be swept and stay so.
	mp := acquirem()
	s.ensureSwept()
	lock(&s.speciallock)
	bits := s.getSharedBits()
	if bits == nil {
		bits = newMarkBits(uintptr(s.nelems))
		atomic.StorepNoWB(unsafe.Pointer(&s.sharedBits), unsafe.Pointer(bits))
	}
	b := bits.bytep(i / 8)
	fresh := atomic.Load8(b)&mask == 0
	if fresh {
		atomic.Or8(b, mask)
	}
	unlock(&s.speciallock)
	releasem(mp)
	if !fresh {
		return 0, nil
	}
	return s.base() + i*s.elemsize, s
}

// refreshSharedBits moves the bits of the objects that are still allocated
// to a bitmap of the next cycle. The sweeper calls it once allocBits are
// the marks of the cycle that ended.
func (s *mspan) refreshSharedBits() {
	old := s.getSharedBits()
	if old == nil {
		return
	}
	n := (uintptr(s.nelems) + 63) / 64
	src := unsafe.Slice((*uint64)(unsafe.Pointer(old)), n)
	live := unsafe.Slice((*uint64)(unsafe.Pointer(s.allocBits)), n)
	var any uint64
	for i := range src {
		any |= src[i] & live[i]
	}
	if any == 0 {
		atomic.StorepNoWB(unsafe.Pointer(&s.sharedBits), nil)
		return
	}
	bits := newMarkBits(uintptr(s.nelems))
	dst := unsafe.Slice((*uint64)(unsafe.Pointer(bits)), n)
	for i := range src {
		dst[i] = src[i] & live[i]
	}
	atomic.StorepNoWB(unsafe.Pointer(&s.sharedBits), unsafe.Pointer(bits))
}

// publish marks the object p points into, and every object reachable from
// it, as shared. The caller owns whatever of it is not shared yet, and
// calls publish before another goroutine can load p.
func publish(p unsafe.Pointer) {
	if !sharingEnabled || p == nil {
		return
	}
	base, s := markShared(uintptr(p))
	if base == 0 {
		return
	}
	publishObject(base, s)
	KeepAlive(p)
}

// publishObject shares what the newly shared object at base, in s, points
// to. The objects still to walk are kept as pointers: the walk allocates,
// and may be preempted for a collection.
func publishObject(base uintptr, s *mspan) {
	var buf [32]unsafe.Pointer
	work := buf[:0]
	for {
		if !s.spanclass.noscan() {
			tp := s.typePointersOfUnchecked(base)
			for {
				var addr uintptr
				if tp, addr = tp.next(base + s.elemsize); addr == 0 {
					break
				}
				q := *(*unsafe.Pointer)(unsafe.Pointer(addr))
				if q == nil {
					continue
				}
				if b, _ := markShared(uintptr(q)); b != 0 {
					work = append(work, unsafe.Pointer(b))
				}
			}
		}
		if len(work) == 0 {
			break
		}
		q := work[len(work)-1]
		work = work[:len(work)-1]
		base = uintptr(q)
		s = spanOf(base)
	}
	// The marks are visible before any pointer the caller stores is.
	publicationBarrier()
}

// publishValue publishes every pointer of the value of type typ at p.
func publishValue(typ *abi.Type, p unsafe.Pointer) {
	if !sharingEnabled || !typ.Pointers() {
		return
	}
	publishWords(typ, p, 0, typ.PtrBytes)
}

// publishWords publishes the pointers in the bytes [off, off+size) of the
// value of type typ, which are at p.
func publishWords(typ *abi.Type, p unsafe.Pointer, off, size uintptr) {
	mask := getGCMask(typ)
	end := min(off+size, typ.PtrBytes)
	for o := alignUp(off, goarch.PtrSize); o < end; o += goarch.PtrSize {
		w := o / goarch.PtrSize
		if *addb(mask, w/8)>>(w%8)&1 != 0 {
			publish(*(*unsafe.Pointer)(add(p, o-off)))
		}
	}
}

// publishStore is called before a store of pointers at dst: if dst is
// shared, so is what is stored there.
func publishStore(typ *abi.Type, dst, src unsafe.Pointer) {
	if sharingEnabled && typ.Pointers() && isShared(dst) {
		publishValue(typ, src)
	}
}

// publishptr is called by compiled code before it stores the pointer val
// at dst, unless it knows that dst is in the stack or in an object that it
// has just allocated.
//
//go:nosplit
func publishptr(dst, val unsafe.Pointer) {
	if isShared(dst) {
		publish(val)
	}
}

// publishmove is called by compiled code before it copies a value of type
// typ, which has pointers, from src to dst.
//
//go:nosplit
func publishmove(typ *abi.Type, dst, src unsafe.Pointer) {
	if isShared(dst) {
		publishValue(typ, src)
	}
}

// Maps (internal/runtime/maps/shared.go).

//go:linkname maps_isShared internal/runtime/maps.isShared
//go:nosplit
func maps_isShared(p unsafe.Pointer) bool {
	return isShared(p)
}

// maps_shareAllocs sets whether what the goroutine allocates for a map is
// shared: it is while it writes a shared map.
//
//go:linkname maps_shareAllocs internal/runtime/maps.shareAllocs
func maps_shareAllocs(on bool) {
	getg().shareAllocs = on
}

//go:linkname maps_shareAlloc internal/runtime/maps.shareAlloc
func maps_shareAlloc(p unsafe.Pointer) {
	if getg().shareAllocs {
		markShared(uintptr(p))
	}
}

//go:linkname maps_lockYield internal/runtime/maps.lockYield
func maps_lockYield(spins int) {
	switch {
	case spins < 4:
		procyield(20)
	case spins < 16:
		osyield()
	default:
		Gosched()
	}
}
