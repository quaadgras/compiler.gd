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

	bases   []*baseClass
	primary *baseClass // the base whose table of virtual functions the class extends
	methods []*symbol  // its member functions, in order
	poly    bool       // has virtual functions
	empty   bool       // has no data: takes no room as a base
	vfuncs  []*symbol  // its table of virtual functions (nil: pure)
	vslot   map[*symbol]int
	vtable  *symbol
	delDtor *symbol // the destructor that also frees the object

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
	obj    expr
	set    []*symbol
	direct bool // its name is qualified: not a virtual call
	targs  []targ
}

func (ci *classInfo) addMethod(sym *symbol) {
	for _, m := range ci.methods {
		if m == sym {
			return
		}
	}
	ci.methods = append(ci.methods, sym)
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
		if r.inst != nil {
			sym.instOf = r.inst.tmpl // with arguments, the template
		}
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
	for _, b := range r.cls.bases {
		if bc := classOf(b.t); bc != nil {
			if v := bc.scope.findIn(name, seen); v != nil {
				return v
			}
		}
	}
	return nil
}

// A baseClass is a direct base of a class: f is its subobject, nil for
// an empty base, which takes no room.
type baseClass struct {
	t *ctype
	f *field
}

// baseOffset is where the subobject of the class base is in an object
// of the class derived: ok is false if it is not a base.
func baseOffset(base, derived *record) (off int64, ok bool) {
	if derived.cls == nil {
		return 0, false
	}
	for _, b := range derived.cls.bases {
		o := int64(0)
		if b.f != nil {
			o = b.f.off
		}
		if b.t.rec == base {
			return o, true
		}
		if bo, ok := baseOffset(base, b.t.rec); ok {
			return o + bo, true
		}
	}
	return 0, false
}

// isBaseOf reports whether base is a base class of derived (not the
// same class).
func isBaseOf(base, derived *ctype) bool {
	if base.kind != kStruct || derived.kind != kStruct || base.rec == derived.rec {
		return false
	}
	_, ok := baseOffset(base.rec, derived.rec)
	return ok
}

// offsetPtr is ptr moved by off bytes, as a pointer to the class to
// (with its pointee's qualifiers): a null pointer stays null, unless
// ptr cannot be one (nonNull).
func (p *parser) offsetPtr(ptr expr, off int64, to *ctype, nonNull bool, at pos) expr {
	pt := ptrTo(to.unqual().qualified(ptr.ty().elem.q))
	if off == 0 {
		return &cast{node{pt, at}, ptr}
	}
	move := func(x expr) expr {
		bytes := &cast{node{ptrTo(tChar), at}, x}
		return &cast{node{pt, at}, &binary{node{ptrTo(tChar), at}, "+", bytes, p.intc(off, tLong, at)}}
	}
	if nonNull {
		return move(ptr)
	}
	var pre []stmt
	if !noEffects(ptr) {
		tmp := p.newLocalOrStatic("ptr", ptr.ty().unqual(), at)
		tv := &varRef{node{tmp.t, at}, tmp}
		pre = []stmt{&exprStmt{&assign{node{tmp.t, at}, "=", tv, ptr, tmp.t}}}
		ptr = tv
	}
	var e expr = &cond{node{pt, at}, ptr, move(ptr), &intConst{node{pt, at}, 0}}
	if pre != nil {
		e = &seqExpr{node{pt, at}, pre, e}
	}
	return e
}

// isNonNull reports whether the pointer ptr is an object's address.
func isNonNull(ptr expr) bool {
	switch e := ptr.(type) {
	case *addrOf:
		return true
	case *cast:
		return isNonNull(e.x)
	case *seqExpr:
		return isNonNull(e.x)
	}
	return false
}

