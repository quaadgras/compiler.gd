// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"fmt"
	"regexp"
	"strings"
)

// Flat functions. Go's goto may not jump into a block, but C's may (into
// a loop, to a case label inside a statement of its switch, back to a
// setjmp). Such a function's control flow is lowered to labels and gotos
// at the top level of its body; its locals are all declared at the top
// already, so a goto jumps over no declaration.

// needsFlat reports whether f must be lowered.
func needsFlat(f *funcDecl) bool {
	if f.hasGoto || f.setjmps > 0 {
		return true
	}
	nested := false
	var walk func(s stmt, sw *switchStmt, top bool)
	walk = func(s stmt, sw *switchStmt, top bool) {
		switch s := s.(type) {
		case *caseStmt:
			if s.sw != sw || !top {
				nested = true
			}
			walk(s.body, sw, top)
		case *block:
			for _, x := range s.list {
				walk(x, sw, top)
			}
		case *ifStmt:
			walk(s.then, sw, false)
			walk(s.els, sw, false)
		case *whileStmt:
			walk(s.body, sw, false)
		case *doStmt:
			walk(s.body, sw, false)
		case *forStmt:
			walk(s.body, sw, false)
		case *labelStmt:
			walk(s.body, sw, top)
		case *switchStmt:
			if b, ok := s.body.(*block); ok {
				for _, x := range b.list {
					walk(x, s, true)
				}
			} else {
				walk(s.body, s, true)
			}
		}
	}
	walk(f.body, nil, false)
	return nested
}

type flatState struct {
	brk, cont []string
	cases     map[*caseStmt]string
	named     map[string][2]string // a labeled loop's or switch's break and continue
	pending   []string             // the labels of the statement next
}

func (g *gen) label() string {
	g.tmpN++
	return fmt.Sprintf("F%d", g.tmpN)
}

func (g *gen) placeLabel(l string) {
	g.p("%s:", l)
	g.p(";")
}

// flatStmt generates s lowered.
func (g *gen) flatStmt(s stmt) {
	fs := g.flatSt
	switch s.(type) {
	case *labelStmt, *caseStmt:
	default:
		defer func() { fs.pending = nil }()
	}
	switch s := s.(type) {
	case nil, *emptyStmt:
	case *block:
		g.blockStmts(s, g.flatStmt)
	case *deferStmt:
		g.flatStmt(s.body)
	case *ifStmt:
		if eff, v, ok := constCond(s.c); ok && !hasTarget(s) {
			if eff != nil {
				g.effect(eff)
			}
			if v {
				g.flatStmt(s.then)
			} else {
				g.flatStmt(s.els)
			}
			return
		}
		if eff, v, ok := constCond(s.c); ok && !v {
			// if (0) { ... case 2: ... }: the start is dead.
			if eff != nil {
				g.effect(eff)
			}
			els, end := g.label(), g.label()
			g.p("goto %s", els)
			g.deadStmt(s.then)
			g.p("goto %s", end)
			g.placeLabel(els)
			g.flatStmt(s.els)
			g.placeLabel(end)
			return
		}
		els, end := g.label(), g.label()
		g.p("if !(%s) { goto %s }", g.cond(s.c), els)
		g.flatStmt(s.then)
		g.p("goto %s", end)
		g.placeLabel(els)
		g.flatStmt(s.els)
		g.placeLabel(end)
	case *whileStmt:
		top, brk := g.label(), g.label()
		g.placeLabel(top)
		g.p("if !(%s) { goto %s }", g.cond(s.c), brk)
		fs.brk, fs.cont = append(fs.brk, brk), append(fs.cont, top)
		g.named(brk, top)
		g.inFrame(frLoop, s.body, g.flatStmt)
		fs.brk, fs.cont = fs.brk[:len(fs.brk)-1], fs.cont[:len(fs.cont)-1]
		g.p("goto %s", top)
		g.placeLabel(brk)
	case *doStmt:
		top, cont, brk := g.label(), g.label(), g.label()
		g.placeLabel(top)
		fs.brk, fs.cont = append(fs.brk, brk), append(fs.cont, cont)
		g.named(brk, cont)
		g.inFrame(frLoop, s.body, g.flatStmt)
		fs.brk, fs.cont = fs.brk[:len(fs.brk)-1], fs.cont[:len(fs.cont)-1]
		g.placeLabel(cont)
		g.p("if %s { goto %s }", g.cond(s.c), top)
		g.placeLabel(brk)
	case *forStmt:
		names := fs.pending
		fs.pending = nil
		g.flatStmt(s.init)
		fs.pending = names
		top, cont, brk := g.label(), g.label(), g.label()
		g.placeLabel(top)
		if s.c != nil {
			g.p("if !(%s) { goto %s }", g.cond(s.c), brk)
		}
		fs.brk, fs.cont = append(fs.brk, brk), append(fs.cont, cont)
		g.named(brk, cont)
		g.inFrame(frLoop, s.body, g.flatStmt)
		fs.brk, fs.cont = fs.brk[:len(fs.brk)-1], fs.cont[:len(fs.cont)-1]
		g.placeLabel(cont)
		if s.post != nil {
			g.effect(s.post)
		}
		g.p("goto %s", top)
		g.placeLabel(brk)
	case *switchStmt:
		x := g.rv(s.x)
		t := g.tmp(goType(s.x.ty()))
		g.p("%s = %s", t, x)
		brk := g.label()
		var dflt string
		for _, c := range s.cases {
			l := g.label()
			fs.cases[c] = l
			switch {
			case c.dflt:
				dflt = l
			case c.lo == c.hi:
				g.p("if %s == %s { goto %s }", t, constLit(s.x.ty(), c.lo), l)
			default:
				g.p("if %s >= %s && %s <= %s { goto %s }", t, constLit(s.x.ty(), c.lo), t, constLit(s.x.ty(), c.hi), l)
			}
		}
		if dflt != "" {
			g.p("goto %s", dflt)
		} else {
			g.p("goto %s", brk)
		}
		fs.brk = append(fs.brk, brk)
		g.named(brk, "")
		g.inFrame(frSwitch, s.body, g.deadStmt)
		fs.brk = fs.brk[:len(fs.brk)-1]
		g.placeLabel(brk)
	case *caseStmt:
		g.placeLabel(fs.cases[s])
		g.flatStmt(s.body)
	case *breakStmt:
		g.exitsForBreak(false)
		if s.label != "" {
			g.p("goto %s", fs.named[s.label][0])
			return
		}
		g.p("goto %s", fs.brk[len(fs.brk)-1])
	case *continueStmt:
		g.exitsForBreak(true)
		if s.label != "" {
			g.p("goto %s", fs.named[s.label][1])
			return
		}
		g.p("goto %s", fs.cont[len(fs.cont)-1])
	case *labelStmt:
		g.placeLabel("L_" + s.l.goName())
		fs.pending = append(fs.pending, s.l.name)
		g.flatStmt(s.body)
	default:
		g.stmt(s)
	}
}

