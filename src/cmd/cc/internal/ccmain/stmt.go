// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"fmt"
	"strconv"
	"strings"
)

// Statements (C23 6.8).

// blockBody parses a compound statement, from its "{".
func (p *parser) blockBody() *block {
	p.push()
	b := p.funcBody()
	p.pop()
	return b
}

// funcBody parses a compound statement, from its "{", in the current
// scope: a function's body is in its parameters' (C23 6.2.1).
func (p *parser) funcBody() *block {
	p.expect("{")
	b := &block{}
	saved := p.fe
	for !p.accept("}") {
		if p.peek().kind == tkEOF {
			p.errorf("expected \"}\" at end of input")
		}
		if p.peek().kind == tkPragma {
			// To the end of this compound statement.
			p.stdcPragma(p.next())
			continue
		}
		b.list = append(b.list, p.blockItem()...)
	}
	p.fe = saved
	return b
}

// pushParams opens a function definition's scope, with the tags its
// parameters declared.
func (p *parser) pushParams(ft *funcType) {
	p.push()
	if ft.tags != nil {
		p.sc.tags = ft.tags
	}
}

// blockItem parses a declaration or a statement.
func (p *parser) blockItem() []stmt {
	if p.is("_Static_assert") || p.is("static_assert") {
		p.staticAssert()
		return nil
	}
	if p.accept("__label__") {
		// GNU's local labels: this block's.
		for {
			name := p.ident()
			if p.sc.labels == nil {
				p.sc.labels = map[string]*label{}
			}
			p.symN++
			p.sc.labels[name] = &label{name: name, local: p.symN}
			if !p.accept(",") {
				break
			}
		}
		p.expect(";")
		return nil
	}
	if p.cxx && p.is("using") {
		p.usingDecl()
		return nil
	}
	if p.is("[") && p.isAt(1, "[") {
		// Attributes, of a declaration or a statement.
		save := p.pos
		p.attributes()
		if p.accept(";") {
			return nil
		}
		decl := p.isDecl()
		p.pos = save
		if !decl {
			p.attributes()
			return []stmt{p.statement()}
		}
	}
	if p.isDecl() {
		return p.declaration()
	}
	return []stmt{p.statement()}
}

// isDecl reports whether a declaration starts here (and not a label or
// an expression that starts with a typedef name).
func (p *parser) isDecl() bool {
	if !p.isTypeStart(0) {
		return false
	}
	if p.cxx && !p.declAhead() {
		return false
	}
	if p.peek().kind == tkIdent && p.isAt(1, ":") && !typeWords[p.peek().text] {
		return false // a label named like a typedef
	}
	return true
}

// declaration parses a block-scope declaration.
func (p *parser) declaration() []stmt {
	at := p.here()
	s := p.declSpecs()
	if p.accept(";") {
		// typeof(i++, vla); evaluates its operand.
		out := p.pendingVLA
		p.pendingVLA = nil
		return out
	}
	var syms []*symbol
	var out []stmt
	for {
		name, t, a := p.declarator(p.baseOr(s))
		a.merge(s.attrs)
		if name == "" {
			p.errorf("expected a declarator")
		}
		if t.kind == kFunc && p.is("{") {
			out = append(out, p.nestedFuncDef(name, t, at))
			return out
		}
		if t.kind == kFunc && s.auto {
			// auto int g(int): a nested function's declaration.
			p.sc.vars[name] = &symbol{name: name, kind: symFunc, t: t, p: at, nested: true}
			if !p.accept(",") {
				break
			}
			continue
		}
		sym := p.declarator2(name, t, s, a, at)
		if sym != nil && sym.once != nil {
			if len(syms) > 0 {
				out = append(out, &declStmt{syms: syms})
				syms = nil
			}
			out = append(out, sym.once)
			sym.once = nil
		}
		if len(p.pendingVLA) > 0 {
			if len(syms) > 0 {
				out = append(out, &declStmt{syms: syms})
				syms = nil
			}
			out = append(out, p.pendingVLA...)
			p.pendingVLA = nil
		}
		if sym != nil && sym.kind == symVar && !sym.global {
			syms = append(syms, sym.extended...)
			syms = append(syms, sym)
		}
		if sym != nil && sym.kind == symTypedef && sym.t.isVLA() {
			// Its lengths were computed where it is.
		}
		if !p.accept(",") {
			break
		}
	}
	p.expect(";")
	if len(syms) > 0 {
		out = append(out, &declStmt{syms: syms})
	}
	return out
}

