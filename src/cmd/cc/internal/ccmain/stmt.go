// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

// Statements (C23 6.8).

// blockBody parses a compound statement, from its "{".
func (p *parser) blockBody() *block {
	p.expect("{")
	p.push()
	b := &block{}
	for !p.accept("}") {
		if p.peek().kind == tkEOF {
			p.errorf("expected \"}\" at end of input")
		}
		b.list = append(b.list, p.blockItem()...)
	}
	p.pop()
	return b
}

// blockItem parses a declaration or a statement.
func (p *parser) blockItem() []stmt {
	if p.is("_Static_assert") || p.is("static_assert") {
		p.staticAssert()
		return nil
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
		return nil
	}
	var syms []*symbol
	var out []stmt
	for {
		name, t, a := p.declarator(p.baseOr(s))
		a.merge(s.attrs)
		if name == "" {
			p.errorf("expected a declarator")
		}
		sym := p.declarator2(name, t, s, a, at)
		if len(p.pendingVLA) > 0 {
			if len(syms) > 0 {
				out = append(out, &declStmt{syms: syms})
				syms = nil
			}
			out = append(out, p.pendingVLA...)
			p.pendingVLA = nil
		}
		if sym != nil && sym.kind == symVar && !sym.global {
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
	at := p.here()
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
			p.expect("(")
			p.push()
			c := p.condition()
			p.expect(")")
			then := p.subStatement()
			var els stmt
			if p.accept("else") {
				els = p.subStatement()
			}
			p.pop()
			return &ifStmt{c, then, els}
		case "while":
			p.next()
			p.expect("(")
			p.push()
			c := p.condition()
			p.expect(")")
			body := p.loopBody()
			p.pop()
			return &whileStmt{c, body}
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
					f.init = &exprStmt{p.rvalueVoid(p.expr())}
				}
				p.expect(";")
			}
			if !p.is(";") {
				f.c = p.condition()
			}
			p.expect(";")
			if !p.is(")") {
				f.post = p.rvalueVoid(p.expr())
			}
			p.expect(")")
			f.body = p.loopBody()
			p.pop()
			return f
		case "switch":
			p.next()
			p.expect("(")
			p.push()
			x := p.rvalue(p.expr())
			if !x.ty().isInteger() {
				p.errorAt(at, "switch quantity not an integer")
			}
			x = p.convert(x, promote(x.ty()))
			p.expect(")")
			sw := &switchStmt{x: x}
			save := p.sw
			p.sw = sw
			p.breakOK++
			sw.body = p.subStatement()
			p.breakOK--
			p.sw = save
			p.pop()
			return sw
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
			p.expect(";")
			return &breakStmt{}
		case "continue":
			p.next()
			if p.continueOK == 0 {
				p.errorAt(at, "continue statement not within a loop")
			}
			p.expect(";")
			return &continueStmt{}
		case "return":
			p.next()
			rt := p.fn.sym.t.fn.ret
			if p.accept(";") {
				return &returnStmt{}
			}
			x := p.expr()
			p.expect(";")
			if rt.kind == kVoid {
				x = p.rvalueVoid(x)
				if x.ty().kind != kVoid {
					// GCC: a warning.
					return &block{list: []stmt{&exprStmt{x}, &returnStmt{}}}
				}
				return &block{list: []stmt{&exprStmt{x}, &returnStmt{}}}
			}
			return &returnStmt{p.assignConvert(p.rvalue(x), rt, at, "return")}
		case "goto":
			p.next()
			if p.is("*") {
				p.errorAt(at, "computed goto is not supported")
			}
			name := p.ident()
			p.expect(";")
			p.fn.hasGoto = true
			return &gotoStmt{p.label(name)}
		case "asm", "__asm__", "__asm":
			p.errorAt(at, "inline assembly is not supported")
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
	x := p.expr()
	p.expect(";")
	return &exprStmt{p.rvalueVoid(x)}
}

// labeledBody parses what follows a label: a statement, or (C23) a
// declaration or nothing before "}".
func (p *parser) labeledBody() stmt {
	if p.is("}") {
		return &emptyStmt{}
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
	l := p.fn.labels[name]
	if l == nil {
		l = &label{name: name}
		p.fn.labels[name] = l
	}
	return l
}

// condition parses the controlling expression of if, while, do and for.
func (p *parser) condition() expr {
	x := p.rvalue(p.expr())
	return p.scalarCond(x, x.at())
}

func (p *parser) caseValue(sw *switchStmt) int64 {
	e := p.conditional()
	v, ok := p.foldInt(p.rvalue(e))
	if !ok {
		p.errorAt(e.at(), "case label does not reduce to an integer constant")
	}
	return wrap(sw.x.ty(), v)
}
