// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

// C++ classes (doc/gd/cxx.md).

// classInfo is what a class has that a C struct has not.
type classInfo struct {
	scope *scope // its members' names
}

// A methodRef is a member function named on an object (x.f, p->f): only
// a call's operand.
type methodRef struct {
	node
	obj expr
	set []*symbol
}

func (s *scope) findMember(name string, seen map[*scope]bool) *symbol { return nil }

// isBaseOf reports whether base is a base class of derived (not the
// same class).
func isBaseOf(base, derived *ctype) bool { return false }

// baseAddr is ptr, a pointer to a class, converted to point to its base
// to, if to is one.
func (p *parser) baseAddr(ptr expr, to *ctype, at pos) expr { return ptr }

// derivedAddr is ptr, a pointer to a class, converted to point to the
// class to derived from it, if to is one.
func (p *parser) derivedAddr(ptr expr, to *ctype, at pos) expr { return ptr }

// classTemp is a temporary of the class t initialized with e.
func (p *parser) classTemp(t *ctype, e expr, at pos, what string) expr {
	if !sameType(t.unqual(), e.ty().unqual()) {
		p.errorAt(at, "cannot convert %s to %s", e.ty(), t)
	}
	return p.materialize(p.rvalue(e), t)
}

// convertClass is e, of class type, converted to the type t that is not
// its class: nil if it has no conversion.
func (p *parser) convertClass(e expr, t *ctype, at pos) expr { return nil }

// construct is a temporary of the class t, made of args.
func (p *parser) construct(t *ctype, args []expr, at pos, explicit bool) expr {
	switch len(args) {
	case 0:
		// Value initialization: zero.
		sym := p.temporary(t, at)
		sym.init = &initializer{}
		ref := &varRef{node{sym.t, at}, sym}
		var pre []stmt
		if p.fn != nil {
			pre = []stmt{&declStmt{syms: []*symbol{sym}}}
		}
		return p.xvalue(&seqExpr{node{sym.t, at}, pre, ref})
	case 1:
		if sameType(t.unqual(), args[0].ty().unqual()) {
			return args[0]
		}
	}
	p.errorAt(at, "no matching constructor for %s", t)
	return nil
}

func (p *parser) classConvRank(a expr, vt, pt *ctype) int {
	if sameType(vt, pt) {
		return rkExact
	}
	return rkNone
}

// classArg is a passed by value to a parameter of the class pt.
func (p *parser) classArg(a expr, pt *ctype, at pos) expr {
	return p.assignConvert(p.rvalue(a), pt, at, "argument")
}

// classResult is the value of the call c, which returns a class.
func (p *parser) classResult(c *call, at pos) expr { return c }

func (p *parser) thisObject(at pos) expr {
	if p.this == nil {
		p.errorAt(at, "invalid use of this outside a non-static member function")
	}
	return &deref{node{p.this.t.elem, at}, &varRef{node{p.this.t, at}, p.this}}
}

// memberCall is the call of x's member function name with args: nil if
// x's class has none.
func (p *parser) memberCall(x expr, name string, args []expr, at pos) expr { return nil }

// specialMember parses a declaration with no type specifier: a
// constructor, destructor or conversion function, declared outside its
// class.
func (p *parser) specialMember(s specs, at pos) {
	p.errorAt(at, "expected a type")
}

// classInit parses the initializer of the class object sym, if C++
// initializes it otherwise than C. It reports whether it did.
func (p *parser) classInit(sym *symbol, s specs, at pos) bool { return false }

// classInitFrom initializes the class object sym with e.
func (p *parser) classInitFrom(sym *symbol, e expr, at pos) {
	sym.init = &initializer{items: []initItem{{t: sym.t, x: p.rvalue(e)}}}
}

// fieldRef is the non-static data member sym named by itself (or
// qualified by the scope in): this's.
func (p *parser) fieldRef(sym *symbol, in *scope, at pos) expr {
	p.errorAt(at, "invalid use of the member %q", sym.src)
	return nil
}

// classReturn is the return statement of e from a function that returns
// the class rt.
func (p *parser) classReturn(e expr, rt *ctype, at pos) stmt {
	return &returnStmt{p.assignConvert(p.rvalue(e), rt, at, "return")}
}
