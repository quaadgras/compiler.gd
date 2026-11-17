// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

// Initializers (C23 6.7.11): parsed into the list of stores they make,
// with designators and brace elision resolved.

// initExpr parses an initializer expression.
func (p *parser) initExpr() expr {
	return p.assignment()
}

// initializer parses the initializer of sym, after "=" (or a compound
// literal's braces), completing an array's length.
func (p *parser) initializer(sym *symbol) *initializer {
	t := sym.t
	if !p.is("{") && t.isScalar() {
		e := p.rvalue(p.initExpr())
		return &initializer{scalar: p.assignConvert(e, t, e.at(), "initializer")}
	}
	if t.isVLA() && p.is("{") && p.isAt(1, "}") {
		// C23: = {} zeroes a VLA, as it is allocated.
		p.next()
		p.next()
		return &initializer{}
	}
	if t.isVLA() && !p.is("{") {
		p.errorf("variable-sized object may not be initialized")
	}
	ip := &initParser{p: p, top: t}
	if t.kind == kArray && t.n < 0 {
		ip.open = true
	}
	if p.is("{") {
		ip.braced(t, 0)
	} else {
		e := p.initExpr()
		ip.single(t, 0, e)
	}
	if t.kind == kArray && t.n < 0 {
		n := (ip.maxEnd + t.elem.sizeof() - 1) / max(t.elem.sizeof(), 1)
		if t.elem.sizeof() == 0 {
			n = ip.count
		}
		sym.t = arrayOf(t.elem, n)
		sym.t.q, sym.t.align = t.q, t.align
	}
	if t.kind == kStruct && t.rec.flex && ip.maxEnd > t.sizeof() {
		// GCC: initializing a flexible array member makes the object
		// bigger, not its type.
		sym.allocSize = (ip.maxEnd + t.rec.align - 1) / t.rec.align * t.rec.align
	}
	if sym.t.isScalar() && len(ip.items) == 1 && ip.items[0].off == 0 {
		return &initializer{scalar: ip.items[0].x}
	}
	if sym.t.isScalar() && len(ip.items) == 0 {
		return &initializer{scalar: p.convert(&intConst{node{tInt, p.here()}, 0}, sym.t)}
	}
	return &initializer{items: ip.items}
}

// initFor is the initializer of a t from the value e.
func (p *parser) initFor(t *ctype, e expr) *initializer {
	if t.isScalar() {
		return &initializer{scalar: p.assignConvert(e, t, e.at(), "initializer")}
	}
	return &initializer{items: []initItem{{t: t, x: e}}}
}

type initParser struct {
	p      *parser
	items  []initItem
	maxEnd int64 // the end of the last element stored, for arrays of unknown length
	count  int64
	open   bool
	// An initializer expression already parsed, for the next scalar: a
	// struct's elements, when it turned out not to be the struct's value.
	pending expr
	top     *ctype           // the object's type
	unions  map[int64]*field // the member each union is initialized as
}

func (ip *initParser) add(it initItem) {
	ip.items = append(ip.items, it)
	end := it.off + it.t.sizeof()
	if end > ip.maxEnd {
		ip.maxEnd = end
	}
}

// single initializes the t at off from the expression e (no braces).
func (ip *initParser) single(t *ctype, off int64, e expr) {
	p := ip.p
	if t.kind == kArray {
		if s, ok := e.(*strLit); ok && isCharArrayFor(t, s) {
			ip.addString(t, off, s)
			return
		}
		p.errorAt(e.at(), "array must be initialized with a brace-enclosed initializer")
	}
	x := p.rvalue(e)
	if t.kind == kStruct {
		if x.ty().kind != kStruct || !compatible(x.ty().unqual(), t.unqual()) {
			p.errorAt(e.at(), "invalid initializer for %s", t)
		}
		ip.add(initItem{off: off, t: t, x: x})
		return
	}
	ip.add(initItem{off: off, t: t, x: p.assignConvert(x, t, e.at(), "initializer")})
}

