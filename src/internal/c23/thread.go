// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// Threads, thread-local storage and atomics. A C thread is a goroutine.

// Thread-local storage.

// A TLS is a _Thread_local object: each goroutine has its own, made by
// new (allocated and initialized) the first time it uses it.
type TLS struct {
	new func() *Void
}

func NewTLS(new func() *Void) *TLS { return &TLS{new: new} }

// gSelf returns the calling goroutine's table, which identifies it while
// it runs.
func GSelf() *gTable {
	slot := gTableSlot()
	tb := (*gTable)(*slot)
	if tb == nil {
		tb = new(gTable)
		*slot = unsafe.Pointer(tb)
	}
	return tb
}

// Get returns the calling goroutine's object.
func (t *TLS) Get() *Void {
	tb := GSelf()
	if p, ok := tb.tls[t]; ok {
		return p
	}
	if tb.tls == nil {
		tb.tls = map[*TLS]*Void{}
	}
	p := t.new()
	tb.tls[t] = p
	return p
}

// Threads: pthread_t is an id.

type Thread struct {
	done  chan struct{}
	Ret   *Void // pthread_exit's value, or the start routine's
	Res   int32 // thrd_exit's, or a <threads.h> start routine's
	Sig   *SigThread
	Start time.Time // when it started: its CPU-time clock counts from then

	// Under ThreadsMu.
	detached bool // pthread_detach'ed: it forgets itself as it ends
	joining  bool // a thread waits in pthread_join for it

	cancel Cancel

	// Its scheduling policy and priority (pthread_setschedparam), which
	// change nothing. Under ThreadsMu.
	Policy, Priority int32
}

var (
	ThreadsMu sync.Mutex
	Threads   = map[uint64]*Thread{}
	threadN   atomic.Uint64
	ThreadID  = NewTLS(func() *Void { return New[uint64]() })
)

// spawn starts a goroutine that is a thread, whose id it stores at thr
// (a pthread_t or thrd_t, each an unsigned long), and runs run in it.
func Spawn(thr *Void, run func(t *Thread)) { spawnThread(thr, false, false, run) }

// SpawnThread is Spawn of a thread that is detached from the start if
// detached. The new thread has its creator's scheduling policy and
// priority.
func SpawnThread(thr *Void, detached bool, run func(t *Thread)) *Thread {
	return spawnThread(thr, detached, false, run)
}

// SpawnBlocked is Spawn of a detached thread that blocks every signal it
// may, as glibc's and musl's threads of SIGEV_THREAD notifications do.
func SpawnBlocked(thr *Void, run func(t *Thread)) { spawnThread(thr, true, true, run) }

func spawnThread(thr *Void, detached, blocked bool, run func(t *Thread)) *Thread {
	id := threadN.Add(1)
	t := newThread()
	self := Self()
	ThreadsMu.Lock()
	t.detached = detached
	if self != nil {
		t.Policy, t.Priority = self.Policy, self.Priority
	}
	Threads[id] = t
	ThreadsMu.Unlock()
	StoreLong(thr, int64(id)) // pthread_t is unsigned long
	fe := inheritFenv()
	sig := sigInherit(blocked)
	t.Sig = sig
	liveAdd(1)
	go func() {
		Store64(ThreadID.Get(), id)
		tb := GSelf()
		tb.fe = fe
		tb.Sig = sig
		tb.th = t
		defer liveAdd(-1)
		defer func() {
			ThreadsMu.Lock()
			if t.detached {
				delete(Threads, id)
			}
			ThreadsMu.Unlock()
		}()
		defer close(t.done)
		// The thread takes no more signals before it is joined: a
		// signal to the process sent after pthread_join returns must not
		// go to it (and take its default action for a mask the thread
		// no longer has).
		defer sigExit(sig)
		defer releaseRobust()
		defer runKeyDestructors()
		defer func() {
			if r := recover(); r != nil {
				if e, ok := r.(ThreadExit); ok {
					t.Ret, t.Res = e.Ret, e.Res
					return
				}
				panic(r)
			}
		}()
		run(t)
	}()
	return t
}

func newThread() *Thread {
	return &Thread{done: make(chan struct{}), Start: time.Now(), cancel: Cancel{ch: make(chan struct{})}}
}

type ThreadExit struct {
	Ret *Void
	Res int32
}

// join waits for thread id to end, and forgets it: nil if there is no
// such thread to join.
func Join(id uint64) *Thread {
	t, _ := JoinErr(id)
	return t
}

