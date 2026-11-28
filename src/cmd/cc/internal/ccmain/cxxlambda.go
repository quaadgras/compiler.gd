// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"fmt"
	"strings"
)

// Lambda expressions (doc/gd/cxx.md). A lambda is a class with an
// operator() and a member for each variable it captures; its value is
// an object of that class, initialized with the captured variables.
// The body is parsed as the operator()'s, in the scope the lambda is
// in: a local of an enclosing function that it names becomes a
// capture, found as it is parsed.

type lambdaInfo struct {
	t         *ctype      // the closure's class
	parent    *lambdaInfo // the lambda this one is in, if any
	encFn     *funcDecl   // the function it is in
	classThis *symbol     // the this of the member function it is in, if any
	byRef     bool        // [&]
	byVal     bool        // [=]
	refNames  map[string]bool
	valNames  map[string]bool
	caps      map[*symbol]*capture
	order     []*capture
	closed    bool // the class is complete: it has its captures
}

// A capture is a captured variable: the closure's member f, a pointer
// for a capture by reference, initialized with init where the lambda
// expression is.
type capture struct {
	f    *field
	ref  bool
	init expr
}

// lambdaExpr parses a lambda expression, after its "[".
func (p *parser) lambdaExpr(at pos) expr {
	lam := &lambdaInfo{parent: p.lambda, encFn: p.fn, classThis: p.this, refNames: map[string]bool{}, valNames: map[string]bool{}, caps: map[*symbol]*capture{}}
	if p.lambda != nil {
		lam.classThis = p.lambda.classThis
	}
	type initCap struct {
		name string
		ref  bool
		e    expr
	}
	var inits []initCap
	for !p.accept("]") {
		switch {
		case p.is("=") && (p.isAt(1, ",") || p.isAt(1, "]")):
			p.next()
			lam.byVal = true
		case p.is("&") && (p.isAt(1, ",") || p.isAt(1, "]")):
			p.next()
			lam.byRef = true
		case p.accept("this"):
		case p.is("*") && p.isAt(1, "this"):
			p.pos += 2
		default:
			ref := p.accept("&")
			name := p.ident()
			p.accept("...")
			switch {
			case p.accept("="):
				inits = append(inits, initCap{name, ref, p.assignment()})
			case p.is("{"):
				l := p.braceList()
				if len(l.args) != 1 {
					p.errorAt(at, "a capture's initializer is one expression")
				}
				inits = append(inits, initCap{name, ref, l.args[0]})
			case p.accept("("):
				e := p.expr()
				p.expect(")")
				inits = append(inits, initCap{name, ref, e})
			case ref:
				lam.refNames[name] = true
			default:
				lam.valNames[name] = true
			}
		}
		if !p.accept(",") {
			p.expect("]")
			break
		}
	}
	// The closure's class.
	p.symN++
	tag := "__lambda" + itoa(int64(p.symN))
	rec := &record{tag: tag, id: p.nextRecord(), sc: p.sc}
	t := &ctype{kind: kStruct, rec: rec}
	lam.t = t
	rec.cls = &classInfo{t: t, nsdmi: map[*field]defArg{}, syms: map[string]*symbol{}, vslot: map[*symbol]int{}}
	rec.cls.scope = &scope{vars: map[string]*symbol{}, tags: map[string]*ctype{}, parent: p.sc, kind: skClass, name: tag, rec: rec}

	// The initialized captures are variables to the body, which it
	// captures as any other.
	body := &scope{vars: map[string]*symbol{}, tags: map[string]*ctype{}, parent: p.sc}
	for _, ic := range inits {
		var ct *ctype
		if ic.ref {
			ct = refTo(ic.e.ty(), 1)
		} else {
			e := ic.e
			if pr, ok := e.(*classPR); ok {
				ct = pr.t
			} else {
				ct = valueType(e)
			}
		}
		v := &symbol{name: ic.name, kind: symVar, t: ct.unqual(), p: at, owner: lam.encFn, capInit: ic.e}
		if ic.ref {
			v.t = ct
			lam.refNames[ic.name] = true
		} else {
			lam.valNames[ic.name] = true
		}
		body.vars[ic.name] = v
	}
	if p.is("(") && p.genericParams() {
		p.genericLambda(lam, body, at)
		return p.closure(lam, nil, at)
	}

	// The operator(): its parameters, what follows them, and its body.
	ft := &funcType{proto: true, ret: &ctype{kind: kInt, isAuto: true}}
	if p.accept("(") {
		pt := p.params()
		pt.ret = ft.ret
		pt.proto = true
		ft = pt
	}
	ft.thisQ = qConst
	for {
		switch {
		case p.accept("mutable"):
			ft.thisQ = 0
			continue
		case p.accept("constexpr"), p.accept("noexcept"):
			if p.is("(") && p.peekAt(-1).text == "noexcept" {
				p.skipParens()
			}
			continue
		}
		break
	}
	p.attributes()
	if p.accept("->") {
		p.push()
		for i, name := range ft.names {
			if name != "" && i < len(ft.psyms) && ft.psyms[i] != nil {
				p.sc.vars[name] = ft.psyms[i]
			}
		}
		ft.ret = p.typeName()
		p.pop()
	}
	if !p.is("{") {
		p.errorf("expected a lambda's body")
	}
	mt := &ctype{kind: kFunc, fn: withRecv(ft, t)}
	saveSc := p.sc
	p.sc, p.declIn = rec.cls.scope, nil
	opSym := p.cxxDeclareFunc("operator()", mt, specs{inline: true}, at)
	p.sc = saveSc
	opSym.linkage, opSym.comdat = 0, true
	rec.cls.addMethod(opSym)

	st := p.saveFunc()
	p.sc = body
	p.lambda = lam
	p.defineFunc(opSym, mt.fn, "operator()", at, true)
	if mt.fn.ret.isAuto {
		mt.fn.ret = tVoid
	}
	p.restoreFunc(st)

	return p.closure(lam, opSym, at)
}

