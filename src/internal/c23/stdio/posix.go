// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stdio

import (
	c23 "internal/c23"
	"os"
)

// cflags are the flags F_GETFL returns of a file os.OpenFile opened with
// flag.
func cflags(flag int) int32 {
	var r int32
	switch flag & (os.O_RDONLY | os.O_WRONLY | os.O_RDWR) {
	case os.O_WRONLY:
		r = c23.O_WRONLY
	case os.O_RDWR:
		r = c23.O_RDWR
	}
	if flag&os.O_APPEND != 0 {
		r |= c23.O_APPEND
	}
	if flag&os.O_SYNC != 0 {
		r |= c23.O_SYNC
	}
	return r | c23.O_LARGEFILE
}

// fdSet makes fd, which is free, a descriptor of f.
func fdSet(fd int32, f *os.File, flags int32) {
	c23.FdMu.Lock()
	defer c23.FdMu.Unlock()
	for int(fd) >= len(c23.FdTab) {
		c23.FdTab = append(c23.FdTab, nil)
	}
	c23.FdTab[fd] = &c23.FdEntry{Of: &c23.Ofile{F: f, Refs: 1, Flags: flags}}
}

// Fileno is fileno.
func Fileno(p c23.Ptr) int32 {
	f := c23.FileOf(p)
	if c23.FdFile(f.Fd) == nil {
		c23.SetErrno(c23.EBADF)
		return -1
	}
	return f.Fd
}

// Fdopen is fdopen: a stream of fd, which fclose closes.
func Fdopen(fd int32, mode c23.Ptr) c23.Ptr {
	_, rd, wr, ok := openMode(c23.GoString(mode))
	if !ok {
		c23.SetErrno(c23.EINVAL)
		return c23.Ptr(nil)
	}
	of := c23.FdFile(fd)
	if of == nil {
		c23.SetErrno(c23.EBADF)
		return c23.Ptr(nil)
	}
	if acc := of.Flags & c23.O_ACCMODE; rd && acc == c23.O_WRONLY || wr && acc == c23.O_RDONLY {
		c23.SetErrno(c23.EINVAL)
		return c23.Ptr(nil)
	}
	return c23.NewFile(fd, rd, wr)
}
