// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package msg is sys/msg.h: XSI message queues, which are the
// process's (shared by its threads; other processes do not see them).
package msg

import (
	c23 "internal/c23"
	"os"
	"sync"
	"time"
)

const (
	MSG_NOERROR = 0o10000
	MSG_EXCEPT  = 0o20000

	// Linux's limits: the most bytes in a message, and in a queue.
	msgmax = 8192
	msgmnb = 16384
)

type message struct {
	mtype int64
	data  []byte
}

type queue struct {
	perm                c23.IpcPerm
	msgs                []message
	cbytes, qbytes      int64
	lspid, lrpid        int32
	stime, rtime, ctime int64
	removed             bool
}

func (q *queue) Perm() *c23.IpcPerm { return &q.perm }

var (
	mu      sync.Mutex
	changed c23.Broadcast
	table   c23.IpcTable[*queue]
)

func fail(e int32) int32 {
	c23.SetErrno(e)
	return -1
}

// Msgget is msgget.
func Msgget(key, flg int32) int32 {
	mu.Lock()
	defer mu.Unlock()
	id, e := table.Get(key, flg, func(perm c23.IpcPerm) (*queue, int32) {
		return &queue{perm: perm, qbytes: msgmnb, ctime: time.Now().Unix()}, 0
	}, func(*queue) int32 { return 0 })
	if e != 0 {
		return fail(e)
	}
	return id
}

// lookup returns queue id, which the process may access as want asks;
// errno EINVAL or EACCES if not. mu is held.
func lookup(id, want int32) *queue {
	q, ok := table.Lookup(id)
	if !ok {
		c23.SetErrno(c23.EINVAL)
		return nil
	}
	if !q.perm.Allows(want) {
		c23.SetErrno(c23.EACCES)
		return nil
	}
	return q
}

// wait waits, mu held, for a change to the queues; false if q was
// removed meanwhile (EIDRM).
func wait(q *queue) bool {
	c := changed.C()
	mu.Unlock()
	<-c
	mu.Lock()
	return !q.removed
}

// Msgsnd is msgsnd: the message is the struct { long mtype; char
// mtext[n]; } at p. It waits while the queue is full (its bytes or
// messages would pass msg_qbytes), unless flg has IPC_NOWAIT (EAGAIN).
func Msgsnd(id int32, p *c23.Void, n uint64, flg int32) int32 {
	if n > msgmax || id < 0 {
		return fail(c23.EINVAL)
	}
	mtype := c23.LoadLong(p)
	if mtype < 1 {
		return fail(c23.EINVAL)
	}
	data := append([]byte(nil), c23.BytesAt(c23.Add(p, int(c23.LongSize)), uintptr(n))...)
	mu.Lock()
	defer mu.Unlock()
	q := lookup(id, 0o200)
	if q == nil {
		return -1
	}
	for q.cbytes+int64(n) > q.qbytes || int64(len(q.msgs))+1 > q.qbytes {
		if flg&c23.IPC_NOWAIT != 0 {
			return fail(c23.EAGAIN)
		}
		if !wait(q) {
			return fail(c23.EIDRM)
		}
	}
	q.msgs = append(q.msgs, message{mtype, data})
	q.cbytes += int64(n)
	q.lspid = int32(os.Getpid())
	q.stime = time.Now().Unix()
	changed.Notify()
	return 0
}

// find returns the index of the message msgrcv takes with msgtyp and
// flg, or -1.
func (q *queue) find(msgtyp int64, flg int32) int {
	switch {
	case msgtyp == 0:
		if len(q.msgs) > 0 {
			return 0
		}
	case msgtyp > 0:
		for i, m := range q.msgs {
			if (m.mtype == msgtyp) != (flg&MSG_EXCEPT != 0) {
				return i
			}
		}
	default:
		best := -1
		for i, m := range q.msgs {
			if m.mtype <= -msgtyp && (best < 0 || m.mtype < q.msgs[best].mtype) {
				best = i
			}
		}
		return best
	}
	return -1
}

