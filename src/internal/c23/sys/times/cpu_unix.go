// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix

package times

import (
	"syscall"
	"time"
)

func tv(t syscall.Timeval) time.Duration {
	return time.Duration(t.Sec)*time.Second + time.Duration(t.Usec)*time.Microsecond
}

// cpuTimes returns the user and system times of the process and of its
// children.
func cpuTimes() (u, s, cu, cs time.Duration) {
	var self, children syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &self)
	syscall.Getrusage(syscall.RUSAGE_CHILDREN, &children)
	return tv(self.Utime), tv(self.Stime), tv(children.Utime), tv(children.Stime)
}
