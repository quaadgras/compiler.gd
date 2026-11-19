// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"runtime"
	"slices"
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
	new func() Ptr
}

func NewTLS(new func() Ptr) *TLS { return &TLS{new: new} }

// gSelf returns the calling goroutine's table, which identifies it while
// it runs.
func gSelf() *gTable {
	slot := gTableSlot()
	tb := (*gTable)(*slot)
	if tb == nil {
		tb = new(gTable)
		*slot = unsafe.Pointer(tb)
	}
	return tb
}

// Get returns the calling goroutine's object.
func (t *TLS) Get() Ptr {
	tb := gSelf()
	if p, ok := tb.tls[t]; ok {
		return p
	}
	if tb.tls == nil {
		tb.tls = map[*TLS]Ptr{}
	}
	p := t.new()
	tb.tls[t] = p
	return p
}

// Threads: pthread_t is an id.

type thread struct {
	done chan struct{}
	ret  Ptr   // pthread_exit's value, or the start routine's
	res  int32 // thrd_exit's, or a <threads.h> start routine's
}

var (
	threadsMu sync.Mutex
	threads   = map[uint64]*thread{}
	threadN   atomic.Uint64
	threadID  = NewTLS(func() Ptr { return New[uint64]() })
)

// PthreadCreate is pthread_create.
func PthreadCreate(thr, attr, start, arg Ptr) int32 {
	f := FuncOf[func(Ptr) Ptr](start)
	spawn(thr, func(t *thread) { t.ret = f(arg) })
	return 0
}

// spawn starts a goroutine that is a thread, whose id it stores at thr
// (a pthread_t or thrd_t, each an unsigned long), and runs run in it.
func spawn(thr Ptr, run func(t *thread)) {
	id := threadN.Add(1)
	t := &thread{done: make(chan struct{})}
	threadsMu.Lock()
	threads[id] = t
	threadsMu.Unlock()
	storeLong(thr, int64(id)) // pthread_t is unsigned long
	fe := inheritFenv()
	sig := sigInherit()
	go func() {
		Store64(threadID.Get(), id)
		gSelf().fe = fe
		gSelf().sig = sig
		defer sigExit(sig)
		defer close(t.done)
		defer runKeyDestructors()
		defer func() {
			if r := recover(); r != nil {
				if e, ok := r.(threadExit); ok {
					t.ret, t.res = e.ret, e.res
					return
				}
				panic(r)
			}
		}()
		run(t)
	}()
}

type threadExit struct {
	ret Ptr
	res int32
}

// PthreadExit is pthread_exit.
func PthreadExit(ret Ptr) { panic(threadExit{ret: ret}) }

// PthreadJoin is pthread_join.
func PthreadJoin(id uint64, ret Ptr) int32 {
	t := join(id)
	if t == nil {
		return 3 // ESRCH
	}
	if Addr(ret) != 0 {
		StorePtr(ret, t.ret)
	}
	return 0
}

// join waits for thread id to end, and forgets it: nil if there is no
// such thread to join.
func join(id uint64) *thread {
	threadsMu.Lock()
	t := threads[id]
	delete(threads, id)
	threadsMu.Unlock()
	if t != nil {
		<-t.done
	}
	return t
}

func PthreadDetach(id uint64) int32 {
	threadsMu.Lock()
	delete(threads, id)
	threadsMu.Unlock()
	return 0
}

// PthreadSelf is pthread_self: 0 for the main thread.
func PthreadSelf() uint64 { return Load64(threadID.Get()) }

func PthreadEqual(a, b uint64) int32 { return B2I(a == b) }

// Mutexes and condition variables: kept by the address of the C object,
// which never moves (it is shared, so not on a goroutine's stack).

// pthread_mutexattr_settype's types: glibc's and musl's.
const (
	PTHREAD_MUTEX_NORMAL     = 0
	PTHREAD_MUTEX_RECURSIVE  = 1
	PTHREAD_MUTEX_ERRORCHECK = 2
)

// A mutex is a pthread_mutex_t. One of type PTHREAD_MUTEX_RECURSIVE or
// PTHREAD_MUTEX_ERRORCHECK knows its owner, the goroutine that locked it.
type mutex struct {
	mu    sync.Mutex
	kind  int32
	owner atomic.Pointer[gTable]
	count int // the owner's locks of a recursive mutex
}

var (
	mutexes sync.Map // uintptr -> *mutex
	conds   sync.Map // uintptr -> *cond
)