// JoinErr is pthread_join's join: it waits for thread id to end, and
// forgets it, or returns pthread_join's error: ESRCH if there is no such
// thread, EINVAL if it is detached or another thread joins it, EDEADLK
// if it is the calling thread. It is a cancellation point: a canceled
// thread stops waiting, and the thread it waited for stays joinable.
func JoinErr(id uint64) (*Thread, int32) {
	TestCancel()
	self := Self()
	ThreadsMu.Lock()
	t := Threads[id]
	switch {
	case t == nil:
		ThreadsMu.Unlock()
		return nil, ESRCH
	case t.detached || t.joining:
		ThreadsMu.Unlock()
		return nil, EINVAL
	case t == self:
		ThreadsMu.Unlock()
		return nil, EDEADLK
	}
	t.joining = true
	ThreadsMu.Unlock()
	select {
	case <-t.done:
	case <-CancelChan():
		select {
		case <-t.done:
		default:
			ThreadsMu.Lock()
			t.joining = false
			ThreadsMu.Unlock()
			TestCancel() // acts: it does not return
		}
	}
	ThreadsMu.Lock()
	delete(Threads, id)
	ThreadsMu.Unlock()
	return t, 0
}

// Detach is pthread_detach: ESRCH if there is no thread id, EINVAL if it
// is detached already.
func Detach(id uint64) int32 {
	ThreadsMu.Lock()
	defer ThreadsMu.Unlock()
	t := Threads[id]
	switch {
	case t == nil:
		return ESRCH
	case t.detached:
		return EINVAL
	}
	select {
	case <-t.done:
		delete(Threads, id)
	default:
		t.detached = true
	}
	return 0
}

// ThreadOf returns thread id (0 is the main thread), or nil if there is
// no such thread, or it ended and was joined or detached.
func ThreadOf(id uint64) *Thread {
	if id == 0 {
		return MainThread
	}
	ThreadsMu.Lock()
	defer ThreadsMu.Unlock()
	return Threads[id]
}

// Done reports whether t has ended.
func (t *Thread) Done() bool {
	select {
	case <-t.done:
		return true
	default:
		return false
	}
}

// Mutexes and condition variables: kept by the address of the C object,
// which never moves (it is shared, so not on a goroutine's stack).

// pthread_mutexattr_settype's types: glibc's and musl's.
const (
	PTHREAD_MUTEX_NORMAL    = 0
	PTHREAD_MUTEX_RECURSIVE = 1
)

// A mutex is a pthread_mutex_t. One of type PTHREAD_MUTEX_RECURSIVE or
// PTHREAD_MUTEX_ERRORCHECK, or a robust one, knows its owner, the
// goroutine that locked it.
type Mutex struct {
	Mu     sync.Mutex
	Kind   int32
	Owner  atomic.Pointer[gTable]
	Count  int  // the owner's locks of a recursive mutex
	Robust bool // PTHREAD_MUTEX_ROBUST

	// pthread_mutexattr_setprotocol's protocol and the priority ceiling,
	// kept for pthread_mutex_getprioceiling.
	Protocol, Ceiling int32

	// A robust mutex's state, under Mu: its owner ended holding it (the
	// next to lock it gets EOWNERDEAD), its state is inconsistent (until
	// pthread_mutex_consistent), or it cannot be used any more
	// (ENOTRECOVERABLE).
	ownerDied      bool
	inconsistent   bool
	notRecoverable atomic.Bool
}

var (
	Mutexes sync.Map // uintptr -> *mutex

)

// How lock waits for a mutex another thread holds.
const (
	LockWait  = iota // until it is free
	LockTry          // not at all: EBUSY
	LockUntil        // until a deadline: ETIMEDOUT
)

// owned reports whether m keeps its owner.
func (m *Mutex) owned() bool { return m.Kind != PTHREAD_MUTEX_NORMAL || m.Robust }

func (m *Mutex) Lock(how int, deadline time.Time) int32 {
	if !m.owned() {
		return m.acquire(how, deadline)
	}
	self := GSelf()
	if m.Owner.Load() == self {
		if m.Kind == PTHREAD_MUTEX_RECURSIVE {
			m.Count++
			return 0
		}
		return If[int32](how == LockTry, EBUSY, EDEADLK)
	}
	if m.notRecoverable.Load() {
		return ENOTRECOVERABLE
	}
	if e := m.acquire(how, deadline); e != 0 {
		return e
	}
	return m.took(self, 1)
}

