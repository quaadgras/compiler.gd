// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package statvfs is sys/statvfs.h's and sys/statfs.h's (sys/vfs.h's)
// functions for cmd/cc: the system's statfs where package syscall has
// it (fs_unix.go), ENOSYS elsewhere.
package statvfs

import (
	c23 "internal/c23"
)

const ST_RDONLY = 1

const ST_NOSUID = 2

// fsInfo is what the system says of a file system.
type fsInfo struct {
	typ, bsize, frsize                  uint64
	blocks, bfree, bavail, files, ffree uint64
	fsid                                [2]int32
	namemax, flags                      uint64
}

// base is the offset of the block counts in both structures: after two
// longs, 8-byte aligned.
var base = (2*int(c23.LongSize) + 7) &^ 7

func store(buf *c23.Void, fi fsInfo, err error, size int, fill func(*c23.Void, fsInfo)) int32 {
	if err != nil {
		c23.SetErrnoOf(err)
		return -1
	}
	c23.Memset(buf, 0, uint64(size))
	fill(buf, fi)
	return 0
}

// statvfsSize is the size of struct statvfs: 112 bytes on LP64, 96 on
// ILP32.
var statvfsSize = base + 56 + 2*int(c23.LongSize) + 24

// fillStatvfs stores fi as Linux's struct statvfs:
//
//	unsigned long f_bsize, f_frsize;
//	fsblkcnt_t f_blocks, f_bfree, f_bavail;	// 64 bits, at base
//	fsfilcnt_t f_files, f_ffree, f_favail;
//	unsigned long f_fsid; (int __f_unused on ILP32)	// base+48
//	unsigned long f_flag, f_namemax;		// base+56
//	int __f_spare[6];
func fillStatvfs(buf *c23.Void, fi fsInfo) {
	l := int(c23.LongSize)
	c23.StoreLong(buf, int64(fi.bsize))
	c23.StoreLong(c23.Add(buf, l), int64(fi.frsize))
	for i, v := range []uint64{fi.blocks, fi.bfree, fi.bavail, fi.files, fi.ffree, fi.ffree} {
		c23.Store64(c23.Add(buf, base+8*i), v)
	}
	fsid := uint64(uint32(fi.fsid[0])) | uint64(uint32(fi.fsid[1]))<<32
	c23.StoreLong(c23.Add(buf, base+48), int64(fsid))
	c23.StoreLong(c23.Add(buf, base+56), int64(fi.flags))
	c23.StoreLong(c23.Add(buf, base+56+l), int64(fi.namemax))
}

// statfsSize is the size of struct statfs: 120 bytes on LP64, 88 on
// ILP32.
var statfsSize = (base + 48 + 7*int(c23.LongSize) + 7) &^ 7

// fillStatfs stores fi as Linux's struct statfs:
//
//	unsigned long f_type, f_bsize;
//	fsblkcnt_t f_blocks, f_bfree, f_bavail;	// 64 bits, at base
//	fsfilcnt_t f_files, f_ffree;
//	fsid_t f_fsid;				// base+40
//	unsigned long f_namelen, f_frsize, f_flags, f_spare[4];	// base+48
func fillStatfs(buf *c23.Void, fi fsInfo) {
	l := int(c23.LongSize)
	c23.StoreLong(buf, int64(fi.typ))
	c23.StoreLong(c23.Add(buf, l), int64(fi.bsize))
	for i, v := range []uint64{fi.blocks, fi.bfree, fi.bavail, fi.files, fi.ffree} {
		c23.Store64(c23.Add(buf, base+8*i), v)
	}
	c23.Store32(c23.Add(buf, base+40), uint32(fi.fsid[0]))
	c23.Store32(c23.Add(buf, base+44), uint32(fi.fsid[1]))
	c23.StoreLong(c23.Add(buf, base+48), int64(fi.namemax))
	c23.StoreLong(c23.Add(buf, base+48+l), int64(fi.frsize))
	c23.StoreLong(c23.Add(buf, base+48+2*l), int64(fi.flags))
}

// fdInfo is fsInfo of a descriptor's file.
func fdInfo(fd int32) (fsInfo, bool, error) {
	f := c23.FdOS(fd)
	if f == nil {
		return fsInfo{}, false, nil
	}
	fi, err := fstatfs(f)
	return fi, true, err
}

// Statvfs is statvfs: f_flag has ST_RDONLY and ST_NOSUID, and Linux's
// other ST_ flags there.
func Statvfs(path, buf *c23.Void) int32 {
	fi, err := statfs(c23.GoString(path))
	return store(buf, fi, err, statvfsSize, fillStatvfs)
}

func Fstatvfs(fd int32, buf *c23.Void) int32 {
	fi, ok, err := fdInfo(fd)
	if !ok {
		return -1
	}
	return store(buf, fi, err, statvfsSize, fillStatvfs)
}

// Statfs is statfs: f_type is the system's (Linux's magic numbers there).
func Statfs(path, buf *c23.Void) int32 {
	fi, err := statfs(c23.GoString(path))
	return store(buf, fi, err, statfsSize, fillStatfs)
}

func Fstatfs(fd int32, buf *c23.Void) int32 {
	fi, ok, err := fdInfo(fd)
	if !ok {
		return -1
	}
	return store(buf, fi, err, statfsSize, fillStatfs)
}
