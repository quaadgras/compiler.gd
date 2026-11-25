// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !wasm.split

package c23

import (
	"internal/abi"
	"unsafe"
)

// seal returns the pointer that C is given to an object of the
// library's (a Handle, a Func, a VaList), and unseal the pointer to the
// object of type t that such a pointer is to, or nil. They are the
// pointer itself here: what keeps C out of the object is that it holds
// an interface and nothing else, which the compiler's checks and the
// runtime's refuse C (see Handle).
func seal(p unsafe.Pointer) *Void { return (*Void)(p) }

func unseal(s *seen, p *Void, t *abi.Type) unsafe.Pointer {
	if !s.is(unsafe.Pointer(p), t) {
		return nil
	}
	return unsafe.Pointer(p)
}