func (p *parser) statement() stmt {
	for p.peek().kind == tkPragma {
		p.stdcPragma(p.next()) // misplaced: from here
	}
	at := p.here()
	if p.is("[") && p.isAt(1, "[") || p.is("__attribute__") {
		// Attributes of a statement ([[fallthrough]];).
		p.attributes()
		if p.accept(";") {
			return &emptyStmt{}
		}
	}
	t := p.peek()
	if t.kind == tkPunct {
		switch t.text {
		case "{":
			return p.blockBody()
		case ";":
			p.next()
			return &emptyStmt{}
		}
	}
	if t.kind == tkIdent {
		switch t.text {
		case "if":
			p.next()
			if p.cxx && p.accept("constexpr") {
				// The branch not taken is not parsed.
				p.expect("(")
				c := p.rvalue(p.expr())
				if c.ty().kind == kStruct {
					c = p.boolOf(c, at)
				}
				v, ok := p.foldTruth(c)
				if !ok {
					p.errorAt(at, "the condition of if constexpr is not a constant")
				}
				p.expect(")")
				if v {
					then := p.subStatement()
					if p.accept("else") {
						p.skipStatement()
					}
					return then
				}
				p.skipStatement()
				if p.accept("else") {
					return p.subStatement()
				}
				return &emptyStmt{}
			}
			p.expect("(")
			p.push()
			decls, c := p.condDecl()
			p.expect(")")
			then := p.subStatement()
			var els stmt
			if p.accept("else") {
				els = p.subStatement()
			}
			p.pop()
			return withDecls(decls, &ifStmt{c, then, els})
		case "while":
			p.next()
			p.expect("(")
			p.push()
			decls, c := p.condDecl()
			p.expect(")")
			body := p.loopBody()
			p.pop()
			if len(decls) == 0 {
				return &whileStmt{c, body}
			}
			return &whileStmt{p.one(), declLoopBody(decls, c, body)}
		case "do":
			p.next()
			body := p.loopBody()
			if !p.accept("while") {
				p.errorf("expected \"while\"")
			}
			p.expect("(")
			c := p.condition()
			p.expect(")")
			p.expect(";")
			return &doStmt{body, c}
		case "for":
			p.next()
			p.expect("(")
			if p.cxx && p.rangeForAhead() {
				return p.rangeFor(at)
			}
			p.push()
			f := &forStmt{}
			if p.isDecl() {
				ds := p.declaration()
				if len(ds) == 1 {
					f.init = ds[0]
				} else if len(ds) > 1 {
					f.init = &block{list: ds}
				}
			} else {
				if !p.is(";") {
					mark := len(p.temps)
					f.init = &exprStmt{p.fullExpr(mark, p.rvalueVoid(p.expr()))}
				}
				p.expect(";")
			}
			var cdecls []stmt
			if !p.is(";") {
				cdecls, f.c = p.condDeclIn(true)
			}
			p.expect(";")
			if !p.is(")") {
				mark := len(p.temps)
				f.post = p.fullExpr(mark, p.rvalueVoid(p.expr()))
			}
			p.expect(")")
			f.body = p.loopBody()
			if len(cdecls) > 0 {
				body := f.body
				if f.post != nil && !hasContinue(body) {
					// The condition's objects live until after the
					// post expression.
					body = &block{list: []stmt{body, &exprStmt{f.post}}}
					f.post = nil
				}
				f.body = declLoopBody(cdecls, f.c, body)
				f.c = nil
			}
			p.pop()
			if d, ok := f.init.(*declStmt); ok && hasCleanup(d) {
				// The for's block: its objects' cleanups run at its end.
				f.init = nil
				return &block{list: []stmt{d, f}}
			}
			return f
		case "switch":
			p.next()
			p.expect("(")
			p.push()
			decls, x := p.condDeclExpr(false)
			if p.cxx && x.ty().kind == kStruct {
				// A class: converted by its conversion function to an
				// integer.
				if c := p.convertClass(x, tLLong, at); c != nil {
					x = c
				}
			}
			x = p.rvalue(x)
			if !x.ty().isInteger() {
				p.errorAt(at, "switch quantity not an integer")
			}
			x = p.convert(x, promote(x.ty()))
			if x.ty().isCap() {
				x = p.convert(x, capInt(x.ty()))
			}
			p.expect(")")
			sw := &switchStmt{x: x}
			save := p.sw
			p.sw = sw
			p.breakOK++
			sw.body = p.subStatement()
			p.breakOK--
			p.sw = save
			p.pop()
			return withDecls(decls, sw)
		case "case":
			p.next()
			if p.sw == nil {
				p.errorAt(at, "case label not within a switch statement")
			}
			lo := p.caseValue(p.sw)
			hi := lo
			if p.accept("...") {
				hi = p.caseValue(p.sw)
			}
			p.expect(":")
			for _, c := range p.sw.cases {
				if !c.dflt && lo <= c.hi && c.lo <= hi {
					p.errorAt(at, "duplicate case value")
				}
			}
			p.caseN++
			c := &caseStmt{lo: lo, hi: hi, sw: p.sw, id: p.caseN}
			p.sw.cases = append(p.sw.cases, c)
			c.body = p.labeledBody()
			return c
		case "default":
			if !p.isAt(1, ":") {
				break
			}
			p.next()
			p.next()
			if p.sw == nil {
				p.errorAt(at, "default label not within a switch statement")
			}
			if p.sw.dflt != nil {
				p.errorAt(at, "multiple default labels in one switch")
			}
			p.caseN++
			c := &caseStmt{dflt: true, sw: p.sw, id: p.caseN}
			p.sw.dflt = c
			p.sw.cases = append(p.sw.cases, c)
			c.body = p.labeledBody()
			return c
		case "break":
			p.next()
			if p.breakOK == 0 {
				p.errorAt(at, "break statement not within loop or switch")
			}
			if p.peek().kind == tkIdent {
				// C2y: break out of the loop or switch with this label.
				p.fn.hasGoto = true
				l := p.ident()
				p.expect(";")
				return &breakStmt{l}
			}
			p.expect(";")
			return &breakStmt{}
		case "continue":
			p.next()
			if p.continueOK == 0 {
				p.errorAt(at, "continue statement not within a loop")
			}
			if p.peek().kind == tkIdent {
				p.fn.hasGoto = true
				l := p.ident()
				p.expect(";")
				return &continueStmt{l}
			}
			p.expect(";")
			return &continueStmt{}
		case "return":
			p.next()
			rt := p.fn.sym.t.fn.ret
			if p.accept(";") {
				if rt.isAuto {
					p.fn.sym.t.fn.ret = tVoid
				}
				return &returnStmt{}
			}
			mark := len(p.temps)
			var x expr
			if p.cxx && p.is("{") {
				x = p.braceList()
			} else {
				x = p.expr()
			}
			if rt.isAuto {
				// A deduced return type: this expression's.
				if pr, ok := x.(*classPR); ok {
					rt = pr.t.unqual()
				} else {
					rt = valueType(x)
				}
				p.fn.sym.t.fn.ret = rt
			}
			p.expect(";")
			if rt.kind == kVoid {
				x = p.fullExpr(mark, p.rvalueVoid(x))
				if x.ty().kind != kVoid {
					// GCC: a warning.
					return &block{list: []stmt{&exprStmt{x}, &returnStmt{}}}
				}
				return &block{list: []stmt{&exprStmt{x}, &returnStmt{}}}
			}
			if p.cxx {
				return p.returnValue(x, rt, at, mark)
			}
			return &returnStmt{p.assignConvert(p.rvalue(x), rt, at, "return")}
		case "_Defer":
			p.next()
			if p.fn == nil {
				p.errorAt(at, "defer outside a function")
			}
			p.breakOK, p.continueOK = p.breakOK+1000, p.continueOK+1000
			body := p.subStatement()
			p.breakOK, p.continueOK = p.breakOK-1000, p.continueOK-1000
			return &deferStmt{body}
		case "goto":
			p.next()
			if p.accept("*") {
				x := p.rvalue(p.expr())
				p.expect(";")
				if !x.ty().isPtr() {
					p.errorAt(at, "computed goto of a %s", x.ty())
				}
				p.fn.hasGoto = true
				return &gotoStmt{x: x}
			}
			name := p.ident()
			p.expect(";")
			p.fn.hasGoto = true
			return &gotoStmt{l: p.label(name)}
		case "asm", "__asm__", "__asm":
			return p.asmStmt(at)
		}
		if p.isAt(1, ":") && !p.isAt(2, ":") {
			// A label.
			name := p.ident()
			p.next()
			p.attributes()
			l := p.label(name)
			if l.p.line != 0 {
				p.errorAt(at, "duplicate label %q", name)
			}
			l.p = at
			return &labelStmt{l, p.labeledBody()}
		}
	}
	mark := len(p.temps)
	x := p.expr()
	p.expect(";")
	x = p.fullExpr(mark, p.rvalueVoid(x))
	if len(p.pendingVLA) > 0 {
		// Variable length array lengths in the expression's types.
		pre := p.pendingVLA
		p.pendingVLA = nil
		x = &seqExpr{node{x.ty(), x.at()}, pre, x}
	}
	return &exprStmt{x}
}

