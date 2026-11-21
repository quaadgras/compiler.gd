// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"os"
	"time"
)

// What the headers of interprocess communication share: XSI IPC's
// permissions and tables of identifiers (sys/msg.h, sys/sem.h and
// sys/shm.h), and struct sigevent's notifications (mqueue.h and aio.h).
//
// The objects are the process's: C's memory is Go's heap, which no other
// process can map, so a message queue, a semaphore set or a shared
// memory segment is shared by the program's threads only. The processes
// the program spawns do not see them.

// sys/ipc.h's flags and commands, Linux's.
const (
	IPC_CREAT  = 0o1000
	IPC_EXCL   = 0o2000
	IPC_NOWAIT = 0o4000
	IPC_RMID   = 0
	IPC_SET    = 1
	IPC_STAT   = 2
)

// An IpcPerm is a struct ipc_perm.
type IpcPerm struct {
	Key                  int32
	Uid, Gid, Cuid, Cgid uint32
	Mode                 uint32
	Seq                  uint16
}

// IpcPermSize is sizeof(struct ipc_perm), Linux's ipc64_perm:
//
//	key_t __key; uid_t uid; gid_t gid; uid_t cuid; gid_t cgid;	// 0, 4, 8, 12, 16
//	mode_t mode; unsigned short __seq, __pad2;			// 20, 24, 26
//	unsigned long __unused1, __unused2;				// 32 (28 on ILP32)
const IpcPermSize = (28+int(LongSize)-1)&^(int(LongSize)-1) + 2*int(LongSize)

// NewIpcPerm is the ipc_perm of an object made now with key and the
// permissions of flg.
func NewIpcPerm(key, flg int32) IpcPerm {
	uid, gid := uint32(os.Getuid()), uint32(os.Getgid())
	return IpcPerm{Key: key, Uid: uid, Gid: gid, Cuid: uid, Cgid: gid, Mode: uint32(flg) & 0o777}
}

// Store stores p as struct ipc_perm at q.
func (p *IpcPerm) Store(q Ptr) {
	Memset(q, 0, uint64(IpcPermSize))
	Store32(q, uint32(p.Key))
	Store32(Add(q, 4), p.Uid)
	Store32(Add(q, 8), p.Gid)
	Store32(Add(q, 12), p.Cuid)
	Store32(Add(q, 16), p.Cgid)
	Store32(Add(q, 20), p.Mode)
	Store16(Add(q, 24), p.Seq)
}

// Set is IPC_SET's: the owner and the permissions of the struct
// ipc_perm at q.
func (p *IpcPerm) Set(q Ptr) {
	p.Uid = Load32(Add(q, 4))
	p.Gid = Load32(Add(q, 8))
	p.Mode = p.Mode&^0o777 | Load32(Add(q, 20))&0o777
}

// Allows reports whether the calling process may access an object of p
// as want asks (read 4, write 2, as the owner's bits of a mode, or the
// mode bits flg of a get): Linux's ipcperms, for the process's user and
// group. The superuser, and a system without users, may do anything.
func (p *IpcPerm) Allows(want int32) bool {
	uid, gid := os.Getuid(), os.Getgid()
	if uid <= 0 {
		return true
	}
	req := uint32(want>>6|want>>3|want) & 7
	granted := p.Mode
	switch {
	case uint32(uid) == p.Uid || uint32(uid) == p.Cuid:
		granted >>= 6
	case uint32(gid) == p.Gid || uint32(gid) == p.Cgid:
		granted >>= 3
	}
	return req&^granted&7 == 0
}

// An IpcObj is a message queue, a semaphore set or a shared memory
// segment, in an IpcTable.
type IpcObj interface {
	Perm() *IpcPerm
}

// An IpcTable is the identifiers of one kind of object: Linux's, the
// index of the object in the table plus 32768 times a sequence number
// that each creation increments. Its user locks it.
type IpcTable[T IpcObj] struct {
	objs map[int32]T     // by identifier
	keys map[int32]int32 // key -> identifier
	used map[int32]bool  // indexes
	seq  int32
}

// Get is msgget's, semget's and shmget's lookup of key: the identifier
// of its object, made by make if flg has IPC_CREAT and there is none (or
// key is IPC_PRIVATE); or -1 and an error number. fits checks an object
// that exists (0 if it may be used).
func (t *IpcTable[T]) Get(key, flg int32, make func(perm IpcPerm) (T, int32), fits func(T) int32) (int32, int32) {
	if t.objs == nil {
		t.objs, t.keys, t.used = map[int32]T{}, map[int32]int32{}, map[int32]bool{}
	}
	if key != 0 {
		if id, ok := t.keys[key]; ok {
			o := t.objs[id]
			if flg&(IPC_CREAT|IPC_EXCL) == IPC_CREAT|IPC_EXCL {
				return -1, EEXIST
			}
			if !o.Perm().Allows(flg & 0o777) {
				return -1, EACCES
			}
			if e := fits(o); e != 0 {
				return -1, e
			}
			return id, 0
		}
		if flg&IPC_CREAT == 0 {
			return -1, ENOENT
		}
	}
	idx := int32(0)
	for t.used[idx] {
		idx++
	}
	if idx >= 32768 {
		return -1, ENOSPC
	}
	perm := NewIpcPerm(key, flg)
	perm.Seq = uint16(t.seq)
	o, e := make(perm)
	if e != 0 {
		return -1, e
	}
	id := t.seq<<15 + idx
	t.seq = (t.seq + 1) & 0xffff
	t.used[idx] = true
	t.objs[id] = o
	if key != 0 {
		t.keys[key] = id
	}
	return id, 0
}

