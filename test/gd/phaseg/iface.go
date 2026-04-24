// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Phase G.2.1: interface method dispatch returning *T.
// Interface methods dispatch through itab. Both the iface method
// declaration and the impl method must agree on extended ABI.
package main
import "fmt"

type T struct{ V int }

type Maker interface {
	Build() *T
}

type Factory struct{ Base int }

//go:noinline
func (f *Factory) Build() *T { return &T{V: f.Base + 1} }

type OtherFactory struct{ Base int }

//go:noinline
func (o OtherFactory) Build() *T { return &T{V: o.Base * 10} }

func main() {
	var m Maker = &Factory{Base: 5}
	a := m.Build()
	if a == nil || a.V != 6 {
		fmt.Println("FAIL: iface dispatch (ptr recv)")
		return
	}

	// Value receiver impl via iface pointer.
	m = OtherFactory{Base: 3}
	b := m.Build()
	if b == nil || b.V != 30 {
		fmt.Println("FAIL: iface dispatch (val recv)")
		return
	}

	// Store in a slice of interface.
	makers := []Maker{&Factory{Base: 1}, OtherFactory{Base: 2}}
	expected := []int{2, 20}
	for i, mk := range makers {
		r := mk.Build()
		if r == nil || r.V != expected[i] {
			fmt.Println("FAIL: iface slice dispatch", i)
			return
		}
	}

	fmt.Println("ok")
}
