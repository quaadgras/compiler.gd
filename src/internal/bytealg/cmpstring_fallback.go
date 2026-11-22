// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// cmpstringFallback is runtime.cmpstring on the 64-bit targets whose
// per-arch cmpstring asm reads a string as its address and its length
// (stock Go's) but for amd64 and arm64: an inline string
// (doc/gd/sso-string.md) is copied to the frame first, then Compare,
// the target's assembly, compares the bytes. The per-arch compare_*.s
// stubs jump here via ·cmpstringFallback.

//go:build loong64 || mips64 || mips64le || ppc64 || ppc64le || riscv64 || s390x || (wasm && !wasm.gc)

package bytealg

import (
	"internal/abi"
	"unsafe"
)

//go:nosplit
func cmpstringFallback(a, b string) int {
	var ab, bb [16]byte
	return Compare(stringBytes(&a, &ab), stringBytes(&b, &bb))
}

// stringBytes returns the bytes of *sp, copied to buf if it is inline.
//
//go:nosplit
func stringBytes(sp *string, buf *[16]byte) []byte {
	if abi.StringIsInline(*sp) {
		var n int
		*buf, n = abi.StringInlineBytes(*sp)
		return buf[:n]
	}
	sh := (*struct {
		p unsafe.Pointer
		n int
	})(unsafe.Pointer(sp))
	return unsafe.Slice((*byte)(sh.p), sh.n)
}
