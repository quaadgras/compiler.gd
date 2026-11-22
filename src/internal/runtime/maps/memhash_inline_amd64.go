// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !wasm.gc

package maps

const memHashInlineImplemented = true

// memHashInlineAES is memHashAES of the bytes of the inline string whose
// words are w0 and w1 (doc/gd/sso-string.md), without storing them.
//
//go:noescape
func memHashInlineAES(w1, w0, h uintptr) uintptr
