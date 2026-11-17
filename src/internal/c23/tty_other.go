// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !linux && !darwin

package c23

import (
	"io/fs"
	"os"
)

// isatty reports whether f is a terminal: a character device, which most
// are (but /dev/null too).
func isatty(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&fs.ModeCharDevice != 0
}

// ioctlInRead is FIONREAD of what is not a regular file, which the system
// does not give.
func ioctlInRead(f *os.File) (int64, error) { return 0, errnoErr(ENOTTY) }

// ioctlWinsize is TIOCGWINSZ, which the system does not give.
func ioctlWinsize(f *os.File) ([4]uint16, error) { return [4]uint16{}, errnoErr(ENOTTY) }
