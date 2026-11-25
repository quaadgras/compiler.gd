// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package devctl is devctl.h's posix_devctl for cmd/cc: ioctl's
// commands, its result an error number.
package devctl

import (
	"internal/c23"
	"internal/c23/sys/ioctl"
)

// PosixDevctl is posix_devctl: what ioctl does with dcmd and data, the
// error number returned rather than set, ioctl's result at info.
func PosixDevctl(fd, dcmd int32, data *c23.Void, nbyte uint64, info *c23.Void) int32 {
	r := ioctl.Ioctl(fd, uint64(uint32(dcmd)), data)
	if r < 0 {
		return c23.Errno()
	}
	if c23.Addr(info) != 0 {
		c23.Store32(info, uint32(r))
	}
	return 0
}
