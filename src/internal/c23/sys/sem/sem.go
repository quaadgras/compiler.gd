// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package sem is sys/sem.h: XSI semaphore sets, which are the
// process's (shared by its threads; other processes do not see them).
package sem

import (
	c23 "internal/c23"
	"os"
	"sync"
	"time"
)

// semctl's commands and semop's flag, Linux's.
const (
	GETPID  = 11
	GETVAL  = 12
	GETALL  = 13
	GETNCNT = 14
	GETZCNT = 15
	SETVAL  = 16
	SETALL  = 17

	SEM_UNDO = 0x1000

	// Linux's limits: semaphores in a set, operations in a semop, and
	// a semaphore's value.
	semmsl = 32000
	semopm = 500
	semvmx = 32767
)

type semaphore struct {
	val        int32
	pid        int32
	ncnt, zcnt int32
}

type set struct {
	perm         c23.IpcPerm
	sems         []semaphore
	otime, ctime int64
	removed      bool
	// undo is SEM_UNDO's adjustment of each semaphore: what the
	// process's exit adds to it.
	undo []int32
}

func (s *set) Perm() *c23.IpcPerm { return &s.perm }

var (
	mu      sync.Mutex
	changed c23.Broadcast
	table   c23.IpcTable[*set]
	undoReg sync.Once
)

func fail(e int32) int32 {
	c23.SetErrno(e)
	return -1
}

// Semget is semget: a set of nsems semaphores of value 0.
func Semget(key, nsems, flg int32) int32 {
	if nsems < 0 || nsems > semmsl {
		return fail(c23.EINVAL)
	}
	mu.Lock()
	defer mu.Unlock()
	id, e := table.Get(key, flg, func(perm c23.IpcPerm) (*set, int32) {
		if nsems == 0 {
			return nil, c23.EINVAL
		}
		return &set{perm: perm, sems: make([]semaphore, nsems), ctime: time.Now().Unix()}, 0
	}, func(s *set) int32 {
		if int(nsems) > len(s.sems) {
			return c23.EINVAL
		}
		return 0
	})
	if e != 0 {
		return fail(e)
	}
	return id
}

func lookup(id, want int32) *set {
	s, ok := table.Lookup(id)
	if !ok {
		c23.SetErrno(c23.EINVAL)
		return nil
	}
	if !s.perm.Allows(want) {
		c23.SetErrno(c23.EACCES)
		return nil
	}
	return s
}

type sembuf struct {
	num      uint16
	op, flg  int16
	blocking bool
}

