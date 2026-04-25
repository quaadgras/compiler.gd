// run

// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Phase G first-cut callee body rewrite: `return new(T)` and
// `return &T{...}` both route through runtime.maybeInPlace so
// the caller's stack buffer (when supplied at the call site) is
// reused. With the call site still emitting nil outBuf in this
// cut, observable behaviour matches stock — the tests below
// just confirm correctness (returned pointer is independent,
// fields are set, repeated calls produce distinct objects when
// outBuf is nil).
package main

import "fmt"

type Payload struct {
	A, B, C, D int64
}

//go:noinline
func makeNew() *Payload {
	return new(Payload)
}

//go:noinline
func makeLit() *Payload {
	return &Payload{A: 1, B: 2, C: 3, D: 4}
}

//go:noinline
func makeLitZero() *Payload {
	return &Payload{}
}

func main() {
	a := makeNew()
	b := makeNew()
	if a == b {
		panic("makeNew returned same pointer twice")
	}
	if a.A != 0 || a.B != 0 || a.C != 0 || a.D != 0 {
		panic("makeNew returned non-zero payload")
	}

	c := makeLit()
	if c.A != 1 || c.B != 2 || c.C != 3 || c.D != 4 {
		panic("makeLit returned wrong fields")
	}
	d := makeLit()
	if c == d {
		panic("makeLit returned same pointer twice")
	}

	e := makeLitZero()
	if e.A != 0 || e.B != 0 || e.C != 0 || e.D != 0 {
		panic("makeLitZero returned non-zero payload")
	}
	fmt.Println("ok")
}
