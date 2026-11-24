// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ir

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/types"
	"cmd/internal/src"
)

// C objects.
//
// C lets a program make the pointer one past the end of an object, and
// keep it: the end of an array that a loop runs to, or back from. To Go
// that pointer is one to the next object of the heap, or to none: the
// collector would keep the wrong object alive and stop on a pointer to
// a free one, and the checks of pointer arithmetic (doc/gd/unsafe.md)
// refuse it. So in a package that is C translated by cmd/cc, one that
// imports internal/c23 (base.Invocation.CObjects), a variable that is
// moved to the heap, and what new allocates, is allocated with a byte
// after it, as struct { v T; _ byte }: the pointer one past its end is
// then a pointer into it. (Go does the same for a struct that ends in
// a field of no size.) The stack, and the data of the program, are each
// one allocation to both the collector and the checks.

// CObjType returns the type that an object of type t is allocated as on
// the heap. The types must be made before the functions are compiled
// (walk does, for what a function may allocate): making one needs its
// size calculated, which the back end, running in parallel, may not do.
func CObjType(gd *base.Invocation, t *types.Type) *types.Type {
	if !gd.CObjects {
		return t
	}
	m, _ := gd.CObjTypes.(map[*types.Type]*types.Type)
	if p := m[t]; p != nil {
		return p
	}
	if gd.CalcSizeDisabled {
		gd.Fatalf("C object of type %v has no type to be allocated as", t)
	}
	if m == nil {
		m = map[*types.Type]*types.Type{}
		gd.CObjTypes = m
	}
	types.CalcSize(gd, t)
	p := t
	if t.Size() != 0 && !t.NotInHeap() {
		p = types.NewStruct([]*types.Field{
			types.NewField(src.NoXPos, types.LocalPkg(gd).Lookup("v"), t),
			types.NewField(src.NoXPos, types.BlankSym(gd), types.Types[types.TUINT8]),
		})
		p.SetNoalg(true)
		types.CalcSize(gd, p)
	}
	m[t] = p
	return p
}
