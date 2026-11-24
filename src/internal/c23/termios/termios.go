// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package termios is termios.h, with Linux's struct termios and
// constants (musl's and glibc's before 2.42). On Linux (where the kernel
// has the generic termios: not on mips, ppc64 or sparc) and on macOS the
// functions work on the terminal, translating to and from macOS's
// struct and flags; elsewhere a descriptor is never a terminal, and they
// fail with ENOTTY.
package termios

import (
	c23 "internal/c23"
	"os"
)

const NCCS = 32

// c_cc's indices.
const (
	VINTR    = 0
	VQUIT    = 1
	VERASE   = 2
	VKILL    = 3
	VEOF     = 4
	VTIME    = 5
	VMIN     = 6
	VSWTC    = 7
	VSTART   = 8
	VSTOP    = 9
	VSUSP    = 10
	VEOL     = 11
	VREPRINT = 12
	VDISCARD = 13
	VWERASE  = 14
	VLNEXT   = 15
	VEOL2    = 16
)

// c_iflag.
const (
	IGNBRK  = 0o1
	BRKINT  = 0o2
	IGNPAR  = 0o4
	PARMRK  = 0o10
	INPCK   = 0o20
	ISTRIP  = 0o40
	INLCR   = 0o100
	IGNCR   = 0o200
	ICRNL   = 0o400
	IUCLC   = 0o1000
	IXON    = 0o2000
	IXANY   = 0o4000
	IXOFF   = 0o10000
	IMAXBEL = 0o20000
	IUTF8   = 0o40000
)

// c_oflag.
const (
	OPOST  = 0o1
	OLCUC  = 0o2
	ONLCR  = 0o4
	OCRNL  = 0o10
	ONOCR  = 0o20
	ONLRET = 0o40
	OFILL  = 0o100
	OFDEL  = 0o200
	TABDLY = 0o14000
	TAB3   = 0o14000
)

// c_cflag.
const (
	CBAUD   = 0o10017
	CBAUDEX = 0o10000
	CSIZE   = 0o60
	CS5     = 0o0
	CS6     = 0o20
	CS7     = 0o40
	CS8     = 0o60
	CSTOPB  = 0o100
	CREAD   = 0o200
	PARENB  = 0o400
	PARODD  = 0o1000
	HUPCL   = 0o2000
	CLOCAL  = 0o4000
	CRTSCTS = 0o20000000000
)

// c_lflag.
const (
	ISIG    = 0o1
	ICANON  = 0o2
	XCASE   = 0o4
	ECHO    = 0o10
	ECHOE   = 0o20
	ECHOK   = 0o40
	ECHONL  = 0o100
	NOFLSH  = 0o200
	TOSTOP  = 0o400
	ECHOCTL = 0o1000
	ECHOPRT = 0o2000
	ECHOKE  = 0o4000
	FLUSHO  = 0o10000
	PENDIN  = 0o40000
	IEXTEN  = 0o100000
	EXTPROC = 0o200000
)

// tcsetattr's actions, tcflush's queues, tcflow's actions.
const (
	TCSANOW   = 0
	TCSADRAIN = 1
	TCSAFLUSH = 2

	TCIFLUSH  = 0
	TCOFLUSH  = 1
	TCIOFLUSH = 2

	TCOOFF = 0
	TCOON  = 1
	TCIOFF = 2
	TCION  = 3
)

// bauds are the speeds' rates, by B0 to B38400 and B57600 to B4000000.
var bauds = [...]uint32{
	0, 50, 75, 110, 134, 150, 200, 300, 600, 1200, 1800, 2400, 4800, 9600, 19200, 38400,
	0, 57600, 115200, 230400, 460800, 500000, 576000, 921600, 1000000, 1152000, 1500000,
	2000000, 2500000, 3000000, 3500000, 4000000,
}

// termios is C's struct termios: Linux's (glibc's and musl's).
type termios struct {
	iflag, oflag, cflag, lflag uint32
	line                       uint8
	cc                         [NCCS]uint8
	ispeed, ospeed             uint32
}

// The offsets of struct termios's fields.
const (
	offLine   = 16
	offCC     = 17
	offIspeed = 52
	offOspeed = 56
)

func load(p *c23.Void) termios {
	var t termios
	t.iflag = c23.Load32(p)
	t.oflag = c23.Load32(c23.Add(p, 4))
	t.cflag = c23.Load32(c23.Add(p, 8))
	t.lflag = c23.Load32(c23.Add(p, 12))
	t.line = c23.Load8(c23.Add(p, offLine))
	for i := range t.cc {
		t.cc[i] = c23.Load8(c23.Add(p, offCC+i))
	}
	t.ispeed = c23.Load32(c23.Add(p, offIspeed))
	t.ospeed = c23.Load32(c23.Add(p, offOspeed))
	return t
}

func store(p *c23.Void, t *termios) {
	c23.Store32(p, t.iflag)
	c23.Store32(c23.Add(p, 4), t.oflag)
	c23.Store32(c23.Add(p, 8), t.cflag)
	c23.Store32(c23.Add(p, 12), t.lflag)
	c23.Store8(c23.Add(p, offLine), t.line)
	for i, c := range t.cc {
		c23.Store8(c23.Add(p, offCC+i), c)
	}
	c23.Store32(c23.Add(p, offIspeed), t.ispeed)
	c23.Store32(c23.Add(p, offOspeed), t.ospeed)
}

