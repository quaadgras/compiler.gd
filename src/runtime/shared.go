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
//
// All of this is the sharing experiment's (GOEXPERIMENT=sharing,
// doc/gd/sharing.md). Without it sharingEnabled and localSpans are false,
// and every path here compiles away: nothing is marked, published,
// locked or allocated in spans of local objects.

package runtime

import (
	"internal/abi"
	"internal/goarch"
	"internal/goexperiment"
	"internal/runtime/atomic"
	"internal/runtime/gc"
	"internal/runtime/sys"
	"internal/stringslite"
	"unsafe"
)

// sharingEnabled is whether objects are marked shared at all: only with
// the sharing experiment, and not where goroutines run one at a time and
// are never preempted between two instructions (wasm), where no
// operation can race.
const sharingEnabled = goexperiment.Sharing && GOARCH != "wasm"

// tearEnabled is whether values of more than one word are loaded and
// stored whole at all: the interfaces always (see "Interfaces" below),
// the strings and the slices with sharing, in shared memory.
const tearEnabled = GOARCH != "wasm"

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
	// (Small enough to inline, so that it is nothing without sharing.)
	if !sharingEnabled || p == nil {
		return false
	}
	return isSharedObject(p)
}

//go:nosplit
func isSharedObject(p unsafe.Pointer) bool {
	if sharePages[uintptr(p)>>sharePageShift&uintptr(len(sharePages)-1)] == 0 {
		// Nothing shared was ever in a page like this one.
		return false
	}
	// The object that was found shared last, which stores in a pooled
	// object hit again and again: one that a goroutine still points to
	// is not freed, nor its slot used again, before the next sweep, and
	// a live object's shared bit is never cleared.
	pp := getg().m.p.ptr()
	if pp != nil && uintptr(p)-pp.sharedObj.base < pp.sharedObj.size && pp.sharedObj.gen == mheap_.sweepgen {
		return true
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
	if shared && pp != nil {
		pp.sharedObj.base = s.base() + i*s.elemsize
		pp.sharedObj.size = s.elemsize
		pp.sharedObj.gen = mheap_.sweepgen
	}
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
			otherPagesAdd(r[0], (r[1]-1)>>gc.PageShift-r[0]>>gc.PageShift+1, 1)
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
//
// An object without pointers is not marked. Nothing in it is locked: what
// is stored whole is a string, a slice or an interface, which have
// pointers, and so do the maps, the channels and the atomic pointers that
// ask whether they are shared. It leads nowhere, and the small ones share
// a block, and would share its bit. So a buffer of bytes costs nothing
// here, and leaves its page's byte clear for isShared.
func markShared(p uintptr) (base uintptr, s *mspan) {
	s = spanOf(p)
	if s == nil || s.state.get() != mSpanInUse || p < s.base() || p >= s.limit || s.isUserArenaChunk || s.spanclass.noscan() {
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
	if fresh && debug.sharestats != 0 {
		shareStats.marked.Add(1)
		shareStats.markedBytes.Add(int64(s.elemsize))
	}
	if fresh && debug.sharesites != 0 {
		shareSiteMarked(s.base() + i*s.elemsize)
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
	// (Small enough to inline, so that it is nothing without sharing.)
	if !sharingEnabled || p == nil {
		return
	}
	publishObjectOf(p)
}

func publishObjectOf(p unsafe.Pointer) {
	base, s := markShared(uintptr(p))
	if base == 0 {
		return
	}
	publishObject(base, s)
	KeepAlive(p)
}

// publishContents shares what the object p points to holds, and not the
// object: it is handed to another goroutine, which is then the only one
// that reaches it (chansendmove1). If it is shared already, so is what it
// holds.
func publishContents(p unsafe.Pointer) {
	if !sharingEnabled || p == nil || isShared(p) {
		return
	}
	publishContentsOf(p)
}

// publishContentsOf is publishContents for an object that is not shared.
func publishContentsOf(p unsafe.Pointer) {
	s := spanOfHeap(uintptr(p))
	if s == nil || s.isUserArenaChunk || s.spanclass.noscan() {
		return
	}
	base := s.base() + s.objIndex(uintptr(p))*s.elemsize
	limit := base + s.elemsize
	// The object is the caller's to read until it is handed over, and
	// p keeps it while what it points to is walked.
	tp := s.typePointersOfUnchecked(base)
	for {
		var addr uintptr
		if tp, addr = tp.next(limit); addr == 0 {
			break
		}
		// A pointer into the object itself (a field that points to
		// another, as fmt's printer has) shares nothing.
		q := *(*uintptr)(unsafe.Pointer(addr))
		if q == 0 || q-base < s.elemsize {
			continue
		}
		// As a rule it is shared already, by the last time the
		// object was handed over, or has no pointers.
		if b, qs := markShared(q); b != 0 {
			publishObject(b, qs)
		}
	}
	KeepAlive(p)
}

// Handing an object to a sync.Pool.
//
// An object put in a pool was shared from then on, and whoever got it
// from the pool took a lock for every store of a slice, a string or an
// interface in it, though it had the object to itself. Where the compiler
// shows that the goroutine that puts an object has no way left to it
// (cmd/compile/internal/escape, move.go), the pool hands it over: it is
// not shared, as a pointer sent on a channel is not (chansendmove1).
//
// Compiled code says so with poolhandover(p) right before it calls
// (*sync.Pool).Put, which asks poolOwned. The proof takes what Get
// returned for an object that nobody else has, or that is shared, which
// is what was put (handed over, or shared by poolOwned) and what New
// returns if New only allocates (poolNewFresh); what another New
// returns, Get shares.

// poolhandover is called by compiled code before a call of
// (*sync.Pool).Put that hands over the object p points to.
//
//go:nosplit
func poolhandover(p unsafe.Pointer) {
	getg().ownedPut = p
}

// publishkept is called by compiled code before a call of a function
// that is only known when the program runs, whose escape mask says that
// it keeps the argument p: an object that its goroutine would hand over
// later, because nothing else that it does keeps it, is shared instead.
func publishkept(p unsafe.Pointer) {
	if keptSeen.Load() == 0 {
		keptSeen.Store(1)
	}
	publish(p)
}

// keptSeen is set once publishkept has shared an object. Until then, an
// object that is handed to a pool which only holds objects that were
// handed over, or that its New allocated, is not shared, and poolOwned
// need not look.
var keptSeen atomic.Uint32

// poolOwned reports whether the object that x, which sync.Pool.Put was
// given, points to is handed over; it then shares what the object points to.
//
// An object that would be handed over but is shared was shared since Get
// returned it (publishkept: a function that is only known when the
// program runs kept it). In a pool whose New returns objects that nobody
// else has (fresh), it is dropped: the pool would give it out again and
// again, and whoever got it would take the locks of shared memory, where
// a new one costs an allocation, once.
//
// Nothing has shared it if no call has found a function that keeps its
// argument (keptSeen) and the pool has no shared objects: its New
// allocates (fresh) and nothing was put that was not handed over (mixed
// is false). To take a shared object for one that is not would only
// keep it in the pool.
//
//go:linkname poolOwned sync.runtime_poolOwned
func poolOwned(x any, fresh, mixed bool) (handed, drop bool) {
	p := efaceOf(&x).data
	gp := getg()
	owned := gp.ownedPut
	gp.ownedPut = nil
	if !sharingEnabled {
		return false, false
	}
	if owned == nil || owned != p {
		// What a pool holds is the next caller of Get's alone, or
		// shared: also in a pool that only this goroutine has,
		// whose memory the store would not share it for.
		publish(p)
		return false, false
	}
	if (!fresh || mixed || keptSeen.Load() != 0) && isShared(p) {
		// (What it points to is shared with it.)
		return !fresh, fresh
	}
	publishContentsOf(p)
	return true, false
}

// poolStoreOwned stores x, which is handed over, in a pool: without
// sharing it, as compiled code would.
//
//go:linkname poolStoreOwned sync.runtime_poolStoreOwned
func poolStoreOwned(dst *any, x any) {
	*dst = x
}

// poolNewFresh reports whether what the function f returned to
// sync.Pool.Get is an object that only its caller has. If not, it is
// shared here.
//
//go:linkname poolNewFresh sync.runtime_poolNew
func poolNewFresh(f func() any, x any) bool {
	if !sharingEnabled {
		return false
	}
	fv := *(**funcval)(unsafe.Pointer(&f))
	if fv != nil {
		if fi := findfunc(fv.fn); fi.valid() && fi.flag&abi.FuncFlagFreshResult != 0 {
			return true
		}
	}
	e := efaceOf(&x)
	publish(e.data)
	return false
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
	if val != nil && isShared(dst) {
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

//go:linkname maps_isShared internal/runtime/maps.runtimeIsShared
//go:nosplit
func maps_isShared(p unsafe.Pointer) bool {
	return isShared(p)
}

// maps_shareAllocs sets whether what the goroutine allocates for a map is
// shared: it is while it writes a shared map.
//
//go:linkname maps_shareAllocs internal/runtime/maps.runtimeShareAllocs
func maps_shareAllocs(on bool) {
	getg().shareAllocs = on
}

//go:linkname maps_shareAlloc internal/runtime/maps.runtimeShareAlloc
func maps_shareAlloc(p unsafe.Pointer) {
	if sharingEnabled && getg().shareAllocs {
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

// storesOrdered is whether the processor keeps stores in order, so that
// what a goroutine stored in a new object is seen before a pointer to it
// that it stores after (doc/gd/publication-point.md). publicationBarrier
// is a call there, which code on a fast path guards with this constant.
const storesOrdered = goarch.IsAmd64|goarch.Is386|goarch.IsS390x|goarch.IsWasm != 0

// reflect_publicationBarrier orders the fill of a function value that
// reflect made before the racy store that may publish it.
//
//go:linkname reflect_publicationBarrier reflect.publicationBarrier
func reflect_publicationBarrier() {
	if !storesOrdered {
		publicationBarrier()
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
	// (A value of a few words is in one block, or two: their locks
	// directly, in the order of shareStripeRange.)
	first, last := p>>shareBlockShift, (p+size-1)>>shareBlockShift
	switch last - first {
	case 0:
		shareLockStripe(first & (shareStripes - 1))
	case 1:
		a, b := first&(shareStripes-1), last&(shareStripes-1)
		if b < a {
			a, b = b, a
		}
		shareLockStripe(a)
		shareLockStripe(b)
	default:
		shareStripeRange(p, size, shareLockStripe)
	}
}

func shareUnlockRange(p, size uintptr) {
	first, last := p>>shareBlockShift, (p+size-1)>>shareBlockShift
	switch last - first {
	case 0:
		atomic.Xadd(&shareSeq[first&(shareStripes-1)].seq, 1)
	case 1:
		atomic.Xadd(&shareSeq[first&(shareStripes-1)].seq, 1)
		atomic.Xadd(&shareSeq[last&(shareStripes-1)].seq, 1)
	default:
		shareStripeRange(p, size, func(i uintptr) { atomic.Xadd(&shareSeq[i].seq, 1) })
	}
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

// sharestorestring, sharestoreslice and sharestoreiface are called by
// compiled code that stores a whole string, slice or interface at dst, in
// a page where something was shared once. If dst is shared they publish
// what v points to, store v under the locks and report true: the store is
// here and not in every function that has one. If not they report false,
// and the caller stores, as it does in memory that is not shared.
func sharestorestring(dst *string, v string) bool {
	if !isShared(unsafe.Pointer(dst)) {
		return false
	}
	publishNew(unsafe.Pointer(dst), *(*unsafe.Pointer)(unsafe.Pointer(&v)))
	mp := acquirem()
	shareLockRange(uintptr(unsafe.Pointer(dst)), unsafe.Sizeof(v))
	*dst = v
	shareUnlockRange(uintptr(unsafe.Pointer(dst)), unsafe.Sizeof(v))
	releasem(mp)
	return true
}

// (A slice of any type: []byte has its words.)
func sharestoreslice(dst *[]byte, v []byte) bool {
	if !isShared(unsafe.Pointer(dst)) {
		return false
	}
	publishNew(unsafe.Pointer(dst), unsafe.Pointer(unsafe.SliceData(v)))
	mp := acquirem()
	shareLockRange(uintptr(unsafe.Pointer(dst)), unsafe.Sizeof(v))
	*dst = v
	shareUnlockRange(uintptr(unsafe.Pointer(dst)), unsafe.Sizeof(v))
	releasem(mp)
	return true
}

// publishNew publishes p, which is to be stored in the first word of the
// shared memory at dst, unless that word holds p already: then p is
// shared, since it was published when it was stored there, or when what
// holds it was (b = append(b, x) with room left).
func publishNew(dst, p unsafe.Pointer) {
	if atomic.Loadp(dst) == p {
		return
	}
	publish(p)
}

func sharestoreiface(dst *eface, v eface) bool {
	if !isShared(unsafe.Pointer(dst)) {
		return false
	}
	publish(unsafe.Pointer(v._type))
	publish(v.data)
	mp := acquirem()
	shareLockRange(uintptr(unsafe.Pointer(dst)), unsafe.Sizeof(v))
	*dst = v
	shareUnlockRange(uintptr(unsafe.Pointer(dst)), unsafe.Sizeof(v))
	releasem(mp)
	return true
}

// shareStripes2 calls f for the locks of the blocks of [a, a+asize) and
// [b, b+bsize), each once, in the order in which every goroutine takes
// them: by index.
func shareStripes2(a, asize, b, bsize uintptr, f func(i uintptr)) {
	var idx [16]uintptr
	n := 0
	for _, r := range [2][2]uintptr{{a, asize}, {b, bsize}} {
		first, last := r[0]>>shareBlockShift, (r[0]+r[1]-1)>>shareBlockShift
		if last-first >= uintptr(len(idx)/2) {
			// Too many to sort here: all of them.
			for i := uintptr(0); i < shareStripes; i++ {
				f(i)
			}
			return
		}
		for i := first; i <= last; i++ {
			// Insert in order, unless it is there.
			x := i & (shareStripes - 1)
			j := n
			for j > 0 && idx[j-1] > x {
				j--
			}
			if j > 0 && idx[j-1] == x {
				continue
			}
			copy(idx[j+1:n+1], idx[j:n])
			idx[j] = x
			n++
		}
	}
	for _, i := range idx[:n] {
		f(i)
	}
}

// sharestore2 is sharestore for the stores of one statement in two
// places, asize bytes at a and bsize bytes at b (a slice and the elements
// appended to it): between two synchronisations no goroutine can tell
// stores made under one lock from stores made each under its own. It
// takes the locks of both if either is shared, and the caller then calls
// shareunlock2 after the stores.
func sharestore2(a unsafe.Pointer, asize uintptr, b unsafe.Pointer, bsize uintptr) bool {
	if !isShared(a) && !isShared(b) {
		return false
	}
	acquirem()
	shareStripes2(uintptr(a), asize, uintptr(b), bsize, shareLockStripe)
	return true
}

func shareunlock2(a unsafe.Pointer, asize uintptr, b unsafe.Pointer, bsize uintptr) {
	shareStripes2(uintptr(a), asize, uintptr(b), bsize, func(i uintptr) { atomic.Xadd(&shareSeq[i].seq, 1) })
	releasem(getg().m)
}

// shareRead copies the size bytes at src to dst, which only this goroutine
// can reach, as no store in progress left them: values of type typ, or
// nil if they hold no string (moveWhole).
func shareRead(typ *_type, dst, src unsafe.Pointer, size uintptr) {
	first, last := uintptr(src)>>shareBlockShift, (uintptr(src)+size-1)>>shareBlockShift
	if last-first >= 4 {
		// Many locks to watch: with them held there is no store in
		// progress.
		mp := acquirem()
		shareLockRange(uintptr(src), size)
		moveWhole(typ, dst, src, size)
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
			moveWhole(typ, dst, src, size)
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

func shareloadslice(p *[]byte) (v []byte) {
	shareRead(nil, unsafe.Pointer(&v), unsafe.Pointer(p), unsafe.Sizeof(v))
	return
}

func shareloadstring(p *string) (v string) {
	shareRead(nil, unsafe.Pointer(&v), unsafe.Pointer(p), unsafe.Sizeof(v))
	return
}

func shareloadiface(p *eface) (v eface) {
	shareRead(nil, unsafe.Pointer(&v), unsafe.Pointer(p), unsafe.Sizeof(v))
	return
}

// shareloadmem is the load of a value that is not loaded in registers:
// size bytes from src to dst, which is in the stack.
func shareloadmem(dst, src unsafe.Pointer, size uintptr) {
	shareRead(nil, dst, src, size)
}

// What a type holds that is loaded and stored whole (tearKind).
const (
	tearAny    = 1 << 0 // a slice, a string or an interface
	tearIface  = 1 << 1 // an interface
	tearString = 1 << 2 // a string, or a slice where it is two words (16 bytes, aligned to 16: abi.SlicePacked)
)

// tearKind says whether a value of type t holds a slice, a string or an
// interface, and whether it holds an interface.
func tearKind(t *_type) uintptr {
	if !t.Pointers() {
		return 0
	}
	switch t.Kind() {
	case abi.String:
		return tearAny | tearString
	case abi.Slice:
		if abi.SlicePacked {
			// Copied and cleared 16 bytes at a time, or under the
			// locks, as a string is (doc/gd/slice16.md).
			return tearAny | tearString
		}
		return tearAny
	case abi.Interface:
		return tearAny | tearIface
	case abi.Array:
		a := (*arraytype)(unsafe.Pointer(t))
		if a.Len == 0 {
			return 0
		}
		return tearKind(a.Elem)
	case abi.Struct:
		// Remember the answer for the structs: one word, the type
		// with the answer in its low bits.
		c := &tearableCache[(uintptr(unsafe.Pointer(t))>>4)%uintptr(len(tearableCache))]
		if v := atomic.Loaduintptr(c); v&^7 == uintptr(unsafe.Pointer(t)) {
			return v & 7
		}
		st := (*structtype)(unsafe.Pointer(t))
		var k uintptr
		if isWholeStruct(t) {
			k = tearAny | tearIface
		} else {
			for i := range st.Fields {
				k |= tearKind(st.Fields[i].Typ)
			}
		}
		atomic.Storeuintptr(c, uintptr(unsafe.Pointer(t))|k)
		return k
	}
	return 0
}

var tearableCache [256]uintptr

// isWholeStruct reports whether the struct type t is one whose words mean
// nothing apart, loaded and stored whole as an interface is, under the
// same locks: reflect.Value, whose type, pointer, inline payload and flags
// must be those of one value. As cmd/compile/internal/types's
// IsWholeStruct.
func isWholeStruct(t *_type) bool {
	// By its fields, not its name: generic code has its shape, and a type
	// defined from it, as in type V reflect.Value, has another name.
	st := (*structtype)(unsafe.Pointer(t))
	return len(st.Fields) == 4 && st.Fields[0].Name.Name() == "typ_" && st.Fields[1].Name.Name() == "ptr" && st.PkgPath.Name() == "reflect"
}

// tearable reports whether a value of type t holds a slice, a string or
// an interface.
func tearable(t *_type) bool {
	return tearKind(t) != 0
}

// Interfaces.
//
// An interface is four words, and the payload of the last two is what the
// first says (doc/gd/fat-iface.md): one with the type of a store and the
// payload of another is not memory safe. So interfaces are loaded and
// stored whole wherever they are but in the stack, whether or not the
// memory is shared, and whatever the compiler knows of it: by default it
// knows nothing (without GOEXPERIMENT=sharing), and it guards the loads and
// stores of interfaces alone, the slices and the strings not. A store of
// an interface takes the lock of the block where it starts, inline
// (ssagen's shareStoreIface) or here if another store holds it
// (ifacestore); a load checks that lock. A value that holds interfaces is
// stored by compiled code one interface at a time, and copied or cleared
// by the runtime a few values at a time under the locks of all their
// blocks (shareMove, shareClearTyped, growslice), decided by type.
//
// No goroutine stops while it holds a lock: compiled code holds it for a
// few instructions where it cannot be preempted, and the runtime with
// acquirem. So one that waits for a lock may spin.

// onCurStack reports whether p points into the stack of the goroutine.
// Nothing else reads or writes there while it runs.
//
//go:nosplit
func onCurStack(p unsafe.Pointer) bool {
	gp := getg()
	return uintptr(p)-gp.stack.lo < gp.stack.hi-gp.stack.lo
}

// holdsIface reports whether values of type t that are not in the stack
// are copied whole, for the interfaces or, with 8-byte words, the
// strings they hold (shared_string.go).
//
//go:nosplit
func holdsIface(t *_type) bool {
	k := tearKind(t)
	return tearEnabled && k&tearIface != 0 || stringsWhole && k&tearString != 0
}

// atomicOnly reports whether values of type t are copied whole without
// locks: they hold strings, but no interface, and a 16-byte access is
// atomic (moveWhole and clearWhole copy and clear them).
//
//go:nosplit
func atomicOnly(t *_type) bool {
	k := tearKind(t)
	return stringsWhole && stringAtomic && k&tearString != 0 && k&tearIface == 0
}

// ifacestore is called by compiled code to store the interface v at dst,
// which may not be in the stack, when another store holds the lock of
// dst's block (or on the targets where it takes no lock inline): it waits
// for it, and stores under it.
func ifacestore(dst *eface, v eface) {
	mp := acquirem()
	// (The stack may move while this waits: the index stays.)
	i := uintptr(unsafe.Pointer(dst)) >> shareBlockShift & (shareStripes - 1)
	shareLockStripe(i)
	*dst = v
	atomic.Xadd(&shareSeq[i].seq, 1)
	releasem(mp)
}

// ifaceCopy returns p, or a copy of the value of type t at p if the value
// holds interfaces and is not in the stack. The runtime reads what reflect
// points it to word by word (a value sent, a map key), where compiled
// code passes a temporary of its stack.
func ifaceCopy(t *_type, p unsafe.Pointer) unsafe.Pointer {
	if p == nil || !holdsIface(t) || onCurStack(p) {
		return p
	}
	tmp := newobject(t)
	shareMove(t, tmp, p, 1)
	return tmp
}

// shareChunk is the most bytes that are copied or cleared under one set
// of locks: values are copied a few at a time, and a value larger than
// that part by part, each whole. 192 bytes are at most four blocks, which
// shareRead checks without taking their locks.
const shareChunk = 192

// shareSides reports whether a copy of values of type typ from src to dst
// takes the locks of dst and checks those of src.
func shareSides(typ *_type, dst, src unsafe.Pointer) (sd, ss bool) {
	switch k := tearKind(typ); {
	case k&tearIface != 0 && tearEnabled, k&tearString != 0 && stringsLocked():
		return !onCurStack(dst), !onCurStack(src)
	case k&tearString != 0 && stringsWhole:
		// Strings alone, each copied whole (moveWhole): no locks.
		return false, false
	case k != 0:
		return isShared(dst), isShared(src)
	}
	return false, false
}

// shareMove copies n values of type typ from src to dst, as typedmemmove
// and typedslicecopy do, for memory that may be shared: the values of
// more than one word are read and written whole.
func shareMove(typ *_type, dst, src unsafe.Pointer, n uintptr) {
	if n*typ.Size_ == 0 || dst == src {
		return
	}
	sd, ss := shareSides(typ, dst, src)
	if !sd && !ss {
		shareMoveRaw(typ, dst, src, n)
	} else {
		shareMoveGuarded(typ, dst, src, n, sd, ss)
	}
}

// shareMoveGuarded is shareMove where the locks of dst are taken (sd), or
// those of src checked (ss), or both. It copies backwards where dst is
// after src in the same memory, as memmove does.
func shareMoveGuarded(typ *_type, dst, src unsafe.Pointer, n uintptr, sd, ss bool) {
	es := typ.Size_
	back := uintptr(dst)-uintptr(src) < n*es
	if es > shareChunk && (typ.Kind() == abi.Struct || typ.Kind() == abi.Array) {
		for j := uintptr(0); j < n; j++ {
			i := j
			if back {
				i = n - 1 - j
			}
			shareMoveParts(typ, add(dst, i*es), add(src, i*es), sd, ss)
		}
		return
	}
	per := max(1, shareChunk/es)
	for done := uintptr(0); done < n; {
		k := min(per, n-done)
		i := done
		if back {
			i = n - done - k
		}
		shareMoveLocked(typ, add(dst, i*es), add(src, i*es), k, sd, ss)
		done += k
	}
}

// shareMoveParts copies one value of type typ, a struct or an array, field
// by field or element by element.
func shareMoveParts(typ *_type, dst, src unsafe.Pointer, sd, ss bool) {
	if typ.Kind() == abi.Array {
		a := (*arraytype)(unsafe.Pointer(typ))
		shareMovePart(a.Elem, dst, src, a.Len, sd, ss)
		return
	}
	st := (*structtype)(unsafe.Pointer(typ))
	off := uintptr(0)
	for i := range st.Fields {
		f := &st.Fields[i]
		if f.Offset > off {
			// (The padding.)
			memmove(add(dst, off), add(src, off), f.Offset-off)
		}
		shareMovePart(f.Typ, add(dst, f.Offset), add(src, f.Offset), 1, sd, ss)
		off = f.Offset + f.Typ.Size_
	}
	if typ.Size_ > off {
		memmove(add(dst, off), add(src, off), typ.Size_-off)
	}
}

func shareMovePart(typ *_type, dst, src unsafe.Pointer, n uintptr, sd, ss bool) {
	if n*typ.Size_ == 0 {
		return
	}
	if !tearable(typ) {
		shareMoveRaw(typ, dst, src, n)
		return
	}
	shareMoveGuarded(typ, dst, src, n, sd, ss)
}

// shareMoveLocked copies n values, at most shareChunk bytes or one value:
// with the locks of dst held if sd, and those of src too if ss; or, if
// only ss, as shareRead does. The goroutine is not preempted while it
// holds locks: see sharestore.
func shareMoveLocked(typ *_type, dst, src unsafe.Pointer, n uintptr, sd, ss bool) {
	size := n * typ.Size_
	if !sd {
		// No other goroutine reads dst while this one writes it: a
		// copy of src that no store in progress left is enough. (If
		// a store makes it copy again, the store's write barrier
		// shaded what it stored.)
		if writeBarrier.enabled && typ.Pointers() {
			bulkBarrierPreWrite(uintptr(dst), uintptr(src), size-typ.Size_+typ.PtrBytes, typ)
		}
		shareRead(typ, dst, src, size)
		return
	}
	mp := acquirem()
	if ss {
		shareStripes2(uintptr(dst), size, uintptr(src), size, shareLockStripe)
	} else {
		shareLockRange(uintptr(dst), size)
	}
	shareMoveRaw(typ, dst, src, n)
	if ss {
		shareStripes2(uintptr(dst), size, uintptr(src), size, shareUnlockStripe)
	} else {
		shareUnlockRange(uintptr(dst), size)
	}
	releasem(mp)
}

func shareUnlockStripe(i uintptr) {
	atomic.Xadd(&shareSeq[i].seq, 1)
}

func shareMoveRaw(typ *_type, dst, src unsafe.Pointer, n uintptr) {
	size := n * typ.Size_
	if writeBarrier.enabled && typ.Pointers() {
		bulkBarrierPreWrite(uintptr(dst), uintptr(src), size-typ.Size_+typ.PtrBytes, typ)
	}
	moveWhole(typ, dst, src, size)
}

// shareCopyFrom copies n values of type typ from src to dst, which no
// other goroutine can reach yet and whose write barriers are done (the
// new array of growslice): as shareRead does, a few values at a time.
func shareCopyFrom(typ *_type, dst, src unsafe.Pointer, n uintptr) {
	if atomicOnly(typ) {
		moveWhole(typ, dst, src, n*typ.Size_)
		return
	}
	es := typ.Size_
	per := max(1, shareChunk/es)
	for i := uintptr(0); i < n; i += per {
		k := min(per, n-i)
		shareRead(typ, add(dst, i*es), add(src, i*es), k*es)
	}
}

// shareClearTyped clears n values of type typ at ptr, as typedmemclr does,
// if they hold interfaces and ptr is not in the stack: each whole. It
// reports whether it did.
func shareClearTyped(typ *_type, ptr unsafe.Pointer, n uintptr) bool {
	if n*typ.Size_ == 0 || !holdsIface(typ) || onCurStack(ptr) {
		return false
	}
	if atomicOnly(typ) {
		bulkBarrierPreWrite(uintptr(ptr), 0, n*typ.Size_, nil)
		clearWhole(typ, ptr, n*typ.Size_)
		return true
	}
	shareClearGuarded(typ, ptr, n)
	return true
}

func shareClearGuarded(typ *_type, ptr unsafe.Pointer, n uintptr) {
	es := typ.Size_
	if es > shareChunk && (typ.Kind() == abi.Struct || typ.Kind() == abi.Array) {
		for i := uintptr(0); i < n; i++ {
			shareClearParts(typ, add(ptr, i*es))
		}
		return
	}
	per := max(1, shareChunk/es)
	for i := uintptr(0); i < n; i += per {
		k := min(per, n-i)
		shareClearLockedTyped(typ, add(ptr, i*es), k*es)
	}
}

func shareClearParts(typ *_type, ptr unsafe.Pointer) {
	if typ.Kind() == abi.Array {
		a := (*arraytype)(unsafe.Pointer(typ))
		shareClearPart(a.Elem, ptr, a.Len)
		return
	}
	st := (*structtype)(unsafe.Pointer(typ))
	off := uintptr(0)
	for i := range st.Fields {
		f := &st.Fields[i]
		if f.Offset > off {
			memclrNoHeapPointers(add(ptr, off), f.Offset-off)
		}
		shareClearPart(f.Typ, add(ptr, f.Offset), 1)
		off = f.Offset + f.Typ.Size_
	}
	if typ.Size_ > off {
		memclrNoHeapPointers(add(ptr, off), typ.Size_-off)
	}
}

func shareClearPart(typ *_type, ptr unsafe.Pointer, n uintptr) {
	size := n * typ.Size_
	if size == 0 {
		return
	}
	if !tearable(typ) {
		if typ.Pointers() {
			bulkBarrierPreWrite(uintptr(ptr), 0, size, nil)
		}
		memclrNoHeapPointers(ptr, size)
		return
	}
	shareClearGuarded(typ, ptr, n)
}

// typedarrayclear is called by compiled code to clear n values of type
// typ, which hold interfaces, at ptr (clear(s), and the loops that do the
// same): memclrHasPointers, each value whole.
func typedarrayclear(typ *_type, ptr unsafe.Pointer, n uintptr) {
	if shareClearTyped(typ, ptr, n) {
		return
	}
	memclrHasPointers(ptr, n*typ.Size_)
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
	shareClearLockedTyped(nil, ptr, n)
}

// shareClearLockedTyped is shareClearLocked for values of type typ
// (clearWhole).
func shareClearLockedTyped(typ *_type, ptr unsafe.Pointer, n uintptr) {
	mp := acquirem()
	shareLockRange(uintptr(ptr), n)
	bulkBarrierPreWrite(uintptr(ptr), 0, n, nil)
	clearWhole(typ, ptr, n)
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
// in spans of its own (localSpans). So the page of a word says whether
// another goroutine may read or write it.

// otherPages has, for each page of memory, found from the page's address
// modulo the table's size, the number of pages like it that hold
// something else than local objects: the pages of every span of the heap
// that is not of a class of local objects (the stacks among them), and of
// the globals. A pointer into a page whose number is zero points to a
// local object, since every object that is not local is in a page that is
// counted while it lives; the reverse does not hold, where two pages have
// the same place in the table. The compiler reads the table for the tags
// it computes (escape's localTest): what is stored in a local object is
// local, and what is stored in a variable of a caller's stack is what
// that caller says.
//
// The numbers change when a span is made and when it is freed, before
// anything is allocated in it and after everything in it is dead.
//
// The first page counts from the start (mallocinit): nil is not a local
// object, and what is appended to a nil slice is not known to stay.
var otherPages [otherPagesLen]uint32

// otherPagesAdd adds delta to the numbers of the npages pages from base.
func otherPagesAdd(base, npages uintptr, delta int32) {
	if !localSpans {
		return
	}
	for i := uintptr(0); i < npages; i++ {
		atomic.Xadd(&otherPages[(base>>gc.PageShift+i)%otherPagesMod], delta)
	}
}

// isLocalObject reports whether p is known to point to a local object,
// as compiled code finds it.
//
//go:nosplit
func isLocalObject(p unsafe.Pointer) bool {
	return localSpans && atomic.Load(&otherPages[uintptr(p)>>gc.PageShift%otherPagesMod]) == 0
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
	if goexperiment.Sharing && uintptr(unsafe.Pointer(typ))&1 != 0 {
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
	// Of the shared ones, those without pointers, which are never
	// marked, and the ones that were marked: stored where another
	// goroutine reaches. The rest another goroutine never came to.
	noscan, noscanBytes atomic.Uint64
	marked, markedBytes atomic.Uint64
}

func shareStatsAlloc(size uintptr, local, noscan bool) {
	if !local && noscan {
		shareStats.noscan.Add(1)
		shareStats.noscanBytes.Add(int64(size))
	}
	if local {
		shareStats.local.Add(1)
		shareStats.localBytes.Add(int64(size))
	} else {
		shareStats.shared.Add(1)
		shareStats.sharedBytes.Add(int64(size))
	}
}

// shareSites remembers, with sharestats=2, where the local objects were
// allocated, by address (the last one of those with the same place in the
// table), for localPublished to say.
var shareSites struct {
	lock mutex
	tab  [1 << 16]struct {
		addr uintptr
		pcs  [5]uintptr
	}
}

func shareStatsSite(x unsafe.Pointer) {
	var pcs [5]uintptr
	callers(3, pcs[:])
	lock(&shareSites.lock)
	e := &shareSites.tab[uintptr(x)>>3%uintptr(len(shareSites.tab))]
	e.addr = uintptr(x)
	e.pcs = pcs
	unlock(&shareSites.lock)
}

func printShareSite(base uintptr) {
	lock(&shareSites.lock)
	e := shareSites.tab[base>>3%uintptr(len(shareSites.tab))]
	unlock(&shareSites.lock)
	if e.addr != base {
		print("\t(allocation not recorded)\n")
		return
	}
	print("\tallocated by:\n")
	for _, pc := range e.pcs {
		if f := findfunc(pc); f.valid() {
			file, line := funcline(f, pc-1)
			print("\t\t", funcname(f), " ", file, ":", line, "\n")
		}
	}
}

func localPublished(base uintptr) {
	if n := shareStats.published.Add(1); debug.sharestats >= 2 && n <= 20 {
		print("runtime: local object ", hex(base), " is published\n")
		printShareSite(base)
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
	if !goexperiment.Sharing {
		return
	}
	printShareSites()
	if debug.sharestats == 0 {
		return
	}
	print("sharestats: local ", shareStats.local.Load(), " objects ", shareStats.localBytes.Load(), " bytes, shared ",
		shareStats.shared.Load(), " objects ", shareStats.sharedBytes.Load(), " bytes, local published ", shareStats.published.Load(),
		"; of the shared, without pointers ", shareStats.noscan.Load(), " objects ", shareStats.noscanBytes.Load(), " bytes, marked ",
		shareStats.marked.Load(), " objects ", shareStats.markedBytes.Load(), " bytes\n")
}

// The bits of a call.
//
// A function cannot tell, from an address, whether what it stores in a
// variable of its caller's stack stays with the goroutine, nor whether
// what it returns does: the caller knows. So a caller says it, in
// g.localBits, right before the call: bit 0 if nothing that is reached
// from the results will be shared, bit k+1 if nothing that is stored
// through argument k (the receiver first) will be. The bits are in the
// high localBitsCount bits of the word, and the low ones are the address
// of the function that is called: a function that uses the bits takes
// the word when it starts, leaves zero, and has the bits if the address
// is its own. So a word that a function did not take says nothing to the
// next one, and no bit is one that the caller did not set for this call:
// bits that are lost only make shared what could have been local.
const (
	localBitsCount = 16
	localBitsShift = 8*goarch.PtrSize - localBitsCount
)

// The compiler knows where g.localBits is.
const _ = uint(unsafe.Offsetof(g{}.localBits) - 7*goarch.PtrSize)
const _ = uint(7*goarch.PtrSize - unsafe.Offsetof(g{}.localBits))

// escMaskAll is an escape mask that says that the function keeps and
// shares every argument: what the tag of an object that is allocated
// for an argument reads if there is no function (cmd/compile's
// escape.callArgTag).
var escMaskAll uint64 = abi.ITabEscAll

// Where the objects that are not local come from.
//
// With GODEBUG=sharesites=1, each object with pointers that is not
// allocated as a local one is counted for the place that allocates it:
// the first function on the stack that is not the runtime's or
// reflect's, once the packages are initialized. When an
// object is first marked as shared, which is when it is stored where
// another goroutine reaches, that is counted for its place too. At exit
// the places are printed that allocated the most objects which were
// never marked: those another goroutine never came to, so that each was
// a local object if the compiler had known. (Some of them outlived
// their goroutine all the same: handed over on a channel or to a pool.)
//
// The table of objects is by address and forgets: an object whose entry
// another has taken is counted as unknown when it is marked.
type shareSite struct {
	pc                 uintptr
	stack              [5]uintptr
	allocs, bytes      uint64
	marked, markedSize uint64
	printed            bool
}

type shareSiteObj struct {
	addr uintptr
	site uint32
	size uint32
}

const shareSiteObjs = 1 << 22

var shareSiteTab struct {
	lock    mutex
	sites   [1 << 15]shareSite
	nsites  int
	full    uint64
	unknown uint64
	objs    *[shareSiteObjs]shareSiteObj
}

// shareSitesMain is set when the packages are initialized: what they
// allocate before is for the variables of the program.
var shareSitesMain bool

// hasSuffixDir reports whether the directory of file ends in dir.
func hasSuffixDir(file, dir string) bool {
	i := len(file)
	for i > 0 && file[i-1] != '/' {
		i--
	}
	return i >= len(dir) && file[i-len(dir):i] == dir
}

func shareSiteAlloc(x unsafe.Pointer, size uintptr) {
	if !shareSitesMain {
		return
	}
	var pcs [12]uintptr
	n := callers(2, pcs[:])
	first := 0
	for i := 0; i < n; i++ {
		f := findfunc(pcs[i])
		if !f.valid() {
			continue
		}
		// (The runtime's functions have other names where another
		// package links to them.)
		name := funcname(f)
		if stringslite.HasPrefix(name, "runtime.") || stringslite.HasPrefix(name, "reflect.") || stringslite.HasPrefix(name, "internal/runtime/") {
			continue
		}
		if file, _ := funcline(f, pcs[i]-1); hasSuffixDir(file, "/runtime/") {
			continue
		}
		first = i
		break
	}
	pc := pcs[first]
	if pc == 0 {
		return
	}
	t := &shareSiteTab
	lock(&t.lock)
	if t.objs == nil {
		t.objs = (*[shareSiteObjs]shareSiteObj)(sysAlloc(unsafe.Sizeof(*t.objs), &memstats.other_sys, "share sites"))
		if t.objs == nil {
			unlock(&t.lock)
			return
		}
	}
	h := uint32(uint64(pc)*0x9e3779b97f4a7c15>>40) % uint32(len(t.sites))
	var st *shareSite
	for probe := 0; probe < 64; probe++ {
		e := &t.sites[(h+uint32(probe))%uint32(len(t.sites))]
		if e.pc == pc {
			st = e
			break
		}
		if e.pc == 0 {
			e.pc = pc
			copy(e.stack[:], pcs[first:n])
			t.nsites++
			st = e
			break
		}
	}
	if st == nil {
		t.full++
		unlock(&t.lock)
		return
	}
	st.allocs++
	st.bytes += uint64(size)
	o := &t.objs[uintptr(x)>>3%shareSiteObjs]
	o.addr = uintptr(x)
	o.site = uint32((uintptr(unsafe.Pointer(st)) - uintptr(unsafe.Pointer(&t.sites[0]))) / unsafe.Sizeof(*st))
	o.size = uint32(min(size, 1<<32-1))
	unlock(&t.lock)
}

func shareSiteMarked(base uintptr) {
	t := &shareSiteTab
	lock(&t.lock)
	if t.objs != nil {
		if o := &t.objs[base>>3%shareSiteObjs]; o.addr == base {
			st := &t.sites[o.site]
			st.marked++
			st.markedSize += uint64(o.size)
			o.addr = 0
		} else {
			t.unknown++
		}
	}
	unlock(&t.lock)
}

func printShareSites() {
	if debug.sharesites == 0 {
		return
	}
	t := &shareSiteTab
	lock(&t.lock)
	var allocs, marked uint64
	for i := range t.sites {
		allocs += t.sites[i].allocs
		marked += t.sites[i].marked
	}
	print("sharesites: ", t.nsites, " places allocated ", allocs, " objects with pointers that are not local; ", marked,
		" were marked as shared, and ", t.unknown, " more of places unknown; ", t.full, " not counted\n")
	var sum uint64
	for n := 0; n < 40; n++ {
		var best *shareSite
		for i := range t.sites {
			e := &t.sites[i]
			if e.pc == 0 || e.printed || e.allocs <= e.marked {
				continue
			}
			if best == nil || e.allocs-e.marked > best.allocs-best.marked {
				best = e
			}
		}
		if best == nil {
			break
		}
		best.printed = true
		sum += best.allocs - best.marked
		print("sharesites: #", n+1, " never shared ", best.allocs-best.marked, " of ", best.allocs, " objects, ", best.bytes-best.markedSize, " bytes (", sum*100/max(allocs-marked, 1), "% so far)\n")
		for _, pc := range best.stack {
			if f := findfunc(pc); f.valid() {
				file, line := funcline(f, pc-1)
				print("sharesites:\t", funcname(f), " ", file, ":", line, "\n")
			}
		}
	}
	unlock(&t.lock)
}
