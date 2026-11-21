// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stat

import (
	c23 "internal/c23"
	"io/fs"
	"os"
	"reflect"
)

const S_IFMT = 0o170000

const S_IFSOCK = 0o140000

const S_IFLNK = 0o120000

const S_IFREG = 0o100000

const S_IFBLK = 0o060000

const S_IFDIR = 0o040000

const S_IFCHR = 0o020000

const S_IFIFO = 0o010000

// cMode is the st_mode of a file of Go's mode m.
func cMode(m fs.FileMode) uint32 {
	r := uint32(m.Perm())
	if m&fs.ModeSetuid != 0 {
		r |= 0o4000
	}
	if m&fs.ModeSetgid != 0 {
		r |= 0o2000
	}
	if m&fs.ModeSticky != 0 {
		r |= 0o1000
	}
	switch {
	case m.IsDir():
		r |= S_IFDIR
	case m&fs.ModeSymlink != 0:
		r |= S_IFLNK
	case m&fs.ModeNamedPipe != 0:
		r |= S_IFIFO
	case m&fs.ModeSocket != 0:
		r |= S_IFSOCK
	case m&fs.ModeCharDevice != 0:
		r |= S_IFCHR
	case m&fs.ModeDevice != 0:
		r |= S_IFBLK
	default:
		r |= S_IFREG
	}
	return r
}

// sysTime returns a timespec field of the system's stat structure: one
// of the names, a struct of Sec and Nsec (Linux's Atim, Darwin's
// Atimespec), or name and name+"Nsec" (js's Atime and AtimeNsec).
func sysTime(sys reflect.Value, names ...string) (sec, nsec int64, ok bool) {
	if !sys.IsValid() {
		return 0, 0, false
	}
	for _, n := range names {
		f := sys.FieldByName(n)
		if f.IsValid() && f.Kind() == reflect.Struct {
			s, ok1 := c23.SysField(f, "Sec")
			ns, ok2 := c23.SysField(f, "Nsec")
			if ok1 && ok2 {
				return int64(s), int64(ns), true
			}
		}
	}
	if s, ok := c23.SysField(sys, names[0]); ok {
		if ns, ok := c23.SysField(sys, names[0]+"Nsec"); ok {
			return int64(s), int64(ns), true
		}
	}
	return 0, 0, false
}

// StatSize is the size of struct stat: the fields are where they are
// on LP64 with 4-byte longs too (st_blocks and the timespecs are 8-byte
// aligned), but for the three longs at the end.
const StatSize = uint64((120 + 3*c23.LongSize + 7) &^ 7)

// FillStat stores fi as Linux's struct stat at buf:
//
//	dev_t st_dev; ino_t st_ino; nlink_t st_nlink;	// 0, 8, 16
//	mode_t st_mode; uid_t st_uid; gid_t st_gid;	// 24, 28, 32
//	dev_t st_rdev; off_t st_size;			// 40, 48
//	blksize_t st_blksize; blkcnt_t st_blocks;	// 56, 64
//	struct timespec st_atim, st_mtim, st_ctim;	// 72, 88, 104
//	long __unused[3];				// 120 (144 bytes; 136 on ILP32)
//
// What the system's stat structure does not have is made up: one link,
// 4096-byte blocks, and the modification time for the others.
//
// FillStat is exported for the other headers' functions that pass a
// struct stat (ftw.h's).
func FillStat(buf c23.Ptr, fi fs.FileInfo) {
	c23.Memset(buf, 0, StatSize)
	sys := reflect.Indirect(reflect.ValueOf(fi.Sys()))
	if sys.IsValid() && sys.Kind() != reflect.Struct {
		sys = reflect.Value{}
	}
	get := func(def uint64, names ...string) uint64 {
		if v, ok := c23.SysField(sys, names...); ok {
			return v
		}
		return def
	}
	mode := cMode(fi.Mode())
	if m, ok := c23.SysField(sys, "Mode"); ok && m&S_IFMT != 0 {
		mode = uint32(m)
	}
	size := fi.Size()
	c23.Store64(buf, get(0, "Dev"))
	c23.Store64(c23.Add(buf, 8), get(0, "Ino"))
	c23.Store64(c23.Add(buf, 16), get(1, "Nlink"))
	c23.Store32(c23.Add(buf, 24), mode)
	c23.Store32(c23.Add(buf, 28), uint32(get(uint64(uint32(os.Getuid())), "Uid")))
	c23.Store32(c23.Add(buf, 32), uint32(get(uint64(uint32(os.Getgid())), "Gid")))
	c23.Store64(c23.Add(buf, 40), get(0, "Rdev"))
	c23.Store64(c23.Add(buf, 48), uint64(size))
	c23.StoreLong(c23.Add(buf, 56), int64(get(4096, "Blksize")))
	c23.Store64(c23.Add(buf, 64), get(uint64(size+511)/512, "Blocks"))
	mt := fi.ModTime()
	ts := func(off int, names ...string) {
		s, ns, ok := sysTime(sys, names...)
		if !ok {
			s, ns = mt.Unix(), int64(mt.Nanosecond())
		}
		c23.Store64(c23.Add(buf, off), uint64(s))
		c23.StoreLong(c23.Add(buf, off+8), int64(ns))
	}
	ts(72, "Atim", "Atimespec", "Atime")
	ts(88, "Mtim", "Mtimespec", "Mtime")
	ts(104, "Ctim", "Ctimespec", "Ctime")
}

func statRet(buf c23.Ptr, fi fs.FileInfo, err error) int32 {
	if err != nil {
		c23.SetErrnoOf(err)
		return -1
	}
	FillStat(buf, fi)
	return 0
}

func Stat(path, buf c23.Ptr) int32 {
	fi, err := os.Stat(c23.GoString(path))
	return statRet(buf, fi, err)
}

func Lstat(path, buf c23.Ptr) int32 {
	fi, err := os.Lstat(c23.GoString(path))
	return statRet(buf, fi, err)
}

func Fstat(fd int32, buf c23.Ptr) int32 {
	if of := c23.FdFile(fd); of != nil && of.Sp != nil {
		c23.Memset(buf, 0, StatSize)
		c23.Store64(c23.Add(buf, 16), 1)
		c23.Store32(c23.Add(buf, 24), of.Sp.Mode())
		c23.Store32(c23.Add(buf, 28), uint32(os.Getuid()))
		c23.Store32(c23.Add(buf, 32), uint32(os.Getgid()))
		c23.StoreLong(c23.Add(buf, 56), 4096)
		return 0
	}
	f := c23.FdOS(fd)
	if f == nil {
		return -1
	}
	fi, err := f.Stat()
	return statRet(buf, fi, err)
}

func Mkdir(path c23.Ptr, mode uint32) int32 {
	return c23.ErrRet(os.Mkdir(c23.GoString(path), c23.GoMode(mode)))
}

func Chmod(path c23.Ptr, mode uint32) int32 {
	return c23.ErrRet(os.Chmod(c23.GoString(path), c23.GoMode(mode)))
}

func Fchmod(fd int32, mode uint32) int32 {
	f := c23.FdOS(fd)
	if f == nil {
		return -1
	}
	return c23.ErrRet(f.Chmod(c23.GoMode(mode)))
}

// Umask is umask: the system's, where it has one; elsewhere, only
// recorded.
func Umask(mask uint32) uint32 { return umask(mask & 0o777) }
