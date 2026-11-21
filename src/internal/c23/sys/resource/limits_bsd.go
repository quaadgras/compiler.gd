// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build darwin || dragonfly || freebsd || openbsd || netbsd

package resource

import "syscall"

// sysResource is the system's resource of C's, which is Linux's: the
// BSDs' have the same first five, then RSS, MEMLOCK, NPROC and NOFILE.
func sysResource(res int32) (int, bool) {
	switch res {
	case RLIMIT_CPU, RLIMIT_FSIZE, RLIMIT_DATA, RLIMIT_STACK, RLIMIT_CORE, RLIMIT_RSS:
		return int(res), true
	case RLIMIT_MEMLOCK:
		return 6, true
	case RLIMIT_NPROC:
		return 7, true
	case RLIMIT_NOFILE:
		return syscall.RLIMIT_NOFILE, true
	case RLIMIT_AS:
		return rlimitAS()
	}
	return 0, false
}
