// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"testing"
	"unsafe"
)

// A union of pointers, as cmd/cc lays one out: no bytes, and the
// pointer to its value.
type testUnion struct {
	_ [0]uint64
	p UnionRef
}

// TestUnionPtr checks that the library reads the pointer that C stored
// as a member of a union, and that C reads the one that the library
// stored: sigaction's handlers are such a union.
func TestUnionPtr(t *testing.T) {
	u := new(testUnion)
	v := (*Void)(unsafe.Pointer(new(int64)))
	*UnionBox[*Void](&u.p) = v
	got := UnionPtr((*Void)(unsafe.Pointer(u)), 0)
	if got != v {
		t.Errorf("UnionPtr = %p, want %p", got, v)
	}
	w := new(testUnion)
	SetUnionPtr((*Void)(unsafe.Pointer(w)), 0, v)
	if got := *UnionBox[*Void](&w.p); got != v {
		t.Errorf("after SetUnionPtr the member is %p, want %p (ref %p)", got, v, w.p)
	}
}