// took makes self, which has just locked m.Mu, m's owner: its count of
// locks is n. It returns EOWNERDEAD for a robust mutex whose previous
// owner ended holding it, and ENOTRECOVERABLE (unlocking it) for one
// that cannot be used.
func (m *Mutex) took(self *gTable, n int) int32 {
	if m.notRecoverable.Load() {
		m.Mu.Unlock()
		return ENOTRECOVERABLE
	}
	m.Owner.Store(self)
	m.Count = n
	if !m.Robust {
		return 0
	}
	if self.robust == nil {
		self.robust = map[*Mutex]struct{}{}
	}
	self.robust[m] = struct{}{}
	if m.ownerDied {
		m.ownerDied = false
		m.inconsistent = true
		return EOWNERDEAD
	}
	return 0
}

// Unlock is pthread_mutex_unlock: EPERM if the mutex keeps its owner and
// the calling thread is not it. A robust mutex unlocked in an
// inconsistent state cannot be used any more.
func (m *Mutex) Unlock() int32 {
	if !m.owned() {
		m.Mu.Unlock()
		return 0
	}
	self := GSelf()
	if m.Owner.Load() != self {
		return EPERM
	}
	if m.Count--; m.Count > 0 {
		return 0
	}
	if m.Robust {
		delete(self.robust, m)
		if m.inconsistent {
			m.inconsistent = false
			m.notRecoverable.Store(true)
		}
	}
	m.Owner.Store(nil)
	m.Mu.Unlock()
	return 0
}

// Release unlocks m for pthread_cond_wait, however many times its owner
// locked it: Reacquire locks it again as many times. EPERM if m keeps
// its owner and the calling thread is not it.
func (m *Mutex) Release() (n int, e int32) {
	if !m.owned() {
		m.Mu.Unlock()
		return 1, 0
	}
	self := GSelf()
	if m.Owner.Load() != self {
		return 0, EPERM
	}
	n = m.Count
	if m.Robust {
		delete(self.robust, m)
	}
	m.Owner.Store(nil)
	m.Mu.Unlock()
	return n, 0
}

// Reacquire locks m again after Release: EOWNERDEAD if it is robust and
// its owner meanwhile ended holding it (ENOTRECOVERABLE, not locking it,
// if it cannot be used).
func (m *Mutex) Reacquire(n int) int32 {
	m.Mu.Lock()
	if !m.owned() {
		return 0
	}
	return m.took(GSelf(), n)
}

// Consistent is pthread_mutex_consistent: EINVAL unless m is robust, the
// calling thread holds it and its state is inconsistent.
func (m *Mutex) Consistent() int32 {
	if !m.Robust || !m.inconsistent || m.Owner.Load() != GSelf() {
		return EINVAL
	}
	m.inconsistent = false
	return 0
}

// releaseRobust unlocks, as a thread ends, the robust mutexes it holds:
// the next thread to lock each gets EOWNERDEAD.
func releaseRobust() {
	tb := GSelf()
	for m := range tb.robust {
		m.ownerDied = true
		m.Owner.Store(nil)
		m.Count = 0
		m.Mu.Unlock()
	}
	tb.robust = nil
}

// acquire locks m.mu. sync.Mutex has no timed lock, so a deadline is
// waited for by trying, at intervals from 50µs that grow to 10ms.
func (m *Mutex) acquire(how int, deadline time.Time) int32 {
	switch how {
	case LockWait:
		m.Mu.Lock()
		return 0
	case LockTry:
		return If[int32](m.Mu.TryLock(), 0, EBUSY)
	}
	for wait := 50 * time.Microsecond; !m.Mu.TryLock(); wait = min(2*wait, 10*time.Millisecond) {
		left := time.Until(deadline)
		if left <= 0 {
			return ETIMEDOUT
		}
		time.Sleep(min(wait, left))
	}
	return 0
}

// loadDeadline reads the struct timespec at ts, a CLOCK_REALTIME
// (TIME_UTC) time; false if it is not one.
func LoadDeadline(ts *Void) (time.Time, bool) {
	sec, nsec := int64(Load64(ts)), LoadLong(Add(ts, 8))
	if nsec < 0 || nsec >= 1e9 {
		return time.Time{}, false
	}
	return time.Unix(sec, nsec), true
}

// pthread_once: pthread_once_t is an int, 0 (PTHREAD_ONCE_INIT) until
// the function has run.

// uintptr -> *sync.Once

// Thread-specific data: pthread_key_t is an index in keys, and each
// goroutine's values are in its gTable.

