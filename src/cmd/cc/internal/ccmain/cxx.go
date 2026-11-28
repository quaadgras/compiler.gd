// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"path/filepath"
	"strings"
)

// C++ (doc/gd/cxx.md): the parser lowers it to the typed C tree. Here:
// namespaces and names, references, linkage, and the expressions that
// are C++'s own. Overloads are in cxxovl.go, classes in cxxclass.go,
// mangled names in mangle.go.

// isCxxFile reports whether a file is C++, by its name.
func isCxxFile(name string) bool {
	switch filepath.Ext(name) {
	case ".cc", ".cpp", ".cxx", ".c++", ".C", ".hpp", ".hh", ".hxx":
		return true
	}
	return false
}

// cxxMacros are predefined for C++, after C's.
const cxxMacros = `
#undef __STDC_VERSION__
#define __cplusplus 201703L
#define __GXX_ABI_VERSION 1016
#define __cpp_constexpr 201603L
#define __cpp_static_assert 201411L
#define __cpp_rvalue_references 200610L
`

type scopeKind uint8

const (
	skBlock     scopeKind = iota // a block's, a function's parameters'
	skNamespace                  // a namespace's; the file's is the global namespace's
	skClass
	skEnum     // a scoped enumeration's constants
	skTemplate // a template instance's parameters, as its arguments
)

var cxxTypeWords = map[string]bool{
	"class": true, "virtual": true, "explicit": true, "friend": true, "mutable": true,
	"typename": true, "decltype": true, "wchar_t": true, "char8_t": true, "char16_t": true,
	"char32_t": true, "consteval": true, "constinit": true, "__underlying_type": true,
	"__make_unsigned": true, "__make_signed": true,
}

// cxxKeywords are not names (of variables, labels, members).
var cxxKeywords = map[string]bool{
	"class": true, "virtual": true, "explicit": true, "friend": true, "mutable": true,
	"typename": true, "decltype": true, "namespace": true, "using": true, "template": true,
	"operator": true, "this": true, "new": true, "delete": true, "public": true, "private": true,
	"protected": true, "static_cast": true, "const_cast": true, "reinterpret_cast": true,
	"dynamic_cast": true, "try": true, "catch": true, "throw": true, "noexcept": true,
	"typeid": true, "final": false, "override": false,
}

// find finds name in s itself: its own names, then those its using
// directives and inline namespaces make visible, then its bases'.
func (s *scope) find(name string) *symbol {
	if v, ok := s.vars[name]; ok {
		return v
	}
	if len(s.using) == 0 && s.kind != skClass {
		return s.findStd(name)
	}
	return s.findIn(name, map[*scope]bool{})
}

func (s *scope) findIn(name string, seen map[*scope]bool) *symbol {
	if seen[s] {
		return nil
	}
	seen[s] = true
	if v, ok := s.vars[name]; ok {
		return v
	}
	for _, u := range s.using {
		if v := u.findIn(name, seen); v != nil {
			return v
		}
	}
	if s.kind == skClass {
		return s.findMember(name, seen)
	}
	return s.findStd(name)
}

// findStd finds in std a name that C's library declares: std's names
// that are C's are the global namespace's.
func (s *scope) findStd(name string) *symbol {
	if s.kind != skNamespace || s.name != "std" || s.parent == nil || s.parent.parent != nil {
		return nil
	}
	if v := s.parent.vars[name]; v != nil && (v.cLink || isCHeader(v.p.file)) {
		return v
	}
	return nil
}

// fileScope is the global namespace's scope.
func (p *parser) fileScope() *scope {
	s := p.sc
	for s.parent != nil {
		s = s.parent
	}
	return s
}

// nsScope is the namespace the current scope is in.
func (p *parser) nsScope() *scope {
	s := p.sc
	for s.kind != skNamespace {
		s = s.parent
	}
	return s
}

// namespaceDef parses a namespace definition or alias, at "namespace"
// (after "inline", if inline).
func (p *parser) namespaceDef(inline bool) {
	p.next()
	p.attributes()
	var names []string
	var inl []bool
	for p.peek().kind == tkIdent && !p.is("{") {
		names = append(names, p.ident())
		inl = append(inl, inline)
		if !p.accept("::") {
			break
		}
		inline = p.accept("inline")
	}
	if len(names) == 1 && p.accept("=") {
		// namespace A = B::C;
		sym, _, n := p.qualName(0)
		if sym == nil || sym.kind != symNamespace {
			p.errorf("expected a namespace's name")
		}
		p.pos += n
		p.expect(";")
		p.sc.vars[names[0]] = sym
		return
	}
	p.attributes()
	p.expect("{")
	save := p.sc
	if len(names) == 0 {
		// An unnamed namespace: one for each namespace it is in, its
		// names visible there, with internal linkage.
		var ns *scope
		for _, u := range p.sc.using {
			if u.anon && u.parent == p.sc {
				ns = u
			}
		}
		if ns == nil {
			ns = &scope{vars: map[string]*symbol{}, tags: map[string]*ctype{}, parent: p.sc, kind: skNamespace, anon: true}
			p.sc.using = append(p.sc.using, ns)
		}
		p.sc = ns
	}
	for i, name := range names {
		sym := p.sc.vars[name]
		if sym == nil {
			ns := &scope{vars: map[string]*symbol{}, tags: map[string]*ctype{}, parent: p.sc, kind: skNamespace, name: name}
			sym = &symbol{name: name, src: name, kind: symNamespace, ns: ns, p: p.here()}
			p.sc.vars[name] = sym
			if inl[i] {
				p.sc.using = append(p.sc.using, ns)
			}
		} else if sym.kind != symNamespace || sym.ns.parent != p.sc {
			p.errorf("%q is not a namespace here", name)
		}
		p.sc = sym.ns
	}
	for !p.accept("}") {
		if p.peek().kind == tkEOF {
			p.errorf("expected \"}\" at end of input")
		}
		p.topLevel()
	}
	p.sc = save
}

// usingDecl parses a using directive, using declaration or alias
// declaration, at "using".
func (p *parser) usingDecl() {
	at := p.here()
	p.next()
	if p.accept("namespace") {
		sym, _, n := p.qualName(0)
		if sym == nil || sym.kind != symNamespace {
			p.errorf("expected a namespace's name")
		}
		p.pos += n
		p.expect(";")
		p.sc.using = append(p.sc.using, sym.ns)
		return
	}
	if p.peek().kind == tkIdent && (p.isAt(1, "=") || p.isAt(1, "[") && p.isAt(2, "[")) {
		// using T = type;
		name := p.ident()
		p.attributes()
		p.expect("=")
		t := p.typeName()
		p.expect(";")
		p.declareType(name, t, at)
		return
	}
	p.accept("typename")
	sym, _, n := p.qualName(0)
	if sym == nil {
		p.errorf("using declaration of an undeclared name")
	}
	name := p.peekAt(n - 1).text
	p.pos += n
	p.expect(";")
	if old := p.sc.vars[name]; old != nil && old != sym && !old.tagName {
		if old.kind == symFunc && sym.kind == symFunc {
			// The functions it names join those declared here: the sets
			// are kept apart (a set is a list through its symbols), so
			// the declared ones are found first, then the used.
			p.sc.usedFuncs = append(p.sc.usedFuncs, sym)
			return
		}
		p.errorAt(at, "%q is already declared in this scope", name)
	}
	p.sc.vars[name] = sym
}

// declareType declares name as a type's name in the current scope
// (typedef, using =).
func (p *parser) declareType(name string, t *ctype, at pos) *symbol {
	sym := &symbol{name: name, src: name, kind: symTypedef, t: t, p: at, sc: p.sc}
	if old, ok := p.sc.vars[name]; ok {
		if old.kind == symTypedef && (sameType(old.t, t) || old.tagName) {
			if old.tagName && !sameType(old.t, t) {
				p.sc.vars[name] = sym
			}
			return old
		}
		p.errorAt(at, "redefinition of %q", name)
	}
	p.sc.vars[name] = sym
	return sym
}

