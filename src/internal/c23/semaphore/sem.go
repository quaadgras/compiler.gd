// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package semaphore

import (
	c23 "internal/c23"
	"strings"
	"sync"
	"time"
)

const semValueMax = 1<<31 - 1

// A sem is a semaphore: its value, and a channel closed (and replaced)
// when it is posted, which waiters wait on.
type sem struct {
	mu     sync.Mutex
	value  int32
	posted chan struct{}
}

var sems sync.Map

func semOf(p c23.Ptr) *sem {
	c23.Check(p, 4)
	s, _ := sems.LoadOrStore(c23.Addr(p), &sem{posted: make(chan struct{})})
	return s.(*sem)
}

// SemInit is sem_init: pshared is accepted, and the semaphore is shared
// by the program's threads, the only ones there are.
func SemInit(p c23.Ptr, pshared int32, value uint32) int32 {
	if value > semValueMax {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	c23.Check(p, 4)
	sems.Store(c23.Addr(p), &sem{value: int32(value), posted: make(chan struct{})})
	return 0
}

func SemDestroy(p c23.Ptr) int32 {
	sems.Delete(c23.Addr(p))
	return 0
}

func SemPost(p c23.Ptr) int32 {
	s := semOf(p)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.value == semValueMax {
		c23.SetErrno(c23.EOVERFLOW)
		return -1
	}
	s.value++
	close(s.posted)
	s.posted = make(chan struct{})
	return 0
}

// wait decrements s, waiting until it is positive: if try, not at all
// (EAGAIN); if deadline is not zero, until then (ETIMEDOUT).
func (s *sem) wait(try bool, deadline time.Time) int32 {
	s.mu.Lock()
	for s.value == 0 {
		if try {
			s.mu.Unlock()
			return c23.EAGAIN
		}
		c := s.posted
		s.mu.Unlock()
		if deadline.IsZero() {
			<-c
		} else {
			d := time.Until(deadline)
			if d <= 0 {
				return c23.ETIMEDOUT
			}
			t := time.NewTimer(d)
			select {
			case <-c:
				t.Stop()
			case <-t.C:
			}
		}
		s.mu.Lock()
	}
	s.value--
	s.mu.Unlock()
	return 0
}

func semRet(e int32) int32 {
	if e != 0 {
		c23.SetErrno(e)
		return -1
	}
	return 0
}

func SemWait(p c23.Ptr) int32 { return semRet(semOf(p).wait(false, time.Time{})) }

func SemTrywait(p c23.Ptr) int32 { return semRet(semOf(p).wait(true, time.Time{})) }

// SemTimedwait is sem_timedwait, which gives up at the CLOCK_REALTIME
// time at ts.
func SemTimedwait(p, ts c23.Ptr) int32 {
	s := semOf(p)
	if e := s.wait(true, time.Time{}); e == 0 {
		return 0
	}
	d, ok := c23.LoadDeadline(ts)
	if !ok {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	if !time.Now().Before(d) {
		c23.SetErrno(c23.ETIMEDOUT)
		return -1
	}
	return semRet(s.wait(false, d))
}

func SemGetvalue(p, value c23.Ptr) int32 {
	s := semOf(p)
	s.mu.Lock()
	c23.Store32(value, uint32(s.value))
	s.mu.Unlock()
	return 0
}

// A namedSem is a semaphore sem_open made: its sem_t, and how many
// opens sem_close has not closed.
type namedSem struct {
	p    c23.Ptr
	refs int
}

var namedMu sync.Mutex

var named = map[string]*namedSem{} // the linked ones

var namedPtr = map[uintptr]*namedSem{}

// semSize is sizeof(sem_t).
const semSize = 32

// SemOpen is sem_open: with O_CREAT, the mode and value are its third
// and fourth arguments. The name is glibc's: leading slashes are
// dropped, and what is left must not be empty or have one. The result is
// SEM_FAILED (null) on error.
func SemOpen(name c23.Ptr, oflag int32, va ...any) c23.Ptr {
	n := strings.TrimLeft(c23.GoString(name), "/")
	if n == "" || strings.Contains(n, "/") {
		c23.SetErrno(c23.EINVAL)
		return c23.Ptr{}
	}
	if len(n) > 251 {
		c23.SetErrno(c23.ENAMETOOLONG)
		return c23.Ptr{}
	}
	namedMu.Lock()
	defer namedMu.Unlock()
	if s := named[n]; s != nil {
		if oflag&(c23.O_CREAT|c23.O_EXCL) == c23.O_CREAT|c23.O_EXCL {
			c23.SetErrno(c23.EEXIST)
			return c23.Ptr{}
		}
		s.refs++
		return s.p
	}
	if oflag&c23.O_CREAT == 0 {
		c23.SetErrno(c23.ENOENT)
		return c23.Ptr{}
	}
	var value uint64
	if len(va) > 1 {
		value = uint64(uint32(c23.ToInt64(va[1])))
	}
	if value > semValueMax {
		c23.SetErrno(c23.EINVAL)
		return c23.Ptr{}
	}
	s := &namedSem{p: c23.Malloc(semSize), refs: 1}
	SemInit(s.p, 1, uint32(value))
	named[n] = s
	namedPtr[c23.Addr(s.p)] = s
	return s.p
}

// SemClose is sem_close: the semaphore lives while it is linked or open.
func SemClose(p c23.Ptr) int32 {
	namedMu.Lock()
	defer namedMu.Unlock()
	s := namedPtr[c23.Addr(p)]
	if s == nil || s.refs == 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	s.refs--
	if s.refs == 0 && !semLinked(s) {
		delete(namedPtr, c23.Addr(p))
		SemDestroy(p)
	}
	return 0
}

// semLinked reports whether a name still names s. namedMu is held.
func semLinked(s *namedSem) bool {
	for _, t := range named {
		if t == s {
			return true
		}
	}
	return false
}

// SemUnlink is sem_unlink: the name goes, and the semaphore with the
// last sem_close.
func SemUnlink(name c23.Ptr) int32 {
	n := strings.TrimLeft(c23.GoString(name), "/")
	namedMu.Lock()
	defer namedMu.Unlock()
	s := named[n]
	if s == nil {
		c23.SetErrno(c23.ENOENT)
		return -1
	}
	delete(named, n)
	if s.refs == 0 {
		delete(namedPtr, c23.Addr(s.p))
		SemDestroy(s.p)
	}
	return 0
}
