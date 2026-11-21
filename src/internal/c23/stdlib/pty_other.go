// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !linux && !darwin

package stdlib

import (
	c23 "internal/c23"
	"os"
)

const havePty = false

var errNoPty = c23.ErrnoErr(c23.EINVAL)

func grantpt(f *os.File) error { return errNoPty }

func unlockpt(f *os.File) error { return errNoPty }

func ptsname(f *os.File) (string, error) { return "", errNoPty }
