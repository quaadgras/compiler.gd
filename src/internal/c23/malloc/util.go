// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package malloc

import (
	c23 "internal/c23"
)

// MallocUsableSize is malloc_usable_size: the size of what malloc
// returned, which is what it was asked for.
func MallocUsableSize(p *c23.Void) uint64 {
	if p == nil {
		return 0
	}
	return c23.SizeOf1(p)
}

// Memalign is memalign: aligned_alloc, with the alignment rounded up to
// a power of two, as glibc does.
func Memalign(align, size uint64) *c23.Void {
	a := uint64(1)
	for a < align {
		a <<= 1
	}
	return c23.AlignedAlloc(a, size)
}

func Valloc(size uint64) *c23.Void { return c23.AlignedAlloc(4096, size) }

func Pvalloc(size uint64) *c23.Void {
	return c23.AlignedAlloc(4096, (size+4095)&^4095)
}
