// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Test that reflect.TypeFor[T]().Methods() keeps the exported methods
// of the type T (which may be a type parameter), and not the methods
// of the other types that are converted to interfaces.

package main

import "reflect"

type T struct{}

func (T) M()  { println("T.M") }
func (T) m()  { println("T.m") }
func (*T) N() { println("(*T).N") } // not in the method set of T

type S struct{}

func (S) M() { println("S.M") }

type P struct{}

func (*P) M() { println("(*P).M") }

type G[A any] struct{}

func (G[A]) M() { println("G.M") }

type E struct{ n int }

func (E) M() { println("E.M") }

type B struct{ E }

//go:noinline
func count[A any]() (n int) {
	for range reflect.TypeFor[A]().Methods() {
		n++
	}
	return n
}

var sink any

func main() {
	sink = S{}
	sink = &T{}
	for m := range reflect.TypeFor[T]().Methods() {
		println(m.Name)
	}
	println(count[*P](), count[G[int]](), count[B]())
}
