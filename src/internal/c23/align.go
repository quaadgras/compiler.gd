// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"reflect"
	"unsafe"
)

const alignMagic = 0xA11_C0DE_CAB_A11

// NewAligned allocates a T for C in the heap, aligned to align bytes:
// more than Go aligns anything to (C's _Alignas). The object is bigger
// than the T: a marker at its start, then padding, then the header and
// the T at an offset that is a multiple of align. Go's allocator puts an
// object of a power-of-two size class at a multiple of that size within
// its page-aligned span, and a large object at the start of a page, so
// the object is made one of those. Alignments beyond a page are a page.
func NewAligned[T any](align uintptr) Ptr {
	ti := tinfoOf[T]()
	const page = 8192
	align = min(max(align, 8), page)
	size := ti.t.Size()
	off := (16 + hdrSize + align - 1) / align * align
	// Go may put a header of its own at the start of an object; the
	// first try finds out how far the T lands from a multiple of align,
	// and the next moves it there.
	for try := 0; ; try++ {
		total := off + size
		var objSize uintptr
		// Go puts a header in a scanned object (the header has
		// pointers) bigger than 512 bytes, which a large object (over
		// 32kB) does without.
		if total+align <= 512 {
			objSize = 16
			for objSize < total+align || objSize < align {
				objSize *= 2
			}
		} else {
			objSize = max(total+align, 32<<10+1) // a large object
		}
		st := reflect.StructOf([]reflect.StructField{
			{Name: "M", Type: reflect.TypeFor[[2]uintptr]()},
			{Name: "P", Type: reflect.ArrayOf(int(off-16-hdrSize), reflect.TypeFor[byte]())},
			{Name: "H", Type: reflect.TypeFor[header]()},
			{Name: "V", Type: ti.t},
			{Name: "T", Type: reflect.ArrayOf(int(objSize-total), reflect.TypeFor[byte]())},
		})
		base := newObj(st)
		v := uintptr(base) + off
		if d := v % align; d != 0 && try < 3 {
			off += align - d
			continue
		}
		*(*[2]uintptr)(base) = [2]uintptr{alignMagic, off}
		h := (*header)(unsafe.Add(base, off-hdrSize))
		*h = header{magic: magic, ti: ti, size: size}
		return Ptr{obj: unsafe.Add(base, off), size: size, ti: cref(ti)}
	}
}
