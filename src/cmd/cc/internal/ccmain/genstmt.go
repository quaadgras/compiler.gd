// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"fmt"
	"go/scanner"
	gotoken "go/token"
	"strings"
)

// Functions and statements.

// local is how a local is held.
type localKind uint8

const (
	lkVar     localKind = iota // a Go variable of its value
	lkCell                     // a c23.Obj on the stack; the Go variable is its *c23.Void
	lkHeap                     // a variable length array: the Go variable is its *c23.Void
	lkAligned                  // aligned more than Go aligns: in the heap (c23.NewAligned)
)

func localKindOf(s *symbol) localKind {
	switch {
	case s.t.isVLA():
		return lkHeap
	case s.t.alignof() > 8 && (s.t.align > 0 || s.addr || s.t.isAggregate()):
		return lkAligned
	case s.addr || s.t.isAggregate():
		return lkCell
	}
	return lkVar
}

// funcSig is the Go type of a C function of type ft.
func (g *gen) funcSig(ft *funcType, names []string) string {
	if g.o.thin {
		return g.tfuncSig(ft, names)
	}
	var ps []string
	if ft.ret.kind == kStruct && names != nil {
		ps = append(ps, "sret *c23.Void")
	} else if ft.ret.kind == kStruct {
		ps = append(ps, "*c23.Void") // a type: its parameters have no names
	}
	for i, pt := range ft.params {
		if names != nil {
			ps = append(ps, names[i]+" "+goType(pt))
		} else {
			ps = append(ps, goType(pt))
		}
	}
	if ft.variadic || !ft.proto && len(ft.params) == 0 {
		if names != nil {
			ps = append(ps, "va ...any")
		} else {
			ps = append(ps, "...any")
		}
	}
	res := ""
	if ft.ret.kind != kVoid && ft.ret.kind != kStruct {
		res = " " + goType(ft.ret)
	}
	return fmt.Sprintf("func(%s)%s", strings.Join(ps, ", "), res)
}

// tmp declares a temporary of Go type t at the top of the function.
func (g *gen) tmp(t string) string {
	g.tmpN++
	n := fmt.Sprintf("t%d", g.tmpN)
	if g.fn == nil {
		// File scope (an initializer): declared where it is.
		g.p("var %s %s", n, t)
		g.p("_ = %s", n)
		return n
	}
	fmt.Fprintf(&g.locals, "var %s %s\n_ = %s\n", n, t, n)
	return n
}

// cellTmp makes a temporary object of type t on the stack: its *c23.Void.
func (g *gen) cellTmp(t *ctype) string {
	g.tmpN++
	if g.fn == nil {
		n := fmt.Sprintf("t%d", g.tmpN)
		g.p("%s := c23.New[%s]()", n, g.mirror(t))
		g.p("_ = %s", n)
		return n
	}
	fmt.Fprintf(&g.cells, "var o%d c23.Obj[%s]\nt%d := c23.LocalT(&o%d, %s)\n_ = t%d\n", g.tmpN, g.mirror(t), g.tmpN, g.tmpN, g.typeDesc(g.mirror(t)), g.tmpN)
	return fmt.Sprintf("t%d", g.tmpN)
}

func (g *gen) localName(s *symbol) string {
	if n, ok := g.names[s]; ok {
		return n
	}
	g.tmpN++
	n := fmt.Sprintf("v%d_%s", g.tmpN, goIdent(strings.ReplaceAll(s.name, ".", "_")))
	g.names[s] = n
	return n
}