// fdTTY returns fd's file, or nil with errno set: EBADF, or ENOTTY for
// a socket.
func fdTTY(fd int32) *os.File {
	f := c23.FdOS(fd)
	if f == nil && c23.Errno() == c23.ESPIPE {
		c23.SetErrno(c23.ENOTTY)
	}
	return f
}

// result is a function's result for err: 0, or -1 with errno set.
func result(err error) int32 {
	if err != nil {
		c23.SetErrnoOf(err)
		return -1
	}
	return 0
}

// Tcgetattr is tcgetattr.
func Tcgetattr(fd int32, t *c23.Void) int32 {
	f := fdTTY(fd)
	if f == nil {
		return -1
	}
	tio, err := getattr(f)
	if err != nil {
		return result(err)
	}
	tio.ispeed = tio.cflag & CBAUD
	tio.ospeed = tio.ispeed
	store(t, &tio)
	return 0
}

// Tcsetattr is tcsetattr.
func Tcsetattr(fd, act int32, t *c23.Void) int32 {
	if act < TCSANOW || act > TCSAFLUSH {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	f := fdTTY(fd)
	if f == nil {
		return -1
	}
	tio := load(t)
	return result(setattr(f, act, &tio))
}

// Tcflush is tcflush.
func Tcflush(fd, queue int32) int32 {
	if queue < TCIFLUSH || queue > TCIOFLUSH {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	f := fdTTY(fd)
	if f == nil {
		return -1
	}
	return result(flush(f, queue))
}

// Tcdrain is tcdrain.
func Tcdrain(fd int32) int32 {
	f := fdTTY(fd)
	if f == nil {
		return -1
	}
	return result(drain(f))
}

// Tcflow is tcflow.
func Tcflow(fd, act int32) int32 {
	if act < TCOOFF || act > TCION {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	f := fdTTY(fd)
	if f == nil {
		return -1
	}
	return result(flow(f, act))
}

// Tcsendbreak is tcsendbreak: a break of the system's length, whatever
// duration says (as Linux's).
func Tcsendbreak(fd, duration int32) int32 {
	f := fdTTY(fd)
	if f == nil {
		return -1
	}
	return result(sendBreak(f))
}

// Tcgetpgrp is tcgetpgrp.
func Tcgetpgrp(fd int32) int32 {
	f := fdTTY(fd)
	if f == nil {
		return -1
	}
	pgrp, err := getpgrp(f)
	if err != nil {
		return result(err)
	}
	return pgrp
}

// Tcsetpgrp is tcsetpgrp.
func Tcsetpgrp(fd, pgrp int32) int32 {
	f := fdTTY(fd)
	if f == nil {
		return -1
	}
	return result(setpgrp(f, pgrp))
}

// Tcgetsid is tcgetsid.
func Tcgetsid(fd int32) int32 {
	f := fdTTY(fd)
	if f == nil {
		return -1
	}
	sid, err := getsid(f)
	if err != nil {
		return result(err)
	}
	return sid
}

// Cfmakeraw is cfmakeraw.
func Cfmakeraw(t *c23.Void) {
	tio := load(t)
	tio.iflag &^= IGNBRK | BRKINT | PARMRK | ISTRIP | INLCR | IGNCR | ICRNL | IXON
	tio.oflag &^= OPOST
	tio.lflag &^= ECHO | ECHONL | ICANON | ISIG | IEXTEN
	tio.cflag &^= CSIZE | PARENB
	tio.cflag |= CS8
	tio.cc[VMIN] = 1
	tio.cc[VTIME] = 0
	store(t, &tio)
}

// Cfgetospeed is cfgetospeed: the B constant c_cflag has.
func Cfgetospeed(t *c23.Void) uint32 { return c23.Load32(c23.Add(t, 8)) & CBAUD }

// Cfgetispeed is cfgetispeed, which is the output's (as musl's).
func Cfgetispeed(t *c23.Void) uint32 { return Cfgetospeed(t) }

// Cfsetospeed is cfsetospeed: speed is a B constant, or EINVAL.
func Cfsetospeed(t *c23.Void, speed uint32) int32 {
	if speed&^CBAUD != 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	cflag := c23.Load32(c23.Add(t, 8))
	c23.Store32(c23.Add(t, 8), cflag&^CBAUD|speed)
	c23.Store32(c23.Add(t, offIspeed), speed)
	c23.Store32(c23.Add(t, offOspeed), speed)
	return 0
}

// Cfsetispeed is cfsetispeed: as cfsetospeed, as there is one speed;
// 0 leaves it.
func Cfsetispeed(t *c23.Void, speed uint32) int32 {
	if speed == 0 {
		return 0
	}
	return Cfsetospeed(t, speed)
}

// Cfsetspeed is cfsetspeed.
func Cfsetspeed(t *c23.Void, speed uint32) int32 { return Cfsetospeed(t, speed) }
