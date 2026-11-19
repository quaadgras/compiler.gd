// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix

package stat

import (
	"syscall"
)

func umask(mask uint32) uint32 { return uint32(syscall.Umask(int(mask))) }
