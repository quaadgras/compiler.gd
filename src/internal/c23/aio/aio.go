// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package aio is aio.h: asynchronous I/O on the descriptors of
// internal/c23's table, each operation on a goroutine. As glibc's, the
// operations on one descriptor run in the order they were asked for, one
// at a time; those on different descriptors run at once. A read or write
// is at aio_offset (pread or pwrite), or, on what has no offset (a pipe,
// a socket), a read or write; a write to a file opened O_APPEND appends.
package aio

import (
	c23 "internal/c23"
	"io"
	"sync"
	"time"
)

const (
	AIO_CANCELED    = 0
	AIO_NOTCANCELED = 1
	AIO_ALLDONE     = 2

	LIO_READ  = 0
	LIO_WRITE = 1
	LIO_NOP   = 2

	LIO_WAIT   = 0
	LIO_NOWAIT = 1

	opFsync = 3 // not LIO's: aio_fsync's
)

const (
	pending = iota // waiting for the descriptor's earlier operations
	running
	done
)

// An op is an operation of an aiocb.
type op struct {
	fd    int32
	kind  int32
	buf   []byte
	off   int64
	sync  int32 // aio_fsync's op
	ev    c23.SigEvent
	list  *list
	state int
	err   int32 // EINPROGRESS until done
	ret   int64
	prev  <-chan struct{}
	done  chan struct{}
}

// A list is lio_listio's LIO_NOWAIT notification: when its last
// operation ends.
type list struct {
	left int
	ev   c23.SigEvent
}

var (
	mu      sync.Mutex
	changed c23.Broadcast
	ops     = map[uintptr]*op{}         // by the aiocb's address
	last    = map[int32]chan struct{}{} // each descriptor's last operation's done
)

// struct aiocb, glibc's:
//
//	int aio_fildes, aio_lio_opcode, aio_reqprio;	// 0, 4, 8
//	volatile void *aio_buf; size_t aio_nbytes;	// 16, 24 (12, 16 on wasm32)
//	struct sigevent aio_sigevent;			// 32 (20)
//	struct aiocb *__next_prio; int __abs_prio, __policy, __error_code;
//	ssize_t __return_value;				// 120 (100)
//	off_t aio_offset;				// 128 (104)
//	char __glibc_reserved[32];			// (168 bytes; 144)
var (
	w        = int(c23.WordSize)
	offBuf   = (12 + w - 1) &^ (w - 1)
	offEvent = offBuf + 2*w
	offRet   = (offEvent + c23.SigEventSize + w + 12 + w - 1) &^ (w - 1)
	offOff   = (offRet + w + 7) &^ 7
)

// submit starts the operation of cb, of kind; 0 or -1 with errno.
func submit(cb *c23.Void, kind int32, syncOp int32, l *list) int32 {
	o := &op{
		fd:   int32(c23.Load32(cb)),
		kind: kind,
		sync: syncOp,
		ev:   c23.LoadSigEvent(c23.Add(cb, offEvent)),
		list: l,
		err:  c23.EINPROGRESS,
		ret:  -1,
		done: make(chan struct{}),
	}
	if kind == LIO_READ || kind == LIO_WRITE {
		o.off = int64(c23.Load64(c23.Add(cb, offOff)))
		n := uint64(c23.LoadLong(c23.Add(cb, offBuf+w)))
		o.buf = c23.BytesAt(c23.LoadPtr(c23.Add(cb, offBuf)), uintptr(n))
	}
	mu.Lock()
	ops[c23.Addr(cb)] = o
	o.prev = last[o.fd]
	last[o.fd] = o.done
	mu.Unlock()
	go o.run()
	return 0
}

func (o *op) run() {
	if o.prev != nil {
		<-o.prev
	}
	mu.Lock()
	if o.state != pending { // canceled
		mu.Unlock()
		o.end()
		return
	}
	o.state = running
	mu.Unlock()
	ret, err := o.do()
	mu.Lock()
	o.state, o.ret, o.err = done, ret, err
	o.buf = nil
	changed.Notify()
	mu.Unlock()
	o.end()
	o.notify()
}

// end lets the descriptor's next operation run.
func (o *op) end() {
	mu.Lock()
	if last[o.fd] == o.done {
		delete(last, o.fd)
	}
	mu.Unlock()
	close(o.done)
}