func mutexOf(p Ptr) *mutex {
	check(p, 4)
	a := Addr(p)
	if m, ok := mutexes.Load(a); ok {
		return m.(*mutex)
	}
	m, _ := mutexes.LoadOrStore(a, new(mutex))
	return m.(*mutex)
}

// PthreadMutexInit is pthread_mutex_init, of the type attr has (a
// normal mutex if attr is null).
func PthreadMutexInit(m, attr Ptr) int32 {
	check(m, 4)
	mu := new(mutex)
	if Addr(attr) != 0 {
		mu.kind = int32(Load32(attr))
	}
	mutexes.Store(Addr(m), mu)
	return 0
}

func PthreadMutexDestroy(m Ptr) int32 {
	if mu, ok := mutexes.Load(Addr(m)); ok && mu.(*mutex).owner.Load() == nil && mu.(*mutex).mu.TryLock() {
		mutexes.Delete(Addr(m))
	}
	return 0
}

func PthreadMutexLock(m Ptr) int32 { return mutexOf(m).lock(lockWait, time.Time{}) }

func PthreadMutexTrylock(m Ptr) int32 { return mutexOf(m).lock(lockTry, time.Time{}) }

// PthreadMutexTimedlock is pthread_mutex_timedlock, which gives up at
// the CLOCK_REALTIME time at ts.
func PthreadMutexTimedlock(m, ts Ptr) int32 {
	d, ok := loadDeadline(ts)
	if !ok {
		return EINVAL
	}
	return mutexOf(m).lock(lockUntil, d)
}

// How lock waits for a mutex another thread holds.
const (
	lockWait  = iota // until it is free
	lockTry          // not at all: EBUSY
	lockUntil        // until a deadline: ETIMEDOUT
)

func (m *mutex) lock(how int, deadline time.Time) int32 {
	if m.kind == PTHREAD_MUTEX_NORMAL {
		return m.acquire(how, deadline)
	}
	self := gSelf()
	if m.owner.Load() == self {
		if m.kind == PTHREAD_MUTEX_RECURSIVE {
			m.count++
			return 0
		}
		return If[int32](how == lockTry, EBUSY, EDEADLK)
	}
	if e := m.acquire(how, deadline); e != 0 {
		return e
	}
	m.owner.Store(self)
	m.count = 1
	return 0
}

