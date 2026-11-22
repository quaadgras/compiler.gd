// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types_test

import (
	"go/types"
	"reflect"
	"runtime"
	"testing"
	"unsafe"
)

// Types that hold strings, which the gd fork aligns to 16 with 8-byte
// words (doc/gd/sso-string.md). gdSizesSrc declares them again for the
// type checker.
type (
	gdS1 struct {
		a int
		s string
	}
	gdS2 struct {
		b byte
		s string
		c byte
	}
	gdS3 struct {
		a [3]byte
		s [2]string
		p *int
	}
	gdS4 struct {
		x int32
		y gdS2
		z []string
		i any
		m map[string]string
	}
	gdS5 [3]gdS2
)

const gdSizesSrc = `
package p

type (
	gdS1 struct {
		a int
		s string
	}
	gdS2 struct {
		b byte
		s string
		c byte
	}
	gdS3 struct {
		a [3]byte
		s [2]string
		p *int
	}
	gdS4 struct {
		x int32
		y gdS2
		z []string
		i any
		m map[string]string
	}
	gdS5 [3]gdS2
	gdStr string
)
`

// TestGCSizesAgree checks that the sizes, alignments and offsets the type
// checker computes for the gc compiler and the target are those of the
// compiler, as reflect and unsafe see them.
func TestGCSizesAgree(t *testing.T) {
	pkg := mustTypecheck(gdSizesSrc, &types.Config{Sizes: types.SizesFor("gc", runtime.GOARCH)}, nil)
	sizes := types.SizesFor("gc", runtime.GOARCH)
	for _, v := range []any{"", gdS1{}, gdS2{}, gdS3{}, gdS4{}, gdS5{}} {
		rt := reflect.TypeOf(v)
		name := rt.Name()
		if name == "string" {
			name = "gdStr"
		}
		typ := pkg.Scope().Lookup(name).Type()
		if got, want := sizes.Sizeof(typ), int64(rt.Size()); got != want {
			t.Errorf("Sizeof(%s) = %d, want %d", name, got, want)
		}
		if got, want := sizes.Alignof(typ), int64(rt.Align()); got != want {
			t.Errorf("Alignof(%s) = %d, want %d", name, got, want)
		}
		st, ok := typ.Underlying().(*types.Struct)
		if !ok {
			continue
		}
		var fields []*types.Var
		for i := range st.NumFields() {
			fields = append(fields, st.Field(i))
		}
		offs := sizes.Offsetsof(fields)
		for i := range fields {
			if got, want := offs[i], int64(rt.Field(i).Offset); got != want {
				t.Errorf("Offsetof(%s.%s) = %d, want %d", name, fields[i].Name(), got, want)
			}
		}
	}

	// And the constants the compiler folds: a string of two 8-byte
	// words is aligned to 16.
	if got, want := unsafe.Alignof(""), uintptr(reflect.TypeFor[string]().Align()); got != want {
		t.Errorf("unsafe.Alignof(\"\") = %d, reflect says %d", got, want)
	}
	if unsafe.Sizeof(uintptr(0)) == 8 && unsafe.Sizeof("") == 16 && unsafe.Alignof(gdS1{}) != 16 {
		t.Errorf("unsafe.Alignof(gdS1{}) = %d, want 16", unsafe.Alignof(gdS1{}))
	}
}
