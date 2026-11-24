// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import "strings"

// C++ classes (doc/gd/cxx.md): a class is a record; its member
// functions are functions that take this; what C++ does with objects
// (construction, destruction, copies) is made of calls of them, in
// cxxobj.go.

// classInfo is what a class has that a C struct has not.
type classInfo struct {
	scope *scope // its members' names
	t     *ctype

	late  []lateFunc        // member functions defined in the class: parsed when it is complete
	nsdmi map[*field]defArg // default member initializers
	syms  map[string]*symbol

	complete    bool
	userCtor    bool // a constructor is declared
	nontrivCtor bool // default initialization runs code
	nontrivDtor bool // destruction runs code
	nontrivCopy bool // a copy is not a copy of the bytes
}

// A lateFunc is a function whose body is in a class's definition.
type lateFunc struct {
	sym   *symbol
	ft    *funcType
	name  string
	start int // the body's first token
	at    pos
}

// A methodRef is a member function named on an object (x.f, p->f): only
// a call's operand.
type methodRef struct {
	node
	obj expr
	set []*symbol
}

func classOf(t *ctype) *classInfo {
	if t != nil && t.kind == kStruct {
		return t.rec.cls
	}
	return nil
}

// elemClass is the class of t, or of the elements of the array t.
func elemClass(t *ctype) *classInfo { return classOf(baseElem(t)) }

func needsCtor(t *ctype) bool {
	ci := elemClass(t)
	return ci != nil && ci.nontrivCtor
}

func needsDtor(t *ctype) bool {
	ci := elemClass(t)
	return ci != nil && ci.nontrivDtor
}

func needsCopy(t *ctype) bool {
	ci := elemClass(t)
	return ci != nil && ci.nontrivCopy
}

// nontrivialABI reports whether a class is passed and returned as an
// object of the caller's, not as its bytes.
func nontrivialABI(t *ctype) bool {
	ci := classOf(t)
	return ci != nil && (ci.nontrivCopy || ci.nontrivDtor)
}

func (s *scope) findMember(name string, seen map[*scope]bool) *symbol {
	r := s.rec
	if r == nil || r.cls == nil {
		return nil
	}
	if name == r.tag && name != "" {
		// The injected class name.
		if sym := r.cls.syms["{name}"]; sym != nil {
			return sym
		}
		sym := &symbol{name: name, src: name, kind: symTypedef, t: r.cls.t, sc: s}
		r.cls.syms["{name}"] = sym
		return sym
	}
	if r.lookupField(name) != nil {
		if sym := r.cls.syms[name]; sym != nil {
			return sym
		}
		sym := &symbol{name: name, src: name, kind: symField, sc: s}
		r.cls.syms[name] = sym
		return sym
	}
	return nil
}

// isBaseOf reports whether base is a base class of derived (not the
// same class).
func isBaseOf(base, derived *ctype) bool { return false }

// baseAddr is ptr, a pointer to a class, converted to point to its base
// to, if to is one.
func (p *parser) baseAddr(ptr expr, to *ctype, at pos) expr { return ptr }

// derivedAddr is ptr, a pointer to a class, converted to point to the
// class to derived from it, if to is one.
func (p *parser) derivedAddr(ptr expr, to *ctype, at pos) expr { return ptr }

// The function being parsed, and what is kept with it: saved where a
// function is parsed within another (a local class's, an implicit
// member's).
type funcState struct {
	fn                    *funcDecl
	this                  *symbol
	sc                    *scope
	breakOK, continueOK   int
	sw                    *switchStmt
	stmtExprs, condDepth  int
	pendingVLA            []stmt
	temps                 []*tempObj
	extend                *[]*symbol
	staticInit, transTime int
}

func (p *parser) saveFunc() funcState {
	s := funcState{p.fn, p.this, p.sc, p.breakOK, p.continueOK, p.sw, p.stmtExprs, p.condDepth, p.pendingVLA, p.temps, p.extend, p.staticInit, p.translationTime}
	p.fn, p.this, p.breakOK, p.continueOK, p.sw, p.stmtExprs, p.condDepth, p.pendingVLA, p.temps, p.extend = nil, nil, 0, 0, nil, 0, 0, nil, nil, nil
	p.staticInit, p.translationTime = 0, 0
	return s
}