// Semtimedop is semtimedop: the nsops struct sembuf { unsigned short
// sem_num; short sem_op, sem_flg; } at sops apply all at once, when
// they all can: a negative sem_op when the value is at least its
// magnitude, a zero one when the value is zero. Until then it waits,
// unless an operation that cannot apply has IPC_NOWAIT (EAGAIN), or
// until the relative timeout at ts, if it is not null (EAGAIN). With
// SEM_UNDO, the process's exit undoes the operation.
func Semtimedop(id int32, sops *c23.Void, nsops uint64, ts *c23.Void) int32 {
	if id < 0 || nsops == 0 {
		return fail(c23.EINVAL)
	}
	if nsops > semopm {
		return fail(c23.E2BIG)
	}
	var deadline time.Time
	if c23.Addr(ts) != 0 {
		d, ok := c23.LoadTimeout(ts)
		if !ok {
			return fail(c23.EINVAL)
		}
		deadline = d
	}
	ops := make([]sembuf, nsops)
	alter, undo := false, false
	for i := range ops {
		p := c23.Add(sops, 6*i)
		ops[i] = sembuf{num: c23.Load16(p), op: int16(c23.Load16(c23.Add(p, 2))), flg: int16(c23.Load16(c23.Add(p, 4)))}
		alter = alter || ops[i].op != 0
		undo = undo || ops[i].flg&SEM_UNDO != 0
	}
	if undo {
		undoReg.Do(func() {
			c23.AtexitMu.Lock()
			c23.AtexitFn = append([]func(){undoAll}, c23.AtexitFn...)
			c23.AtexitMu.Unlock()
		})
	}
	want := int32(0o400)
	if alter {
		want = 0o200
	}
	mu.Lock()
	defer mu.Unlock()
	s := lookup(id, want)
	if s == nil {
		return -1
	}
	for _, op := range ops {
		if int(op.num) >= len(s.sems) {
			return fail(c23.EFBIG)
		}
	}
	for {
		// Try them in order on a copy, as Linux's perform_atomic_semop.
		vals := make([]int32, len(s.sems))
		for i := range s.sems {
			vals[i] = s.sems[i].val
		}
		block := -1
		for i, op := range ops {
			v := vals[op.num]
			switch {
			case op.op == 0 && v != 0, op.op < 0 && v+int32(op.op) < 0:
				block = i
			case v+int32(op.op) > semvmx:
				return fail(c23.ERANGE)
			default:
				vals[op.num] = v + int32(op.op)
				continue
			}
			break
		}
		if block < 0 {
			pid := int32(os.Getpid())
			for _, op := range ops {
				s.sems[op.num].pid = pid
				if op.flg&SEM_UNDO != 0 {
					if s.undo == nil {
						s.undo = make([]int32, len(s.sems))
					}
					s.undo[op.num] -= int32(op.op)
				}
			}
			for i := range s.sems {
				s.sems[i].val = vals[i]
			}
			s.otime = time.Now().Unix()
			changed.Notify()
			return 0
		}
		op := ops[block]
		if op.flg&c23.IPC_NOWAIT != 0 {
			return fail(c23.EAGAIN)
		}
		cnt := &s.sems[op.num].ncnt
		if op.op == 0 {
			cnt = &s.sems[op.num].zcnt
		}
		*cnt++
		c := changed.C()
		mu.Unlock()
		ok := c23.WaitUntil(c, deadline)
		mu.Lock()
		*cnt--
		if s.removed {
			return fail(c23.EIDRM)
		}
		if !ok {
			return fail(c23.EAGAIN)
		}
	}
}

// Semop is semop: semtimedop without a timeout.
func Semop(id int32, sops *c23.Void, nsops uint64) int32 {
	return Semtimedop(id, sops, nsops, (*c23.Void)(nil))
}

// undoAll applies the SEM_UNDO adjustments, as the process's exit does
// (they would matter to other processes, which do not see the sets, and
// to the threads still running while exit runs). Each value is kept
// between 0 and the most a semaphore may hold, as Linux keeps it.
func undoAll() {
	mu.Lock()
	defer mu.Unlock()
	table.Each(func(s *set) {
		for i, a := range s.undo {
			s.sems[i].val = min(max(s.sems[i].val+a, 0), semvmx)
		}
		s.undo = nil
	})
	changed.Notify()
}

// semArg is semctl's fourth argument, a union semun { int val; struct
// semid_ds *buf; unsigned short *array; } passed by value (which arrives
// as a copy of it), or its member itself: the int, or the pointer.
func semArg(va []any, wantPtr bool) (int32, *c23.Void) {
	if len(va) == 0 {
		return 0, (*c23.Void)(nil)
	}
	if u, ok := va[0].(c23.ByValue); ok {
		// The union's copy: it holds the int, or the pointer.
		if !wantPtr {
			return int32(c23.Load32(u.P)), (*c23.Void)(nil)
		}
		if p, ok := c23.UnionValuePtr(u.P); ok {
			return 0, p
		}
		return 0, c23.LoadPtr(u.P)
	}
	if p, ok := va[0].(*c23.Void); ok {
		return int32(c23.Addr(p)), p
	}
	v := c23.ToInt64(va[0])
	return int32(v), c23.FromInt(uintptr(v))
}

