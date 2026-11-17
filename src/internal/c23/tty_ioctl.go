// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux || darwin

package c23

import (
	"os"
	"syscall"
	"unsafe"
)

// isatty reports whether f is a terminal: whether the system gets its
// terminal attributes.
func isatty(f *os.File) bool {
	c, err := f.SyscallConn()
	if err != nil {
		return false
	}
	var termios [256]byte // more than any system's struct termios
	ok := false
	c.Control(func(fd uintptr) {
		_, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, ioctlGetattr, uintptr(unsafe.Pointer(&termios)))
		ok = e == 0
	})
	return ok
}
