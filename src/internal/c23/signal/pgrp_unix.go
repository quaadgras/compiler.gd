// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux || darwin || dragonfly || freebsd || netbsd || openbsd

package signal

import "syscall"

// getpgrp is the program's process group.
func getpgrp() int32 { return int32(syscall.Getpgrp()) }

// pgidOf is process pid's group, or -1 if it has none (it ended).
func pgidOf(pid int32) int32 {
	g, err := syscall.Getpgid(int(pid))
	if err != nil {
		return -1
	}
	return int32(g)
}
