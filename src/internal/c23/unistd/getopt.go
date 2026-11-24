// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package unistd

import (
	c23 "internal/c23"
)

func OptargLoc() *c23.Void { return c23.OptargObj }

func OptindLoc() *c23.Void { return c23.OptindObj }

func OpterrLoc() *c23.Void { return c23.OpterrObj }

func OptoptLoc() *c23.Void { return c23.OptoptObj }

// Getopt is glibc's getopt: it permutes argv so that the non-options come
// last, unless optstring starts with + or POSIXLY_CORRECT is set; with -
// it returns each non-option as the option 1, in optarg. Setting optind to
// 0 starts over.
func Getopt(argc int32, argv, optstring *c23.Void) int32 {
	return c23.Getopt(argc, argv, optstring, (*c23.Void)(nil), (*c23.Void)(nil), false)
}
