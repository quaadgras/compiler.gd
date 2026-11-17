// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import "math/bits"

// strings.h, and string.h's POSIX functions.

func lowerASCII(c uint8) int32 {
	if 'A' <= c && c <= 'Z' {
		c += 'a' - 'A'
	}
	return int32(c)
}

func Strcasecmp(a, b Ptr) int32 { return Strncasecmp(a, b, ^uint64(0)) }

func Strncasecmp(a, b Ptr, n uint64) int32 {
	for ; n > 0; n-- {
		x, y := lowerASCII(Load8(a)), lowerASCII(Load8(b))
		if x != y || x == 0 {
			return x - y
		}
		a, b = Add(a, 1), Add(b, 1)
	}
	return 0
}

func Bzero(p Ptr, n uint64) { Memset(p, 0, n) }

// Bcopy is bcopy: memmove, with the source first.
func Bcopy(src, dst Ptr, n uint64) { Memmove(dst, src, uintptr(n)) }

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

// Stpcpy is stpcpy: strcpy, which returns the end of d.
func Stpcpy(d, s Ptr) Ptr {
	n := Strlen(s)
	Memmove(d, s, uintptr(n+1))
	return Add(d, int(n))
}

// Stpncpy is stpncpy: strncpy, which returns d's first null, or d+n.
func Stpncpy(d, s Ptr, n uint64) Ptr {
	k := Strnlen(s, n)
	Memmove(d, s, uintptr(k))
	Memset(Add(d, int(k)), 0, n-k)
	return Add(d, int(k))
}

// Strlcpy is strlcpy: at most n-1 bytes of s, and a null, at d; it
// returns the length of s.
func Strlcpy(d, s Ptr, n uint64) uint64 {
	l := Strlen(s)
	if n > 0 {
		k := min(l, n-1)
		Memmove(d, s, uintptr(k))
		Store8(Add(d, int(k)), 0)
	}
	return l
}

// Strlcat is strlcat: s appended to the string at d, within n bytes; it
// returns the length of the string it tried to make.
func Strlcat(d, s Ptr, n uint64) uint64 {
	dl := Strnlen(d, n)
	if dl == n {
		return n + Strlen(s)
	}
	return dl + Strlcpy(Add(d, int(dl)), s, n-dl)
}
