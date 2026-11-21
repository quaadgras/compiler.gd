// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix

package wait

import (
	"os"
	"syscall"
)

// sysRusage is the struct rusage, as longs, that the system gave of the
// child's end (os.ProcessState has no portable API but its times).
func sysRusage(ps *os.ProcessState) [18]int64 {
	ru, ok := ps.SysUsage().(*syscall.Rusage)
	if !ok || ru == nil {
		return [18]int64{}
	}
	return [18]int64{
		0, 0, 0, 0,
		int64(ru.Maxrss), int64(ru.Ixrss), int64(ru.Idrss), int64(ru.Isrss), int64(ru.Minflt),
		int64(ru.Majflt), int64(ru.Nswap), int64(ru.Inblock), int64(ru.Oublock), int64(ru.Msgsnd),
		int64(ru.Msgrcv), int64(ru.Nsignals), int64(ru.Nvcsw), int64(ru.Nivcsw),
	}
}