func isCharArrayFor(t *ctype, s *strLit) bool {
	et := t.elem.unqual()
	st := s.t.elem
	if st.sizeof() == 1 {
		return et.kind == kChar || et.kind == kSChar || et.kind == kUChar
	}
	return et.sizeof() == st.sizeof() && et.isInteger()
}

func (ip *initParser) addString(t *ctype, off int64, s *strLit) {
	n := t.n
	if n < 0 {
		n = s.t.n
	}
	at := arrayOf(t.elem, n)
	ip.add(initItem{off: off, t: at, x: s})
}

// braced parses a braced initializer for the t at off.
func (ip *initParser) braced(t *ctype, off int64) {
	p := ip.p
	p.expect("{")
	if t.isScalar() {
		if p.accept("}") {
			ip.add(initItem{off: off, t: t, x: p.convert(&intConst{node{tInt, p.here()}, 0}, t)})
			return
		}
		e := p.initExprOrBraced(t, off, ip)
		if e != nil {
			ip.single(t, off, e)
		}
		p.accept(",")
		for !p.accept("}") {
			// Excess elements: GCC warns.
			if p.accept("{") {
				ip.skipBraced()
			} else {
				p.assignment()
			}
			if !p.accept(",") {
				p.expect("}")
				break
			}
		}
		return
	}
	if t.kind == kArray {
		// char a[] = {"..."}, or {("...")}.
		k := 0
		for p.isAt(k, "(") {
			k++
		}
		if p.peekAt(k).kind == tkString {
			j := k + 1
			for i := 0; i < k && p.isAt(j, ")"); i++ {
				j++
			}
			if j == 2*k+1 && (p.isAt(j, "}") || p.isAt(j, ",") && p.isAt(j+1, "}")) {
				e := p.initExpr()
				if s, ok := e.(*strLit); ok && isCharArrayFor(t, s) {
					ip.addString(t, off, s)
					p.accept(",")
					p.expect("}")
					return
				}
				p.errorAt(e.at(), "invalid initializer for %s", t)
			}
		}
	}
	if t.kind == kStruct && t.rec.union {
		// An initialized union's other bytes are zero.
		ip.add(initItem{off: off, t: t, zero: true})
	}
	ip.list(t, off, true, false)
}

func (p *parser) peekString() (*strLit, bool) {
	if p.peek().kind == tkString && (p.isAt(1, "}") || p.isAt(1, ",") && p.isAt(2, "}")) {
		t := p.peek()
		n := int64(len(t.str))/t.ty.sizeof() + 1
		return &strLit{node{arrayOf(t.ty, n), p.here()}, t.str}, true
	}
	return nil, false
}

// initExprOrBraced parses one initializer for a scalar: a nested brace
// level is the same scalar.
func (p *parser) initExprOrBraced(t *ctype, off int64, ip *initParser) expr {
	if p.is("{") {
		ip.braced(t, off)
		return nil
	}
	return p.initExpr()
}