// struct semid_ds, Linux's:
//
//	struct ipc_perm sem_perm;				// 0
//	time_t sem_otime; unsigned long __unused1;		// 48, 56 (40, 48 on ILP32)
//	time_t sem_ctime; unsigned long __unused2;		// 64, 72 (56, 64)
//	unsigned long sem_nsems;				// 80 (68)
//	unsigned long __unused3, __unused4;			// 88, 96 (104 bytes)
var (
	l        = int(c23.LongSize)
	offTimes = (c23.IpcPermSize + 7) &^ 7
	offNsems = offTimes + 24 + l
	dsSize   = (offNsems + 3*l + 7) &^ 7
)

// Semctl is semctl: the commands of POSIX (GETVAL, SETVAL, GETPID,
// GETNCNT, GETZCNT, GETALL, SETALL, IPC_STAT, IPC_SET and IPC_RMID),
// whose argument is the union semun after cmd. Setting a value clears
// its SEM_UNDO adjustment, as Linux does.
func Semctl(id, num, cmd int32, va ...any) int32 {
	mu.Lock()
	defer mu.Unlock()
	switch cmd {
	case c23.IPC_RMID, c23.IPC_SET:
		s := lookup(id, 0)
		if s == nil {
			return -1
		}
		if uid := os.Getuid(); uid > 0 && uint32(uid) != s.perm.Uid && uint32(uid) != s.perm.Cuid {
			return fail(c23.EPERM)
		}
		if cmd == c23.IPC_RMID {
			s.removed = true
			table.Remove(id)
			changed.Notify()
			return 0
		}
		_, buf := semArg(va, true)
		s.perm.Set(buf)
		s.ctime = time.Now().Unix()
		return 0
	case c23.IPC_STAT:
		s := lookup(id, 0o400)
		if s == nil {
			return -1
		}
		_, buf := semArg(va, true)
		c23.Memset(buf, 0, uint64(dsSize))
		s.perm.Store(buf)
		c23.Store64(c23.Add(buf, offTimes), uint64(s.otime))
		c23.Store64(c23.Add(buf, offTimes+16), uint64(s.ctime))
		c23.StoreLong(c23.Add(buf, offNsems), int64(len(s.sems)))
		return 0
	case GETALL:
		s := lookup(id, 0o400)
		if s == nil {
			return -1
		}
		_, arr := semArg(va, true)
		for i, m := range s.sems {
			c23.Store16(c23.Add(arr, 2*i), uint16(m.val))
		}
		return 0
	case SETALL:
		s := lookup(id, 0o200)
		if s == nil {
			return -1
		}
		_, arr := semArg(va, true)
		vals := make([]uint16, len(s.sems))
		for i := range vals {
			if vals[i] = c23.Load16(c23.Add(arr, 2*i)); vals[i] > semvmx {
				return fail(c23.ERANGE)
			}
		}
		pid := int32(os.Getpid())
		for i := range s.sems {
			s.sems[i].val, s.sems[i].pid = int32(vals[i]), pid
		}
		s.undo = nil
		s.ctime = time.Now().Unix()
		changed.Notify()
		return 0
	case GETVAL, GETPID, GETNCNT, GETZCNT, SETVAL:
		want := int32(0o400)
		if cmd == SETVAL {
			want = 0o200
		}
		s := lookup(id, want)
		if s == nil {
			return -1
		}
		if num < 0 || int(num) >= len(s.sems) {
			return fail(c23.EINVAL)
		}
		m := &s.sems[num]
		switch cmd {
		case GETVAL:
			return m.val
		case GETPID:
			return m.pid
		case GETNCNT:
			return m.ncnt
		case GETZCNT:
			return m.zcnt
		}
		v, _ := semArg(va, false)
		if v < 0 || v > semvmx {
			return fail(c23.ERANGE)
		}
		m.val, m.pid = v, int32(os.Getpid())
		if s.undo != nil {
			s.undo[num] = 0
		}
		s.ctime = time.Now().Unix()
		changed.Notify()
		return 0
	}
	return fail(c23.EINVAL)
}
