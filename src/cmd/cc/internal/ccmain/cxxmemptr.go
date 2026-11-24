// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

// Pointers to members (doc/gd/cxx.md). A pointer to a data member is the
// member's offset, an integer (-1 is null). A pointer to a member
// function is two words, as in the Itanium ABI: a pointer to a function
// that takes this, and what to add to this before the call; for a
// virtual function the function is one that makes the virtual call.

// A memPtr says what a pointer to member points to: a member of type t
// of the class cls.
type memPtr struct {
	cls *ctype
	t   *ctype
}

// memPtrOf is the pointer to member that a type is, if it is one.
func memPtrOf(t *ctype) *memPtr {
	if t.kind == kStruct && t.rec.mp != nil {
		return t.rec.mp
	}
	return t.mp
}

func isPMF(t *ctype) bool { return t.kind == kStruct && t.rec.mp != nil }

// memPtrTo is the type of a pointer to a member of type t of the class
// cls.
func (p *parser) memPtrTo(cls, t *ctype) *ctype {
	cls = cls.unqual()
	if hasTParam(cls) || hasTParam(t) {
		return &ctype{kind: kMemPtr, elem: t, mp: &memPtr{cls: cls, t: t}}
	}
	if t.kind != kFunc {
		return &ctype{kind: kLong, mp: &memPtr{cls: cls, t: t}}
	}
	q := ""
	if t.fn.thisQ&qConst != 0 {
		q = "K"
	}
	key := rawType(cls) + ":" + q + rawType(t)
	if pt := p.pmfTypes[key]; pt != nil {
		return pt
	}
	r := &record{id: p.nextRecord(), sc: p.fileScope(), mp: &memPtr{cls: cls, t: t}}
	r.fields = []*field{{name: "__pfn", t: fnPtrType, bits: -1}, {name: "__adj", t: tLong, bits: -1}}
	r.layout()
	pt := &ctype{kind: kStruct, rec: r}
	if p.pmfTypes == nil {
		p.pmfTypes = map[string]*ctype{}
	}
	p.pmfTypes[key] = pt
	p.u.records = append(p.u.records, pt)
	return pt
}

// memPtrAhead reports whether C::* is next, a pointer to member's
// declarator: the class, and the tokens to the "*".
func (p *parser) memPtrAhead(i int) (*ctype, int) {
	if t := p.peekAt(i); t.kind != tkIdent && !(t.kind == tkPunct && t.text == "::") {
		return nil, 0
	}
	if in, j := p.nested(i); in != nil && in.kind == skClass && p.isAt(j, "*") {
		return in.rec.cls.t, j - i + 1
	}
	sym, n := p.cxxTypeAt(i)
	if sym == nil || !p.isAt(i+n, "::") || !p.isAt(i+n+1, "*") {
		return nil, 0
	}
	if sym.t.kind != kStruct && !hasTParam(sym.t) {
		return nil, 0
	}
	return sym.t, n + 2
}

// methodType is the type of the member function sym as a pointer to
// member has it: without this, with this's qualifiers.
func methodType(sym *symbol) *ctype {
	ft := *sym.t.fn
	ft.thisQ = ft.recv.elem.q
	ft.params = ft.params[1:]
	if len(ft.names) > 0 {
		ft.names = ft.names[1:]
	}
	ft.recv, ft.psyms, ft.defaults, ft.packs = nil, nil, nil, nil
	return &ctype{kind: kFunc, fn: &ft}
}

// A memFnRef is &C::f where f is overloaded: the one that the type it
// is converted to names.
type memFnRef struct {
	node
	cls *ctype
	set []*symbol
}

// memberAddress parses &C::m, the address of a non-static member, at
// its "&": ok is false, and nothing is parsed, if that is not next.
func (p *parser) memberAddress(at pos) (expr, bool) {
	sym, in, n := p.qualName(1)
	if sym == nil || in == nil || in.kind != skClass || p.isAt(1+n, "(") {
		return nil, false
	}
	name := p.peekAt(n).text
	cls := in.rec.cls.t
	switch sym.kind {
	case symField:
		path := in.rec.lookupField(name)
		if path == nil {
			return nil, false
		}
		off := int64(0)
		for _, f := range path {
			if f.bits >= 0 {
				p.errorAt(at, "the address of the bit-field %q", name)
			}
			off += f.off
		}
		p.pos += 1 + n
		ft := path[len(path)-1].t
		return &intConst{node{p.memPtrTo(cls, ft), at}, off}, true
	case symFunc:
		var set []*symbol
		for _, s := range funcSet(sym) {
			if s.tmpl == nil && s.t.fn.recv != nil {
				set = append(set, s)
			}
		}
		if len(set) == 0 {
			return nil, false // a static member function: a function's address
		}
		p.pos += 1 + n
		if len(set) == 1 {
			return p.pmfConst(set[0], at), true
		}
		return &memFnRef{node{p.memPtrTo(cls, methodType(set[0])), at}, cls, set}, true
	}
	return nil, false
}

