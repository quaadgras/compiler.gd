// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stdio

import (
	c23 "internal/c23"
	"os"
	"sync"
	"time"
)

// A stream's lock (flockfile): a recursive mutex, made when first
// locked. The streams' functions do not take it themselves, so the
// _unlocked functions are the others.
var fileLocks sync.Map // *c23.File -> *c23.Mutex

func fileLock(p *c23.Void) *c23.Mutex {
	f := c23.FileOf(p)
	if m, ok := fileLocks.Load(f); ok {
		return m.(*c23.Mutex)
	}
	m, _ := fileLocks.LoadOrStore(f, &c23.Mutex{Kind: c23.PTHREAD_MUTEX_RECURSIVE})
	return m.(*c23.Mutex)
}

// Flockfile is flockfile: the calling thread holds the stream's lock,
// once more if it holds it already.
func Flockfile(p *c23.Void) { fileLock(p).Lock(c23.LockWait, time.Time{}) }

// Ftrylockfile is ftrylockfile: flockfile if the lock is free or the
// thread's (0), else nonzero.
func Ftrylockfile(p *c23.Void) int32 { return fileLock(p).Lock(c23.LockTry, time.Time{}) }

// Funlockfile is funlockfile: one of the thread's holds of the lock is
// released.
func Funlockfile(p *c23.Void) {
	m := fileLock(p)
	if m.Owner.Load() != c23.GSelf() {
		return
	}
	if m.Count--; m.Count > 0 {
		return
	}
	m.Owner.Store(nil)
	m.Mu.Unlock()
}

// Renameat is renameat: rename of names relative to directory
// descriptors (c23.AtPath).
func Renameat(olddirfd int32, old *c23.Void, newdirfd int32, new *c23.Void) int32 {
	oname, ok := c23.AtName(olddirfd, old)
	if !ok {
		return -1
	}
	nname, ok := c23.AtName(newdirfd, new)
	if !ok {
		return -1
	}
	return c23.ErrRet(os.Rename(oname, nname))
}

// Ctermid is ctermid: "/dev/tty", in s if it is not null (of L_ctermid
// bytes), else in an array of its own.
func Ctermid(s *c23.Void) *c23.Void {
	if c23.Addr(s) == 0 {
		return c23.CString("/dev/tty")
	}
	copy(c23.BytesAt(s, 9), "/dev/tty\x00")
	return s
}
