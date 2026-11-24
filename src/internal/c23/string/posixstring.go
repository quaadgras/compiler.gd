// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package string

import (
	c23 "internal/c23"
)

// Stpcpy is stpcpy: strcpy, which returns the end of d.
func Stpcpy(d, s *c23.Void) *c23.Void {
	n := Strlen(s)
	c23.Memmove(d, s, uintptr(n+1))
	return c23.Add(d, int(n))
}

// Stpncpy is stpncpy: strncpy, which returns d's first null, or d+n.
func Stpncpy(d, s *c23.Void, n uint64) *c23.Void {
	k := Strnlen(s, n)
	c23.Memmove(d, s, uintptr(k))
	c23.Memset(c23.Add(d, int(k)), 0, n-k)
	return c23.Add(d, int(k))
}

// Strlcpy is strlcpy: at most n-1 bytes of s, and a null, at d; it
// returns the length of s.
func Strlcpy(d, s *c23.Void, n uint64) uint64 {
	l := Strlen(s)
	if n > 0 {
		k := min(l, n-1)
		c23.Memmove(d, s, uintptr(k))
		c23.Store8(c23.Add(d, int(k)), 0)
	}
	return l
}

// Strlcat is strlcat: s appended to the string at d, within n bytes; it
// returns the length of the string it tried to make.
func Strlcat(d, s *c23.Void, n uint64) uint64 {
	dl := Strnlen(d, n)
	if dl == n {
		return n + Strlen(s)
	}
	return dl + Strlcpy(c23.Add(d, int(dl)), s, n-dl)
}