// declareTag makes a class's or enumeration's tag a type name, as it is
// in C++, unless the name is something else already (C's struct stat
// and stat).
func (p *parser) declareTag(sc *scope, tag string, t *ctype) {
	if !p.cxx || tag == "" {
		return
	}
	if old, ok := sc.vars[tag]; ok {
		if old.tagName {
			old.t = t
		}
		return
	}
	sc.vars[tag] = &symbol{name: tag, src: tag, kind: symTypedef, t: t, sc: sc, tagName: true}
}

type tidKey struct {
	pos     int
	sc      *scope
	pattern int
	pack    int // the element of the packs being expanded, -1 outside an expansion
}

type tidVal struct {
	tm   *template
	sym  *symbol
	end  int
	half bool // it ends within a ">>" token, at end
}

// nameAt resolves the name at token offset j, in the scope in (nil: by
// unqualified lookup): its symbol, and the offset after it, which is
// after its template arguments if it names a template and has them.
func (p *parser) nameAt(j int, in *scope) (*symbol, int) {
	t := p.peekAt(j)
	var s *symbol
	if in == nil {
		s = p.lookup(t.text)
	} else {
		s = in.find(t.text)
	}
	if s != nil && s.kind == symPack && p.packOn {
		s = p.packElem(s)
	}
	if s == nil || !p.isAt(j+1, "<") {
		return s, j + 1
	}
	var tm *template
	switch {
	case s.kind == symTemplate:
		tm = s.tmpl
	case s.kind == symTypedef && s.instOf != nil:
		tm = s.instOf
	case s.kind == symVar && in == nil && s.sc != nil && s.sc.kind == skTemplate && s.sc.parent != nil:
		// A variable template's instance, named in its own
		// initializer: the template (fact<N - 1>).
		if o := s.sc.parent.find(t.text); o != nil && o.kind == symTemplate && o.tmpl.kind == 'v' {
			tm = o.tmpl
		}
	}
	if tm == nil {
		return s, j + 1
	}
	key := tidKey{p.pos + j, p.sc, p.pattern, -1}
	if p.packOn {
		key.pack = p.packI
	}
	if c, ok := p.tids[key]; ok && c.tm == tm {
		if c.half {
			p.gtHalf = c.end
		}
		return c.sym, c.end - p.pos
	}
	sym, end := p.templateID(tm, j+1, pos{t.file, t.line})
	if p.tids == nil {
		p.tids = map[tidKey]tidVal{}
	}
	p.tids[key] = tidVal{tm, sym, p.pos + end, p.gtHalf == p.pos+end}
	return sym, end
}

// nested scans the nested name specifier at token offset i (A::B::,
// ::): the scope it names, nil if there is none, and the offset after it.
func (p *parser) nested(i int) (in *scope, j int) {
	j = i
	if p.isAt(j, "::") {
		in = p.fileScope()
		j++
	}
	for p.peekAt(j).kind == tkIdent {
		if !p.isAt(j+1, "::") && !p.isAt(j+1, "<") {
			break
		}
		s, e := p.nameAt(j, in)
		if !p.isAt(e, "::") {
			break
		}
		if s != nil && in == nil && (s.kind == symVar || s.kind == symFunc || s.kind == symField) && p.isAt(j+1, "::") {
			// Before "::" only namespaces and types are looked for: one
			// that a variable of the same name hides is found.
			for sc := p.sc; sc != nil; sc = sc.parent {
				if v := sc.find(p.peekAt(j).text); v != nil && (v.kind == symNamespace || v.kind == symTypedef) {
					s = v
					break
				}
			}
		}
		var next *scope
		if s != nil {
			switch s.kind {
			case symNamespace:
				next = s.ns
			case symTypedef:
				p.complete(s.t)
				next = typeScope(s.t)
			}
		}
		if next == nil {
			break
		}
		in = next
		j = e + 1
		if p.isAt(j, "template") {
			j++
		}
	}
	return in, j
}

// qualName resolves the name at token offset i, which may be qualified
// (A::B::c, ::c): its symbol (nil if it is not declared), the scope its
// qualifier names (nil if it has none), and how many tokens it is. If no
// identifier follows the qualifier (~T, operator+), the symbol is nil
// and n counts the qualifier only.
func (p *parser) qualName(i int) (sym *symbol, in *scope, n int) {
	in, j := p.nested(i)
	t := p.peekAt(j)
	if t.kind != tkIdent || t.text == "operator" || t.text == "template" {
		return nil, in, j - i
	}
	sym, e := p.nameAt(j, in)
	return sym, in, e - i
}

// typeScope is the scope of a class's members or an enumeration's
// constants, if t is one.
func typeScope(t *ctype) *scope {
	if t.enum != nil {
		return t.enum.names
	}
	if t.kind == kStruct && t.rec.cls != nil {
		return t.rec.cls.scope
	}
	return nil
}

// cxxTypeAt reports whether a (qualified) type name starts at token
// offset i, and how many tokens it is.
func (p *parser) cxxTypeAt(i int) (*symbol, int) {
	t := p.peekAt(i)
	if t.kind != tkIdent && !(t.kind == tkPunct && t.text == "::") {
		return nil, 0
	}
	sym, _, n := p.qualName(i)
	if sym != nil && sym.kind == symTypedef {
		return sym, n
	}
	return nil, 0
}

// C++'s character types that are types of their own, not the integer
// types they are represented as.
var (
	tWChar  = &ctype{kind: kInt, chr: 'w'}
	tChar16 = &ctype{kind: kUShort, chr: 's'}
	tChar32 = &ctype{kind: kUInt, chr: 'i'}
)

// References. A reference is a pointer in the tree (ctype.ref); no
// expression has reference type.

func refTo(t *ctype, kind uint8) *ctype {
	if t.kind == kPtr && t.ref != 0 {
		// Reference collapsing: an lvalue reference wins.
		if t.ref == 1 || kind == 2 {
			return t
		}
		return &ctype{kind: kPtr, elem: t.elem, ref: 1}
	}
	return &ctype{kind: kPtr, elem: t, ref: kind}
}

func (t *ctype) isRef() bool { return t.kind == kPtr && t.ref != 0 }

// unref is the expression e names: what a reference refers to, if e's
// type is a reference's.
func (p *parser) unref(e expr) expr {
	t := e.ty()
	if !t.isRef() {
		return e
	}
	if t.elem.kind == kFunc {
		return e // a reference to a function: its pointer, as a function designator's value
	}
	return &deref{node{t.elem, e.at()}, &cast{node{ptrTo(t.elem), e.at()}, e}}
}

// isXvalue reports whether e, an lvalue in the tree, is an rvalue to
// C++: a temporary the parser made, or an rvalue reference's referent
// that a cast or a call named.
func (p *parser) isXvalue(e expr) bool {
	if p.xvalues[e] {
		return true
	}
	if m, ok := e.(*member); ok {
		return p.isXvalue(m.x)
	}
	return false
}

// xvalue marks e, an lvalue in the tree, as an rvalue to C++.
func (p *parser) xvalue(e expr) expr {
	if p.xvalues == nil {
		p.xvalues = map[expr]bool{}
	}
	p.xvalues[e] = true
	return e
}

// isLvalueCxx reports whether e is an lvalue to C++.
func (p *parser) isLvalueCxx(e expr) bool {
	if p.isXvalue(e) {
		return false
	}
	switch e := e.(type) {
	case *seqExpr:
		return p.isLvalueCxx(e.x)
	case *strLit:
		return true
	case *member:
		return p.isLvalueCxx(e.x)
	}
	return isLvalue(e)
}

