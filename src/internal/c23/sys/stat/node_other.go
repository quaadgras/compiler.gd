// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package stat

import c23 "internal/c23"

func mkfifo(name string, mode uint32) error { return c23.ErrnoErr(c23.ENOSYS) }

func mknod(name string, mode uint32, dev uint64) error { return c23.ErrnoErr(c23.ENOSYS) }
