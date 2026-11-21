// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix

package resource

import (
	"syscall"
)

// rusage is getrusage's struct rusage, as longs.
func rusage(who int32) ([18]int64, error) {
	w := syscall.RUSAGE_SELF
	if who == RUSAGE_CHILDREN {
		w = syscall.RUSAGE_CHILDREN
	}
	var ru syscall.Rusage
	if err := syscall.Getrusage(w, &ru); err != nil {
		return [18]int64{}, err
	}
	return [18]int64{
		int64(ru.Utime.Sec), int64(ru.Utime.Usec), int64(ru.Stime.Sec), int64(ru.Stime.Usec),
		int64(ru.Maxrss), int64(ru.Ixrss), int64(ru.Idrss), int64(ru.Isrss), int64(ru.Minflt),
		int64(ru.Majflt), int64(ru.Nswap), int64(ru.Inblock), int64(ru.Oublock), int64(ru.Msgsnd),
		int64(ru.Msgrcv), int64(ru.Nsignals), int64(ru.Nvcsw), int64(ru.Nivcsw),
	}, nil
}