// pmfConst is the pointer to the member function sym: an object of
// static duration.
func (p *parser) pmfConst(sym *symbol, at pos) expr {
	cls := sym.t.fn.recv.elem.unqual()
	t := p.memPtrTo(cls, methodType(sym))
	if c := p.pmfConsts[sym]; c != nil {
		return &varRef{node{t, at}, c}
	}
	target := sym
	if sym.virtual {
		// The function that calls it as the object's class has it.
		target = &symbol{name: sym.name + "$v", src: sym.src, kind: symFunc, t: sym.t, global: true, linkage: sym.linkage, p: at, sc: sym.sc, comdat: true, implicit: true}
		p.u.syms = append(p.u.syms, target)
		p.synth(target, func(ps []*symbol) []stmt {
			var args []expr
			for _, s := range ps[1:] {
				args = append(args, p.unref(&varRef{node{s.t, at}, s}))
			}
			call := p.callFunc(sym, p.thisObject(at), args, at)
			if sym.t.fn.ret.kind == kVoid {
				return []stmt{&exprStmt{p.rvalueVoid(call)}}
			}
			return []stmt{p.returnValue(call, sym.t.fn.ret, at, len(p.temps))}
		})
	}
	target.used = true
	p.use(target)
	p.symN++
	c := &symbol{name: "pmf." + itoa(int64(p.symN)), kind: symVar, t: t, global: true, storage: sStatic, p: at, defined: true, addr: true}
	fn := &cast{node{fnPtrType, at}, &decay{node{ptrTo(target.t), at}, &varRef{node{target.t, at}, target}}}
	c.init = &initializer{items: []initItem{{off: 0, t: fnPtrType, x: fn}, {off: fnPtrType.sizeof(), t: tLong, x: p.intc(0, tLong, at)}}}
	p.u.statics = append(p.u.statics, c)
	if p.pmfConsts == nil {
		p.pmfConsts = map[*symbol]*symbol{}
	}
	p.pmfConsts[sym] = c
	return &varRef{node{t, at}, c}
}

// pmfNull is the null pointer to member function of type t.
func (p *parser) pmfNull(t *ctype, at pos) expr {
	t = t.unqual()
	if c := p.pmfNulls[t.rec]; c != nil {
		return &varRef{node{t, at}, c}
	}
	p.symN++
	c := &symbol{name: "pmfnull." + itoa(int64(p.symN)), kind: symVar, t: t, global: true, storage: sStatic, p: at, defined: true, addr: true, init: &initializer{}}
	p.u.statics = append(p.u.statics, c)
	if p.pmfNulls == nil {
		p.pmfNulls = map[*record]*symbol{}
	}
	p.pmfNulls[t.rec] = c
	return &varRef{node{t, at}, c}
}

// pickMember is the function of the overloaded &C::f that the pointer
// to member function type t names: nil if none.
func (m *memFnRef) pick(t *ctype) *symbol {
	mp := memPtrOf(t)
	if mp == nil || mp.t.kind != kFunc {
		return nil
	}
	for _, s := range m.set {
		if st := methodType(s); sameType(st.fn.ret, mp.t.fn.ret) && sameParams(st.fn, mp.t.fn) && st.fn.thisQ == mp.t.fn.thisQ {
			return s
		}
	}
	return nil
}

// memPtrRank ranks the conversion of a to the pointer to member type pt:
// handled is false if it is not one that pointers to members have.
func (p *parser) memPtrRank(a expr, pt *ctype) (rank int, handled bool) {
	pt = pt.unqual()
	mp := memPtrOf(pt)
	if m, ok := a.(*memFnRef); ok {
		if mp != nil && m.pick(pt) != nil {
			return rkExact, true
		}
		return rkNone, true
	}
	amp := memPtrOf(a.ty())
	if mp == nil && amp == nil {
		return 0, false
	}
	switch {
	case mp == nil:
		if pt.kind == kBool {
			return rkConv, true
		}
		return rkNone, true
	case amp == nil:
		if a.ty().kind == kNullptr || isNullConst(a) {
			return rkConv, true
		}
		return rkNone, true
	}
	if !sameType(mp.t, amp.t) || mp.t.kind == kFunc && mp.t.fn.thisQ != amp.t.fn.thisQ {
		return rkNone, true
	}
	if sameType(mp.cls, amp.cls) {
		return rkExact, true
	}
	if isBaseOf(amp.cls, mp.cls) {
		return rkConv, true // a base's member is the derived class's
	}
	return rkNone, true
}

