// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Phase G.2.1: named func type.
// `type Factory func() *T` creates a named type whose underlying
// is an extended func type. Named types inherit the Phase G flag
// via SetUnderlying (types/type.go), so FillOutBufArgs triggers
// on calls through the named type.
package main

type T struct{ X int }

type Factory func() *T

//go:noinline
func Make() *T { return &T{X: 7} }

func main() {
	var f Factory = Make
	t := f()
	if t == nil || t.X != 7 {
		println("FAIL: named func type direct")
		return
	}

	// Named type parameter: function taking a Factory.
	result := use(f)
	if result == nil || result.X != 7 {
		println("FAIL: named func type param")
		return
	}

	println("ok")
}

//go:noinline
func use(fn Factory) *T { return fn() }
