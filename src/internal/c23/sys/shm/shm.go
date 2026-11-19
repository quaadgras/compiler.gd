// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package shm is sys/shm.h: XSI shared memory segments, which are the
// process's. C's memory is Go's heap, which no other process can map, so
// a segment is one C object of its size, and every shmat of it returns a
// pointer to that object: the program's threads share it, other
// processes do not see it.
package shm

import (
	c23 "internal/c23"
	"os"
	"sync"
	"time"
)

const (
	SHM_RDONLY = 0o10000
	SHM_RND    = 0o20000
	SHM_REMAP  = 0o40000
	SHM_EXEC   = 0o100000
	SHM_LOCK   = 11
	SHM_UNLOCK = 12
	SHM_DEST   = 0o1000 // in shm_perm.mode: removed, destroyed at the last detach

	shmmax = 1<<63 - 1
)

type segment struct {
	perm                c23.IpcPerm
	p                   c23.Ptr
	size                uint64
	nattch              int64
	cpid, lpid          int32
	atime, dtime, ctime int64
}

func (s *segment) Perm() *c23.IpcPerm { return &s.perm }

var (
	mu    sync.Mutex
	table c23.IpcTable[*segment]
)

func fail(e int32) int32 {
	c23.SetErrno(e)
	return -1
}

// Shmget is shmget: a segment of size bytes, zeroed.
func Shmget(key int32, size uint64, flg int32) int32 {
	mu.Lock()
	defer mu.Unlock()
	id, e := table.Get(key, flg, func(perm c23.IpcPerm) (*segment, int32) {
		if size < 1 || size > shmmax {
			return nil, c23.EINVAL
		}
		return &segment{perm: perm, size: size, cpid: int32(os.Getpid()), ctime: time.Now().Unix()}, 0
	}, func(s *segment) int32 {
		if size > s.size {
			return c23.EINVAL
		}
		return 0
	})
	if e != 0 {
		return fail(e)
	}
	return id
}

func lookup(id, want int32) *segment {
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

// shmFailed is shmat's (void *)-1.
var shmFailed = c23.FromInt(^uintptr(0))

// Shmat is shmat: the segment's object, made at the first attach. The
// segment cannot be placed at an address (a shmaddr that is not null is
// EINVAL, but for the segment's own address), and SHM_RDONLY attaches it
// for writing too.
func Shmat(id int32, addr c23.Ptr, flg int32) c23.Ptr {
	mu.Lock()
	defer mu.Unlock()
	want := int32(0o600)
	if flg&SHM_RDONLY != 0 {
		want = 0o400
	}
	s := lookup(id, want)
	if s == nil {
		return shmFailed
	}
	if s.p.Obj == nil {
		s.p = c23.Malloc(s.size)
	}
	if c23.Addr(addr) != 0 && c23.Addr(addr) != c23.Addr(s.p) {
		c23.SetErrno(c23.EINVAL)
		return shmFailed
	}
	s.nattch++
	s.lpid = int32(os.Getpid())
	s.atime = time.Now().Unix()
	return s.p
}

// Shmdt is shmdt: one attach of the segment at addr ends; a removed
// segment goes with its last.
func Shmdt(addr c23.Ptr) int32 {
	mu.Lock()
	defer mu.Unlock()
	var found *segment
	var foundID int32
	table.EachID(func(id int32, s *segment) {
		if found == nil && s.nattch > 0 && s.p.Obj != nil && c23.Addr(s.p) == c23.Addr(addr) {
			found, foundID = s, id
		}
	})
	if found == nil {
		return fail(c23.EINVAL)
	}
	found.nattch--
	found.lpid = int32(os.Getpid())
	found.dtime = time.Now().Unix()
	if found.nattch == 0 && found.perm.Mode&SHM_DEST != 0 {
		table.Remove(foundID)
	}
	return 0
}

// struct shmid_ds, Linux's:
//
//	struct ipc_perm shm_perm;				// 0
//	size_t shm_segsz;					// 48 (36 on ILP32)
//	time_t shm_atime, shm_dtime, shm_ctime;		// 56, 64, 72 (40, 48, 56)
//	pid_t shm_cpid, shm_lpid;				// 80, 84 (64, 68)
//	shmatt_t shm_nattch;					// 88 (72)
//	unsigned long __unused5, __unused6;			// 96, 104 (112 bytes)
var (
	l        = int(c23.LongSize)
	offTimes = (c23.IpcPermSize + l + 7) &^ 7
	offPids  = offTimes + 24
	dsSize   = (offPids + 8 + 3*l + 7) &^ 7
)

// Shmctl is shmctl: IPC_STAT, IPC_SET, IPC_RMID (the segment goes at
// its last detach, and its key names it no more), and SHM_LOCK and
// SHM_UNLOCK, which do nothing.
func Shmctl(id, cmd int32, buf c23.Ptr) int32 {
	mu.Lock()
	defer mu.Unlock()
	switch cmd {
	case c23.IPC_STAT:
		s := lookup(id, 0o400)
		if s == nil {
			return -1
		}
		c23.Memset(buf, 0, uint64(dsSize))
		s.perm.Store(buf)
		c23.StoreLong(c23.Add(buf, c23.IpcPermSize), int64(s.size))
		c23.Store64(c23.Add(buf, offTimes), uint64(s.atime))
		c23.Store64(c23.Add(buf, offTimes+8), uint64(s.dtime))
		c23.Store64(c23.Add(buf, offTimes+16), uint64(s.ctime))
		c23.Store32(c23.Add(buf, offPids), uint32(s.cpid))
		c23.Store32(c23.Add(buf, offPids+4), uint32(s.lpid))
		c23.StoreLong(c23.Add(buf, offPids+8), s.nattch)
		return 0
	case c23.IPC_SET, c23.IPC_RMID, SHM_LOCK, SHM_UNLOCK:
		s := lookup(id, 0)
		if s == nil {
			return -1
		}
		if uid := os.Getuid(); uid > 0 && uint32(uid) != s.perm.Uid && uint32(uid) != s.perm.Cuid {
			return fail(c23.EPERM)
		}
		switch cmd {
		case c23.IPC_SET:
			s.perm.Set(buf)
			s.ctime = time.Now().Unix()
		case c23.IPC_RMID:
			s.perm.Mode |= SHM_DEST
			if s.nattch == 0 {
				table.Remove(id)
			} else {
				table.UnlinkKey(id)
			}
		}
		return 0
	}
	return fail(c23.EINVAL)
}
