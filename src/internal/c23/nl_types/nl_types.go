// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package nl_types is nl_types.h's functions for cmd/cc: there are no
// message catalogs, so catgets gives the program's own messages.
package nl_types

import (
	c23 "internal/c23"
)

// Catopen is catopen: (nl_catd)-1 and ENOENT, as no catalog exists.
func Catopen(name *c23.Void, oflag int32) *c23.Void {
	c23.SetErrno(c23.ENOENT)
	return c23.FromInt(^uintptr(0))
}

// Catgets is catgets: the default message s.
func Catgets(catd *c23.Void, setID, msgID int32, s *c23.Void) *c23.Void {
	if c23.Addr(catd) == ^uintptr(0) {
		c23.SetErrno(c23.EBADF)
	} else {
		c23.SetErrno(c23.ENOMSG)
	}
	return s
}

// Catclose is catclose: EBADF for (nl_catd)-1.
func Catclose(catd *c23.Void) int32 {
	if c23.Addr(catd) == ^uintptr(0) || c23.Addr(catd) == 0 {
		c23.SetErrno(c23.EBADF)
		return -1
	}
	return 0
}
