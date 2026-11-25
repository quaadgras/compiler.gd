// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

// The range-based for statement: for (decl : range) body is
//
//	{
//		auto &&__range = range;
//		for (auto __begin = begin, __end = end; __begin != __end; ++__begin) {
//			decl = *__begin;
//			body
//		}
//	}
//
// where begin and end are the array's bounds, the range's members, or
// functions found by its type.

// rangeForAhead reports whether the for statement whose "(" was just
// read is range-based: a ":" comes before any ";".
func (p *parser) rangeForAhead() bool {
	depth := 0
	for j := 0; ; j++ {
		t := p.peekAt(j)
		if t.kind == tkEOF {
			return false
		}
		if t.kind != tkPunct {
			continue
		}
		switch t.text {
		case "(", "[", "{":
			depth++
		case ")", "]", "}":
			if depth == 0 {
				return false
			}
			depth--
		case ";", "?":
			if depth == 0 {
				return false
			}
		case ":":
			if depth == 0 {
				return true
			}
		}
	}
}

// localOf makes a local variable named name (not declared in any scope)
// initialized with e, of e's type: auto name = e.
func (p *parser) localOf(name string, e expr, at pos) *symbol {
	var t *ctype
	if pr, ok := e.(*classPR); ok {
		t = pr.t.unqual()
	} else {
		t = valueType(e)
	}
	sym := &symbol{name: name, kind: symVar, t: t, p: at, owner: p.fn, defined: true}
	p.fn.locals = append(p.fn.locals, sym)
	mark := len(p.temps)
	p.initVar(sym, e, at)
	p.finishVar(sym, mark, at)
	return sym
}

// rangeFor parses a range-based for statement, after its "(".
func (p *parser) rangeFor(at pos) stmt {
	p.push()
	defer p.pop()
	s := p.declSpecs()
	name, dt, _ := p.declarator(p.baseOr(s))
	p.expect(":")
	var rng expr
	if p.is("{") {
		p.errorAt(at, "a braced list as a for statement's range is not supported yet")
	}
	rng = p.expr()
	p.expect(")")

	// The range: referred to if it is an lvalue, kept if it is not.
	var rangeSym *symbol
	var obj expr
	if p.isLvalueCxx(rng) {
		rt := refTo(rng.ty(), 1)
		rangeSym = &symbol{name: "__range", kind: symVar, t: rt, p: at, owner: p.fn, defined: true}
		p.fn.locals = append(p.fn.locals, rangeSym)
		rangeSym.init = &initializer{scalar: &cast{node{rt, at}, p.addressOf(rng, at)}}
		obj = p.unref(&varRef{node{rt, at}, rangeSym})
	} else {
		rangeSym = p.localOf("__range", rng, at)
		obj = &varRef{node{rangeSym.t, at}, rangeSym}
	}
	ot := obj.ty()
	p.complete(ot)
	var begin, end expr
	switch {
	case ot.kind == kArray:
		begin = p.rvalue(obj)
		end = &binary{node{begin.ty(), at}, "+", begin, p.intc(ot.n, tLong, at)}
	case ot.kind == kStruct && classOf(ot) != nil && classOf(ot).scope.find("begin") != nil:
		begin = p.memberCall(obj, "begin", nil, at)
		end = p.memberCall(obj, "end", nil, at)
	default:
		free := func(fn string) expr {
			a := &adlRef{node: node{tVoid, at}, name: fn}
			if sym := p.lookup(fn); sym != nil && sym.kind == symFunc {
				a.set = funcSet(sym)
			}
			return p.cxxCallArgs(a, []expr{obj}, at)
		}
		begin, end = free("begin"), free("end")
	}
	beginSym := p.localOf("__begin", begin, at)
	endSym := p.localOf("__end", end, at)
	bv := expr(&varRef{node{beginSym.t, at}, beginSym})
	ev := expr(&varRef{node{endSym.t, at}, endSym})

	mark := len(p.temps)
	c := p.rvalue(p.cxxBinary(at, "!=", bv, ev))
	c = p.fullExpr(mark, p.scalarCond(c, at))
	mark = len(p.temps)
	var post expr
	if overloadable(bv.ty()) {
		post = p.overloaded("++", []expr{bv}, at)
		if post == nil {
			p.errorAt(at, "no match for operator++ (operand type is %s)", bv.ty())
		}
	} else {
		post = p.incDecOf(bv, "++", false, at)
	}
	post = p.fullExpr(mark, p.rvalueVoid(post))

	// The declared variable: *__begin, each time.
	mark = len(p.temps)
	var elem expr
	if overloadable(bv.ty()) {
		elem = p.overloaded("*", []expr{bv}, at)
		if elem == nil {
			p.errorAt(at, "no match for operator* (operand type is %s)", bv.ty())
		}
	} else {
		elem = p.derefOf(p.rvalue(bv), at)
	}
	sym := &symbol{name: name, kind: symVar, t: dt, p: at, owner: p.fn, defined: true}
	if hasAuto(dt) {
		sym.t = p.autoType(dt, elem, at)
	}
	p.complete(baseElem(sym.t))
	if name != "" {
		p.declare(sym, at)
	}
	p.fn.locals = append(p.fn.locals, sym)
	p.extending(sym, func() { p.initVar(sym, elem, at) })
	p.finishVar(sym, mark, at)
	body := p.loopBody()

	inner := &block{list: []stmt{&declStmt{syms: append(sym.extended[:len(sym.extended):len(sym.extended)], sym)}, body}}
	loop := &forStmt{c: c, post: post, body: inner}
	return &block{list: []stmt{
		&declStmt{syms: []*symbol{rangeSym}},
		&declStmt{syms: []*symbol{beginSym, endSym}},
		loop,
	}}
}
