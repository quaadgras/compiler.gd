// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package ulimit

import "syscall"

const rlimInfinity = ^uint64(0)

func getFsize() (int64, error) {
	var rl syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &rl); err != nil {
		return 0, err
	}
	cur := uint64(rl.Cur)
	if cur == rlimInfinity || cur == 1<<63-1 || cur/512 > uint64(longMax) {
		return longMax, nil
	}
	return int64(cur / 512), nil
}

func setFsize(n int64) error {
	var rl syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &rl); err != nil {
		return err
	}
	v := uint64(n) * 512
	if n == longMax || uint64(n) > rlimInfinity/512 {
		v = rlimInfinity
	}
	setRlim(&rl.Cur, v)
	return syscall.Setrlimit(syscall.RLIMIT_FSIZE, &rl)
}

// setRlim sets a limit of the system's type.
func setRlim[T ~int64 | ~uint64](p *T, v uint64) { *p = T(v) }
