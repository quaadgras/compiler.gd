// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// gd SSO: arm64's CountString asm has been rewritten with a 24 B
// string prolog (count_arm64.s). The other 64-bit non-amd64 arches
// (loong64, mips64{,le}, ppc64{,le}, riscv64, s390x) still use stock
// 16 B asm and fall back to the generic Go implementation in
// count_generic.go; port them as needed.

//go:build amd64 || arm || arm64

package bytealg

import "unsafe"

// (No race instrumentation, as the assembly has none: the runtime calls
// it before it may.)
//
//go:norace
func Count(b []byte, c byte) int {
	return countSlice(unsafe.SliceData(b), len(b), cap(b), c)
}

// The assembly takes the words of stock Go's slices, the array, the
// length and the capacity: with 8-byte words a slice is packed, or is a
// huge one's header (doc/gd/slice16.md), which it would not read.
//
//go:noescape
func countSlice(b *byte, blen, bcap int, c byte) int

//go:noescape
func CountString(s string, c byte) int

// A backup implementation to use by assembly.
// (The words of the slice, as countSlice has them.)
func countGeneric(b *byte, blen, bcap int, c byte) int {
	n := 0
	for _, x := range unsafe.Slice(b, blen) {
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
