// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc && wasm.split

package runtime

import (
	"internal/goarch"
	"unsafe"
)

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

// The array of a block has an element for each pointer word
// (goarch.PtrSize bytes: 4 with wasm32), whatever is in it: the
// pointer at byte b of a block is element b/goarch.PtrSize.

// splitslots returns the number of elements of the array of a block of
// n bytes that has pointers: one for every word, to the end.
func splitslots(n uintptr) uintptr {
	return (n + goarch.PtrSize - 1) / goarch.PtrSize
}

// The compiler replaces calls to these by instructions.

// newblockptr returns a pointer to the block of linear memory at addr,
// of size bytes, with a new array of nslots nil pointers.
func newblockptr(addr, nslots, size uintptr) unsafe.Pointer

// ptrlim returns the size of what p points into, from where element 0
// of its array is: 0 for a pointer that was made of an integer.
func ptrlim(p unsafe.Pointer) uintptr

// ptroff returns how far the address of p is from where element 0 of
// its array is.
func ptroff(p unsafe.Pointer) uintptr

// ptrnslots returns the length of the array of p (0 if it has none).
func ptrnslots(p unsafe.Pointer) uintptr

// splitmove copies n bytes from src to dst, and the pointers among
// them; splitzero zeroes n bytes at p, and the pointers among them.
func splitmove(dst, src unsafe.Pointer, n uintptr)
func splitzero(p unsafe.Pointer, n uintptr)

// splitmovec and splitzeroc are splitmove and splitzero for pointers of
// the program's: the n bytes are in what each points into, or the
// program stops.
func splitmovec(dst, src unsafe.Pointer, n uintptr)
func splitzeroc(p unsafe.Pointer, n uintptr)

// splitcheck stops the program unless the n bytes at p are in what p
// points into, and splitcheckw unless they are also bytes that may be
// stored in: for what the runtime loads and stores by its address
// (linaddr), where no load through the pointer asks.
func splitcheck(p unsafe.Pointer, n uintptr)
func splitcheckw(p unsafe.Pointer, n uintptr)

// splitsetlim returns p as a pointer to something of lim bytes: for
// what the runtime gives the program a pointer to that it must not look
// into (lim 0), and for making that the pointer to it again.
func splitsetlim(p unsafe.Pointer, lim uintptr) unsafe.Pointer

// splitintptr returns the integer v as a pointer of the program's: one
// with no size, which nothing is loaded or stored through (nil for 0).
func splitintptr(v uintptr) unsafe.Pointer

// splitalloc returns a pointer to n zeroed bytes in a new block, which
// has an array for the pointers of its first nslots words.
func splitalloc(n, nslots uintptr) unsafe.Pointer {
	if n == 0 {
		return unsafe.Pointer(&zerobase)
	}
	addr := linalloc(n)
	p := newblockptr(addr, nslots, n)
	linregister(p, addr)
	return p
}

// newarray returns a pointer to n zeroed values of type t.
func newarray(t *_type, n int) unsafe.Pointer {
	if n < 0 || t.Size_ != 0 && uintptr(n) > maxLinear/t.Size_ {
		panicmakeslicelen()
	}
	size := uintptr(n) * t.Size_
	// (An element for every word, to the end: a copy of whole values
	// copies the words of each, pointers or not.)
	var nslots uintptr
	if t.Pointers() {
		nslots = splitslots(size)
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

// makeslice64 is makeslice for a length or a capacity of 64 bits where
// an int is 32 (wasm32).
func makeslice64(et *_type, len64, cap64 int64) unsafe.Pointer {
	len := int(len64)
	if int64(len) != len64 {
		panicmakeslicelen()
	}
	cap := int(cap64)
	if int64(cap) != cap64 {
		panicmakeslicecap()
	}
	return makeslice(et, len, cap)
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

// typeWord is the word of an interface that holds its type or its
// table. With split it is a pointer in memory, as the compiler has it
// (cmd/compile/internal/ssa's Types.ITab): it is read as the pointer
// that was stored there, and not made of an integer.
type typeWord = unsafe.Pointer

func typeWordOf(p unsafe.Pointer) typeWord  { return p }
func typeWordPtr(w typeWord) unsafe.Pointer { return w }

// splitfault is what code that is not the runtime's calls where it
// would load or store through a pointer something that is not in what
// the pointer points into (cmd/compile/internal/wasm/gcsplit.go): the
// program stops, and cannot recover.
func splitfault() {
	fatal("load or store through a pointer that is not in what it points into, or through one that the program made of an integer")
}

// staticptr returns the pointer to the address addr of the program's
// data: for the runtime's walks of what the linker laid out (the itabs,
// the descriptors of types by their offsets), which it has as integers.
// The pointer is one into all of the data. An integer that is no
// address of the data gives a pointer that nothing is loaded through.
//
// It is the one conversion of an integer to a pointer that the runtime
// makes for itself: the code of the runtime is checked as any other
// (cmd/compile/internal/wasm/gcsplit.go), and a conversion elsewhere is
// to a pointer with no size.
//
//go:nocheckptr
//go:noinline
func staticptr(addr uintptr) unsafe.Pointer { return unsafe.Pointer(addr) }

// syscall_hostBytes stops the program unless the n bytes at p are in
// what p points into: package syscall gives the host their address
// (GOOS=wasip1; syscall/js has its own, in jscall.go).
//
//go:linkname syscall_hostBytes syscall.hostBytes
func syscall_hostBytes(p unsafe.Pointer, n int) {
	if n > 0 {
		splitcheck(p, uintptr(n))
	}
}
