// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

// Parameter packs (cxxtmpl.go). An instance's pack is a symbol with its
// elements; a pattern followed by "..." is parsed once for each
// element, with the packs it names standing for that element.

// A paramPack is a function's parameter pack: its parameters are
// params[start : start+n] of the function.
type paramPack struct {
	name     string
	start, n int
}

// packElem is the element of the pack s that the expansion being parsed
// is at.
func (p *parser) packElem(s *symbol) *symbol {
	if !p.packOn {
		p.errorf("the parameter pack %q is not expanded", s.src)
	}
	i := p.packI
	if s.packSyms != nil || s.isParamPack {
		if i >= len(s.packSyms) {
			p.errorf("the parameter packs of an expansion differ in length")
		}
		return s.packSyms[i]
	}
	if i >= len(s.pack) {
		p.errorf("the parameter packs of an expansion differ in length")
	}
	a := s.pack[i]
	switch {
	case a.tmpl != nil:
		return a.tmpl.sym
	case a.isVal:
		vt := a.vt
		if vt == nil {
			vt = tInt
		}
		return &symbol{name: s.name, src: s.src, kind: symEnumConst, t: vt, val: a.v}
	}
	return &symbol{name: s.name, src: s.src, kind: symTypedef, t: a.t}
}

func packLen(s *symbol) int {
	if s.isParamPack {
		return len(s.packSyms)
	}
	return len(s.pack)
}

// packAhead looks at the list item at the parser's position, which the
// first "," or closing bracket outside brackets ends (angle: template
// brackets count, and ">" ends it): if it is a pack expansion, a
// pattern with "..." last (or, declaring a parameter, before its name),
// n is the length of the packs it names, dots the token of its "...",
// and end the token after the item.
func (p *parser) packAhead(angle bool) (n, dots, end int, ok bool) {
	depth, adepth := 0, 0
	dots = -1
	n = -1
	j := p.pos
scan:
	for ; ; j++ {
		t := p.toks[j]
		switch t.kind {
		case tkEOF:
			return 0, 0, 0, false
		case tkIdent:
			if n < 0 && dots < 0 {
				if s := p.lookup(t.text); s != nil && s.kind == symPack {
					n = packLen(s)
				}
			}
		case tkPunct:
			switch t.text {
			case "(", "[", "{":
				depth++
			case ")", "]", "}":
				if depth == 0 {
					break scan
				}
				depth--
			case "<":
				// A template's bracket: always in template arguments,
				// elsewhere after a template's name.
				if depth == 0 && (angle || p.templateNameAt(j-1)) {
					adepth++
				}
			case ">":
				if depth == 0 {
					if adepth == 0 {
						if angle {
							break scan
						}
						continue
					}
					adepth--
				}
			case ">>":
				if depth == 0 {
					if adepth < 2 {
						if angle {
							break scan
						}
						adepth = 0
						continue
					}
					adepth -= 2
				}
			case ",":
				if depth == 0 && adepth == 0 {
					break scan
				}
			case "...":
				if depth == 0 && adepth == 0 && j > p.pos {
					dots = j
				}
			}
		}
	}
	if dots < 0 || n < 0 {
		return 0, 0, 0, false
	}
	return n, dots, j, true
}

// expand parses the pack expansion found by packAhead with one, once
// for each element; then the parser is after the item.
func (p *parser) expand(n, end int, one func()) {
	start := p.pos
	saveOn, saveI := p.packOn, p.packI
	for i := 0; i < n; i++ {
		p.pos = start
		p.packOn, p.packI = true, i
		one()
	}
	p.packOn, p.packI = saveOn, saveI
	p.pos = end
}

// exprList parses a list of expressions to the closing bracket end,
// which it consumes: unconverted, with pack expansions spread.
func (p *parser) exprList(end string) []expr {
	var args []expr
	for !p.is(end) {
		if n, dots, e, ok := p.packAhead(false); ok && dots == e-1 {
			p.expand(n, e, func() { args = append(args, p.assignment()) })
		} else {
			args = append(args, p.assignment())
		}
		if !p.accept(",") {
			break
		}
	}
	p.expect(end)
	return args
}

// cxxBinary is x op y in C++: an overloaded operator's call, or C's
// operator.
func (p *parser) cxxBinary(at pos, op string, x, y expr) expr {
	if op == "," {
		y = p.rvalueVoid(y)
		return &binary{node{y.ty().unqual(), at}, ",", p.rvalueVoid(x), y}
	}
	if e := p.memPtrCompare(op, x, y, at); e != nil {
		return e
	}
	if overloadable(x.ty()) || overloadable(y.ty()) {
		if e := p.overloaded(op, []expr{x, y}, at); e != nil {
			return e
		}
		x, y = p.classOperand(x, y, at), p.classOperand(y, x, at)
	}
	e := p.binop(at, op, p.rvalue(x), p.rvalue(y))
	if prec := binPrec[op]; prec <= 7 && prec != 3 && prec != 4 && prec != 5 {
		e = p.boolResult(e) // &&, ||, comparisons
	}
	return e
}

