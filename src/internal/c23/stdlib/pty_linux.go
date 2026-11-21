// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stdlib

import (
	"os"
	"strconv"
	"syscall"
	"unsafe"
)

// grantpt: devpts makes the slave the user's; fd must be a master.
func grantpt(f *os.File) error {
	_, err := ptsname(f)
	return err
}

func unlockpt(f *os.File) error {
	var unlock int32
	return ptyIoctl(f, syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock)))
}

func ptsname(f *os.File) (string, error) {
	var n uint32
	if err := ptyIoctl(f, syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); err != nil {
		return "", err
	}
	return "/dev/pts/" + strconv.FormatUint(uint64(n), 10), nil
}
