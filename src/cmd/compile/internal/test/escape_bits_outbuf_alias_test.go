// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"runtime"
	"testing"
)

// Soundness of Phase G's return-value outBuf (doc/gd/escape-bits.md):
// `return &T{...}` may build its result in a buffer on the caller's
// stack. These tests keep results alive in the ways that must not share
// or reuse that buffer.

type ebgT struct {
	a    int
	next *ebgT
}

//go:noinline
func ebgMk(a int) *ebgT { return &ebgT{a: a} }

//go:noinline
func ebgSucc(prev *ebgT) *ebgT { return &ebgT{a: prev.a + 1} }

//go:noinline
func ebgBuild(n int) *ebgT {
	var head *ebgT
	for i := range n {
		head = &ebgT{a: i, next: head}
	}
	return head
}

//go:noinline
func ebgRec(n int) *ebgT {
	if n == 0 {
		return &ebgT{}
	}
	return &ebgT{a: n, next: ebgRec(n - 1)}
}

var ebgGlobal *ebgT

var ebgSink int

func TestEscapeBitsOutBufInUse(t *testing.T) {
	if n := testing.AllocsPerRun(100, func() { p := ebgMk(1); ebgSink += p.a }); n != 0 {
		t.Skipf("Phase G outBuf not in use (%v allocs); the tests below would not exercise it", n)
	}
}

func TestEscapeBitsOutBufFedBack(t *testing.T) {
	p := ebgMk(0)
	for range 5 {
		p = ebgSucc(p)
	}
	if p.a != 5 {
		t.Fatalf("p = succ(p) five times: a=%d, want 5", p.a)
	}
}

func TestEscapeBitsOutBufPreviousIteration(t *testing.T) {
	var prev, first *ebgT
	sum := 0
	for i := 1; i <= 3; i++ {
		cur := ebgMk(i)
		if first == nil {
			first = cur
		} else if cur == first {
			t.Fatalf("iteration %d reused the first result", i)
		}
		if prev != nil {
			sum += prev.a * 10
		}
		sum += cur.a
		prev = cur
	}
	if sum != 36 {
		t.Fatalf("sum=%d, want 36", sum)
	}
}

func TestEscapeBitsOutBufKeptInCaller(t *testing.T) {
	var hold [3]*ebgT
	for i := range hold {
		hold[i] = ebgMk(i)
	}
	m := map[int]*ebgT{}
	for i := range 3 {
		m[i] = ebgMk(i)
	}
	head := ebgMk(1)
	head.next = ebgMk(2)
	ebaSmash(10)
	runtime.GC()
	if hold[0] == hold[1] || hold[2].a != 2 || m[0] == m[1] || m[2].a != 2 || head.next == head || head.next.a != 2 {
		t.Fatalf("results kept in the caller were shared or clobbered")
	}
}

func TestEscapeBitsOutBufMultipleAllocations(t *testing.T) {
	h := ebgBuild(4)
	r := ebgRec(3)
	ebaSmash(10)
	runtime.GC()
	for _, c := range []struct {
		name string
		p    *ebgT
	}{{"loop builder", h}, {"recursive builder", r}} {
		n, want := 0, 3
		for q := c.p; q != nil && n < 10; q = q.next {
			if q.a != want {
				t.Fatalf("%s: node %d has a=%d, want %d", c.name, n, q.a, want)
			}
			n++
			want--
		}
		if n != 4 {
			t.Fatalf("%s: %d nodes, want 4", c.name, n)
		}
	}
}

func TestEscapeBitsOutBufRetainedByDynamicCallee(t *testing.T) {
	keep := func(p *ebgT) { ebgGlobal = p }
	p := ebgMk(11)
	keep(p)
	ebaSmash(10)
	runtime.GC()
	if ebgGlobal.a != 11 {
		t.Fatalf("retained result a=%d, want 11", ebgGlobal.a)
	}
}