func (p *parser) restoreFunc(s funcState) {
	p.fn, p.this, p.sc, p.breakOK, p.continueOK, p.sw, p.stmtExprs, p.condDepth, p.pendingVLA, p.temps, p.extend = s.fn, s.this, s.sc, s.breakOK, s.continueOK, s.sw, s.stmtExprs, s.condDepth, s.pendingVLA, s.temps, s.extend
	p.staticInit, p.translationTime = s.staticInit, s.transTime
}

// Class definitions.

// beginClass starts the definition of the class t: its scope is the
// current one until its "}".
func (p *parser) beginClass(t *ctype) (saveSc *scope, saveCls *ctype) {
	r := t.rec
	r.cls = &classInfo{t: t, nsdmi: map[*field]defArg{}, syms: map[string]*symbol{}}
	r.cls.scope = &scope{vars: map[string]*symbol{}, tags: map[string]*ctype{}, parent: p.sc, kind: skClass, name: r.tag, rec: r}
	if r.tag == "" {
		r.cls.scope.name = "._" + itoa(int64(r.id))
	}
	saveSc, saveCls = p.sc, p.cls
	p.sc, p.cls = r.cls.scope, t
	return
}

// inBlock reports whether sc is within a function's block.
func inBlock(sc *scope) bool {
	for ; sc != nil; sc = sc.parent {
		if sc.kind == skBlock {
			return true
		}
	}
	return false
}

// classMember parses a member declaration of the class ct, adding its
// non-static data members to fields.
func (p *parser) classMember(ct *ctype, fields *[]*field) {
	ci := ct.rec.cls
	at := p.here()
	if (p.is("public") || p.is("private") || p.is("protected")) && p.isAt(1, ":") {
		p.pos += 2
		return
	}
	if p.is("using") {
		p.usingDecl()
		return
	}
	if p.is("template") {
		p.errorf("member templates are not supported yet")
	}
	if p.is("friend") && (p.isAt(1, "class") || p.isAt(1, "struct")) && p.isAt(3, ";") {
		p.pos += 4 // friend class X;
		return
	}
	s := p.declSpecs()
	if s.noType {
		if p.is(";") {
			p.errorf("expected a member declaration")
		}
		p.memberFunc(ct, s, at)
		return
	}
	if s.t == nil && !s.auto {
		p.errorf("expected a member type")
	}
	if p.accept(";") {
		if s.t != nil && s.t.kind == kStruct && s.t.rec.tag == "" && !s.friend {
			// An anonymous struct or union.
			*fields = append(*fields, &field{t: s.t, bits: -1})
		}
		return
	}
	if s.virtual {
		p.errorAt(at, "virtual functions are not supported yet")
	}
	for {
		f := &field{bits: -1}
		var fa attrs
		name := ""
		t := p.baseOr(s)
		if !p.is(":") {
			name, t, fa = p.declarator(t)
		}
		fa.merge(p.attributes())
		switch {
		case t.kind == kFunc && s.storage != sTypedef:
			if p.declareMember(ct, name, t, s, at) {
				return // defined: no ";"
			}
		case s.storage == sTypedef:
			p.declareType(name, t, at)
		case s.friend:
			p.errorAt(at, "expected a function or a class after friend")
		case s.storage == sStatic || s.constx:
			p.staticMember(name, t, s, at)
		default:
			f.name, f.t = name, t
			if p.accept(":") {
				f.bits = int(p.constInt(p.conditional()))
				if !f.t.isInteger() || f.t.isCap() {
					p.errorf("bit-field %q has an invalid type", f.name)
				}
				if f.bits < 0 || int64(f.bits) > f.t.sizeof()*8 {
					p.errorf("width of bit-field %q exceeds its type", f.name)
				}
			}
			fa.merge(p.attributes())
			f.t = withMode(f.t, max(fa.mode, s.attrs.mode))
			if al := max(s.align, fa.aligned, s.attrs.aligned); al > 0 {
				c := *f.t
				c.align = max(c.align, al)
				f.t = &c
			}
			f.packed = fa.packed || s.attrs.packed
			f.mutable = s.mutable
			if p.is("=") || p.is("{") {
				// A default member initializer: parsed in each
				// constructor that uses it.
				ci.nsdmi[f] = defArg{p.pos, p.sc}
				p.skipInit()
			}
			*fields = append(*fields, f)
		}
		if !p.accept(",") {
			break
		}
	}
	p.expect(";")
}

