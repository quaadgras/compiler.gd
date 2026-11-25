// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.split

package c23

import (
	"internal/abi"
	"unsafe"
)

// seal returns the pointer that C is given to an object of the
// library's (a Handle, a Func, a VaList), and unseal the pointer to the
// object of type t that such a pointer is to, or nil.
//
// With GOWASM=gc,split a pointer has the size of what it points into,
// and code loads and stores only inside: the pointer that C is given
// has no size. C keeps it, compares it and gives it back, and looks at
// nothing through it. The runtime makes the pointer to the object of
// it again if it is the pointer to the start of a block that
// newObject allocated as a t, which neither arithmetic on a pointer to
// another block nor an integer gives.
func seal(p unsafe.Pointer) *Void { return (*Void)(sealPtr(p)) }

func unseal(s *seen, p *Void, t *abi.Type) unsafe.Pointer {
	return unsealPtr(unsafe.Pointer(p), t)
}

//go:linkname sealPtr runtime.c23Seal
func sealPtr(p unsafe.Pointer) unsafe.Pointer

//go:linkname unsealPtr runtime.c23Unseal
func unsealPtr(p unsafe.Pointer, t *abi.Type) unsafe.Pointer
