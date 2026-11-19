// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package ulimit

import c23 "internal/c23"

// getFsize is no limit, where the system has none C can see.
func getFsize() (int64, error) { return longMax, nil }

func setFsize(n int64) error { return c23.ErrnoErr(c23.EPERM) }
