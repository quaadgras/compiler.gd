// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package resource

import (
	"runtime"
	"syscall"
)

func getpriority(which, who int) (int, error) {
	v, err := syscall.Getpriority(which, who)
	if runtime.GOOS == "linux" {
		v = 20 - v // the system call's, which is not negative
	}
	return v, err
}

func setpriority(which, who, prio int) error { return syscall.Setpriority(which, who, prio) }

// rlimInfinity is the system's RLIM_INFINITY, or a value above which
// any is none.
const rlimInfinity = 1<<63 - 1

func getrlimit(res int32) (cur, max uint64, err error) {
	r, ok := sysResource(res)
	if !ok {
		return RLIM_INFINITY, RLIM_INFINITY, nil
	}
	var rl syscall.Rlimit
	if err := syscall.Getrlimit(r, &rl); err != nil {
		return 0, 0, err
	}
	conv := func(v uint64) uint64 {
		if v >= rlimInfinity {
			return RLIM_INFINITY
		}
		return v
	}
	return conv(uint64(rl.Cur)), conv(uint64(rl.Max)), nil
}

func setrlimit(res int32, cur, max uint64) error {
	r, ok := sysResource(res)
	if !ok {
		return syscall.EPERM
	}
	conv := func(v uint64) uint64 {
		if v == RLIM_INFINITY && runtime.GOOS != "linux" {
			return rlimInfinity
		}
		return v
	}
	var rl syscall.Rlimit
	setRlim(&rl.Cur, conv(cur))
	setRlim(&rl.Max, conv(max))
	return syscall.Setrlimit(r, &rl)
}

// setRlim sets a limit of the system's type.
func setRlim[T ~int64 | ~uint64](p *T, v uint64) { *p = T(v) }