// list parses the elements of the aggregate t at off, up to the "}"
// that ends its braces (if braced) or as many as it holds (if not, by
// brace elision). If desig, it starts with a designator of t's: the rest
// of a designation like [1].b, after which the following initializers
// continue in t (C23 6.7.11's current object).
func (ip *initParser) list(t *ctype, off int64, braced, desig bool) {
	p := ip.p
	var fields []*field
	if t.kind == kStruct {
		for _, f := range t.rec.fields {
			if f.name != "" || f.bits < 0 {
				fields = append(fields, f)
			}
		}
	}
	n := int64(-1)
	if t.kind == kArray {
		n = t.n
	} else {
		n = int64(len(fields))
		if t.rec.union {
			n = min(n, 1)
		}
	}
	elemAt := func(i int64) (*ctype, int64, *field) {
		if t.kind == kArray {
			return t.elem, off + i*t.elem.sizeof(), nil
		}
		f := fields[i]
		return f.t, off + f.off, f
	}
	top := t == ip.top
	idx := int64(0)
	first := true
	for {
		isDes := false
		if ip.pending == nil {
			if braced && p.accept("}") {
				return
			}
			if !braced && (p.is("}") || p.peek().kind == tkEOF) {
				return
			}
			isDes = p.is("[") || p.is(".")
			if isDes && !braced && !(first && desig) {
				return // the enclosing brace level's
			}
		}
		switch {
		case isDes:
			k, hi := ip.designator(t, off, fields)
			idx = k
			et, eoff, ef := elemAt(k)
			if t.kind == kStruct && t.rec.union {
				ip.unionMember(off, t, ef)
			}
			start := len(ip.items)
			if p.is("[") || p.is(".") {
				if !et.isAggregate() {
					p.errorf("designator for a member of a non-aggregate")
				}
				ip.list(et, eoff, false, true)
			} else {
				if !p.accept("=") {
					p.errorf("expected \"=\" after a designator")
				}
				ip.element(et, eoff, ef, true)
			}
			if hi > k {
				// GNU's [lo ... hi]: the same for each.
				size := t.elem.sizeof()
				items := append([]initItem(nil), ip.items[start:]...)
				for j := k + 1; j <= hi; j++ {
					for _, it := range items {
						c := it
						c.off += (j - k) * size
						ip.add(c)
					}
				}
				idx = hi
			}
			idx++
		case n >= 0 && idx >= n && !(t.kind == kArray && t.n < 0):
			// Excess initializers: GCC warns and drops them.
			if ip.pending != nil {
				ip.pending = nil
			} else if p.is("{") {
				p.next()
				ip.skipBraced()
			} else {
				p.assignment()
			}
		default:
			et, eoff, ef := elemAt(idx)
			if t.kind == kStruct && t.rec.union {
				ip.unionMember(off, t, ef)
			}
			ip.element(et, eoff, ef, false)
			idx++
		}
		if top && t.kind == kArray {
			ip.count = max(ip.count, idx)
		}
		first = false
		if !braced && n >= 0 && idx >= n && !(t.kind == kArray && t.n < 0) {
			return // full: what follows, a comma first, is the enclosing list's
		}
		if !braced && p.is(",") && (p.isAt(1, ".") || p.isAt(1, "[")) {
			return // a designator: the enclosing list's
		}
		if !p.accept(",") {
			if braced {
				p.expect("}")
			}
			return
		}
	}
}

// skipBraced skips a braced initializer, after its "{".
func (ip *initParser) skipBraced() {
	p := ip.p
	depth := 1
	for depth > 0 {
		t := p.next()
		switch {
		case t.kind == tkEOF:
			p.errorf("unterminated initializer")
		case t.kind == tkPunct && t.text == "{":
			depth++
		case t.kind == tkPunct && t.text == "}":
			depth--
		}
	}
}

// unionMember notes that the union at off is initialized as member f:
// its bytes are zeroed first if it was another.
func (ip *initParser) unionMember(off int64, t *ctype, f *field) {
	if ip.unions == nil {
		ip.unions = map[int64]*field{}
	}
	if cur, ok := ip.unions[off]; ok && cur == f {
		return
	}
	ip.unions[off] = f
	ip.add(initItem{off: off, t: t, zero: true})
}

// designator parses one designator of t, [i], [lo ... hi] or .f: the
// index of the element (or member) it designates, and for a range the
// last.
func (ip *initParser) designator(t *ctype, off int64, fields []*field) (int64, int64) {
	p := ip.p
	if p.accept("[") {
		if t.kind != kArray {
			p.errorf("array index in non-array initializer")
		}
		i := p.constInt(p.conditional())
		hi := i
		if p.accept("...") {
			hi = p.constInt(p.conditional())
		}
		p.expect("]")
		if i < 0 || hi < i || t.n >= 0 && hi >= t.n {
			p.errorf("array index in initializer exceeds array bounds")
		}
		return i, hi
	}
	p.expect(".")
	name := p.ident()
	if t.kind != kStruct {
		p.errorf("field name not in record or union initializer")
	}
	path := t.rec.lookupField(name)
	if path == nil {
		p.errorf("%s has no member named %q", t, name)
	}
	top := path[0]
	for i, f := range fields {
		if f == top {
			if len(path) > 1 {
				// A member of an anonymous member: designate that, then
				// the rest of the path inside it.
				p.pos -= 2
			}
			return int64(i), int64(i)
		}
	}
	p.errorf("%s has no member named %q", t, name)
	return 0, 0
}

