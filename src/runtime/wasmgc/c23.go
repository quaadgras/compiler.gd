// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc && wasm.split

package runtime

import "unsafe"

// Hooks for C compiled by cmd/cc (internal/c23, doc/gd/cc.md), with
// GOWASM=gc,split. Every value has an address in linear memory and every
// pointer knows its block, so finding an object needs no lookup; stacks
// are never copied, so the side tables need no re-keying.

// c23Object returns the base of the block that p points into, as a
// pointer derived from p (so with p's array of pointers), and the bytes
// from there to the end of the blocks; nil, 0 if p is not into one.
//
//go:linkname c23Object
func c23Object(p unsafe.Pointer) (unsafe.Pointer, uintptr) {
	if splitbase(p) == 0 {
		return nil, 0
	}
	base := unsafe.Add(p, -int(ptroff(p)))
	return base, linbrk - uintptr(base)
}

// c23Stack returns no bounds: a goroutine's stack never moves, so C's
// side tables key its words by address, as they do the heap's.
//
//go:linkname c23Stack
func c23Stack() (lo, hi uintptr) { return 0, 0 }

// c23GTable returns the goroutine's slot for internal/c23's table of
// it (_Thread_local objects, errno, pthread keys, thread id).
//
//go:linkname c23GTable
func c23GTable() *unsafe.Pointer { return &curg.c23 }

//go:linkname c23SetStackMove
func c23SetStackMove(f func(t unsafe.Pointer, lo, hi, delta uintptr)) {}
