// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stdio

import (
	c23 "internal/c23"
)

func Fgetpos(f, pos c23.Ptr) int32 {
	off := Ftell(f)
	if off < 0 {
		return -1
	}
	c23.Store64(pos, uint64(off))
	return 0
}

func Fsetpos(f, pos c23.Ptr) int32 { return Fseek(f, int64(c23.Load64(pos)), 0) }
