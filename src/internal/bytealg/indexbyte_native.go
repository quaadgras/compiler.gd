// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build 386 || (amd64 && !plan9) || arm || mips || mipsle

// gd SSO: 64-bit non-amd64 arches (arm64, loong64, ppc64{,le}, mips64{,le},
// riscv64, s390x, wasm) are deliberately not listed here. Their asm
// implementations were written for the stock 16-byte string ABI and
// never updated for the fork's 24-byte header — they still read s_len
// from word 1 (the hash slot) and c from word 2 (part of len). They
// also have no inline-rep prolog like amd64. Until per-arch asm is
// rewritten, those arches fall through to the generic Go fallback in
// indexbyte_generic.go, which uses the compiler's inline-aware len /
// index lowering and is correct by construction. 32-bit arches (386,
// arm, mips, mipsle) have no inline rep (12 B header), so their
// stock-shaped asm stays correct.

package bytealg

//go:noescape
func IndexByte(b []byte, c byte) int

//go:noescape
func IndexByteString(s string, c byte) int