// temporary makes an unnamed object of type t: a local, or at file scope
// an object of static duration.
func (p *parser) temporary(t *ctype, at pos) *symbol {
	t = t.unqual()
	if p.fn != nil {
		sym := p.newLocal("tmp", t, at)
		sym.addr = true
		return sym
	}
	p.symN++
	sym := &symbol{name: "tmp." + itoa(int64(p.symN)), kind: symVar, t: t, global: true, storage: sStatic, p: at, defined: true}
	p.u.statics = append(p.u.statics, sym)
	return sym
}

// materialize makes a temporary of e's value (an rvalue that is not a
// class's): the temporary, as an lvalue that C++ takes for an rvalue.
func (p *parser) materialize(e expr, t *ctype) expr {
	at := e.at()
	sym := p.temporary(t, at)
	ref := &varRef{node{sym.t, at}, sym}
	var init stmt
	if sym.t.kind == kStruct {
		init = &exprStmt{&assign{node{sym.t, at}, "=", ref, e, sym.t}}
	} else {
		init = &exprStmt{&assign{node{sym.t, at}, "=", ref, p.assignConvert(p.rvalue(e), sym.t, at, "initializer"), sym.t}}
	}
	return p.xvalue(&seqExpr{node{sym.t, at}, []stmt{init}, ref})
}

// bindRef is the pointer a reference of type t is initialized with to
// bind it to e.
func (p *parser) bindRef(t *ctype, e expr, at pos, what string) expr {
	if l, ok := e.(*initList); ok {
		e = p.fromList(l, t.elem, at)
		if _, isPR := e.(*classPR); !isPR {
			e = p.materialize(e, t.elem)
		}
	}
	to := t.elem
	et := e.ty()
	if to.kind == kFunc {
		return p.convertAt(p.rvalue(e), t, at)
	}
	if to.kind == kStruct && et.kind == kStruct && to.rec != et.rec {
		p.complete(et) // a template's instance: its bases
	}
	if pr, ok := e.(*classPR); ok && to.kind == kStruct && !sameType(to.unqual(), et.unqual()) && isBaseOf(to, et) {
		// A prvalue of a derived class: the reference binds to the base
		// of its temporary.
		e = p.materializePR(pr)
	}
	lv := p.isLvalueCxx(e) && et.kind != kFunc
	same := sameType(to.unqual(), et.unqual()) || isBaseOf(to, et) || similarArrays(to, et)
	if same && (lv && t.ref == 1 || !lv && isLvalue(e)) {
		if lv && to.q&et.q != et.q {
			p.errorAt(at, "binding a %s to a reference to %s discards qualifiers", et, to)
		}
		if !lv && t.ref == 1 && to.q&qConst == 0 {
			p.errorAt(at, "cannot bind a non-const lvalue reference to an rvalue of type %s", et)
		}
		if lv && t.ref == 2 {
			p.errorAt(at, "cannot bind an rvalue reference to an lvalue of type %s", et)
		}
		if f := bitField(e); f != nil {
			p.errorAt(at, "cannot bind a reference to a bit-field")
		}
		return &cast{node{t, at}, p.baseAddr(p.addressOf(e, at), to, at)}
	}
	if lv && t.ref == 2 && same {
		p.errorAt(at, "cannot bind an rvalue reference to an lvalue of type %s", et)
	}
	if t.ref == 1 && to.q&qConst == 0 {
		if same {
			p.errorAt(at, "cannot bind a non-const lvalue reference to an rvalue of type %s", et)
		}
		p.errorAt(at, "invalid initialization of a reference of type %s from an expression of type %s", t, et)
	}
	// A temporary of the referred type, initialized from e.
	var tmp expr
	if _, isPR := e.(*classPR); to.kind == kStruct && same && !isPR {
		// A value of the class already (a call's, a conditional's): it
		// is the temporary.
		tmp = p.materialize(p.baseObject(e, to, at), to)
	} else if to.kind == kStruct {
		tmp = p.classTemp(to, e, at, what)
	} else {
		tmp = p.materialize(p.initConvert(e, to.unqual(), at, what), to)
	}
	return &cast{node{t, at}, p.addressOf(tmp, at)}
}

// initConvert is e converted to initialize an object, parameter or
// result of type t, which is not a class: a reference is bound.
func (p *parser) initConvert(e expr, t *ctype, at pos, what string) expr {
	if l, ok := e.(*initList); ok {
		if t.isRef() {
			e = p.fromList(l, t.elem, at)
		} else {
			return p.fromList(l, t, at)
		}
	}
	if _, ok := e.(*memFnRef); ok || memPtrOf(t) != nil && !t.isRef() {
		if c := p.toMemPtr(e, t, at); c != nil {
			e = c
			if isPMF(t) {
				return p.rvalue(e)
			}
		}
	} else if memPtrOf(e.ty()) != nil && t.kind == kBool {
		return p.memPtrTruth(e, at)
	}
	if t.isRef() {
		return p.bindRef(t, e, at, what)
	}
	if e.ty().kind == kStruct && t.kind != kStruct {
		if c := p.convertClass(e, t, at); c != nil {
			e = c
		}
	}
	return p.assignConvert(p.rvalue(e), t, at, what)
}

// sameType reports whether a and b are the same type, as C++ has it:
// C's compatible types that differ (an enumeration and its integer type,
// arrays of unknown length) are not.
func sameType(a, b *ctype) bool {
	if a == b {
		return true
	}
	if a.kind != b.kind || a.q != b.q || a.enum != b.enum || a.ref != b.ref || a.chr != b.chr {
		return false
	}
	if a.mp != nil || b.mp != nil {
		return a.mp != nil && b.mp != nil && sameType(a.mp.cls, b.mp.cls) && sameType(a.mp.t, b.mp.t)
	}
	switch a.kind {
	case kBitInt, kUBitInt, kTParam:
		return a.n == b.n
	case kPtr, kComplex:
		return sameType(a.elem, b.elem)
	case kArray:
		return a.n == b.n && sameType(a.elem, b.elem)
	case kStruct:
		return a.rec == b.rec
	case kFunc:
		return a.fn.thisQ == b.fn.thisQ && sameType(a.fn.ret, b.fn.ret) && sameParams(a.fn, b.fn)
	}
	return true
}

// sameParams reports whether two functions have the same parameters: the
// same signature, for overloading (top-level qualifiers do not count).
func sameParams(a, b *funcType) bool {
	if len(a.params) != len(b.params) || a.variadic != b.variadic || (a.recv == nil) != (b.recv == nil) {
		return false
	}
	for i := range a.params {
		x, y := a.params[i], b.params[i]
		if x.ref == 0 && y.ref == 0 {
			x, y = x.unqual(), y.unqual()
		}
		if !sameType(x, y) {
			return false
		}
	}
	return true
}

// Linkage.

// isCHeader reports whether a file is one of cmd/cc's C headers, whose
// declarations have C linkage in C++.
func isCHeader(file string) bool {
	return strings.HasPrefix(file, "<") && strings.HasSuffix(file, ".h>")
}

// cLinkage reports whether a declaration at at has C linkage.
func (p *parser) cLinkage(at pos) bool {
	return p.externC > 0 || isCHeader(at.file)
}

// linkageSpec parses extern "C" ... or extern "C" { ... }, at "extern".
func (p *parser) linkageSpec() {
	p.next()
	lang := p.next().str
	delta := 0
	switch lang {
	case "C":
		delta = 1
	case "C++":
		// Within extern "C", C++ linkage again.
		delta = -p.externC
	default:
		p.errorf("unknown linkage %q", lang)
	}
	p.externC += delta
	if p.accept("{") {
		for !p.accept("}") {
			if p.peek().kind == tkEOF {
				p.errorf("expected \"}\" at end of input")
			}
			p.topLevel()
		}
	} else {
		p.externSpec = true
		p.topLevel()
		p.externSpec = false
	}
	p.externC -= delta
}

// Casts and other expressions of C++'s own.

