// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"reflect"
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
		// gd small-string optimization: each `string` field is 24 B on
		// 64-bit (was 16 B), 12 B on 32-bit (was 8 B). Sizes here include
		// +8/+4 per string field for every struct that holds a string
		// (directly or via embedded `object`, which has `name string`).

		// Types
		{Basic{}, 20, 40}, // +1 string (name)
		{Array{}, 32, 40},
		{Slice{}, 24, 32},
		{Struct{}, 24, 48},
		{Pointer{}, 24, 32},
		{Tuple{}, 12, 24},
		{Signature{}, 28, 56},
		{Union{}, 12, 24},
		{Interface{}, 40, 80},
		{Map{}, 48, 64},
		{Chan{}, 28, 40},
		{Named{}, 100, 160},
		{TypeParam{}, 44, 64},
		{term{}, 28, 40},

		// Objects (all embed `object` with 1 string field)
		{PkgName{}, 60, 104},
		{Const{}, 80, 128},
		{TypeName{}, 56, 96},
		{Var{}, 64, 112},
		{Func{}, 64, 112},
		{Label{}, 60, 104},
		{Builtin{}, 60, 104},
		{Nil{}, 56, 96},

		// Misc
		{Scope{}, 48, 96},  // +1 string (comment)
		{Package{}, 56, 112}, // +3 strings (path, name, goVersion)
		{_TypeSet{}, 28, 56},
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
