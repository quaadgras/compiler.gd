// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"reflect"
	"runtime"
	"testing"
)

// Signal size changes of important structures.
func TestSizeof(t *testing.T) {
	const _64bit = ^uint(0)>>32 != 0

	var tests = []struct {
		val    any     // type as a value
		_32bit uintptr // size on 32bit platforms
		_64bit uintptr // size on 64bit platforms
	}{

		// Types
		{Basic{}, 16, 32},
		{Array{}, 32, 40},
		{Slice{}, 24, 32},
		{Struct{}, 24, 32},
		{Pointer{}, 24, 32},
		{Tuple{}, 12, 16},
		{Signature{}, 32, 64},
		{Union{}, 12, 16},
		{Interface{}, 40, 64},
		{Map{}, 48, 64},
		{Chan{}, 28, 40},
		{Named{}, 100, 152},
		{TypeParam{}, 44, 64},
		{term{}, 28, 40},

		// Objects
		{PkgName{}, 56, 112},
		{Const{}, 76, 128},
		{TypeName{}, 52, 96},
		{Var{}, 60, 112},
		{Func{}, 60, 112},
		{Label{}, 56, 112},
		{Builtin{}, 56, 112},
		{Nil{}, 52, 96},

		// Misc
		{Scope{}, 44, 96},
		{Package{}, 44, 80},
		{_TypeSet{}, 28, 40},
	}
	if !_64bit && runtime.GOARCH == "wasm" {
		// wasm32 is not the 32-bit column: a string is 16 bytes, and an
		// interface and an int64 are aligned to 8.
		t.Skip("the 32-bit sizes are those of 386 and arm")
	}
	for _, test := range tests {
		got := reflect.TypeOf(test.val).Size()
		want := test._32bit
		if _64bit {
			want = test._64bit
		}
		if got != want {
			t.Errorf("unsafe.Sizeof(%T) = %d, want %d", test.val, got, want)
		}
	}
}
