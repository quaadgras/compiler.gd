// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Phase G.2.1: generic function returning *Box[T].
// Simplest generic case — direct call with a concrete type.
// Exercises the generic-instantiation path through
// ssagen / noder's shape machinery.
package main

type Box[T any] struct{ V T }

//go:noinline
func NewBox[T any](x T) *Box[T] { return &Box[T]{V: x} }

func main() {
	// Instantiated with int.
	a := NewBox[int](42)
	if a == nil || a.V != 42 {
		println("FAIL: generic[int] direct")
		return
	}

	// Instantiated with string.
	b := NewBox[string]("hi")
	if b == nil || b.V != "hi" {
		println("FAIL: generic[string] direct")
		return
	}

	// Pointer type argument — the Box is *Box[*int] returning
	// pointer-to-struct-of-pointer. Result type Box[*int] has
	// the extended rewrite baked in for *Box[*int].
	x := 7
	c := NewBox[*int](&x)
	if c == nil || c.V == nil || *c.V != 7 {
		println("FAIL: generic[*int] direct")
		return
	}

	println("ok")
}