type Key struct {
	Destructor *Void
	Live       bool
}

var (
	KeysMu sync.Mutex
	Keys   []Key
)

// runKeyDestructors runs, as a thread ends, the destructors of its
// non-null values (again for values they set, at most 4 times:
// PTHREAD_DESTRUCTOR_ITERATIONS).
func runKeyDestructors() {
	tb := GSelf()
	for range 4 {
		ran := false
		for k, v := range tb.Keys {
			if Addr(v) == 0 {
				continue
			}
			delete(tb.Keys, k)
			KeysMu.Lock()
			var d *Void
			if int(k) < len(Keys) && Keys[k].Live {
				d = Keys[k].Destructor
			}
			KeysMu.Unlock()
			if Addr(d) != 0 {
				FuncOf[func(*Void)](d)(v)
				ran = true
			}
		}
		if !ran {
			return
		}
	}
}

// Atomics. 4- and 8-byte scalars use sync/atomic; others (and pointers,
// whose words carry capabilities) a lock chosen by the address.
//
// These are what translated C does to an _Atomic lvalue of its own, as
// c23.Peek and c23.Poke are its loads and stores: the address is one
// that the compiler checked where C made the pointer, and nothing is
// checked here. A function of the library that is given a pointer to
// an atomic object (atomic_flag_test_and_set, pthread_once) checks it
// first (c23.Check).

type atomicT interface {
	~int8 | ~uint8 | ~int16 | ~uint16 | ~int32 | ~uint32 | ~int64 | ~uint64 | ~float32 | ~float64
}

var stripes [64]sync.Mutex

func stripe(p *Void) *sync.Mutex { return &stripes[(Addr(p)>>3)%64] }

//go:nocheckptr
func AtomicLoad[T atomicT](p *Void) T {
	var z T
	n := unsafe.Sizeof(z)
	a := unsafe.Pointer(p)
	switch n {
	case 4:
		b := atomic.LoadUint32((*uint32)(a))
		return *(*T)(unsafe.Pointer(&b))
	case 8:
		b := atomic.LoadUint64((*uint64)(a))
		return *(*T)(unsafe.Pointer(&b))
	}
	m := stripe(p)
	m.Lock()
	v := *(*T)(a)
	m.Unlock()
	return v
}

//go:nocheckptr
func AtomicStore[T atomicT](p *Void, v T) {
	n := unsafe.Sizeof(v)
	a := unsafe.Pointer(p)
	switch n {
	case 4:
		atomic.StoreUint32((*uint32)(a), *(*uint32)(unsafe.Pointer(&v)))
		return
	case 8:
		atomic.StoreUint64((*uint64)(a), *(*uint64)(unsafe.Pointer(&v)))
		return
	}
	m := stripe(p)
	m.Lock()
	*(*T)(a) = v
	m.Unlock()
}

// AtomicCAS replaces old at p with new if it is there (by bits, as C
// compares).
//
//go:nocheckptr
func AtomicCAS[T atomicT](p *Void, old, new T) bool {
	n := unsafe.Sizeof(old)
	a := unsafe.Pointer(p)
	switch n {
	case 4:
		return atomic.CompareAndSwapUint32((*uint32)(a), *(*uint32)(unsafe.Pointer(&old)), *(*uint32)(unsafe.Pointer(&new)))
	case 8:
		return atomic.CompareAndSwapUint64((*uint64)(a), *(*uint64)(unsafe.Pointer(&old)), *(*uint64)(unsafe.Pointer(&new)))
	}
	m := stripe(p)
	m.Lock()
	defer m.Unlock()
	if *(*T)(a) == old {
		*(*T)(a) = new
		return true
	}
	return false
}

// memEq reports whether the n bytes at a and at b are the same.
func memEq(a, b unsafe.Pointer, n uintptr) bool {
	return string(slice(a, n)) == string(slice(b, n))
}

// AtomicCmpXchg is atomic_compare_exchange: if p holds what expected
// points to, desired replaces it; if not, what p holds is stored where
// expected points.
//
//go:nocheckptr
func AtomicCmpXchg[T atomicT](p, expected *Void, desired T) int32 {
	var old T
	switch any(old).(type) {
	case float32:
		old = any(PeekF32(expected)).(T)
	case float64:
		old = any(PeekF64(expected)).(T)
	default:
		old = *(*T)(unsafe.Pointer(expected))
	}
	for {
		if AtomicCAS(p, old, desired) {
			return 1
		}
		cur := AtomicLoad[T](p)
		if !memEq(unsafe.Pointer(&cur), unsafe.Pointer(&old), unsafe.Sizeof(old)) {
			*(*T)(unsafe.Pointer(expected)) = cur
			return 0
		}
	}
}

