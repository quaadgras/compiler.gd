// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !linux && !darwin

package ioctl

import (
	"internal/c23"
	"os"
)

// ioctlInRead is FIONREAD of what is not a regular file, which the system
// does not give.
func ioctlInRead(f *os.File) (int64, error) { return 0, c23.ErrnoErr(c23.ENOTTY) }

// ioctlWinsize is TIOCGWINSZ, which the system does not give.
func ioctlWinsize(f *os.File) ([4]uint16, error) { return [4]uint16{}, c23.ErrnoErr(c23.ENOTTY) }
