// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package unistd

import (
	c23 "internal/c23"
	"os"
	"unsafe"
)

// pathconf's names: Linux's.
const (
	_PC_LINK_MAX           = 0
	_PC_MAX_CANON          = 1
	_PC_MAX_INPUT          = 2
	_PC_NAME_MAX           = 3
	_PC_PATH_MAX           = 4
	_PC_PIPE_BUF           = 5
	_PC_CHOWN_RESTRICTED   = 6
	_PC_NO_TRUNC           = 7
	_PC_VDISABLE           = 8
	_PC_SYNC_IO            = 9
	_PC_ASYNC_IO           = 10
	_PC_PRIO_IO            = 11
	_PC_SOCK_MAXBUF        = 12
	_PC_FILESIZEBITS       = 13
	_PC_REC_INCR_XFER_SIZE = 14
	_PC_REC_MAX_XFER_SIZE  = 15
	_PC_REC_MIN_XFER_SIZE  = 16
	_PC_REC_XFER_ALIGN     = 17
	_PC_ALLOC_SIZE_MIN     = 18
	_PC_SYMLINK_MAX        = 19
	_PC_2_SYMLINKS         = 20
)

// pathconfValues are Linux's limits, those of every file system: what
// glibc and musl agree on, and glibc's where they differ (_PC_LINK_MAX,
// which glibc finds by the file system and is 127 for an unknown one).
// -1 is no limit (errno unchanged).
var pathconfValues = [...]int64{
	_PC_LINK_MAX:           127,
	_PC_MAX_CANON:          255,
	_PC_MAX_INPUT:          255,
	_PC_NAME_MAX:           255,
	_PC_PATH_MAX:           4096,
	_PC_PIPE_BUF:           4096,
	_PC_CHOWN_RESTRICTED:   1,
	_PC_NO_TRUNC:           1,
	_PC_VDISABLE:           0,
	_PC_SYNC_IO:            1,
	_PC_ASYNC_IO:           -1,
	_PC_PRIO_IO:            -1,
	_PC_SOCK_MAXBUF:        -1,
	_PC_FILESIZEBITS:       64,
	_PC_REC_INCR_XFER_SIZE: 4096,
	_PC_REC_MAX_XFER_SIZE:  4096,
	_PC_REC_MIN_XFER_SIZE:  4096,
	_PC_REC_XFER_ALIGN:     4096,
	_PC_ALLOC_SIZE_MIN:     4096,
	_PC_SYMLINK_MAX:        -1,
	_PC_2_SYMLINKS:         1,
}