// skipInit skips a member's initializer: to the "," or ";" that ends it.
func (p *parser) skipInit() {
	depth := 0
	for {
		t := p.peek()
		if t.kind == tkEOF {
			p.errorf("unterminated initializer")
		}
		if t.kind == tkPunct {
			switch t.text {
			case "(", "[", "{":
				depth++
			case ")", "]", "}":
				depth--
			case ",", ";":
				if depth == 0 {
					return
				}
			}
		}
		p.next()
	}
}

// skipBraces skips a balanced {...}.
func (p *parser) skipBraces() {
	p.expect("{")
	depth := 1
	for depth > 0 {
		t := p.next()
		switch {
		case t.kind == tkEOF:
			p.errorf("unbalanced braces")
		case t.kind == tkPunct && t.text == "{":
			depth++
		case t.kind == tkPunct && t.text == "}":
			depth--
		}
	}
}

// skipFuncBody skips a function's body, with a constructor's member
// initializers.
func (p *parser) skipFuncBody() {
	if p.accept(":") {
		for {
			for !p.is("(") && !p.is("{") {
				if p.next().kind == tkEOF {
					p.errorf("unterminated member initializers")
				}
			}
			if p.is("(") {
				p.skipParens()
			} else {
				p.skipBraces()
			}
			if !p.accept(",") {
				break
			}
		}
	}
	p.skipBraces()
}

// staticMember declares a static data member in the current class.
func (p *parser) staticMember(name string, t *ctype, s specs, at pos) {
	if s.constx {
		t = constexprType(t)
	}
	sym := &symbol{name: mangleVar(p.sc, name), src: name, kind: symVar, t: t, storage: sExtern, global: true, linkage: 'e', sc: p.sc, p: at, constx: s.constx}
	if inBlock(p.sc) {
		sym.linkage = 0
	}
	p.declare(sym, at)
	p.u.syms = append(p.u.syms, sym)
	if !p.is("=") && !p.is("{") {
		return
	}
	p.accept("=")
	p.staticInit++
	p.translationTime++
	if hasAuto(t) {
		e := p.initExpr()
		sym.t = constexprType(p.autoType(t, e, at))
		sym.init = &initializer{scalar: p.initConvert(e, sym.t, at, "initializer")}
	} else {
		sym.init = p.initializer(sym)
	}
	p.translationTime--
	p.staticInit--
	sym.defined, sym.storage, sym.comdat = true, sAuto, true
	if sym.t.q&qConst != 0 && sym.t.isArith() && sym.init.scalar != nil && p.constInit(sym.init) {
		sym.constx = true
		p.constexprValue(sym, at)
	}
}

// withRecv is the type of a non-static member function of the class ct
// declared with the function type ft: this is its first parameter.
func withRecv(ft *funcType, ct *ctype) *funcType {
	n := *ft
	n.recv = ptrTo(ct.unqual().qualified(ft.thisQ))
	n.params = append([]*ctype{n.recv}, ft.params...)
	n.names = append([]string{"this"}, ft.names...)
	for len(n.names) < len(n.params) {
		n.names = append(n.names, "")
	}
	if ft.psyms != nil {
		n.psyms = append([]*symbol{nil}, ft.psyms...)
	}
	if ft.defaults != nil {
		n.defaults = append([]defArg{{}}, ft.defaults...)
	}
	n.proto = true
	return &n
}

