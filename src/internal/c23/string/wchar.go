// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package string

import (
	c23 "internal/c23"
)

func Strxfrm(d, s *c23.Void, n uint64) uint64 {
	k := uint64(Strlen(s))
	if k < n {
		c23.Memmove(d, s, uintptr(k+1))
	}
	return k
}