func pathconf(name int32) int64 {
	if name < 0 || int(name) >= len(pathconfValues) {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	return pathconfValues[name]
}

// Pathconf is pathconf: Linux's limits (pathconfValues) of a file that
// exists.
func Pathconf(path c23.Ptr, name int32) int64 {
	if _, err := os.Stat(c23.GoString(path)); err != nil {
		c23.SetErrnoOf(err)
		return -1
	}
	return pathconf(name)
}

// Fpathconf is fpathconf.
func Fpathconf(fd int32, name int32) int64 {
	if c23.FdFile(fd) == nil {
		c23.SetErrno(c23.EBADF)
		return -1
	}
	return pathconf(name)
}

// confstr's names: Linux's.
const (
	_CS_PATH                            = 0
	_CS_POSIX_V6_WIDTH_RESTRICTED_ENVS  = 1
	_CS_POSIX_V5_WIDTH_RESTRICTED_ENVS  = 4
	_CS_POSIX_V7_WIDTH_RESTRICTED_ENVS  = 5
	_CS_POSIX_V6_ILP32_OFF32_CFLAGS     = 1116
	_CS_POSIX_V7_LPBIG_OFFBIG_LINTFLAGS = 1147
	_CS_V6_ENV                          = 1148
	_CS_V7_ENV                          = 1149
)

// Confstr is confstr: _CS_PATH is "/bin:/usr/bin", the environments of
// width-restricted programs the target's (LP64_OFF64 or ILP32_OFFBIG),
// and the compilation flags none (""). The result is the size the value
// needs, with its NUL; what fits of it is stored in buf.
func Confstr(name int32, buf c23.Ptr, n uint64) uint64 {
	var v string
	env := "LP64_OFF64"
	if c23.LongSize == 4 {
		env = "ILP32_OFFBIG"
	}
	switch {
	case name == _CS_PATH:
		v = "/bin:/usr/bin"
	case name == _CS_POSIX_V6_WIDTH_RESTRICTED_ENVS:
		v = "POSIX_V6_" + env
	case name == _CS_POSIX_V5_WIDTH_RESTRICTED_ENVS:
		v = "XBS5_" + env
	case name == _CS_POSIX_V7_WIDTH_RESTRICTED_ENVS:
		v = "POSIX_V7_" + env
	case name >= _CS_POSIX_V6_ILP32_OFF32_CFLAGS && name <= _CS_V7_ENV:
	default:
		c23.SetErrno(c23.EINVAL)
		return 0
	}
	if n > 0 && c23.Addr(buf) != 0 {
		k := min(uint64(len(v)), n-1)
		b := c23.BytesAt(buf, uintptr(k+1))
		copy(b, v[:k])
		b[k] = 0
	}
	return uint64(len(v) + 1)
}

// Gethostid is gethostid: the id in /etc/hostid, as glibc reads it, or
// 0 (musl's) if there is none.
func Gethostid() int64 {
	b, err := os.ReadFile("/etc/hostid")
	if err != nil || len(b) < 4 {
		return 0
	}
	var id int32
	copy((*[4]byte)(unsafe.Pointer(&id))[:], b)
	return int64(id)
}

var loginBuf c23.Ptr

// login is the name of the user logged in: $LOGNAME, as musl's getlogin
// (glibc finds the controlling terminal's in utmp).
func login() (string, bool) {
	name := os.Getenv("LOGNAME")
	return name, name != ""
}

// Getlogin is getlogin: null (errno ENXIO) if there is no login name.
func Getlogin() c23.Ptr {
	name, ok := login()
	if !ok {
		c23.SetErrno(c23.ENXIO)
		return c23.Ptr(nil)
	}
	if c23.Addr(loginBuf) == 0 {
		loginBuf = c23.Malloc(256)
	}
	if len(name) > 255 {
		name = name[:255]
	}
	copy(c23.BytesAt(loginBuf, uintptr(len(name)+1)), name+"\x00")
	return loginBuf
}

// GetloginR is getlogin_r: an errno value (ENXIO, ERANGE) or 0.
func GetloginR(buf c23.Ptr, n uint64) int32 {
	name, ok := login()
	if !ok {
		return c23.ENXIO
	}
	if uint64(len(name)) >= n {
		return c23.ERANGE
	}
	copy(c23.BytesAt(buf, uintptr(len(name)+1)), name+"\x00")
	return 0
}

// Getgroups is getgroups: the supplementary groups (none where the
// system has no groups), their number if n is 0, EINVAL if more than n.
func Getgroups(n int32, list c23.Ptr) int32 {
	gs, err := os.Getgroups()
	if err != nil {
		gs = nil
	}
	if n == 0 {
		return int32(len(gs))
	}
	if n < 0 || int(n) < len(gs) {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	for i, g := range gs {
		c23.Store32(c23.Add(list, 4*i), uint32(g))
	}
	return int32(len(gs))
}

var ttyBuf c23.Ptr

// Ttyname is ttyname: the name of the terminal fd is a descriptor of, in
// an array the next call reuses; null with errno EBADF or ENOTTY.
func Ttyname(fd int32) c23.Ptr {
	name, e := ttynameOf(fd)
	if e != 0 {
		c23.SetErrno(e)
		return c23.Ptr(nil)
	}
	if c23.Addr(ttyBuf) == 0 {
		ttyBuf = c23.Malloc(4096)
	}
	if len(name) >= 4096 {
		c23.SetErrno(c23.ERANGE)
		return c23.Ptr(nil)
	}
	copy(c23.BytesAt(ttyBuf, uintptr(len(name)+1)), name+"\x00")
	return ttyBuf
}

// TtynameR is ttyname_r: an errno value (EBADF, ENOTTY, ERANGE) or 0.
func TtynameR(fd int32, buf c23.Ptr, n uint64) int32 {
	name, e := ttynameOf(fd)
	if e != 0 {
		return e
	}
	if uint64(len(name)) >= n {
		return c23.ERANGE
	}
	copy(c23.BytesAt(buf, uintptr(len(name)+1)), name+"\x00")
	return 0
}

func ttynameOf(fd int32) (string, int32) {
	of := c23.FdFile(fd)
	if of == nil {
		return "", c23.EBADF
	}
	if of.F == nil || !isatty(of.F) {
		return "", c23.ENOTTY
	}
	name, ok := ttyPath(of.F)
	if !ok {
		return "", c23.ENOTTY
	}
	return name, 0
}