// labeledBody parses what follows a label: a statement, or (C23) a
// declaration or nothing before "}".
func (p *parser) labeledBody() stmt {
	if p.is("}") {
		return &emptyStmt{}
	}
	if p.is("[") && p.isAt(1, "[") {
		return &block{list: p.blockItem()}
	}
	if p.isDecl() {
		return &block{list: p.declaration()}
	}
	return p.statement()
}

// subStatement parses the body of if, else and switch: a scope of its
// own.
func (p *parser) subStatement() stmt {
	p.push()
	s := p.statement()
	p.pop()
	return s
}

func (p *parser) loopBody() stmt {
	p.breakOK++
	p.continueOK++
	saveSw := p.sw
	s := p.subStatement()
	p.sw = saveSw
	p.breakOK--
	p.continueOK--
	return s
}

func (p *parser) label(name string) *label {
	for s := p.sc; s != nil; s = s.parent {
		if l, ok := s.labels[name]; ok {
			return l
		}
	}
	l := p.fn.labels[name]
	if l == nil {
		l = &label{name: name}
		p.fn.labels[name] = l
	}
	return l
}

// condDecl parses C2y's condition of if or switch, which may declare:
// (declaration; expression), or (declaration), whose value is the last
// declared object's.
func (p *parser) condDecl() ([]stmt, expr) {
	return p.condDeclIn(false)
}

