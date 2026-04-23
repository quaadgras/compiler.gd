// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Avoid IndexByte and IndexByteString on Plan 9 because it uses
// SSE instructions on x86 machines, and those are classified as
// floating point instructions, which are illegal in a note handler.
//
// gd SSO: 64-bit non-amd64 arches fall into this generic fallback
// because their asm predates the fork's 24-byte string ABI / inline
// rep. See indexbyte_native.go for the rationale.

//go:build !386 && (!amd64 || plan9) && !arm && !arm64 && !mips && !mipsle

package bytealg

func IndexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

func IndexByteString(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
