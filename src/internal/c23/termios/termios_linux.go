// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux && (386 || amd64 || arm || arm64 || loong64 || riscv64 || s390x)

package termios

import (
	"os"
	"syscall"
	"unsafe"
)

// The generic termios's ioctls (asm-generic/ioctls.h).
const (
	tcgets    = 0x5401
	tcsets    = 0x5402 // tcsetsw and tcsetsf follow
	tcsbrk    = 0x5409
	tcxonc    = 0x540A
	tcflsh    = 0x540B
	tiocgpgrp = 0x540F
	tiocspgrp = 0x5410
	tiocgsid  = 0x5429
)

// ktermios is the kernel's struct termios, which C's begins with.
type ktermios struct {
	iflag, oflag, cflag, lflag uint32
	line                       uint8
	cc                         [19]uint8
	_                          [32]byte // room, should a kernel write more
}

func ioctl(f *os.File, req, arg uintptr) error {
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

func getattr(f *os.File) (termios, error) {
	var k ktermios
	if err := ioctl(f, tcgets, uintptr(unsafe.Pointer(&k))); err != nil {
		return termios{}, err
	}
	t := termios{iflag: k.iflag, oflag: k.oflag, cflag: k.cflag, lflag: k.lflag, line: k.line}
	copy(t.cc[:], k.cc[:])
	return t, nil
}

func setattr(f *os.File, act int32, t *termios) error {
	k := ktermios{iflag: t.iflag, oflag: t.oflag, cflag: t.cflag, lflag: t.lflag, line: t.line}
	copy(k.cc[:], t.cc[:])
	return ioctl(f, tcsets+uintptr(act), uintptr(unsafe.Pointer(&k)))
}

func flush(f *os.File, queue int32) error { return ioctl(f, tcflsh, uintptr(queue)) }

func drain(f *os.File) error { return ioctl(f, tcsbrk, 1) }

func flow(f *os.File, act int32) error { return ioctl(f, tcxonc, uintptr(act)) }

func sendBreak(f *os.File) error { return ioctl(f, tcsbrk, 0) }

func getpgrp(f *os.File) (int32, error) {
	var pgrp int32
	err := ioctl(f, tiocgpgrp, uintptr(unsafe.Pointer(&pgrp)))
	return pgrp, err
}

func setpgrp(f *os.File, pgrp int32) error {
	return ioctl(f, tiocspgrp, uintptr(unsafe.Pointer(&pgrp)))
}

func getsid(f *os.File) (int32, error) {
	var sid int32
	err := ioctl(f, tiocgsid, uintptr(unsafe.Pointer(&sid)))
	return sid, err
}

func getwinsize(f *os.File) ([4]uint16, error) {
	var ws [4]uint16
	err := ioctl(f, syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws)))
	return ws, err
}

func setwinsize(f *os.File, ws *[4]uint16) error {
	return ioctl(f, syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(ws)))
}
