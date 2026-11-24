// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package unistd

import (
	c23 "internal/c23"
	"io"
	"os"
)

// Ftruncate is ftruncate.
func Ftruncate(fd int32, length int64) int32 {
	f := c23.FdOS(fd)
	if f == nil {
		return -1
	}
	if length < 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	return c23.ErrRet(f.Truncate(length))
}

// Truncate is truncate.
func Truncate(path *c23.Void, length int64) int32 {
	if length < 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	return c23.ErrRet(os.Truncate(c23.GoString(path), length))
}

// Fsync is fsync.
func Fsync(fd int32) int32 {
	f := c23.FdOS(fd)
	if f == nil {
		return -1
	}
	return c23.ErrRet(f.Sync())
}

// Fdatasync is fdatasync, which syncs the metadata too.
func Fdatasync(fd int32) int32 { return Fsync(fd) }

// cid is the int os takes for a uid_t or gid_t: -1 (unchanged) for
// (uid_t)-1.
func cid(id uint32) int { return int(int32(id)) }

func Chown(path *c23.Void, uid, gid uint32) int32 {
	return c23.ErrRet(os.Chown(c23.GoString(path), cid(uid), cid(gid)))
}

func Lchown(path *c23.Void, uid, gid uint32) int32 {
	return c23.ErrRet(os.Lchown(c23.GoString(path), cid(uid), cid(gid)))
}

func Fchown(fd int32, uid, gid uint32) int32 {
	f := c23.FdOS(fd)
	if f == nil {
		return -1
	}
	return c23.ErrRet(f.Chown(cid(uid), cid(gid)))
}

// Readlink is readlink: the link's contents, truncated to n bytes and not
// terminated.
func Readlink(path, buf *c23.Void, n uint64) int64 {
	s, err := os.Readlink(c23.GoString(path))
	if err != nil {
		c23.SetErrnoOf(err)
		return -1
	}
	k := min(uint64(len(s)), n)
	copy(c23.BytesAt(buf, uintptr(k)), s)
	return int64(k)
}

func Symlink(target, link *c23.Void) int32 {
	return c23.ErrRet(os.Symlink(c23.GoString(target), c23.GoString(link)))
}

func Link(old, new *c23.Void) int32 {
	return c23.ErrRet(os.Link(c23.GoString(old), c23.GoString(new)))
}

// Pread is pread: it reads at off, and the file's offset stays.
func Pread(fd int32, buf *c23.Void, n uint64, off int64) int64 {
	f := c23.FdOS(fd)
	if f == nil {
		return -1
	}
	if off < 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	k, err := f.ReadAt(c23.BytesAt(buf, uintptr(n)), off)
	if err != nil && err != io.EOF {
		c23.SetErrnoOf(err)
		return -1
	}
	return int64(k)
}

// Pwrite is pwrite: it writes at off, and the file's offset stays. A file
// opened O_APPEND is appended to, as Linux does.
func Pwrite(fd int32, buf *c23.Void, n uint64, off int64) int64 {
	of := c23.FdFile(fd)
	if of == nil {
		c23.SetErrno(c23.EBADF)
		return -1
	}
	if off < 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	b := c23.BytesAt(buf, uintptr(n))
	var k int
	var err error
	if of.Flags&c23.O_APPEND != 0 {
		k, err = of.F.Write(b)
	} else {
		k, err = of.F.WriteAt(b, off)
	}
	if err != nil && k == 0 {
		c23.SetErrnoOf(err)
		return -1
	}
	return int64(k)
}

func Getpagesize() int32 { return int32(os.Getpagesize()) }
