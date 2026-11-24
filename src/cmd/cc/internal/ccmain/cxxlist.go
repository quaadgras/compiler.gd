// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

// Braced initializer lists where an expression is: return {a, b},
// f({1, 2}), x = {}, and within one another.

// An initList is a braced list: not a value, but what initializes the
// object, parameter or result that takes it.
type initList struct {
	node
	args []expr
}

// braceList parses a braced initializer list, at its "{".
func (p *parser) braceList() *initList {
	at := p.here()
	p.next()
	return &initList{node{tVoid, at}, p.exprList("}")}
}

// ilElem is the element type of t, if t is a std::initializer_list.
func ilElem(t *ctype) *ctype {
	if t.kind != kStruct || t.rec.inst == nil || t.rec.tag != "initializer_list" {
		return nil
	}
	if sc := t.rec.sc; sc == nil || sc.name != "std" {
		return nil
	}
	if args := t.rec.inst.args; len(args) == 1 && args[0].t != nil {
		return args[0].t
	}
	return nil
}

// listCtor is the constructor of the class t that takes a
// std::initializer_list whose elements the list l can initialize, and
// its element type: nil if t has none.
func (p *parser) listCtor(t *ctype, l *initList) (*symbol, *ctype) {
	for _, s := range ctors(t) {
		if s.tmpl != nil || len(s.t.fn.params) < 2 {
			continue
		}
		pt := s.t.fn.params[1]
		if pt.isRef() {
			pt = pt.elem
		}
		et := ilElem(pt.unqual())
		if et == nil {
			continue
		}
		ok := true
		for i := 2; i < len(s.t.fn.params); i++ {
			if i >= len(s.t.fn.defaults) || s.t.fn.defaults[i].start == 0 {
				ok = false
			}
		}
		for _, a := range l.args {
			if p.convRank(a, et) == rkNone {
				ok = false
			}
		}
		if ok {
			return s, et
		}
	}
	return nil, nil
}

// listRank ranks the conversion of the braced list l to a parameter of
// type pt.
func (p *parser) listRank(l *initList, pt *ctype) int {
	if pt.isRef() {
		if pt.ref == 1 && pt.elem.q&qConst == 0 {
			return rkNone
		}
		// The temporary it makes: T && binds it better than const T &.
		r := p.listRank(l, pt.elem)
		if r != rkNone && pt.ref == 1 {
			r++
		}
		return r
	}
	pt = pt.unqual()
	p.complete(pt)
	switch {
	case pt.kind == kStruct:
		if ilElem(pt) != nil {
			for _, a := range l.args {
				if p.convRank(a, ilElem(pt)) == rkNone {
					return rkNone
				}
			}
			return rkConv
		}
		ci := classOf(pt)
		if ci == nil || !ci.userCtor {
			// An aggregate: its members, in order.
			if len(l.args) > len(pt.rec.fields) && !(len(l.args) == 1 && sameType(l.args[0].ty().unqual(), pt)) {
				return rkNone
			}
			return rkUser
		}
		if s, _ := p.listCtor(pt, l); s != nil {
			return rkUser
		}
		p.userConv++
		s := p.resolve(ctors(pt), nil, l.args, l.p, "", true)
		p.userConv--
		if s == nil || s.deleted {
			return rkNone
		}
		return rkUser
	case pt.kind == kArray:
		return rkUser
	case len(l.args) == 0:
		return rkExact
	case len(l.args) == 1:
		return p.convRank(l.args[0], pt)
	}
	return rkNone
}

