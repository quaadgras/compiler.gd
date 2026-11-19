// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux || darwin

package stdlib

import (
	"os"
	"syscall"
)

const havePty = true

func ptyIoctl(f *os.File, req, arg uintptr) error {
	c, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var e syscall.Errno
	if err := c.Control(func(fd uintptr) {
		_, _, e = syscall.Syscall(syscall.SYS_IOCTL, fd, req, arg)
	}); err != nil {
		return err
	}
	if e != 0 {
		return e
	}
	return nil
}
