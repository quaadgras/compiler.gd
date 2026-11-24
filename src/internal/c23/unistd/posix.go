// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package unistd

import (
	c23 "internal/c23"
	"io"
	"os"
	"runtime"
	"time"
)

// Read is read, a cancellation point when it is called (not while it
// waits).
func Read(fd int32, buf *c23.Void, n uint64) int64 {
	c23.TestCancel()
	f := c23.FdFile(fd)
	if f == nil {
		c23.SetErrno(c23.EBADF)
		return -1
	}
	k, err := f.Read(c23.BytesAt(buf, uintptr(n)))
	if err != nil && err != io.EOF {
		c23.SetErrnoOf(err)
		return -1
	}
	return int64(k)
}

// Write is write, a cancellation point when it is called (not while it
// waits).
func Write(fd int32, buf *c23.Void, n uint64) int64 {
	c23.TestCancel()
	f := c23.FdFile(fd)
	if f == nil {
		c23.SetErrno(c23.EBADF)
		return -1
	}
	k, err := f.Write(c23.BytesAt(buf, uintptr(n)))
	if err != nil && k == 0 {
		c23.SetErrnoOf(err)
		return -1
	}
	return int64(k)
}

func Close(fd int32) int32 {
	if err := c23.FdClose(fd); err != nil {
		c23.SetErrnoOf(err)
		return -1
	}
	return 0
}

func Lseek(fd int32, off int64, whence int32) int64 {
	f := c23.FdOS(fd)
	if f == nil {
		return -1
	}
	if whence < 0 || whence > 2 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	pos, err := f.Seek(off, int(whence))
	if err != nil {
		c23.SetErrnoOf(err)
		return -1
	}
	return pos
}

// Unlink is unlink, which does not remove directories (EISDIR, as Linux).
func Unlink(path *c23.Void) int32 {
	name := c23.GoString(path)
	if fi, err := os.Lstat(name); err == nil && fi.IsDir() {
		c23.SetErrno(c23.EISDIR)
		return -1
	}
	return c23.ErrRet(os.Remove(name))
}

// Rmdir is rmdir, which removes only directories.
func Rmdir(path *c23.Void) int32 {
	name := c23.GoString(path)
	fi, err := os.Lstat(name)
	if err != nil {
		return c23.ErrRet(err)
	}
	if !fi.IsDir() {
		c23.SetErrno(c23.ENOTDIR)
		return -1
	}
	return c23.ErrRet(os.Remove(name))
}

func Access(path *c23.Void, mode int32) int32 {
	if mode&^7 != 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	return c23.ErrRet(access(c23.GoString(path), uint32(mode)))
}

func Getpid() int32 { return int32(os.Getpid()) }

func Getppid() int32 { return int32(os.Getppid()) }

func Getuid() uint32 { return uint32(os.Getuid()) }

func Geteuid() uint32 {
	return uint32(os.Geteuid())
}

func Getgid() uint32 { return uint32(os.Getgid()) }

func Getegid() uint32 { return uint32(os.Getegid()) }

// Isatty is isatty: whether fd is a terminal (errno ENOTTY if not).
func Isatty(fd int32) int32 {
	f := c23.FdOS(fd)
	if f == nil {
		if c23.Errno() == c23.ESPIPE {
			c23.SetErrno(c23.ENOTTY)
		}
		return 0
	}
	if !isatty(f) {
		c23.SetErrno(c23.ENOTTY)
		return 0
	}
	return 1
}

// Getcwd is getcwd, which allocates the array if buf is null (as glibc:
// of size bytes, or as many as it takes if size is 0).
func Getcwd(buf *c23.Void, size uint64) *c23.Void {
	wd, err := os.Getwd()
	if err != nil {
		c23.SetErrnoOf(err)
		return (*c23.Void)(nil)
	}
	n := uint64(len(wd) + 1)
	if c23.Addr(buf) == 0 {
		if size == 0 {
			size = n
		}
		if n > size {
			c23.SetErrno(c23.ERANGE)
			return (*c23.Void)(nil)
		}
		buf = c23.Malloc(size)
	} else if size == 0 {
		c23.SetErrno(c23.EINVAL)
		return (*c23.Void)(nil)
	} else if n > size {
		c23.SetErrno(c23.ERANGE)
		return (*c23.Void)(nil)
	}
	copy(c23.BytesAt(buf, uintptr(n)), wd+"\x00")
	return buf
}

func Chdir(path *c23.Void) int32 { return c23.ErrRet(os.Chdir(c23.GoString(path))) }

// Sleep is sleep, a cancellation point.
func Sleep(s uint32) uint32 {
	c23.CancelSleep(time.Duration(s) * time.Second)
	return 0
}

// Usleep is usleep, a cancellation point.
func Usleep(us uint32) int32 {
	c23.CancelSleep(time.Duration(us) * time.Microsecond)
	return 0
}

// ExitRaw is _exit: the program ends at once, without flushing C's
// streams.
func ExitRaw(status int32) { os.Exit(int(status)) }

func Dup(fd int32) int32 { return c23.DupFrom(fd, 0, false) }

func Dup2(fd, fd2 int32) int32 {
	c23.FdMu.Lock()
	defer c23.FdMu.Unlock()
	e := c23.FdEnt(fd)
	if e == nil || fd2 < 0 || fd2 >= 1<<20 {
		c23.SetErrno(c23.EBADF)
		return -1
	}
	if fd == fd2 {
		return fd2
	}
	for int(fd2) >= len(c23.FdTab) {
		c23.FdTab = append(c23.FdTab, nil)
	}
	if old := c23.FdTab[fd2]; old != nil {
		old.Of.Release()
	}
	e.Of.Refs++
	c23.FdTab[fd2] = &c23.FdEntry{Of: e.Of}
	return fd2
}

// Pipe is pipe: fds[0] reads what fds[1] writes.
func Pipe(fds *c23.Void) int32 {
	r, w, err := os.Pipe()
	if err != nil {
		c23.SetErrnoOf(err)
		return -1
	}
	c23.Store32(fds, uint32(c23.FdNew(r, c23.O_RDONLY)))
	c23.Store32(c23.Add(fds, 4), uint32(c23.FdNew(w, c23.O_WRONLY)))
	return 0
}

const _SC_CLK_TCK = 2

const _SC_PAGESIZE = 30

const _SC_NPROCESSORS_CONF = 83

const _SC_NPROCESSORS_ONLN = 84

func Sysconf(name int32) int64 {
	switch name {
	case _SC_CLK_TCK:
		return 100
	case _SC_PAGESIZE:
		return int64(os.Getpagesize())
	case _SC_NPROCESSORS_CONF, _SC_NPROCESSORS_ONLN:
		return int64(runtime.NumCPU())
	}
	c23.SetErrno(c23.EINVAL)
	return -1
}

// Gethostname is gethostname: a name that does not fit is truncated,
// and the result is -1 with errno ENAMETOOLONG, as glibc.
func Gethostname(name *c23.Void, n uint64) int32 {
	h, err := os.Hostname()
	if err != nil {
		c23.SetErrnoOf(err)
		return -1
	}
	b := append([]byte(h), 0)
	if uint64(len(b)) > n {
		copy(c23.BytesAt(name, uintptr(n)), b)
		c23.SetErrno(c23.ENAMETOOLONG)
		return -1
	}
	copy(c23.BytesAt(name, uintptr(len(b))), b)
	return 0
}
