// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// cmpstringFallback is the Go implementation of string comparison used
// by non-amd64/arm/arm64 arches whose per-arch cmpstring asm still
// reads the stock 16 B string header. len(a) / a[i] lower to tag-aware
// helpers under the fork, so the generic byte loop is correct for
// heap, inline, and mixed representations. The per-arch compare_*.s
// stubs jump here via ·cmpstringFallback.

//go:build loong64 || mips64 || mips64le || ppc64 || ppc64le || riscv64 || s390x || wasm

package bytealg

//go:nosplit
func cmpstringFallback(a, b string) int {
	l := len(a)
	if len(b) < l {
		l = len(b)
	}
	for i := 0; i < l; i++ {
		c1, c2 := a[i], b[i]
		if c1 < c2 {
			return -1
		}
		if c1 > c2 {
			return +1
		}
	}
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return +1
	}
	return 0
}
