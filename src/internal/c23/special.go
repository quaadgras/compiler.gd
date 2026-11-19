// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"io"
	"io/fs"
	"sync"
	"time"
)

// Descriptors of what is not an os.File (sockets, from internal/c23/sys/socket,
// which links net only into programs that use it), and readiness for
// poll and select: Go has no readiness of its own, so a descriptor whose
// reads may block (a pipe, a terminal, a socket) is read ahead by a
// goroutine into a buffer, and is readable when that has data or an end.

// poll's events (poll.h, Linux's values).
const (
	POLLIN  = 0x1
	POLLPRI = 0x2
	POLLOUT = 0x4
	POLLERR = 0x8
	POLLHUP = 0x10
)

// A Special is what a descriptor names if it is not an os.File.
type Special interface {
	// Read and Write are read and write; nonblock is the descriptor's
	// O_NONBLOCK: they return EAGAIN (as an Errno) rather than wait.
	Read(b []byte, nonblock bool) (int, error)
	Write(b []byte, nonblock bool) (int, error)
	Close() error
	// Ready returns poll's events that hold now (of POLLIN, POLLOUT,
	// POLLPRI, POLLERR, POLLHUP), without waiting.
	Ready() int16
	// Mode is fstat's st_mode.
	Mode() uint32
}

// ErrnoError returns errno value e as a Go error, which setErrnoOf
// sets errno to.
func ErrnoError(e int32) error { return ErrnoErr(e) }

// FdNewSpecial returns a new descriptor of sp, with flags as F_GETFL
// returns them (O_NONBLOCK) and FD_CLOEXEC if cloexec.
func FdNewSpecial(sp Special, flags int32, cloexec bool) int32 {
	FdMu.Lock()
	defer FdMu.Unlock()
	return fdInstall(&FdEntry{Of: &Ofile{Sp: sp, Refs: 1, Flags: flags | O_RDWR}, Cloexec: cloexec}, 0)
}

// FdSpecial returns what fd names if it is a Special, and whether it is
// in non-blocking mode; if not, it sets errno to EBADF (no descriptor)
// or notSpecial and returns nil.
func FdSpecial(fd int32, notSpecial int32) (Special, bool) {
	of := FdFile(fd)
	if of == nil {
		SetErrno(EBADF)
		return nil, false
	}
	if of.Sp == nil {
		SetErrno(notSpecial)
		return nil, false
	}
	return of.Sp, of.Flags&O_NONBLOCK != 0
}

// SetNonblock sets or clears fd's O_NONBLOCK.
func SetNonblock(fd int32, on bool) {
	if of := FdFile(fd); of != nil {
		FdMu.Lock()
		if on {
			of.Flags |= O_NONBLOCK
		} else {
			of.Flags &^= O_NONBLOCK
		}
		FdMu.Unlock()
	}
}

// Readiness changes are announced by closing a channel that pollers wait
// on, and replacing it.
var (
	pollMu sync.Mutex
	pollCh = make(chan struct{})
)

// PollNotify wakes poll and select: something may have become ready.
func PollNotify() {
	pollMu.Lock()
	close(pollCh)
	pollCh = make(chan struct{})
	pollMu.Unlock()
}

func PollWait() <-chan struct{} {
	pollMu.Lock()
	defer pollMu.Unlock()
	return pollCh
}

// A ReadAhead reads src ahead, in a goroutine, into a buffer of up to
// 64 KiB, which reads then take from.
type ReadAhead struct {
	mu   sync.Mutex
	cond sync.Cond
	buf  []byte
	err  error // what ended reading, once buf is empty
	src  io.Reader
	n    uint64 // the reads that brought data or the end (Arrivals)
}

const readAheadMax = 64 << 10

// NewReadAhead starts reading src ahead.
func NewReadAhead(src io.Reader) *ReadAhead {
	ra := &ReadAhead{src: src}
	ra.cond.L = &ra.mu
	go ra.run()
	return ra
}

func (ra *ReadAhead) run() {
	tmp := make([]byte, 16<<10)
	for {
		ra.mu.Lock()
		for len(ra.buf) >= readAheadMax {
			ra.cond.Wait()
		}
		ra.mu.Unlock()
		n, err := ra.src.Read(tmp)
		ra.mu.Lock()
		ra.buf = append(ra.buf, tmp[:n]...)
		if err != nil {
			ra.err = err
		}
		ra.n++
		ra.cond.Broadcast()
		ra.mu.Unlock()
		PollNotify()
		if err != nil {
			return
		}
	}
}

// Read reads what has been read ahead, waiting for some unless nonblock
// (then EAGAIN). At the end it returns io.EOF, or what ended reading.
func (ra *ReadAhead) Read(b []byte, nonblock bool) (int, error) {
	return ra.Recv(b, nonblock, false, time.Time{})
}