// declareLocal declares a local at the top of the function.
func (g *gen) declareLocal(s *symbol) {
	if g.o.thin {
		g.tdeclareLocal(s)
		return
	}
	if s.nrvo {
		g.names[s] = "sret"
		return
	}
	n := g.localName(s)
	switch localKindOf(s) {
	case lkVar:
		fmt.Fprintf(&g.locals, "var %s %s\n_ = %s\n", n, goType(s.t), n)
	case lkCell:
		g.tmpN++
		fmt.Fprintf(&g.cells, "var o%d c23.Obj[%s]\n%s := c23.LocalT(&o%d, %s)\n_ = %s\n", g.tmpN, g.objMirror(s.t), n, g.tmpN, g.typeDesc(g.objMirror(s.t)), n)
		if g.objMirror(s.t) == g.mirror(s.t) {
			g.objs[s] = fmt.Sprintf("o%d.V", g.tmpN)
		}
	case lkHeap:
		fmt.Fprintf(&g.locals, "var %s *c23.Void\n_ = %s\n", n, n)
	case lkAligned:
		fmt.Fprintf(&g.cells, "%s := c23.NewAlignedT(%s, %d)\n_ = %s\n", n, g.typeDesc(g.objMirror(s.t)), s.t.alignof(), n)
	}
}

func (g *gen) function(f *funcDecl) {
	g.tmpN = 0
	lit := g.funcLit(f)
	fmt.Fprintf(&g.funcs, "func %s%s\n\n", g.names[f.sym], strings.TrimPrefix(lit, "func"))
	g.fn = nil
}

// funcLit generates f as a Go function literal.
func (g *gen) funcLit(f *funcDecl) string {
	g.fn = f
	g.locals.Reset()
	g.cells.Reset()
	g.pending, g.inBody = nil, map[string]bool{}
	// A goto may not jump over a Go declaration, and setjmp's and a flat
	// function's code is entered at labels: there the locals are all
	// declared at the top.
	g.inPlace = g.o.thin && !needsFlat(f) && f.setjmps == 0 && len(f.labels) == 0 && len(f.addrLabels) == 0
	var fb strings.Builder
	ft := f.sym.t.fn
	var body strings.Builder
	g.out, g.ind = &body, 1
	g.sret = ft.ret.kind == kStruct
	names := []string{} // not nil: named
	for _, ps := range f.params {
		if g.o.thin {
			if ps.t.kind != kStruct {
				names = append(names, g.localName(ps)) // the Go parameter is the object
				continue
			}
			g.tmpN++
			pn := fmt.Sprintf("p%d", g.tmpN)
			names = append(names, pn)
			if ps.noCopy {
				g.names[ps] = "(*" + pn + ")" // C++: the caller's object
				continue
			}
			g.declareLocal(ps)
			g.p("%s = *%s", g.localName(ps), pn)
			continue
		}
		if localKindOf(ps) == lkVar {
			names = append(names, g.localName(ps))
			continue
		}
		g.tmpN++
		pn := fmt.Sprintf("p%d", g.tmpN)
		names = append(names, pn)
		if ps.noCopy {
			// C++: the caller's object.
			g.names[ps] = pn
			continue
		}
		g.declareLocal(ps)
		g.store(g.localName(ps), ps.t, pn, nil)
	}
	for _, s := range f.locals {
		g.declareLocal(s)
	}
	if f.usesVA {
		fmt.Fprintf(&g.locals, "_ = va\n")
	}
	g.jmp = f.setjmps > 0
	g.flatSt = nil
	if needsFlat(f) {
		g.flatSt = &flatState{cases: map[*caseStmt]string{}}
		g.flatStmt(f.body)
	} else {
		g.stmt(f.body)
	}
	sig := g.funcSig(ft, names)
	hasRes := ft.ret.kind != kVoid && ft.ret.kind != kStruct
	fmt.Fprintf(&fb, "%s {\n", sig)
	for _, s := range g.pending {
		g.thoist(s)
	}
	text := body.String()
	locals := g.locals.String()
	if g.o.thin {
		locals, text = usedLocals(locals, g.cells.String(), text, g.inBody)
	}
	fb.WriteString(locals)
	fb.WriteString(g.cells.String())
	if g.flatSt != nil {
		text = dropUnusedLabels(text)
	}
	if !g.jmp {
		fb.WriteString(text)
		if hasRes && !endsInReturn(text) {
			fmt.Fprintf(&fb, "\treturn %s\n", g.zero(ft.ret))
		}
	} else {
		// setjmp: the body runs again from a setjmp's site when longjmp
		// unwinds to it (c23.Longjmp).
		fb.WriteString("jf := c23.NewJmpFrame()\ndefer jf.Done()\nvar jsite, jval int32\n_ = jval\nfor {\n")
		if hasRes {
			fmt.Fprintf(&fb, "r, ok := func() (r %s, ok bool) {\n", g.gt(ft.ret))
		} else {
			fb.WriteString("ok := func() (ok bool) {\n")
		}
		fb.WriteString("defer func() {\nif x := recover(); x != nil {\ns, v, mine := jf.Catch(x)\nif !mine {\npanic(x)\n}\njsite, jval = s, v\n}\n}()\nswitch jsite {\n")
		for k := 1; k <= f.setjmps; k++ {
			fmt.Fprintf(&fb, "case %d:\ngoto J%d\n", k, k)
		}
		fb.WriteString("}\n")
		fb.WriteString(text)
		if hasRes {
			fmt.Fprintf(&fb, "return %s, true\n}()\nif ok {\nreturn r\n}\n}\n", g.zero(ft.ret))
		} else {
			fb.WriteString("return true\n}()\nif ok {\nreturn\n}\n}\n")
		}
	}
	fb.WriteString("}")
	return fb.String()
}