// closure completes the class of the lambda lam, whose operator() is
// op (nil for a generic lambda, which has a template of them), and is
// the lambda expression's value.
func (p *parser) closure(lam *lambdaInfo, opSym *symbol, at pos) expr {
	t := lam.t
	rec := t.rec
	// The class is complete: its members are the captures.
	lam.closed = true
	for _, c := range lam.order {
		rec.fields = append(rec.fields, c.f)
	}
	rec.layout()
	if rec.size == 0 {
		rec.size = 1
		rec.cls.empty = true
	}
	p.u.records = append(p.u.records, t)
	p.completeClass(t)
	if len(lam.order) == 0 && opSym != nil {
		p.lambdaToPointer(lam, opSym, at)
	}
	order := lam.order
	return &classPR{node{t, at}, func(dst expr) expr {
		var list []stmt
		for _, c := range order {
			m := &member{node{c.f.t, at}, dst, c.f}
			if c.ref {
				list = append(list, &exprStmt{&assign{node{c.f.t, at}, "=", m, &cast{node{c.f.t, at}, p.addressOf(c.init, at)}, c.f.t}})
			} else if c.f.t.kind == kStruct {
				// A copy that is direct-initialization: an explicit
				// copy constructor makes it too.
				list = append(list, &exprStmt{p.constructAt(m, c.f.t, []expr{c.init}, at, mValue)})
			} else {
				list = append(list, &exprStmt{p.initWith(m, c.init, at)})
			}
		}
		if len(list) == 0 {
			return p.nothing(at)
		}
		return &stmtExpr{node{tVoid, at}, &block{list: list}, nil}
	}}
}

// captureOf is the capture of the variable sym by the lambda lam, made
// if it is not captured yet. self is lam's operator().
func (p *parser) captureOf(lam *lambdaInfo, sym *symbol, at pos) *capture {
	if c := lam.caps[sym]; c != nil {
		return c
	}
	if lam.closed {
		p.errorAt(at, "%q is captured by a lambda with auto parameters, which did not find it in its body", sym.name)
	}
	ref := lam.refNames[sym.name] || lam.byRef && !lam.valNames[sym.name]
	if sym == lam.classThis {
		ref = false // this is captured as the pointer it is
	} else if !ref && !lam.byVal && !lam.valNames[sym.name] {
		p.errorAt(at, "%q is not captured", sym.name)
	}
	// What it is initialized with, where the lambda expression is: the
	// variable, or the enclosing lambda's capture of it.
	var init expr
	switch {
	case sym.capInit != nil:
		init = sym.capInit
	case lam.parent != nil && sym.owner != lam.encFn:
		init = p.capRef(lam.parent, lam.encFn, sym, at)
	default:
		init = p.unref(&varRef{node{sym.t, at}, sym})
	}
	ot := sym.t
	if ot.isRef() {
		ot = ot.elem
	}
	f := &field{name: "__" + itoa(int64(len(lam.order))) + "_" + sym.name, bits: -1}
	if ref {
		f.t = ptrTo(ot)
	} else {
		f.t = ot.unqual()
		p.complete(baseElem(f.t))
	}
	c := &capture{f: f, ref: ref, init: init}
	lam.caps[sym] = c
	lam.order = append(lam.order, c)
	return c
}

