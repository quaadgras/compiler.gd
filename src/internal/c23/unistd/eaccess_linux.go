// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package unistd

import "syscall"

// eaccess is access with the effective ids.
func eaccess(name string, mode uint32) error {
	const atEaccess = 0x200
	return syscall.Faccessat(-100, name, mode, atEaccess)
}