// Lookup returns the object of identifier id.
func (t *IpcTable[T]) Lookup(id int32) (T, bool) {
	o, ok := t.objs[id]
	return o, ok
}

// Each calls f with each object.
func (t *IpcTable[T]) Each(f func(T)) {
	for _, o := range t.objs {
		f(o)
	}
}

// EachID calls f with each object and its identifier.
func (t *IpcTable[T]) EachID(f func(int32, T)) {
	for id, o := range t.objs {
		f(id, o)
	}
}

// Remove removes the object of identifier id from the table: its key
// names no object any more (IPC_RMID).
func (t *IpcTable[T]) Remove(id int32) {
	o, ok := t.objs[id]
	if !ok {
		return
	}
	if k := o.Perm().Key; k != 0 && t.keys[k] == id {
		delete(t.keys, k)
	}
	delete(t.objs, id)
	delete(t.used, id&0x7fff)
}

// UnlinkKey makes the object of identifier id private: its key names no
// object, though the object stays (shmctl's IPC_RMID of an attached
// segment).
func (t *IpcTable[T]) UnlinkKey(id int32) {
	if o, ok := t.objs[id]; ok {
		if k := o.Perm().Key; k != 0 && t.keys[k] == id {
			delete(t.keys, k)
		}
		o.Perm().Key = 0
	}
}

// A Broadcast announces changes to the goroutines waiting for one: C
// returns a channel that the next Notify closes. Its user locks it.
type Broadcast struct {
	ch chan struct{}
}

func (b *Broadcast) C() <-chan struct{} {
	if b.ch == nil {
		b.ch = make(chan struct{})
	}
	return b.ch
}

func (b *Broadcast) Notify() {
	if b.ch != nil {
		close(b.ch)
		b.ch = nil
	}
}

// LoadTimeout reads the struct timespec at ts, a relative timeout, as a
// deadline from now; false if it is not a valid one.
func LoadTimeout(ts Ptr) (time.Time, bool) {
	sec, nsec := int64(Load64(ts)), LoadLong(Add(ts, 8))
	if sec < 0 || nsec < 0 || nsec >= 1e9 {
		return time.Time{}, false
	}
	d := time.Duration(nsec)
	if sec >= (1<<63-1)/int64(time.Second) {
		d = 1<<63 - 1
	} else {
		d += time.Duration(sec) * time.Second
	}
	return time.Now().Add(max(d, 1)), true
}

// struct sigevent's notifications, glibc's and musl's values.
const (
	SIGEV_SIGNAL    = 0
	SIGEV_NONE      = 1
	SIGEV_THREAD    = 2
	SIGEV_THREAD_ID = 4

	SI_MESGQ   = -3
	SI_ASYNCIO = -4
)

// A SigEvent is a struct sigevent:
//
//	union sigval sigev_value; int sigev_signo; int sigev_notify;	// 0, 8, 12 (0, 4, 8 on wasm32)
//	void (*sigev_notify_function)(union sigval);			// 16 (12)
//	pthread_attr_t *sigev_notify_attributes;			// 24 (16)
//
// in 64 bytes.
type SigEvent struct {
	Value    Ptr
	Signo    int32
	Notify   int32
	Function Ptr
}

// SigEventSize is sizeof(struct sigevent).
const SigEventSize = 64

// LoadSigEvent reads the struct sigevent at p.
func LoadSigEvent(p Ptr) SigEvent {
	fn := (int(WordSize) + 8 + int(WordSize) - 1) &^ (int(WordSize) - 1)
	return SigEvent{
		Value:    LoadPtr(p),
		Signo:    int32(Load32(Add(p, int(WordSize)))),
		Notify:   int32(Load32(Add(p, int(WordSize)+4))),
		Function: LoadPtr(Add(p, fn)),
	}
}

// Valid reports whether ev is a notification mq_notify and aio.h may
// take: SIGEV_NONE, SIGEV_SIGNAL of a signal, or SIGEV_THREAD of a
// function (SIGEV_THREAD_ID is Linux's, for its timers).
func (ev *SigEvent) Valid() bool {
	switch ev.Notify {
	case SIGEV_NONE:
		return true
	case SIGEV_SIGNAL:
		return ev.Signo > 0 && ev.Signo < Nsig
	case SIGEV_THREAD:
		return Addr(ev.Function) != 0
	}
	return false
}

// Fire notifies as ev says: a signal with si_code code and ev's value,
// which self sends if it is the program's thread (the thread runs the
// handler at once if it does not block the signal, as the kernel
// delivers a process's signal to the thread that caused it), or ev's
// function, given ev's value, on a new detached thread that blocks the
// signals, as glibc's and musl's (the thread's attributes are ignored).
// As glibc, a signal that is not one, or SIGEV_THREAD without a
// function, notifies nothing.
func (ev *SigEvent) Fire(code int32, self *SigThread) {
	switch ev.Notify {
	case SIGEV_SIGNAL:
		if ev.Signo > 0 && ev.Signo < Nsig {
			SigSendProcess(ev.Signo, SigInfo{Code: code, Pid: int32(os.Getpid()), Uid: int32(os.Getuid()), Value: ev.Value}, self)
		}
	case SIGEV_THREAD:
		if Addr(ev.Function) == 0 {
			return
		}
		fn, val := ev.Function, ev.Value
		thr := New[uint64]()
		SpawnBlocked(thr, func(t *Thread) {
			ThreadsMu.Lock()
			delete(Threads, uint64(LoadLong(thr)))
			ThreadsMu.Unlock()
			v := Malloc(uint64(WordSize)) // the union sigval, by value
			StorePtr(v, val)
			FuncOf[func(Ptr)](fn)(v)
		})
	}
}