// capRef is the captured variable sym as the body of the lambda lam
// names it: the member of the closure, which self's this points to.
func (p *parser) capRef(lam *lambdaInfo, self *funcDecl, sym *symbol, at pos) expr {
	c := p.captureOf(lam, sym, at)
	this := self.params[0]
	obj := &deref{node{this.t.elem, at}, &varRef{node{this.t, at}, this}}
	ft := c.f.t
	if !c.ref && this.t.elem.q&qConst != 0 {
		ft = qualifyDeep(ft, qConst)
	}
	m := &member{node{ft, at}, obj, c.f}
	if c.ref {
		return &deref{node{c.f.t.elem, at}, m}
	}
	return m
}

// captured is the local sym of an enclosing function, named in the body
// of the lambda being parsed.
func (p *parser) captured(sym *symbol, at pos) expr {
	return p.capRef(p.lambda, p.fn, sym, at)
}

// lambdaToPointer gives a lambda that captures nothing its conversion to
// a pointer to a function: a function that calls the operator() of an
// object of the closure's class.
func (p *parser) lambdaToPointer(lam *lambdaInfo, op *symbol, at pos) {
	mft := op.t.fn
	if mft.ret.kind == kStruct || mft.variadic || len(mft.packs) > 0 {
		return
	}
	t := lam.t
	sc := t.rec.cls.scope
	ft := &funcType{proto: true, ret: mft.ret, params: mft.params[1:]}
	for range ft.params {
		ft.names = append(ft.names, "")
	}
	fnT := &ctype{kind: kFunc, fn: ft}
	invoke := &symbol{name: "__invoke", src: "__invoke", kind: symFunc, t: fnT, global: true, p: at, sc: sc, comdat: true}
	p.u.syms = append(p.u.syms, invoke)
	p.synth(invoke, func(ps []*symbol) []stmt {
		obj := p.temporary(t, at)
		var args []expr
		for _, s := range ps {
			args = append(args, p.unref(&varRef{node{s.t, at}, s}))
		}
		call := p.callFunc(op, &varRef{node{obj.t, at}, obj}, args, at)
		if ft.ret.kind == kVoid {
			return []stmt{&exprStmt{p.rvalueVoid(call)}}
		}
		return []stmt{&returnStmt{p.initConvert(call, ft.ret, at, "return")}}
	})
	// operator R (*)(A...)() const.
	pt := ptrTo(fnT)
	cft := &funcType{proto: true, ret: pt, conv: pt, thisQ: qConst}
	save := p.sc
	p.sc, p.declIn = sc, nil
	conv := p.cxxDeclareFunc("operator "+rawType(pt), &ctype{kind: kFunc, fn: withRecv(cft, t)}, specs{inline: true}, at)
	p.sc = save
	conv.linkage, conv.comdat = 0, true
	p.synth(conv, func(ps []*symbol) []stmt {
		invoke.used = true
		return []stmt{&returnStmt{&decay{node{pt, at}, &varRef{node{fnT, at}, invoke}}}}
	})
}

// A generic lambda, one with a parameter that is auto, has an operator()
// for each type it is called with: a member function template of the
// closure's class, with a type parameter for each auto. The template is
// parsed as one that the program wrote,
//
//	template <class __cc_auto0> auto operator()(const __cc_auto0 &x) const { body }
//
// from tokens that are made of the lambda's. The body is parsed when the
// template is instantiated, in the scope the lambda is in; the class is
// complete where the lambda expression is, so the captures are found
// before that, by the names of the body's tokens: every local of the
// enclosing functions that the body names where it may name a variable.
// That is more than the body may capture, if it declares a name again,
// and never less.

// genericParams reports whether the parameters at p.pos, a "(", have an
// auto.
func (p *parser) genericParams() bool {
	depth := 0
	for i := 0; ; i++ {
		t := p.peekAt(i)
		switch {
		case t.kind == tkEOF:
			return false
		case t.kind == tkPunct && t.text == "(":
			depth++
		case t.kind == tkPunct && t.text == ")":
			depth--
			if depth == 0 {
				return false
			}
		case t.kind == tkIdent && t.text == "auto":
			return true
		}
	}
}

