// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stdlib

import (
	"bytes"
	"os"
	"syscall"
	"unsafe"
)

func grantpt(f *os.File) error { return ptyIoctl(f, syscall.TIOCPTYGRANT, 0) }

func unlockpt(f *os.File) error { return ptyIoctl(f, syscall.TIOCPTYUNLK, 0) }

func ptsname(f *os.File) (string, error) {
	var b [128]byte
	if err := ptyIoctl(f, syscall.TIOCPTYGNAME, uintptr(unsafe.Pointer(&b))); err != nil {
		return "", err
	}
	if i := bytes.IndexByte(b[:], 0); i >= 0 {
		return string(b[:i]), nil
	}
	return string(b[:]), nil
}