// acquire locks m.mu. sync.Mutex has no timed lock, so a deadline is
// waited for by trying, at intervals from 50µs that grow to 10ms.
func (m *mutex) acquire(how int, deadline time.Time) int32 {
	switch how {
	case lockWait:
		m.mu.Lock()
		return 0
	case lockTry:
		return If[int32](m.mu.TryLock(), 0, EBUSY)
	}
	for wait := 50 * time.Microsecond; !m.mu.TryLock(); wait = min(2*wait, 10*time.Millisecond) {
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
func loadDeadline(ts Ptr) (time.Time, bool) {
	sec, nsec := int64(Load64(ts)), loadLong(Add(ts, 8))
	if nsec < 0 || nsec >= 1e9 {
		return time.Time{}, false
	}
	return time.Unix(sec, nsec), true
}

func PthreadMutexUnlock(m Ptr) int32 {
	mu := mutexOf(m)
	if mu.kind != PTHREAD_MUTEX_NORMAL {
		if mu.owner.Load() != gSelf() {
			return EPERM
		}
		if mu.count--; mu.count > 0 {
			return 0
		}
		mu.owner.Store(nil)
	}
	mu.mu.Unlock()
	return 0
}

// PthreadMutexattrInit is pthread_mutexattr_init: pthread_mutexattr_t
// holds the type.
func PthreadMutexattrInit(attr Ptr) int32 { Store32(attr, PTHREAD_MUTEX_NORMAL); return 0 }

func PthreadMutexattrDestroy(attr Ptr) int32 { return 0 }

func PthreadMutexattrSettype(attr Ptr, kind int32) int32 {
	if kind < PTHREAD_MUTEX_NORMAL || kind > PTHREAD_MUTEX_ERRORCHECK {
		return EINVAL
	}
	Store32(attr, uint32(kind))
	return 0
}

func PthreadMutexattrGettype(attr, kind Ptr) int32 {
	Store32(kind, Load32(attr))
	return 0
}

// A cond is a pthread_cond_t or cnd_t: the channels of the threads
// waiting for it, which a signal closes (sync.Cond has no timed wait).
type cond struct {
	mu      sync.Mutex
	waiters []chan struct{}
}

func condOf(c Ptr) *cond {
	check(c, 4)
	a := Addr(c)
	if v, ok := conds.Load(a); ok {
		return v.(*cond)
	}
	v, _ := conds.LoadOrStore(a, new(cond))
	return v.(*cond)
}

func PthreadCondInit(c, attr Ptr) int32 { return 0 }

func PthreadCondDestroy(c Ptr) int32 {
	if v, ok := conds.Load(Addr(c)); ok {
		cv := v.(*cond)
		cv.mu.Lock()
		if len(cv.waiters) == 0 {
			conds.Delete(Addr(c))
		}
		cv.mu.Unlock()
	}
	return 0
}

// PthreadCondWait is pthread_cond_wait.
func PthreadCondWait(c, m Ptr) int32 { return condOf(c).wait(mutexOf(m), time.Time{}) }

// PthreadCondTimedwait is pthread_cond_timedwait, which gives up at the
// CLOCK_REALTIME time at ts.
func PthreadCondTimedwait(c, m, ts Ptr) int32 {
	d, ok := loadDeadline(ts)
	if !ok {
		return EINVAL
	}
	return condOf(c).wait(mutexOf(m), d)
}

// wait unlocks mu, waits for a signal (or the deadline, if it is not
// zero: ETIMEDOUT) and locks mu again. A recursive mutex is unlocked
// however many times its owner locked it, and locked as many again.
func (cv *cond) wait(mu *mutex, deadline time.Time) int32 {
	self := gSelf()
	if mu.kind != PTHREAD_MUTEX_NORMAL && mu.owner.Load() != self {
		return EPERM
	}
	ch := make(chan struct{})
	cv.mu.Lock()
	cv.waiters = append(cv.waiters, ch)
	cv.mu.Unlock()

	n := mu.count
	if mu.kind != PTHREAD_MUTEX_NORMAL {
		mu.owner.Store(nil)
	}
	mu.mu.Unlock()
	var e int32
	if deadline.IsZero() {
		<-ch
	} else {
		t := time.NewTimer(time.Until(deadline))
		select {
		case <-ch:
		case <-t.C:
			// Timed out, unless a signal took ch from the waiters
			// meanwhile: then it is that signal's wakeup.
			cv.mu.Lock()
			if i := slices.Index(cv.waiters, ch); i >= 0 {
				cv.waiters = slices.Delete(cv.waiters, i, i+1)
				e = ETIMEDOUT
			}
			cv.mu.Unlock()
		}
		t.Stop()
	}
	mu.mu.Lock()
	if mu.kind != PTHREAD_MUTEX_NORMAL {
		mu.owner.Store(self)
		mu.count = n
	}
	return e
}

func PthreadCondSignal(c Ptr) int32 {
	if v, ok := conds.Load(Addr(c)); ok {
		cv := v.(*cond)
		cv.mu.Lock()
		if len(cv.waiters) > 0 {
			close(cv.waiters[0])
			cv.waiters = cv.waiters[1:]
		}
		cv.mu.Unlock()
	}
	return 0
}

func PthreadCondBroadcast(c Ptr) int32 {
	if v, ok := conds.Load(Addr(c)); ok {
		cv := v.(*cond)
		cv.mu.Lock()
		for _, ch := range cv.waiters {
			close(ch)
		}
		cv.waiters = nil
		cv.mu.Unlock()
	}
	return 0
}

// pthread_once: pthread_once_t is an int, 0 (PTHREAD_ONCE_INIT) until
// the function has run.

var onces sync.Map // uintptr -> *sync.Once

// PthreadOnce is pthread_once.
func PthreadOnce(once, f Ptr) int32 {
	if AtomicLoad[int32](once) != 0 {
		return 0
	}
	v, _ := onces.LoadOrStore(Addr(once), new(sync.Once))
	v.(*sync.Once).Do(func() {
		FuncOf[func()](f)()
		AtomicStore[int32](once, 1)
	})
	return 0
}

// Thread-specific data: pthread_key_t is an index in keys, and each
// goroutine's values are in its gTable.

type key struct {
	destructor Ptr
	live       bool
}

var (
	keysMu sync.Mutex
	keys   []key
)

// PthreadKeyCreate is pthread_key_create.
func PthreadKeyCreate(k, destructor Ptr) int32 {
	keysMu.Lock()
	defer keysMu.Unlock()
	if len(keys) >= 1024 { // PTHREAD_KEYS_MAX
		return EAGAIN
	}
	keys = append(keys, key{destructor, true})
	Store32(k, uint32(len(keys)-1))
	return 0
}

func PthreadKeyDelete(k uint32) int32 {
	keysMu.Lock()
	defer keysMu.Unlock()
	if int(k) >= len(keys) || !keys[k].live {
		return EINVAL
	}
	keys[k] = key{}
	return 0
}

func PthreadGetspecific(k uint32) Ptr {
	return gSelf().keys[k]
}

func PthreadSetspecific(k uint32, v Ptr) int32 {
	keysMu.Lock()
	ok := int(k) < len(keys) && keys[k].live
	keysMu.Unlock()
	if !ok {
		return EINVAL
	}
	tb := gSelf()
	if tb.keys == nil {
		tb.keys = map[uint32]Ptr{}
	}
	tb.keys[k] = v
	return 0
}

// runKeyDestructors runs, as a thread ends, the destructors of its
// non-null values (again for values they set, at most 4 times:
// PTHREAD_DESTRUCTOR_ITERATIONS).
func runKeyDestructors() {
	tb := gSelf()
	for range 4 {
		ran := false
		for k, v := range tb.keys {
			if Addr(v) == 0 {
				continue
			}
			delete(tb.keys, k)
			keysMu.Lock()
			var d Ptr
			if int(k) < len(keys) && keys[k].live {
				d = keys[k].destructor
			}
			keysMu.Unlock()
			if Addr(d) != 0 {
				FuncOf[func(Ptr)](d)(v)
				ran = true
			}
		}
		if !ran {
			return
		}
	}
}

func SchedYield() int32 { runtime.Gosched(); return 0 }

// Atomics. 4- and 8-byte scalars use sync/atomic; others (and pointers,
// whose words carry capabilities) a lock chosen by the address.

type atomicT interface {
	~int8 | ~uint8 | ~int16 | ~uint16 | ~int32 | ~uint32 | ~int64 | ~uint64 | ~float32 | ~float64
}

var stripes [64]sync.Mutex

func stripe(p Ptr) *sync.Mutex { return &stripes[(Addr(p)>>3)%64] }

func AtomicLoad[T atomicT](p Ptr) T {
	var z T
	n := unsafe.Sizeof(z)
	a := check(p, n)
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

func AtomicStore[T atomicT](p Ptr, v T) {
	n := unsafe.Sizeof(v)
	a := checkScalar(p, n)
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
func AtomicCAS[T atomicT](p Ptr, old, new T) bool {
	n := unsafe.Sizeof(old)
	a := checkScalar(p, n)
	switch n {
	case 4:
		return atomic.CompareAndSwapUint32((*uint32)(a), *(*uint32)(unsafe.Pointer(&old)), *(*uint32)(unsafe.Pointer(&new)))
	case 8:
		return atomic.CompareAndSwapUint64((*uint64)(a), *(*uint64)(unsafe.Pointer(&old)), *(*uint64)(unsafe.Pointer(&new)))
	}
	m := stripe(p)
	m.Lock()
	defer m.Unlock()
	if memEq(a, unsafe.Pointer(&old), n) {
		*(*T)(a) = new
		return true
	}
	return false
}

func memEq(a, b unsafe.Pointer, n uintptr) bool {
	return string(unsafe.Slice((*byte)(a), n)) == string(unsafe.Slice((*byte)(b), n))
}

// AtomicCmpXchg is atomic_compare_exchange: if p holds what expected
// points to, desired replaces it; if not, what p holds is stored where
// expected points.
func AtomicCmpXchg[T atomicT](p, expected Ptr, desired T) int32 {
	var old T
	switch any(old).(type) {
	case float32:
		old = any(LoadF32(expected)).(T)
	case float64:
		old = any(LoadF64(expected)).(T)
	default:
		old = *(*T)(check(expected, unsafe.Sizeof(old)))
	}
	for {
		if AtomicCAS(p, old, desired) {
			return 1
		}
		cur := AtomicLoad[T](p)
		if !memEq(unsafe.Pointer(&cur), unsafe.Pointer(&old), unsafe.Sizeof(old)) {
			*(*T)(checkScalar(expected, unsafe.Sizeof(old))) = cur
			return 0
		}
	}
}

// AtomicFetch applies op ('+', '-', '&', '|', '^', '=') with v at p, and
// returns what p held.
func AtomicFetch[T atomicT](p Ptr, op byte, v T) T {
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
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&a)), n), unsafe.Slice((*byte)(unsafe.Pointer(&x)), n))
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&b)), n), unsafe.Slice((*byte)(unsafe.Pointer(&y)), n))
	switch op {
	case '&':
		a &= b
	case '|':
		a |= b
	case '^':
		a ^= b
	}
	var r T
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&r)), n), unsafe.Slice((*byte)(unsafe.Pointer(&a)), n))
	return r
}

