// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Phase G.2.1: function value assignment.
// The motivating failure case for Phase G.2.0 (virtual-view): two
// structurally identical `func() *T` types where one was declared
// as a package-level func (flagged) and one as a variable's type
// (unflagged) led to register mismatch on indirect call.
//
// With NewSignature-at-construction + universal extension, both
// types pass through the same factory and end up extended, so
// indirect calls match.
package main

type T struct{ X int }

//go:noinline
func NewT() *T { return &T{X: 99} }

//go:noinline
func apply(f func() *T) *T { return f() }

func main() {
	// Direct assignment into a variable of func() *T.
	var f func() *T = NewT
	a := f()
	if a == nil || a.X != 99 {
		println("FAIL: func value assignment")
		return
	}

	// Pass as argument into a function expecting func() *T.
	b := apply(NewT)
	if b == nil || b.X != 99 {
		println("FAIL: func value argument")
		return
	}

	// Stored in a slice, then called.
	fs := []func() *T{NewT, NewT, NewT}
	for i, fn := range fs {
		r := fn()
		if r == nil || r.X != 99 {
			println("FAIL: func value from slice", i)
			return
		}
	}

	// Stored in a map, keyed by string.
	m := map[string]func() *T{"default": NewT}
	c := m["default"]()
	if c == nil || c.X != 99 {
		println("FAIL: func value from map")
		return
	}

	println("ok")
}
