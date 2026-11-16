// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

import "unsafe"

// Linear memory holds the data that has no pointers and that other
// code may want as bytes: the arrays of slices whose elements have no
// pointers. The engine's collector does not see it. A block is owned by
// the object that points to it (a wasm.GCLinear); pointers derived from
// that one keep it alive; and when it is collected, the host calls
// linfree with the address of the block (see linregister).
//
// TODO: blocks are freed only when the host runs finalizers, which a
// program that never returns to the event loop does not let it do.

// The compiler replaces calls to these by instructions.

func linload8(addr uintptr) uint8
func linload16(addr uintptr) uint16
func linload32(addr uintptr) uint32
func linload64(addr uintptr) uint64
func linstore8(addr uintptr, v uint8)
func linstore16(addr uintptr, v uint16)
func linstore32(addr uintptr, v uint32)
func linstore64(addr uintptr, v uint64)

// heapstart returns the size of linear memory as the module made it:
// where the blocks of linalloc start.
//
//go:wasmimport gd heapstart
func heapstart() uint64

// linsize returns the size of linear memory.
func linsize() uintptr

// lingrow grows linear memory by a number of 64 KB pages.
func lingrow(pages uintptr)

// growmem grows linear memory by a number of 64 KB pages, on the host's
// own stack: V8 aborts when memory grows on a goroutine's stack after
// the host has looked at it ("Check failed: IsOnCentralStack"). A
// goroutine asks the host, which grows it from a microtask while the
// goroutine is suspended.
func growmem(pages uintptr) {
	if onSysStack {
		hostgrowsync(int32(pages))
		return
	}
	hostgrow(int32(pages))
}

//go:wasmimport gd grow
func hostgrow(pages int32) int32

//go:wasmimport gd growsync
func hostgrowsync(pages int32) int32

func linzero(addr, n uintptr)
func lincopy(dst, src, n uintptr)

// newlinear returns a pointer to linear memory that owns its block.
func newlinear(addr uintptr) unsafe.Pointer

// linaddr returns the address of a pointer to linear memory.
func linaddr(p unsafe.Pointer) uintptr

// linregister asks the host to call linfree(addr) once p is collected.
//
//go:wasmimport gd register
func linregister(p unsafe.Pointer, addr uintptr)

const maxLinear = 1 << 30

// The allocator: blocks of sizes that are powers of two, each with a
// header word that holds the power, and a free list per size. Blocks
// are never split or merged.
//
// TODO: something better for large blocks.

var (
	linfreelist [32]uintptr // by power: the first free block
	linbrk      uintptr     // the end of the blocks
)

const linheader = 8

// linalloc returns the address of n zeroed bytes.
func linalloc(n uintptr) uintptr {
	c := uintptr(4)
	for 1<<c < n+linheader {
		c++
	}
	if b := linfreelist[c]; b != 0 {
		linfreelist[c] = uintptr(linload64(b + linheader))
		linzero(b+linheader, 1<<c-linheader)
		return b + linheader
	}
	if linbrk == 0 {
		// Past everything the linker laid out (the size of the memory
		// before the host reserved more). Memory grown is zero.
		linbrk = uintptr(heapstart())
	}
	b := linbrk
	linbrk += 1 << c
	if size := linsize(); linbrk > size {
		// By a quarter of the memory at least: each growth costs
		// (see growmem).
		pages := (linbrk - size + 1<<16 - 1) >> 16
		if q := size >> 2 >> 16; pages < q {
			pages = q
		}
		growmem(pages)
	}
	linstore64(b, uint64(c))
	return b + linheader
}

// linfree frees the block at addr. The linker exports it to the host
// as "free".
func linfree(addr uintptr) {
	b := addr - linheader
	c := uintptr(linload64(b))
	if i := c >> 32; i != 0 {
		c &= 1<<32 - 1
		systemstack(func() { queueLinearCleanups(uint64(i - 1)) })
	}
	linstore64(b+linheader, uint64(linfreelist[c]))
	linfreelist[c] = b
}

// linnew returns a pointer to n zeroed bytes of linear memory, freed
// when it and the pointers derived from it are collected.
func linnew(n uintptr) unsafe.Pointer {
	addr := linalloc(n)
	p := newlinear(addr)
	linregister(p, addr)
	return p
}

// The header of a block holds its size class in its low 32 bits and, in
// its high ones, 1 + the index in linCleanups of the cleanups that
// runtime.AddCleanup attached to the block, to run when it is freed (0:
// none).

// linbase returns the address of the block of linear memory (from
// linalloc) that p points into, or 0 if p points to no such block (to
// data, a stack, or an object). The compiler replaces calls to it.
func linbase(p unsafe.Pointer) uintptr

// trylinaddr returns the address of a pointer to linear memory, or 0 if
// p is not one. The compiler replaces calls to it by instructions.
func trylinaddr(p unsafe.Pointer) uintptr
