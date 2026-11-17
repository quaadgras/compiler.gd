// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !wasm.split

package c23

import "unsafe"

// Tag marks a pointer word whose address is not a whole value of the
// type it points to: not canonical, so it faults where Go dereferences it
// (on wasm, where addresses are 32 bits, the compiler's nil checks catch
// it), and the collector does not take it for a pointer.
const Tag = 1 << 55

// noescape hides a pointer from escape analysis, as the runtime's does.
// The header is part of its object and lives exactly as long as it does;
// without this, making the object's side table would count as a leak of
// everything the object points to.
//
//go:nosplit
func noescape(p unsafe.Pointer) unsafe.Pointer {
	x := uintptr(p)
	return unsafe.Pointer(x ^ 0)
}