// namedCast parses static_cast<T>(e) and its kin, after the keyword.
func (p *parser) namedCast(kw string, at pos) expr {
	p.expect("<")
	p.noGT++
	t := p.typeName()
	p.noGT--
	p.expectGT()
	p.expect("(")
	e := p.expr()
	p.expect(")")
	return p.castTo(e, t, at, kw)
}

// expectGT expects the ">" that ends template arguments or a named
// cast's type: ">>" is two.
func (p *parser) expectGT() {
	if p.is(">>") {
		// Two brackets in one token, which is not changed (a template's
		// tokens are parsed again): gtHalf says its first is taken.
		if p.gtHalf == p.pos {
			p.gtHalf = -1
			p.pos++
		} else {
			p.gtHalf = p.pos
		}
		return
	}
	p.expect(">")
}

// castTo is e explicitly converted to t (a cast, in any of C++'s
// notations; how is the keyword, or "" for C's).
func (p *parser) castTo(e expr, t *ctype, at pos, how string) expr {
	if t.isRef() {
		// A cast to a reference: an lvalue (an xvalue, for &&) of the
		// referred type, at e's address.
		to := t.elem
		var ptr expr
		if isLvalue(e) && (how == "reinterpret_cast" || how == "const_cast" || sameType(to.unqual(), e.ty().unqual()) || isBaseOf(to, e.ty()) || isBaseOf(e.ty(), to) || how == "") {
			ptr = &cast{node{ptrTo(to), at}, p.derivedAddr(p.baseAddr(p.addressOf(e, at), to, at), to, at)}
		} else {
			ptr = p.bindRef(t, e, at, "cast")
		}
		d := &deref{node{to, at}, &cast{node{ptrTo(to), at}, ptr}}
		if t.ref == 2 {
			p.xvalue(d)
		}
		return d
	}
	if t.kind == kVoid {
		return &cast{node{tVoid, at}, p.rvalueVoid(e)}
	}
	if _, ok := e.(*memFnRef); ok || memPtrOf(t) != nil || memPtrOf(e.ty()) != nil && (t.kind == kBool || isPMF(e.ty())) {
		p.castingMemPtr = true // a cast converts any to any
		c := p.toMemPtr(e, t, at)
		p.castingMemPtr = false
		if c != nil {
			return c
		}
	}
	if t.kind == kStruct {
		if sameType(t.unqual(), e.ty().unqual()) {
			return e
		}
		return p.construct(t, []expr{e}, at, true)
	}
	if e.ty().kind == kStruct {
		if c := p.convertClass(e, t, at); c != nil {
			e = c
		}
	}
	x := p.rvalue(e)
	if !t.isScalar() {
		p.errorAt(at, "conversion to non-scalar type %s requested", t)
	}
	if !x.ty().isScalar() {
		p.errorAt(at, "%s used where a scalar is required", x.ty())
	}
	if t.isFloat() && x.ty().isPtr() || t.isPtr() && x.ty().isFloat() {
		p.errorAt(at, "cannot convert between pointer and floating types")
	}
	if t.isPtr() && x.ty().isPtr() && how != "reinterpret_cast" && how != "const_cast" {
		// To a base, or (static_cast) from one.
		x = p.derivedAddr(p.baseAddr(x, t.elem, at), t.elem, at)
	}
	return p.convertAt(x, t.unqual(), at)
}

// functionalCast parses T(args) or T{args}, after the type T: a
// conversion, or a temporary's construction.
func (p *parser) functionalCast(t *ctype, at pos) expr {
	brace := p.is("{")
	if brace && t.kind == kStruct {
		p.complete(t) // a template's instance: its members, before they are initialized
	}
	if ci := classOf(t); brace && (t.kind == kArray || t.kind == kStruct && (ci == nil || !ci.userCtor && len(ci.nsdmi) == 0 && !needsCtor(t) && !needsCopy(t))) {
		// An aggregate's: a compound literal. (One with members that
		// constructors or default member initializers initialize is
		// initialized member by member, below.)
		return p.compoundLiteral(t, at)
	}
	if brace && t.kind == kStruct {
		// T{...} of a class with constructors.
		l := p.braceList()
		p.requireComplete(t, at)
		return &classPR{node{t.unqual(), at}, func(dst expr) expr { return p.listInit(dst, t, l, at) }}
	}
	p.next()
	end := ")"
	if brace {
		end = "}"
	}
	args := p.exprList(end)
	if t.kind == kStruct {
		return p.construct(t, args, at, true)
	}
	switch len(args) {
	case 0:
		// Value initialization: zero.
		if t.kind == kVoid {
			return &cast{node{tVoid, at}, p.intc(0, tInt, at)}
		}
		return p.convertAt(p.intc(0, tInt, at), t.unqual(), at)
	case 1:
		return p.castTo(args[0], t, at, "")
	}
	p.errorAt(at, "too many initializers for a %s", t)
	return nil
}

// replaceAuto is t with its placeholder auto replaced by d.
func replaceAuto(t, d *ctype) *ctype {
	if t.isAuto {
		return d.qualified(t.q)
	}
	switch t.kind {
	case kPtr, kArray:
		if t.elem != nil {
			if e := replaceAuto(t.elem, d); e != t.elem {
				c := *t
				c.elem = e
				return &c
			}
		}
	}
	return t
}

func hasAuto(t *ctype) bool {
	for t != nil {
		if t.isAuto {
			return true
		}
		if t.kind != kPtr && t.kind != kArray {
			return false
		}
		t = t.elem
	}
	return false
}

// autoType is the type a declaration with the placeholder type t (auto,
// auto &, const auto *, ...) has, initialized with e.
func (p *parser) autoType(t *ctype, e expr, at pos) *ctype {
	var d *ctype
	if t.isRef() {
		d = e.ty()
		if t.ref == 2 && p.isLvalueCxx(e) {
			// A forwarding reference bound to an lvalue.
			return refTo(replaceAuto(t.elem, d), 1)
		}
		return replaceAuto(t, d.unqual().qualified(d.q&^t.elem.q))
	}
	if pr, ok := e.(*classPR); ok {
		d = pr.t.unqual() // its type only: it is not made here
	} else {
		d = valueType(e)
	}
	if t.kind == kPtr && d.kind == kPtr {
		// auto *p = q: auto is what q points to.
		return replaceAuto(t, d.elem.unqual().qualified(d.elem.q&^t.elem.q))
	}
	return replaceAuto(t, d)
}

// isCtorName reports whether the type name sym, n tokens long at the
// parser's position, is not a type specifier but a constructor's name.
func (p *parser) isCtorName(sym *symbol, n int) bool {
	if sym.t.kind != kStruct || !p.isAt(n, "(") || p.peekAt(n-1).kind != tkIdent {
		return false
	}
	if in, j := p.nested(0); in != nil && j == n-1 {
		return in.kind == skClass && in.rec == sym.t.rec && p.peekAt(n-1).text == in.rec.tag // A::A(
	}
	cs := p.sc
	for cs != nil && cs.kind == skTemplate {
		cs = cs.parent
	}
	return n == 1 && p.exprType == 0 && p.cls != nil && p.cls.rec == sym.t.rec && cs != nil && cs.kind == skClass && cs.rec == sym.t.rec
}

// declaratorID parses a declarator's name in C++, "" if it has none: an
// identifier, ~T, operator+ or operator T, which may be qualified. in is
// the scope its qualifier names.
func (p *parser) declaratorID() (name string, in *scope) {
	if p.varSpec && p.peek().kind == tkIdent && p.isAt(1, "<") {
		// A variable template's specialization, name<pattern>: the
		// pattern is what selected it.
		p.varSpec = false
		name = p.ident()
		p.skipAngles()
		return name, nil
	}
	if p.is("::") || p.peek().kind == tkIdent && (p.isAt(1, "::") || p.isAt(1, "<")) {
		var j int
		in, j = p.nested(0)
		p.pos += j
	}
	switch {
	case p.is("~") && p.peekAt(1).kind == tkIdent:
		p.next()
		return "~" + p.ident(), in
	case p.is("operator"):
		p.next()
		return p.operatorName(), in
	case p.peek().kind == tkIdent && !p.isTypeStartWord() && !cxxKeywords[p.peek().text]:
		return p.ident(), in
	}
	if in != nil {
		p.errorf("expected a name after ::")
	}
	return "", nil
}

