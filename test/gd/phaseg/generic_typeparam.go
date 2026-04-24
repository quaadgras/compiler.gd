// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Phase G.2.1: generic function whose type set INCLUDES pointer
// types but doesn't guarantee them. Result type is the type
// parameter itself (T, not *T), so the concrete result can be
// pointer or non-pointer depending on instantiation.
//
// Under Phase G.2.1 conservative extension, the shape body
// compiles with extended ABI because T is a TypeParam.
// Concrete instantiations:
//   - Identity[int]    → func(int) int           (STOCK ABI)
//   - Identity[*Foo]   → func(*Foo) *Foo         (EXTENDED ABI)
//
// Converting Identity[int] to func(int) int creates a function
// value whose concrete type is stock. If the value points directly
// at the shape body (extended), calling through the value causes
// register mismatch. The fork must either emit a wrapper at
// materialisation (stock→extended trampoline) OR keep shape
// bodies stock-compatible for non-pointer T.
//
// This is the motivating case the user flagged as "hard" — requires
// per-shape flag handling because a single shape serves both
// pointer and non-pointer instantiations of T.
package main

import "fmt"

//go:noinline
func Identity[T any](x T) T { return x }

//go:noinline
func Pair[T, U any](a T, b U) (T, U) { return a, b }

func main() {
	// Non-pointer concrete instantiation via function value.
	// Shape body (extended) vs concrete type func(int) int (stock).
	var fInt func(int) int = Identity[int]
	if got := fInt(42); got != 42 {
		fmt.Println("FAIL: Identity[int] via func value, got", got)
		return
	}

	// Pointer concrete instantiation via function value — should
	// match (both extended).
	x := 7
	var fPtr func(*int) *int = Identity[*int]
	if got := fPtr(&x); got == nil || *got != 7 {
		fmt.Println("FAIL: Identity[*int] via func value")
		return
	}

	// String (reference type, not a regular pointer).
	var fStr func(string) string = Identity[string]
	if got := fStr("hello"); got != "hello" {
		fmt.Println("FAIL: Identity[string] via func value, got", got)
		return
	}

	// Slice — also a reference shape but not a pointer type.
	var fSlice func([]int) []int = Identity[[]int]
	if got := fSlice([]int{1, 2, 3}); len(got) != 3 || got[0] != 1 {
		fmt.Println("FAIL: Identity[[]int] via func value")
		return
	}

	// Multi-result generic: Pair returns two type parameters.
	var fp func(int, int) (int, int) = Pair[int, int]
	a, b := fp(10, 20)
	if a != 10 || b != 20 {
		fmt.Println("FAIL: Pair[int, int] via func value")
		return
	}

	// Mixed: one of the results is a pointer type.
	y := 99
	var fpMixed func(int, *int) (int, *int) = Pair[int, *int]
	ra, rb := fpMixed(5, &y)
	if ra != 5 || rb == nil || *rb != 99 {
		fmt.Println("FAIL: Pair[int, *int] via func value")
		return
	}

	// Direct call (not via function value) — compiler knows the
	// shape body ABI at the call site and should emit matching
	// args. This should pass regardless of the value-conversion
	// case above.
	if Identity[int](7) != 7 {
		fmt.Println("FAIL: Identity[int] direct")
		return
	}
	if *Identity[*int](&x) != 7 {
		fmt.Println("FAIL: Identity[*int] direct")
		return
	}

	fmt.Println("ok")
}