// condDeclIn is condDecl, for a for's condition if inFor (where ";"
// ends it).
func (p *parser) condDeclIn(inFor bool) ([]stmt, expr) {
	mark := len(p.temps)
	decls, x := p.condDeclExpr(inFor)
	x = p.rvalue(x)
	return decls, p.fullExpr(mark, p.scalarCond(x, x.at()))
}

func (p *parser) condDeclExpr(inFor bool) ([]stmt, expr) {
	if !p.isDecl() {
		return nil, p.expr()
	}
	if p.cxx && (!p.declAhead() || p.isAt(p.typeSpecEnd(0), "(")) {
		// T(x) == y: a condition's declaration has no parentheses
		// after its type.
		return nil, p.expr()
	}
	at := p.here()
	var decls []stmt
	var last *symbol
again:
	s := p.declSpecs()
	for {
		name, t, a := p.declarator(p.baseOr(s))
		a.merge(s.attrs)
		sym := p.declarator2(name, t, s, a, at)
		decls = append(decls, p.pendingVLA...)
		p.pendingVLA = nil
		if sym.kind == symVar && !sym.global {
			decls = append(decls, &declStmt{syms: append(sym.extended[:len(sym.extended):len(sym.extended)], sym)})
		}
		last = sym
		if !p.accept(",") {
			break
		}
	}
	if !inFor && p.accept(";") {
		if p.isDecl() {
			goto again
		}
		return decls, p.expr()
	}
	if last == nil || last.kind != symVar {
		p.errorAt(at, "a condition declares no object")
	}
	return decls, &varRef{node{last.t, at}, last}
}