// operatorName parses an operator function's name, after "operator":
// "operator+", or for a conversion function "operator T", with T kept
// in p.convType.
func (p *parser) operatorName() string {
	t := p.next()
	if t.kind == tkPunct && t.text != "::" {
		switch t.text {
		case "(":
			p.expect(")")
			return "operator()"
		case "[":
			p.expect("]")
			return "operator[]"
		case "->":
			if p.accept("*") {
				return "operator->*"
			}
		case "<=":
			if p.is(">") && !p.peek().space {
				p.next()
				return "operator<=>"
			}
		}
		if _, ok := operatorCodes[t.text]; ok {
			return "operator" + t.text
		}
		p.errorf("%s is not an operator that can be overloaded", t)
	}
	if t.kind == tkString && t.str == "" && p.peek().kind == tkIdent {
		// A literal operator: operator""_uz.
		return `operator""` + p.next().text
	}
	if t.kind == tkIdent && (t.text == "new" || t.text == "delete") {
		if p.is("[") && p.isAt(1, "]") {
			p.pos += 2
			return "operator" + t.text + "[]"
		}
		return "operator" + t.text
	}
	// A conversion function: operator T, T a type without a function's
	// or array's declarator.
	p.pos--
	ct := p.specQualType()
	for p.is("*") || p.is("&") || p.is("&&") {
		if op := p.next().text; op[0] == '&' {
			ct = refTo(ct, uint8(len(op)))
			continue
		}
		ct = ptrTo(ct)
		for p.is("const") || p.is("volatile") {
			if p.next().text == "const" {
				ct = ct.qualified(qConst)
			} else {
				ct = ct.qualified(qVolatile)
			}
		}
	}
	p.convType = ct
	return "operator " + rawType(ct)
}

// directInitAhead reports whether the "(" next, after a declarator's
// name, starts an initializer and not a function's parameters.
func (p *parser) directInitAhead() bool {
	if p.isAt(1, ")") || p.isAt(1, "...") {
		return false
	}
	return !p.paramsAhead(0)
}

// paramsAhead reports whether what the "(" at i starts can be a list of
// parameters: its first item is a parameter's declaration, and no later
// one is plainly an expression. (The later ones are judged by their
// tokens alone: naming a type may instantiate a template, which is for
// the declaration's own parse to do.)
func (p *parser) paramsAhead(i int) bool {
	if !p.paramAhead(i + 1) {
		return false
	}
	known := func(j int) *symbol {
		if t := p.peekAt(j); t.kind == tkIdent {
			return p.lookup(t.text)
		}
		return nil
	}
	depth, angle := 0, 0
	for j := i + 1; ; j++ {
		t := p.peekAt(j)
		if t.kind == tkEOF {
			return true
		}
		if t.kind != tkPunct {
			continue
		}
		switch t.text {
		case "(", "[", "{":
			depth++
		case ")", "]", "}":
			depth--
		case "<":
			if s := known(j - 1); p.peekAt(j-1).kind == tkIdent && (s == nil || s.kind == symTypedef || s.kind == symTemplate) {
				angle++
			}
		case ">":
			angle = max(angle-1, 0)
		case ">>":
			angle = max(angle-2, 0)
		}
		if depth < 0 {
			return true
		}
		if depth != 0 || angle != 0 || t.text != "," {
			continue
		}
		// The next item: an expression if it starts as only one does.
		n := p.peekAt(j + 1)
		switch n.kind {
		case tkPunct:
			if n.text != "[" && n.text != "::" && n.text != "..." {
				return false
			}
		case tkIdent:
			switch n.text {
			case "this", "nullptr", "true", "false", "sizeof", "alignof", "new", "static_cast", "const_cast", "reinterpret_cast", "typeid", "noexcept":
				return false
			}
			s := known(j + 1)
			if s == nil {
				break
			}
			switch s.kind {
			case symVar, symFunc, symEnumConst, symField:
				if !p.isAt(j+2, "::") {
					return false
				}
			case symTypedef:
				// T(1, 2), T{}: a temporary; T(), T(*), T(U): a type.
				if p.isAt(j+2, "{") {
					return false
				}
				if p.isAt(j+2, "(") {
					m := p.peekAt(j + 3)
					if m.kind != tkIdent && m.kind != tkPunct {
						return false
					}
					if m.kind == tkIdent {
						if ms := known(j + 3); ms != nil && ms.kind != symTypedef && ms.kind != symTemplate {
							return false
						}
					}
				}
			}
		default:
			return false // a constant
		}
	}
}

// paramAhead reports whether the first item of a parenthesized list, at
// i, can be a parameter's declaration.
func (p *parser) paramAhead(i int) bool {
	if k := p.attrEnd(i); k > i {
		return p.paramAhead(k) // (__attribute__((unused)) T x)
	}
	if p.isTypeStart(i) {
		// A parameter's type, unless it is T(args) or T{args}: a
		// temporary. T(name) declares a parameter only if name is not
		// something declared.
		k := p.typeSpecEnd(i)
		switch {
		case p.isAt(k, "{"):
			return false
		case p.isAt(k, "("):
			if _, m := p.memPtrAhead(k + 1); m > 0 {
				return true
			}
			m := k + 1
			for p.isAt(m, "*") || p.isAt(m, "&") || p.isAt(m, "&&") {
				m++
			}
			if p.isAt(m, ")") && m == k+1 {
				// T(): a parameter of function type, if a parameter's
				// end follows; else a temporary.
				return p.isAt(m+1, ",") || p.isAt(m+1, ")")
			}
			if p.isAt(m, ")") {
				return true // T(*), T(&): a parameter's type
			}
			if p.isTypeStart(m) {
				return p.paramsAhead(k) // T(U): a function type
			}
			if t := p.peekAt(m); t.kind == tkIdent && p.isAt(m+1, ")") && p.lookup(t.text) == nil {
				return true
			}
			return false
		}
		return true
	}
	if t := p.peekAt(i); t.kind == tkIdent {
		if s := p.lookup(t.text); s != nil && s.kind == symPack && !s.isParamPack {
			return true // a pack of types: a parameter pack's
		}
	}
	return false
}

// funcQuals parses what follows a function declarator's parameters in
// C++: qualifiers of *this, noexcept, override and final.
func (p *parser) funcQuals(ft *funcType) {
	for {
		switch {
		case p.is("const"):
			ft.thisQ |= qConst
		case p.is("volatile"):
			ft.thisQ |= qVolatile
		case p.is("noexcept"):
			ft.noexcept = true
			if p.isAt(1, "(") {
				p.next()
				p.skipParens()
				continue
			}
		case p.is("throw") && p.isAt(1, "("):
			p.next()
			p.skipParens()
			continue
		case p.is("override"), p.is("final"):
		default:
			return
		}
		p.next()
	}
}

// skipDefaultArg skips a default argument: to the "," or ")" that ends
// it.
func (p *parser) skipDefaultArg() {
	depth, angle := 0, 0
	for {
		t := p.peek()
		if t.kind == tkEOF {
			p.errorf("unterminated default argument")
		}
		if t.kind == tkPunct {
			switch t.text {
			case "(", "[", "{":
				depth++
			case ")", "]", "}":
				if depth == 0 {
					return
				}
				depth--
			case "<":
				// A template's arguments: their commas are not the list's.
				if depth == 0 && p.templateNameAt(p.pos-1) {
					angle++
				}
			case ">":
				if depth == 0 && angle > 0 {
					angle--
				}
			case ">>":
				if depth == 0 {
					angle = max(angle-2, 0)
				}
			case ",":
				if depth == 0 && angle == 0 {
					return
				}
			}
		}
		p.next()
	}
}