// usedLocals takes out of a function's Go what Go's rule that a variable
// be used made it carry. locals are the declarations at its top, each
// "var x T" followed by "_ = x"; cells and body are the rest; inBody
// are the variables body declares itself, each also followed by
// "_ = x". A variable that is read needs no blank use; one at the top
// that is never named needs no declaration; one that is only assigned
// keeps both (what is assigned may have effects).
func usedLocals(locals, cells, body string, inBody map[string]bool) (string, string) {
	reads, writes := varUses(cells + body)
	var out strings.Builder
	lines := strings.SplitAfter(locals, "\n")
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		name, ok := "", false
		if f := strings.Fields(l); len(f) >= 3 && f[0] == "var" && i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == "_ = "+f[1] {
			name, ok = f[1], true
		}
		switch {
		case !ok:
			out.WriteString(l)
		case reads[name] > 0:
			out.WriteString(l)
			i++
		case writes[name] == 0:
			i++
		default:
			out.WriteString(l)
			out.WriteString(lines[i+1])
			i++
		}
	}
	if len(inBody) == 0 {
		return out.String(), body
	}
	var b strings.Builder
	for _, l := range strings.SplitAfter(body, "\n") {
		// Its own blank use is one of the reads counted.
		if name, ok := strings.CutPrefix(strings.TrimSpace(l), "_ = "); ok && inBody[name] && reads[name] > 1 {
			continue
		}
		b.WriteString(l)
	}
	return out.String(), b.String()
}

// varUses counts, for each identifier of the Go statements src, where
// it is read and where it is only assigned (x = v, x++, x += v at the
// start of a statement, which Go does not take for a use). A
// declaration's own name and a selector's field are neither.
func varUses(src string) (reads, writes map[string]int) {
	reads, writes = map[string]int{}, map[string]int{}
	fset := gotoken.NewFileSet()
	var sc scanner.Scanner
	sc.Init(fset.AddFile("", fset.Base(), len(src)), []byte(src), nil, 0)
	type tok struct {
		t   gotoken.Token
		lit string
	}
	var toks []tok
	for {
		_, t, lit := sc.Scan()
		if t == gotoken.EOF {
			break
		}
		toks = append(toks, tok{t, lit})
	}
	for i, k := range toks {
		if k.t != gotoken.IDENT {
			continue
		}
		prev := gotoken.SEMICOLON
		if i > 0 {
			prev = toks[i-1].t
		}
		if prev == gotoken.VAR || prev == gotoken.PERIOD {
			continue
		}
		if i+1 < len(toks) && toks[i+1].t == gotoken.DEFINE {
			continue // x := v declares it
		}
		next := gotoken.EOF
		if i+1 < len(toks) {
			next = toks[i+1].t
		}
		if next == gotoken.COLON && (prev == gotoken.LBRACE || prev == gotoken.COMMA) {
			continue // a field's name in a composite literal
		}
		start := prev == gotoken.SEMICOLON || prev == gotoken.LBRACE || prev == gotoken.RBRACE || prev == gotoken.COLON
		assigned := next == gotoken.ASSIGN || next == gotoken.INC || next == gotoken.DEC || next >= gotoken.ADD_ASSIGN && next <= gotoken.AND_NOT_ASSIGN
		if start && assigned {
			writes[k.lit]++
		} else {
			reads[k.lit]++
		}
	}
	return reads, writes
}

