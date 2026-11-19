// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !linux

package stat

import (
	c23 "internal/c23"
	"time"
)

// lutimes cannot set a symbolic link's own times.
func lutimes(name string, at, mt time.Time) error { return c23.ErrnoErr(c23.EOPNOTSUPP) }
