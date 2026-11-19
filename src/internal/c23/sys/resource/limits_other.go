// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package resource

import c23 "internal/c23"

// The system has no priorities or limits C can see: the nice value is
// 0, which may not be changed, and there are no limits, which may not
// be set.

func getpriority(which, who int) (int, error) { return 0, nil }

func setpriority(which, who, prio int) error { return c23.ErrnoErr(c23.EPERM) }

func getrlimit(res int32) (cur, max uint64, err error) { return RLIM_INFINITY, RLIM_INFINITY, nil }

func setrlimit(res int32, cur, max uint64) error { return c23.ErrnoErr(c23.EPERM) }
