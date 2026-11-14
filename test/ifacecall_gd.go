// run

// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// gd fat-interface method calls pass the iface data word as the
// receiver and the inline payload in two trailing float registers;
// inline and spread types are called through an adapter in the itab
// (see cmd/compile/internal/abi.IfaceExtFits). Check every receiver
// representation through compiled calls, runtime-built itabs, method
// values, reflect calls, and a signature whose payload does not fit in
// registers.

package main

import (
	"fmt"
	"reflect"
)

type Getter interface {
	Get(x int, f float64) (int, float64)
}

type Small struct{ a, b int32 } // inline

func (s Small) Get(x int, f float64) (int, float64) { return int(s.a+s.b) + x, f + float64(s.b) }

type Pair struct{ a, b int64 } // inline, 16 B

func (p Pair) Get(x int, f float64) (int, float64) { return int(p.a*p.b) + x, f * 2 }

type F float64 // inline, float bits through the payload

func (v F) Get(x int, f float64) (int, float64) { return int(v*2) + x, f + float64(v) }

type Name string // spread

func (n Name) Get(x int, f float64) (int, float64) { return len(n) + x, f }

type Ints []int // spread

func (s Ints) Get(x int, f float64) (int, float64) { return s[0] + len(s) + x, f + 1 }

type Node struct{ v int } // boxed, pointer receiver

func (n *Node) Get(x int, f float64) (int, float64) { return n.v + x, f - 1 }

type Big struct{ a, b, c int64 } // boxed by value

func (b Big) Get(x int, f float64) (int, float64) { return int(b.a+b.b+b.c) + x, f }

// Many has more float arguments than registers left for the payload, so
// its calls keep the staged pointer convention.
type Many interface {
	M(a, b, c, d, e, f, g, h, i, j, k, l, m, n float64) float64
}

func (p Pair) M(a, b, c, d, e, f, g, h, i, j, k, l, m, n float64) float64 {
	return a + n + float64(p.a)
}

func (n Name) M(a, b, c, d, e, f, g, h, i, j, k, l, m, n2 float64) float64 {
	return a + n2 + float64(len(n))
}

type want struct {
	i int
	f float64
}

func check(what string, v Getter, gotI int, gotF float64, w want) {
	if gotI != w.i || gotF != w.f {
		panic(fmt.Sprintf("%s %T: got %d, %v; want %d, %v", what, v, gotI, gotF, w.i, w.f))
	}
}

//go:noinline
func call(g Getter) (int, float64) { return g.Get(10, 1.5) }

func main() {
	vals := []Getter{Small{1, 2}, Pair{3, 4}, F(2.25), Name("hello"), Ints{7, 8, 9}, &Node{5}, Big{1, 2, 3}}
	wants := []want{{13, 3.5}, {22, 3}, {14, 3.75}, {15, 1.5}, {20, 2.5}, {15, 0.5}, {16, 1.5}}

	for i, v := range vals {
		w := wants[i]

		gi, gf := call(v)
		check("call", v, gi, gf, w)

		// Itab built at run time by an assertion from any.
		var e any = v
		gi, gf = e.(Getter).Get(10, 1.5)
		check("dynamic itab", v, gi, gf, w)

		// Method value bound to the interface.
		mv := v.Get
		gi, gf = mv(10, 1.5)
		check("method value", v, gi, gf, w)

		// Method expression on the interface type.
		gi, gf = Getter.Get(v, 10, 1.5)
		check("method expr", v, gi, gf, w)

		// reflect: concrete receiver, interface receiver, method value
		// and Type.Method.Func.
		args := func() []reflect.Value { return []reflect.Value{reflect.ValueOf(10), reflect.ValueOf(1.5)} }
		out := reflect.ValueOf(v).MethodByName("Get").Call(args())
		check("reflect concrete", v, int(out[0].Int()), out[1].Float(), w)
		iv := reflect.ValueOf(&vals[i]).Elem()
		out = iv.Method(0).Call(args())
		check("reflect iface", v, int(out[0].Int()), out[1].Float(), w)
		rf := iv.Method(0).Interface().(func(int, float64) (int, float64))
		gi, gf = rf(10, 1.5)
		check("reflect method value", v, gi, gf, w)
		m, _ := reflect.TypeOf(v).MethodByName("Get")
		out = m.Func.Call(append([]reflect.Value{reflect.ValueOf(v)}, args()...))
		check("reflect Type.Method", v, int(out[0].Int()), out[1].Float(), w)
	}

	for _, m := range []Many{Pair{5, 0}, Name("abc")} {
		w := 15.0 // a + n, plus the receiver's term
		switch v := m.(type) {
		case Pair:
			w += float64(v.a)
		case Name:
			w += float64(len(v))
		}
		if got := m.M(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14); got != w {
			panic(fmt.Sprintf("Many %T: got %v, want %v", m, got, w))
		}
		out := reflect.ValueOf(&m).Elem().Method(0).Call([]reflect.Value{
			reflect.ValueOf(1.0), reflect.ValueOf(2.0), reflect.ValueOf(3.0), reflect.ValueOf(4.0),
			reflect.ValueOf(5.0), reflect.ValueOf(6.0), reflect.ValueOf(7.0), reflect.ValueOf(8.0),
			reflect.ValueOf(9.0), reflect.ValueOf(10.0), reflect.ValueOf(11.0), reflect.ValueOf(12.0),
			reflect.ValueOf(13.0), reflect.ValueOf(14.0)})
		if got := out[0].Float(); got != w {
			panic(fmt.Sprintf("reflect Many %T: got %v, want %v", m, got, w))
		}
	}

	// A reflect-built struct embedding an inline type: inline when small,
	// boxed (through the embedded type's pointer method, which needs *Small
	// in the binary) when not.
	var _ Getter = &Small{}
	for _, extra := range []reflect.Type{nil, reflect.TypeOf([4]int64{})} {
		fields := []reflect.StructField{{Name: "Small", Anonymous: true, Type: reflect.TypeOf(Small{})}}
		if extra != nil {
			fields = append(fields, reflect.StructField{Name: "Pad", Type: extra})
		}
		sv := reflect.New(reflect.StructOf(fields)).Elem()
		sv.Field(0).Set(reflect.ValueOf(Small{1, 2}))
		gi, gf := sv.Interface().(Getter).Get(10, 1.5)
		check("StructOf", Small{}, gi, gf, want{13, 3.5})
	}
}
