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
	"internal/runtime/sys"
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
	if sharePages[uintptr(p)>>sharePageShift&uintptr(len(sharePages)-1)] == 0 {
		// Nothing shared was ever in a page like this one.
		return false
	}
	s := spanOf(uintptr(p))
	if s == nil {
		// Not the heap's: a global is shared. What else there is
		// (the runtime's own memory, memory from the system) is not
		// where Go's pointers are handed over.
		return isGlobal(uintptr(p))
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

// sharePages has a byte for each page of memory, found from the page's
// address modulo the table's size, that is not zero if something in a page
// with that byte is shared, or was: an object of the heap, or a module's
// globals. Most of what a program stores in is in pages where nothing is,
// and one load says so, in the runtime and in compiled code before it
// calls the runtime at all (ssagen's shareLock). The bytes are only ever
// set.
var sharePages [1 << 20]uint8

const sharePageShift = 13 // must match ssagen's

// sharePagesSet sets the bytes of the pages of [p, p+size).
func sharePagesSet(p, size uintptr) {
	for a := p >> sharePageShift; a <= (p+size-1)>>sharePageShift; a++ {
		if b := &sharePages[a&uintptr(len(sharePages)-1)]; *b == 0 {
			*b = 1
		}
	}
}

// shareModule is called when a module is loaded: its globals are shared.
func shareModule(datap *moduledata) {
	if !sharingEnabled {
		return
	}
	for _, r := range [...][2]uintptr{{datap.data, datap.edata}, {datap.bss, datap.ebss}, {datap.noptrdata, datap.enoptrdata}, {datap.noptrbss, datap.enoptrbss}} {
		if r[1] > r[0] {
			sharePagesSet(r[0], r[1]-r[0])
		}
	}
}

// isGlobal reports whether p is in the data or bss of a module.
//
//go:nosplit
func isGlobal(p uintptr) bool {
	for datap := &firstmoduledata; datap != nil; datap = datap.next {
		if datap.data <= p && p < datap.edata || datap.bss <= p && p < datap.ebss || datap.noptrdata <= p && p < datap.enoptrdata || datap.noptrbss <= p && p < datap.enoptrbss {
			return true
		}
	}
	return false
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
	if s.isFree(i) {
		// A pointer that outlived its object: whatever is
		// allocated there next is not shared for it.
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
		sharePagesSet(s.base()+i*s.elemsize, s.elemsize)
		atomic.Or8(b, mask)
	}
	unlock(&s.speciallock)
	releasem(mp)
	if fresh && s.spanclass.local() {
		localPublished(s.base() + i*s.elemsize)
	}
	if !fresh {
		return 0, nil
	}
	return s.base() + i*s.elemsize, s
}

// refreshSharedBits moves the bits of the objects that are still allocated
// to a bitmap of the next cycle. The sweeper calls it once allocBits are
// the marks of the cycle that ended.
func (s *mspan) refreshSharedBits() {
	s.refreshObjectBits(&s.sharedBits)
}

// refreshObjectBits moves the bits, in the bitmap at *field of s, of the
// objects that are still allocated to a bitmap of the next cycle, or
// leaves none if there is none of them.
func (s *mspan) refreshObjectBits(field **gcBits) {
	old := (*gcBits)(atomic.Loadp(unsafe.Pointer(field)))
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
		atomic.StorepNoWB(unsafe.Pointer(field), nil)
		return
	}
	bits := newMarkBits(uintptr(s.nelems))
	dst := unsafe.Slice((*uint64)(unsafe.Pointer(bits)), n)
	for i := range src {
		dst[i] = src[i] & live[i]
	}
	atomic.StorepNoWB(unsafe.Pointer(field), unsafe.Pointer(bits))
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
	shareYield(spins)
}

// shareYield waits for a lock that was not free the last spins times.
func shareYield(spins int) {
	switch {
	case spins < 4:
		procyield(20)
	case spins < 16 || getg().m.locks != 0 || getg() == getg().m.g0:
		// (A goroutine that may not be descheduled, as one that
		// sync.Pool pinned, can only give its thread away.)
		osyield()
	default:
		Gosched()
	}
}

