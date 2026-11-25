// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package mqueue is mqueue.h: POSIX message queues, which are the
// process's, by name, as named semaphores are. A message queue
// descriptor (mqd_t) is a descriptor of internal/c23's table, a
// c23.Special that poll, select and epoll see: readable when a message
// waits, writable when the queue is not full. Other processes do not see
// the queues.
package mqueue

import (
	c23 "internal/c23"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	MQ_PRIO_MAX = 32768

	// Linux's defaults, and its hard limits (what a privileged
	// process may ask for).
	defaultMaxmsg  = 10
	defaultMsgsize = 8192
	hardMaxmsg     = 65536
	hardMsgsize    = 16 << 20
)

type message struct {
	data []byte
	prio uint32
}

// A queue is a message queue: its messages, by priority (the highest
// first) and in order within one.
type queue struct {
	mode    uint32
	maxmsg  int64
	msgsize int64
	msgs    []message

	// notify is mq_notify's registration, if there is one.
	notify    *c23.SigEvent
	receivers int // the threads waiting in mq_receive

	arrivals uint64
}

// mu locks every queue, and changed announces what may let a send or a
// receive go on.
var (
	mu      sync.Mutex
	changed c23.Broadcast
	queues  = map[string]*queue{}
)

// An mqd is an open message queue: what a descriptor names.
type mqd struct {
	q   *queue
	acc int32 // O_RDONLY, O_WRONLY or O_RDWR
}

// Read and Write are not message queues' (on Linux, a read gives a line
// of the queue's state): EINVAL.
func (d *mqd) Read(b []byte, nonblock bool) (int, error) {
	return 0, c23.ErrnoError(c23.EINVAL)
}

func (d *mqd) Write(b []byte, nonblock bool) (int, error) {
	return 0, c23.ErrnoError(c23.EINVAL)
}

// Close is the last close of the descriptor: as Linux, a notification
// the process registered goes (every registration is the process's).
func (d *mqd) Close() error {
	mu.Lock()
	d.q.notify = nil
	mu.Unlock()
	return nil
}

// Ready is Linux's: readable when a message waits, writable when the
// queue is not full, whatever the descriptor's access.
func (d *mqd) Ready() int16 {
	mu.Lock()
	defer mu.Unlock()
	var r int16
	if len(d.q.msgs) > 0 {
		r |= c23.POLLIN
	}
	if int64(len(d.q.msgs)) < d.q.maxmsg {
		r |= c23.POLLOUT
	}
	return r
}

func (d *mqd) Mode() uint32 { return 0o100000 | d.q.mode }

func (d *mqd) Arrivals() uint64 {
	mu.Lock()
	defer mu.Unlock()
	return d.q.arrivals
}

// mqdOf returns the open queue of descriptor fd, and whether it is in
// non-blocking mode; nil, with errno EBADF, if it is not one.
func mqdOf(fd int32) (*mqd, bool) {
	sp, nonblock := c23.FdSpecial(fd, c23.EBADF)
	d, ok := sp.(*mqd)
	if !ok {
		c23.SetErrno(c23.EBADF)
		return nil, false
	}
	return d, nonblock
}

// queueName is the name of a queue, as glibc and Linux take it: it must
// begin with a slash (EINVAL), and what follows must be a file's name
// (EACCES), of at most NAME_MAX bytes (ENAMETOOLONG).
func queueName(name *c23.Void) (string, int32) {
	n := c23.GoString(name)
	if !strings.HasPrefix(n, "/") {
		return "", c23.EINVAL
	}
	n = n[1:]
	switch {
	case len(n) > 255:
		return "", c23.ENAMETOOLONG
	case n == "" || n == "." || n == ".." || strings.Contains(n, "/"):
		return "", c23.EACCES
	}
	return n, 0
}