// endsInReturn reports whether the last statement of body, a function's,
// is a return: Go then wants none after it.
func endsInReturn(body string) bool {
	body = strings.TrimRight(body, " \t\n")
	last := body[strings.LastIndexByte(body, '\n')+1:]
	return strings.HasPrefix(strings.TrimSpace(last), "return")
}

// nestedFunc generates GNU's nested function f where it is defined: a
// Go closure, which its own locals and the enclosing function's reach.
func (g *gen) nestedFunc(f *funcDecl) {
	saveFn, saveOut, saveInd := g.fn, g.out, g.ind
	saveLocals, saveCells := g.locals.String(), g.cells.String()
	savePending, saveInPlace, saveInBody := g.pending, g.inPlace, g.inBody
	saveSret, saveJmp, saveFlat, saveFrames := g.sret, g.jmp, g.flatSt, g.frames
	g.frames = nil
	lit := g.funcLit(f)
	g.fn, g.out, g.ind = saveFn, saveOut, saveInd
	g.locals.Reset()
	g.locals.WriteString(saveLocals)
	g.cells.Reset()
	g.cells.WriteString(saveCells)
	g.sret, g.jmp, g.flatSt, g.frames = saveSret, saveJmp, saveFlat, saveFrames
	g.pending, g.inPlace, g.inBody = savePending, saveInPlace, saveInBody
	name := g.localName(f.sym)
	fmt.Fprintf(&g.locals, "var %s %s\n_ = %s\n", name, g.funcSig(f.sym.t.fn, nil), name)
	g.p("%s = %s", name, lit)
}

// ret emits a return of v ("" if none).
func (g *gen) ret(v string) {
	if g.hasExits(0) {
		if v != "" {
			t := g.tmp(g.gt(g.fn.sym.t.fn.ret))
			g.p("%s = %s", t, v)
			v = t
		}
		g.exitsForReturn()
	}
	switch {
	case g.jmp && v != "":
		g.p("return %s, true", v)
	case g.jmp:
		g.p("return true")
	case v != "":
		g.p("return %s", v)
	default:
		g.p("return")
	}
}

func zeroOf(t *ctype) string {
	if goType(t) == "*c23.Void" {
		return "(*c23.Void)(nil)"
	}
	if is128(t) {
		return goType(t) + "{}"
	}
	return "0"
}

