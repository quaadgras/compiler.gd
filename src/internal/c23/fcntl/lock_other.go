// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !unix

package fcntl

import "os"

// lockFile is fcntl's lock command cmd of l on f, where the system has no
// record locks C can use: a process's locks never conflict with its own,
// so with no other process taking them every lock is granted, and F_GETLK
// finds none.
func lockFile(f *os.File, cmd int32, l *flock) error {
	if cmd == F_GETLK {
		l.typ = F_UNLCK
	}
	return nil
}
