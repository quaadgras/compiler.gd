// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"os"
	"sync"
)

// POSIX's file descriptors (unistd.h, fcntl.h): small ints that name
// os.Files in a table of the program's own, which C's streams use too.
// The numbers are not the system's: they are allocated as POSIX says,
// the lowest free first, with 0, 1 and 2 standard input, output and
// error.

// fcntl.h's values: Linux's.
const (
	O_RDONLY    = 0
	O_WRONLY    = 1
	O_RDWR      = 2
	O_ACCMODE   = 3
	O_CREAT     = 0o100
	O_EXCL      = 0o200
	O_NOCTTY    = 0o400
	O_TRUNC     = 0o1000
	O_APPEND    = 0o2000
	O_NONBLOCK  = 0o4000
	O_DSYNC     = 0o10000
	O_LARGEFILE = 0o100000
	O_DIRECTORY = 0o200000
	O_NOFOLLOW  = 0o400000
	O_CLOEXEC   = 0o2000000
	O_SYNC      = 0o4010000
)

// An ofile is an open file: what the descriptors dup makes share.
type Ofile struct {
	F     *os.File
	Sp    Special    // what it is if not an os.File (f is nil)
	ra    *ReadAhead // f read ahead, once poll or O_NONBLOCK needs it (special.go)
	Refs  int
	Flags int32 // as F_GETFL returns them
}

type FdEntry struct {
	Of      *Ofile
	Cloexec bool
}

var (
	FdMu  sync.Mutex
	FdTab = []*FdEntry{
		{Of: &Ofile{F: os.Stdin, Refs: 1, Flags: O_RDONLY}},
		{Of: &Ofile{F: os.Stdout, Refs: 1, Flags: O_WRONLY}},
		{Of: &Ofile{F: os.Stderr, Refs: 1, Flags: O_WRONLY}},
	}
)

// fdInstall puts e at the lowest free descriptor from min on.
// fdMu must be held.
func fdInstall(e *FdEntry, min int32) int32 {
	fd := int(min)
	for fd < len(FdTab) && FdTab[fd] != nil {
		fd++
	}
	for fd >= len(FdTab) {
		FdTab = append(FdTab, nil)
	}
	FdTab[fd] = e
	return int32(fd)
}

// fdNew returns a new descriptor of f.
func FdNew(f *os.File, flags int32) int32 {
	FdMu.Lock()
	defer FdMu.Unlock()
	return fdInstall(&FdEntry{Of: &Ofile{F: f, Refs: 1, Flags: flags}}, 0)
}

// fdEnt returns fd's entry, or nil (errno EBADF).
// fdMu must be held.
func FdEnt(fd int32) *FdEntry {
	if fd < 0 || int(fd) >= len(FdTab) || FdTab[fd] == nil {
		return nil
	}
	return FdTab[fd]
}

// fdFile returns fd's open file, or nil.
func FdFile(fd int32) *Ofile {
	FdMu.Lock()
	defer FdMu.Unlock()
	if e := FdEnt(fd); e != nil {
		return e.Of
	}
	return nil
}

// fdOS returns fd's os.File, or nil and sets errno to EBADF (or ESPIPE
// for a socket).
func FdOS(fd int32) *os.File {
	if of := FdFile(fd); of != nil {
		if of.Sp != nil {
			SetErrno(ESPIPE)
			return nil
		}
		return of.F
	}
	SetErrno(EBADF)
	return nil
}

// release drops a descriptor of of: the last closes the file, unless it
// is one of os's standard files. fdMu must be held.
func (of *Ofile) Release() error {
	of.Refs--
	if of.Refs > 0 || of.F == os.Stdin || of.F == os.Stdout || of.F == os.Stderr {
		return nil
	}
	if of.Sp != nil {
		return of.Sp.Close()
	}
	return of.F.Close()
}

// fdClose frees fd.
func FdClose(fd int32) error {
	FdMu.Lock()
	defer FdMu.Unlock()
	e := FdEnt(fd)
	if e == nil {
		return ErrnoErr(EBADF)
	}
	FdTab[fd] = nil
	return e.Of.Release()
}