// baseAddr is ptr, a pointer to a class, converted to point to its base
// to, if to is one.
func (p *parser) baseAddr(ptr expr, to *ctype, at pos) expr {
	pt := ptr.ty()
	if !pt.isPtr() || pt.elem.kind != kStruct || to.kind != kStruct || pt.elem.rec == to.rec {
		return ptr
	}
	off, ok := baseOffset(to.rec, pt.elem.rec)
	if !ok {
		return ptr
	}
	return p.offsetPtr(ptr, off, to, isNonNull(ptr), at)
}

// derivedAddr is ptr, a pointer to a class, converted to point to the
// class to derived from it, if to is one.
func (p *parser) derivedAddr(ptr expr, to *ctype, at pos) expr {
	pt := ptr.ty()
	if !pt.isPtr() || pt.elem.kind != kStruct || to.kind != kStruct || pt.elem.rec == to.rec {
		return ptr
	}
	off, ok := baseOffset(pt.elem.rec, to.rec)
	if !ok {
		return ptr
	}
	return p.offsetPtr(ptr, -off, to, isNonNull(ptr), at)
}

// baseObject is e, an object of a class, as its base to, if to is one.
func (p *parser) baseObject(e expr, to *ctype, at pos) expr {
	if e.ty().kind != kStruct || !isBaseOf(to, e.ty()) {
		return e
	}
	bt := to.unqual().qualified(e.ty().q)
	return &deref{node{bt, at}, p.offsetPtr(p.addressOf(e, at), mustOffset(to.rec, e.ty().rec), to, true, at)}
}

func mustOffset(base, derived *record) int64 {
	off, _ := baseOffset(base, derived)
	return off
}

// parseBases parses a class's base clause, after its ":".
func (p *parser) parseBases() []*ctype {
	var bases []*ctype
	for {
		p.attributes()
		for p.is("virtual") || p.is("public") || p.is("private") || p.is("protected") {
			if p.next().text == "virtual" {
				p.errorf("virtual base classes are not supported")
			}
		}
		sym, n := p.cxxTypeAt(0)
		if sym == nil || sym.t.kind != kStruct {
			p.errorf("expected a base class's name, found %s", p.peek())
		}
		p.pos += n
		p.requireComplete(sym.t, p.here())
		bases = append(bases, sym.t.unqual())
		if !p.accept(",") {
			return bases
		}
	}
}

// fnPtrType is the type of the entries of a table of virtual functions.
var fnPtrType = ptrTo(&ctype{kind: kFunc, fn: &funcType{ret: tVoid, proto: true}})

// leadingFields are the fields a class's object begins with, before its
// own members: its bases' subobjects, or the pointer to its table of
// virtual functions if no base has one.
func (p *parser) leadingFields(t *ctype, own []*field) []*field {
	ci := t.rec.cls
	for _, m := range ci.methods {
		ci.poly = ci.poly || m.virtual
	}
	for _, b := range ci.bases {
		bc := classOf(b.t)
		if bc != nil && bc.poly {
			ci.poly = true
			if ci.primary == nil {
				ci.primary = b
			}
		}
	}
	var out []*field
	if ci.poly && ci.primary == nil {
		out = append(out, &field{name: "__vptr", t: ptrTo(fnPtrType), bits: -1, vptr: true})
	}
	add := func(b *baseClass) {
		if bc := classOf(b.t); bc != nil && bc.empty {
			return
		}
		b.f = &field{t: b.t, bits: -1, base: true}
		out = append(out, b.f)
	}
	if ci.primary != nil {
		add(ci.primary)
	}
	for _, b := range ci.bases {
		if b == ci.primary {
			continue
		}
		if bc := classOf(b.t); bc != nil && bc.poly {
			p.errorf("a second base class with virtual functions (%s) is not supported yet", b.t)
		}
		add(b)
	}
	ci.empty = len(out) == 0 && len(own) == 0
	return out
}

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
	lambda                *lambdaInfo
}

