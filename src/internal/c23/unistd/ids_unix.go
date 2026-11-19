// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package unistd

import (
	c23 "internal/c23"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
)

// The process's ids and priority, and sync: the system's.

func Setuid(uid uint32) int32 { return c23.ErrRet(syscall.Setuid(cid(uid))) }

func Setgid(gid uint32) int32 { return c23.ErrRet(syscall.Setgid(cid(gid))) }

func Seteuid(uid uint32) int32 { return c23.ErrRet(syscall.Seteuid(cid(uid))) }

func Setegid(gid uint32) int32 { return c23.ErrRet(syscall.Setegid(cid(gid))) }

func Setreuid(ruid, euid uint32) int32 { return c23.ErrRet(syscall.Setreuid(cid(ruid), cid(euid))) }

func Setregid(rgid, egid uint32) int32 { return c23.ErrRet(syscall.Setregid(cid(rgid), cid(egid))) }

// Sync is sync: the system's.
func Sync() { syscall.Sync() }

// Nice is nice: the calling thread's nice value (Linux's, which is the
// thread's) changed by inc, within -20 and 19; the new value, or -1 with
// errno EPERM if it may not be lowered.
func Nice(inc int32) int32 {
	cur, err := syscall.Getpriority(syscall.PRIO_PROCESS, 0)
	if err != nil {
		c23.SetErrnoOf(err)
		return -1
	}
	if runtime.GOOS == "linux" {
		cur = 20 - cur // the system call's, which is not negative
	}
	v := min(max(cur+int(inc), -20), 19)
	if err := syscall.Setpriority(syscall.PRIO_PROCESS, 0, v); err != nil {
		if err == syscall.EACCES {
			err = syscall.EPERM
		}
		c23.SetErrnoOf(err)
		return -1
	}
	return int32(v)
}

// ttyPath is the name of the terminal f is: on Linux, the link
// /proc/self/fd/N; elsewhere, the device of /dev or /dev/pts that has its
// device number.
func ttyPath(f *os.File) (string, bool) {
	rc, err := f.SyscallConn()
	if err != nil {
		return "", false
	}
	var st syscall.Stat_t
	var sysfd uintptr
	var serr error
	rc.Control(func(fd uintptr) {
		sysfd = fd
		serr = syscall.Fstat(int(fd), &st)
	})
	if serr != nil {
		return "", false
	}
	if runtime.GOOS == "linux" {
		if name, err := os.Readlink("/proc/self/fd/" + strconv.Itoa(int(sysfd))); err == nil && filepath.IsAbs(name) {
			return name, true
		}
	}
	for _, dir := range []string{"/dev", "/dev/pts"} {
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if e.Type()&os.ModeCharDevice == 0 {
				continue
			}
			name := dir + "/" + e.Name()
			var dst syscall.Stat_t
			if syscall.Stat(name, &dst) == nil && uint64(dst.Rdev) == uint64(st.Rdev) {
				return name, true
			}
		}
	}
	return "", false
}
