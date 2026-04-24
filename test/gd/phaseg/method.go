// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Phase G.2.1: method returning *U on a value receiver.
// Exercises:
//   - Direct method call: t.Method()
//   - Method value materialisation: f := t.Method; f()
//
// Both must route through the extended-ABI body without ABI drift
// at the method-value wrapper boundary.
package main
import "fmt"

type T struct{ X int }

type U struct{ V int }

//go:noinline
func (t *T) Double() *U { return &U{V: t.X * 2} }

func main() {
	t := &T{X: 21}

	// Direct method call.
	u1 := t.Double()
	if u1 == nil || u1.V != 42 {
		fmt.Println("FAIL: direct method call")
		return
	}

	// Method value — the wrapper translates the call frame.
	f := t.Double
	u2 := f()
	if u2 == nil || u2.V != 42 {
		fmt.Println("FAIL: method value call")
		return
	}

	// Method expression — T.Method as a func(t *T) *U.
	g := (*T).Double
	u3 := g(t)
	if u3 == nil || u3.V != 42 {
		fmt.Println("FAIL: method expression call")
		return
	}

	fmt.Println("ok")
}
