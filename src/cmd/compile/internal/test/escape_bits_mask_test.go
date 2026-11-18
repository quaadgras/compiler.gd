// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"internal/testenv"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unsafe"
)

// Escape-bits soundness of the mask each kind of function value
// carries (doc/gd/escape-bits.md): the caller keeps x on its stack
// and passes &x; where the callee keeps the pointer, or hands it back
// for the caller to keep, the mask must say so, whichever way the
// function value was made. A zero mask says that nothing escapes.

type ebmT struct {
	a   int
	pad [3]int
}

var (
	ebmSink  *ebmT
	ebmISink *int
	ebmASink any
)

// Bare function values, whose mask is in the function's symbol: the
// argument leaks to a result only, not to the heap.

func ebmRet(p *ebmT) *ebmT { return p }

var (
	ebmRetTop                           = ebmRet
	ebmRetLit                           = func(p *ebmT) *ebmT { return p }
	ebmRetAny                           = func(p *ebmT) any { return p }
	ebmRetField                         = func(p *ebmT) *int { return &p.pad[1] }
	ebmRetStruct                        = func(p *ebmT) struct{ p *ebmT } { return struct{ p *ebmT }{p} }
	ebmRetSixth                         = func(p *ebmT) (a, b, c, d, e, f *ebmT) { return nil, nil, nil, nil, nil, p }
	ebmRetMethod func(*ebmT) *ebmT      = (*ebmT).self
	ebmKeepIface func(ebmKeeper, *ebmT) = ebmKeeper.Keep
)

func (t *ebmT) self() *ebmT { return t }

// An interface whose method's parameter has no name: the wrappers
// made from its signature (ebmKeeper.Keep above, k.Keep below) pass on
// a parameter that has none either.
type ebmKeeper interface{ Keep(*ebmT) }

type ebmRetainer struct{}

func (ebmRetainer) Keep(p *ebmT) { ebmSink = p }

var ebmK ebmKeeper = ebmRetainer{}

// A method that only forwards to an interface in its receiver has a
// mask that is computed from the receiver at an interface call; its
// method expression is called with no such receiver.
type ebmFwd struct {
	n     int
	inner ebmKeeper
}

func (f *ebmFwd) Keep(p *ebmT) { f.inner.Keep(p) }

type ebmEmbed struct{ ebmKeeper }

var (
	ebmFwdExpr   func(*ebmFwd, *ebmT)  = (*ebmFwd).Keep
	ebmEmbedExpr func(ebmEmbed, *ebmT) = ebmEmbed.Keep
)

//go:noinline
func ebmSmash(n int) int {
	var buf [64]int
	for i := range buf {
		buf[i] = -1
	}
	if n == 0 {
		return buf[3]
	}
	return ebmSmash(n-1) + buf[n%64]
}

func TestEscapeBitsMaskOfFuncValues(t *testing.T) {
	for _, c := range []struct {
		name string
		f    func() // leaves a pointer to its local, whose a is 42, in a sink
		got  func() int
	}{
		{"top-level function returning its argument", func() { var x ebmT; x.a = 42; ebmSink = ebmRetTop(&x) }, func() int { return ebmSink.a }},
		{"func literal returning its argument", func() { var x ebmT; x.a = 42; ebmSink = ebmRetLit(&x) }, func() int { return ebmSink.a }},
		{"in an interface", func() { var x ebmT; x.a = 42; ebmASink = ebmRetAny(&x) }, func() int { return ebmASink.(*ebmT).a }},
		{"a field's address", func() { var x ebmT; x.pad[1] = 42; ebmISink = ebmRetField(&x) }, func() int { return *ebmISink }},
		{"in a struct", func() { var x ebmT; x.a = 42; ebmSink = ebmRetStruct(&x).p }, func() int { return ebmSink.a }},
		{"as a sixth result", func() { var x ebmT; x.a = 42; _, _, _, _, _, ebmSink = ebmRetSixth(&x) }, func() int { return ebmSink.a }},
		{"method expression returning its receiver", func() { var x ebmT; x.a = 42; ebmSink = ebmRetMethod(&x) }, func() int { return ebmSink.a }},
		{"interface method expression", func() { var x ebmT; x.a = 42; ebmKeepIface(ebmK, &x) }, func() int { return ebmSink.a }},
		{"interface method value", func() { f := ebmK.Keep; ebmCall(f) }, func() int { return ebmSink.a }},
		{"method expression of a forwarder", func() { var x ebmT; x.a = 42; ebmFwdExpr(&ebmFwd{inner: ebmK}, &x) }, func() int { return ebmSink.a }},
		{"method expression of an embedded interface's method", func() { var x ebmT; x.a = 42; ebmEmbedExpr(ebmEmbed{ebmK}, &x) }, func() int { return ebmSink.a }},
	} {
		t.Run(c.name, func(t *testing.T) {
			ebmSink, ebmISink, ebmASink = nil, nil, nil
			ebmRun(c.f)
			ebmSmash(20)
			runtime.GC()
			if got := c.got(); got != 42 {
				t.Errorf("the pointer the callee handed out reads %d, want 42", got)
			}
		})
	}
}

//go:noinline
func ebmRun(f func()) { f() }

