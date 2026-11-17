// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix

package c23

import (
	"errors"
	"os"
	"syscall"
)

// setInt sets an integer of the system's type.
func setInt[T ~int16 | ~int32 | ~int64](p *T, v int64) { *p = T(v) }

// lockFile is fcntl's lock command cmd of l on f's descriptor: the
// system's record locks, which are the process's, as C's are.
func lockFile(f *os.File, cmd int32, l *flock) error {
	var lk syscall.Flock_t
	setInt(&lk.Type, int64([...]int16{F_RDLCK: syscall.F_RDLCK, F_WRLCK: syscall.F_WRLCK, F_UNLCK: syscall.F_UNLCK}[l.typ]))
	setInt(&lk.Whence, int64(l.whence))
	setInt(&lk.Start, l.start)
	setInt(&lk.Len, l.len)
	c := map[int32]int{F_GETLK: syscall.F_GETLK, F_SETLK: syscall.F_SETLK, F_SETLKW: syscall.F_SETLKW}[cmd]
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var lerr error
	if err := rc.Control(func(fd uintptr) {
		for {
			lerr = syscall.FcntlFlock(fd, c, &lk)
			if lerr != syscall.EINTR {
				return
			}
		}
	}); err != nil {
		return err
	}
	if errors.Is(lerr, syscall.EAGAIN) || errors.Is(lerr, syscall.EACCES) {
		return errnoErr(EAGAIN)
	}
	if lerr != nil {
		return lerr
	}
	if cmd == F_GETLK {
		switch int(lk.Type) {
		case syscall.F_RDLCK:
			l.typ = F_RDLCK
		case syscall.F_WRLCK:
			l.typ = F_WRLCK
		default:
			l.typ = F_UNLCK
		}
		l.whence, l.start, l.len, l.pid = int16(lk.Whence), int64(lk.Start), int64(lk.Len), int32(lk.Pid)
	}
	return nil
}

// rusage is getrusage's struct rusage, as longs.
func rusage(who int32) ([18]int64, error) {
	w := syscall.RUSAGE_SELF
	if who == RUSAGE_CHILDREN {
		w = syscall.RUSAGE_CHILDREN
	}
	var ru syscall.Rusage
	if err := syscall.Getrusage(w, &ru); err != nil {
		return [18]int64{}, err
	}
	return [18]int64{
		int64(ru.Utime.Sec), int64(ru.Utime.Usec), int64(ru.Stime.Sec), int64(ru.Stime.Usec),
		int64(ru.Maxrss), int64(ru.Ixrss), int64(ru.Idrss), int64(ru.Isrss), int64(ru.Minflt),
		int64(ru.Majflt), int64(ru.Nswap), int64(ru.Inblock), int64(ru.Oublock), int64(ru.Msgsnd),
		int64(ru.Msgrcv), int64(ru.Nsignals), int64(ru.Nvcsw), int64(ru.Nivcsw),
	}, nil
}