// AtomicFetch applies op ('+', '-', '&', '|', '^', '=') with v at p, and
// returns what p held.
func AtomicFetch[T atomicT](p *Void, op byte, v T) T {
	for {
		old := AtomicLoad[T](p)
		var new T
		switch op {
		case '+':
			new = old + v
		case '-':
			new = old - v
		case '=':
			new = v
		default:
			new = bitOp(old, op, v)
		}
		if AtomicCAS(p, old, new) {
			return old
		}
	}
}

func bitOp[T atomicT](x T, op byte, y T) T {
	var a, b uint64
	n := unsafe.Sizeof(x)
	copy(slice(unsafe.Pointer(&a), n), slice(unsafe.Pointer(&x), n))
	copy(slice(unsafe.Pointer(&b), n), slice(unsafe.Pointer(&y), n))
	switch op {
	case '&':
		a &= b
	case '|':
		a |= b
	case '^':
		a ^= b
	}
	var r T
	copy(slice(unsafe.Pointer(&r), n), slice(unsafe.Pointer(&a), n))
	return r
}

// Pointers.

func AtomicLoadP(p *Void) *Void {
	m := stripe(p)
	m.Lock()
	defer m.Unlock()
	return peekWord(p)
}

func AtomicStoreP(p, v *Void) {
	m := stripe(p)
	m.Lock()
	defer m.Unlock()
	pokeWord(p, v)
}

func AtomicCASP(p, old, new *Void) bool {
	m := stripe(p)
	m.Lock()
	defer m.Unlock()
	if Addr(peekWord(p)) != Addr(old) {
		return false
	}
	pokeWord(p, new)
	return true
}

func AtomicCmpXchgP(p, expected, desired *Void) int32 {
	m := stripe(p)
	m.Lock()
	defer m.Unlock()
	cur := peekWord(p)
	if Addr(cur) == Addr(peekWord(expected)) {
		pokeWord(p, desired)
		return 1
	}
	pokeWord(expected, cur)
	return 0
}

// AtomicFetchP applies op ('+' by bytes, '=') at p, returning what it held.
func AtomicFetchP(p *Void, op byte, n int, v *Void) *Void {
	m := stripe(p)
	m.Lock()
	defer m.Unlock()
	old := peekWord(p)
	if op == '=' {
		pokeWord(p, v)
	} else {
		pokeWord(p, Add(old, n))
	}
	return old
}

// Fence is atomic_thread_fence: every atomic here is sequentially
// consistent already.
func Fence() {}

// Atomic operations on objects other than scalars (an _Atomic struct):
// under a lock, one of those striped by the object's address.
var aggLocks [64]sync.Mutex

func aggLock(p *Void) *sync.Mutex { return &aggLocks[(Addr(p)>>4)%uintptr(len(aggLocks))] }

// AtomicLoadAgg copies the n bytes of the atomic object at src to dst.
func AtomicLoadAgg(dst, src *Void, n uintptr) {
	m := aggLock(src)
	m.Lock()
	Move(dst, src, n)
	m.Unlock()
}

// AtomicStoreAgg copies n bytes at src to the atomic object at dst.
func AtomicStoreAgg(dst, src *Void, n uintptr) {
	m := aggLock(dst)
	m.Lock()
	Move(dst, src, n)
	m.Unlock()
}

// AtomicSwapAgg replaces the atomic object at p with v's n bytes, and
// copies what it was to old.
func AtomicSwapAgg(p, old, v *Void, n uintptr) {
	m := aggLock(p)
	m.Lock()
	Move(old, p, n)
	Move(p, v, n)
	m.Unlock()
}

// AtomicCmpXchgAgg is atomic_compare_exchange of the n bytes at p: if
// they are those at exp, they become those at des; if not, exp's become
// p's.
func AtomicCmpXchgAgg(p, exp, des *Void, n uintptr) bool {
	m := aggLock(p)
	m.Lock()
	defer m.Unlock()
	if memEq(unsafe.Pointer(p), unsafe.Pointer(exp), n) {
		Move(p, des, n)
		return true
	}
	Move(exp, p, n)
	return false
}

