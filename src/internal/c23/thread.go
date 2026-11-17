// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"runtime"
	"sync"
	"sync/atomic"
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
	ret  Ptr
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
	id := threadN.Add(1)
	t := &thread{done: make(chan struct{})}
	threadsMu.Lock()
	threads[id] = t
	threadsMu.Unlock()
	storeLong(thr, int64(id)) // pthread_t is unsigned long
	go func() {
		Store64(threadID.Get(), id)
		defer close(t.done)
		defer runKeyDestructors()
		defer func() {
			if r := recover(); r != nil {
				if e, ok := r.(threadExit); ok {
					t.ret = e.ret
					return
				}
				panic(r)
			}
		}()
		t.ret = f(arg)
	}()
	return 0
}

type threadExit struct{ ret Ptr }

// PthreadExit is pthread_exit.
func PthreadExit(ret Ptr) { panic(threadExit{ret}) }

// PthreadJoin is pthread_join.
func PthreadJoin(id uint64, ret Ptr) int32 {
	threadsMu.Lock()
	t := threads[id]
	delete(threads, id)
	threadsMu.Unlock()
	if t == nil {
		return 3 // ESRCH
	}
	<-t.done
	if Addr(ret) != 0 {
		StorePtr(ret, t.ret)
	}
	return 0
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
	conds   sync.Map // uintptr -> *sync.Cond
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

func PthreadMutexLock(m Ptr) int32 { return mutexOf(m).lock(false) }

func PthreadMutexTrylock(m Ptr) int32 { return mutexOf(m).lock(true) }

func (m *mutex) lock(try bool) int32 {
	if m.kind == PTHREAD_MUTEX_NORMAL {
		if try && !m.mu.TryLock() {
			return EBUSY
		} else if !try {
			m.mu.Lock()
		}
		return 0
	}
	self := gSelf()
	if m.owner.Load() == self {
		if m.kind == PTHREAD_MUTEX_RECURSIVE {
			m.count++
			return 0
		}
		return If[int32](try, EBUSY, EDEADLK)
	}
	if try && !m.mu.TryLock() {
		return EBUSY
	} else if !try {
		m.mu.Lock()
	}
	m.owner.Store(self)
	m.count = 1
	return 0
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

func condOf(c, m Ptr) *sync.Cond {
	a := Addr(c)
	if v, ok := conds.Load(a); ok {
		return v.(*sync.Cond)
	}
	v, _ := conds.LoadOrStore(a, sync.NewCond(&mutexOf(m).mu))
	return v.(*sync.Cond)
}

func PthreadCondInit(c, attr Ptr) int32 { return 0 }
func PthreadCondDestroy(c Ptr) int32    { return 0 }

// PthreadCondWait is pthread_cond_wait, which unlocks a recursive mutex
// however many times its owner locked it, and locks it as many again.
func PthreadCondWait(c, m Ptr) int32 {
	mu := mutexOf(m)
	if mu.kind == PTHREAD_MUTEX_NORMAL {
		condOf(c, m).Wait()
		return 0
	}
	self := gSelf()
	if mu.owner.Load() != self {
		return EPERM
	}
	n := mu.count
	mu.owner.Store(nil)
	condOf(c, m).Wait()
	mu.owner.Store(self)
	mu.count = n
	return 0
}

func PthreadCondSignal(c Ptr) int32 {
	if v, ok := conds.Load(Addr(c)); ok {
		v.(*sync.Cond).Signal()
	}
	return 0
}
func PthreadCondBroadcast(c Ptr) int32 {
	if v, ok := conds.Load(Addr(c)); ok {
		v.(*sync.Cond).Broadcast()
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
