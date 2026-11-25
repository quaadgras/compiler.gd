// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package strings

import (
	c23 "internal/c23"
	"math/bits"
)

func Strcasecmp(a, b *c23.Void) int32 { return Strncasecmp(a, b, ^uint64(0)) }

func Strncasecmp(a, b *c23.Void, n uint64) int32 {
	for ; n > 0; n-- {
		x, y := c23.LowerASCII(c23.Load8(a)), c23.LowerASCII(c23.Load8(b))
		if x != y || x == 0 {
			return x - y
		}
		a, b = c23.Add(a, 1), c23.Add(b, 1)
	}
	return 0
}

func Bzero(p *c23.Void, n uint64) { c23.Memset(p, 0, n) }

// Bcopy is bcopy: memmove, with the source first.
func Bcopy(src, dst *c23.Void, n uint64) { c23.Memmove(dst, src, uintptr(n)) }

// Ffs is ffs: the position of i's least significant bit set, from 1, or 0.
func Ffs(i int32) int32 {
	if i == 0 {
		return 0
	}
	return int32(bits.TrailingZeros32(uint32(i))) + 1
}

// Ffsl is ffsl and ffsll.
func Ffsl(i int64) int32 {
	if i == 0 {
		return 0
	}
	return int32(bits.TrailingZeros64(uint64(i))) + 1
}
