// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package abi_test

import (
	"internal/abi"
	"testing"
)

// scanRaw and scanHeapified are the comparison fixtures for Heapify's
// in-loop dispatch elimination. scanRaw indexes the parameter directly
// (per-access SSO dispatch); scanHeapified routes through Heapify so the
// indexing happens against a string the compiler can prove is heap-rep.

func scanRaw(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func scanHeapified(s string, c byte) int {
	cs := abi.Heapify(&s)
	for i := 0; i < len(cs); i++ {
		if cs[i] == c {
			return i
		}
	}
	return -1
}

var (
	benchHeapASCII   = makeASCII(100000)
	benchInlineASCII = "abcdefghijkl" // 12 bytes → SSO inline-rep
)

func makeASCII(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	return string(b)
}

func TestHeapifyEquivalence(t *testing.T) {
	cases := []struct {
		s    string
		c    byte
		want int
	}{
		{"", 'x', -1},
		{"abc", 'b', 1},
		{"abc", 'x', -1},
		{"hello world", 'o', 4},
		{makeASCII(50), 'x', 23},
	}
	for _, tc := range cases {
		if got := scanRaw(tc.s, tc.c); got != tc.want {
			t.Errorf("scanRaw(%q, %q) = %d, want %d", tc.s, tc.c, got, tc.want)
		}
		if got := scanHeapified(tc.s, tc.c); got != tc.want {
			t.Errorf("scanHeapified(%q, %q) = %d, want %d", tc.s, tc.c, got, tc.want)
		}
	}
}

func BenchmarkScanRaw_Heap(b *testing.B) {
	for i := 0; i < b.N; i++ {
		scanRaw(benchHeapASCII, 'x')
	}
}
func BenchmarkScanHeapified_Heap(b *testing.B) {
	for i := 0; i < b.N; i++ {
		scanHeapified(benchHeapASCII, 'x')
	}
}
func BenchmarkScanRaw_Inline(b *testing.B) {
	for i := 0; i < b.N; i++ {
		scanRaw(benchInlineASCII, 'l')
	}
}
func BenchmarkScanHeapified_Inline(b *testing.B) {
	for i := 0; i < b.N; i++ {
		scanHeapified(benchInlineASCII, 'l')
	}
}
