// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package termios

import (
	"os"
	"syscall"
	"time"
	"unsafe"
)

// dtermios is macOS's struct termios.
type dtermios struct {
	iflag, oflag, cflag, lflag uint64
	cc                         [20]uint8
	ispeed, ospeed             uint64
}

// The flags' bits, Linux's and macOS's.
var (
	iflags = [][2]uint64{
		{IGNBRK, 0x1}, {BRKINT, 0x2}, {IGNPAR, 0x4}, {PARMRK, 0x8}, {INPCK, 0x10},
		{ISTRIP, 0x20}, {INLCR, 0x40}, {IGNCR, 0x80}, {ICRNL, 0x100}, {IXON, 0x200},
		{IXANY, 0x800}, {IXOFF, 0x400}, {IMAXBEL, 0x2000}, {IUTF8, 0x4000},
	}
	oflags = [][2]uint64{
		{OPOST, 0x1}, {ONLCR, 0x2}, {OCRNL, 0x10}, {ONOCR, 0x20}, {ONLRET, 0x40},
		{OFILL, 0x80}, {OFDEL, 0x20000}, {TAB3, 0x4},
	}
	cflags = [][2]uint64{ // CS8 is CS6|CS7 on both
		{CS6, 0x100}, {CS7, 0x200}, {CSTOPB, 0x400}, {CREAD, 0x800}, {PARENB, 0x1000},
		{PARODD, 0x2000}, {HUPCL, 0x4000}, {CLOCAL, 0x8000}, {CRTSCTS, 0x30000},
	}
	lflags = [][2]uint64{
		{ISIG, 0x80}, {ICANON, 0x100}, {ECHO, 0x8}, {ECHOE, 0x2}, {ECHOK, 0x4},
		{ECHONL, 0x10}, {NOFLSH, 0x80000000}, {TOSTOP, 0x400000}, {ECHOCTL, 0x40},
		{ECHOPRT, 0x20}, {ECHOKE, 0x1}, {FLUSHO, 0x800000}, {PENDIN, 0x20000000},
		{IEXTEN, 0x400}, {EXTPROC, 0x800},
	}
	// ccs are c_cc's indices, Linux's and macOS's.
	ccs = [][2]int{
		{VINTR, 8}, {VQUIT, 9}, {VERASE, 3}, {VKILL, 5}, {VEOF, 0}, {VTIME, 17},
		{VMIN, 16}, {VSTART, 12}, {VSTOP, 13}, {VSUSP, 10}, {VEOL, 1}, {VREPRINT, 6},
		{VDISCARD, 15}, {VWERASE, 4}, {VLNEXT, 14}, {VEOL2, 2},
	}
)

// toLinux and toDarwin translate the bits of flags; toDarwin keeps the
// bits of old that Linux has not.
func toLinux(d uint64, flags [][2]uint64) uint32 {
	var l uint32
	for _, f := range flags {
		if d&f[1] == f[1] {
			l |= uint32(f[0])
		}
	}
	return l
}

func toDarwin(l uint32, old uint64, flags [][2]uint64) uint64 {
	d := old
	for _, f := range flags {
		d &^= f[1]
	}
	for _, f := range flags {
		if uint64(l)&f[0] == f[0] {
			d |= f[1]
		}
	}
	return d
}

// vdisable is a disabled c_cc character, macOS's (Linux's is 0).
const vdisable = 0xff

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

func getd(f *os.File) (dtermios, error) {
	var d dtermios
	err := ioctl(f, syscall.TIOCGETA, uintptr(unsafe.Pointer(&d)))
	return d, err
}

func getattr(f *os.File) (termios, error) {
	d, err := getd(f)
	if err != nil {
		return termios{}, err
	}
	t := termios{
		iflag: toLinux(d.iflag, iflags),
		oflag: toLinux(d.oflag, oflags),
		cflag: toLinux(d.cflag, cflags),
		lflag: toLinux(d.lflag, lflags),
	}
	for i, b := range bauds {
		if uint64(b) == d.ospeed && i != 16 {
			t.cflag |= uint32(i&15) | uint32(i>>4)*CBAUDEX
			break
		}
	}
	for _, c := range ccs {
		v := d.cc[c[1]]
		if v == vdisable && c[0] != VMIN && c[0] != VTIME {
			v = 0
		}
		t.cc[c[0]] = v
	}
	return t, nil
}

func setattr(f *os.File, act int32, t *termios) error {
	d, err := getd(f)
	if err != nil {
		return err
	}
	d.iflag = toDarwin(t.iflag, d.iflag, iflags)
	d.oflag = toDarwin(t.oflag, d.oflag, oflags)
	d.cflag = toDarwin(t.cflag, d.cflag, cflags)
	d.lflag = toDarwin(t.lflag, d.lflag, lflags)
	b := t.cflag & CBAUD
	speed := uint64(bauds[b&15+(b>>12)*16])
	d.ispeed, d.ospeed = speed, speed
	for _, c := range ccs {
		v := t.cc[c[0]]
		if v == 0 && c[0] != VMIN && c[0] != VTIME {
			v = vdisable
		}
		d.cc[c[1]] = v
	}
	req := [...]uintptr{syscall.TIOCSETA, syscall.TIOCSETAW, syscall.TIOCSETAF}[act]
	return ioctl(f, req, uintptr(unsafe.Pointer(&d)))
}

func flush(f *os.File, queue int32) error {
	const fread, fwrite = 1, 2
	which := int32([...]int32{fread, fwrite, fread | fwrite}[queue])
	return ioctl(f, syscall.TIOCFLUSH, uintptr(unsafe.Pointer(&which)))
}

func drain(f *os.File) error { return ioctl(f, syscall.TIOCDRAIN, 0) }

func flow(f *os.File, act int32) error {
	switch act {
	case TCOOFF:
		return ioctl(f, syscall.TIOCSTOP, 0)
	case TCOON:
		return ioctl(f, syscall.TIOCSTART, 0)
	}
	// Send the STOP or START character.
	d, err := getd(f)
	if err != nil {
		return err
	}
	c := d.cc[13]
	if act == TCION {
		c = d.cc[12]
	}
	if c != vdisable {
		_, err = f.Write([]byte{c})
	}
	return err
}

func sendBreak(f *os.File) error {
	if err := ioctl(f, syscall.TIOCSBRK, 0); err != nil {
		return err
	}
	time.Sleep(400 * time.Millisecond)
	return ioctl(f, syscall.TIOCCBRK, 0)
}

func getpgrp(f *os.File) (int32, error) {
	var pgrp int32
	err := ioctl(f, syscall.TIOCGPGRP, uintptr(unsafe.Pointer(&pgrp)))
	return pgrp, err
}

func setpgrp(f *os.File, pgrp int32) error {
	return ioctl(f, syscall.TIOCSPGRP, uintptr(unsafe.Pointer(&pgrp)))
}

// getsid is the session of the terminal's foreground process group: macOS
// has no TIOCGSID.
func getsid(f *os.File) (int32, error) {
	pgrp, err := getpgrp(f)
	if err != nil {
		return 0, err
	}
	sid, err := syscall.Getsid(int(pgrp))
	return int32(sid), err
}
