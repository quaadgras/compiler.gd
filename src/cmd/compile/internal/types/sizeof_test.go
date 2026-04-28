// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

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
		// gd small-string optimization: each `string` field grows by
		// 8 B on 64-bit / 4 B on 32-bit. Sym has 2 string fields
		// (Linkname, Name) → +16 / +8.
		//
		// gd parallelism-ast: Type gained a cache struct of two
		// atomic.Pointer[Type] (cache.ptr, cache.slice) so concurrent
		// in-process compile invocations don't race on shared composite-
		// type caching. +16 B on 64-bit / +8 B on 32-bit. Then a
		// per-Type calcSizeOnce sync.Once was added to replace the
		// process-global map[*Type]*sync.Once that pinned every Type
		// for cmd/go's lifetime under in-process compile (commit
		// 31c94ef222). +12 B on 64-bit / +8 B on 32-bit, padded.
		{Sym{}, 64, 96},
		{Type{}, 112, 144},
		{Map{}, 12, 24},
		{Forward{}, 20, 32},
		{Func{}, 32, 56},
		{Struct{}, 12, 24},
		{Interface{}, 0, 0},
		{Chan{}, 8, 16},
		{Array{}, 12, 16},
		{FuncArgs{}, 4, 8},
		{ChanArgs{}, 4, 8},
		{Ptr{}, 4, 8},
		{Slice{}, 4, 8},
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
