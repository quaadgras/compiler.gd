// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package c23

import "os"

// flockFile is flock's operation op on f, where the system has no
// whole-file locks C can use: with no other process taking them, every
// lock is granted.
func flockFile(f *os.File, op int32) error { return nil }
