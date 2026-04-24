// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Phase G.2.1: generic function used as a value of the concrete
// instantiation type. This is the case that forces option-A
// conditional rewrite: the closure/function value boundary
// demands matching ABI between the generic's instantiated body
// and the concrete func type at the capture site.
//
//   var f func() *int = GenericMake[int]
//   f()  // ABI must be extended (one outBuf) at the call site
//
// If the shape body uses stock ABI (option B) but the closure's
// concrete type is extended, register mismatch → garbage.
package main

//go:noinline
func Default[T any]() *T {
	var zero T
	return &zero
}

func main() {
	// Generic function expression → concrete func value.
	// At this boundary, the concrete type is `func() *int`
	// which is extended; the shape body (Default[shape.*uint8])
	// must dispatch through extended ABI.
	var fInt func() *int = Default[int]
	a := fInt()
	if a == nil || *a != 0 {
		println("FAIL: Default[int] via func value")
		return
	}

	// Same shape, different concrete type.
	var fStr func() *string = Default[string]
	b := fStr()
	if b == nil || *b != "" {
		println("FAIL: Default[string] via func value")
		return
	}

	// Pointer-of-pointer: Default[*int] returns **int.
	var fPtr func() **int = Default[*int]
	c := fPtr()
	if c == nil {
		println("FAIL: Default[*int] via func value")
		return
	}

	// Pass generic as arg expecting concrete func type.
	r := apply(Default[int])
	if r == nil || *r != 0 {
		println("FAIL: Default[int] passed as arg")
		return
	}

	println("ok")
}

//go:noinline
func apply(f func() *int) *int { return f() }
