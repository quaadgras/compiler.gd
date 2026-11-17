// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !compiler_bootstrap && wasm.ptr32

package unsafeheader

import "unsafe"

// String is the runtime representation of a string on wasm32 (see
// internal/abi/string_ptr32.go). Data and Len are the address of the
// bytes and the length of a string that is not kept in its header,
// whose tag, the top 4 bits of Hash, is 0; a string of 1 to 15 bytes
// kept there has its bytes from Data on, and Data is not a pointer
// (use internal/abi's StringIsInline and StringBytes first).
type String struct {
	Data unsafe.Pointer
	Len  int
	Hash uint64
}
