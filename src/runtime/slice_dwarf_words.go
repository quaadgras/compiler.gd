// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build 386 || arm || mips || mipsle || wasm

package runtime

import "unsafe"

// sliceDWARF is a slice in memory, for DWARF (the linker's
// synthesizeslicetypes): three words.
type sliceDWARF struct {
	array unsafe.Pointer
	len   int
	cap   int
}