// listInit is the expression that initializes the object dst of type t
// with the braced list l.
func (p *parser) listInit(dst expr, t *ctype, l *initList, at pos) expr {
	t = t.unqual()
	p.complete(baseElem(t))
	switch {
	case t.kind == kStruct:
		if et := ilElem(t); et != nil {
			return p.constructAt(dst, t, p.ilArgs(et, l, at), at, mValue)
		}
		ci := classOf(t)
		if len(l.args) == 1 && l.args[0].ty().kind == kStruct && (sameType(l.args[0].ty().unqual(), t) || isBaseOf(t, l.args[0].ty())) {
			return p.constructAt(dst, t, l.args, at, mImplicit)
		}
		if ci != nil && ci.userCtor {
			if s, et := p.listCtor(t, l); s != nil {
				il := s.t.fn.params[1]
				if il.isRef() {
					il = il.elem
				}
				tmp := p.construct(il.unqual(), p.ilArgs(et, l, at), at, true)
				return p.callFunc(s, dst, []expr{tmp}, at)
			}
			return p.constructAt(dst, t, l.args, at, mValue)
		}
		// An aggregate: its members in order, the rest zero.
		list := []stmt{&exprStmt{p.zeroObject(dst, at)}}
		fields := t.rec.fields
		if t.rec.union && len(fields) > 1 {
			fields = fields[:1]
		}
		if len(l.args) > len(fields) {
			p.errorAt(at, "too many initializers for %s", t)
		}
		for i, a := range l.args {
			f := fields[i]
			list = append(list, &exprStmt{p.initWith(&member{node{f.t, at}, dst, f}, a, at)})
		}
		return &stmtExpr{node{tVoid, at}, &block{list: list}, nil}
	case t.kind == kArray:
		list := []stmt{&exprStmt{p.zeroObject(dst, at)}}
		if t.n >= 0 && int64(len(l.args)) > t.n {
			p.errorAt(at, "too many initializers for %s", t)
		}
		base := p.rvalue(dst)
		for i, a := range l.args {
			elem := &deref{node{t.elem, at}, &binary{node{ptrTo(t.elem), at}, "+", base, p.intc(int64(i), tLong, at)}}
			list = append(list, &exprStmt{p.initWith(elem, a, at)})
		}
		return &stmtExpr{node{tVoid, at}, &block{list: list}, nil}
	case len(l.args) == 0:
		return p.zeroObject(dst, at)
	case len(l.args) == 1:
		return p.initWith(dst, l.args[0], at)
	}
	p.errorAt(at, "too many initializers for a %s", t)
	return nil
}

// initWith is the expression that initializes the object dst with a: an
// expression, or a braced list.
func (p *parser) initWith(dst, a expr, at pos) expr {
	t := dst.ty()
	if l, ok := a.(*initList); ok {
		return p.listInit(dst, t, l, at)
	}
	switch {
	case t.kind == kStruct:
		return p.constructAt(dst, t, []expr{a}, at, mImplicit)
	case t.kind == kArray:
		if s, ok := a.(*strLit); ok {
			return p.copyBytes(dst, s, at)
		}
		p.errorAt(at, "an array is initialized with braces")
	}
	return p.initScalar(dst, a, at)
}

// ilArgs are the arguments of std::initializer_list<et>'s constructor
// for the list l: the first of an array of its elements, and their
// number.
func (p *parser) ilArgs(et *ctype, l *initList, at pos) []expr {
	n := int64(len(l.args))
	if n == 0 {
		return []expr{&intConst{node{ptrTo(et.qualified(qConst)), at}, 0}, p.intc(0, tULong, at)}
	}
	at2 := arrayOf(et.unqual(), n)
	arr := p.temporary(at2, at)
	ref := &varRef{node{arr.t, at}, arr}
	var pre []stmt
	for i, a := range l.args {
		elem := &deref{node{et.unqual(), at}, &binary{node{ptrTo(et.unqual()), at}, "+", p.rvalue(ref), p.intc(int64(i), tLong, at)}}
		pre = append(pre, &exprStmt{p.initWith(elem, a, at)})
	}
	first := &seqExpr{node{ptrTo(et.qualified(qConst)), at}, pre, &cast{node{ptrTo(et.qualified(qConst)), at}, p.rvalue(ref)}}
	return []expr{first, p.intc(n, tULong, at)}
}

// fromList is the value of type t that the braced list l makes: a
// class's prvalue, or a scalar.
func (p *parser) fromList(l *initList, t *ctype, at pos) expr {
	t = t.unqual()
	if t.kind == kStruct || t.kind == kArray {
		return &classPR{node{t, at}, func(dst expr) expr { return p.listInit(dst, t, l, at) }}
	}
	switch len(l.args) {
	case 0:
		return p.convertAt(p.intc(0, tInt, at), t, at)
	case 1:
		return p.initConvert(l.args[0], t, at, "initializer")
	}
	p.errorAt(at, "too many initializers for a %s", t)
	return nil
}
