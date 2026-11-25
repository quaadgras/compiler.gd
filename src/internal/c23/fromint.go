// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !wasm.split

package c23

import "unsafe"

// FromInt returns the pointer whose address is u, unchecked: for the
// library's own constants (MAP_FAILED, SIG_IGN) and for the bits that
// intcap.go keeps in a pointer. An integer of the program's is converted
// by IntPtr.
//
//go:nocheckptr
func FromInt(u uintptr) *Void { return (*Void)(unsafe.Pointer(u)) }