func (p *parser) one() expr { return p.intc(1, tInt, p.here()) }

// asmStmt parses GNU's asm statement. One with an empty template, a
// barrier to the optimizer, is what it does to C: its inputs evaluated,
// and each output tied to an input ("=r"(x) : "0"(y)) assigned it;
// other outputs keep their values. Any other assembly is not supported.
func (p *parser) asmStmt(at pos) stmt {
	p.next()
	for p.is("volatile") || p.is("__volatile__") || p.is("__volatile") || p.is("inline") || p.is("__inline__") || p.is("__inline") || p.is("goto") {
		p.next()
	}
	p.expect("(")
	tmpl := ""
	for p.peek().kind == tkString {
		tmpl += p.next().str
	}
	if strings.TrimSpace(tmpl) != "" {
		p.errorAt(at, "inline assembly is not supported")
	}
	colons := 0 // a "::" token's second colon
	section := func() bool {
		switch {
		case colons > 0:
			colons--
		case p.accept(":"):
		case p.accept("::"):
			colons = 1
		default:
			return false
		}
		return true
	}
	type operand struct {
		constraint string
		x          expr
	}
	operands := func() []operand {
		var ops []operand
		for colons == 0 && (p.peek().kind == tkString || p.is("[")) {
			if p.accept("[") {
				p.ident()
				p.expect("]")
			}
			c := p.next().str
			p.expect("(")
			x := p.expr()
			p.expect(")")
			ops = append(ops, operand{c, x})
			if !p.accept(",") {
				break
			}
		}
		return ops
	}
	var outs, ins []operand
	if section() {
		outs = operands()
		if section() {
			ins = operands()
			if section() {
				for colons == 0 && p.peek().kind == tkString {
					p.next() // a clobber
					if !p.accept(",") {
						break
					}
				}
				if section() {
					for p.peek().kind == tkIdent {
						p.label(p.ident()) // asm goto's: never taken
						if !p.accept(",") {
							break
						}
					}
				}
			}
		}
	}
	p.expect(")")
	p.expect(";")
	b := &block{}
	// The outputs, each evaluated once: "+r"(*f()).
	lvals := make([]expr, len(outs))
	for i, out := range outs {
		x := out.x
		if !isLvalue(x) {
			p.errorAt(at, "invalid lvalue in asm output")
		}
		if !noEffects(x) {
			pt := ptrTo(x.ty())
			tmp := p.newLocal("asm", pt, at)
			tmp.init = &initializer{scalar: p.addressOf(x, at)}
			b.list = append(b.list, &declStmt{syms: []*symbol{tmp}})
			x = p.derefOf(&varRef{node{pt, at}, tmp}, at)
		}
		lvals[i] = x
	}
	var ties []stmt
	for _, in := range ins {
		k, err := strconv.Atoi(in.constraint)
		if err != nil || k < 0 || k >= len(outs) {
			b.list = append(b.list, &exprStmt{p.rvalueVoid(in.x)})
			continue
		}
		out := lvals[k]
		p.checkModifiable(out, at)
		tmp := p.newLocal("asm", out.ty().unqual(), at)
		tmp.init = &initializer{scalar: p.convert(p.rvalue(in.x), tmp.t)}
		b.list = append(b.list, &declStmt{syms: []*symbol{tmp}})
		ties = append(ties, &exprStmt{p.makeAssign(at, "=", out, &varRef{node{tmp.t, at}, tmp})})
	}
	b.list = append(b.list, ties...)
	return b
}