// cxxVarName gives the variable sym, declared as name, its linkage and
// its name with it.
func (p *parser) cxxVarName(sym *symbol, name string, s specs, at pos) {
	sym.src = name
	sym.sc = p.sc
	if p.fn != nil || p.sc.kind != skNamespace || s.storage == sTypedef {
		return
	}
	if !p.cLinkage(at) {
		sym.name = mangleVar(p.sc, name)
	}
	if s.inline {
		// An inline variable: each unit that has it defines it.
		sym.comdat = true
	} else if inAnon(p.sc) || (baseElem(sym.t).q&qConst != 0 || s.constx) && s.storage != sExtern && sym.t.ref == 0 {
		// const objects have internal linkage, unless extern says not.
		sym.linkage = 'i'
	}
}

// cxxInit parses sym's initializer where C++ initializes otherwise than
// C: a deduced type, T x(a) and T x{a}. It reports whether it did.
func (p *parser) cxxInit(sym *symbol, s specs, at pos) bool {
	t := sym.t
	if t.isRef() && p.is("=") && p.isAt(1, "{") {
		// A reference to the temporary that a braced list makes.
		p.next()
		p.initVar(sym, p.braceList(), at)
		sym.defined = true
		if sym.storage == sExtern {
			sym.storage = sAuto
		}
		return true
	}
	if t.kind == kArray && (p.is("{") || p.is("=") && p.isAt(1, "{")) && (!sym.global && p.fn != nil && p.packInBraces() || hasUserCtor(baseElem(t))) {
		// An array whose initializer expands a pack, or of a class
		// with constructors: element by element.
		p.accept("=")
		l := p.braceList()
		if t.n < 0 {
			sym.t = arrayOf(t.elem, int64(len(l.args)))
			sym.t.q = t.q
		}
		sym.ctor = p.listInit(&varRef{node{sym.t, at}, sym}, sym.t, l, at)
		sym.defined = true
		return true
	}
	if !hasAuto(t) && !(p.is("(") || p.is("{")) {
		if t.kind == kStruct {
			return p.classInit(sym, s, at)
		}
		return false
	}
	if sym.global {
		p.staticInit++
		defer func() { p.staticInit-- }()
	}
	if t.isAuto && t.ctad != nil {
		// T x(args): T's template arguments from a constructor's.
		var args []expr
		switch {
		case p.accept("("):
			args = p.exprList(")")
		case p.accept("{"):
			args = p.exprList("}")
		case p.accept("="):
			args = []expr{p.initExpr()}
		}
		sym.t = p.ctad(t.ctad, args, at).qualified(t.q)
		sym.ctor = p.constructAt(&varRef{node{sym.t, at}, sym}, sym.t, args, at, mValue)
	} else if hasAuto(t) {
		var e expr
		switch {
		case p.accept("="):
			e = p.initExpr()
		case p.accept("("):
			e = p.expr()
			p.expect(")")
		case p.accept("{"):
			e = p.initExpr()
			p.expect("}")
		default:
			p.errorAt(at, "%q declared auto without an initializer", sym.src)
		}
		sym.t = p.autoType(t, e, at)
		if s.constx {
			sym.t = constexprType(sym.t)
		}
		p.initVar(sym, e, at)
	} else if t.kind == kStruct {
		return p.classInit(sym, s, at)
	} else if t.kind == kArray {
		return false
	} else if p.accept("(") {
		e := p.expr()
		p.expect(")")
		p.initVar(sym, e, at)
	} else {
		p.expect("{")
		if p.accept("}") {
			p.initVar(sym, p.intc(0, tInt, at), at)
		} else {
			e := p.initExpr()
			p.accept(",")
			p.expect("}")
			p.initVar(sym, e, at)
		}
	}
	sym.defined = true
	if sym.storage == sExtern {
		sym.storage = sAuto
	}
	return true
}

// hasUserCtor reports whether t is a class with a constructor that the
// program declares: braces do not initialize its members.
func hasUserCtor(t *ctype) bool {
	ci := classOf(t)
	return ci != nil && ci.complete && ci.userCtor
}

// initVar initializes the variable sym with the expression e.
func (p *parser) initVar(sym *symbol, e expr, at pos) {
	if sym.t.kind == kStruct {
		p.classInitFrom(sym, e, at)
		return
	}
	sym.init = &initializer{scalar: p.initConvert(e, sym.t, at, "initializer")}
}

// extending runs f, the parsing of the initializer of sym: if sym is a
// reference, the temporaries bound to it live as long as it does.
func (p *parser) extending(sym *symbol, f func()) {
	if !sym.t.isRef() || sym.global || p.fn == nil {
		f()
		return
	}
	save := p.extend
	var ext []*symbol
	p.extend = &ext
	f()
	p.extend = save
	sym.extended = ext
}

