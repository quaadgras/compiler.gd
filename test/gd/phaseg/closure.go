// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Phase G.2.1: closures returning *T.
// Anonymous function literals create closures whose Type is a
// fresh func type. Must end up extended like any other
// pointer-returning sig.
package main

type T struct{ V int }

func main() {
	// Closure capturing a local.
	base := 100
	build := func() *T { return &T{V: base + 1} }
	a := build()
	if a == nil || a.V != 101 {
		println("FAIL: closure direct")
		return
	}

	// Closure stored in a variable of func() *T.
	var f func() *T = func() *T { return &T{V: 42} }
	b := f()
	if b == nil || b.V != 42 {
		println("FAIL: closure via func value")
		return
	}

	// Closure returned from a factory function.
	make2 := func(v int) func() *T {
		return func() *T { return &T{V: v * 2} }
	}
	g := make2(7)
	c := g()
	if c == nil || c.V != 14 {
		println("FAIL: closure returned from factory")
		return
	}

	println("ok")
}