// memberType is the type of the member function name of the class ct,
// declared with the function type t and the specifiers s.
func (p *parser) memberType(ct *ctype, name string, t *ctype, s specs) *ctype {
	ft := *t.fn
	tag := ct.rec.tag
	switch {
	case name == tag && s.noType:
		ft.ctor = true
		ft.ret = tVoid
	case name == "~"+tag:
		ft.dtor = true
		ft.ret = tVoid
	case strings.HasPrefix(name, "operator "):
		ft.conv = p.convType
		ft.ret = p.convType
	case s.noType:
		p.errorf("expected a type for %q", name)
	}
	static := s.storage == sStatic || name == "operator new" || name == "operator delete" || name == "operator new[]" || name == "operator delete[]"
	if !static {
		return &ctype{kind: kFunc, fn: withRecv(&ft, ct)}
	}
	return &ctype{kind: kFunc, fn: &ft}
}

// memberFunc parses the declaration of a constructor, destructor or
// conversion function in its class ct, after its specifiers.
func (p *parser) memberFunc(ct *ctype, s specs, at pos) {
	name, t, _ := p.declarator(tVoid)
	if t.kind != kFunc {
		p.errorAt(at, "expected a member function")
	}
	if !p.declareMember(ct, name, t, s, at) {
		p.expect(";")
	}
}

// declareMember declares the member function name of ct. It reports
// whether its definition followed, which is kept for later.
func (p *parser) declareMember(ct *ctype, name string, t *ctype, s specs, at pos) bool {
	ci := ct.rec.cls
	if s.friend {
		// A friend is a function of the enclosing namespace.
		p.declTo = p.nsScope()
		sym := p.cxxDeclareFunc(name, t, s, at)
		p.declTo = nil
		if p.is("{") {
			sym.comdat = true
			ci.late = append(ci.late, lateFunc{sym, t.fn, name, p.pos, at})
			p.skipFuncBody()
			return true
		}
		return false
	}
	mt := p.memberType(ct, name, t, s)
	p.declIn = nil
	sym := p.cxxDeclareFunc(name, mt, s, at)
	sym.explicit = s.explicit
	if inBlock(p.sc) {
		sym.linkage = 0
	}
	if p.accept("=") {
		switch {
		case p.accept("default"):
			sym.implicit = true
		case p.accept("delete"):
			sym.deleted = true
		default:
			p.errorf("pure virtual functions are not supported yet")
		}
		return false
	}
	if p.is("{") || p.is(":") {
		if sym.defined {
			p.errorAt(at, "redefinition of %q", name)
		}
		sym.comdat = true
		ci.late = append(ci.late, lateFunc{sym, mt.fn, name, p.pos, at})
		p.skipFuncBody()
		return true
	}
	return false
}

