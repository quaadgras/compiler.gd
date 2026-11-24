// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package epoll is sys/epoll.h: an epoll instance is a c23.Special that
// keeps a list of interests and finds them ready with the readiness poll
// uses (c23.Ofile.Ready), so it works on every descriptor poll does,
// everywhere. It is level-triggered unless EPOLLET, and is itself
// pollable: readable when one of its interests is ready.
package epoll

import (
	c23 "internal/c23"
	"sync"
	"time"
)

const (
	EPOLLIN        = 0x1
	EPOLLPRI       = 0x2
	EPOLLOUT       = 0x4
	EPOLLERR       = 0x8
	EPOLLHUP       = 0x10
	EPOLLRDNORM    = 0x40
	EPOLLRDBAND    = 0x80
	EPOLLWRNORM    = 0x100
	EPOLLWRBAND    = 0x200
	EPOLLMSG       = 0x400
	EPOLLRDHUP     = 0x2000
	EPOLLEXCLUSIVE = 1 << 28
	EPOLLWAKEUP    = 1 << 29
	EPOLLONESHOT   = 1 << 30
	EPOLLET        = 1 << 31

	EPOLL_CTL_ADD = 1
	EPOLL_CTL_DEL = 2
	EPOLL_CTL_MOD = 3

	EPOLL_CLOEXEC = c23.O_CLOEXEC
)

// struct epoll_event {uint32_t events; epoll_data_t data;} is 16 bytes,
// data at 8, on every target. Linux packs it on x86-64 (12 bytes, data at
// 4), but there epoll_data_t's pointer would be unaligned, and cmd/cc
// keeps a pointer's capability only in an aligned word.
const (
	eventSize = 16
	dataOff   = 8
)

// An interest is a descriptor epoll_ctl added.
type interest struct {
	fd     int32
	of     *c23.Ofile // what fd named when it was added
	events uint32
	data   *c23.Void // 8 bytes: the epoll_data_t, a pointer kept as one
	off    bool      // EPOLLONESHOT reported it: disabled until EPOLL_CTL_MOD

	// For EPOLLET: what was ready, and the count of arrivals, when it
	// was last reported.
	last     uint32
	arrivals uint64
}

type epoll struct {
	mu   sync.Mutex
	list []*interest // in the order they are reported in
	by   map[int32]*interest
}

func newEpoll() *epoll { return &epoll{by: map[int32]*interest{}} }

// The c23.Special methods.

func (ep *epoll) Read(b []byte, nonblock bool) (int, error)  { return 0, c23.ErrnoError(c23.EINVAL) }
func (ep *epoll) Write(b []byte, nonblock bool) (int, error) { return 0, c23.ErrnoError(c23.EINVAL) }
func (ep *epoll) Mode() uint32                               { return 0o600 }

func (ep *epoll) Close() error {
	ep.mu.Lock()
	ep.list, ep.by = nil, map[int32]*interest{}
	ep.mu.Unlock()
	return nil
}

func (ep *epoll) Ready() int16 {
	for _, it := range ep.snapshot() {
		ev, arrivals := ep.check(it)
		ep.mu.Lock()
		news := it.news(ev, arrivals)
		ep.mu.Unlock()
		if news {
			return c23.POLLIN
		}
	}
	return 0
}

func (ep *epoll) snapshot() []*interest {
	ep.mu.Lock()
	defer ep.mu.Unlock()
	return append([]*interest(nil), ep.list...)
}

// check returns the events of it to report now, and its count of
// arrivals, without changing it. A descriptor that was closed (or now
// names another file) is no longer an interest, as Linux drops an
// interest when its file is closed.
func (ep *epoll) check(it *interest) (uint32, uint64) {
	if c23.FdFile(it.fd) != it.of {
		ep.mu.Lock()
		ep.remove(it)
		ep.mu.Unlock()
		return 0, 0
	}
	ep.mu.Lock()
	events, off := it.events, it.off
	ep.mu.Unlock()
	if off {
		return 0, 0
	}
	r := it.of.Ready()
	var ev uint32
	for _, m := range [...]struct {
		poll int16
		ep   uint32
	}{{c23.POLLIN, EPOLLIN | EPOLLRDNORM}, {c23.POLLPRI, EPOLLPRI}, {c23.POLLOUT, EPOLLOUT | EPOLLWRNORM}, {c23.POLLERR, EPOLLERR}, {c23.POLLHUP, EPOLLHUP | EPOLLRDHUP}} {
		if r&m.poll != 0 {
			ev |= m.ep
		}
	}
	ev &= events | EPOLLERR | EPOLLHUP
	var arrivals uint64
	if events&EPOLLET != 0 && ev != 0 {
		arrivals = it.of.Arrivals()
	}
	return ev, arrivals
}

