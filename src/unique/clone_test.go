// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package unique

import (
	"internal/abi"
	"reflect"
	"testing"
	"unsafe"
)

func TestMakeCloneSeq(t *testing.T) {
	// gd: strides of strings are strSize (16 bytes on wasm32 too), and
	// testStruct's string follows a float64 at strAlign.
	testCloneSeq[testString](t, cSeq(0))
	testCloneSeq[testIntArray](t, cSeq())
	testCloneSeq[testEface](t, cSeq())
	testCloneSeq[testStringArray](t, cSeq(0, strSize, 2*strSize))
	testCloneSeq[testStringStruct](t, cSeq(0))
	testCloneSeq[testStringStructArrayStruct](t, cSeq(0, strSize))
	testCloneSeq[testStruct](t, cSeq(max(8, strAlign)))
}

const (
	strSize  = unsafe.Sizeof("")
	strAlign = unsafe.Alignof("")
)

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