// MqOpen is mq_open: with O_CREAT, the mode and a struct mq_attr (null
// for Linux's defaults, 10 messages of 8192 bytes) are its third and
// fourth arguments. The descriptor is close-on-exec, as Linux's.
// Permissions are checked as Linux checks them for the process's user.
func MqOpen(name *c23.Void, oflag int32, va ...any) int32 {
	n, e := queueName(name)
	if e != 0 {
		c23.SetErrno(e)
		return -1
	}
	acc := oflag & c23.O_ACCMODE
	if acc == c23.O_ACCMODE {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	mu.Lock()
	q := queues[n]
	switch {
	case q != nil && oflag&(c23.O_CREAT|c23.O_EXCL) == c23.O_CREAT|c23.O_EXCL:
		mu.Unlock()
		c23.SetErrno(c23.EEXIST)
		return -1
	case q != nil:
		want := [...]int32{0o400, 0o200, 0o600}[acc]
		perm := c23.IpcPerm{Uid: uint32(os.Getuid()), Gid: uint32(os.Getgid()), Mode: q.mode}
		if !perm.Allows(want) {
			mu.Unlock()
			c23.SetErrno(c23.EACCES)
			return -1
		}
	case oflag&c23.O_CREAT == 0:
		mu.Unlock()
		c23.SetErrno(c23.ENOENT)
		return -1
	default:
		q = &queue{maxmsg: defaultMaxmsg, msgsize: defaultMsgsize}
		if len(va) > 0 {
			q.mode = uint32(c23.ToInt64(va[0])) & 0o777
		}
		if len(va) > 1 {
			if attr, ok := va[1].(*c23.Void); ok && c23.Addr(attr) != 0 {
				q.maxmsg = c23.LoadLong(c23.Add(attr, int(c23.LongSize)))
				q.msgsize = c23.LoadLong(c23.Add(attr, 2*int(c23.LongSize)))
				if q.maxmsg <= 0 || q.msgsize <= 0 || q.maxmsg > hardMaxmsg || q.msgsize > hardMsgsize {
					mu.Unlock()
					c23.SetErrno(c23.EINVAL)
					return -1
				}
			}
		}
		queues[n] = q
	}
	mu.Unlock()
	return c23.FdNewSpecial(&mqd{q: q, acc: acc}, oflag&c23.O_NONBLOCK, true)
}

// MqClose is mq_close: close of the descriptor.
func MqClose(fd int32) int32 {
	d, _ := mqdOf(fd)
	if d == nil {
		return -1
	}
	mu.Lock()
	d.q.notify = nil
	mu.Unlock()
	return c23.ErrRet(c23.FdClose(fd))
}

// MqUnlink is mq_unlink: the name goes, and the queue with its last
// descriptor.
func MqUnlink(name *c23.Void) int32 {
	n, e := queueName(name)
	if e != 0 {
		c23.SetErrno(e)
		return -1
	}
	mu.Lock()
	defer mu.Unlock()
	if queues[n] == nil {
		c23.SetErrno(c23.ENOENT)
		return -1
	}
	delete(queues, n)
	return 0
}

// wait waits, mu held, for changed or the deadline (if not zero), and
// reports whether the deadline came first.
func wait(deadline time.Time) bool {
	c := changed.C()
	mu.Unlock()
	ok := c23.WaitUntil(c, deadline)
	mu.Lock()
	return !ok
}

// loadDeadline reads mq_timedsend's and mq_timedreceive's absolute
// CLOCK_REALTIME timeout, if ts is not null: as Linux, an invalid one is
// EINVAL whether or not the call would wait.
func loadDeadline(ts *c23.Void) (time.Time, int32) {
	if c23.Addr(ts) == 0 {
		return time.Time{}, 0
	}
	d, ok := c23.LoadDeadline(ts)
	if !ok || d.Unix() < 0 {
		return time.Time{}, c23.EINVAL
	}
	return d, 0
}

// MqTimedsend is mq_timedsend: the message goes after those of its
// priority and before those of lower ones, waiting while the queue is
// full, unless the descriptor is non-blocking (EAGAIN), until the
// absolute time at ts (ETIMEDOUT).
func MqTimedsend(fd int32, msg *c23.Void, n uint64, prio uint32, ts *c23.Void) int32 {
	d, nonblock := mqdOf(fd)
	if d == nil {
		return -1
	}
	deadline, e := loadDeadline(ts)
	switch {
	case e != 0:
	case d.acc == c23.O_RDONLY:
		e = c23.EBADF
	case prio >= MQ_PRIO_MAX:
		e = c23.EINVAL
	case n > uint64(d.q.msgsize):
		e = c23.EMSGSIZE
	}
	if e != 0 {
		c23.SetErrno(e)
		return -1
	}
	data := append([]byte(nil), c23.BytesAt(msg, uintptr(n))...)
	mu.Lock()
	q := d.q
	for int64(len(q.msgs)) >= q.maxmsg {
		if nonblock {
			mu.Unlock()
			c23.SetErrno(c23.EAGAIN)
			return -1
		}
		if wait(deadline) {
			mu.Unlock()
			c23.SetErrno(c23.ETIMEDOUT)
			return -1
		}
	}
	i := len(q.msgs)
	for i > 0 && q.msgs[i-1].prio < prio {
		i--
	}
	q.msgs = append(q.msgs, message{})
	copy(q.msgs[i+1:], q.msgs[i:])
	q.msgs[i] = message{data: data, prio: prio}
	q.arrivals++
	changed.Notify()
	// As Linux: a message arriving at an empty queue that no thread
	// waits to receive notifies the registered process, whose
	// registration then goes.
	var ev *c23.SigEvent
	if len(q.msgs) == 1 && q.receivers == 0 && q.notify != nil {
		ev, q.notify = q.notify, nil
	}
	mu.Unlock()
	c23.PollNotify()
	if ev != nil {
		ev.Fire(c23.SI_MESGQ, c23.SigSelf())
	}
	return 0
}

func MqSend(fd int32, msg *c23.Void, n uint64, prio uint32) int32 {
	return MqTimedsend(fd, msg, n, prio, (*c23.Void)(nil))
}

// MqTimedreceive is mq_timedreceive: it takes the oldest message of the
// highest priority, storing its priority at prio (if not null), waiting
// while there is none as mq_timedsend waits.
func MqTimedreceive(fd int32, msg *c23.Void, n uint64, prio, ts *c23.Void) int64 {
	d, nonblock := mqdOf(fd)
	if d == nil {
		return -1
	}
	deadline, e := loadDeadline(ts)
	switch {
	case e != 0:
	case d.acc == c23.O_WRONLY:
		e = c23.EBADF
	case n < uint64(d.q.msgsize):
		e = c23.EMSGSIZE
	}
	if e != 0 {
		c23.SetErrno(e)
		return -1
	}
	mu.Lock()
	q := d.q
	for len(q.msgs) == 0 {
		if nonblock {
			mu.Unlock()
			c23.SetErrno(c23.EAGAIN)
			return -1
		}
		q.receivers++
		timedOut := wait(deadline)
		q.receivers--
		if timedOut {
			mu.Unlock()
			c23.SetErrno(c23.ETIMEDOUT)
			return -1
		}
	}
	m := q.msgs[0]
	q.msgs[0] = message{}
	q.msgs = q.msgs[1:]
	changed.Notify()
	mu.Unlock()
	c23.PollNotify()
	if len(m.data) > 0 {
		copy(c23.Writable(msg, uintptr(len(m.data))), m.data)
	}
	if c23.Addr(prio) != 0 {
		c23.Store32(prio, m.prio)
	}
	return int64(len(m.data))
}

func MqReceive(fd int32, msg *c23.Void, n uint64, prio *c23.Void) int64 {
	return MqTimedreceive(fd, msg, n, prio, (*c23.Void)(nil))
}

// MqNotify is mq_notify: the process registers for a notification
// (struct sigevent at sev) of the next message to arrive at the empty
// queue, or (sev null) removes its registration. Only one process may
// register (EBUSY), and every registration here is the process's.
func MqNotify(fd int32, sev *c23.Void) int32 {
	d, _ := mqdOf(fd)
	if d == nil {
		return -1
	}
	if c23.Addr(sev) == 0 {
		mu.Lock()
		d.q.notify = nil
		mu.Unlock()
		return 0
	}
	ev := c23.LoadSigEvent(sev)
	if !ev.Valid() {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	mu.Lock()
	defer mu.Unlock()
	if d.q.notify != nil {
		c23.SetErrno(c23.EBUSY)
		return -1
	}
	d.q.notify = &ev
	return 0
}

// storeAttr stores the queue's attributes as struct mq_attr at p:
// long mq_flags, mq_maxmsg, mq_msgsize, mq_curmsgs, __pad[4]. mu is
// held.
func storeAttr(p *c23.Void, q *queue, nonblock bool) {
	l := int(c23.LongSize)
	c23.Memset(p, 0, uint64(8*l))
	if nonblock {
		c23.StoreLong(p, c23.O_NONBLOCK)
	}
	c23.StoreLong(c23.Add(p, l), q.maxmsg)
	c23.StoreLong(c23.Add(p, 2*l), q.msgsize)
	c23.StoreLong(c23.Add(p, 3*l), int64(len(q.msgs)))
}

// MqGetattr is mq_getattr.
func MqGetattr(fd int32, attr *c23.Void) int32 {
	d, nonblock := mqdOf(fd)
	if d == nil {
		return -1
	}
	mu.Lock()
	defer mu.Unlock()
	storeAttr(attr, d.q, nonblock)
	return 0
}

// MqSetattr is mq_setattr: of the new attributes, only mq_flags's
// O_NONBLOCK changes anything (other flags are EINVAL, as Linux's).
func MqSetattr(fd int32, attr, old *c23.Void) int32 {
	d, nonblock := mqdOf(fd)
	if d == nil {
		return -1
	}
	flags := c23.LoadLong(attr)
	if flags&^c23.O_NONBLOCK != 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	if c23.Addr(old) != 0 {
		mu.Lock()
		storeAttr(old, d.q, nonblock)
		mu.Unlock()
	}
	c23.SetNonblock(fd, flags&c23.O_NONBLOCK != 0)
	return 0
}
