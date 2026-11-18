// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"runtime"
	"testing"
	"unsafe"
)

// The tests in this file stress the soundness of the escape-bits
// re-homing (doc/gd/escape-bits.md): when a dynamic callee retains
// &x, the call site copies x to the heap and points x's box at the
// copy. That is only sound when no other pointer to x's old storage
// exists. Each test takes such a pointer in a different way and checks
// that the program behaves as under stock Go: aliases taken before
// the call must observe the callee's object, and a pointer that
// reaches the callee without going through the wrap must not point
// into a dead frame.

type ebaT struct{ a, b int }

var (
	ebaSink  *ebaT
	ebaUSink unsafe.Pointer

	// Opaque callees: set in init so call sites can't see through them.
	ebaRetain  func(*ebaT)
	ebaURetain func(unsafe.Pointer)
)

func init() {
	ebaRetain = func(p *ebaT) { ebaSink = p }
	ebaURetain = func(p unsafe.Pointer) { ebaUSink = p }
}

type ebaKeeper interface{ Keep(*ebaT) }

type ebaRetainer struct{}

func (ebaRetainer) Keep(p *ebaT) { ebaSink = p }

var ebaIface ebaKeeper = ebaRetainer{}

func (t *ebaT) keepArg(p *ebaT) { ebaSink = p }
func (t *ebaT) self() *ebaT     { return t }

// ebaSmash overwrites a good stretch of stack below the caller with
// junk, so a pointer into a returned frame reads garbage.
//
//go:noinline
func ebaSmash(n int) int {
	var buf [64]int
	for i := range buf {
		buf[i] = -1
	}
	if n == 0 {
		return buf[3]
	}
	return ebaSmash(n-1) + buf[n%64]
}

func ebaCheckSink(t *testing.T, want int) {
	t.Helper()
	ebaSmash(10)
	runtime.GC()
	if ebaSink.a != want {
		t.Fatalf("retained pointer reads a=%d, want %d", ebaSink.a, want)
	}
}

func TestEscapeBitsAliasBeforeCall(t *testing.T) {
	var x ebaT
	q := &x
	ebaRetain(&x)
	q.a = 7
	if ebaSink != q || ebaSink.a != 7 || x.a != 7 {
		t.Fatalf("alias diverged: sink=%p q=%p sink.a=%d x.a=%d", ebaSink, q, ebaSink.a, x.a)
	}
}

func TestEscapeBitsInteriorPointerBeforeCall(t *testing.T) {
	var x ebaT
	q := &x.b
	ebaRetain(&x)
	*q = 9
	if ebaSink.b != 9 || x.b != 9 {
		t.Fatalf("interior pointer diverged: sink.b=%d x.b=%d", ebaSink.b, x.b)
	}
}

type ebaHolder struct{ p *ebaT }

func TestEscapeBitsPointerInStackStruct(t *testing.T) {
	var x ebaT
	h := ebaHolder{p: &x}
	ebaRetain(&x)
	h.p.a = 11
	if ebaSink.a != 11 || x.a != 11 {
		t.Fatalf("stored pointer diverged: sink.a=%d x.a=%d", ebaSink.a, x.a)
	}
}

func TestEscapeBitsAddressIdentity(t *testing.T) {
	var x ebaT
	p1 := &x
	ebaRetain(&x)
	p2 := &x
	if p1 != p2 || ebaSink != p2 {
		t.Fatalf("&x changed across the call: before=%p after=%p sink=%p", p1, p2, ebaSink)
	}
}

func TestEscapeBitsIfaceAliasBeforeCall(t *testing.T) {
	var x ebaT
	q := &x
	ebaIface.Keep(&x)
	q.a = 13
	if ebaSink.a != 13 || x.a != 13 {
		t.Fatalf("iface alias diverged: sink.a=%d x.a=%d", ebaSink.a, x.a)
	}
}

func TestEscapeBitsAliasAcrossStackGrowth(t *testing.T) {
	var x ebaT
	q := &x
	ebaRetain(&x)
	ebaSmash(2000)
	runtime.GC()
	q.a = 17
	if ebaSink.a != 17 || x.a != 17 {
		t.Fatalf("alias diverged after stack growth: sink.a=%d x.a=%d", ebaSink.a, x.a)
	}
}

