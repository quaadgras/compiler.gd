// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package unistd

import (
	c23 "internal/c23"
	"io/fs"
	"os"
	"path/filepath"
)

// The *at functions name files relative to a directory descriptor's
// directory, by its name (c23.AtPath).

// Unlinkat is unlinkat: unlink, or rmdir with AT_REMOVEDIR.
func Unlinkat(dirfd int32, path c23.Ptr, flags int32) int32 {
	if flags&^c23.AT_REMOVEDIR != 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	name, ok := c23.AtName(dirfd, path)
	if !ok {
		return -1
	}
	fi, err := os.Lstat(name)
	if err != nil {
		return c23.ErrRet(err)
	}
	if flags&c23.AT_REMOVEDIR != 0 && !fi.IsDir() {
		c23.SetErrno(c23.ENOTDIR)
		return -1
	}
	if flags&c23.AT_REMOVEDIR == 0 && fi.IsDir() {
		c23.SetErrno(c23.EISDIR)
		return -1
	}
	return c23.ErrRet(os.Remove(name))
}

// Linkat is linkat: a link to the symbolic link old itself, or with
// AT_SYMLINK_FOLLOW to what it names.
func Linkat(olddirfd int32, old c23.Ptr, newdirfd int32, new c23.Ptr, flags int32) int32 {
	if flags&^(c23.AT_SYMLINK_FOLLOW|c23.AT_EMPTY_PATH) != 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	oname, ok := c23.AtName(olddirfd, old)
	if !ok {
		return -1
	}
	nname, ok := c23.AtName(newdirfd, new)
	if !ok {
		return -1
	}
	if flags&c23.AT_SYMLINK_FOLLOW != 0 {
		if fi, err := os.Lstat(oname); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
			t, err := filepath.EvalSymlinks(oname)
			if err != nil {
				return c23.ErrRet(err)
			}
			oname = t
		}
	}
	return c23.ErrRet(os.Link(oname, nname))
}

// Symlinkat is symlinkat: a link at path, relative to dirfd, whose
// contents are target (not resolved).
func Symlinkat(target c23.Ptr, dirfd int32, path c23.Ptr) int32 {
	name, ok := c23.AtName(dirfd, path)
	if !ok {
		return -1
	}
	return c23.ErrRet(os.Symlink(c23.GoString(target), name))
}

// Readlinkat is readlinkat.
func Readlinkat(dirfd int32, path, buf c23.Ptr, n uint64) int64 {
	name, ok := c23.AtName(dirfd, path)
	if !ok {
		return -1
	}
	s, err := os.Readlink(name)
	if err != nil {
		c23.SetErrnoOf(err)
		return -1
	}
	k := min(uint64(len(s)), n)
	copy(c23.BytesAt(buf, uintptr(k)), s)
	return int64(k)
}

// Faccessat is faccessat: access of path relative to dirfd, with the
// effective ids if AT_EACCESS (where the system can check with them;
// elsewhere, the real ones).
func Faccessat(dirfd int32, path c23.Ptr, mode, flags int32) int32 {
	if mode&^7 != 0 || flags&^(c23.AT_EACCESS|c23.AT_SYMLINK_NOFOLLOW) != 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	name, ok := c23.AtName(dirfd, path)
	if !ok {
		return -1
	}
	if flags&c23.AT_SYMLINK_NOFOLLOW != 0 {
		if fi, err := os.Lstat(name); err != nil {
			return c23.ErrRet(err)
		} else if fi.Mode()&fs.ModeSymlink != 0 {
			return 0 // a link's permissions are all granted
		}
	}
	if flags&c23.AT_EACCESS != 0 {
		return c23.ErrRet(eaccess(name, uint32(mode)))
	}
	return c23.ErrRet(access(name, uint32(mode)))
}