// completeClass finishes the class t, whose members are declared and
// laid out: its implicit members, then the bodies of the functions
// defined in it.
func (p *parser) completeClass(t *ctype) {
	r := t.rec
	ci := r.cls
	sc := ci.scope
	ci.userCtor = sc.vars["{ctor}"] != nil
	userDtor := sc.vars["~"+r.tag]
	userDefault, defaultedDefault, userCopy, userAssign := false, false, false, false
	isSelf := func(pt *ctype) bool {
		if pt.isRef() {
			pt = pt.elem
		}
		return pt.kind == kStruct && pt.rec == r
	}
	for o := sc.vars["{ctor}"]; o != nil; o = o.next {
		ps := o.t.fn.params[1:]
		if len(ps) == 0 {
			if o.implicit {
				defaultedDefault = true
			} else {
				userDefault = true
			}
		}
		if len(ps) == 1 && isSelf(ps[0]) && !o.implicit {
			userCopy = true
		}
		if !o.implicit {
			ci.nontrivCtor = true
		}
	}
	for o := sc.vars["operator="]; o != nil; o = o.next {
		if ps := o.t.fn.params; len(ps) == 2 && isSelf(ps[1]) && !o.implicit {
			userAssign = true
		}
	}
	ci.nontrivCtor = ci.nontrivCtor || userDefault || len(ci.nsdmi) > 0
	ci.nontrivDtor = userDtor != nil && !userDtor.implicit
	ci.nontrivCopy = userCopy || userAssign
	if !r.union {
		for _, f := range r.fields {
			ci.nontrivCtor = ci.nontrivCtor || needsCtor(f.t)
			ci.nontrivDtor = ci.nontrivDtor || needsDtor(f.t)
			ci.nontrivCopy = ci.nontrivCopy || needsCopy(f.t)
		}
	}
	ci.complete = true

	// The implicit members: declared, and defined if they do anything.
	at := pos{p.file, p.here().line}
	self := t.unqual()
	cref := refTo(self.qualified(qConst), 1)
	implicit := func(name string, ft *funcType, trivial bool) *symbol {
		var sym *symbol
		key := name
		if ft.ctor {
			key = "{ctor}"
		}
		mt := &ctype{kind: kFunc, fn: withRecv(ft, self)}
		for o := sc.vars[key]; o != nil; o = o.next {
			if o.implicit && sameParams(o.t.fn, mt.fn) {
				sym = o // declared = default
			}
		}
		if sym == nil {
			save := p.sc
			p.sc, p.declIn = sc, nil
			sym = p.cxxDeclareFunc(name, mt, specs{inline: true}, at)
			p.sc = save
			sym.implicit = true
		}
		if inBlock(sc) {
			sym.linkage = 0
		}
		sym.comdat = true
		sym.trivial = trivial
		return sym
	}
	var todo []func()
	if !ci.userCtor || defaultedDefault {
		sym := implicit(r.tag, &funcType{ret: tVoid, proto: true, ctor: true}, !ci.nontrivCtor)
		if !sym.trivial && !sym.defined {
			todo = append(todo, func() { p.implicitCtor(sym, t) })
		}
	}
	if !userCopy {
		sym := implicit(r.tag, &funcType{ret: tVoid, proto: true, ctor: true, params: []*ctype{cref}, names: []string{"o"}}, !ci.nontrivCopy)
		if !sym.trivial && !sym.defined {
			todo = append(todo, func() { p.implicitCopy(sym, t, false) })
		}
	}
	if !userAssign {
		sym := implicit("operator=", &funcType{ret: refTo(self, 1), proto: true, params: []*ctype{cref}, names: []string{"o"}}, !ci.nontrivCopy)
		if !sym.trivial && !sym.defined {
			todo = append(todo, func() { p.implicitCopy(sym, t, true) })
		}
	}
	if userDtor == nil || userDtor.implicit {
		sym := implicit("~"+r.tag, &funcType{ret: tVoid, proto: true, dtor: true}, !ci.nontrivDtor)
		if !sym.trivial && !sym.defined {
			todo = append(todo, func() { p.implicitDtor(sym, t) })
		}
	}
	for _, f := range todo {
		f()
	}

	// The functions defined in the class, once the outermost class
	// is complete: they may use any member of it.
	if outer := classOf(p.cls); outer != nil && !outer.complete {
		outer.late = append(outer.late, ci.late...)
		ci.late = nil
		return
	}
	late := ci.late
	ci.late = nil
	for _, lf := range late {
		save, saveCls := p.pos, p.cls
		st := p.saveFunc()
		p.pos, p.cls = lf.start, nil
		p.sc = lf.sym.sc
		if lf.sym.sc.kind != skClass {
			p.sc = sc // a friend: in the class's scope
		}
		p.defineFunc(lf.sym, lf.ft, lf.name, lf.at, true)
		p.restoreFunc(st)
		p.pos, p.cls = save, saveCls
	}
}

// synth defines the function sym with the body that body makes, given
// its parameters.
func (p *parser) synth(sym *symbol, body func(params []*symbol) []stmt) {
	st := p.saveFunc()
	ft := sym.t.fn
	f := &funcDecl{sym: sym, labels: map[string]*label{}, inline: true}
	sym.fn, sym.defined = f, true
	p.fn = f
	p.sc = sym.sc
	p.push()
	for i, pt := range ft.params {
		ps := &symbol{name: "p" + itoa(int64(i)), kind: symVar, t: pt, p: sym.p, isParam: true}
		f.params = append(f.params, ps)
	}
	if ft.recv != nil {
		p.this = f.params[0]
	}
	f.body = &block{list: body(f.params)}
	p.pop()
	p.u.funcs = append(p.u.funcs, f)
	p.restoreFunc(st)
}

