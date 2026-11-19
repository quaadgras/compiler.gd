// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux || darwin

package ioctl

import (
	"os"
	"syscall"
	"unsafe"
)

// ioctlInRead is the system's FIONREAD of f: the bytes a read would
// return.
func ioctlInRead(f *os.File) (int64, error) {
	var n int32
	err := ioctlPtr(f, ioctlFIONREAD, unsafe.Pointer(&n))
	return int64(n), err
}

// ioctlWinsize is the system's TIOCGWINSZ of f: the terminal's struct
// winsize.
func ioctlWinsize(f *os.File) ([4]uint16, error) {
	var ws [4]uint16
	err := ioctlPtr(f, syscall.TIOCGWINSZ, unsafe.Pointer(&ws))
	return ws, err
}

func ioctlPtr(f *os.File, req uintptr, arg unsafe.Pointer) error {
	c, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var e syscall.Errno
	if err := c.Control(func(fd uintptr) {
		_, _, e = syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg))
	}); err != nil {
		return err
	}
	if e != 0 {
		return e
	}
	return nil
}
