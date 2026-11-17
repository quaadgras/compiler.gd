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
	if t.isVLA() && !p.is("{") {
		p.errorf("variable-sized object may not be initialized")
	}
	ip := &initParser{p: p}
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
		// bigger.
		c := *t
		r := *t.rec
		r.size = (ip.maxEnd + r.align - 1) / r.align * r.align
		c.rec = &r
		sym.t = &c
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
			p.assignment()
			if !p.accept(",") {
				p.expect("}")
				break
			}
		}
		return
	}
	if t.kind == kArray {
		if s, ok := p.peekString(); ok && isCharArrayFor(t, s) {
			// char a[] = {"..."}.
			p.next()
			ip.addString(t, off, s)
			p.accept(",")
			p.expect("}")
			return
		}
	}
	if t.kind == kStruct && t.rec.union {
		// An initialized union's other bytes are zero.
		ip.add(initItem{off: off, t: t, zero: true})
	}
	ip.list(t, off, true)
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
// brace elision).
func (ip *initParser) list(t *ctype, off int64, braced bool) {
	p := ip.p
	idx := int64(0) // the next element or field
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
		if t.n < 0 && !ip.open {
			n = -1
		}
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
	for {
		if braced && p.accept("}") {
			return
		}
		if !braced && (p.is("}") || n >= 0 && idx >= n && !p.is(".") && !p.is("[")) {
			return
		}
		if !braced && (p.is(".") || p.is("[")) && idx > 0 {
			// A designator belongs to an enclosing brace level.
			return
		}
		designated := false
		if p.is("[") || p.is(".") {
			if !braced && idx == 0 {
				return
			}
			et, eoff, ef, next := ip.designation(t, off, fields)
			designated = true
			idx = next
			ip.element(et, eoff, ef, true)
			idx++
			if t.kind == kArray && ip.open && off == 0 {
				ip.count = max(ip.count, idx)
			}
		} else {
			if n >= 0 && idx >= n {
				if t.kind == kStruct && t.rec.flex && idx == n {
					// No more members: excess.
				}
				// Excess initializers: GCC warns and drops them.
				p.assignment()
				if !p.accept(",") && braced {
					p.expect("}")
					return
				}
				continue
			}
			et, eoff, ef := elemAt(idx)
			if t.kind == kStruct && t.rec.union {
				ip.element(et, eoff, ef, false)
			} else {
				ip.element(et, eoff, ef, false)
			}
			idx++
			if t.kind == kArray && ip.open && off == 0 {
				ip.count = max(ip.count, idx)
			}
		}
		_ = designated
		if !p.accept(",") {
			if braced {
				p.expect("}")
			}
			return
		}
	}
}

// element parses the initializer of one element (or member): braced,
// a string for a char array, a value of its type, or, by brace elision,
// the first of its own elements.
func (ip *initParser) element(t *ctype, off int64, f *field, afterDesignator bool) {
	p := ip.p
	if p.is("{") {
		if f != nil && f.bits >= 0 {
			p.expect("{")
			e := p.initExpr()
			p.accept(",")
			p.expect("}")
			ip.add(initItem{off: off, t: t, f: f, x: p.assignConvert(p.rvalue(e), t, e.at(), "initializer")})
			return
		}
		ip.braced(t, off)
		return
	}
	if t.kind == kArray && t.n == 0 && f != nil && !afterDesignator {
		// A flexible array member with no braces: nothing.
		return
	}
	if t.isAggregate() {
		if t.kind == kArray {
			if s, ok := p.peekStringAny(); ok && isCharArrayFor(t, s) {
				p.next()
				ip.addString(t, off, s)
				return
			}
		}
		// A struct value, or brace elision.
		if t.kind == kStruct {
			save := p.pos
			e := p.initExpr()
			if et := e.ty(); et.kind == kStruct && compatible(et.unqual(), t.unqual()) {
				ip.add(initItem{off: off, t: t, x: p.rvalue(e)})
				return
			}
			p.pos = save
		}
		if t.kind == kStruct && t.rec.union {
			ip.add(initItem{off: off, t: t, zero: true})
		}
		ip.list(t, off, false)
		return
	}
	e := p.initExpr()
	x := p.assignConvert(p.rvalue(e), t, e.at(), "initializer")
	if f != nil && f.bits >= 0 {
		ip.add(initItem{off: off, t: t, f: f, x: x})
		return
	}
	ip.add(initItem{off: off, t: t, x: x})
}

func (p *parser) peekStringAny() (*strLit, bool) {
	if p.peek().kind == tkString {
		t := p.peek()
		n := int64(len(t.str))/t.ty.sizeof() + 1
		return &strLit{node{arrayOf(t.ty, n), p.here()}, t.str}, true
	}
	return nil, false
}

// designation parses a designation, [i], [i ... j] or .f, possibly
// nested, up to "=": the element it designates, and the index of the
// first designator in t.
func (ip *initParser) designation(t *ctype, off int64, fields []*field) (*ctype, int64, *field, int64) {
	p := ip.p
	var idx int64
	cur, curOff := t, off
	var cf *field
	first := true
	var rangeHi int64 = -1
	for p.is("[") || p.is(".") {
		if p.accept("[") {
			if cur.kind != kArray {
				p.errorf("array index in non-array initializer")
			}
			i := p.constInt(p.conditional())
			hi := i
			if p.accept("...") {
				hi = p.constInt(p.conditional())
			}
			p.expect("]")
			if i < 0 || cur.n >= 0 && hi >= cur.n && !(ip.open && cur == t) {
				p.errorf("array index in initializer exceeds array bounds")
			}
			if first {
				idx = i
				if hi != i {
					rangeHi = hi
				}
			}
			curOff += i * cur.elem.sizeof()
			if hi != i {
				// GNU's ranges: the rest repeats for each index.
				defer func(elem *ctype, base int64, lo, hi int64) {
					// Copy the items made for lo to lo+1..hi.
					var copies []initItem
					start := base + lo*elem.sizeof()
					end := start + elem.sizeof()
					for _, it := range ip.items {
						if it.off >= start && it.off < end {
							copies = append(copies, it)
						}
					}
					for k := lo + 1; k <= hi; k++ {
						for _, it := range copies {
							c := it
							c.off += (k - lo) * elem.sizeof()
							ip.add(c)
						}
					}
				}(cur.elem, curOff-i*cur.elem.sizeof(), i, hi)
			}
			cur, cf = cur.elem, nil
		} else {
			p.next()
			name := p.ident()
			if cur.kind != kStruct {
				p.errorf("field name not in record or union initializer")
			}
			path := cur.rec.lookupField(name)
			if path == nil {
				p.errorf("%s has no member named %q", cur, name)
			}
			if first {
				top := path[0]
				for i, f := range fields {
					if f == top {
						idx = int64(i)
					}
				}
			}
			for k, f := range path {
				if cur.rec.union && (k < len(path)-1 || true) {
					ip.add(initItem{off: curOff, t: cur, zero: true})
				}
				curOff += f.off
				cur, cf = f.t, f
			}
		}
		first = false
	}
	p.accept("=")
	if rangeHi >= 0 {
		idx = rangeHi
	}
	return cur, curOff, cf, idx
}
