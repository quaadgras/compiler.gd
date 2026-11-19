// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package string

import (
	c23 "internal/c23"
	"strings"
)

// Strsep is strsep: the token at *stringp, which it ends at the first
// byte of delim with a null, and *stringp past that (or null).
func Strsep(stringp, delim c23.Ptr) c23.Ptr {
	s := c23.LoadPtr(stringp)
	if c23.Addr(s) == 0 {
		return c23.Ptr{}
	}
	d := c23.GoString(delim)
	for p := s; ; p = c23.Add(p, 1) {
		c := c23.Load8(p)
		if c == 0 {
			c23.StorePtr(stringp, c23.Ptr{})
			return s
		}
		if strings.IndexByte(d, c) >= 0 {
			c23.Store8(p, 0)
			c23.StorePtr(stringp, c23.Add(p, 1))
			return s
		}
	}
}
