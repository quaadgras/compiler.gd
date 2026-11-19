// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package threads

import (
	c23 "internal/c23"
)

// PthreadSelf is pthread_self: 0 for the main thread.
func PthreadSelf() uint64 { return c23.Load64(c23.ThreadID.Get()) }

func PthreadEqual(a, b uint64) int32 { return c23.B2I(a == b) }
