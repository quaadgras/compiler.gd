// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package unique

import (
	"internal/abi"
	"internal/goarch"
	"reflect"
	"testing"
)

func TestMakeCloneSeq(t *testing.T) {
	// gd small-string optimization: each `string` is 3 words (was 2),
	// so per-element strides in arrays of strings (and arrays-of-structs-
	// of-strings) become 3*PtrSize instead of 2*PtrSize.
	testCloneSeq[testString](t, cSeq(0))
	testCloneSeq[testIntArray](t, cSeq())
	testCloneSeq[testEface](t, cSeq())
	testCloneSeq[testStringArray](t, cSeq(0, 3*goarch.PtrSize, 6*goarch.PtrSize))
	testCloneSeq[testStringStruct](t, cSeq(0))
	testCloneSeq[testStringStructArrayStruct](t, cSeq(0, 3*goarch.PtrSize))
	testCloneSeq[testStruct](t, cSeq(8))
}

func cSeq(stringOffsets ...uintptr) cloneSeq {
	return cloneSeq{stringOffsets: stringOffsets}
}

func testCloneSeq[T any](t *testing.T, want cloneSeq) {
	typName := reflect.TypeFor[T]().Name()
	typ := abi.TypeFor[T]()
	t.Run(typName, func(t *testing.T) {
		got := makeCloneSeq(typ)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("unexpected cloneSeq for type %s: got %#v, want %#v", typName, got, want)
		}
	})
}