// Msgrcv is msgrcv: it takes the first message (msgtyp 0), the first of
// type msgtyp (> 0; with MSG_EXCEPT, of another type), or the first of
// the lowest type at most -msgtyp (< 0), into the struct at p, waiting
// for one unless flg has IPC_NOWAIT (ENOMSG). A message longer than n
// bytes is E2BIG, and stays, unless flg has MSG_NOERROR: then it is cut.
func Msgrcv(id int32, p *c23.Void, n uint64, msgtyp int64, flg int32) int64 {
	if id < 0 || int64(n) < 0 {
		return int64(fail(c23.EINVAL))
	}
	mu.Lock()
	q := lookup(id, 0o400)
	if q == nil {
		mu.Unlock()
		return -1
	}
	var i int
	for {
		if i = q.find(msgtyp, flg); i >= 0 {
			if uint64(len(q.msgs[i].data)) > n && flg&MSG_NOERROR == 0 {
				mu.Unlock()
				return int64(fail(c23.E2BIG))
			}
			break
		}
		if flg&c23.IPC_NOWAIT != 0 {
			mu.Unlock()
			return int64(fail(c23.ENOMSG))
		}
		if !wait(q) {
			mu.Unlock()
			return int64(fail(c23.EIDRM))
		}
	}
	m := q.msgs[i]
	q.msgs = append(q.msgs[:i:i], q.msgs[i+1:]...)
	q.cbytes -= int64(len(m.data))
	q.lrpid = int32(os.Getpid())
	q.rtime = time.Now().Unix()
	changed.Notify()
	mu.Unlock()
	data := m.data[:min(uint64(len(m.data)), n)]
	c23.StoreLong(p, m.mtype)
	if len(data) > 0 {
		copy(c23.Writable(c23.Add(p, int(c23.LongSize)), uintptr(len(data))), data)
	}
	return int64(len(data))
}

// struct msqid_ds, Linux's:
//
//	struct ipc_perm msg_perm;				// 0
//	time_t msg_stime, msg_rtime, msg_ctime;		// 48, 56, 64 (40, 48, 56 on ILP32)
//	unsigned long __msg_cbytes;				// 72
//	msgqnum_t msg_qnum; msglen_t msg_qbytes;		// 80, 88
//	pid_t msg_lspid, msg_lrpid;				// 96, 100
//	unsigned long __unused4, __unused5;			// 104, 112 (120 bytes)
var (
	l        = int(c23.LongSize)
	offTimes = (c23.IpcPermSize + 7) &^ 7
	offBytes = offTimes + 24
	offPids  = offBytes + 3*l
	dsSize   = ((offPids+8+l-1)&^(l-1) + 2*l + 7) &^ 7
)

// Msgctl is msgctl: IPC_STAT, IPC_SET (the owner, the permissions and
// msg_qbytes) and IPC_RMID (threads waiting on the queue fail with
// EIDRM).
func Msgctl(id, cmd int32, buf *c23.Void) int32 {
	mu.Lock()
	defer mu.Unlock()
	switch cmd {
	case c23.IPC_STAT:
		q := lookup(id, 0o400)
		if q == nil {
			return -1
		}
		c23.Memset(buf, 0, uint64(dsSize))
		q.perm.Store(buf)
		c23.Store64(c23.Add(buf, offTimes), uint64(q.stime))
		c23.Store64(c23.Add(buf, offTimes+8), uint64(q.rtime))
		c23.Store64(c23.Add(buf, offTimes+16), uint64(q.ctime))
		c23.StoreLong(c23.Add(buf, offBytes), q.cbytes)
		c23.StoreLong(c23.Add(buf, offBytes+l), int64(len(q.msgs)))
		c23.StoreLong(c23.Add(buf, offBytes+2*l), q.qbytes)
		c23.Store32(c23.Add(buf, offPids), uint32(q.lspid))
		c23.Store32(c23.Add(buf, offPids+4), uint32(q.lrpid))
		return 0
	case c23.IPC_SET, c23.IPC_RMID:
		q := lookup(id, 0)
		if q == nil {
			return -1
		}
		if uid := os.Getuid(); uid > 0 && uint32(uid) != q.perm.Uid && uint32(uid) != q.perm.Cuid {
			return fail(c23.EPERM)
		}
		if cmd == c23.IPC_RMID {
			q.removed = true
			table.Remove(id)
			changed.Notify()
			return 0
		}
		qbytes := c23.LoadLong(c23.Add(buf, offBytes+2*l))
		if uint64(qbytes) > msgmnb && os.Getuid() > 0 {
			return fail(c23.EPERM)
		}
		q.perm.Set(buf)
		q.qbytes = qbytes
		q.ctime = time.Now().Unix()
		changed.Notify()
		return 0
	}
	return fail(c23.EINVAL)
}