// implicitCtor defines a class's implicit default constructor.
func (p *parser) implicitCtor(sym *symbol, t *ctype) {
	p.synth(sym, func(ps []*symbol) []stmt {
		return p.initMembers(t, nil, sym.p)
	})
}

// implicitCopy defines a class's implicit copy constructor, or copy
// assignment operator: member by member.
func (p *parser) implicitCopy(sym *symbol, t *ctype, isAssign bool) {
	p.synth(sym, func(ps []*symbol) []stmt {
		at := sym.p
		this := p.thisObject(at)
		other := p.unref(&varRef{node{ps[1].t, at}, ps[1]})
		var out []stmt
		for _, f := range t.rec.fields {
			dst := &member{node{f.t, at}, this, f}
			src := &member{node{qualifyDeep(f.t, qConst), at}, other, f}
			switch {
			case f.bits >= 0:
				out = append(out, &exprStmt{&assign{node{f.t.unqual(), at}, "=", dst, &cast{node{f.t.unqual(), at}, src}, f.t}})
			case !needsCopy(f.t):
				out = append(out, &exprStmt{p.copyBytes(dst, src, at)})
			case isAssign:
				out = append(out, p.eachElem(dst, src, at, func(d, s expr) expr { return p.rvalueVoid(p.classAssign("=", d, s, at)) }))
			default:
				out = append(out, p.eachElem(dst, src, at, func(d, s expr) expr { return p.constructAt(d, d.ty(), []expr{s}, at, 0) }))
			}
		}
		if isAssign {
			out = append(out, &returnStmt{&cast{node{sym.t.fn.ret, at}, &varRef{node{p.this.t, at}, p.this}}})
		}
		return out
	})
}

// implicitDtor defines a class's implicit destructor.
func (p *parser) implicitDtor(sym *symbol, t *ctype) {
	p.synth(sym, func(ps []*symbol) []stmt {
		return p.destroyMembers(t, sym.p)
	})
}

// A memInit is a member's initializer in a constructor.
type memInit struct {
	args    []expr
	braceAt int  // the "{" of a braced initializer, to parse
	dflt    bool // default initialization
	at      pos
}

// initMembers are the statements that initialize the members of *this
// in a constructor: inits are its member initializers.
func (p *parser) initMembers(t *ctype, inits map[*field]memInit, at pos) []stmt {
	ci := t.rec.cls
	var out []stmt
	for _, f := range t.rec.fields {
		in, ok := inits[f]
		if !ok && t.rec.union {
			continue
		}
		if !ok {
			d, has := ci.nsdmi[f]
			switch {
			case has:
				// The default member initializer, parsed here.
				save, saveSc := p.pos, p.sc
				p.pos, p.sc = d.start, ci.scope
				p.push()
				in = memInit{at: at}
				if p.accept("=") && !p.is("{") {
					in.args = []expr{p.initExpr()}
				} else {
					in.braceAt = p.pos
				}
				s := p.initMember(f, in, at)
				p.pop()
				p.pos, p.sc = save, saveSc
				out = append(out, s)
				continue
			case needsCtor(f.t):
				in = memInit{at: at, dflt: true}
			default:
				continue
			}
		}
		out = append(out, p.initMember(f, in, at))
	}
	return out
}

