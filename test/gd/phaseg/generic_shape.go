// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Phase G.2.1: generic shape collapse.
// Multiple pointer-type instantiations collapse to the same
// shape at codegen time (e.g. `*int` and `*string` both shape
// to `go.shape.*uint8`). The SINGLE shared body must match the
// extended ABI for every instantiation that flows through it.
package main

type Holder[T any] struct{ V T }

//go:noinline
func First[T any](xs []T) *T {
	if len(xs) == 0 {
		return nil
	}
	return &xs[0]
}

//go:noinline
func Make[T any](x T) *Holder[T] { return &Holder[T]{V: x} }

func main() {
	// First[*int] and First[*string] share shape go.shape.*uint8.
	ints := []*int{new(int), new(int)}
	*ints[0] = 10
	*ints[1] = 20
	ap := First(ints)
	if ap == nil || **ap != 10 {
		println("FAIL: First[*int]")
		return
	}

	strs := []*string{new(string), new(string)}
	*strs[0] = "hello"
	*strs[1] = "world"
	sp := First(strs)
	if sp == nil || **sp != "hello" {
		println("FAIL: First[*string]")
		return
	}

	// Make[*int] and Make[*string] — the returned *Holder type
	// is itself a pointer, doubling up on the rewrite.
	h1 := Make[*int](new(int))
	if h1 == nil || h1.V == nil {
		println("FAIL: Make[*int]")
		return
	}
	h2 := Make[*string](new(string))
	if h2 == nil || h2.V == nil {
		println("FAIL: Make[*string]")
		return
	}

	println("ok")
}
