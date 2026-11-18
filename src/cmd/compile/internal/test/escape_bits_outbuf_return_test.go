// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"runtime"
	"testing"
	"unsafe"
)

// Phase G (doc/gd/escape-bits.md): escape analysis places an
// allocation in the buffer the caller passes for a pointer result when
// its address reaches that result and nothing else that outlives the
// call, and it is the only such allocation, or every one is the
// operand of a return statement (escape/phaseg.go). The first test
// checks that the shapes it covers use the buffer; the rest, that
// shapes it must not cover stay correct.

//go:noinline
func ebrFillLater(a int) *ebgT { p := &ebgT{}; p.a = a; return p }

//go:noinline
func ebrNamedLocal(a int) *ebgT {
	var t ebgT
	t.a = a
	return &t
}

//go:noinline
func ebrSetA(p *ebgT, a int) { p.a = a }

//go:noinline
func ebrNewThenInit(a int) *ebgT { p := new(ebgT); ebrSetA(p, a); return p }

//go:noinline
func ebrMaybeNil(a int) *ebgT {
	if a < 0 {
		return nil
	}
	p := &ebgT{a: a}
	p.a *= 2
	return p
}

//go:noinline
func ebrTwoTerminal(a int) *ebgT {
	if a > 10 {
		return &ebgT{a: 1}
	}
	return &ebgT{a: a}
}

//go:noinline
func ebrWrap(a int) *ebgT {
	p := ebgMk(a)
	p.a++
	return p
}

//go:noinline
func ebrReturnInLoop(a int) *ebgT {
	for i := 0; i < 10; i++ {
		p := &ebgT{a: i}
		if i == a {
			return p
		}
	}
	return nil
}

func TestEscapeBitsOutBufReturnShapes(t *testing.T) {
	for _, c := range []struct {
		name string
		f    func(int) *ebgT
		in   int
		want int
	}{
		{"filled after allocation", ebrFillLater, 3, 3},
		{"named local", ebrNamedLocal, 4, 4},
		{"new, then a call", ebrNewThenInit, 5, 5},
		{"nil on another path", ebrMaybeNil, 6, 12},
		{"two return statements", ebrTwoTerminal, 7, 7},
		{"a call's result, changed", ebrWrap, 8, 9},
		{"returned from a loop", ebrReturnInLoop, 9, 9},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, in := c.f, c.in
			if p := f(in); p.a != c.want {
				t.Fatalf("got a=%d, want %d", p.a, c.want)
			}
			if n := testing.AllocsPerRun(100, func() { p := f(in); ebgSink += p.a }); n != 0 {
				t.Errorf("%v allocs, want 0", n)
			}
		})
	}
}

//go:noinline
func ebrPickCalls(x, y int) *ebgT {
	a := ebgMk(x)
	b := ebgMk(y)
	if a.a > b.a {
		return a
	}
	return b
}

//go:noinline
func ebrPickAllocs(x, y int) *ebgT {
	a := &ebgT{a: x}
	b := &ebgT{a: y}
	if a.a > b.a {
		return a
	}
	return b
}

// Both used to be built in the caller's one buffer (chain-fold).
func TestEscapeBitsOutBufTwoCandidates(t *testing.T) {
	for _, f := range []func(int, int) *ebgT{ebrPickCalls, ebrPickAllocs} {
		if p := f(5, 3); p.a != 5 {
			t.Errorf("got a=%d, want 5", p.a)
		}
		if p := f(3, 5); p.a != 5 {
			t.Errorf("got a=%d, want 5", p.a)
		}
	}
}

//go:noinline
func ebrNestedCall(a int) *ebgT { return &ebgT{a: a + 1, next: ebgMk(a)} }

//go:noinline
func ebrNestedLocal(a int) *ebgT {
	x := &ebgT{a: a}
	r := &ebgT{a: a + 1}
	r.next = x
	return r
}

//go:noinline
func ebrKeptAcross(n int) *ebgT {
	var first, p *ebgT
	for i := 1; i <= n; i++ {
		p = &ebgT{a: i}
		if first == nil {
			first = p
		}
	}
	p.next = first
	return p
}

func TestEscapeBitsOutBufNested(t *testing.T) {
	for _, c := range []struct {
		name       string
		f          func(int) *ebgT
		in         int
		want, next int
	}{
		{"call in a literal", ebrNestedCall, 1, 2, 1},
		{"local stored in another", ebrNestedLocal, 1, 2, 1},
		{"first of a loop's", ebrKeptAcross, 3, 3, 1},
	} {
		p := c.f(c.in)
		ebaSmash(10)
		runtime.GC()
		if p.a != c.want || p.next == nil || p.next == p || p.next.a != c.next {
			t.Errorf("%s: got %+v -> %+v", c.name, *p, p.next)
		}
	}
}

//go:noinline
func ebrKeepGlobal(a int) *ebgT {
	p := &ebgT{a: a}
	ebgGlobal = p
	return p
}

func TestEscapeBitsOutBufAlsoGlobal(t *testing.T) {
	func() { p := ebrKeepGlobal(9); ebgSink += p.a }()
	ebaSmash(10)
	runtime.GC()
	if ebgGlobal.a != 9 {
		t.Fatalf("global reads a=%d, want 9", ebgGlobal.a)
	}
}

//go:noinline
func ebrDeferred(a int) (r *ebgT) {
	defer func() { r.a += 100 }()
	r = &ebgT{a: a}
	return
}

