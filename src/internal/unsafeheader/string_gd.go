// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !compiler_bootstrap

package unsafeheader

import "unsafe"

// String is the runtime representation of a string under the gd
// small-string optimization: 3-word header (ptr, hash, len).
// See doc/gd/sso-string.md.
//
// Hash is word 1 — cached 64-bit string hash for heap-rep strings
// (always zero in Phase A; inline bytes[0:8] under the future inline
// rep). Len is word 2 with the upper 4 bits reserved as the inline
// length tag (always zero in Phase A).
//
// The bootstrap variant (string_bootstrap.go, //go:build
// compiler_bootstrap) keeps the 2-word layout so the host-Go-built
// cmd/compile does not corrupt its own stack writing through this type.
type String struct {
	Data unsafe.Pointer
	Hash uint
	Len  int
}
