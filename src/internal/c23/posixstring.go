// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

// strings.h, and string.h's POSIX functions.

func LowerASCII(c uint8) int32 {
	if 'A' <= c && c <= 'Z' {
		c += 'a' - 'A'
	}
	return int32(c)
}
