// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build amd64 || arm64

package runtime

import "unsafe"

// memmove16 copies n bytes, a multiple of 16, from src to dst, 16 bytes
// at a time, each with one access (MOVUPS, LDP/STP): a string aligned to
// 16 in them is read and written whole where stringAtomic. Overlapping
// memory is copied as memmove copies it.
//
//go:noescape
func memmove16(dst, src unsafe.Pointer, n uintptr)

// memclr16 clears n bytes, a multiple of 16, at p, 16 bytes at a time,
// each with one access.
//
//go:noescape
func memclr16(p unsafe.Pointer, n uintptr)