// Pointers.

func AtomicLoadP(p Ptr) Ptr {
	m := stripe(p)
	m.Lock()
	defer m.Unlock()
	return LoadPtr(p)
}

func AtomicStoreP(p, v Ptr) {
	m := stripe(p)
	m.Lock()
	defer m.Unlock()
	StorePtr(p, v)
}

func AtomicCASP(p, old, new Ptr) bool {
	m := stripe(p)
	m.Lock()
	defer m.Unlock()
	if Addr(LoadPtr(p)) != Addr(old) {
		return false
	}
	StorePtr(p, new)
	return true
}

func AtomicCmpXchgP(p, expected, desired Ptr) int32 {
	m := stripe(p)
	m.Lock()
	defer m.Unlock()
	cur := LoadPtr(p)
	if Addr(cur) == Addr(LoadPtr(expected)) {
		StorePtr(p, desired)
		return 1
	}
	StorePtr(expected, cur)
	return 0
}

// AtomicFetchP applies op ('+' by bytes, '=') at p, returning what it held.
func AtomicFetchP(p Ptr, op byte, n int, v Ptr) Ptr {
	m := stripe(p)
	m.Lock()
	defer m.Unlock()
	old := LoadPtr(p)
	if op == '=' {
		StorePtr(p, v)
	} else {
		StorePtr(p, Add(old, n))
	}
	return old
}

