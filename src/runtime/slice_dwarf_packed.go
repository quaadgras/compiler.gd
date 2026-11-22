// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !386 && !arm && !mips && !mipsle && !wasm

package runtime

import "unsafe"

// sliceDWARF is a slice in memory, for DWARF (the linker's
// synthesizeslicetypes): with 8-byte words but on wasm its length and
// capacity are uint32s, or array is the address of a huge slice's header
// and cap 0xFFFFFFFF (doc/gd/slice16.md).
type sliceDWARF struct {
	array unsafe.Pointer
	len   uint32
	cap   uint32
}