// Values of more than one word.
//
// A slice, a string and an interface are several words, and a goroutine
// that loads one while another stores it may get the words of two values:
// a pointer with another array's length. So the stores of such a value in
// shared memory, and its loads from memory that may be shared, are made
// atomic with sequence locks: a store takes the locks of the blocks it
// writes, and a load checks that the lock of the block it reads from did
// not move and was not held meanwhile, and starts again if it did.
//
// The compiler does the loads inline (cmd/compile/internal/ssagen's
// shareLoad) with no test of the shared bit: memory that is not shared has
// no other writer, and its locks never move for it. For a store it calls
// sharestore, which takes the locks only if the memory is shared, and then
// shareunlock.
//
// A lock is found from the address of a value's first byte, and a store
// takes the lock of every block it touches, so a load of a value inside a
// larger one that is being stored sees the lock held.

const (
	shareBlockShift = 6       // a lock for every 64 bytes
	shareStripes    = 1 << 14 // both must match ssagen's shareSeqAddr
)

// shareSeq are the sequence locks: odd while a store holds one.
//
// Each has a cache line to itself, at the address of its block modulo the
// table's size: two goroutines that store in blocks next to each other
// (two Ps' halves of a sync.Pool) do not write the same line, and the
// compiler finds the lock of an address with one mask.
var shareSeq [shareStripes]struct {
	seq uint32
	_   [1<<shareBlockShift - 4]byte
}

func shareLockStripe(i uintptr) {
	l := &shareSeq[i].seq
	for spins := 0; ; spins++ {
		if v := atomic.Load(l); v&1 == 0 && atomic.Cas(l, v, v+1) {
			// The lock is seen held before any of the stores
			// that follow is seen, also where stores may be
			// reordered.
			publicationBarrier()
			return
		}
		shareYield(spins)
	}
}

// shareLoadBarrier orders the loads before it before those after it. It
// is an atomic operation on a word of its own, which is a barrier where
// one is needed (arm64 and the like) and nearly free where none is.
//
//go:nosplit
func shareLoadBarrier() {
	if goarch.IsAmd64|goarch.Is386 != 0 {
		return
	}
	var x uint32
	atomic.Xadd(&x, 1)
}

// shareStripeRange calls f for the locks of the blocks of [p, p+size), in
// the order in which every goroutine takes them: by index.
func shareStripeRange(p, size uintptr, f func(i uintptr)) {
	first, last := p>>shareBlockShift, (p+size-1)>>shareBlockShift
	if last-first >= shareStripes-1 {
		for i := uintptr(0); i < shareStripes; i++ {
			f(i)
		}
		return
	}
	lo, hi := first&(shareStripes-1), last&(shareStripes-1)
	if lo <= hi {
		for i := lo; i <= hi; i++ {
			f(i)
		}
		return
	}
	for i := uintptr(0); i <= hi; i++ {
		f(i)
	}
	for i := lo; i < shareStripes; i++ {
		f(i)
	}
}

func shareLockRange(p, size uintptr) {
	shareStripeRange(p, size, shareLockStripe)
}

func shareUnlockRange(p, size uintptr) {
	shareStripeRange(p, size, func(i uintptr) { atomic.Xadd(&shareSeq[i].seq, 1) })
}

// sharestore is called by compiled code before it stores size bytes that
// hold a value of more than one word at dst. If dst is shared it takes
// the locks and reports true, and the caller calls shareunlock after the
// store.
//
// The goroutine is not preempted while it holds the locks: one that
// waits for them may be unable to give its P away.
func sharestore(dst unsafe.Pointer, size uintptr) bool {
	if !isShared(dst) {
		return false
	}
	acquirem()
	shareLockRange(uintptr(dst), size)
	return true
}

func shareunlock(dst unsafe.Pointer, size uintptr) {
	shareUnlockRange(uintptr(dst), size)
	releasem(getg().m)
}

// shareRead copies the size bytes at src to dst, which only this goroutine
// can reach, as no store in progress left them.
func shareRead(dst, src unsafe.Pointer, size uintptr) {
	first, last := uintptr(src)>>shareBlockShift, (uintptr(src)+size-1)>>shareBlockShift
	if last-first >= 4 {
		// Many locks to watch: with them held there is no store in
		// progress.
		mp := acquirem()
		shareLockRange(uintptr(src), size)
		memmove(dst, src, size)
		shareUnlockRange(uintptr(src), size)
		releasem(mp)
		return
	}
	// As the inline load does, without taking the locks: a reader that
	// took them would send the other readers of the block here.
	var seq [4]uint32
	for spins := 0; ; spins++ {
		quiet := true
		for i := uintptr(0); i <= last-first; i++ {
			seq[i] = atomic.Load(&shareSeq[(first+i)&(shareStripes-1)].seq)
			quiet = quiet && seq[i]&1 == 0
		}
		if quiet {
			memmove(dst, src, size)
			// The loads of the value are done before the locks
			// are read again, also where loads may be reordered.
			shareLoadBarrier()
			for i := uintptr(0); i <= last-first; i++ {
				quiet = quiet && seq[i] == atomic.Load(&shareSeq[(first+i)&(shareStripes-1)].seq)
			}
			if quiet {
				return
			}
		}
		shareYield(spins)
	}
}