// initMember is the statement that initializes the member f of *this.
func (p *parser) initMember(f *field, in memInit, at pos) stmt {
	mark := len(p.temps)
	this := p.thisObject(at)
	dst := expr(&member{node{f.t, at}, this, f})
	t := f.t
	var e expr
	switch {
	case in.braceAt != 0:
		save := p.pos
		p.pos = in.braceAt
		ci := elemClass(t)
		switch {
		case t.isScalar():
			p.next()
			if p.accept("}") {
				e = p.zeroObject(dst, at)
			} else {
				v := p.initExpr()
				p.expect("}")
				e = p.initScalar(dst, v, at)
			}
		case ci == nil || t.kind == kArray || !ci.userCtor:
			// An aggregate's: as a compound literal's, copied.
			if needsCopy(t) {
				p.errorAt(at, "braces for an aggregate with members that are copied by constructors are not supported yet")
			}
			e = p.copyBytes(dst, p.compoundLiteral(t.unqual(), at), at)
		default:
			p.next()
			var args []expr
			for !p.is("}") {
				args = append(args, p.assignment())
				if !p.accept(",") {
					break
				}
			}
			p.expect("}")
			e = p.constructAt(dst, t, args, at, mValue)
		}
		p.pos = save
	case t.kind == kStruct:
		mode := mValue
		if in.dflt {
			mode = 0
		}
		e = p.constructAt(dst, t, in.args, at, mode)
	case t.kind == kArray:
		if in.dflt && needsCtor(t) {
			return p.eachElem(dst, nil, at, func(d, _ expr) expr { return p.constructAt(d, d.ty(), nil, at, 0) })
		}
		if len(in.args) != 0 {
			p.errorAt(at, "an array member is initialized with braces")
		}
		e = p.zeroObject(dst, at)
	case len(in.args) == 0:
		e = p.zeroObject(dst, at)
	case len(in.args) == 1:
		e = p.initScalar(dst, in.args[0], at)
	default:
		p.errorAt(at, "too many initializers for the member %q", f.name)
	}
	return &exprStmt{p.fullExpr(mark, e)}
}

// destroyMembers are the statements that destroy the members of *this,
// the last first.
func (p *parser) destroyMembers(t *ctype, at pos) []stmt {
	var out []stmt
	if t.rec.union {
		return nil
	}
	for i := len(t.rec.fields) - 1; i >= 0; i-- {
		f := t.rec.fields[i]
		if !needsDtor(f.t) {
			continue
		}
		if d := p.destroy(&member{node{f.t, at}, p.thisObject(at), f}, at); d != nil {
			out = append(out, d)
		}
	}
	return out
}

// ctorBody parses the body of the constructor or destructor sym, with a
// constructor's member initializers: what C++ adds around it is in the
// block it returns.
func (p *parser) ctorBody(sym *symbol, at pos) *block {
	ft := sym.t.fn
	ct := ft.recv.elem.unqual()
	r := ct.rec
	if ft.dtor {
		body := p.funcBody()
		if members := p.destroyMembers(ct, at); len(members) > 0 {
			// When the body is left, however it is.
			return &block{list: []stmt{&deferStmt{&block{list: members}}, body}}
		}
		return body
	}
	inits := map[*field]memInit{}
	var pre []stmt
	delegated := false
	if p.accept(":") {
		for {
			mat := p.here()
			name := p.ident()
			in := memInit{at: mat}
			if p.is("{") {
				in.braceAt = p.pos
				p.skipBraces()
			} else {
				p.expect("(")
				in.args = p.callArgs()
			}
			if name == r.tag {
				// A delegating constructor.
				if in.braceAt != 0 {
					p.errorAt(mat, "a delegating constructor's arguments are in parentheses here")
				}
				mark := len(p.temps)
				pre = append(pre, &exprStmt{p.fullExpr(mark, p.constructAt(p.thisObject(mat), ct, in.args, mat, mValue))})
				delegated = true
			} else {
				path := r.lookupField(name)
				if len(path) != 1 {
					p.errorAt(mat, "%s has no member %q to initialize", ct, name)
				}
				inits[path[0]] = in
			}
			if !p.accept(",") {
				break
			}
		}
	}
	if !delegated {
		pre = append(pre, p.initMembers(ct, inits, at)...)
	}
	body := p.funcBody()
	if len(pre) == 0 {
		return body
	}
	return &block{list: append(pre, body)}
}