func (g *gen) stmt(s stmt) {
	switch s := s.(type) {
	case nil, *emptyStmt:
	case *block:
		g.blockStmts(s, g.stmt)
	case *nestedFuncStmt:
		g.nestedFunc(s.f)
	case *deferStmt:
		// Not in a block's list: its own block.
		g.stmt(s.body)
	case *exprStmt:
		g.effect(s.x)
	case *declStmt:
		for _, sym := range s.syms {
			g.localInit(sym)
		}
	case *ifStmt:
		if eff, v, ok := constCond(s.c); ok && !hasTarget(s) {
			if eff != nil {
				g.effect(eff)
			}
			if v {
				g.stmt(s.then)
			} else {
				g.stmt(s.els)
			}
			return
		}
		c := g.cond(s.c)
		g.p("if %s {", c)
		g.stmt(s.then)
		if s.els != nil {
			g.p("} else {")
			g.stmt(s.els)
		}
		g.p("}")
	case *whileStmt:
		pre, c := g.condPre(s.c)
		if pre == "" {
			g.p("for %s {", c)
		} else {
			g.p("for {")
			g.out.WriteString(pre)
			g.p("if !(%s) { break }", c)
		}
		g.inFrame(frLoop, s.body, g.stmt)
		g.p("}")
	case *doStmt:
		pre, c := g.condPre(s.c)
		if !hasContinue(s.body) {
			g.p("for {")
			g.inFrame(frLoop, s.body, g.stmt)
			g.out.WriteString(pre)
			g.p("if !(%s) { break }", c)
			g.p("}")
			return
		}
		g.tmpN++
		f := fmt.Sprintf("f%d", g.tmpN)
		g.p("for %s := true; ; %s = false {", f, f)
		g.p("if !%s {", f)
		g.out.WriteString(pre)
		g.p("if !(%s) { break }", c)
		g.p("}")
		g.inFrame(frLoop, s.body, g.stmt)
		g.p("}")
	case *forStmt:
		g.stmt(s.init)
		pre, c := "", ""
		if s.c != nil {
			pre, c = g.condPre(s.c)
		}
		post := ""
		if s.post != nil {
			post = g.capture(func() { g.effect(s.post) })
		}
		simplePost := !strings.Contains(strings.TrimSpace(post), "\n") && !strings.ContainsAny(post, "{") && !strings.HasPrefix(strings.TrimSpace(post), "var ")
		switch {
		case pre == "" && simplePost:
			g.p("for ; %s; %s {", c, strings.TrimSpace(post))
		case simplePost && !hasContinue(s.body):
			g.p("for ; ; %s {", strings.TrimSpace(post))
			if c != "" {
				g.out.WriteString(pre)
				g.p("if !(%s) { break }", c)
			}
		default:
			g.tmpN++
			f := fmt.Sprintf("f%d", g.tmpN)
			g.p("for %s := true; ; %s = false {", f, f)
			if post != "" {
				g.p("if !%s {", f)
				g.out.WriteString(post)
				g.p("}")
			}
			if c != "" {
				g.out.WriteString(pre)
				g.p("if !(%s) { break }", c)
			}
		}
		g.inFrame(frLoop, s.body, g.stmt)
		g.p("}")
	case *switchStmt:
		g.switchStmt(s)
	case *caseStmt:
		g.errorf(pos{g.u.file, 0}, "case label in a statement nested in its switch is not supported")
	case *breakStmt:
		g.exitsForBreak(false)
		g.p("break")
	case *continueStmt:
		g.exitsForBreak(true)
		g.p("continue")
	case *returnStmt:
		if g.inExpr > 0 {
			g.errorf(pos{g.u.file, 0}, "return in a statement expression is not supported")
		}
		switch {
		case s.x == nil:
			if g.fn.sym.t.fn.ret.kind != kVoid && g.fn.sym.t.fn.ret.kind != kStruct {
				g.ret(g.zero(g.fn.sym.t.fn.ret))
			} else {
				g.ret("")
			}
		case s.x.ty().kind == kStruct:
			v := g.rv(s.x)
			if g.o.thin {
				if v != "sret" {
					g.p("*sret = *%s", v)
				}
			} else {
				g.p("%s", moveCode("sret", v, s.x.ty()))
			}
			g.ret("")
		default:
			g.ret(g.rv(s.x))
		}
	case *gotoStmt:
		if s.x != nil {
			// goto *x: to the label whose address x is.
			if g.o.thin {
				g.p("switch c23.LabelOf(%s) {", g.vptr(g.rv(s.x), s.x.ty()))
			} else {
				g.p("switch c23.Addr(%s) {", g.rv(s.x))
			}
			for _, l := range g.fn.addrLabels {
				g.p("case %d:", l.id)
				g.p("goto L_%s", l.goName())
			}
			g.p("default:")
			g.p("c23.Trap()")
			g.p("}")
			return
		}
		s.l.used = true
		g.exitsForGoto(s.l)
		g.p("goto L_%s", s.l.goName())
	case *labelStmt:
		if s.l.used || g.labelUsed(s.l) {
			g.p("L_%s:", s.l.goName())
			g.p(";")
		}
		g.stmt(s.body)
	default:
		panic(fmt.Sprintf("unknown statement %T", s))
	}
}