// foldExpr parses a fold expression, (pack op ...), (... op pack) or
// one with an initial value, after its "(": ok is false, and nothing
// is parsed, if what follows is not one.
func (p *parser) foldExpr(at pos) (expr, bool) {
	// The "..." outside brackets, and the ")".
	depth, dots, end := 0, -1, -1
	for j := p.pos; ; j++ {
		t := p.toks[j]
		if t.kind == tkEOF {
			return nil, false
		}
		if t.kind != tkPunct {
			continue
		}
		switch t.text {
		case "(", "[", "{":
			depth++
		case ")", "]", "}":
			if depth == 0 {
				end = j
			}
			depth--
		case "...":
			if depth == 0 {
				dots = j
			}
		case ";":
			return nil, false
		}
		if end >= 0 {
			break
		}
	}
	if dots < 0 {
		return nil, false
	}
	isOp := func(j int) bool {
		t := p.toks[j]
		_, bin := binPrec[t.text]
		return t.kind == tkPunct && (bin || t.text == "," || isAssignOp(t.text))
	}
	var op string
	var patStart, patEnd, initStart, initEnd int // token ranges; init empty if none
	left := false
	switch {
	case dots == p.pos && isOp(dots+1):
		// (... op pack)
		op, patStart, patEnd, left = p.toks[dots+1].text, dots+2, end, true
	case dots == end-1 && isOp(dots-1):
		// (pack op ...)
		op, patStart, patEnd = p.toks[dots-1].text, p.pos, dots-1
	case isOp(dots-1) && isOp(dots+1):
		// (a op ... op b): the pack is the side that names one.
		op = p.toks[dots-1].text
		hasPack := false
		for j := p.pos; j < dots-1; j++ {
			if t := p.toks[j]; t.kind == tkIdent {
				if s := p.lookup(t.text); s != nil && s.kind == symPack {
					hasPack = true
				}
			}
		}
		if hasPack {
			patStart, patEnd, initStart, initEnd = p.pos, dots-1, dots+2, end
		} else {
			patStart, patEnd, initStart, initEnd, left = dots+2, end, p.pos, dots-1, true
		}
	default:
		return nil, false
	}
	n := -1
	for j := patStart; j < patEnd; j++ {
		if t := p.toks[j]; t.kind == tkIdent {
			if s := p.lookup(t.text); s != nil && s.kind == symPack {
				n = packLen(s)
				break
			}
		}
	}
	if n < 0 {
		return nil, false
	}
	operand := func() expr {
		if prec, ok := binPrec[op]; ok {
			return p.binaryExpr(prec)
		}
		return p.assignment()
	}
	var xs []expr
	saveOn, saveI := p.packOn, p.packI
	for i := 0; i < n; i++ {
		p.pos = patStart
		p.packOn, p.packI = true, i
		xs = append(xs, operand())
		if p.pos != patEnd {
			p.errorAt(at, "unexpected %s in a fold expression", p.peek())
		}
	}
	p.packOn, p.packI = saveOn, saveI
	var init expr
	if initEnd > initStart {
		p.pos = initStart
		init = operand()
	}
	p.pos = end + 1
	if left {
		if init != nil {
			xs = append([]expr{init}, xs...)
		}
	} else if init != nil {
		xs = append(xs, init)
	}
	if len(xs) == 0 {
		switch op {
		case "&&":
			return &intConst{node{tBool, at}, 1}, true
		case "||":
			return &intConst{node{tBool, at}, 0}, true
		case ",":
			return p.nothing(at), true
		}
		p.errorAt(at, "a fold over an empty pack with the operator %s", op)
	}
	var e expr
	if left {
		e = xs[0]
		for _, x := range xs[1:] {
			e = p.cxxBinary(at, op, e, x)
		}
	} else {
		e = xs[len(xs)-1]
		for i := len(xs) - 2; i >= 0; i-- {
			e = p.cxxBinary(at, op, xs[i], e)
		}
	}
	return e, true
}

// packInBraces reports whether the braced list next (after "=", if
// that is next) has a pack expansion among its elements.
func (p *parser) packInBraces() bool {
	j := 0
	if p.isAt(j, "=") {
		j++
	}
	if !p.isAt(j, "{") {
		return false
	}
	depth := 0
	for ; ; j++ {
		t := p.peekAt(j)
		if t.kind == tkEOF {
			return false
		}
		if t.kind != tkPunct {
			continue
		}
		switch t.text {
		case "{", "(", "[":
			depth++
		case "}", ")", "]":
			depth--
			if depth == 0 {
				return false
			}
		case "...":
			if depth == 1 {
				return true
			}
		}
	}
}

// templateNameAt reports whether the token at index j (not an offset)
// names a template: a "<" after it is a bracket.
func (p *parser) templateNameAt(j int) bool {
	if j < 0 {
		return false
	}
	t := p.toks[j]
	if t.kind != tkIdent {
		return false
	}
	s := p.lookup(t.text)
	if s == nil {
		return false
	}
	return s.kind == symTemplate || s.instOf != nil || s.kind == symFunc && hasTemplate(funcSet(s))
}