// news reports whether it is to be reported with ev ready and arrivals:
// if ev is not empty and, edge-triggered, something became ready or
// arrived since it was last reported. ep.mu is held.
func (it *interest) news(ev uint32, arrivals uint64) bool {
	if ev == 0 || it.events&EPOLLET == 0 {
		return ev != 0
	}
	return ev&^it.last != 0 || arrivals != it.arrivals
}

// remove forgets it; ep.mu is held.
func (ep *epoll) remove(it *interest) {
	if ep.by[it.fd] != it {
		return
	}
	delete(ep.by, it.fd)
	for i, x := range ep.list {
		if x == it {
			ep.list = append(ep.list[:i], ep.list[i+1:]...)
			break
		}
	}
}

// collect stores up to max ready events at events, and returns how many.
func (ep *epoll) collect(events *c23.Void, max int) int {
	n := 0
	var reported []*interest
	for _, it := range ep.snapshot() {
		if n == max {
			break
		}
		ev, arrivals := ep.check(it)
		ep.mu.Lock()
		if ep.by[it.fd] != it {
			ep.mu.Unlock()
			continue
		}
		news := it.news(ev, arrivals)
		it.last &= ev // what is no longer ready may rise again
		if !news {
			ep.mu.Unlock()
			continue
		}
		it.last, it.arrivals = ev, arrivals
		if it.events&EPOLLONESHOT != 0 {
			it.off = true
		}
		e := c23.Add(events, n*eventSize)
		c23.Store32(e, ev)
		c23.Memmove(c23.Add(e, dataOff), it.data, 8)
		ep.mu.Unlock()
		reported = append(reported, it)
		n++
	}
	if len(reported) > 0 {
		// Those reported go to the end, so that a maxevents smaller than
		// what is ready reports the others next.
		ep.mu.Lock()
		var rest []*interest
		for _, it := range ep.list {
			found := false
			for _, r := range reported {
				if r == it {
					found = true
					break
				}
			}
			if !found {
				rest = append(rest, it)
			}
		}
		for _, r := range reported {
			if ep.by[r.fd] == r {
				rest = append(rest, r)
			}
		}
		ep.list = rest
		ep.mu.Unlock()
	}
	return n
}

// EpollCreate is epoll_create: size, a hint, must be positive.
func EpollCreate(size int32) int32 {
	if size <= 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	return EpollCreate1(0)
}

// EpollCreate1 is epoll_create1.
func EpollCreate1(flags int32) int32 {
	if flags&^EPOLL_CLOEXEC != 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	return c23.FdNewSpecial(newEpoll(), 0, flags&EPOLL_CLOEXEC != 0)
}

// epollOf returns the epoll instance epfd names, or nil with errno set.
func epollOf(epfd int32) *epoll {
	sp, _ := c23.FdSpecial(epfd, c23.EINVAL)
	if sp == nil {
		return nil
	}
	ep, ok := sp.(*epoll)
	if !ok {
		c23.SetErrno(c23.EINVAL)
		return nil
	}
	return ep
}

// contains reports whether ep watches target, through any depth of
// epoll instances.
func (ep *epoll) contains(target *epoll, depth int) bool {
	if ep == target {
		return true
	}
	if depth > 5 {
		return true // Linux's EPOLL_MAX_NESTS: ELOOP
	}
	for _, it := range ep.snapshot() {
		if sub, ok := it.of.Sp.(*epoll); ok && sub.contains(target, depth+1) {
			return true
		}
	}
	return false
}