// notify notifies of the operation's end, and of its list's.
func (o *op) notify() {
	o.ev.Fire(c23.SI_ASYNCIO, nil)
	if o.list != nil {
		mu.Lock()
		o.list.left--
		end := o.list.left == 0
		mu.Unlock()
		if end {
			o.list.ev.Fire(c23.SI_ASYNCIO, nil)
		}
	}
}

// do does the operation: its result, and an error number.
func (o *op) do() (int64, int32) {
	of := c23.FdFile(o.fd)
	if of == nil {
		return -1, c23.EBADF
	}
	acc := of.Flags & c23.O_ACCMODE
	if o.off < 0 {
		return -1, c23.EINVAL
	}
	switch o.kind {
	case opFsync:
		if of.F == nil {
			return -1, c23.EINVAL
		}
		if err := of.F.Sync(); err != nil {
			return -1, c23.ErrnoOf(err)
		}
		return 0, 0
	case LIO_READ:
		if acc == c23.O_WRONLY {
			return -1, c23.EBADF
		}
		if of.F != nil && of.Sp == nil {
			n, err := of.F.ReadAt(o.buf, o.off)
			if err == nil || err == io.EOF || n > 0 {
				return int64(n), 0
			}
			if e := c23.ErrnoOf(err); e != c23.ESPIPE {
				return -1, e
			}
		}
		n, err := of.Read(o.buf)
		if err != nil && err != io.EOF && n == 0 {
			return -1, c23.ErrnoOf(err)
		}
		return int64(n), 0
	case LIO_WRITE:
		if acc == c23.O_RDONLY {
			return -1, c23.EBADF
		}
		if of.F != nil && of.Sp == nil && of.Flags&c23.O_APPEND == 0 {
			n, err := of.F.WriteAt(o.buf, o.off)
			if err == nil || n > 0 {
				return int64(n), 0
			}
			if e := c23.ErrnoOf(err); e != c23.ESPIPE {
				return -1, e
			}
		}
		n, err := of.Write(o.buf)
		if err != nil && n == 0 {
			return -1, c23.ErrnoOf(err)
		}
		return int64(n), 0
	}
	return -1, c23.EINVAL
}

// AioRead is aio_read: it starts reading aio_nbytes bytes at aio_offset
// of aio_fildes into aio_buf. As glibc's, what fails once started (a
// descriptor that is not open, say) is aio_error's.
func AioRead(cb *c23.Void) int32 { return submit(cb, LIO_READ, 0, nil) }

// AioWrite is aio_write.
func AioWrite(cb *c23.Void) int32 { return submit(cb, LIO_WRITE, 0, nil) }

