// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux || darwin || dragonfly || freebsd || netbsd || openbsd

package c23

import "syscall"

// Process groups and sessions: the system's, which package os does not
// have.

func getpgrp() int32 { return int32(syscall.Getpgrp()) }

func getpgid(pid int32) (int32, error) {
	g, err := syscall.Getpgid(int(pid))
	return int32(g), err
}

func getsid(pid int32) (int32, error) {
	s, _, e := syscall.RawSyscall(syscall.SYS_GETSID, uintptr(pid), 0, 0)
	if e != 0 {
		return 0, e
	}
	return int32(s), nil
}

func setpgid(pid, pgid int32) error { return syscall.Setpgid(int(pid), int(pgid)) }

func setsid() (int32, error) {
	s, err := syscall.Setsid()
	return int32(s), err
}
