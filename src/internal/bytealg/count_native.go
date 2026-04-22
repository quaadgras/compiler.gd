// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// gd SSO: non-amd64 64-bit arches (arm64, loong64, mips64{,le},
// ppc64{,le}, riscv64, s390x) are dropped from the native list — their
// CountString asm uses the stock 16-byte string ABI and has no
// inline-rep prolog like amd64's. They fall back to the generic Go
// implementation in count_generic.go, which is correct under the
// fork. 32-bit arm stays on native because 32-bit strings have no
// inline rep (12 B header).

//go:build amd64 || arm

package bytealg

//go:noescape
func Count(b []byte, c byte) int

//go:noescape
func CountString(s string, c byte) int

// A backup implementation to use by assembly.
func countGeneric(b []byte, c byte) int {
	n := 0
	for _, x := range b {
		if x == c {
			n++
		}
	}
	return n
}
func countGenericString(s string, c byte) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			n++
		}
	}
	return n
}
