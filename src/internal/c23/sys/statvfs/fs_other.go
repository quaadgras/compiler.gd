// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !(aix || darwin || dragonfly || freebsd || linux || openbsd)

package statvfs

import (
	c23 "internal/c23"
	"os"
)

// statfs fails where package syscall has no statfs (Windows, wasm, the
// other Unixes): ENOSYS.
func statfs(path string) (fsInfo, error) {
	if _, err := os.Stat(path); err != nil {
		return fsInfo{}, err
	}
	return fsInfo{}, c23.ErrnoErr(c23.ENOSYS)
}

func fstatfs(f *os.File) (fsInfo, error) { return fsInfo{}, c23.ErrnoErr(c23.ENOSYS) }
