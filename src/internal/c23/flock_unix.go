// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package c23

import (
	"errors"
	"os"
	"syscall"
)

// flockFile is flock's operation op (LOCK_SH, LOCK_EX, LOCK_UN, with
// LOCK_NB) on f: the system's whole-file locks.
func flockFile(f *os.File, op int32) error {
	how := 0
	switch op &^ LOCK_NB {
	case LOCK_SH:
		how = syscall.LOCK_SH
	case LOCK_EX:
		how = syscall.LOCK_EX
	case LOCK_UN:
		how = syscall.LOCK_UN
	}
	if op&LOCK_NB != 0 {
		how |= syscall.LOCK_NB
	}
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var lerr error
	if err := rc.Control(func(fd uintptr) {
		for {
			lerr = syscall.Flock(int(fd), how)
			if lerr != syscall.EINTR {
				return
			}
		}
	}); err != nil {
		return err
	}
	if errors.Is(lerr, syscall.EWOULDBLOCK) {
		return errnoErr(EAGAIN)
	}
	return lerr
}