// EpollCtl is epoll_ctl. A regular file or a directory cannot be added
// (EPERM), as on Linux.
func EpollCtl(epfd, op, fd int32, event *c23.Void) int32 {
	ep := epollOf(epfd)
	if ep == nil {
		return -1
	}
	of := c23.FdFile(fd)
	if of == nil {
		c23.SetErrno(c23.EBADF)
		return -1
	}
	if fd == epfd {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	if of.Sp == nil {
		if fi, err := of.F.Stat(); err == nil && (fi.Mode().IsRegular() || fi.IsDir()) {
			c23.SetErrno(c23.EPERM)
			return -1
		}
	}
	var events uint32
	if op == EPOLL_CTL_ADD || op == EPOLL_CTL_MOD {
		if c23.Addr(event) == 0 {
			c23.SetErrno(c23.EFAULT)
			return -1
		}
		events = c23.Load32(event)
	}
	switch op {
	case EPOLL_CTL_ADD:
		if sub, ok := of.Sp.(*epoll); ok && sub.contains(ep, 0) {
			c23.SetErrno(c23.ELOOP)
			return -1
		}
		ep.mu.Lock()
		defer ep.mu.Unlock()
		if it := ep.by[fd]; it != nil && c23.FdFile(fd) == it.of {
			c23.SetErrno(c23.EEXIST)
			return -1
		} else if it != nil {
			ep.remove(it)
		}
		it := &interest{fd: fd, of: of, events: events, data: c23.Malloc(8)}
		c23.Memmove(it.data, c23.Add(event, dataOff), 8)
		ep.by[fd] = it
		ep.list = append(ep.list, it)
	case EPOLL_CTL_MOD:
		ep.mu.Lock()
		defer ep.mu.Unlock()
		it := ep.by[fd]
		if it == nil || it.of != of {
			c23.SetErrno(c23.ENOENT)
			return -1
		}
		if events&EPOLLEXCLUSIVE != 0 || it.events&EPOLLEXCLUSIVE != 0 {
			c23.SetErrno(c23.EINVAL)
			return -1
		}
		it.events, it.off, it.last = events, false, 0
		c23.Memmove(it.data, c23.Add(event, dataOff), 8)
	case EPOLL_CTL_DEL:
		ep.mu.Lock()
		defer ep.mu.Unlock()
		it := ep.by[fd]
		if it == nil || it.of != of {
			c23.SetErrno(c23.ENOENT)
			return -1
		}
		ep.remove(it)
	default:
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	c23.PollNotify()
	return 0
}

// EpollWait is epoll_wait: timeout is in milliseconds, -1 for none. It
// is not interrupted by signals (their handlers run on other goroutines).
func EpollWait(epfd int32, events *c23.Void, maxevents, timeout int32) int32 {
	return epollWait(epfd, events, maxevents, timeout, false, 0)
}

// EpollPwait is epoll_pwait: the calling thread's signal mask is sigmask
// while it waits, if that is not null, and if a pending signal it then
// unblocks runs its handler before anything is ready, the result is -1
// with errno EINTR.
func EpollPwait(epfd int32, events *c23.Void, maxevents, timeout int32, sigmask *c23.Void) int32 {
	if c23.Addr(sigmask) == 0 {
		return EpollWait(epfd, events, maxevents, timeout)
	}
	return epollWait(epfd, events, maxevents, timeout, true, c23.LoadSigset(sigmask)&^c23.SigUnblockable)
}

func epollWait(epfd int32, events *c23.Void, maxevents, timeout int32, setMask bool, mask uint64) int32 {
	ep := epollOf(epfd)
	if ep == nil {
		return -1
	}
	if maxevents <= 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	if setMask {
		t := c23.SigSelf()
		c23.SigMu.Lock()
		old := t.Mask
		t.Mask = mask
		c23.SigMu.Unlock()
		defer func() {
			c23.SigMu.Lock()
			t.Mask = old
			c23.SigMu.Unlock()
			t.DeliverPending()
		}()
		if t.DeliverPending() {
			if n := ep.collect(events, int(maxevents)); n > 0 {
				return int32(n)
			}
			c23.SetErrno(c23.EINTR)
			return -1
		}
	}
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(time.Duration(timeout) * time.Millisecond)
	}
	for {
		ch := c23.PollWait()
		if n := ep.collect(events, int(maxevents)); n > 0 || timeout == 0 {
			return int32(n)
		}
		if !c23.WaitUntil(ch, deadline) {
			return 0
		}
	}
}
