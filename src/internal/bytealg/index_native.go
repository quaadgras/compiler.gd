// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build amd64 || arm64 || loong64 || s390x || ppc64le || ppc64

package bytealg

import "unsafe"

// Index returns the index of the first instance of b in a, or -1 if b is not present in a.
// Requires 2 <= len(b) <= MaxLen.
// (No race instrumentation, as the assembly has none: the runtime calls
// it before it may.)
//
//go:norace
func Index(a, b []byte) int {
	return indexSlices(unsafe.SliceData(a), len(a), cap(a), unsafe.SliceData(b), len(b), cap(b))
}

// The assembly takes the words of stock Go's slices, the array, the
// length and the capacity: with 8-byte words a slice is packed, or is a
// huge one's header (doc/gd/slice16.md), which it would not read.
//
//go:noescape
func indexSlices(a *byte, alen, acap int, b *byte, blen, bcap int) int