//go:noinline
func ebmCall(f func(*ebmT)) {
	var x ebmT
	x.a = 42
	f(&x)
}

// The mask has bits for 63 arguments. A local passed after those
// isn't left on the stack.

var ebmMany func(a0, a1, a2, a3, a4, a5, a6, a7, a8, a9,
	b0, b1, b2, b3, b4, b5, b6, b7, b8, b9,
	c0, c1, c2, c3, c4, c5, c6, c7, c8, c9,
	d0, d1, d2, d3, d4, d5, d6, d7, d8, d9,
	e0, e1, e2, e3, e4, e5, e6, e7, e8, e9,
	f0, f1, f2, f3, f4, f5, f6, f7, f8, f9,
	g0, g1, g2, g3, g4, g5 *ebmT)

var ebmManySink [5]*ebmT

//go:noinline
func ebmCallMany() {
	var first, n61, n62, n63, last, rest ebmT
	first.a, n61.a, n62.a, n63.a, last.a = 1, 2, 3, 4, 5
	r := &rest
	ebmMany(&first, r, r, r, r, r, r, r, r, r,
		r, r, r, r, r, r, r, r, r, r,
		r, r, r, r, r, r, r, r, r, r,
		r, r, r, r, r, r, r, r, r, r,
		r, r, r, r, r, r, r, r, r, r,
		r, r, r, r, r, r, r, r, r, r,
		r, &n61, &n62, &n63, r, &last)
}

func TestEscapeBitsMaskManyArgs(t *testing.T) {
	ebmMany = func(a0, a1, a2, a3, a4, a5, a6, a7, a8, a9,
		b0, b1, b2, b3, b4, b5, b6, b7, b8, b9,
		c0, c1, c2, c3, c4, c5, c6, c7, c8, c9,
		d0, d1, d2, d3, d4, d5, d6, d7, d8, d9,
		e0, e1, e2, e3, e4, e5, e6, e7, e8, e9,
		f0, f1, f2, f3, f4, f5, f6, f7, f8, f9,
		g0, g1, g2, g3, g4, g5 *ebmT) {
		ebmManySink = [5]*ebmT{a0, g1, g2, g3, g5}
	}
	ebmCallMany()
	ebmSmash(20)
	runtime.GC()
	for i, p := range ebmManySink {
		if p.a != i+1 {
			t.Errorf("retained argument %d reads %d, want %d", i, p.a, i+1)
		}
	}
}

// A variable that holds its own address without escape analysis
// knowing, as strings.Builder does for its copy check, still does
// once the call site has moved it to the heap.

type ebmSelf struct {
	addr *ebmSelf
	n    int
}

//go:nosplit
func ebmNoEscape(p unsafe.Pointer) unsafe.Pointer {
	x := uintptr(p)
	return unsafe.Pointer(x ^ 0)
}

//go:noinline
func (b *ebmSelf) add() {
	if b.addr == nil {
		b.addr = (*ebmSelf)(ebmNoEscape(unsafe.Pointer(b)))
	} else if b.addr != b {
		panic("ebmSelf was copied")
	}
	b.n++
}

var (
	ebmSelfSink *ebmSelf
	ebmSelfKeep = func(p *ebmSelf) { ebmSelfSink = p }
)

//go:noinline
func ebmUseSelf() int {
	var b ebmSelf
	b.add()
	ebmSelfKeep(&b)
	b.add()
	ebmSelfSink.add()
	return b.n
}

func TestEscapeBitsSelfPointerMoves(t *testing.T) {
	if got := ebmUseSelf(); got != 3 {
		t.Errorf("got %d, want 3", got)
	}
	ebmSmash(20)
	runtime.GC()
	if ebmSelfSink.addr != ebmSelfSink {
		t.Errorf("the retained variable's own address is %p, want %p", ebmSelfSink.addr, ebmSelfSink)
	}
}

// A method value made in a function inlined from another package uses
// that package's wrapper, which this one doesn't analyse.
func TestEscapeBitsMaskOfImportedMethodValue(t *testing.T) {
	testenv.MustHaveGoRun(t)
	dir := t.TempDir()
	write := func(name, src string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module ebm\n\ngo 1.24\n")
	write("b/b.go", `package b

type T struct {
	V   int
	Pad [3]int
}
type R struct{ Last *T }

var Kept *T

func (r *R) Keep(p *T) { Kept = p }

// Bind is inlined into its callers.
func Bind(r *R) func(*T) { return r.Keep }
`)
	write("main.go", `package main

import "ebm/b"

var f func(*b.T)

//go:noinline
func via() {
	var x b.T
	x.V = 42
	f(&x)
}

//go:noinline
func smash(n int) int {
	var buf [64]int
	for i := range buf {
		buf[i] = -1
	}
	if n == 0 {
		return buf[3]
	}
	return smash(n-1) + buf[n%64]
}

func main() {
	f = b.Bind(&b.R{})
	via()
	smash(20)
	println(b.Kept.V)
}
`)
	cmd := testenv.Command(t, testenv.GoToolPath(t), "run", ".")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "42" {
		t.Errorf("the retained pointer reads %s, want 42", got)
	}
}
