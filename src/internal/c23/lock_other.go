// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !unix

package c23

import (
	"os"
	"time"
)

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

// rusage is getrusage's struct rusage, as longs: the time since the
// program started as its user time.
func rusage(who int32) ([18]int64, error) {
	var ru [18]int64
	if who != RUSAGE_CHILDREN {
		d := time.Since(start)
		ru[0], ru[1] = int64(d/time.Second), int64(d%time.Second/time.Microsecond)
	}
	return ru, nil
}
