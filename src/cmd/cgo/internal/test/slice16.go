// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains //export directives on Go functions
// and so it must NOT contain C definitions (only declarations).

package cgotest

/*
#include <stdint.h>
#include <stdlib.h>

extern int sliceRoundTrip(void *, int64_t, int64_t);
*/
import "C"

import (
	"testing"
	"unsafe"
)

// A slice crosses into C as a GoSlice, stock Go's {data, len, cap}, also
// where Go's slices are packed, two words of which the second has the
// length and the capacity as uint32s (gd, doc/gd/slice16.md): the
// exported function's wrapper converts it, a huge one too.

//export cgoSliceRoundTrip
func cgoSliceRoundTrip(b []byte, i int) ([]byte, byte) {
	return b[1:], b[i]
}

func testSliceRoundTrip(t *testing.T) {
	// (C memory: a Go function may not return Go memory to C.)
	b := []byte("hello, world")
	p := C.CBytes(b)
	defer C.free(p)
	if r := C.sliceRoundTrip(p, C.int64_t(len(b)), 4); r != 0 {
		t.Fatalf("sliceRoundTrip of %d bytes: %d", len(b), r)
	}
	if p, n := hugeBytesForC(t); p != nil {
		const hi = 1<<32 + 5
		*(*byte)(unsafe.Add(p, hi)) = 7
		if r := C.sliceRoundTrip(p, C.int64_t(n), hi); r != 0 {
			t.Fatalf("sliceRoundTrip of %d bytes: %d", n, r)
		}
	}
}
