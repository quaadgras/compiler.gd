// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mman

import (
	c23 "internal/c23"
	"os"
	"runtime"
	"strings"
)

// Mlock is mlock: memory is not paged out by request, and the request
// succeeds for memory that the program has.
func Mlock(addr *c23.Void, length uint64) int32 {
	if !advisable(addr, length) {
		c23.SetErrno(c23.ENOMEM)
		return -1
	}
	return 0
}

// Munlock is munlock.
func Munlock(addr *c23.Void, length uint64) int32 { return Mlock(addr, length) }

// mlockall's flags.
const (
	MCL_CURRENT = 1
	MCL_FUTURE  = 2
	MCL_ONFAULT = 4
)

// Mlockall is mlockall, as mlock.
func Mlockall(flags int32) int32 {
	if flags == 0 || flags&^(MCL_CURRENT|MCL_FUTURE|MCL_ONFAULT) != 0 || flags == MCL_ONFAULT {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	return 0
}

// Munlockall is munlockall.
func Munlockall() int32 { return 0 }

// posix_madvise's advice.
const (
	POSIX_MADV_NORMAL     = 0
	POSIX_MADV_RANDOM     = 1
	POSIX_MADV_SEQUENTIAL = 2
	POSIX_MADV_WILLNEED   = 3
	POSIX_MADV_DONTNEED   = 4
)

// PosixMadvise is posix_madvise, which takes no advice: 0, or EINVAL.
func PosixMadvise(addr *c23.Void, length uint64, advice int32) int32 {
	if advice < POSIX_MADV_NORMAL || advice > POSIX_MADV_DONTNEED {
		return c23.EINVAL
	}
	if !advisable(addr, length) {
		return c23.ENOMEM
	}
	return 0
}

// shmName is the file of the shared memory object name: a file of
// /dev/shm on Linux, as glibc's and musl's, and of the temporary
// directory elsewhere (or if there is no /dev/shm). As theirs, the name's
// leading slashes are dropped, and what is left must be a file name of
// at most 255 bytes, not "." or "..".
func shmName(p *c23.Void) (string, bool) {
	name := strings.TrimLeft(c23.GoString(p), "/")
	switch {
	case len(name) > 255:
		c23.SetErrno(c23.ENAMETOOLONG)
		return "", false
	case name == "" || name == "." || name == ".." || strings.ContainsRune(name, '/'):
		c23.SetErrno(c23.EINVAL)
		return "", false
	}
	dir := os.TempDir()
	if runtime.GOOS == "linux" {
		if fi, err := os.Stat("/dev/shm"); err == nil && fi.IsDir() {
			dir = "/dev/shm"
		}
	}
	return dir + string(os.PathSeparator) + name, true
}

// ShmOpen is shm_open: open of the object's file (shmName), close-on-exec
// and not through a symbolic link. Its shared mappings (mmap's
// MAP_SHARED) are shared in the process, not with others.
func ShmOpen(name *c23.Void, flags int32, mode uint32) int32 {
	file, ok := shmName(name)
	if !ok {
		return -1
	}
	return c23.Open(file, flags|c23.O_NOFOLLOW|c23.O_CLOEXEC, mode)
}

// ShmUnlink is shm_unlink: the object's file is removed.
func ShmUnlink(name *c23.Void) int32 {
	file, ok := shmName(name)
	if !ok {
		return -1
	}
	return c23.ErrRet(os.Remove(file))
}