// declLoopBody is a loop's body when its condition declares: each time
// round, the declarations, then the test, then the body, in a block
// whose end runs their cleanups.
func declLoopBody(decls []stmt, c expr, body stmt) stmt {
	if len(decls) == 0 {
		if c == nil {
			return body
		}
		// No declarations: the condition stays the loop's.
		return body
	}
	list := append([]stmt(nil), decls...)
	list = append(list, &ifStmt{c: &unary{node{tInt, c.at()}, "!", c}, then: &breakStmt{}}, body)
	return &block{list: list}
}

func hasCleanup(d *declStmt) bool {
	for _, s := range d.syms {
		if s.cleanup != nil || s.dtor != nil {
			return true
		}
	}
	return false
}

// nestedFuncDef parses GNU's nested function definition, after its
// declarator.
func (p *parser) nestedFuncDef(name string, t *ctype, at pos) stmt {
	sym := &symbol{name: name, kind: symFunc, t: t, p: at, defined: true, nested: true}
	if old, ok := p.sc.vars[name]; ok && old.kind == symFunc && old.nested && !old.defined {
		sym = old // declared auto before
		sym.t, sym.defined = t, true
	}
	p.sc.vars[name] = sym
	outer := p.fn
	saveB, saveC, saveSw := p.breakOK, p.continueOK, p.sw
	p.breakOK, p.continueOK, p.sw = 0, 0, nil
	f := &funcDecl{sym: sym, labels: map[string]*label{}}
	sym.fn = f
	p.fn = f
	ft := t.fn
	p.pushParams(ft)
	for i, pt := range ft.params {
		pn := ""
		if i < len(ft.names) {
			pn = ft.names[i]
		}
		ps := &symbol{name: pn, kind: symVar, t: pt, p: at, isParam: true}
		if i < len(ft.psyms) && ft.psyms[i] != nil {
			ps = ft.psyms[i]
			ps.t, ps.p, ps.isParam = pt, at, true
		}
		if pn == "" {
			ps.name = fmt.Sprintf("_%d", i)
		} else {
			p.declare(ps, at)
		}
		f.params = append(f.params, ps)
	}
	f.locals = append(f.locals, ft.vlas...)
	f.body = p.funcBody()
	if len(ft.vlas) > 0 {
		f.body.list = append([]stmt{&declStmt{syms: ft.vlas}}, f.body.list...)
	}
	p.pop()
	for _, l := range f.labels {
		if l.p.line == 0 {
			p.errorAt(at, "label %q used but not defined (a goto out of a nested function is not supported)", l.name)
		}
	}
	p.fn = outer
	p.breakOK, p.continueOK, p.sw = saveB, saveC, saveSw
	return &nestedFuncStmt{f}
}

// withDecls puts a statement after the declarations of its condition.
func withDecls(decls []stmt, s stmt) stmt {
	if len(decls) == 0 {
		return s
	}
	return &block{list: append(decls, s)}
}

// condition parses the controlling expression of if, while, do and for.
func (p *parser) condition() expr {
	mark := len(p.temps)
	x := p.rvalue(p.expr())
	return p.fullExpr(mark, p.scalarCond(x, x.at()))
}

func (p *parser) caseValue(sw *switchStmt) int64 {
	e := p.conditional()
	v, ok := p.foldInt(p.rvalue(e))
	if !ok {
		p.errorAt(e.at(), "case label does not reduce to an integer constant")
	}
	return wrap(sw.x.ty(), v)
}