// Recv is Read that leaves what it reads to be read again if peek, and
// waits only until deadline, if that is not zero (then EAGAIN, as a
// socket's SO_RCVTIMEO does).
func (ra *ReadAhead) Recv(b []byte, nonblock, peek bool, deadline time.Time) (int, error) {
	ra.mu.Lock()
	defer ra.mu.Unlock()
	expired := false
	if !deadline.IsZero() && len(ra.buf) == 0 && ra.err == nil && !nonblock {
		t := time.AfterFunc(time.Until(deadline), func() {
			ra.mu.Lock()
			expired = true
			ra.cond.Broadcast()
			ra.mu.Unlock()
		})
		defer t.Stop()
	}
	for len(ra.buf) == 0 && ra.err == nil {
		if nonblock || expired {
			return 0, ErrnoErr(EAGAIN)
		}
		ra.cond.Wait()
	}
	if len(ra.buf) == 0 {
		return 0, ra.err
	}
	n := copy(b, ra.buf)
	if peek {
		return n, nil
	}
	ra.buf = ra.buf[n:]
	if len(ra.buf) == 0 {
		ra.buf = nil
	}
	ra.cond.Broadcast()
	return n, nil
}

// Readable reports whether a read would not wait, and whether reading
// has ended (with nothing left).
func (ra *ReadAhead) Readable() (ready, end bool) {
	ra.mu.Lock()
	defer ra.mu.Unlock()
	return len(ra.buf) > 0 || ra.err != nil, len(ra.buf) == 0 && ra.err != nil
}

// Arrivals counts what arrived: each read ahead that brought data or the
// end. epoll's edge trigger reports a descriptor again when it changed.
func (ra *ReadAhead) Arrivals() uint64 {
	ra.mu.Lock()
	defer ra.mu.Unlock()
	return ra.n
}

// Arrivals counts what arrived at the open file, as its read ahead or its
// Special (if that has an Arrivals method) counts it, for epoll's edge
// trigger: a descriptor whose readiness stays the same is reported again
// when the count changed.
func (of *Ofile) Arrivals() uint64 {
	if of.Sp != nil {
		if a, ok := of.Sp.(interface{ Arrivals() uint64 }); ok {
			return a.Arrivals()
		}
		return 0
	}
	if ra := of.readAhead(false); ra != nil {
		return ra.Arrivals()
	}
	return 0
}

// Buffered returns how many bytes a read may take without waiting.
func (ra *ReadAhead) Buffered() int {
	ra.mu.Lock()
	defer ra.mu.Unlock()
	return len(ra.buf)
}

// read reads the open file: its Special, what is read ahead of it, or
// the os.File. A non-blocking read of a file that may block reads it
// ahead from then on.
func (of *Ofile) Read(b []byte) (int, error) {
	nonblock := of.Flags&O_NONBLOCK != 0
	if of.Sp != nil {
		return of.Sp.Read(b, nonblock)
	}
	if ra := of.readAhead(nonblock); ra != nil {
		return ra.Read(b, nonblock)
	}
	return of.F.Read(b)
}

func (of *Ofile) Write(b []byte) (int, error) {
	if of.Sp != nil {
		return of.Sp.Write(b, of.Flags&O_NONBLOCK != 0)
	}
	return of.F.Write(b)
}

// readAhead returns the file's ReadAhead, starting it if start and the
// file's reads may block (it is not a regular file or a directory).
func (of *Ofile) readAhead(start bool) *ReadAhead {
	FdMu.Lock()
	defer FdMu.Unlock()
	if of.ra != nil || !start || of.Flags&O_ACCMODE == O_WRONLY {
		return of.ra
	}
	if fi, err := of.F.Stat(); err != nil || fi.Mode()&(fs.ModeType&^fs.ModeSymlink) == 0 || fi.IsDir() {
		return nil
	}
	of.ra = NewReadAhead(of.F)
	return of.ra
}

// ready returns poll's events that hold for the open file now.
func (of *Ofile) Ready() int16 {
	if of.Sp != nil {
		return of.Sp.Ready()
	}
	var r int16
	acc := of.Flags & O_ACCMODE
	if acc != O_RDONLY {
		r |= POLLOUT
	}
	if acc != O_WRONLY {
		if ra := of.readAhead(true); ra != nil {
			if ok, end := ra.Readable(); end {
				r |= POLLHUP // as Linux: a pipe at its end is not readable
			} else if ok {
				r |= POLLIN
			}
		} else {
			r |= POLLIN // a regular file is always ready
		}
	}
	return r
}

// waitUntil waits for ch to be closed or the deadline (if not zero),
// and reports which: false for the deadline.
func WaitUntil(ch <-chan struct{}, deadline time.Time) bool {
	if deadline.IsZero() {
		<-ch
		return true
	}
	d := time.Until(deadline)
	if d <= 0 {
		return false
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ch:
		return true
	case <-t.C:
		return false
	}
}