// genericLambda parses the rest of a generic lambda, from its
// parameters' "(": the closure gets its captures and the template of
// its operator().
func (p *parser) genericLambda(lam *lambdaInfo, body *scope, at pos) {
	t := lam.t
	rec := t.rec
	// The parameters, what follows them, and the body.
	match := func(i int, open, close string) int {
		depth := 0
		for ; ; i++ {
			tk := p.toks[i]
			switch {
			case tk.kind == tkEOF:
				p.errorAt(at, "unterminated lambda")
			case tk.kind == tkPunct && tk.text == open:
				depth++
			case tk.kind == tkPunct && tk.text == close:
				depth--
				if depth == 0 {
					return i
				}
			}
		}
	}
	po := p.pos
	pc := match(po, "(", ")")
	lb := pc + 1
	mutable := false
	arrow := -1
	for depth := 0; ; lb++ {
		tk := p.toks[lb]
		if tk.kind == tkEOF {
			p.errorAt(at, "expected a lambda's body")
		}
		if tk.kind == tkPunct {
			switch tk.text {
			case "(", "[":
				depth++
			case ")", "]":
				depth--
			}
		}
		if depth == 0 && tk.kind == tkPunct && tk.text == "{" {
			break
		}
		if depth == 0 && tk.kind == tkIdent && tk.text == "mutable" {
			mutable = true
		}
		if depth == 0 && tk.kind == tkPunct && tk.text == "->" && arrow < 0 {
			arrow = lb
		}
	}
	rb := match(lb, "{", "}")

	// The captures.
	save := p.sc
	p.sc = body
	if lam.classThis != nil {
		p.captureOf(lam, lam.classThis, at)
	}
	for i := pc + 1; i < rb; i++ {
		tk := p.toks[i]
		if tk.kind != tkIdent {
			continue
		}
		if prev := p.toks[i-1]; prev.kind == tkPunct && (prev.text == "." || prev.text == "->" || prev.text == "::") {
			continue
		}
		sym := p.lookup(tk.text)
		if sym == nil || sym.kind != symVar || sym.global || sym.owner == nil && sym.capInit == nil || sym == lam.classThis {
			continue
		}
		if lam.caps[sym] != nil {
			continue
		}
		if !(lam.byRef || lam.byVal || lam.refNames[sym.name] || lam.valNames[sym.name]) {
			continue // not captured: an error if the body does use it
		}
		p.captureOf(lam, sym, at)
	}

	// The template's tokens.
	var head strings.Builder
	head.WriteString("template <")
	n := 0
	var params []token
	for i := po; i <= pc; i++ {
		tk := p.toks[i]
		if tk.kind == tkIdent && tk.text == "auto" {
			if n > 0 {
				head.WriteString(", ")
			}
			name := "__cc_auto" + itoa(int64(n))
			fmt.Fprintf(&head, "class %s", name)
			tk.text = name
			n++
		}
		params = append(params, tk)
	}
	head.WriteString("> auto operator()")
	lexed := func(src string) []token {
		toks, err := lex(at.file, src)
		if err != nil {
			p.errorAt(at, "%v", err)
		}
		for len(toks) > 0 && toks[len(toks)-1].kind == tkEOF {
			toks = toks[:len(toks)-1]
		}
		for i := range toks {
			toks[i].line = at.line
		}
		return toks
	}
	synth := lexed(head.String())
	synth = append(synth, params...)
	if !mutable {
		synth = append(synth, lexed("const")...)
	}
	if arrow >= 0 {
		synth = append(synth, p.toks[arrow:lb]...)
	}
	synth = append(synth, p.toks[lb:rb+1]...)
	synth = append(synth, lexed(";")...)
	synth = append(synth, token{kind: tkEOF, file: at.file, line: at.line})
	start := len(p.toks)
	p.toks = append(p.toks, synth...)

	// The template, a member of the closure's class.
	saveCls, saveDeclIn := p.cls, p.declIn
	rec.cls.scope.parent = body
	p.sc, p.cls, p.declIn = rec.cls.scope, t, nil
	p.pos = start
	p.templateDecl()
	p.sc, p.cls, p.declIn = save, saveCls, saveDeclIn
	p.pos = rb + 1
	if op := rec.cls.scope.vars["operator()"]; op != nil && op.tmpl != nil {
		op.tmpl.lam = lam
	} else {
		p.errorAt(at, "internal error: a generic lambda's operator() is no template")
	}
}

// inLambda sets the lambda that the function sym, whose body is about
// to be parsed, is the operator() of, if it is an instance of a generic
// lambda's.
func (p *parser) inLambda(sym *symbol) {
	if sym.inst != nil && sym.inst.tmpl != nil && sym.inst.tmpl.lam != nil {
		p.lambda = sym.inst.tmpl.lam
	}
}