// toMemPtr is e converted to the pointer to member type t, if either is
// a pointer to member: nil if neither is.
func (p *parser) toMemPtr(e expr, t *ctype, at pos) expr {
	t = t.unqual()
	mp := memPtrOf(t)
	if m, ok := e.(*memFnRef); ok {
		s := m.pick(t)
		if s == nil {
			if mp != nil {
				p.errorAt(at, "no member function %s of this type: %s", m.set[0].src, t)
			}
			s = m.set[0]
		}
		e = p.pmfConst(s, at)
	}
	amp := memPtrOf(e.ty())
	if mp == nil && amp == nil {
		return nil
	}
	switch {
	case mp == nil:
		if t.kind == kBool {
			return p.memPtrTruth(e, at)
		}
		if p.castingMemPtr {
			return nil
		}
		p.errorAt(at, "a pointer to member is not converted to %s", t)
	case amp == nil:
		if e.ty().kind != kNullptr && !isNullConst(e) {
			p.errorAt(at, "%s is not converted to a pointer to member", e.ty())
		}
		if isPMF(t) {
			return p.pmfNull(t, at)
		}
		return &intConst{node{t, at}, -1}
	}
	if sameType(t, e.ty().unqual()) {
		return e
	}
	off, ok := baseOffset(amp.cls.rec, mp.cls.rec)
	if !ok {
		// To a base's (a cast): the derived class's place taken off.
		if back, isBase := baseOffset(mp.cls.rec, amp.cls.rec); isBase {
			off, ok = -back, true
		}
	}
	if !p.castingMemPtr && (!ok && !sameType(mp.cls, amp.cls) || !sameType(mp.t, amp.t)) {
		p.errorAt(at, "%s is not converted to %s", e.ty(), t)
	}
	if !isPMF(t) {
		// A data member's offset, in the derived class.
		v := p.rvalue(e)
		if c, ok := v.(*intConst); ok {
			if c.v < 0 {
				return &intConst{node{t, at}, c.v}
			}
			return &intConst{node{t, at}, c.v + off}
		}
		return &cast{node{t, at}, &binary{node{tLong, at}, "+", &cast{node{tLong, at}, v}, p.intc(off, tLong, at)}}
	}
	// The same function, with the base's place in the derived class
	// added to this.
	tmp := p.temporary(t, at)
	ref := &varRef{node{t, at}, tmp}
	src := p.addressable(e, at)
	f0, f1 := t.rec.fields[0], t.rec.fields[1]
	s0, s1 := e.ty().rec.fields[0], e.ty().rec.fields[1]
	pre := []stmt{
		&exprStmt{&assign{node{f0.t, at}, "=", &member{node{f0.t, at}, ref, f0}, &member{node{s0.t, at}, src, s0}, f0.t}},
		&exprStmt{&assign{node{tLong, at}, "=", &member{node{tLong, at}, ref, f1},
			&binary{node{tLong, at}, "+", &member{node{tLong, at}, src, s1}, p.intc(off, tLong, at)}, tLong}},
	}
	return p.xvalue(&seqExpr{node{t, at}, pre, ref})
}

// addressable is e, a struct's value, as an object whose members can be
// named: itself, or a temporary of it.
func (p *parser) addressable(e expr, at pos) expr {
	if pr, ok := e.(*classPR); ok {
		return p.materializePR(pr)
	}
	if isLvalue(e) {
		return e
	}
	if _, ok := e.(*seqExpr); ok && p.xvalues[e] {
		return e
	}
	return p.materialize(e, e.ty())
}

// memPtrTruth is whether the pointer to member e is not null.
func (p *parser) memPtrTruth(e expr, at pos) expr {
	if isPMF(e.ty()) {
		f0 := e.ty().rec.fields[0]
		fn := &member{node{f0.t, at}, p.addressable(e, at), f0}
		return &binary{node{tBool, at}, "!=", fn, &intConst{node{f0.t, at}, 0}}
	}
	return &binary{node{tBool, at}, "!=", &cast{node{tLong, at}, p.rvalue(e)}, p.intc(-1, tLong, at)}
}

