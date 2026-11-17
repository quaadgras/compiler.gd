// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

import (
	"internal/goarch"
	"unsafe"
)

// nextslicecap computes the next appropriate slice length.
func nextslicecap(newLen, oldCap int) int {
	newcap := oldCap
	doublecap := newcap + newcap
	if newLen > doublecap {
		return newLen
	}
	const threshold = 256
	if oldCap < threshold {
		return doublecap
	}
	for {
		// Transition from growing 2x for small slices
		// to growing 1.25x for large slices.
		newcap += (newcap + 3*threshold) >> 2
		if uint(newcap) >= uint(newLen) {
			break
		}
	}
	if newcap <= 0 {
		return newLen
	}
	return newcap
}

// growslicer is growslice for a slice of pointers, whose array is an
// array of references.
func growslicer(oldPtr *unsafe.Pointer, newLen, oldCap, num int) []unsafe.Pointer {
	oldLen := newLen - num
	if newLen < 0 {
		panicmakeslicelen()
	}
	memstatsMallocs++
	s := make([]unsafe.Pointer, newLen, roundupcap(nextslicecap(newLen, oldCap), goarch.PtrSize))
	old := unsafe.Slice(oldPtr, oldLen)
	for i := range old {
		s[i] = old[i]
	}
	return s
}

// A slice whose elements have no pointers is in linear memory.

// makeslicelin is makeslice for elements of the size given.
func makeslicelin(size, len, cap int) unsafe.Pointer {
	memstatsMallocs++
	checkmakeslice(size, len, cap)
	return linnew(uintptr(cap * size))
}

// checkmakeslice panics as makeslice does if a slice of len and cap
// elements of the size given cannot be made.
func checkmakeslice(size, len, cap int) {
	tooBig := func(n int) bool { return size != 0 && n > maxLinear/size }
	if cap < 0 || tooBig(cap) || len < 0 || len > cap {
		if len < 0 || tooBig(len) {
			panicmakeslicelen()
		}
		panicmakeslicecap()
	}
}

// makeslicer is makeslice for elements that are pointers.
func makeslicer(len, cap int) unsafe.Pointer {
	memstatsMallocs++
	checkmakeslice(goarch.PtrSize, len, cap)
	return unsafe.Pointer(unsafe.SliceData(make([]unsafe.Pointer, cap)))
}

// growslicelin is growslice for elements of the size given.
func growslicelin(oldPtr unsafe.Pointer, newLen, oldCap, num, size int) (unsafe.Pointer, int, int) {
	oldLen := newLen - num
	if newLen < 0 || size != 0 && newLen > maxLinear/size {
		panicmakeslicelen()
	}
	newCap := nextslicecap(newLen, oldCap)
	if size != 0 && newCap > maxLinear/size {
		newCap = newLen
	} else {
		newCap = roundupcap(newCap, size)
	}
	memstatsMallocs++
	p := linnew(uintptr(newCap * size))
	if oldLen > 0 && size > 0 {
		// The old array may not be in linear memory: it may be a
		// slice of an array variable.
		memmove(p, oldPtr, uintptr(oldLen*size))
	}
	return p, newLen, newCap
}

// A slice whose elements are more than one scalar and have pointers is
// an array of objects, one per element.

// The compiler replaces calls to these by instructions.

// newobjarray returns an array of n nil objects.
func newobjarray(n int) unsafe.Pointer

// isobjarray reports whether p is an array of objects. The compiler
// replaces a call to it.
func isobjarray(p unsafe.Pointer) bool
func objarrayget(a unsafe.Pointer, i int) unsafe.Pointer
func objarrayset(a unsafe.Pointer, i int, p unsafe.Pointer)

// gcclone returns a copy of the object that holds a Go value.
func gcclone(p unsafe.Pointer) unsafe.Pointer

// boxobj and boxoff return the object and the byte offset of a pointer
// into an object: the pointer and 0 if it is not one.
func boxobj(p unsafe.Pointer) unsafe.Pointer
func boxoff(p unsafe.Pointer) uintptr

// makesliceobj is makeslice for elements that are objects, of which
// zero is the zero value.
func makesliceobj(zero unsafe.Pointer, len, cap int) unsafe.Pointer {
	memstatsMallocs++
	checkmakeslice(goarch.PtrSize, len, cap)
	a := newobjarray(cap)
	for i := 0; i < cap; i++ {
		objarrayset(a, i, gcclone(zero))
	}
	return a
}

// growsliceobj is growslice for elements that are objects, of the Go
// size given.
func growsliceobj(oldPtr unsafe.Pointer, newLen, oldCap, num, size int, zero unsafe.Pointer) (unsafe.Pointer, int, int) {
	oldLen := newLen - num
	if newLen < 0 {
		panicmakeslicelen()
	}
	newCap := roundupcap(nextslicecap(newLen, oldCap), size)
	memstatsMallocs++
	a := newobjarray(newCap)
	// The elements of the new array are not those of the old.
	old, first := boxobj(oldPtr), 0
	if size != 0 {
		first = int(boxoff(oldPtr)) / size
	}
	if oldLen > 0 && !isobjarray(old) {
		// The array of a single element, which is a struct.
		objarrayset(a, 0, gcclone(old))
	} else {
		for i := 0; i < oldLen; i++ {
			objarrayset(a, i, gcclone(objarrayget(old, first+i)))
		}
	}
	for i := oldLen; i < newCap; i++ {
		objarrayset(a, i, gcclone(zero))
	}
	return a, newLen, newCap
}

// makeslicezero and growslicezero are makeslice and growslice for
// elements of size zero: there is no array.
func makeslicezero(len, cap int) unsafe.Pointer {
	if len < 0 || len > cap {
		panicmakeslicelen()
	}
	return unsafe.Pointer(&zerobase)
}

func growslicezero(oldPtr unsafe.Pointer, newLen, oldCap, num int) (unsafe.Pointer, int, int) {
	if newLen < 0 {
		panicmakeslicelen()
	}
	return unsafe.Pointer(&zerobase), newLen, newLen
}

// sizeClasses are the sizes Go's allocator rounds a small allocation up
// to (internal/runtime/gc): growing a slice gives it the capacity that
// Go would, which programs and tests can see.
var sizeClasses = [...]uint16{8, 16, 24, 32, 48, 64, 80, 96, 112, 128, 144, 160, 176, 192, 208, 224, 240, 256, 288, 320, 352, 384, 416, 448, 480, 512, 576, 640, 704, 768, 896, 1024, 1152, 1280, 1408, 1536, 1792, 2048, 2304, 2688, 3072, 3200, 3456, 4096, 4864, 5376, 6144, 6528, 6784, 6912, 8192, 9472, 9728, 10240, 10880, 12288, 13568, 14336, 16384, 18432, 19072, 20480, 21760, 24576, 27264, 28672, 32768}

// roundupsize returns the size of the block Go would allocate for size
// bytes.
func roundupsize(size uintptr) uintptr {
	if size <= 32768 {
		for _, c := range sizeClasses {
			if uintptr(c) >= size {
				return uintptr(c)
			}
		}
	}
	return (size + 8191) &^ 8191
}

// roundupcap returns the capacity Go would give a slice of cap elements
// of size bytes.
func roundupcap(cap, size int) int {
	if size == 0 || cap == 0 {
		return cap
	}
	return int(roundupsize(uintptr(cap*size)) / uintptr(size))
}
