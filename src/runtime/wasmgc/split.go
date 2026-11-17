// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc && wasm.split

package runtime

import "unsafe"

// With GOWASM=gc,split (stage 15 of doc/gd/wasm-gc.md) every Go value
// is in linear memory, laid out as Go lays it out, and the pointers in
// it are also in an array of references that the block it is in has, a
// word per element. A pointer is the address, that array, and where
// the address is from element 0 of it: unsafe.Add, uintptr arithmetic
// that is converted straight back and copies by size alone all keep
// the pointers a value has. The engine's collector follows the arrays;
// a block is freed when its array is collected (see linfree).
//
// The code of the other model that this one does without stays in the
// files, under !splitMode, which the compiler leaves out.

const splitMode = true

// The compiler replaces calls to these by instructions.

// newblockptr returns a pointer to the block of linear memory at addr,
// with a new array of nslots nil pointers.
func newblockptr(addr, nslots uintptr) unsafe.Pointer

// ptroff returns how far the address of p is from where element 0 of
// its array is.
func ptroff(p unsafe.Pointer) uintptr

// ptrnslots returns the length of the array of p (0 if it has none).
func ptrnslots(p unsafe.Pointer) uintptr

// splitmove copies n bytes from src to dst, and the pointers among
// them; splitzero zeroes n bytes at p, and the pointers among them.
func splitmove(dst, src unsafe.Pointer, n uintptr)
func splitzero(p unsafe.Pointer, n uintptr)

// splitalloc returns a pointer to n zeroed bytes in a new block, which
// has an array for the pointers of its first nslots words.
func splitalloc(n, nslots uintptr) unsafe.Pointer {
	if n == 0 {
		return unsafe.Pointer(&zerobase)
	}
	addr := linalloc(n)
	p := newblockptr(addr, nslots)
	linregister(p, addr)
	return p
}

// newarray returns a pointer to n zeroed values of type t.
func newarray(t *_type, n int) unsafe.Pointer {
	if n < 0 || t.Size_ != 0 && uintptr(n) > maxLinear/t.Size_ {
		panicmakeslicelen()
	}
	size := uintptr(n) * t.Size_
	var nslots uintptr
	if t.Pointers() && n > 0 {
		nslots = (size - t.Size_ + t.PtrBytes + 7) / 8
	}
	return splitalloc(size, nslots)
}

// newobject returns a pointer to a new zeroed value of type t. (The
// compiler counts the allocation, in code that is not the runtime's.)
func newobject(t *_type) unsafe.Pointer {
	return newarray(t, 1)
}

func makeslice(et *_type, len, cap int) unsafe.Pointer {
	checkmakeslice(int(et.Size_), len, cap)
	memstatsMallocs++
	return newarray(et, cap)
}

func growslice(oldPtr unsafe.Pointer, newLen, oldCap, num int, et *_type) slice {
	oldLen := newLen - num
	size := int(et.Size_)
	if newLen < 0 || size != 0 && newLen > maxLinear/size {
		panicmakeslicelen()
	}
	if size == 0 {
		return slice{unsafe.Pointer(&zerobase), newLen, newLen}
	}
	newCap := nextslicecap(newLen, oldCap)
	if newCap > maxLinear/size {
		newCap = newLen
	} else {
		newCap = roundupcap(newCap, size)
	}
	memstatsMallocs++
	p := newarray(et, newCap)
	if oldLen > 0 {
		splitmove(p, oldPtr, uintptr(oldLen*size))
	}
	return slice{p, newLen, newCap}
}

type slice struct {
	array unsafe.Pointer
	len   int
	cap   int
}

// splitbase returns the address of the block of linear memory that p
// points into, or 0 if it is not into one (data, a stack).
func splitbase(p unsafe.Pointer) uintptr {
	a := uintptr(p)
	if a < uintptr(heapstart()) || a >= linbrk {
		return 0
	}
	return a - ptroff(p)
}
