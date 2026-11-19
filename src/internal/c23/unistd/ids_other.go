// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package unistd

import (
	c23 "internal/c23"
	"os"
	"sync/atomic"
)

// Where the system has no ids C can change, an id may only be set to
// what it is (EPERM otherwise).

func setID(id uint32, cur ...int) int32 {
	for _, c := range cur {
		if uint32(c) != id {
			c23.SetErrno(c23.EPERM)
			return -1
		}
	}
	return 0
}

func Setuid(uid uint32) int32 { return setID(uid, os.Getuid(), os.Geteuid()) }

func Setgid(gid uint32) int32 { return setID(gid, os.Getgid(), os.Getegid()) }

func Seteuid(uid uint32) int32 { return setID(uid, os.Geteuid()) }

func Setegid(gid uint32) int32 { return setID(gid, os.Getegid()) }

func Setreuid(ruid, euid uint32) int32 {
	if ruid != ^uint32(0) && setID(ruid, os.Getuid()) != 0 {
		return -1
	}
	if euid != ^uint32(0) {
		return setID(euid, os.Geteuid())
	}
	return 0
}

func Setregid(rgid, egid uint32) int32 {
	if rgid != ^uint32(0) && setID(rgid, os.Getgid()) != 0 {
		return -1
	}
	if egid != ^uint32(0) {
		return setID(egid, os.Getegid())
	}
	return 0
}

// Sync is sync, which does nothing: Go syncs only a file.
func Sync() {}

var niceVal atomic.Int32

// Nice is nice: the nice value is only recorded, from 0, and may not be
// lowered (EPERM).
func Nice(inc int32) int32 {
	if inc < 0 {
		c23.SetErrno(c23.EPERM)
		return -1
	}
	for {
		old := niceVal.Load()
		v := min(old+min(inc, 39), 19)
		if niceVal.CompareAndSwap(old, v) {
			return v
		}
	}
}

// ttyPath: no file is a terminal C can name.
func ttyPath(f *os.File) (string, bool) { return "", false }