// Fence is atomic_thread_fence: every atomic here is sequentially
// consistent already.
func Fence() {}

// stdatomic.h's functions, for C that takes their addresses (calls are
// the macros'). An atomic_flag is an _Atomic _Bool.
func AtomicThreadFence(order uint32) {}

func AtomicFlagTestAndSet(p Ptr) uint8 { return AtomicFetch[uint8](p, '=', 1) }

func AtomicFlagTestAndSetExplicit(p Ptr, order uint32) uint8 { return AtomicFlagTestAndSet(p) }

func AtomicFlagClear(p Ptr) { AtomicStore[uint8](p, 0) }

func AtomicFlagClearExplicit(p Ptr, order uint32) { AtomicFlagClear(p) }

// Atomic operations on objects other than scalars (an _Atomic struct):
// under a lock, one of those striped by the object's address.
var aggLocks [64]sync.Mutex

func aggLock(p Ptr) *sync.Mutex { return &aggLocks[(Addr(p)>>4)%uintptr(len(aggLocks))] }

// AtomicLoadAgg copies the n bytes of the atomic object at src to dst.
func AtomicLoadAgg(dst, src Ptr, n uintptr) {
	m := aggLock(src)
	m.Lock()
	Memmove(dst, src, n)
	m.Unlock()
}

// AtomicStoreAgg copies n bytes at src to the atomic object at dst.
func AtomicStoreAgg(dst, src Ptr, n uintptr) {
	m := aggLock(dst)
	m.Lock()
	Memmove(dst, src, n)
	m.Unlock()
}

// AtomicSwapAgg replaces the atomic object at p with v's n bytes, and
// copies what it was to old.
func AtomicSwapAgg(p, old, v Ptr, n uintptr) {
	m := aggLock(p)
	m.Lock()
	Memmove(old, p, n)
	Memmove(p, v, n)
	m.Unlock()
}

// AtomicCmpXchgAgg is atomic_compare_exchange of the n bytes at p: if
// they are those at exp, they become those at des; if not, exp's become
// p's.
func AtomicCmpXchgAgg(p, exp, des Ptr, n uintptr) bool {
	m := aggLock(p)
	m.Lock()
	defer m.Unlock()
	if Memcmp(p, exp, uint64(n)) == 0 {
		Memmove(p, des, n)
		return true
	}
	Memmove(exp, p, n)
	return false
}
