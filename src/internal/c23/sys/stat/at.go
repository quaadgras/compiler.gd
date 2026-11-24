// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stat

import (
	c23 "internal/c23"
	"io/fs"
	"os"
	"time"
)

// The *at functions name files relative to a directory descriptor's
// directory, by its name (c23.AtPath).

// Fstatat is fstatat: stat, or lstat with AT_SYMLINK_NOFOLLOW, of path
// relative to dirfd; with AT_EMPTY_PATH and an empty path, fstat of dirfd.
func Fstatat(dirfd int32, path, buf *c23.Void, flags int32) int32 {
	if flags&^(c23.AT_SYMLINK_NOFOLLOW|c23.AT_EMPTY_PATH) != 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	if flags&c23.AT_EMPTY_PATH != 0 && c23.Load8(path) == 0 {
		if dirfd == c23.AT_FDCWD {
			fi, err := os.Stat(".")
			return statRet(buf, fi, err)
		}
		return Fstat(dirfd, buf)
	}
	name, ok := c23.AtName(dirfd, path)
	if !ok {
		return -1
	}
	var fi fs.FileInfo
	var err error
	if flags&c23.AT_SYMLINK_NOFOLLOW != 0 {
		fi, err = os.Lstat(name)
	} else {
		fi, err = os.Stat(name)
	}
	return statRet(buf, fi, err)
}

// Mkdirat is mkdirat.
func Mkdirat(dirfd int32, path *c23.Void, mode uint32) int32 {
	name, ok := c23.AtName(dirfd, path)
	if !ok {
		return -1
	}
	return c23.ErrRet(os.Mkdir(name, c23.GoMode(mode)))
}

// Fchmodat is fchmodat. With AT_SYMLINK_NOFOLLOW, a symbolic link's mode
// cannot be changed (EOPNOTSUPP), as on Linux.
func Fchmodat(dirfd int32, path *c23.Void, mode uint32, flags int32) int32 {
	if flags&^c23.AT_SYMLINK_NOFOLLOW != 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	name, ok := c23.AtName(dirfd, path)
	if !ok {
		return -1
	}
	if flags&c23.AT_SYMLINK_NOFOLLOW != 0 {
		fi, err := os.Lstat(name)
		if err != nil {
			return c23.ErrRet(err)
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			c23.SetErrno(c23.EOPNOTSUPP)
			return -1
		}
	}
	return c23.ErrRet(os.Chmod(name, c23.GoMode(mode)))
}

// utimensat's special nanoseconds.
const (
	UTIME_NOW  = 1<<30 - 1
	UTIME_OMIT = 1<<30 - 2
)

// loadTimes reads utimensat's times: two struct timespecs, or now if
// times is null. A zero time.Time is one not to change (UTIME_OMIT).
func loadTimes(times *c23.Void) (at, mt time.Time, ok bool) {
	now := time.Now()
	if c23.Addr(times) == 0 {
		return now, now, true
	}
	var ts [2]time.Time
	for i := range ts {
		p := c23.Add(times, 16*i)
		sec, nsec := int64(c23.Load64(p)), c23.LoadLong(c23.Add(p, 8))
		switch {
		case nsec == UTIME_NOW:
			ts[i] = now
		case nsec == UTIME_OMIT:
		case nsec < 0 || nsec > 999999999:
			c23.SetErrno(c23.EINVAL)
			return at, mt, false
		default:
			ts[i] = time.Unix(sec, nsec)
		}
	}
	return ts[0], ts[1], true
}

// Utimensat is utimensat: the access and modification times of path
// relative to dirfd, to nanoseconds. With AT_SYMLINK_NOFOLLOW, a
// symbolic link's own times are set only on Linux (EOPNOTSUPP elsewhere).
func Utimensat(dirfd int32, path, times *c23.Void, flags int32) int32 {
	if flags&^c23.AT_SYMLINK_NOFOLLOW != 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	at, mt, ok := loadTimes(times)
	if !ok {
		return -1
	}
	name, ok := c23.AtName(dirfd, path)
	if !ok {
		return -1
	}
	if at.IsZero() && mt.IsZero() {
		_, err := os.Lstat(name)
		return c23.ErrRet(err)
	}
	if flags&c23.AT_SYMLINK_NOFOLLOW != 0 {
		if fi, err := os.Lstat(name); err != nil {
			return c23.ErrRet(err)
		} else if fi.Mode()&fs.ModeSymlink != 0 {
			return c23.ErrRet(lutimes(name, at, mt))
		}
	}
	return c23.ErrRet(os.Chtimes(name, at, mt))
}

// Futimens is futimens: the times of fd's file, which are set by its
// name (the name it was opened by).
func Futimens(fd int32, times *c23.Void) int32 {
	f := c23.FdOS(fd)
	if f == nil {
		return -1
	}
	at, mt, ok := loadTimes(times)
	if !ok {
		return -1
	}
	if at.IsZero() && mt.IsZero() {
		return 0
	}
	return c23.ErrRet(os.Chtimes(f.Name(), at, mt))
}

// Mkfifo is mkfifo: the system's, where it has named pipes (ENOSYS
// elsewhere).
func Mkfifo(path *c23.Void, mode uint32) int32 {
	return c23.ErrRet(mkfifo(c23.GoString(path), mode&0o7777))
}

// Mkfifoat is mkfifoat.
func Mkfifoat(dirfd int32, path *c23.Void, mode uint32) int32 {
	name, ok := c23.AtName(dirfd, path)
	if !ok {
		return -1
	}
	return c23.ErrRet(mkfifo(name, mode&0o7777))
}

// Mknod is mknod: the system's (ENOSYS where it has none). A regular
// file or a FIFO needs no privilege; a device does.
func Mknod(path *c23.Void, mode uint32, dev uint64) int32 {
	return c23.ErrRet(mknod(c23.GoString(path), mode, dev))
}

// Mknodat is mknodat.
func Mknodat(dirfd int32, path *c23.Void, mode uint32, dev uint64) int32 {
	name, ok := c23.AtName(dirfd, path)
	if !ok {
		return -1
	}
	return c23.ErrRet(mknod(name, mode, dev))
}
