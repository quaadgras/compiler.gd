// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package errno

import (
	c23 "internal/c23"
)

// ErrnoLoc returns the pointer to the calling thread's errno
// (__cc_errno).
func ErrnoLoc() *c23.Void { return c23.ErrnoLoc() }
