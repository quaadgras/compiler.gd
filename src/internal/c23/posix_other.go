// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !unix

package c23

import (
	"os"
	"sync/atomic"
)

// access checks mode against the permission bits of the file's owner.
func access(name string, mode uint32) error {
	fi, err := os.Stat(name)
	if err != nil {
		return err
	}
	if perm := uint32(fi.Mode().Perm()>>6) & mode; perm != mode {
		return errnoErr(EACCES)
	}
	return nil
}

var umaskVal atomic.Uint32

func init() { umaskVal.Store(0o022) }

// umask records the mask, which the system does not have.
func umask(mask uint32) uint32 { return umaskVal.Swap(mask) }