// AioFsync is aio_fsync: once the descriptor's earlier operations end,
// its file is synchronized (op O_SYNC or O_DSYNC: both are fsync's).
func AioFsync(syncOp int32, cb *c23.Void) int32 {
	if syncOp != c23.O_SYNC && syncOp != c23.O_DSYNC {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	if c23.FdFile(int32(c23.Load32(cb))) == nil {
		c23.SetErrno(c23.EBADF)
		return -1
	}
	return submit(cb, opFsync, syncOp, nil)
}

// AioError is aio_error: EINPROGRESS, or the operation's error number
// (ECANCELED if it was canceled; 0 if it succeeded). An aiocb of no
// operation is EINVAL.
func AioError(cb *c23.Void) int32 {
	mu.Lock()
	defer mu.Unlock()
	o := ops[c23.Addr(cb)]
	if o == nil {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	return o.err
}

// AioReturn is aio_return: the operation's result, once, after it ended
// (EINVAL before, or for an aiocb of no operation).
func AioReturn(cb *c23.Void) int64 {
	mu.Lock()
	defer mu.Unlock()
	o := ops[c23.Addr(cb)]
	if o == nil || o.state != done {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	delete(ops, c23.Addr(cb))
	if o.err != 0 {
		c23.SetErrno(o.err)
	}
	return o.ret
}

// AioCancel is aio_cancel: the operations of descriptor fd (or only
// cb's, if it is not null) that have not started are canceled, and
// notify as they would have ended. AIO_CANCELED if all were,
// AIO_NOTCANCELED if one runs, AIO_ALLDONE if all had ended.
func AioCancel(fd int32, cb *c23.Void) int32 {
	if c23.FdFile(fd) == nil {
		c23.SetErrno(c23.EBADF)
		return -1
	}
	if c23.Addr(cb) != 0 && int32(c23.Load32(cb)) != fd {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	mu.Lock()
	var canceled []*op
	notCanceled := false
	cancel := func(o *op) {
		switch o.state {
		case pending:
			o.state, o.err, o.ret, o.buf = done, c23.ECANCELED, -1, nil
			canceled = append(canceled, o)
		case running:
			notCanceled = true
		}
	}
	if c23.Addr(cb) != 0 {
		if o := ops[c23.Addr(cb)]; o != nil {
			cancel(o)
		}
	} else {
		for _, o := range ops {
			if o.fd == fd {
				cancel(o)
			}
		}
	}
	if len(canceled) > 0 {
		changed.Notify()
	}
	mu.Unlock()
	for _, o := range canceled {
		o.notify()
	}
	switch {
	case notCanceled:
		return AIO_NOTCANCELED
	case len(canceled) > 0:
		return AIO_CANCELED
	}
	return AIO_ALLDONE
}

// AioSuspend is aio_suspend: it waits until one of the nent operations
// of the aiocbs at list (null ones ignored) has ended, or the relative
// timeout at ts, if it is not null, has passed (EAGAIN).
func AioSuspend(lst *c23.Void, nent int32, ts *c23.Void) int32 {
	if nent < 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	var deadline time.Time
	if c23.Addr(ts) != 0 {
		d, ok := c23.LoadTimeout(ts)
		if !ok {
			c23.SetErrno(c23.EINVAL)
			return -1
		}
		deadline = d
	}
	cbs := make([]uintptr, 0, nent)
	for i := range int(nent) {
		if cb := c23.LoadPtr(c23.Add(lst, i*w)); c23.Addr(cb) != 0 {
			cbs = append(cbs, c23.Addr(cb))
		}
	}
	mu.Lock()
	for {
		for _, a := range cbs {
			if o := ops[a]; o == nil || o.state == done {
				mu.Unlock()
				return 0
			}
		}
		c := changed.C()
		mu.Unlock()
		if !c23.WaitUntil(c, deadline) {
			c23.SetErrno(c23.EAGAIN)
			return -1
		}
		mu.Lock()
	}
}

// LioListio is lio_listio: it starts the operations of the nent aiocbs at
// lst (each's aio_lio_opcode: LIO_READ, LIO_WRITE or LIO_NOP; null ones
// ignored). With LIO_WAIT it waits for them all (EIO if one failed);
// with LIO_NOWAIT the struct sigevent at sev (if not null) notifies when
// they all have ended. Each operation also notifies as its aiocb says.
func LioListio(mode int32, lst *c23.Void, nent int32, sev *c23.Void) int32 {
	if mode != LIO_WAIT && mode != LIO_NOWAIT || nent < 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	var l *list
	if mode == LIO_NOWAIT && c23.Addr(sev) != 0 {
		l = &list{ev: c23.LoadSigEvent(sev), left: 1}
	}
	var started []*c23.Void
	failed := false
	for i := range int(nent) {
		cb := c23.LoadPtr(c23.Add(lst, i*w))
		if c23.Addr(cb) == 0 {
			continue
		}
		kind := int32(c23.Load32(c23.Add(cb, 4)))
		if kind == LIO_NOP {
			continue
		}
		if kind != LIO_READ && kind != LIO_WRITE {
			failed = true
			continue
		}
		if l != nil {
			mu.Lock()
			l.left++
			mu.Unlock()
		}
		if submit(cb, kind, 0, l) != 0 {
			failed = true
			if l != nil {
				mu.Lock()
				l.left--
				mu.Unlock()
			}
			continue
		}
		started = append(started, cb)
	}
	if l != nil {
		// The list's own reference: it notifies now if all have ended.
		mu.Lock()
		l.left--
		end := l.left == 0
		mu.Unlock()
		if end {
			l.ev.Fire(c23.SI_ASYNCIO, nil)
		}
	}
	if mode == LIO_WAIT {
		for _, cb := range started {
			mu.Lock()
			o := ops[c23.Addr(cb)]
			mu.Unlock()
			if o == nil {
				continue
			}
			for {
				mu.Lock()
				st, err := o.state, o.err
				c := changed.C()
				mu.Unlock()
				if st == done {
					if err != 0 {
						failed = true
					}
					break
				}
				<-c
			}
		}
	}
	if failed {
		c23.SetErrno(c23.EIO)
		return -1
	}
	return 0
}
