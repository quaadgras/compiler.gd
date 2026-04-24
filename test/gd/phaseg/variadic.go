// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Phase G.2.1: variadic sig `(prefix string, xs ...int) *T`.
// outBufs must be inserted BEFORE the variadic slice, so caller
// splices outBuf nils between the mandatory prefix arg and the
// variadic pack. Covers:
//   - zero variadic args
//   - many variadic args
//   - slice-form (foo(prefix, s...))
package main
import "fmt"

type T struct {
	Name string
	Sum  int
}

//go:noinline
func NewT(prefix string, xs ...int) *T {
	total := 0
	for _, x := range xs {
		total += x
	}
	return &T{Name: prefix, Sum: total}
}

func main() {
	// Zero variadic args.
	a := NewT("zero")
	if a == nil || a.Name != "zero" || a.Sum != 0 {
		fmt.Println("FAIL: zero variadic")
		return
	}

	// Several variadic args.
	b := NewT("sum", 1, 2, 3, 4)
	if b == nil || b.Name != "sum" || b.Sum != 10 {
		fmt.Println("FAIL: multi variadic")
		return
	}

	// Slice-form.
	xs := []int{5, 6, 7}
	c := NewT("slice", xs...)
	if c == nil || c.Name != "slice" || c.Sum != 18 {
		fmt.Println("FAIL: slice-form variadic")
		return
	}

	fmt.Println("ok")
}
