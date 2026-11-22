// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build compiler_bootstrap || !wasm.gc

package unsafeheader

import "unsafe"

// String is the runtime representation of a string: two words, as in
// stock Go (and in the host Go of a bootstrap compilation). With 8-byte
// words a short string is inline instead: see internal/abi/string.go
// (Data has bit 63 set).
// GOWASM=gc has others (string_gd.go, string_ptr32.go).
type String struct {
	Data unsafe.Pointer
	Len  int
}
