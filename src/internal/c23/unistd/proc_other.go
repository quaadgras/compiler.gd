// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !(linux || darwin || dragonfly || freebsd || netbsd || openbsd)

package unistd

import (
	"internal/c23"
	"os"
)

// Process groups and sessions, where the system has none C can use: the
// program is alone in its group and session, which it cannot change
// (ENOSYS).

func getpgrp() int32 { return int32(os.Getpid()) }

func getpgid(pid int32) (int32, error) {
	if pid != 0 && int(pid) != os.Getpid() {
		return 0, c23.ErrnoErr(c23.ESRCH)
	}
	return int32(os.Getpid()), nil
}

func getsid(pid int32) (int32, error) { return getpgid(pid) }

func setpgid(pid, pgid int32) error { return c23.ErrnoErr(c23.ENOSYS) }

func setsid() (int32, error) { return 0, c23.ErrnoErr(c23.ENOSYS) }
