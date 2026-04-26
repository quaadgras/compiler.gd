// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types2

import (
	"reflect"
	"testing"
)

// Signal size changes of important structures.

func TestSizeof(t *testing.T) {
	const _64bit = ^uint(0)>>32 != 0

	// gd small-string optimization: each `string` field is 24 B on
	// 64-bit (was 16 B), 12 B on 32-bit (was 8 B). Sizes here include
	// the +8/+4 per string field for every struct that holds a string
	// (either directly or via embedded `object`, which has `name string`).
	var tests = []struct {
		val    any     // type as a value
		_32bit uintptr // size on 32bit platforms
		_64bit uintptr // size on 64bit platforms
	}{
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

		// Objects (all embed `object`, which has 1 string field "name")
		{PkgName{}, 76, 120},
		{Const{}, 96, 144},
		{TypeName{}, 72, 112},
		{Var{}, 80, 128},
		{Func{}, 80, 128},
		{Label{}, 76, 120},
		{Builtin{}, 76, 120},
		{Nil{}, 72, 112},

		// Misc
		{Scope{}, 64, 112},   // +1 string (comment)
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
