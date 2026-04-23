// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build 386 || (amd64 && !plan9) || arm || arm64 || mips || mipsle

// gd SSO: arm64's asm has been rewritten with a 24 B string prolog
// that spills inline-rep bytes to a local frame (indexbyte_arm64.s).
// The other 64-bit non-amd64 arches (loong64, ppc64{,le},
// mips64{,le}, riscv64, s390x, wasm) still have stock 16 B ABI asm
// and fall through to the generic Go fallback; port them as needed.
// 32-bit arches (386, arm, mips, mipsle) have no inline rep (12 B
// header), so their stock-shaped asm stays correct.

package bytealg

//go:noescape
func IndexByte(b []byte, c byte) int

//go:noescape
func IndexByteString(s string, c byte) int