// cxxName parses a name in an expression, which may be qualified.
func (p *parser) cxxName(at pos) expr {
	sym, in, n := p.qualName(0)
	if p.pattern > 0 && sym != nil && sym.kind == symTypedef && hasTParam(sym.t) && p.isAt(n, "::") {
		// In a pattern: a member of a type that depends on a parameter.
		p.pos += n + 1
		p.skipQualName()
		return &depConst{intConst{node{tInt, at}, 0}, -1}
	}
	if sym != nil && sym.kind == symFunc && p.isAt(n, "<") && hasTemplate(funcSet(sym)) {
		// A function template with explicit arguments.
		name := p.peekAt(n - 1).text
		p.pos += n
		a := &adlRef{node{tVoid, at}, name, funcSet(sym), p.templateArgs(), in != nil}
		if !p.is("(") {
			// Not called: the instance its arguments name.
			for _, s := range a.set {
				if s.tmpl == nil {
					continue
				}
				if targs, ok := p.deduce(s.tmpl, a.targs, nil, at); ok {
					if inst := p.instFunc(s.tmpl, targs, at); inst != nil {
						inst.used = true
						p.use(inst)
						return &varRef{node{inst.t, at}, inst}
					}
				}
			}
			p.errorAt(at, "no instance of the function template %q for these arguments", name)
		}
		return a
	}
	if in == nil && n == 1 && p.isAt(1, "(") && (sym == nil || sym.kind == symFunc && (sym.tmpl != nil || sym.t.fn.recv == nil) && (sym.sc == nil || sym.sc.kind != skClass)) {
		// An unqualified function's call: the functions of its
		// arguments' namespaces are candidates too.
		name := p.next().text
		a := &adlRef{node: node{tVoid, at}, name: name}
		if sym != nil {
			sym.used = true
			a.set = funcSet(sym)
		}
		return a
	}
	if sym == nil && in != nil && p.isAt(n, "operator") {
		// A qualified operator function: Base::operator=(x).
		p.pos += n + 1
		name := p.operatorName()
		sym = in.find(name)
		if in.parent == nil && (strings.HasPrefix(name, "operatornew") || strings.HasPrefix(name, "operatordelete")) {
			// The language's own, unless the program has it: the C
			// library's.
			own := false
			if sym != nil && sym.kind == symFunc {
				for _, o := range funcSet(sym) {
					own = own || len(o.t.fn.params) == 1
				}
			}
			if !own {
				sym = nil
			}
			lib := name
			if own {
				lib = ""
			}
			switch lib {
			case "operatornew", "operatornew[]":
				return p.libcFunc("malloc", at)
			case "operatordelete", "operatordelete[]":
				return p.libcFunc("free", at)
			}
		}
		if sym == nil || sym.kind != symFunc {
			p.errorAt(at, "%q is not a member of %s", name, in.name)
		}
		sym.used = true
		p.use(sym)
		v := &varRef{node{sym.t, at}, sym}
		if p.qualified == nil {
			p.qualified = map[expr]bool{}
		}
		p.qualified[v] = true
		return v
	}
	if sym == nil {
		t := p.peekAt(n)
		if n > 0 && p.peekAt(n-1).kind == tkIdent {
			t = p.peekAt(n - 1)
		}
		if t.kind != tkIdent {
			p.errorAt(at, "expected a name, found %s", t)
		}
		if in != nil && in.rec != nil {
			name := in.name
			if in.rec.mtag != "" {
				name = in.rec.mtag
			}
			p.errorAt(at, "%q is not a member of %s", t.text, name)
		}
		if in != nil && debugSFINAE {
			var names []string
			for k := range in.vars {
				names = append(names, k)
			}
			println("scope", in.name, "kind", in.kind, "has", strings.Join(names, " "))
		}
		p.errorAt(at, "%q undeclared", t.text)
	}
	name := p.peekAt(n - 1).text
	p.pos += n
	switch sym.kind {
	case symEnumConst:
		if sym.depVal {
			return &depConst{intConst{node{sym.t, at}, 0}, sym.depParam}
		}
		return p.intc(sym.val, sym.t, at)
	case symTemplate:
		if sym.tmpl.kind == 'c' && (p.is("(") || p.is("{")) {
			// A class template's name, with no arguments: they are
			// deduced from a constructor's.
			end := ")"
			if p.next().text == "{" {
				end = "}"
			}
			args := p.exprList(end)
			return p.construct(p.ctad(sym.tmpl, args, at), args, at, true)
		}
		p.errorAt(at, "unexpected template name %q", name)
	case symPack:
		p.errorAt(at, "unexpected template name %q", name)
	case symTypedef:
		if p.is("(") || p.is("{") {
			return p.functionalCast(sym.t, at)
		}
		p.errorAt(at, "unexpected type name %q", name)
	case symNamespace:
		p.errorAt(at, "unexpected namespace name %q", name)
	case symField:
		return p.fieldRef(sym, in, at)
	}
	sym.used = true
	if sym.kind == symFunc && sym.tmpl != nil {
		// A function template, by lookup: called.
		if !p.is("(") && !(p.is(")") && p.isAt(1, "(")) {
			// ((std::max)(a, b) is a call too: the parentheses keep a
			// macro of the name from being one.)
			p.errorAt(at, "the function template %q is only called here", name)
		}
		return &adlRef{node{tVoid, at}, name, funcSet(sym), nil, in != nil}
	}
	p.use(sym)
	if p.lambda != nil && sym.kind == symVar && !sym.global && sym.owner != p.fn && (sym.owner != nil || sym.capInit != nil) {
		return p.captured(sym, at) // an enclosing function's local
	}
	v := &varRef{node{sym.t, at}, sym}
	if in != nil && sym.kind == symFunc {
		if p.qualified == nil {
			p.qualified = map[expr]bool{}
		}
		p.qualified[v] = true
	}
	return p.unref(v)
}

// typeSpecEnd is the token offset after the simple type specifier at
// offset i: i if there is none.
func (p *parser) typeSpecEnd(i int) int {
	j := i
	named := false
	for {
		t := p.peekAt(j)
		if t.kind == tkIdent && (t.text == "struct" || t.text == "class" || t.text == "union" || t.text == "enum") && p.peekAt(j+1).kind == tkIdent {
			j += 2
			named = true
			continue
		}
		if t.kind == tkIdent && (typeWords[t.text] || cxxTypeWords[t.text]) {
			j++
			switch t.text {
			case "void", "char", "short", "int", "long", "float", "double", "signed", "unsigned", "bool", "_Bool",
				"wchar_t", "char8_t", "char16_t", "char32_t", "auto":
				// The type is said: a type's name after it is what is
				// declared (int S = 2; auto tm = f();, where S and tm
				// are classes too).
				named = true
			case "decltype", "typeof", "__typeof__", "__typeof", "__underlying_type", "__make_unsigned", "__make_signed", "alignas", "_Alignas", "_Atomic":
				// With its operand in parentheses.
				if p.isAt(j, "(") {
					depth := 0
					for ; ; j++ {
						if p.isAt(j, "(") {
							depth++
						} else if p.isAt(j, ")") {
							depth--
							if depth == 0 {
								j++
								break
							}
						} else if p.peekAt(j).kind == tkEOF {
							break
						}
					}
				}
			}
			continue
		}
		if !named {
			if _, n := p.cxxTypeAt(j); n > 0 {
				j += n
				named = true
				continue
			}
		}
		return j
	}
}

// castAhead reports whether the "(" next, with a type after it, starts
// a cast or a compound literal, not a parenthesized T(x) or T{x}.
func (p *parser) castAhead() bool {
	n := p.typeSpecEnd(1)
	if p.isAt(n, "{") {
		return false
	}
	if p.isAt(n, "(") {
		if _, m := p.memPtrAhead(n + 1); m > 0 {
			return true // (T (C::*)(...))
		}
		if p.isAt(n+1, ")") && p.isAt(n+2, ")") {
			// (T()): a cast to a function type, unless nothing that it
			// could convert follows.
			t := p.peekAt(n + 3)
			if t.kind == tkEOF {
				return false
			}
			if t.kind == tkPunct {
				switch t.text {
				case "(", "+", "-", "*", "&", "!", "~", "++", "--", "[", "::":
				default:
					return false
				}
			}
			return true
		}
		// (T() ...) not followed by ")" is a temporary's use: (T() * x).
		return p.isAt(n+1, "*") || p.isAt(n+1, "&") || p.isAt(n+1, "^") || p.isTypeStart(n+1) && p.paramsAhead(n)
	}
	return true
}

// declAhead reports whether the statement that starts with the type
// name next is a declaration, not an expression (T(x).f(); T{1};).
func (p *parser) declAhead() bool {
	t := p.peek()
	if t.kind == tkIdent && (typeWords[t.text] || cxxTypeWords[t.text]) {
		switch t.text {
		case "void", "char", "short", "int", "long", "float", "double", "signed", "unsigned", "bool", "_Bool",
			"wchar_t", "char8_t", "char16_t", "char32_t", "typename", "decltype", "auto":
		default:
			return true // a specifier only a declaration has
		}
	}
	n := p.typeSpecEnd(0)
	switch {
	case p.isAt(n, "{"):
		return false
	case p.isAt(n, "("):
		// T(x); and T(*p)... declare; T(1), T(a, b), T(x).f() do not.
		if _, m := p.memPtrAhead(n + 1); m > 0 {
			return true // T (C::*name)...
		}
		k := n + 1
		for p.isAt(k, "*") || p.isAt(k, "&") || p.isAt(k, "(") {
			k++
		}
		if p.peekAt(k).kind != tkIdent {
			return false
		}
		k++
		closed := false
		for p.isAt(k, ")") {
			k++
			closed = true
		}
		// (T(a, b) is an expression; T(a), b; declares.)
		return p.isAt(k, ";") || p.isAt(k, "=") || p.isAt(k, "[") || p.isAt(k, "(") || p.isAt(k, ",") && closed
	}
	if tk := p.peekAt(n); tk.kind == tkPunct {
		switch tk.text {
		case "*", "&", "&&", "::", "[", "<":
			return true
		case ";":
			return true
		}
		return false
	}
	return true
}

// returnValue is the return statement of the value e from a function
// that returns rt.
func (p *parser) returnValue(e expr, rt *ctype, at pos, mark int) stmt {
	if rt.kind == kStruct {
		return p.classReturn(e, rt, at, mark)
	}
	return &returnStmt{p.fullExpr(mark, p.initConvert(e, rt, at, "return"))}
}

