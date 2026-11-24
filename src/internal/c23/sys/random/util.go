// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package random

import (
	c23 "internal/c23"
)

// randomFill fills b with random bytes.
func randomFill(b []byte) {
	for i := 0; i < len(b); i += 8 {
		x := c23.RuntimeRand()
		for j := i; j < i+8 && j < len(b); j++ {
			b[j] = byte(x)
			x >>= 8
		}
	}
}

// Getrandom is getrandom: buflen random bytes, from the runtime's
// generator, whatever the flags (GRND_NONBLOCK, GRND_RANDOM and
// GRND_INSECURE): it never blocks.
func Getrandom(buf *c23.Void, n uint64, flags uint32) int64 {
	if flags&^7 != 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	randomFill(c23.BytesAt(buf, uintptr(n)))
	return int64(n)
}

// Getentropy is getentropy: at most 256 random bytes (errno EIO for
// more).
func Getentropy(buf *c23.Void, n uint64) int32 {
	if n > 256 {
		c23.SetErrno(c23.EIO)
		return -1
	}
	randomFill(c23.BytesAt(buf, uintptr(n)))
	return 0
}
