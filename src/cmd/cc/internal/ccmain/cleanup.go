// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import "fmt"

// Code that runs when a block is left: C2y's defer, and GNU's cleanup
// attribute. It is generated where the block is left: at its end, and
// before a break, continue, return or goto that leaves it, each time in
// a closure of its own (so that its labels are its own).

type frameKind uint8

const (
	frBlock frameKind = iota
	frLoop
	frSwitch
)

type frame struct {
	kind  frameKind
	b     *block
	exits []func() // run in reverse order
}

func (g *gen) pushFrame(k frameKind, b *block) { g.frames = append(g.frames, &frame{kind: k, b: b}) }
func (g *gen) popFrame()                       { g.frames = g.frames[:len(g.frames)-1] }

// hasExits reports whether any code must run on leaving frames from i.
func (g *gen) hasExits(from int) bool {
	for _, f := range g.frames[from:] {
		if len(f.exits) > 0 {
			return true
		}
	}
	return false
}

// runExits generates the exits of the frames from the innermost down to,
// not including, the one at index stop.
func (g *gen) runExits(stop int) {
	for i := len(g.frames) - 1; i > stop; i-- {
		f := g.frames[i]
		for j := len(f.exits) - 1; j >= 0; j-- {
			f.exits[j]()
		}
	}
}

// exitsFor generates the exits a jump runs: break (k frLoop or
// frSwitch, whichever is innermost), continue (frLoop), return (all).
func (g *gen) exitsForBreak(cont bool) {
	for i := len(g.frames) - 1; i >= 0; i-- {
		k := g.frames[i].kind
		if k == frLoop || k == frSwitch && !cont {
			if g.hasExits(i + 1) {
				g.runExits(i)
			}
			return
		}
	}
}

func (g *gen) exitsForReturn() {
	if g.hasExits(0) {
		g.runExits(-1)
	}
}

// exitsForGoto generates the exits of the blocks a goto to l leaves.
func (g *gen) exitsForGoto(l *label) {
	for i := len(g.frames) - 1; i >= 0; i-- {
		f := g.frames[i]
		if f.b != nil && containsLabelOf(f.b, l) {
			if g.hasExits(i + 1) {
				g.runExits(i)
			}
			return
		}
	}
	g.exitsForReturn()
}

func containsLabelOf(s stmt, l *label) bool {
	found := false
	var walk func(s stmt)
	walk = func(s stmt) {
		if found {
			return
		}
		switch s := s.(type) {
		case *labelStmt:
			if s.l == l {
				found = true
			}
			walk(s.body)
		case *block:
			for _, x := range s.list {
				walk(x)
			}
		case *ifStmt:
			walk(s.then)
			walk(s.els)
		case *whileStmt:
			walk(s.body)
		case *doStmt:
			walk(s.body)
		case *forStmt:
			walk(s.body)
		case *switchStmt:
			walk(s.body)
		case *caseStmt:
			walk(s.body)
		}
	}
	walk(s)
	return found
}

// blockStmts generates a block's statements with gen, running its exits
// at its end.
func (g *gen) blockStmts(b *block, gen func(stmt)) {
	g.pushFrame(frBlock, b)
	f := g.frames[len(g.frames)-1]
	list := live(b.list)
	for _, s := range list {
		switch s := s.(type) {
		case *deferStmt:
			body := s.body
			f.exits = append(f.exits, func() { g.closure(func() { gen(body) }) })
			continue
		case *declStmt:
			gen(s)
			for _, sym := range s.syms {
				if sym.dtor != nil {
					d := sym.dtor
					f.exits = append(f.exits, func() { gen(d) })
				}
				if sym.cleanup != nil {
					sym := sym
					f.exits = append(f.exits, func() {
						l := g.lv(&varRef{node{sym.t, sym.p}, sym})
						addr := l.addr

						// To the type of the function's parameter.
						addr = taddr(l.x)
						if ft := sym.cleanup.t.fn; ft != nil && len(ft.params) == 1 {
							addr = g.retype(addr, "*"+g.mirror(sym.t), g.gt(ft.params[0]))
						}

						g.p("%s(%s)", g.funcName(sym.cleanup, sym.p), addr)
					})
				}
			}
			continue
		}
		gen(s)
	}
	if len(f.exits) > 0 && (len(list) == 0 || !isJump(list[len(list)-1])) {
		g.runExits(len(g.frames) - 2)
	}
	g.popFrame()
}

// closure generates what f generates in a Go closure, called.
func (g *gen) closure(f func()) {
	g.p("func() {")
	saveFlat := g.flatSt
	if saveFlat != nil {
		g.flatSt = &flatState{cases: saveFlat.cases}
	}
	f()
	g.flatSt = saveFlat
	g.p("}()")
}

// loopBody generates a loop's body with gen, in a loop frame.
func (g *gen) inFrame(k frameKind, s stmt, gen func(stmt)) {
	g.pushFrame(k, nil)
	gen(s)
	g.popFrame()
}

var _ = fmt.Sprint