// labelUsed reports whether a goto in the function names l.
func (g *gen) labelUsed(l *label) bool {
	found := false
	var walk func(s stmt)
	walk = func(s stmt) {
		if found {
			return
		}
		switch s := s.(type) {
		case *gotoStmt:
			if s.l == l {
				found = true
			}
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
			walk(s.init)
			walk(s.body)
		case *switchStmt:
			walk(s.body)
		case *caseStmt:
			walk(s.body)
		case *labelStmt:
			walk(s.body)
		}
	}
	walk(g.fn.body)
	if found {
		l.used = true
	}
	return found
}

// hasContinue reports whether s has a continue for the loop it is in.
func hasContinue(s stmt) bool {
	switch s := s.(type) {
	case *continueStmt:
		return true
	case *block:
		for _, x := range s.list {
			if hasContinue(x) {
				return true
			}
		}
	case *ifStmt:
		return hasContinue(s.then) || hasContinue(s.els)
	case *switchStmt:
		return hasContinue(s.body)
	case *caseStmt:
		return hasContinue(s.body)
	case *labelStmt:
		return hasContinue(s.body)
	}
	return false
}

// condPre returns the statements a condition needs, and the condition.
func (g *gen) condPre(e expr) (string, string) {
	var c string
	pre := g.capture(func() { c = g.cond(e) })
	return pre, c
}

// capture returns what f emits, instead of emitting it.
func (g *gen) capture(f func()) string {
	save := g.out
	var b strings.Builder
	g.out = &b
	f()
	g.out = save
	return b.String()
}

func (g *gen) switchStmt(s *switchStmt) {
	x := g.rv(s.x)
	t := g.tmp(g.gt(s.x.ty()))
	g.p("%s = %s", t, x)
	// The clauses: each case label starts one, with the statements up to
	// the next.
	type clause struct {
		c    *caseStmt
		body []stmt
	}
	var clauses []*clause
	var list []stmt
	if b, ok := s.body.(*block); ok {
		list = b.list
	} else {
		list = []stmt{s.body}
	}
	var add func(st stmt)
	add = func(st stmt) {
		if c, ok := st.(*caseStmt); ok && c.sw == s {
			clauses = append(clauses, &clause{c: c})
			add(c.body)
			return
		}
		if len(clauses) == 0 {
			// Before the first case: reached only by goto.
			if containsLabel(st) {
				g.errorf(pos{g.u.file, 0}, "a label before the first case of a switch is not supported")
			}
			return
		}
		if containsCase(st, s) {
			g.errorf(s.x.at(), "a case label nested in a statement of its switch is not supported")
		}
		cl := clauses[len(clauses)-1]
		cl.body = append(cl.body, st)
	}
	for _, st := range list {
		add(st)
	}
	g.p("switch {")
	g.pushFrame(frSwitch, nil)
	defer g.popFrame()
	for i, cl := range clauses {
		switch {
		case cl.c.dflt:
			g.p("default:")
		case cl.c.lo == cl.c.hi:
			g.p("case %s == %s:", t, constLit(s.x.ty(), cl.c.lo))
		default:
			g.p("case %s >= %s && %s <= %s:", t, constLit(s.x.ty(), cl.c.lo), t, constLit(s.x.ty(), cl.c.hi))
		}
		for _, st := range cl.body {
			g.stmt(st)
		}
		if i < len(clauses)-1 && !endsJump(cl.body) {
			g.p("fallthrough")
		}
	}
	g.p("}")
}

func endsJump(list []stmt) bool {
	if len(list) == 0 {
		return false
	}
	switch s := list[len(list)-1].(type) {
	case *breakStmt, *continueStmt, *returnStmt, *gotoStmt:
		return true
	case *block:
		return endsJump(s.list)
	}
	return false
}

