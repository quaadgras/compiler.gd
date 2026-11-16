// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package atomic

import "unsafe"

// StorepNoWB performs *ptr = val atomically and without a write
// barrier. (With GOWASM=gc there is one thread, and the engine's
// collector needs no barrier: atomic_wasm.s is not assembled.)
//
// NO go:noescape annotation; see atomic_pointer.go.
func StorepNoWB(ptr unsafe.Pointer, val unsafe.Pointer) {
	*(*unsafe.Pointer)(ptr) = val
}
