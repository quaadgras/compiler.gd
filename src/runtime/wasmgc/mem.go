// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

import (
	"internal/goarch"
	"unsafe"
)

// Bytes through pointers. A pointer to linear memory is an address, and
// these are the instructions of linear memory; any other pointer goes
// through the accessors the linker generates, a byte at a time.

func add(p unsafe.Pointer, x uintptr) unsafe.Pointer {
	return unsafe.Add(p, x)
}

// memmove copies n bytes from "from" to "to".
func memmove(to, from unsafe.Pointer, n uintptr) {
	if n == 0 {
		return
	}
	if splitMode {
		splitmove(to, from, n)
		return
	}
	t, f := trylinaddr(to), trylinaddr(from)
	if t != 0 && f != 0 {
		lincopy(t, f, n)
		return
	}
	// Within an object, pointers are ordered as integers (their
	// identity is that of the object, plus the offset): a move to
	// after where it overlaps goes backwards.
	if t, f := uintptr(to), uintptr(from); t > f && t-f < n {
		for i := n; i > 0; i-- {
			*(*byte)(add(to, i-1)) = *(*byte)(add(from, i-1))
		}
		return
	}
	for i := uintptr(0); i < n; i++ {
		*(*byte)(add(to, i)) = *(*byte)(add(from, i))
	}
}

// memequal reports whether the n bytes at p and at q are the same.
func memequal(p, q unsafe.Pointer, n uintptr) bool {
	if n == 0 {
		return true
	}
	if splitMode {
		splitcheck(p, n)
		splitcheck(q, n)
	}
	a, b := trylinaddr(p), trylinaddr(q)
	if a != 0 && b != 0 {
		if a == b {
			return true
		}
		i := uintptr(0)
		for ; i+8 <= n; i += 8 {
			if linload64(a+i) != linload64(b+i) {
				return false
			}
		}
		for ; i < n; i++ {
			if linload8(a+i) != linload8(b+i) {
				return false
			}
		}
		return true
	}
	for i := uintptr(0); i < n; i++ {
		if *(*byte)(add(p, i)) != *(*byte)(add(q, i)) {
			return false
		}
	}
	return true
}

func memclrNoHeapPointers(p unsafe.Pointer, n uintptr) {
	if splitMode {
		splitzero(p, n)
		return
	}
	if a := trylinaddr(p); a != 0 {
		linzero(a, n)
		return
	}
	for i := uintptr(0); i < n; i++ {
		*(*byte)(add(p, i)) = 0
	}
}

func memequal0(p, q unsafe.Pointer) bool   { return true }
func memequal8(p, q unsafe.Pointer) bool   { return memequal(p, q, 1) }
func memequal16(p, q unsafe.Pointer) bool  { return memequal(p, q, 2) }
func memequal32(p, q unsafe.Pointer) bool  { return memequal(p, q, 4) }
func memequal64(p, q unsafe.Pointer) bool  { return memequal(p, q, 8) }
func memequal128(p, q unsafe.Pointer) bool { return memequal(p, q, 16) }

// getclosureptr returns the function value that the calling function
// was called through. The compiler replaces calls to it.
func getclosureptr() uintptr

// closurectxt returns the closure that was last called, which is in a
// global that the function called takes it from, and setclosurectxt
// sets it: for what runs between a call and the function's own code
// (see morestackgc). The compiler replaces calls to them.
func closurectxt() unsafe.Pointer
func setclosurectxt(p unsafe.Pointer)

// memequal_varlen and memhash_varlen are the functions of function
// values that carry a size in their second word.

func memequal_varlen(a, b unsafe.Pointer) bool {
	size := *(*uintptr)(staticptr(getclosureptr() + goarch.PtrSize))
	return memequal(a, b, size)
}

func memhash_varlen(p unsafe.Pointer, h uintptr) uintptr {
	size := *(*uintptr)(staticptr(getclosureptr() + goarch.PtrSize))
	return memhash(p, h, size)
}