// deadStmt generates s, which cannot be reached at its start.
func (g *gen) deadStmt(s stmt) {
	if b, ok := s.(*block); ok {
		for _, x := range liveFrom(b.list, true) {
			g.flatStmt(x)
		}
		return
	}
	if hasTarget(s) {
		g.flatStmt(s)
	}
}

// named records a loop or switch's labels for break and continue with
// its label's name.
func (g *gen) named(brk, cont string) {
	fs := g.flatSt
	if fs.named == nil {
		fs.named = map[string][2]string{}
	}
	for _, l := range fs.pending {
		fs.named[l] = [2]string{brk, cont}
	}
	fs.pending = nil
}

var labelDef = regexp.MustCompile(`(?m)^\s*([A-Za-z_][A-Za-z0-9_]*):\n\s*;\n`)
var labelUse = regexp.MustCompile(`goto ([A-Za-z_][A-Za-z0-9_]*)`)

// dropUnusedLabels removes the labels no goto names: Go rejects them.
func dropUnusedLabels(body string) string {
	used := map[string]bool{}
	for _, m := range labelUse.FindAllStringSubmatch(body, -1) {
		used[m[1]] = true
	}
	return labelDef.ReplaceAllStringFunc(body, func(s string) string {
		name := strings.TrimSpace(s)
		name = name[:strings.IndexByte(name, ':')]
		if used[name] {
			return s
		}
		return ""
	})
}

// live drops the statements of list that cannot run: after a jump, up to
// one a jump could reach (a label or a case inside it). GCC drops them
// even when not optimizing, and with them their references (to functions
// never defined, say).
func live(list []stmt) []stmt { return liveFrom(list, false) }

// liveFrom is live, for a list that cannot be reached at its start if
// dead.
func liveFrom(list []stmt, dead bool) []stmt {
	var out []stmt
	for _, s := range list {
		if dead && !hasTarget(s) {
			continue
		}
		dead = false
		out = append(out, s)
		if isJump(s) {
			dead = true
		}
	}
	return out
}

func isJump(s stmt) bool {
	switch s := s.(type) {
	case *breakStmt, *continueStmt, *returnStmt, *gotoStmt:
		return true
	case *exprStmt:
		if c, ok := s.x.(*call); ok && c.ft.noreturn {
			return true
		}
	case *block:
		l := live(s.list)
		return len(l) > 0 && isJump(l[len(l)-1]) && !hasTarget(s)
	case *caseStmt:
		return isJump(s.body)
	case *labelStmt:
		return isJump(s.body)
	case *ifStmt:
		if _, v, ok := constCond(s.c); ok && !hasTarget(s) {
			if v {
				return isJump(s.then)
			}
			return s.els != nil && isJump(s.els)
		}
		return s.els != nil && isJump(s.then) && isJump(s.els)
	}
	return false
}

// hasTarget reports whether s has a label or case a jump may reach.
func hasTarget(s stmt) bool {
	found := false
	var walk func(s stmt)
	walk = func(s stmt) {
		if found {
			return
		}
		switch s := s.(type) {
		case *labelStmt, *caseStmt:
			found = true
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
			// Its cases are its own; its labels are not.
			walk(s.body)
		}
	}
	walk(s)
	return found
}

// constCond reports the value of a condition that is constant but for
// effects first (x, 0): the effects, and the value.
func constCond(e expr) (expr, bool, bool) {
	switch e := e.(type) {
	case *intConst:
		return nil, e.v != 0, true
	case *binary:
		if e.op == "," {
			if c, ok := e.y.(*intConst); ok {
				return e.x, c.v != 0, true
			}
		}
	}
	return nil, false, false
}