// specialMember parses a declaration with no type specifier: a
// constructor, destructor or conversion function, defined outside its
// class.
func (p *parser) specialMember(s specs, at pos) {
	name, t, a := p.declarator(tVoid)
	if t.kind != kFunc || p.declIn == nil || p.declIn.kind != skClass {
		p.errorAt(at, "expected a type")
	}
	if !p.is("{") && !p.is(":") {
		p.errorAt(at, "expected the definition of %s::%s", p.declIn.name, name)
	}
	s.noType = true
	p.funcDef(name, t, s, a, at)
}

func (p *parser) thisObject(at pos) expr {
	if p.this == nil {
		p.errorAt(at, "invalid use of a member outside a non-static member function")
	}
	return &deref{node{p.this.t.elem, at}, &varRef{node{p.this.t, at}, p.this}}
}

// fieldRef is the non-static data member sym named by itself (or
// qualified by the scope in): this's.
func (p *parser) fieldRef(sym *symbol, in *scope, at pos) expr {
	return p.memberOf(p.thisObject(at), sym.src, at)
}

// cxxMember parses a member access in C++, after its "." or "->".
func (p *parser) cxxMember(x expr, arrow bool, at pos) expr {
	if pr, ok := x.(*classPR); ok {
		x = p.materializePR(pr)
	}
	if arrow {
		for x.ty().kind == kStruct {
			e := p.memberCall(x, "operator->", nil, at)
			if e == nil {
				p.errorAt(at, "%s has no operator->", x.ty())
			}
			x = e
			if pr, ok := x.(*classPR); ok {
				x = p.materializePR(pr)
			}
		}
		b := p.rvalue(x)
		if !b.ty().isPtr() {
			p.errorAt(at, "invalid type argument of -> (have %s)", b.ty())
		}
		x = p.derefOf(b, at)
	}
	t := x.ty()
	if p.is("~") {
		// x.~T(): the destructor, or nothing for a type that has none.
		p.next()
		if _, n := p.cxxTypeAt(0); n > 0 {
			p.pos += n
		} else {
			p.ident()
		}
		p.expect("(")
		p.expect(")")
		if d := p.destroy(x, at); d != nil {
			return &stmtExpr{node{tVoid, at}, &block{list: []stmt{d}}, nil}
		}
		return p.nothing(at)
	}
	if t.kind != kStruct {
		p.errorAt(at, "request for a member in something not a class (%s)", t)
	}
	p.requireComplete(t, at)
	var in *scope
	if p.peek().kind == tkIdent && p.isAt(1, "::") {
		var j int
		in, j = p.nested(0)
		p.pos += j
	}
	var name string
	if p.accept("operator") {
		name = p.operatorName()
	} else {
		name = p.ident()
	}
	if in == nil && t.rec.lookupField(name) != nil {
		return p.memberOf(x, name, at)
	}
	sc := in
	if sc == nil {
		if t.rec.cls == nil {
			p.errorAt(at, "%s has no member named %q", t, name)
		}
		sc = t.rec.cls.scope
	}
	sym := sc.find(name)
	if sym == nil {
		p.errorAt(at, "%s has no member named %q", t, name)
	}
	switch sym.kind {
	case symFunc:
		return &methodRef{node{sym.t, at}, x, funcSet(sym)}
	case symEnumConst:
		return p.intc(sym.val, sym.t, at)
	case symVar:
		sym.used = true
		return p.unref(&varRef{node{sym.t, at}, sym})
	case symField:
		return p.memberOf(x, name, at)
	}
	p.errorAt(at, "%q is not a member that an object has", name)
	return nil
}

// memberCall is the call of x's member function name with args: nil if
// x's class has none.
func (p *parser) memberCall(x expr, name string, args []expr, at pos) expr {
	ci := classOf(x.ty())
	if ci == nil {
		return nil
	}
	head := ci.scope.find(name)
	if head == nil || head.kind != symFunc {
		return nil
	}
	if pr, ok := x.(*classPR); ok {
		x = p.materializePR(pr)
	}
	sym := p.resolve(funcSet(head), x, args, at, name, false)
	if sym.t.fn.recv == nil {
		return p.callFunc(sym, nil, args, at)
	}
	return p.callFunc(sym, x, args, at)
}
