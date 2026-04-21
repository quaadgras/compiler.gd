// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package obj

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
		// gd small-string optimization: each `string` field is 24 B on
		// 64-bit (was 16 B), 12 B on 32-bit (was 8 B). LSym holds 2
		// string fields (Name, Pkg) → +16 bytes on 64-bit, +8 on 32-bit.
		{Addr{}, 48, 64},
		{LSym{}, 80, 136},
		{Prog{}, 164, 232},
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
