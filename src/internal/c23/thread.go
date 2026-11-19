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
	new func() Ptr
}

func NewTLS(new func() Ptr) *TLS { return &TLS{new: new} }

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
func (t *TLS) Get() Ptr {
	tb := GSelf()
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

type Thread struct {
	done chan struct{}
	Ret  Ptr   // pthread_exit's value, or the start routine's
	Res  int32 // thrd_exit's, or a <threads.h> start routine's
	Sig  *SigThread
}

var (
	ThreadsMu sync.Mutex
	Threads   = map[uint64]*Thread{}
	threadN   atomic.Uint64
	ThreadID  = NewTLS(func() Ptr { return New[uint64]() })
)

// spawn starts a goroutine that is a thread, whose id it stores at thr
// (a pthread_t or thrd_t, each an unsigned long), and runs run in it.
func Spawn(thr Ptr, run func(t *Thread)) {
	id := threadN.Add(1)
	t := &Thread{done: make(chan struct{})}
	ThreadsMu.Lock()
	Threads[id] = t
	ThreadsMu.Unlock()
	StoreLong(thr, int64(id)) // pthread_t is unsigned long
	fe := inheritFenv()
	sig := sigInherit()
	t.Sig = sig
	go func() {
		Store64(ThreadID.Get(), id)
		GSelf().fe = fe
		GSelf().Sig = sig
		defer sigExit(sig)
		defer close(t.done)
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
}

type ThreadExit struct {
	Ret Ptr
	Res int32
}

// join waits for thread id to end, and forgets it: nil if there is no
// such thread to join.
func Join(id uint64) *Thread {
	ThreadsMu.Lock()
	t := Threads[id]
	delete(Threads, id)
	ThreadsMu.Unlock()
	if t != nil {
		<-t.done
	}
	return t
}

// Mutexes and condition variables: kept by the address of the C object,
// which never moves (it is shared, so not on a goroutine's stack).

// pthread_mutexattr_settype's types: glibc's and musl's.
const (
	PTHREAD_MUTEX_NORMAL    = 0
	PTHREAD_MUTEX_RECURSIVE = 1
)

// A mutex is a pthread_mutex_t. One of type PTHREAD_MUTEX_RECURSIVE or
// PTHREAD_MUTEX_ERRORCHECK knows its owner, the goroutine that locked it.
type Mutex struct {
	Mu    sync.Mutex
	Kind  int32
	Owner atomic.Pointer[gTable]
	Count int // the owner's locks of a recursive mutex
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

func (m *Mutex) Lock(how int, deadline time.Time) int32 {
	if m.Kind == PTHREAD_MUTEX_NORMAL {
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
	if e := m.acquire(how, deadline); e != 0 {
		return e
	}
	m.Owner.Store(self)
	m.Count = 1
	return 0
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
func LoadDeadline(ts Ptr) (time.Time, bool) {
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
	Destructor Ptr
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
			var d Ptr
			if int(k) < len(Keys) && Keys[k].Live {
				d = Keys[k].Destructor
			}
			KeysMu.Unlock()
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
	a := Check(p, n)
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
		old = *(*T)(Check(expected, unsafe.Sizeof(old)))
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