// The loads that compiled code calls when its inline load found a store
// in progress.

func shareloadslice(p *slice) (v slice) {
	shareRead(unsafe.Pointer(&v), unsafe.Pointer(p), unsafe.Sizeof(v))
	return
}

func shareloadstring(p *string) (v string) {
	shareRead(unsafe.Pointer(&v), unsafe.Pointer(p), unsafe.Sizeof(v))
	return
}

func shareloadiface(p *eface) (v eface) {
	shareRead(unsafe.Pointer(&v), unsafe.Pointer(p), unsafe.Sizeof(v))
	return
}

// shareloadmem is the load of a value that is not loaded in registers:
// size bytes from src to dst, which is in the stack.
func shareloadmem(dst, src unsafe.Pointer, size uintptr) {
	shareRead(dst, src, size)
}

// tearable reports whether a value of type t holds a slice, a string or
// an interface.
func tearable(t *_type) bool {
	if !t.Pointers() {
		return false
	}
	switch t.Kind() {
	case abi.String, abi.Slice, abi.Interface:
		return true
	case abi.Array:
		a := (*arraytype)(unsafe.Pointer(t))
		return a.Len > 0 && tearable(a.Elem)
	case abi.Struct:
		// Remember the answer for the structs: one word, the type
		// with the answer in its low bit.
		c := &tearableCache[(uintptr(unsafe.Pointer(t))>>4)%uintptr(len(tearableCache))]
		if v := atomic.Loaduintptr(c); v&^1 == uintptr(unsafe.Pointer(t)) {
			return v&1 != 0
		}
		st := (*structtype)(unsafe.Pointer(t))
		var is uintptr
		for i := range st.Fields {
			if tearable(st.Fields[i].Typ) {
				is = 1
				break
			}
		}
		atomic.Storeuintptr(c, uintptr(unsafe.Pointer(t))|is)
		return is != 0
	}
	return false
}

var tearableCache [256]uintptr

// shareMove copies n values of type typ from src to dst, as typedmemmove
// and typedslicecopy do, for memory that may be shared: the values of
// more than one word are read and written whole.
func shareMove(typ *_type, dst, src unsafe.Pointer, n uintptr) {
	size := n * typ.Size_
	sd, ss := isShared(dst), isShared(src)
	if size == 0 || dst == src || !sd && !ss || !tearable(typ) {
		shareMoveRaw(typ, dst, src, n)
		return
	}
	if sd && ss {
		// By way of a copy, so that the locks of one are not held
		// while waiting for the other's.
		tmp := mallocgc(size, typ, true)
		shareMoveLocked(typ, tmp, src, n, uintptr(src))
		shareMoveLocked(typ, dst, tmp, n, uintptr(dst))
		return
	}
	if ss {
		shareMoveLocked(typ, dst, src, n, uintptr(src))
	} else {
		shareMoveLocked(typ, dst, src, n, uintptr(dst))
	}
}

// shareMoveLocked copies with the locks of the n values at p held. The
// goroutine is not preempted meanwhile: see sharestore.
func shareMoveLocked(typ *_type, dst, src unsafe.Pointer, n, p uintptr) {
	mp := acquirem()
	shareLockRange(p, n*typ.Size_)
	shareMoveRaw(typ, dst, src, n)
	shareUnlockRange(p, n*typ.Size_)
	releasem(mp)
}

func shareMoveRaw(typ *_type, dst, src unsafe.Pointer, n uintptr) {
	size := n * typ.Size_
	if writeBarrier.enabled && typ.Pointers() {
		bulkBarrierPreWrite(uintptr(dst), uintptr(src), size-typ.Size_+typ.PtrBytes, typ)
	}
	memmove(dst, src, size)
}

// sharetypedmemmove is typedmemmove for compiled code that copies a value
// with values of more than one word between two places that are not in
// the stack.
func sharetypedmemmove(typ *_type, dst, src unsafe.Pointer) {
	if dst == src {
		return
	}
	publishStore(typ, dst, src)
	shareMove(typ, dst, src, 1)
}