func TestEscapeBitsMethodValueAlias(t *testing.T) {
	var r ebaT
	f := r.keepArg
	var x ebaT
	q := &x
	f(&x)
	q.a = 25
	if ebaSink.a != 25 {
		t.Fatalf("method value alias diverged: sink.a=%d", ebaSink.a)
	}
}

func TestEscapeBitsPointerMethodAlias(t *testing.T) {
	var x ebaT
	q := x.self() // implicit &x
	ebaRetain(&x)
	q.a = 29
	if ebaSink.a != 29 || x.a != 29 {
		t.Fatalf("pointer-method alias diverged: sink.a=%d x.a=%d", ebaSink.a, x.a)
	}
}

func TestEscapeBitsArraySliceAlias(t *testing.T) {
	var arr [2]int
	s := arr[:]
	retain := func(p *[2]int) { ebaArr = p }
	if testing.Short() && len(s) == 3 {
		retain = func(*[2]int) {}
	}
	retain(&arr)
	s[0] = 31
	if ebaArr[0] != 31 || arr[0] != 31 {
		t.Fatalf("slice of array diverged: retained=%d arr=%d", ebaArr[0], arr[0])
	}
}

var ebaArr *[2]int

func TestEscapeBitsByRefCapture(t *testing.T) {
	var x ebaT
	set := func(v int) { x.a = v }
	ebaRetain(&x)
	set(37)
	if ebaSink.a != 37 {
		t.Fatalf("closure capture diverged: sink.a=%d x.a=%d", ebaSink.a, x.a)
	}
}

// The remaining tests pass the pointer to the dynamic callee without a
// direct &x argument at the call, which the wrap can't see; x must be
// heap-allocated up front.

//go:noinline
func ebaViaLocal() {
	var x ebaT
	x.a = 42
	p := &x
	ebaRetain(p)
}

func TestEscapeBitsPointerViaLocal(t *testing.T) {
	ebaViaLocal()
	ebaCheckSink(t, 42)
}

//go:noinline
func ebaViaUnsafe() {
	var x ebaT
	x.a = 42
	ebaURetain(unsafe.Pointer(&x))
}

func TestEscapeBitsPointerViaUnsafe(t *testing.T) {
	ebaViaUnsafe()
	ebaSmash(10)
	runtime.GC()
	if got := (*ebaT)(ebaUSink).a; got != 42 {
		t.Fatalf("retained unsafe.Pointer reads a=%d, want 42", got)
	}
}

func ebaForward(f func(*ebaT), p *ebaT) { f(p) }

//go:noinline
func ebaViaInlinedForwarder() {
	var x ebaT
	x.a = 42
	ebaForward(ebaRetain, &x)
}

func TestEscapeBitsPointerViaInlinedForwarder(t *testing.T) {
	ebaViaInlinedForwarder()
	ebaCheckSink(t, 42)
}

//go:noinline
func ebaViaClosure() {
	var x ebaT
	x.a = 42
	func() { ebaRetain(&x) }()
}

func TestEscapeBitsPointerViaClosure(t *testing.T) {
	ebaViaClosure()
	ebaCheckSink(t, 42)
}

//go:noinline
func ebaViaDefer() {
	var x ebaT
	x.a = 42
	defer ebaRetain(&x)
}

func TestEscapeBitsPointerViaDefer(t *testing.T) {
	ebaViaDefer()
	ebaCheckSink(t, 42)
}

func TestEscapeBitsLoopRetainsEachIteration(t *testing.T) {
	var all []*ebaT
	keep := func(p *ebaT) { all = append(all, p) }
	for i := range 3 {
		var x ebaT
		x.a = i
		keep(&x)
	}
	ebaSmash(10)
	runtime.GC()
	if len(all) != 3 || all[0] == all[1] || all[0].a != 0 || all[1].a != 1 || all[2].a != 2 {
		t.Fatalf("loop-scoped vars not distinct: %v", all)
	}
}
