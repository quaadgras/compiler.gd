// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !unix

package unistd

import (
	"internal/c23"
	"os"
)

// access checks mode against the permission bits of the file's owner.
func access(name string, mode uint32) error {
	fi, err := os.Stat(name)
	if err != nil {
		return err
	}
	if perm := uint32(fi.Mode().Perm()>>6) & mode; perm != mode {
		return c23.ErrnoErr(c23.EACCES)
	}
	return nil
}