//go:noinline
func ebrOrdinals(a int) (int, *ebgT, *ebgT) {
	x := ebgMk(a)
	y := &ebgT{a: a + 1}
	return 7, x, y
}

//go:noinline
func ebrLoopVar(a int) *ebgT {
	for x := (ebgT{}); x.a < 10; x.a++ {
		if x.a == a {
			return &x
		}
	}
	return nil
}

type ebrInner struct {
	t   ebgT
	pad int
}

//go:noinline
func ebrUnsafe(a int) *ebgT {
	var v ebrInner
	v.t.a = a
	v.pad = 3
	return (*ebgT)(unsafe.Pointer(&v))
}

//go:noinline
func ebrSelf(a int) *ebgT {
	p := &ebgT{a: a}
	p.next = p
	return p
}

//go:noinline
func ebrBoth(a int) (*ebgT, *ebgT) {
	p := &ebgT{a: a}
	return p, p
}

func TestEscapeBitsOutBufOtherShapes(t *testing.T) {
	if p := ebrDeferred(1); p.a != 101 {
		t.Errorf("deferred: a=%d, want 101", p.a)
	}
	if i, x, y := ebrOrdinals(1); i != 7 || x == y || x.a != 1 || y.a != 2 {
		t.Errorf("results after a non-pointer one: %d %+v %+v", i, *x, *y)
	}
	if p := ebrLoopVar(4); p == nil || p.a != 4 {
		t.Errorf("loop variable: %v", p)
	}
	if p := ebrUnsafe(13); p.a != 13 {
		t.Errorf("unsafe conversion: a=%d, want 13", p.a)
	}
	if p := ebrSelf(17); p.next != p || p.a != 17 {
		t.Errorf("self-referencing: %p %+v", p, *p)
	}
	if x, y := ebrBoth(18); x != y || x.a != 18 {
		t.Errorf("same pointer twice: %p %p", x, y)
	}
}

// The caller passes a buffer for any call whose result doesn't outlive
// its frame, wherever the call is, also to callees that take pointers:
// an argument can only point into the buffer if an earlier result of
// the same call is still live, which makes the result escape.

//go:noinline
func ebrLink(p *ebgT) *ebgT { return &ebgT{a: p.a + 1, next: p} }

//go:noinline
func ebrReadAfter(p *ebgT) *ebgT {
	n := &ebgT{}
	n.a = p.a + 1
	return n
}

//go:noinline
func ebrUse(p *ebgT) int { return p.a }

func TestEscapeBitsOutBufCallerShapes(t *testing.T) {
	for _, c := range []struct {
		name string
		f    func() int
		want int
	}{
		{"field of a result", func() int { return ebgMk(3).a }, 3},
		{"result as an argument", func() int { return ebrUse(ebgMk(4)) }, 4},
		{"callee with a pointer input", func() int { v := ebgT{a: 1}; return ebgSucc(&v).a }, 2},
		{"input read after the result is made", func() int { v := ebgT{a: 1}; return ebrReadAfter(&v).a }, 2},
		{"nested calls", func() int { return ebgSucc(ebgSucc(ebgMk(1))).a }, 3},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := c.f(); got != c.want {
				t.Fatalf("got %d, want %d", got, c.want)
			}
			f := c.f
			if n := testing.AllocsPerRun(100, func() { ebgSink += f() }); n != 0 {
				t.Errorf("%v allocs, want 0", n)
			}
		})
	}
}

func TestEscapeBitsOutBufInputsFromSameCall(t *testing.T) {
	x := ebgMk(1)
	for range 5 {
		x = ebrReadAfter(x)
	}
	if x.a != 6 {
		t.Errorf("x = readAfter(x): a=%d, want 6", x.a)
	}
	l := ebgMk(0)
	for range 4 {
		l = ebrLink(l)
	}
	ebaSmash(10)
	runtime.GC()
	n := 0
	for c := l; c != nil && n < 10; c = c.next {
		if c.a != 4-n {
			t.Fatalf("node %d has a=%d, want %d", n, c.a, 4-n)
		}
		n++
	}
	if n != 5 {
		t.Fatalf("%d nodes, want 5", n)
	}
	c := ebgMk(0)
	for c = ebgSucc(c); c.a < 5; c = ebgSucc(c) {
	}
	if c.a != 5 {
		t.Errorf("loop header: a=%d, want 5", c.a)
	}
}

type ebrOp func() int

var ebrOpCache = map[int]ebrOp{}

// ebrOpFor stores &o in a map before returning it (encoding/gob's
// decIgnoreOpFor): a flow to the heap that escape analysis records
// without an edge, so o must not go in the caller's buffer.
//
//go:noinline
func ebrOpFor(id int, inProgress map[int]*ebrOp) *ebrOp {
	if p := inProgress[id]; p != nil {
		return p
	}
	o, ok := ebrOpCache[id]
	if !ok {
		inProgress[id] = &o
		o = func() int { return id }
	}
	return &o
}

func TestEscapeBitsOutBufAlsoInMap(t *testing.T) {
	in := map[int]*ebrOp{}
	func() { p := ebrOpFor(1, in); ebgSink += (*p)() }()
	ebaSmash(10)
	runtime.GC()
	if got := (*in[1])(); got != 1 {
		t.Fatalf("op kept in the map returns %d, want 1", got)
	}
}