// Fchownat is fchownat: chown, or lchown with AT_SYMLINK_NOFOLLOW; with
// AT_EMPTY_PATH and an empty path, fchown of dirfd.
func Fchownat(dirfd int32, path c23.Ptr, uid, gid uint32, flags int32) int32 {
	if flags&^(c23.AT_SYMLINK_NOFOLLOW|c23.AT_EMPTY_PATH) != 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	if flags&c23.AT_EMPTY_PATH != 0 && c23.Load8(path) == 0 && dirfd != c23.AT_FDCWD {
		return Fchown(dirfd, uid, gid)
	}
	name, ok := c23.AtName(dirfd, path)
	if !ok {
		return -1
	}
	if flags&c23.AT_SYMLINK_NOFOLLOW != 0 {
		return c23.ErrRet(os.Lchown(name, cid(uid), cid(gid)))
	}
	return c23.ErrRet(os.Chown(name, cid(uid), cid(gid)))
}

// Fchdir is fchdir: the working directory becomes fd's directory (by
// its name).
func Fchdir(fd int32) int32 {
	dir, e := c23.FdDirName(fd)
	if e != 0 {
		c23.SetErrno(e)
		return -1
	}
	return c23.ErrRet(os.Chdir(dir))
}

// Dup3 is dup3: dup2, with FD_CLOEXEC if flags has O_CLOEXEC; fd and fd2
// must differ (EINVAL).
func Dup3(fd, fd2, flags int32) int32 {
	if flags&^c23.O_CLOEXEC != 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	c23.FdMu.Lock()
	defer c23.FdMu.Unlock()
	e := c23.FdEnt(fd)
	if e == nil || fd2 < 0 || fd2 >= 1<<20 {
		c23.SetErrno(c23.EBADF)
		return -1
	}
	if fd == fd2 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	for int(fd2) >= len(c23.FdTab) {
		c23.FdTab = append(c23.FdTab, nil)
	}
	if old := c23.FdTab[fd2]; old != nil {
		old.Of.Release()
	}
	e.Of.Refs++
	c23.FdTab[fd2] = &c23.FdEntry{Of: e.Of, Cloexec: flags&c23.O_CLOEXEC != 0}
	return fd2
}

// Pipe2 is pipe2: pipe, with O_CLOEXEC (both descriptors are
// FD_CLOEXEC) and O_NONBLOCK (both ends are non-blocking).
func Pipe2(fds c23.Ptr, flags int32) int32 {
	if flags&^(c23.O_CLOEXEC|c23.O_NONBLOCK) != 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	r, w, err := os.Pipe()
	if err != nil {
		c23.SetErrnoOf(err)
		return -1
	}
	nb := flags & c23.O_NONBLOCK
	cloexec := flags&c23.O_CLOEXEC != 0
	c23.Store32(fds, uint32(c23.FdInstallHeld(&c23.Ofile{F: r, Refs: 1, Flags: c23.O_RDONLY | nb}, cloexec)))
	c23.Store32(c23.Add(fds, 4), uint32(c23.FdInstallHeld(&c23.Ofile{F: w, Refs: 1, Flags: c23.O_WRONLY | nb}, cloexec)))
	return 0
}

// Fexecve is fexecve: execve of the program fd is a descriptor of, by the
// name it was opened by (the exec family's emulation).
func Fexecve(fd int32, argv, envp c23.Ptr) int32 {
	of := c23.FdFile(fd)
	if of == nil || of.F == nil {
		c23.SetErrno(c23.EBADF)
		return -1
	}
	return execProc(of.F.Name(), false, c23.GoStrings(argv), envOrEmpty(envp))
}

// POSIX_CLOSE_RESTART is posix_close's flag: 0, as on Linux, where
// close is not restarted.
const POSIX_CLOSE_RESTART = 0

// PosixClose is posix_close: close; another flag than 0 still closes fd,
// and fails with EINVAL.
func PosixClose(fd, flag int32) int32 {
	if r := Close(fd); r != 0 || flag == 0 {
		return r
	}
	c23.SetErrno(c23.EINVAL)
	return -1
}

// Setpgrp is setpgrp: setpgid(0, 0).
func Setpgrp() int32 { return Setpgid(0, 0) }
