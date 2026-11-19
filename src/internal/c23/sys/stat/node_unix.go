// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package stat

import "syscall"

func mkfifo(name string, mode uint32) error { return syscall.Mkfifo(name, mode) }

func mknod(name string, mode uint32, dev uint64) error {
	return mknodOf(syscall.Mknod, name, mode, dev)
}

// mknodOf calls the system's mknod, whose dev is an int or a uint64.
func mknodOf[D ~int | ~uint64](f func(string, uint32, D) error, name string, mode uint32, dev uint64) error {
	return f(name, mode, D(dev))
}