// peekWord and pokeWord load and store the word at p, which holds a
// pointer or an intptr_t of C's that is _Atomic: an lvalue of C's, which
// the functions above are given the address of and check nothing of.
//
//go:nocheckptr
func peekWord(p *Void) *Void { return *(**Void)(unsafe.Pointer(p)) }

//go:nocheckptr
func pokeWord(p, v *Void) { *(**Void)(unsafe.Pointer(p)) = v }

// The atomic operations on C's integers of 4 and 8 bytes, which are the
// machine's: each is a function small enough to be inlined where
// cmd/cc calls it, as AtomicFetch and the others, which are for every
// type and operation, are not. A reference count is one of these.

type atomic32 interface{ ~int32 | ~uint32 }
type atomic64 interface{ ~int64 | ~uint64 }

//go:nocheckptr
func AtomicLoad32[T atomic32](p *Void) T {
	return T(atomic.LoadUint32((*uint32)(unsafe.Pointer(p))))
}

//go:nocheckptr
func AtomicLoad64[T atomic64](p *Void) T {
	return T(atomic.LoadUint64((*uint64)(unsafe.Pointer(p))))
}

//go:nocheckptr
func AtomicStore32[T atomic32](p *Void, v T) {
	atomic.StoreUint32((*uint32)(unsafe.Pointer(p)), uint32(v))
}

//go:nocheckptr
func AtomicStore64[T atomic64](p *Void, v T) {
	atomic.StoreUint64((*uint64)(unsafe.Pointer(p)), uint64(v))
}

//go:nocheckptr
func AtomicCAS32[T atomic32](p *Void, old, new T) bool {
	return atomic.CompareAndSwapUint32((*uint32)(unsafe.Pointer(p)), uint32(old), uint32(new))
}

//go:nocheckptr
func AtomicCAS64[T atomic64](p *Void, old, new T) bool {
	return atomic.CompareAndSwapUint64((*uint64)(unsafe.Pointer(p)), uint64(old), uint64(new))
}

// AtomicAdd32 adds v at p and returns what p held, as AtomicFetch does;
// AtomicSub32 takes v from it, and AtomicSwap32 stores v.
//
//go:nocheckptr
func AtomicAdd32[T atomic32](p *Void, v T) T {
	return T(atomic.AddUint32((*uint32)(unsafe.Pointer(p)), uint32(v))) - v
}

//go:nocheckptr
func AtomicAdd64[T atomic64](p *Void, v T) T {
	return T(atomic.AddUint64((*uint64)(unsafe.Pointer(p)), uint64(v))) - v
}

//go:nocheckptr
func AtomicSub32[T atomic32](p *Void, v T) T {
	return T(atomic.AddUint32((*uint32)(unsafe.Pointer(p)), -uint32(v))) + v
}

//go:nocheckptr
func AtomicSub64[T atomic64](p *Void, v T) T {
	return T(atomic.AddUint64((*uint64)(unsafe.Pointer(p)), -uint64(v))) + v
}

//go:nocheckptr
func AtomicSwap32[T atomic32](p *Void, v T) T {
	return T(atomic.SwapUint32((*uint32)(unsafe.Pointer(p)), uint32(v)))
}

//go:nocheckptr
func AtomicSwap64[T atomic64](p *Void, v T) T {
	return T(atomic.SwapUint64((*uint64)(unsafe.Pointer(p)), uint64(v)))
}

// AtomicCmpXchg32 is AtomicCmpXchg for an integer of 4 bytes.
//
//go:nocheckptr
func AtomicCmpXchg32[T atomic32](p, expected *Void, desired T) int32 {
	e := (*uint32)(unsafe.Pointer(expected))
	for {
		old := *e
		if atomic.CompareAndSwapUint32((*uint32)(unsafe.Pointer(p)), old, uint32(desired)) {
			return 1
		}
		if cur := atomic.LoadUint32((*uint32)(unsafe.Pointer(p))); cur != old {
			*e = cur
			return 0
		}
	}
}

// AtomicCmpXchg64 is AtomicCmpXchg for an integer of 8 bytes.
//
//go:nocheckptr
func AtomicCmpXchg64[T atomic64](p, expected *Void, desired T) int32 {
	e := (*uint64)(unsafe.Pointer(expected))
	for {
		old := *e
		if atomic.CompareAndSwapUint64((*uint64)(unsafe.Pointer(p)), old, uint64(desired)) {
			return 1
		}
		if cur := atomic.LoadUint64((*uint64)(unsafe.Pointer(p))); cur != old {
			*e = cur
			return 0
		}
	}
}