// element parses the initializer of one element (or member): braced,
// a string for a char array, a value of its type, or, by brace elision,
// the first of its own elements.
func (ip *initParser) element(t *ctype, off int64, f *field, afterDesignator bool) {
	p := ip.p
	if p.is("{") && ip.pending == nil {
		if f != nil && f.bits >= 0 {
			p.expect("{")
			e := p.initExpr()
			p.accept(",")
			p.expect("}")
			ip.add(initItem{off: off, t: t, f: f, x: p.assignConvert(p.rvalue(e), t, e.at(), "initializer")})
			return
		}
		if t.kind == kArray && t.n == 0 && f != nil {
			// A flexible array member: as long as its initializer.
			t = arrayOf(t.elem, -1)
		}
		ip.braced(t, off)
		return
	}
	if t.kind == kArray && t.n == 0 && f != nil {
		// A flexible array member, by brace elision (GNU): the rest.
		t = arrayOf(t.elem, -1)
	}
	if t.isAggregate() {
		if t.kind == kArray && ip.pending == nil {
			if s, ok := p.peekStringAny(); ok && isCharArrayFor(t, s) {
				p.next()
				ip.addString(t, off, s)
				return
			}
			if k := p.parenString(); k > 0 {
				e := p.initExpr()
				if s, ok := e.(*strLit); ok && isCharArrayFor(t, s) {
					ip.addString(t, off, s)
					return
				}
				ip.pending = e
			}
		}
		if s, ok := ip.pending.(*strLit); ok && t.kind == kArray && isCharArrayFor(t, s) {
			ip.pending = nil
			ip.addString(t, off, s)
			return
		}
		// A struct value, or brace elision: parse the expression once,
		// and if it is not this struct's value it is the first of its
		// elements'.
		if t.kind == kStruct {
			e := ip.pending
			if e == nil {
				e = p.initExpr()
			}
			ip.pending = nil
			if et := e.ty(); et.kind == kStruct && compatible(et.unqual(), t.unqual()) {
				ip.add(initItem{off: off, t: t, x: p.rvalue(e)})
				return
			}
			ip.pending = e
		}
		if t.kind == kStruct && t.rec.union {
			ip.add(initItem{off: off, t: t, zero: true})
		}
		ip.list(t, off, false, false)
		return
	}
	e := ip.pending
	ip.pending = nil
	if e == nil {
		e = p.initExpr()
	}
	x := p.assignConvert(p.rvalue(e), t, e.at(), "initializer")
	if f != nil && f.bits >= 0 {
		ip.add(initItem{off: off, t: t, f: f, x: x})
		return
	}
	ip.add(initItem{off: off, t: t, x: x})
}

// parenString reports whether a string literal in parentheses is next:
// how many parentheses, or 0.
func (p *parser) parenString() int {
	k := 0
	for p.isAt(k, "(") {
		k++
	}
	if k == 0 || p.peekAt(k).kind != tkString {
		return 0
	}
	for i := 0; i < k; i++ {
		if !p.isAt(k+1+i, ")") {
			return 0
		}
	}
	return k
}

func (p *parser) peekStringAny() (*strLit, bool) {
	if p.peek().kind == tkString {
		t := p.peek()
		n := int64(len(t.str))/t.ty.sizeof() + 1
		return &strLit{node{arrayOf(t.ty, n), p.here()}, t.str}, true
	}
	return nil, false
}
