// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ir

import (
	"reflect"
	"testing"
	"unsafe"
)

// Assert that the size of important structures do not change unexpectedly.

func TestSizeof(t *testing.T) {
	const _64bit = unsafe.Sizeof(uintptr(0)) == 8

	var tests = []struct {
		val    any     // type as a value
		_32bit uintptr // size on 32bit platforms
		_64bit uintptr // size on 64bit platforms
	}{
		// gd fork: bitset8 widened from uint8 → struct{n uint32} so
		// concurrent in-process compile invocations can CAS bit-flag
		// updates on shared (BuiltinPkg/UnsafePkg) miniNode bitsets.
		// That adds 8 bytes (3 bytes pad before the uint32 + 4 bytes
		// data + 1 byte pad after esc) on every Node — accounted for
		// in the +8 across all four sizes below.
		{Func{}, 220, 376},
		{Name{}, 160, 248},
		{miniExpr{}, 48, 72},
		{miniNode{}, 28, 32},
	}

	for _, tt := range tests {
		want := tt._32bit
		if _64bit {
			want = tt._64bit
		}
		got := reflect.TypeOf(tt.val).Size()
		if want != got {
			t.Errorf("unsafe.Sizeof(%T) = %d, want %d", tt.val, got, want)
		}
	}
}
