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

// Descriptors of what is not an os.File (sockets, from internal/c23/sock,
// which links net only into programs that use it), and readiness for
// poll and select: Go has no readiness of its own, so a descriptor whose
// reads may block (a pipe, a terminal, a socket) is read ahead by a
// goroutine into a buffer, and is readable when that has data or an end.

// poll's events (poll.h, Linux's values).
const (
	POLLIN   = 0x1
	POLLPRI  = 0x2
	POLLOUT  = 0x4
	POLLERR  = 0x8
	POLLHUP  = 0x10
	POLLNVAL = 0x20
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
func ErrnoError(e int32) error { return errnoErr(e) }

// FdNewSpecial returns a new descriptor of sp, with flags as F_GETFL
// returns them (O_NONBLOCK) and FD_CLOEXEC if cloexec.
func FdNewSpecial(sp Special, flags int32, cloexec bool) int32 {
	fdMu.Lock()
	defer fdMu.Unlock()
	return fdInstall(&fdEntry{of: &ofile{sp: sp, refs: 1, flags: flags | O_RDWR}, cloexec: cloexec}, 0)
}

// FdSpecial returns what fd names if it is a Special, and whether it is
// in non-blocking mode; if not, it sets errno to EBADF (no descriptor)
// or notSpecial and returns nil.
func FdSpecial(fd int32, notSpecial int32) (Special, bool) {
	of := fdFile(fd)
	if of == nil {
		SetErrno(EBADF)
		return nil, false
	}
	if of.sp == nil {
		SetErrno(notSpecial)
		return nil, false
	}
	return of.sp, of.flags&O_NONBLOCK != 0
}

// SetNonblock sets or clears fd's O_NONBLOCK.
func SetNonblock(fd int32, on bool) {
	if of := fdFile(fd); of != nil {
		fdMu.Lock()
		if on {
			of.flags |= O_NONBLOCK
		} else {
			of.flags &^= O_NONBLOCK
		}
		fdMu.Unlock()
	}
}

// WordSize and LongSize are the sizes of C's pointers and longs.
const (
	WordSize = wordSize
	LongSize = longSize
)

// LoadLong and StoreLong load and store a C long.
func LoadLong(p Ptr) int64     { return loadLong(p) }
func StoreLong(p Ptr, v int64) { storeLong(p, v) }

// BytesAt returns the n bytes at p, checked.
func BytesAt(p Ptr, n uint64) []byte { return bytesAt(p, uintptr(n)) }

// SetErrnoOf sets errno to err's.
func SetErrnoOf(err error) { setErrnoOf(err) }

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

func pollWait() <-chan struct{} {
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
			return 0, errnoErr(EAGAIN)
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

// Buffered returns how many bytes a read may take without waiting.
func (ra *ReadAhead) Buffered() int {
	ra.mu.Lock()
	defer ra.mu.Unlock()
	return len(ra.buf)
}

// read reads the open file: its Special, what is read ahead of it, or
// the os.File. A non-blocking read of a file that may block reads it
// ahead from then on.
func (of *ofile) read(b []byte) (int, error) {
	nonblock := of.flags&O_NONBLOCK != 0
	if of.sp != nil {
		return of.sp.Read(b, nonblock)
	}
	if ra := of.readAhead(nonblock); ra != nil {
		return ra.Read(b, nonblock)
	}
	return of.f.Read(b)
}

func (of *ofile) write(b []byte) (int, error) {
	if of.sp != nil {
		return of.sp.Write(b, of.flags&O_NONBLOCK != 0)
	}
	return of.f.Write(b)
}

// readAhead returns the file's ReadAhead, starting it if start and the
// file's reads may block (it is not a regular file or a directory).
func (of *ofile) readAhead(start bool) *ReadAhead {
	fdMu.Lock()
	defer fdMu.Unlock()
	if of.ra != nil || !start || of.flags&O_ACCMODE == O_WRONLY {
		return of.ra
	}
	if fi, err := of.f.Stat(); err != nil || fi.Mode()&(fs.ModeType&^fs.ModeSymlink) == 0 || fi.IsDir() {
		return nil
	}
	of.ra = NewReadAhead(of.f)
	return of.ra
}

// ready returns poll's events that hold for the open file now.
func (of *ofile) ready() int16 {
	if of.sp != nil {
		return of.sp.Ready()
	}
	var r int16
	acc := of.flags & O_ACCMODE
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

// Poll is poll: fds is an array of n struct pollfd {int fd; short
// events; short revents;}; timeout is in milliseconds, -1 for none.
func Poll(fds Ptr, n uint64, timeout int32) int32 {
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(time.Duration(timeout) * time.Millisecond)
	}
	for {
		ch := pollWait()
		count := int32(0)
		for i := range n {
			p := Add(fds, int(8*i))
			fd := int32(Load32(p))
			events := int16(Load16(Add(p, 4)))
			var rev int16
			if fd >= 0 {
				if of := fdFile(fd); of == nil {
					rev = POLLNVAL
				} else {
					rev = of.ready() & (events | POLLERR | POLLHUP)
				}
			}
			Store16(Add(p, 6), uint16(rev))
			if rev != 0 {
				count++
			}
		}
		if count > 0 || timeout == 0 {
			return count
		}
		if !waitUntil(ch, deadline) {
			return 0
		}
	}
}

// waitUntil waits for ch to be closed or the deadline (if not zero),
// and reports which: false for the deadline.
func waitUntil(ch <-chan struct{}, deadline time.Time) bool {
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

// Select is select: the fd_sets are bit arrays (descriptor i is bit i%8
// of byte i/8, on a little-endian target, whatever the word), timeout a
// struct timeval or null for none.
func Select(nfds int32, rd, wr, ex, timeout Ptr) int32 {
	var deadline time.Time
	poll := true
	if Addr(timeout) != 0 {
		sec, usec := int64(Load64(timeout)), int64(Load64(Add(timeout, 8)))
		if sec < 0 || usec < 0 || usec >= 1e6 {
			SetErrno(EINVAL)
			return -1
		}
		d := time.Duration(sec)*time.Second + time.Duration(usec)*time.Microsecond
		poll = d == 0
		deadline = time.Now().Add(d)
	}
	return selectFds(nfds, rd, wr, ex, deadline, poll)
}

// Pselect is pselect: timeout is a struct timespec; the signal mask is
// not changed.
func Pselect(nfds int32, rd, wr, ex, timeout, sigmask Ptr) int32 {
	var deadline time.Time
	poll := true
	if Addr(timeout) != 0 {
		sec, nsec := int64(Load64(timeout)), loadLong(Add(timeout, 8))
		if sec < 0 || nsec < 0 || nsec >= 1e9 {
			SetErrno(EINVAL)
			return -1
		}
		d := time.Duration(sec)*time.Second + time.Duration(nsec)
		poll = d == 0
		deadline = time.Now().Add(d)
	}
	return selectFds(nfds, rd, wr, ex, deadline, poll)
}

func selectFds(nfds int32, rd, wr, ex Ptr, deadline time.Time, poll bool) int32 {
	if nfds < 0 || nfds > 1024 {
		SetErrno(EINVAL)
		return -1
	}
	nb := uintptr(nfds+7) / 8
	sets := [3][]byte{}
	want := [3][]byte{}
	for i, p := range [3]Ptr{rd, wr, ex} {
		if Addr(p) != 0 {
			sets[i] = bytesAt(p, nb)
			want[i] = append([]byte(nil), sets[i]...)
		}
	}
	isSet := func(s []byte, fd int32) bool { return s != nil && s[fd/8]&(1<<(fd%8)) != 0 }
	for fd := range nfds {
		if (isSet(want[0], fd) || isSet(want[1], fd) || isSet(want[2], fd)) && fdFile(fd) == nil {
			SetErrno(EBADF)
			return -1
		}
	}
	for {
		ch := pollWait()
		count := int32(0)
		var got [3][]byte
		for i := range got {
			if want[i] != nil {
				got[i] = make([]byte, nb)
			}
		}
		for fd := range nfds {
			if !isSet(want[0], fd) && !isSet(want[1], fd) && !isSet(want[2], fd) {
				continue
			}
			of := fdFile(fd)
			if of == nil {
				continue
			}
			r := of.ready()
			for i, ev := range [3]int16{POLLIN | POLLHUP | POLLERR, POLLOUT | POLLERR, POLLPRI} {
				if isSet(want[i], fd) && r&ev != 0 {
					got[i][fd/8] |= 1 << (fd % 8)
					count++
				}
			}
		}
		if count > 0 || poll || !deadline.IsZero() && !time.Now().Before(deadline) {
			for i := range sets {
				if sets[i] != nil {
					copy(sets[i], got[i])
				}
			}
			return count
		}
		waitUntil(ch, deadline)
	}
}
