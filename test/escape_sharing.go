// errorcheck -0 -m -l

//go:build goexperiment.sharing

// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Test the escape analysis of the goroutine sharing experiment (gd,
// doc/gd/sharing.md) where it differs from stock escape analysis (see
// escape2.go's foo98, escape_param.go's caller2a and caller4a).

package escape

var sink interface{}

// What is loaded from a map is reached from it, for which goroutines
// reach it.
func foo98(m map[int]*int) *int { // ERROR "leaking param: m to result ~r0 level=1$"
	return m[0]
}

func param2(p1 *int, p2 **int) { // ERROR "leaking param: p1$" "p2 does not escape$"
	*p2 = p1
}

// Stored through the other argument only, which stays on the stack.
func caller2a() {
	i := 0
	var p *int
	param2(&i, &p)
	_ = p
}

func caller2b() {
	i := 0 // ERROR "moved to heap: i$"
	var p *int
	param2(&i, &p)
	sink = p
}

type Pair struct {
	p1 *int
	p2 *int
}

func (p *Pair) param4(i *int) { // ERROR "p does not escape$" "leaking param: i$"
	p.p1 = i
}

// Stored through the receiver only, which stays on the stack.
func caller4a() {
	i := 0
	p := Pair{}
	p.param4(&i)
	_ = p
}