// cxxUnary parses the unary expressions that are C++'s own, or that an
// operand of class type makes a function's call: ok is false for the
// rest.
func (p *parser) cxxUnary(t token, at pos) (expr, bool) {
	if t.kind == tkIdent {
		switch t.text {
		case "new":
			p.next()
			return p.newExpr(at), true
		case "delete":
			p.next()
			return p.deleteExpr(at), true
		}
		return nil, false
	}
	if t.kind == tkPunct && t.text == "::" && (p.isAt(1, "new") || p.isAt(1, "delete")) {
		p.next()
		if p.next().text == "new" {
			return p.newExpr(at), true
		}
		return p.deleteExpr(at), true
	}
	if t.kind != tkPunct {
		return nil, false
	}
	if t.text == "&" {
		return p.memberAddress(at)
	}
	switch t.text {
	case "+", "-", "~", "!", "*", "++", "--":
	default:
		return nil, false
	}
	// The operand first: if it is not of a class or an enumeration, the
	// operator is C's, parsed again.
	save := p.pos
	p.next()
	var x expr
	if t.text == "++" || t.text == "--" {
		x = p.unaryExpr()
	} else {
		x = p.castExpr()
	}
	if !overloadable(x.ty()) {
		p.pos = save
		return nil, false
	}
	if e := p.overloaded(t.text, []expr{x}, at); e != nil {
		return e, true
	}
	if x.ty().kind == kStruct {
		if t.text == "!" {
			return p.fold(&unary{node{tInt, at}, "!", p.boolOf(x, at)}), true
		}
		if t.text == "+" || t.text == "-" || t.text == "~" {
			// C's operator, on what the class's conversion function
			// to an arithmetic type gives.
			var to *ctype
			for _, f := range p.convFuncs(x.ty(), nil) {
				rt := f.t.fn.ret
				if rt.isRef() {
					rt = rt.elem
				}
				rt = rt.unqual()
				if f.explicit || f.deleted || !rt.isArith() || t.text == "~" && !rt.isInteger() {
					continue
				}
				if to != nil && !sameType(to, rt) {
					p.errorAt(at, "operator%s on %s is ambiguous: it converts to %s and to %s", t.text, x.ty(), to, rt)
				}
				to = rt
			}
			if to != nil {
				if c := p.convertClass(x, to, at); c != nil {
					v := p.rvalue(c)
					rt := promote(v.ty())
					v = p.convert(v, rt)
					if t.text == "+" {
						return v, true
					}
					return p.fold(&unary{node{rt, at}, t.text, v}), true
				}
			}
		}
		p.errorAt(at, "no match for operator%s (operand type is %s)", t.text, x.ty())
	}
	p.pos = save
	return nil, false
}

// classOperand is x as an operand of a built-in operator whose other
// operand is y: an object of class type is converted by its conversion
// function.
func (p *parser) classOperand(x, y expr, at pos) expr {
	if x.ty().kind != kStruct {
		return x
	}
	to := y.ty()
	if to.kind == kStruct || to.kind == kArray {
		to = tInt
	}
	if to.kind == kNullptr {
		// With nullptr: the pointer the class converts to, if it is to
		// one type of pointer.
		var via *ctype
		for _, f := range p.convFuncs(x.ty(), nil) {
			rt := f.t.fn.ret.unqual()
			if f.explicit || f.deleted || f.tmpl != nil || !rt.isPtr() || rt.isRef() {
				continue
			}
			if via != nil && !sameType(via, rt) {
				via = nil
				break
			}
			via = rt
		}
		if via != nil {
			if c := p.convertClass(x, via, at); c != nil {
				return c
			}
		}
	}
	if c := p.convertClass(x, valueType(&goVal{node{to, at}, ""}), at); c != nil {
		return c
	}
	if to.isInteger() {
		// With an integer: the pointer the class converts to, if it
		// is to one type of pointer and to nothing arithmetic (buf + n,
		// of an object that owns an array).
		var via *ctype
		for _, f := range p.convFuncs(x.ty(), nil) {
			rt := f.t.fn.ret.unqual()
			if f.explicit || f.deleted || f.tmpl != nil || !rt.isPtr() || rt.isRef() {
				continue
			}
			if via != nil && !sameType(via, rt) {
				via = nil
				break
			}
			via = rt
		}
		if via != nil {
			if c := p.convertClass(x, via, at); c != nil {
				return c
			}
		}
	}
	if to.isPtr() && to.elem.kind == kStruct {
		// To a pointer the other operand's converts to or from: a
		// pointer to a base, or to a derived class.
		var via *ctype
		for _, f := range p.convFuncs(x.ty(), nil) {
			rt := f.t.fn.ret.unqual()
			if f.explicit || f.deleted || f.tmpl != nil || !rt.isPtr() || rt.isRef() || rt.elem.kind != kStruct {
				continue
			}
			p.complete(to.elem)
			p.complete(rt.elem)
			if !isBaseOf(rt.elem, to.elem) && !isBaseOf(to.elem, rt.elem) {
				continue
			}
			if via != nil && !sameType(via, rt) {
				p.errorAt(at, "an operator with operands of types %s and %s is ambiguous", x.ty(), y.ty())
			}
			via = rt
		}
		if via != nil {
			if c := p.convertClass(x, via, at); c != nil {
				return c
			}
		}
	}
	p.errorAt(at, "no match for an operator with operands of types %s and %s", x.ty(), y.ty())
	return nil
}

// boolResult is e, the result of a comparison or a logical operator,
// which C types int, as C++'s bool.
func (p *parser) boolResult(e expr) expr {
	switch x := e.(type) {
	case *binary:
		if x.t.kind == kInt {
			x.t = tBool
			return x
		}
	case *unary:
		if x.op == "!" {
			x.t = tBool
			return x
		}
	case *intConst:
		return &intConst{node{tBool, x.p}, b2i(x.v != 0)}
	}
	return p.convert(e, tBool)
}

// skipQualName skips a qualified name that cannot be resolved, in a
// template's pattern: A<T>::B::c.
func (p *parser) skipQualName() {
	p.accept("::")
	for {
		p.accept("template")
		if p.peek().kind != tkIdent {
			return
		}
		p.next()
		if p.is("<") {
			p.skipAngles()
		}
		if !p.accept("::") {
			return
		}
	}
}

// skipStatement skips a statement without parsing it: a branch of if
// constexpr that is not taken.
func (p *parser) skipStatement() {
	switch {
	case p.is("{"):
		p.skipBraces()
		return
	case p.is("if"):
		p.next()
		p.accept("constexpr")
		p.skipParens()
		p.skipStatement()
		if p.accept("else") {
			p.skipStatement()
		}
		return
	case p.is("for") || p.is("while") || p.is("switch"):
		p.next()
		p.skipParens()
		p.skipStatement()
		return
	case p.is("do"):
		p.next()
		p.skipStatement()
		p.expect("while")
		p.skipParens()
		p.expect(";")
		return
	}
	depth := 0
	for {
		t := p.next()
		if t.kind == tkEOF {
			p.errorf("unterminated statement")
		}
		if t.kind != tkPunct {
			continue
		}
		switch t.text {
		case "(", "[", "{":
			depth++
		case ")", "]", "}":
			depth--
		case ";":
			if depth == 0 {
				return
			}
		}
	}
}

// afterDeclarator reports whether the token before the next one ends a
// declarator: a name, or a closing bracket.
func (p *parser) afterDeclarator() bool {
	t := p.peekAt(-1)
	return t.kind == tkIdent && !typeWords[t.text] && !cxxTypeWords[t.text] || t.kind == tkPunct && (t.text == ")" || t.text == "]")
}

// similarArrays reports whether a reference to the array type to may
// bind to an array of type at: the same but for qualifiers that to's
// elements add.
func similarArrays(to, at *ctype) bool {
	if to.kind != kArray || at.kind != kArray || to.n != at.n {
		return false
	}
	te, ae := to.elem, at.elem
	return te.q&ae.q == ae.q && (sameType(te.unqual(), ae.unqual()) || similarArrays(te, ae))
}