// memPtrCompare is x == y or x != y where one is a pointer to member
// function: nil if neither is.
func (p *parser) memPtrCompare(op string, x, y expr, at pos) expr {
	if op != "==" && op != "!=" {
		return nil
	}
	_, xm := x.(*memFnRef)
	_, ym := y.(*memFnRef)
	xp, yp := isPMF(x.ty()) || xm, isPMF(y.ty()) || ym
	if !xp && !yp {
		return nil
	}
	t := x.ty()
	if !xp {
		t = y.ty()
	}
	x, y = p.toMemPtr(x, t, at), p.toMemPtr(y, t, at)
	xs, ys := p.addressable(x, at), p.addressable(y, at)
	f0, f1 := t.rec.fields[0], t.rec.fields[1]
	eq := &binary{node{tInt, at}, "&&",
		&binary{node{tInt, at}, "==", &member{node{f0.t, at}, xs, f0}, &member{node{f0.t, at}, ys, f0}},
		&binary{node{tInt, at}, "==", &member{node{tLong, at}, xs, f1}, &member{node{tLong, at}, ys, f1}}}
	if op == "!=" {
		return p.boolResult(&unary{node{tInt, at}, "!", eq})
	}
	return p.boolResult(eq)
}

// A pmfCall is obj.*pmf, a member function to call: only a call's
// operand.
type pmfCall struct {
	node
	obj expr
	pmf expr
}

// memberPointerAccess parses the right operand of .* or ->*, after the
// operator: x.*pm.
func (p *parser) memberPointerAccess(x expr, arrow bool, at pos) expr {
	if pr, ok := x.(*classPR); ok {
		x = p.materializePR(pr)
	}
	if arrow {
		b := p.rvalue(x)
		if !b.ty().isPtr() {
			p.errorAt(at, "invalid type argument of ->* (have %s)", b.ty())
		}
		x = p.derefOf(b, at)
	}
	p.noPM++
	pm := p.castExpr()
	p.noPM--
	if m, ok := pm.(*memFnRef); ok {
		pm = p.pmfConst(m.set[0], at)
	}
	mp := memPtrOf(pm.ty())
	if mp == nil {
		p.errorAt(at, "the right operand of .* is not a pointer to member (%s)", pm.ty())
	}
	p.complete(x.ty())
	if isPMF(pm.ty()) {
		return &pmfCall{node{mp.t, at}, x, pm}
	}
	mt := qualifyDeep(mp.t, x.ty().q)
	base := &cast{node{ptrTo(tChar), at}, p.baseAddr(p.addressOf(x, at), mp.cls, at)}
	return &deref{node{mt, at}, &cast{node{ptrTo(mt), at}, &binary{node{ptrTo(tChar), at}, "+", base, &cast{node{tLong, at}, p.rvalue(pm)}}}}
}

// callPMF is the call of the member function c names with args.
func (p *parser) callPMF(c *pmfCall, args []expr, at pos) expr {
	pt := c.pmf.ty()
	mp := memPtrOf(pt)
	mft := mp.t.fn
	if len(args) < len(mft.params) || len(args) > len(mft.params) && !mft.variadic {
		p.errorAt(at, "wrong number of arguments to a member function called through a pointer")
	}
	// The pointer, once.
	pm := p.addressable(c.pmf, at)
	var pre []stmt
	if !noEffects(pm) {
		tmp := p.temporary(pt, at)
		ref := &varRef{node{pt.unqual(), at}, tmp}
		pre = append(pre, &exprStmt{&assign{node{pt.unqual(), at}, "=", ref, p.rvalue(pm), pt.unqual()}})
		pm = ref
	}
	f0, f1 := pt.rec.fields[0], pt.rec.fields[1]
	recv := ptrTo(mp.cls.qualified(mft.thisQ))
	full := withRecv(mft, mp.cls)
	this := &cast{node{recv, at}, &binary{node{ptrTo(tChar), at}, "+",
		&cast{node{ptrTo(tChar), at}, p.baseAddr(p.addressOf(c.obj, at), mp.cls, at)}, &member{node{tLong, at}, pm, f1}}}
	out := []expr{this}
	for i, a := range args {
		if i < len(mft.params) {
			out = append(out, p.passArg(a, mft.params[i], at))
		} else {
			v := p.rvalue(a)
			out = append(out, p.convert(v, promoteArg(v.ty())))
		}
	}
	fnT := &ctype{kind: kFunc, fn: full}
	fn := &cast{node{ptrTo(fnT), at}, &member{node{f0.t, at}, pm, f0}}
	return p.wrapPre(pre, p.callResult(&call{node{full.ret.unqual(), at}, fn, full, out}, full, at))
}