func containsCase(s stmt, sw *switchStmt) bool {
	found := false
	var walk func(s stmt)
	walk = func(s stmt) {
		switch s := s.(type) {
		case *caseStmt:
			if s.sw == sw {
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
		case *labelStmt:
			walk(s.body)
		case *switchStmt:
			walk(s.body)
		}
	}
	walk(s)
	return found
}

func containsLabel(s stmt) bool {
	switch s := s.(type) {
	case *labelStmt:
		return true
	case *block:
		for _, x := range s.list {
			if containsLabel(x) {
				return true
			}
		}
	}
	return false
}

// localInit runs a local's declaration: allocates a VLA, and initializes.
func (g *gen) localInit(s *symbol) {
	if g.o.thin {
		g.tlocalInit(s)
		return
	}
	n := g.localName(s)
	switch localKindOf(s) {
	case lkHeap:
		size := g.vlaSize(s.t)
		if a := s.t.alignof(); a > 8 {
			// _Alignas
			g.p("%s = c23.NewArrayAlignedT(%s, uintptr(%s), %d)", n, g.typeDesc(g.mirror(baseElem(s.t))), size, a)
			return
		}
		g.p("%s = c23.NewArrayT(%s, uintptr(%s))", n, g.typeDesc(g.mirror(baseElem(s.t))), size)
		return
	case lkVar:
		if s.init != nil {
			if s.init.scalar == nil {
				g.errorf(s.p, "internal error: %s has an aggregate initializer", s.name)
			}
			g.p("%s = %s", n, g.rv(s.init.scalar))
		} else if !s.global {
			// Without an initializer, its value becomes indeterminate
			// each time the declaration is reached (C11 6.2.4): zero.
			// The Go variable is the function's, declared at its top; set
			// here, it does not carry a value from one pass of a loop to
			// the next, which Go's compiler would otherwise keep live
			// around the loop (and in registers at its head): Lua's
			// interpreter loop reloaded 15 such floats each instruction.
			g.p("%s = %s", n, zeroOf(s.t))
		}
		if s.ctor != nil {
			g.effect(s.ctor)
		}
		return
	}
	if s.init != nil {
		if s.init.scalar == nil {
			g.p("c23.Memset(%s, 0, %d)", n, s.t.sizeof())
		}
		g.initObject(n, s.t, s.init)
	}
	if s.ctor != nil {
		g.effect(s.ctor)
	}
}

func baseElem(t *ctype) *ctype {
	for t.kind == kArray {
		t = t.elem
	}
	return t
}

// vlaSize is the Go expression of the size in bytes of a variable length
// array type.
func (g *gen) vlaSize(t *ctype) string {
	if t.kind != kArray || !t.isVLA() {
		return fmt.Sprintf("%s(%d)", goType(tULong), t.sizeof())
	}
	var n string
	if t.vla != nil {
		n = g.rv(t.vla)
	} else {
		n = fmt.Sprintf("%s(%d)", goType(tULong), t.n)
	}
	return fmt.Sprintf("(%s * %s)", n, g.vlaSize(t.elem))
}

// initObject stores an initializer into the object of type t at addr,
// which is zero.
func (g *gen) initObject(addr string, t *ctype, in *initializer) {
	if g.o.thin {
		g.tinitObject(tderef(addr), t, in)
		return
	}
	if in.scalar != nil {
		g.store(addr, t, g.rv(in.scalar), in.scalar)
		return
	}
	for _, it := range in.items {
		dst := addOff(addr, it.off)
		switch {
		case it.zero:
			g.p("c23.Memset(%s, 0, %d)", dst, it.t.sizeof())
		case it.f != nil && it.f.bits >= 0:
			g.bitStore(dst, it.f, g.rv(it.x), it.x)
		default:
			if s, ok := it.x.(*strLit); ok && it.t.kind == kArray {
				n := min(it.t.sizeof(), s.t.sizeof())
				g.p("c23.Memmove(%s, %s, %d)", dst, g.str(s), n)
				continue
			}
			g.store(dst, it.t, g.rv(it.x), it.x)
		}
	}
}

func addOff(addr string, off int64) string {
	if off == 0 {
		return addr
	}
	return fmt.Sprintf("c23.Add(%s, %d)", addr, off)
}