func (p *parser) saveFunc() funcState {
	s := funcState{p.fn, p.this, p.sc, p.breakOK, p.continueOK, p.sw, p.stmtExprs, p.condDepth, p.pendingVLA, p.temps, p.extend, p.staticInit, p.translationTime, p.lambda}
	p.lambda = nil
	p.fn, p.this, p.breakOK, p.continueOK, p.sw, p.stmtExprs, p.condDepth, p.pendingVLA, p.temps, p.extend = nil, nil, 0, 0, nil, 0, 0, nil, nil, nil
	p.staticInit, p.translationTime = 0, 0
	return s
}

func (p *parser) restoreFunc(s funcState) {
	p.fn, p.this, p.sc, p.breakOK, p.continueOK, p.sw, p.stmtExprs, p.condDepth, p.pendingVLA, p.temps, p.extend = s.fn, s.this, s.sc, s.breakOK, s.continueOK, s.sw, s.stmtExprs, s.condDepth, s.pendingVLA, s.temps, s.extend
	p.staticInit, p.translationTime = s.staticInit, s.transTime
	p.lambda = s.lambda
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
		p.templateDecl()
		return
	}
	if p.is("friend") && (p.isAt(1, "class") || p.isAt(1, "struct") || p.isAt(1, "union")) {
		// friend class X; friend class X<T>;: access is not checked.
		for !p.accept(";") {
			if p.next().kind == tkEOF {
				p.errorf("unterminated friend declaration")
			}
		}
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
			p.complete(baseElem(f.t))
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
	sym.comdat = inInstance(p.sc)
	p.declare(sym, at)
	p.u.syms = append(p.u.syms, sym)
	if !p.is("=") && !p.is("{") {
		return
	}
	if t.kind == kStruct {
		// An object of a class: constructed when the program starts.
		p.complete(t)
		p.staticInit++
		done := p.classInit(sym, s, at)
		p.staticInit--
		if done {
			sym.defined, sym.storage, sym.comdat = true, sAuto, true
			return
		}
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
	if ft.packs != nil {
		n.packs = nil
		for _, pk := range ft.packs {
			pk.start++ // after this
			n.packs = append(n.packs, pk)
		}
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
	static := s.storage == sStatic || name == "operatornew" || name == "operatordelete" || name == "operatornew[]" || name == "operatordelete[]"
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
	sym.virtual = sym.virtual || s.virtual
	ci.addMethod(sym)
	if inBlock(p.sc) {
		sym.linkage = 0
	}
	if p.accept("=") {
		switch {
		case p.accept("default"):
			sym.implicit = true
		case p.accept("delete"):
			sym.deleted = true
		case p.peek().kind == tkInt && p.peek().ival == 0:
			p.next()
			sym.pure, sym.virtual = true, true
		default:
			p.errorf("expected default, delete or 0")
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
		if o.tmpl != nil {
			continue
		}
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
	}
	for o := sc.vars["operator="]; o != nil; o = o.next {
		if ps := o.t.fn.params; o.tmpl == nil && len(ps) == 2 && isSelf(ps[1]) && !o.implicit {
			userAssign = true
		}
	}
	ci.nontrivCtor = ci.nontrivCtor || userDefault || len(ci.nsdmi) > 0 || ci.poly
	ci.nontrivDtor = userDtor != nil && !userDtor.implicit
	ci.nontrivCopy = userCopy || userAssign || ci.poly
	if !r.union {
		for _, f := range r.fields {
			ci.nontrivCtor = ci.nontrivCtor || needsCtor(f.t)
			ci.nontrivDtor = ci.nontrivDtor || needsDtor(f.t)
			ci.nontrivCopy = ci.nontrivCopy || needsCopy(f.t)
		}
	}
	for _, b := range ci.bases {
		ci.nontrivCtor = ci.nontrivCtor || needsCtor(b.t)
		ci.nontrivDtor = ci.nontrivDtor || needsDtor(b.t)
		ci.nontrivCopy = ci.nontrivCopy || needsCopy(b.t)
		if bc := classOf(b.t); bc != nil {
			for _, v := range bc.vfuncs {
				if v.t.fn.dtor {
					// An implicit destructor overrides a virtual one.
					ci.nontrivDtor = true
				}
			}
		}
	}
	if userDtor != nil && userDtor.virtual {
		ci.nontrivDtor = true
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
			if o.tmpl == nil && o.implicit && sameParams(o.t.fn, mt.fn) {
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
		ci.addMethod(sym)
		return sym
	}
	// A class template's instance defines its members when they are
	// used, the implicit ones too.
	lazy := true
	var todo []*symbol
	later := func(sym *symbol, f func()) {
		if !sym.trivial && !sym.defined {
			sym.synth = f
			todo = append(todo, sym)
		}
	}
	if !ci.userCtor || defaultedDefault {
		sym := implicit(r.tag, &funcType{ret: tVoid, proto: true, ctor: true}, !ci.nontrivCtor)
		later(sym, func() { p.implicitCtor(sym, t) })
	}
	if !userCopy {
		sym := implicit(r.tag, &funcType{ret: tVoid, proto: true, ctor: true, params: []*ctype{cref}, names: []string{"o"}}, !ci.nontrivCopy)
		later(sym, func() { p.implicitCopy(sym, t, false) })
	}
	if !userAssign {
		sym := implicit("operator=", &funcType{ret: refTo(self, 1), proto: true, params: []*ctype{cref}, names: []string{"o"}}, !ci.nontrivCopy)
		later(sym, func() { p.implicitCopy(sym, t, true) })
	}
	if userDtor == nil || userDtor.implicit {
		sym := implicit("~"+r.tag, &funcType{ret: tVoid, proto: true, dtor: true}, !ci.nontrivDtor)
		later(sym, func() { p.implicitDtor(sym, t) })
	}
	p.buildVtable(t, at)
	if !lazy {
		for _, sym := range todo {
			f := sym.synth
			sym.synth = nil
			f()
		}
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
	if lazy {
		for _, lf := range late {
			lf := lf
			lf.sym.late = &lf
			if lf.sym.sc.kind != skClass {
				lf.sym.lateSc = sc // a friend: in the class's scope
			}
			if lf.sym.used {
				p.use(lf.sym) // used before its body was kept (a nested class's)
			}
		}
		for _, v := range ci.vfuncs {
			p.use(v) // the table has its address
		}
		return
	}
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
		return p.initMembers(t, nil, nil, sym.p)
	})
}

// implicitCopy defines a class's implicit copy constructor, or copy
// assignment operator: base by base, then member by member.
func (p *parser) implicitCopy(sym *symbol, t *ctype, isAssign bool) {
	p.synth(sym, func(ps []*symbol) []stmt {
		at := sym.p
		ci := t.rec.cls
		this := p.thisObject(at)
		other := p.unref(&varRef{node{ps[1].t, at}, ps[1]})
		var out []stmt
		copyObj := func(dst, src expr) {
			switch {
			case !needsCopy(dst.ty()):
				if dst.ty().sizeof() > 0 {
					out = append(out, &exprStmt{p.copyBytes(dst, src, at)})
				}
			case isAssign:
				out = append(out, p.eachElem(dst, src, at, func(d, s expr) expr { return p.rvalueVoid(p.classAssign("=", d, s, at)) }))
			default:
				out = append(out, p.eachElem(dst, src, at, func(d, s expr) expr { return p.constructAt(d, d.ty(), []expr{s}, at, 0) }))
			}
		}
		for _, b := range ci.bases {
			if b.f == nil && !needsCopy(b.t) {
				continue
			}
			copyObj(p.baseObject(this, b.t, at), p.baseObject(other, b.t, at))
		}
		if !isAssign {
			out = append(out, p.setVptr(t, at)...)
		}
		for _, f := range t.rec.fields {
			if f.base || f.vptr {
				continue
			}
			dst := &member{node{f.t, at}, this, f}
			src := &member{node{qualifyDeep(f.t, qConst), at}, other, f}
			if f.bits >= 0 {
				out = append(out, &exprStmt{&assign{node{f.t.unqual(), at}, "=", dst, &cast{node{f.t.unqual(), at}, src}, f.t}})
				continue
			}
			copyObj(dst, src)
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
func (p *parser) initMembers(t *ctype, inits map[*field]memInit, baseInits map[*baseClass]memInit, at pos) []stmt {
	ci := t.rec.cls
	var out []stmt
	for _, b := range ci.bases {
		in, ok := baseInits[b]
		if !ok && !needsCtor(b.t) {
			continue
		}
		mark := len(p.temps)
		mode := mValue
		if !ok {
			mode = 0
		}
		if in.braceAt != 0 {
			save := p.pos
			p.pos = in.braceAt + 1
			for !p.is("}") {
				in.args = append(in.args, p.assignment())
				if !p.accept(",") {
					break
				}
			}
			p.pos = save
		}
		e := p.constructAt(p.baseObject(p.thisObject(at), b.t, at), b.t, in.args, at, mode)
		out = append(out, &exprStmt{p.fullExpr(mark, e)})
	}
	out = append(out, p.setVptr(t, at)...)
	for _, f := range t.rec.fields {
		if f.base || f.vptr {
			continue
		}
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
		if !needsDtor(f.t) || f.base || f.vptr {
			continue
		}
		if d := p.destroy(&member{node{f.t, at}, p.thisObject(at), f}, at); d != nil {
			out = append(out, d)
		}
	}
	bases := t.rec.cls.bases
	for i := len(bases) - 1; i >= 0; i-- {
		if d := p.destroy(p.baseObject(p.thisObject(at), bases[i].t, at), at); d != nil {
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
		list := p.setVptr(ct, at)
		if members := p.destroyMembers(ct, at); len(members) > 0 {
			// When the body is left, however it is.
			list = append(list, &deferStmt{&block{list: members}})
		}
		if len(list) == 0 {
			return body
		}
		return &block{list: append(list, body)}
	}
	inits := map[*field]memInit{}
	baseInits := map[*baseClass]memInit{}
	var pre, anon []stmt
	delegated := false
	if p.accept(":") {
		for {
			mat := p.here()
			var base *baseClass
			name := ""
			if sym, n := p.cxxTypeAt(0); sym != nil && sym.t.kind == kStruct && (p.isAt(n, "(") || p.isAt(n, "{")) {
				p.pos += n
				name = sym.t.rec.tag
				for _, b := range r.cls.bases {
					if b.t.rec == sym.t.rec {
						base = b
					}
				}
				if base == nil && sym.t.rec != r {
					p.errorAt(mat, "%s is not a base of %s", sym.t, ct)
				}
			} else {
				name = p.ident()
			}
			in := memInit{at: mat}
			if p.is("{") {
				in.braceAt = p.pos
				p.skipBraces()
			} else {
				p.expect("(")
				in.args = p.callArgs()
			}
			if base != nil {
				baseInits[base] = in
			} else if name == r.tag {
				// A delegating constructor.
				if in.braceAt != 0 {
					p.errorAt(mat, "a delegating constructor's arguments are in parentheses here")
				}
				mark := len(p.temps)
				pre = append(pre, &exprStmt{p.fullExpr(mark, p.constructAt(p.thisObject(mat), ct, in.args, mat, mValue))})
				delegated = true
			} else {
				path := r.lookupField(name)
				switch {
				case len(path) == 1:
					inits[path[0]] = in
				case len(path) > 1:
					// A member of an anonymous struct or union.
					dst := p.memberOf(p.thisObject(mat), name, mat)
					mark := len(p.temps)
					var e expr
					switch {
					case in.braceAt != 0:
						save := p.pos
						p.pos = in.braceAt + 1
						if p.is("}") {
							e = p.zeroObject(dst, mat)
						} else {
							e = p.initScalar(dst, p.initExpr(), mat)
						}
						p.pos = save
					case dst.ty().kind == kStruct:
						e = p.constructAt(dst, dst.ty(), in.args, mat, mValue)
					case len(in.args) == 0:
						e = p.zeroObject(dst, mat)
					case len(in.args) == 1:
						e = p.initScalar(dst, in.args[0], mat)
					default:
						p.errorAt(mat, "too many initializers for the member %q", name)
					}
					anon = append(anon, &exprStmt{p.fullExpr(mark, e)})
				default:
					p.errorAt(mat, "%s has no member %q to initialize", ct, name)
				}
			}
			if !p.accept(",") {
				break
			}
		}
	}
	if !delegated {
		pre = append(pre, p.initMembers(ct, inits, baseInits, at)...)
	}
	pre = append(pre, anon...)
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
	if lam := p.lambda; lam != nil {
		// In a lambda: the this it captured.
		if lam.classThis == nil {
			p.errorAt(at, "invalid use of a member in a lambda outside a non-static member function")
		}
		ptr := p.rvalue(p.captured(lam.classThis, at))
		return &deref{node{ptr.ty().elem, at}, ptr}
	}
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
	p.accept("template") // x.template f<T>()
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
		m := &methodRef{node{sym.t, at}, x, funcSet(sym), in != nil, nil}
		if p.is("<") && hasTemplate(m.set) {
			m.targs = p.templateArgs()
		}
		return m
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
	p.complete(x.ty())
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

// Virtual functions.

// sameVirtual reports whether the member function m overrides the
// virtual function v of a base class.
func sameVirtual(m, v *symbol) bool {
	mf, vf := m.t.fn, v.t.fn
	if mf.dtor || vf.dtor {
		return mf.dtor && vf.dtor
	}
	if m.src != v.src || len(mf.params) != len(vf.params) || mf.variadic != vf.variadic || constThis(mf) != constThis(vf) {
		return false
	}
	for i := 1; i < len(mf.params); i++ {
		x, y := mf.params[i], vf.params[i]
		if x.ref == 0 && y.ref == 0 {
			x, y = x.unqual(), y.unqual()
		}
		if !sameType(x, y) {
			return false
		}
	}
	return true
}

// buildVtable makes the table of virtual functions of the class t: its
// primary base's, with the functions t overrides replaced and those it
// adds at the end. A virtual destructor has two entries: the destructor,
// and the function that also frees the object, which delete calls.
func (p *parser) buildVtable(t *ctype, at pos) {
	ci := t.rec.cls
	ci.vslot = map[*symbol]int{}
	if !ci.poly {
		return
	}
	if ci.primary != nil {
		ci.vfuncs = append([]*symbol(nil), classOf(ci.primary.t).vfuncs...)
	}
	deleting := func(dtor *symbol) *symbol {
		sym := &symbol{name: strings.Replace(dtor.name, "D1Ev", "D0Ev", 1), src: dtor.src, kind: symFunc, t: dtor.t, global: true,
			linkage: dtor.linkage, p: at, sc: dtor.sc, comdat: true, implicit: true}
		p.u.syms = append(p.u.syms, sym)
		ci.delDtor = sym
		return sym
	}
	for _, m := range ci.methods {
		ft := m.t.fn
		if ft.recv == nil || ft.ctor {
			continue
		}
		found := -1
		for i, v := range ci.vfuncs {
			if sameVirtual(m, v) {
				found = i
				break
			}
		}
		switch {
		case found >= 0:
			m.virtual = true
			ci.vfuncs[found] = m
			ci.vslot[m] = found
			if ft.dtor {
				ci.vfuncs[found+1] = deleting(m)
			}
		case m.virtual:
			ci.vslot[m] = len(ci.vfuncs)
			ci.vfuncs = append(ci.vfuncs, m)
			if ft.dtor {
				ci.vfuncs = append(ci.vfuncs, deleting(m))
			}
		}
	}
	// The table: an array of pointers to functions.
	vt := &symbol{name: "_ZTV" + rawType(t.unqual()), kind: symVar, t: arrayOf(fnPtrType, int64(len(ci.vfuncs))), global: true,
		linkage: 'e', defined: true, comdat: true, p: at, addr: true}
	vt.src = vt.name
	if inBlock(ci.scope) {
		vt.linkage = 0
	}
	in := &initializer{}
	for i, f := range ci.vfuncs {
		var x expr = &intConst{node{fnPtrType, at}, 0}
		if !f.pure {
			f.used = true
			x = &cast{node{fnPtrType, at}, &decay{node{ptrTo(f.t), at}, &varRef{node{f.t, at}, f}}}
		}
		in.items = append(in.items, initItem{off: int64(i) * fnPtrType.sizeof(), t: fnPtrType, x: x})
	}
	vt.init = in
	p.u.syms = append(p.u.syms, vt)
	ci.vtable = vt
	if d := ci.delDtor; d != nil {
		dtor := ci.scope.vars["~"+t.rec.tag]
		p.synth(d, func(ps []*symbol) []stmt {
			this := &varRef{node{p.this.t, at}, p.this}
			return []stmt{
				&exprStmt{p.callFunc(dtor, p.thisObject(at), nil, at)},
				p.deallocate(&cast{node{ptrTo(tVoid), at}, this}, t, false, at),
			}
		})
	}
}

// setVptr are the statements that make *this an object of the class t
// to virtual calls: its pointer to the table is t's.
func (p *parser) setVptr(t *ctype, at pos) []stmt {
	ci := t.rec.cls
	if ci == nil || !ci.poly || ci.vtable == nil {
		return nil
	}
	vpt := ptrTo(fnPtrType)
	this := &varRef{node{p.this.t, at}, p.this}
	slot := &deref{node{vpt, at}, &cast{node{ptrTo(vpt), at}, this}}
	vt := ci.vtable
	val := &decay{node{vpt, at}, &varRef{node{vt.t, at}, vt}}
	return []stmt{&exprStmt{&assign{node{vpt, at}, "=", slot, val, vpt}}}
}

// virtualFn is the function that the entry slot of the table of the
// object this points to (a pointer to a class with virtual functions,
// with no effects) holds, as a pointer to a function of type ft.
func (p *parser) virtualFn(this expr, slot int, ft *ctype, at pos) expr {
	vpt := ptrTo(fnPtrType)
	vptr := &deref{node{vpt, at}, &cast{node{ptrTo(vpt), at}, this}}
	entry := &deref{node{fnPtrType, at}, &binary{node{vpt, at}, "+", vptr, p.intc(int64(slot), tLong, at)}}
	return &cast{node{ptrTo(ft), at}, entry}
}

// exactObject reports whether obj is an object whose class is known: a
// variable or a temporary, not what a pointer or reference refers to.
func (p *parser) exactObject(obj expr) bool {
	switch e := obj.(type) {
	case *varRef:
		return e.sym.t.kind == kStruct
	case *seqExpr:
		return p.xvalues[obj]
	case *member:
		return !e.f.base && p.exactObject(e.x)
	}
	return false
}

// wrapPre is e after the statements pre.
func (p *parser) wrapPre(pre []stmt, e expr) expr {
	if len(pre) == 0 {
		return e
	}
	switch x := e.(type) {
	case *classPR:
		return &classPR{x.node, func(dst expr) expr {
			in := x.build(dst)
			return &seqExpr{node{in.ty(), in.at()}, pre, in}
		}}
	case *deref:
		// A reference's referent: still an lvalue.
		d := &deref{x.node, &seqExpr{node{x.x.ty(), x.p}, pre, x.x}}
		if p.xvalues[e] {
			p.xvalue(d)
		}
		return d
	}
	return &seqExpr{node{e.ty(), e.at()}, pre, e}
}