// fdIO reads and writes a stream's file: its descriptor's, whatever that
// is now (dup2 may change it).
type FdIO struct{ F *File }

func (d FdIO) Write(b []byte) (int, error) {
	if d.F.Mem != nil {
		return d.F.Mem.Write(b)
	}
	of := FdFile(d.F.Fd)
	if of == nil {
		return 0, ErrnoErr(EBADF)
	}
	return of.Write(b)
}

func (d FdIO) Read(b []byte) (int, error) {
	if d.F.Mem != nil {
		return d.F.Mem.Read(b)
	}
	of := FdFile(d.F.Fd)
	if of == nil {
		return 0, ErrnoErr(EBADF)
	}
	return of.Read(b)
}

// osFile returns the stream's os.File, or nil.
func (f *File) OsFile() *os.File {
	if f.Mem != nil {
		return nil
	}
	if of := FdFile(f.Fd); of != nil {
		return of.F
	}
	return nil
}

func (f *File) Seek(off int64, whence int) (int64, error) {
	if f.Mem != nil {
		return f.Mem.Seek(off, whence)
	}
	of := f.OsFile()
	if of == nil {
		return 0, ErrnoErr(EBADF)
	}
	return of.Seek(off, whence)
}

// unistd.h

// errRet returns 0 for no error, or -1 with errno set to err's.
func ErrRet(err error) int32 {
	if err != nil {
		SetErrnoOf(err)
		return -1
	}
	return 0
}

// access's modes.
const (
	F_OK = 0
	X_OK = 1
	W_OK = 2
	R_OK = 4
)

// dupFrom makes the lowest free descriptor from min on one of fd's file.
func DupFrom(fd, min int32, cloexec bool) int32 {
	FdMu.Lock()
	defer FdMu.Unlock()
	e := FdEnt(fd)
	if e == nil || min < 0 {
		SetErrno(If(e == nil, int32(EBADF), EINVAL))
		return -1
	}
	e.Of.Refs++
	return fdInstall(&FdEntry{Of: e.Of, Cloexec: cloexec}, min)
}

// sysconf's names.
const ()

// fcntl.h

func Open(name string, flags int32, mode uint32) int32 {
	var flag int
	switch flags & O_ACCMODE {
	case O_RDONLY:
		flag = os.O_RDONLY
	case O_WRONLY:
		flag = os.O_WRONLY
	case O_RDWR:
		flag = os.O_RDWR
	default:
		SetErrno(EINVAL)
		return -1
	}
	for _, m := range [...]struct {
		c int32
		g int
	}{{O_CREAT, os.O_CREATE}, {O_EXCL, os.O_EXCL}, {O_TRUNC, os.O_TRUNC}, {O_APPEND, os.O_APPEND}, {O_SYNC, os.O_SYNC}} {
		if flags&m.c == m.c {
			flag |= m.g
		}
	}
	if flags&O_NOFOLLOW != 0 {
		if fi, err := os.Lstat(name); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			SetErrno(ELOOP)
			return -1
		}
	}
	f, err := os.OpenFile(name, flag, GoMode(mode))
	if err != nil {
		SetErrnoOf(err)
		return -1
	}
	if flags&O_DIRECTORY != 0 {
		if fi, err := f.Stat(); err != nil || !fi.IsDir() {
			f.Close()
			SetErrno(ENOTDIR)
			return -1
		}
	}
	kept := flags &^ (O_CREAT | O_EXCL | O_NOCTTY | O_TRUNC | O_CLOEXEC)
	FdMu.Lock()
	defer FdMu.Unlock()
	return fdInstall(&FdEntry{Of: &Ofile{F: f, Refs: 1, Flags: kept | O_LARGEFILE}, Cloexec: flags&O_CLOEXEC != 0}, 0)
}

// goMode is the os.FileMode of C's permission bits.
func GoMode(mode uint32) os.FileMode {
	m := os.FileMode(mode & 0o777)
	if mode&0o4000 != 0 {
		m |= os.ModeSetuid
	}
	if mode&0o2000 != 0 {
		m |= os.ModeSetgid
	}
	if mode&0o1000 != 0 {
		m |= os.ModeSticky
	}
	return m
}
