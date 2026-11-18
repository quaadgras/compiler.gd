// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"runtime"
	"testing"
)

// Phase G (doc/gd/escape-bits.md): a result variable that a loop
// assigns by name holds what an earlier iteration allocated while a
// later one runs, so those allocations can't all be made in the one
// buffer the caller passed for the result. (A return statement's
// assignment ends the function: TestEscapeBitsOutBufReturnShapes's
// ebrReturnInLoop.)

type eblN struct {
	v    int
	next *eblN
}

//go:noinline
func eblMk(v int) *eblN { return &eblN{v: v} }

var eblMkDyn = eblMk

//go:noinline
func eblList(n int) (p *eblN) {
	for i := 0; i < n; i++ {
		p = &eblN{v: i + 1, next: p}
	}
	return
}

//go:noinline
func eblSum(n int) (p *eblN) {
	for i := 0; i < n; i++ {
		q := &eblN{v: i + 1}
		if p != nil {
			q.v += p.v
		}
		p = q
	}
	return
}

//go:noinline
func eblSumNew(n int) (p *eblN) {
	for i := 0; i < n; i++ {
		q := new(eblN)
		q.v = i + 1
		if p != nil {
			q.v += p.v
		}
		p = q
	}
	return p
}

//go:noinline
func eblSumLocal(n int) (p *eblN) {
	for i := 0; i < n; i++ {
		var x eblN
		x.v = i + 1
		if p != nil {
			x.v += p.v
		}
		p = &x
	}
	return
}

//go:noinline
func eblSumCall(n int) (p *eblN) {
	for i := 0; i < n; i++ {
		q := eblMk(i + 1)
		if p != nil {
			q.v += p.v
		}
		p = q
	}
	return
}

//go:noinline
func eblSumDynCall(n int) (p *eblN) {
	for i := 0; i < n; i++ {
		q := eblMkDyn(i + 1)
		if p != nil {
			q.v += p.v
		}
		p = q
	}
	return
}

//go:noinline
func eblSumGoto(n int) (p *eblN) {
	i := 0
loop:
	q := &eblN{v: i + 1}
	if p != nil {
		q.v += p.v
	}
	p = q
	i++
	if i < n {
		goto loop
	}
	return
}

//go:noinline
func eblSumNested(n int) (p *eblN) {
	for i := 0; i < n; i++ {
		for j := 0; j < 1; j++ {
			q := &eblN{v: i + 1}
			if p != nil {
				q.v += p.v
			}
			p = q
		}
	}
	return
}

//go:noinline
func eblSumTwo(n int) (p *eblN, k int) {
	for i := 0; i < n; i++ {
		prev := p
		p, k = &eblN{v: i + 1}, i
		if prev != nil {
			p.v += prev.v
		}
	}
	return
}

//go:noinline
func eblBest(n int) (p *eblN) {
	for i := 0; i < n; i++ {
		q := &eblN{v: (i * 7) % 5}
		if p == nil || q.v > p.v {
			p = q
		}
	}
	return
}

func TestEscapeBitsOutBufResultAssignedInLoop(t *testing.T) {
	if p := eblList(4); p.v != 4 || p.next == nil || p.next.v != 3 || p.next.next.next.next != nil {
		t.Errorf("eblList(4) is not the list 4 3 2 1: first %d, next %v", p.v, p.next)
	}
	for _, c := range []struct {
		name string
		f    func(int) *eblN
		want int
	}{
		{"&T{}", eblSum, 10},
		{"new", eblSumNew, 10},
		{"local", eblSumLocal, 10},
		{"call", eblSumCall, 10},
		{"dynamic call", eblSumDynCall, 10},
		{"goto loop", eblSumGoto, 10},
		{"nested loop", eblSumNested, 10},
		{"with a second result", func(n int) *eblN { p, _ := eblSumTwo(n); return p }, 10},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := c.f(4)
			ebmSmash(20)
			runtime.GC()
			if p.v != c.want {
				t.Errorf("got %d, want %d", p.v, c.want)
			}
		})
	}
	if p := eblBest(5); p.v != 4 {
		t.Errorf("eblBest(5) = %d, want 4", p.v)
	}
}
