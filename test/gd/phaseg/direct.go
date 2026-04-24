// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Phase G.2.1: plain func() *T direct call.
// Baseline case — with extended ABI, caller passes one outBuf nil,
// callee ignores it, returns a heap pointer.
package main
import "fmt"

type T struct{ X int }

//go:noinline
func NewT(x int) *T { return &T{X: x} }

func main() {
	a := NewT(42)
	b := NewT(42)
	if a == b {
		// Different allocations — pointer identity must differ.
		fmt.Println("FAIL: same pointer for different NewT calls")
		return
	}
	if a.X != 42 || b.X != 42 {
		fmt.Println("FAIL: wrong field values")
		return
	}
	fmt.Println("ok")
}
