// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stat

import (
	"syscall"
	"time"
	"unsafe"
)

// lutimes sets the times of the symbolic link name itself (a zero time
// is not changed).
func lutimes(name string, at, mt time.Time) error {
	p, err := syscall.BytePtrFromString(name)
	if err != nil {
		return err
	}
	var ts [2]syscall.Timespec
	for i, t := range [2]time.Time{at, mt} {
		if t.IsZero() {
			ts[i].Nsec = UTIME_OMIT
		} else {
			ts[i] = syscall.NsecToTimespec(t.UnixNano())
		}
	}
	atFdcwd := -100
	_, _, e := syscall.Syscall6(syscall.SYS_UTIMENSAT, uintptr(atFdcwd), uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&ts)), 0x100, 0, 0)
	if e != 0 {
		return e
	}
	return nil
}