// shareseqload loads a sequence lock, for compiled code on the targets
// where the compiler has no inline atomic load.
//
//go:nosplit
func shareseqload(seq *uint32) uint32 {
	return atomic.Load(seq)
}

// shareClear clears n bytes at ptr, which hold pointers, as
// memclrHasPointers does. In shared memory the values of more than one
// word are cleared whole.
//
//go:nosplit
func shareClear(ptr unsafe.Pointer, n uintptr) bool {
	if !sharingEnabled || n <= goarch.PtrSize || !isShared(ptr) {
		return false
	}
	shareClearLocked(ptr, n)
	return true
}

func shareClearLocked(ptr unsafe.Pointer, n uintptr) {
	mp := acquirem()
	shareLockRange(uintptr(ptr), n)
	bulkBarrierPreWrite(uintptr(ptr), 0, n, nil)
	memclrNoHeapPointers(ptr, n)
	shareUnlockRange(uintptr(ptr), n)
	releasem(mp)
}

// sharecheck is called by code compiled with -d=sharecheck before it
// stores or loads at p without a lock, because escape analysis found that
// no other goroutine can reach that memory. If one can, the analysis is
// wrong.
func sharecheck(p unsafe.Pointer) {
	if isShared(p) {
		s := spanOf(uintptr(p))
		if s == nil {
			// Not the heap's: a global the analysis took for a
			// local, or memory the runtime does not know.
			throw("sharecheck: memory found unshared at compile time is not in the heap")
		}
		if s.spanclass.noscan() {
			// No pointers: nothing there is locked, and small
			// objects without pointers share a block and its bit.
			return
		}
		throw("sharecheck: memory found unshared at compile time is shared")
	}
}

// Where an object is allocated (doc/gd/sharing.md).
//
// Whether another goroutine may come to reach an object is decided when
// it is allocated, from everything that is known then about where its
// address goes; what is not known counts as shared. An object that only
// the goroutine that allocates it reaches is local, and it is allocated
// in the local range of addresses (heapSplit), with the stacks. So the
// address of a word says whether another goroutine may read or write it.

// localHeapBase is the first address of the local range. Until there is
// one it is an address that nothing has, so that nothing is local. The
// compiler reads it for the tags it computes (escape's localTag).
var localHeapBase uintptr = ^uintptr(0) >> 1

// isLocal reports whether p is in the local range.
//
//go:nosplit
func isLocal(p unsafe.Pointer) bool {
	return uintptr(p)-localHeapBase < localHeapSize
}

// localType returns typ tagged for the allocation functions that take
// a type: the object is local.
func localType(typ *_type) *_type {
	return (*_type)(add(unsafe.Pointer(typ), 1))
}

// allocType returns the type that typ is, and whether typ was tagged as
// the type of a local object.
//
//go:nosplit
func allocType(typ *_type) (*_type, bool) {
	if uintptr(unsafe.Pointer(typ))&1 != 0 {
		return (*_type)(add(unsafe.Pointer(typ), ^uintptr(0))), true
	}
	return typ, false
}

// shareStats counts, with GODEBUG=sharestats=1, the objects allocated
// in each range, and the local ones that another goroutine came to
// reach all the same: each of those is a decision at an allocation
// that was wrong. With sharestats=2 the first ones are traced.
var shareStats struct {
	local, localBytes   atomic.Uint64
	shared, sharedBytes atomic.Uint64
	published           atomic.Uint64
}

func shareStatsAlloc(size uintptr, local bool) {
	if local {
		shareStats.local.Add(1)
		shareStats.localBytes.Add(int64(size))
	} else {
		shareStats.shared.Add(1)
		shareStats.sharedBytes.Add(int64(size))
	}
}

func localPublished(base uintptr) {
	if n := shareStats.published.Add(1); debug.sharestats >= 2 && n <= 20 {
		print("runtime: local object ", hex(base), " is published\n")
		if gp := getg(); gp == gp.m.curg {
			pc, sp := sys.GetCallerPC(), sys.GetCallerSP()
			systemstack(func() {
				traceback(pc, sp, 0, gp)
			})
		}
	}
	if debug.sharestats >= 3 {
		throw("local object published")
	}
}

func printShareStats() {
	if debug.sharestats == 0 {
		return
	}
	print("sharestats: local ", shareStats.local.Load(), " objects ", shareStats.localBytes.Load(), " bytes, shared ",
		shareStats.shared.Load(), " objects ", shareStats.sharedBytes.Load(), " bytes, local published ", shareStats.published.Load(), "\n")
}
