// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"math"
)

// Expressions: parsed, checked and typed (C23 6.5).

func (p *parser) expr() expr {
	e := p.assignment()
	for p.is(",") {
		at := p.here()
		p.next()
		y := p.rvalueVoid(p.assignment())
		e = &binary{node{y.ty(), at}, ",", p.rvalueVoid(e), y}
	}
	return e
}

// rvalueVoid is e evaluated for its effects: any type, void included.
func (p *parser) rvalueVoid(e expr) expr {
	if e.ty().kind == kVoid {
		return e
	}
	return p.rvalue(e)
}

func isAssignOp(s string) bool {
	switch s {
	case "=", "+=", "-=", "*=", "/=", "%=", "<<=", ">>=", "&=", "|=", "^=":
		return true
	}
	return false
}

func (p *parser) assignment() expr {
	x := p.conditional()
	t := p.peek()
	if t.kind != tkPunct || !isAssignOp(t.text) {
		return x
	}
	at := p.here()
	p.next()
	y := p.rvalue(p.assignment())
	return p.makeAssign(at, t.text, x, y)
}

func (p *parser) makeAssign(at pos, op string, x, y expr) expr {
	p.checkModifiable(x, at)
	xt := x.ty()
	if op == "=" {
		return &assign{node{xt.unqual(), at}, "=", x, p.assignConvert(y, xt, at, "assignment"), xt}
	}
	bop := op[:len(op)-1]
	// x op= y is x = x op y, with x evaluated once.
	if xt.isPtr() && (bop == "+" || bop == "-") {
		if !y.ty().isInteger() {
			p.errorAt(at, "invalid operands to %s", op)
		}
		p.checkPtrArith(xt, at)
		return &assign{node{xt.unqual(), at}, op, x, p.convert(y, tLong), xt}
	}
	if !xt.isArith() || !y.ty().isArith() {
		p.errorAt(at, "invalid operands to %s (have %s and %s)", op, xt, y.ty())
	}
	var ct *ctype
	if bop == "<<" || bop == ">>" {
		if !xt.isInteger() || !y.ty().isInteger() {
			p.errorAt(at, "invalid operands to %s", op)
		}
		ct = promote(xt)
		y = p.convert(y, promote(y.ty()))
	} else {
		ct = usual(xt, y.ty())
		if (bop == "%" || bop == "&" || bop == "|" || bop == "^") && !ct.isInteger() {
			p.errorAt(at, "invalid operands to %s", op)
		}
		y = p.convert(y, ct)
	}
	return &assign{node{xt.unqual(), at}, op, x, y, ct}
}

func (p *parser) checkModifiable(x expr, at pos) {
	if !isLvalue(x) {
		p.errorAt(at, "lvalue required as left operand of assignment")
	}
	t := x.ty()
	if t.q&qConst != 0 {
		p.errorAt(at, "assignment of read-only location")
	}
	if t.kind == kArray {
		p.errorAt(at, "assignment to expression with array type")
	}
	if t.kind == kStruct && hasConstMember(t) {
		p.errorAt(at, "assignment of read-only location")
	}
}

func hasConstMember(t *ctype) bool {
	for _, f := range t.rec.fields {
		if f.t.q&qConst != 0 || f.t.kind == kStruct && hasConstMember(f.t) {
			return true
		}
	}
	return false
}

func isLvalue(e expr) bool {
	switch e := e.(type) {
	case *varRef:
		return e.sym.kind == symVar
	case *deref, *compoundLit, *strLit:
		return true
	case *member:
		return isLvalue(e.x) || isStructValue(e.x)
	}
	return false
}

// isStructValue reports whether e is a struct value that lives in a
// temporary (a call's result, an assignment's value...): its members are
// not lvalues, but cmd/cc can still reach them in memory.
func isStructValue(e expr) bool {
	return e.ty().kind == kStruct
}

func (p *parser) conditional() expr {
	c := p.binaryExpr(0)
	if !p.is("?") {
		return c
	}
	at := p.here()
	p.next()
	c = p.scalarCond(p.rvalue(c), at)
	if v, ok := p.foldTruth(c); ok {
		if _, isInt := c.(*intConst); !isInt {
			c = p.intc(b2i(v), tInt, at)
		}
	}
	var x expr
	if p.is(":") {
		// GNU's c ?: y.
		x = c
	} else {
		x = p.rvalueVoid(p.expr())
	}
	p.expect(":")
	y := p.rvalueVoid(p.conditional())
	if cv, ok := p.foldInt(c); ok && isConstExpr(x) && isConstExpr(y) {
		// Keep it a constant expression.
		t := p.condType(x, y, at)
		if cv != 0 {
			return p.convert(x, t)
		}
		return p.convert(y, t)
	}
	t := p.condType(x, y, at)
	if t.kind != kVoid && t.kind != kStruct {
		x, y = p.convert(x, t), p.convert(y, t)
	}
	return &cond{node{t, at}, c, x, y}
}

func isConstExpr(e expr) bool {
	switch e.(type) {
	case *intConst, *floatConst:
		return true
	}
	return false
}

// condType is the type of c ? x : y.
func (p *parser) condType(x, y expr, at pos) *ctype {
	xt, yt := x.ty(), y.ty()
	switch {
	case xt.isArith() && yt.isArith():
		return usual(xt, yt)
	case xt.kind == kStruct && yt.kind == kStruct && compatible(xt.unqual(), yt.unqual()):
		return xt.unqual()
	case xt.kind == kVoid || yt.kind == kVoid:
		return tVoid
	case xt.kind == kNullptr && yt.kind == kNullptr:
		return tNullptr
	case xt.isPtr() && (isNullConst(y) || yt.kind == kNullptr):
		return xt
	case yt.isPtr() && (isNullConst(x) || xt.kind == kNullptr):
		return yt
	case xt.isPtr() && yt.isPtr():
		q := xt.elem.q | yt.elem.q
		if xt.elem.kind == kVoid {
			return ptrTo(xt.elem.qualified(q))
		}
		if yt.elem.kind == kVoid {
			return ptrTo(yt.elem.qualified(q))
		}
		if !compatible(xt.elem.unqual(), yt.elem.unqual()) {
			// GCC warns; the first.
			return xt
		}
		return ptrTo(composite(xt.elem, yt.elem).qualified(q))
	case xt.isPtr() && yt.isInteger():
		return xt
	case yt.isPtr() && xt.isInteger():
		return yt
	}
	p.errorAt(at, "type mismatch in conditional expression (%s and %s)", xt, yt)
	return nil
}

var binPrec = map[string]int{
	"||": 1, "&&": 2, "|": 3, "^": 4, "&": 5, "==": 6, "!=": 6,
	"<": 7, ">": 7, "<=": 7, ">=": 7, "<<": 8, ">>": 8, "+": 9, "-": 9, "*": 10, "/": 10, "%": 10,
}

func (p *parser) binaryExpr(min int) expr {
	x := p.castExpr()
	for {
		t := p.peek()
		prec, ok := binPrec[t.text]
		if t.kind != tkPunct || !ok || prec <= min {
			return x
		}
		at := p.here()
		p.next()
		y := p.binaryExpr(prec)
		x = p.binop(at, t.text, p.rvalue(x), p.rvalue(y))
	}
}

// binop types x op y, both rvalues.
func (p *parser) binop(at pos, op string, x, y expr) expr {
	xt, yt := x.ty(), y.ty()
	switch op {
	case "&&", "||":
		x, y = p.scalarCond(x, at), p.scalarCond(y, at)
		if xv, ok := p.foldTruth(x); ok {
			if yv, ok := p.foldTruth(y); ok {
				if op == "&&" {
					return p.intc(b2i(xv && yv), tInt, at)
				}
				return p.intc(b2i(xv || yv), tInt, at)
			}
			// Short-circuited: the result is known.
			if op == "&&" && !xv {
				return p.intc(0, tInt, at)
			}
			if op == "||" && xv {
				return p.intc(1, tInt, at)
			}
		}
		// Known but for x's effects: (x, value).
		if eff, xv, ok := constCond(x); ok && eff != nil && (op == "&&" && !xv || op == "||" && xv) {
			return &binary{node{tInt, at}, ",", eff, p.intc(b2i(xv), tInt, at)}
		}
		if yv, ok := p.foldTruth(y); ok && (op == "&&" && !yv || op == "||" && yv) {
			if _, isConst := x.(*intConst); !isConst {
				return &binary{node{tInt, at}, ",", x, p.intc(b2i(yv), tInt, at)}
			}
		}
		return &binary{node{tInt, at}, op, x, y}
	case "+":
		if xt.isInteger() && yt.isPtr() {
			x, y, xt, yt = y, x, yt, xt
		}
		if xt.isPtr() && yt.isInteger() {
			p.checkPtrArith(xt, at)
			y = p.convert(y, tLong)
			if c, ok := p.ptrConstAdd(x, y, false); ok {
				return c
			}
			return &binary{node{xt, at}, "+", x, y}
		}
	case "-":
		if xt.isPtr() && yt.isInteger() {
			p.checkPtrArith(xt, at)
			y = p.convert(y, tLong)
			if c, ok := p.ptrConstAdd(x, y, true); ok {
				return c
			}
			return &binary{node{xt, at}, "-", x, y}
		}
		if xt.isPtr() && yt.isPtr() {
			if !compatible(xt.elem.unqual(), yt.elem.unqual()) {
				p.errorAt(at, "invalid operands to binary - (have %s and %s)", xt, yt)
			}
			p.checkPtrArith(xt, at)
			cx, ok1 := x.(*intConst)
			cy, ok2 := y.(*intConst)
			if sz := elemSizeOf(xt); ok1 && ok2 && sz > 0 {
				return p.intc((cx.v-cy.v)/sz, tLong, at)
			}
			return &binary{node{tLong, at}, "-", x, y}
		}
	case "==", "!=", "<", ">", "<=", ">=":
		if xt.isPtr() || yt.isPtr() || xt.kind == kNullptr || yt.kind == kNullptr {
			eq := op == "==" || op == "!="
			switch {
			case xt.isPtr() && yt.isPtr():
			case eq && (xt.isPtr() || xt.kind == kNullptr) && (isNullConst(y) || yt.kind == kNullptr):
			case eq && (yt.isPtr() || yt.kind == kNullptr) && (isNullConst(x) || xt.kind == kNullptr):
			case xt.isPtr() && yt.isInteger() || yt.isPtr() && xt.isInteger():
				// GCC allows it, with a warning.
			default:
				p.errorAt(at, "invalid operands to %s (have %s and %s)", op, xt, yt)
			}
			cx, ok1 := x.(*intConst)
			cy, ok2 := y.(*intConst)
			if ok1 && ok2 {
				v, _ := foldOp(op, tULong, cx.v, cy.v)
				return p.intc(v, tInt, at)
			}
			return &binary{node{tInt, at}, op, p.toPtrish(x), p.toPtrish(y)}
		}
	}
	if !xt.isArith() || !yt.isArith() {
		p.errorAt(at, "invalid operands to binary %s (have %s and %s)", op, xt, yt)
	}
	if op == "<<" || op == ">>" {
		if !xt.isInteger() || !yt.isInteger() {
			p.errorAt(at, "invalid operands to binary %s", op)
		}
		t := promote(xt)
		return p.fold(&binary{node{t, at}, op, p.convert(x, t), p.convert(y, promote(yt))})
	}
	t := usual(xt, yt)
	if (op == "%" || op == "&" || op == "|" || op == "^") && !t.isInteger() {
		p.errorAt(at, "invalid operands to binary %s (have %s and %s)", op, xt, yt)
	}
	rt := t
	switch op {
	case "==", "!=", "<", ">", "<=", ">=":
		rt = tInt
	}
	return p.fold(&binary{node{rt, at}, op, p.convert(x, t), p.convert(y, t)})
}

// constexprLoad is the value of a scalar in a constexpr object at a
// constant offset (arr[2], s.f), a constant.
func (p *parser) constexprLoad(e expr) (expr, bool) {
	if !e.ty().isScalar() {
		return nil, false
	}
	sym, off, ok := constexprPlace(e)
	if !ok || sym.init == nil {
		return nil, false
	}
	bf := bitField(e)
	for i := len(sym.init.items) - 1; i >= 0; i-- {
		it := sym.init.items[i]
		if it.x == nil {
			continue
		}
		if bf != nil || it.f != nil {
			if it.f == bf && it.off == off && isConstExpr(it.x) {
				return p.convert(it.x, e.ty().unqual()), true
			}
			continue
		}
		if it.off == off && it.t.isScalar() && isConstExpr(it.x) {
			return p.convert(it.x, e.ty().unqual()), true
		}
		if s, ok := it.x.(*strLit); ok && off >= it.off && off < it.off+it.t.sizeof() && e.ty().sizeof() == 1 {
			k := off - it.off
			v := int64(0)
			if k < int64(len(s.data)) {
				v = int64(s.data[k])
			}
			return p.intc(v, e.ty().unqual(), e.at()), true
		}
	}
	// Not initialized: zero.
	if off >= 0 && off < sym.t.sizeof() {
		return p.convert(p.intc(0, tInt, e.at()), e.ty().unqual()), true
	}
	return nil, false
}

// bitField is the bit-field e names, if it does.
func bitField(e expr) *field {
	if m, ok := e.(*member); ok && m.f.bits >= 0 {
		return m.f
	}
	return nil
}

// constexprPlace finds the constexpr object e is in, and its offset.
func constexprPlace(e expr) (*symbol, int64, bool) {
	switch e := e.(type) {
	case *varRef:
		if e.sym.constx && !e.sym.t.isScalar() {
			return e.sym, 0, true
		}
	case *member:
		if s, off, ok := constexprPlace(e.x); ok {
			return s, off + e.f.off, true
		}
	case *deref:
		if b, ok := e.x.(*binary); ok && (b.op == "+" || b.op == "-") {
			if d, ok := b.x.(*decay); ok {
				if c, ok := b.y.(*intConst); ok {
					if s, off, ok := constexprPlace(d.x); ok {
						n := c.v
						if b.op == "-" {
							n = -n
						}
						return s, off + n*elemSizeOf(b.t), true
					}
				}
			}
		}
		if d, ok := e.x.(*decay); ok {
			return constexprPlace(d.x)
		}
	}
	return nil, 0, false
}

// qualifyDeep qualifies t, or an array's elements (an array's
// qualifiers are its elements').
func qualifyDeep(t *ctype, q qual) *ctype {
	if t.kind == kArray {
		c := *t
		c.elem = qualifyDeep(t.elem, q)
		return &c
	}
	return t.qualified(q)
}

func elemSizeOf(t *ctype) int64 {
	if t.elem.kind == kVoid || t.elem.kind == kFunc {
		return 1
	}
	return t.elem.sizeof()
}

// ptrConstAdd folds a constant pointer plus or minus a constant.
func (p *parser) ptrConstAdd(x, y expr, neg bool) (expr, bool) {
	cx, ok1 := x.(*intConst)
	cy, ok2 := y.(*intConst)
	if !ok1 || !ok2 || x.ty().elem.isVLA() {
		return nil, false
	}
	n := cy.v * elemSizeOf(x.ty())
	if neg {
		n = -n
	}
	return &intConst{node{x.ty(), x.at()}, cx.v + n}, true
}

// foldTruth evaluates a scalar constant as a condition: integer and
// floating constants, and addresses of objects and functions, which are
// never null.
func (p *parser) foldTruth(e expr) (bool, bool) {
	switch e := e.(type) {
	case *intConst:
		return e.v != 0, true
	case *floatConst:
		return e.v != 0, true
	case *cast:
		if e.t.isPtr() || e.t.kind == kBool {
			return p.foldTruth(e.x)
		}
	case *decay:
		return isStaticAddr(e.x)
	case *addrOf:
		return isStaticAddr(e.x)
	}
	return false, false
}

// isStaticAddr reports whether &e is an address constant (so not null).
func isStaticAddr(e expr) (bool, bool) {
	switch e := e.(type) {
	case *varRef:
		if e.sym.weak {
			return false, false
		}
		if e.sym.kind == symFunc || e.sym.kind == symVar && e.sym.storage != sRegister {
			// An object's address is never null.
			return true, true
		}
	case *strLit:
		return true, true
	case *compoundLit:
		if e.sym.global {
			return true, true
		}
	case *member:
		return isStaticAddr(e.x)
	}
	return false, false
}

// toPtrish leaves pointers as they are and makes integers (null
// constants, nullptr) comparable with them.
func (p *parser) toPtrish(e expr) expr {
	if e.ty().isPtr() {
		return e
	}
	if e.ty().kind == kNullptr {
		return p.convert(e, ptrTo(tVoid))
	}
	return p.convert(e, ptrTo(tVoid))
}

func (p *parser) checkPtrArith(t *ctype, at pos) {
	if t.elem.kind == kStruct && !t.elem.rec.complete {
		p.errorAt(at, "arithmetic on a pointer to an incomplete type")
	}
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// scalarCond checks that e may be a condition.
func (p *parser) scalarCond(e expr, at pos) expr {
	if !e.ty().isScalar() {
		p.errorAt(at, "used %s where a scalar is required", e.ty())
	}
	return e
}

func isNullConst(e expr) bool {
	for {
		c, ok := e.(*cast)
		if !ok || !(c.t.isPtr() && c.t.elem.kind == kVoid && c.t.elem.q == 0) {
			break
		}
		e = c.x
	}
	if c, ok := e.(*intConst); ok && c.v == 0 && (c.t.isInteger() || c.t.isPtr() && c.t.elem.kind == kVoid) {
		return true
	}
	return false
}

func (p *parser) castExpr() expr {
	if p.is("(") && p.isAt(1, "__extension__") && !p.isTypeStart(2) {
		// (__extension__ expr)
		return p.postfix(p.primary())
	}
	if p.is("(") && p.isTypeStart(1) {
		at := p.here()
		save := p.pos
		n := len(p.pendingVLA)
		p.next()
		t := p.typeName()
		p.expect(")")
		if p.is("{") {
			// A compound literal.
			p.pendingVLA = p.pendingVLA[:n]
			p.pos = save
			return p.postfix(p.unaryExpr())
		}
		// The type's lengths are evaluated after the operand.
		typeVLA := append([]stmt(nil), p.pendingVLA[n:]...)
		p.pendingVLA = p.pendingVLA[:n]
		x := p.rvalue(p.castExpr())
		if len(typeVLA) > 0 && p.fn != nil {
			if t.kind == kVoid {
				return &seqExpr{node{tVoid, at}, append([]stmt{&exprStmt{x}}, typeVLA...), &cast{node{tVoid, at}, p.intc(0, tInt, at)}}
			}
			tmp := p.newLocal("cast", x.ty().unqual(), at)
			tmp.init = &initializer{scalar: x}
			pre := append([]stmt{&declStmt{syms: []*symbol{tmp}}}, typeVLA...)
			x = &seqExpr{node{x.ty().unqual(), at}, pre, &varRef{node{tmp.t, at}, tmp}}
		}
		if t.kind == kVoid {
			return &cast{node{tVoid, at}, x}
		}
		if !t.isScalar() {
			if t.kind == kStruct && compatible(t.unqual(), x.ty().unqual()) {
				return x // GNU: a cast to the same struct type
			}
			p.errorAt(at, "conversion to non-scalar type %s requested", t)
		}
		if !x.ty().isScalar() {
			p.errorAt(at, "%s used where a scalar is required", x.ty())
		}
		if t.isFloat() && x.ty().isPtr() || t.isPtr() && x.ty().isFloat() {
			p.errorAt(at, "cannot convert between pointer and floating types")
		}
		return p.withVLA(p.convertAt(x, t.unqual(), at))
	}
	return p.unaryExpr()
}

func (p *parser) unaryExpr() expr {
	at := p.here()
	t := p.peek()
	if t.kind == tkPunct {
		switch t.text {
		case "+", "-", "~":
			p.next()
			x := p.rvalue(p.castExpr())
			if t.text == "~" && x.ty().kind == kComplex {
				// GNU: the conjugate.
				return &builtinCall{node{x.ty(), at}, "conj", []expr{x}}
			}
			if !x.ty().isArith() || t.text == "~" && !x.ty().isInteger() {
				p.errorAt(at, "wrong type argument to unary %s", t.text)
			}
			rt := promote(x.ty())
			x = p.convert(x, rt)
			if t.text == "+" {
				return x
			}
			return p.fold(&unary{node{rt, at}, t.text, x})
		case "!":
			p.next()
			x := p.scalarCond(p.rvalue(p.castExpr()), at)
			return p.fold(&unary{node{tInt, at}, "!", x})
		case "&":
			p.next()
			x := p.castExpr()
			return p.addressOf(x, at)
		case "*":
			p.next()
			x := p.rvalue(p.castExpr())
			return p.derefOf(x, at)
		case "++", "--":
			p.next()
			x := p.unaryExpr()
			return p.incDecOf(x, t.text, false, at)
		case "&&":
			// GNU's labels as values.
			p.next()
			if p.fn == nil {
				p.errorAt(at, "label address outside a function")
			}
			l := p.label(p.ident())
			if l.id == 0 {
				p.fn.labelIDs++
				l.id = p.fn.labelIDs
				p.fn.addrLabels = append(p.fn.addrLabels, l)
			}
			p.fn.hasGoto = true
			return &labelAddr{node{ptrTo(tVoid), at}, l}
		}
	}
	if t.kind == tkIdent {
		switch t.text {
		case "sizeof":
			p.next()
			if p.is("(") && p.isTypeStart(1) {
				p.next()
				ty := p.typeName()
				p.expect(")")
				if p.is("{") {
					// sizeof (T){...}
					p.pos -= 0
					cl := p.compoundLiteral(ty, at)
					return p.sizeofType(p.postfix(cl).ty(), at)
				}
				return p.withVLA(p.sizeofType(ty, at))
			}
			x := p.unaryExpr()
			if x.ty().isVLA() {
				// The operand of a variable length array type is
				// evaluated.
				return p.withVLA(&seqExpr{node{tULong, at}, []stmt{&exprStmt{p.vlaOperand(x)}}, p.sizeofType(x.ty(), at)})
			}
			return p.withVLA(p.sizeofType(x.ty(), at))
		case "_Alignof", "alignof", "__alignof__", "__alignof":
			p.next()
			if p.is("(") && p.isTypeStart(1) {
				p.next()
				n := len(p.pendingVLA)
				ty := p.typeName()
				p.pendingVLA = p.pendingVLA[:n] // not evaluated
				p.expect(")")
				if p.is("{") {
					// _Alignof (T){...}
					x := p.postfix(p.compoundLiteral(ty, at))
					return p.intc(x.ty().alignof(), tULong, at)
				}
				return p.intc(ty.alignof(), tULong, at)
			}
			if p.is("(") && p.isTypeStart(1) {
				// _Alignof (T){...}
				p.next()
				ty := p.typeName()
				p.expect(")")
				x := p.postfix(p.compoundLiteral(ty, at))
				return p.intc(x.ty().alignof(), tULong, at)
			}
			x := p.unaryExpr()
			if v, ok := x.(*varRef); ok {
				return p.intc(v.sym.t.alignof(), tULong, at)
			}
			return p.intc(x.ty().alignof(), tULong, at)
		case "_Countof", "__countof__":
			p.next()
			var ty *ctype
			n := len(p.pendingVLA)
			if p.is("(") && p.isTypeStart(1) {
				p.next()
				ty = p.typeName()
				p.expect(")")
			} else {
				x := p.unaryExpr()
				ty = x.ty()
				if ty.kind == kArray && ty.vla != nil {
					return p.withVLA(&seqExpr{node{tULong, at}, []stmt{&exprStmt{p.vlaOperand(x)}}, ty.vla})
				}
			}
			if ty.kind != kArray {
				p.errorAt(at, "_Countof of a %s", ty)
			}
			if ty.vla != nil {
				return p.withVLA(ty.vla)
			}
			p.pendingVLA = p.pendingVLA[:n] // not evaluated
			return p.intc(ty.n, tULong, at)
		case "__extension__":
			p.next()
			return p.castExpr()
		case "__real__", "__real", "__imag__", "__imag":
			p.next()
			lx := p.castExpr()
			if lx.ty().kind == kComplex && isLvalue(lx) {
				// An lvalue: the part, in memory.
				et := lx.ty().elem
				ptr := &cast{node{ptrTo(et), at}, p.addressOf(lx, at)}
				if t.text[2] == 'r' {
					return &deref{node{et, at}, ptr}
				}
				return &deref{node{et, at}, &binary{node{ptrTo(et), at}, "+", ptr, p.intc(1, tLong, at)}}
			}
			x := p.rvalue(lx)
			if !x.ty().isArith() {
				p.errorAt(at, "%s of a %s", t.text, x.ty())
			}
			if x.ty().kind != kComplex {
				if t.text[2] == 'r' {
					return x
				}
				return &binary{node{x.ty(), at}, ",", x, p.convert(p.intc(0, tInt, at), x.ty())}
			}
			name := "creal"
			if t.text[2] == 'i' {
				name = "cimag"
			}
			return &builtinCall{node{x.ty().elem, at}, name, []expr{x}}
		}
	}
	return p.postfix(p.primary())
}

// withVLA makes e evaluate the lengths of the variable length array
// types parsed since the last declaration first.
func (p *parser) withVLA(e expr) expr {
	if len(p.pendingVLA) == 0 {
		return e
	}
	pre := p.pendingVLA
	p.pendingVLA = nil
	return &seqExpr{node{e.ty(), e.at()}, pre, e}
}

// vlaOperand is the operand of sizeof or _Countof of VLA type, to
// evaluate for its effects: its address (it is an array).
func (p *parser) vlaOperand(x expr) expr {
	if isLvalue(x) {
		return &addrOf{node{ptrTo(x.ty()), x.at()}, x}
	}
	return x
}

func (p *parser) sizeofType(t *ctype, at pos) expr {
	if t.isVLA() {
		return &vlaSizeof{node{tULong, at}, t}
	}
	if t.kind == kFunc {
		return p.intc(1, tULong, at)
	}
	if !t.isComplete() && t.kind != kVoid && !(t.kind == kArray && t.n == 0) {
		p.errorAt(at, "invalid application of sizeof to an incomplete type %s", t)
	}
	return p.intc(t.sizeof(), tULong, at)
}

// addressOf types &x.
// constAddr is the address of an lvalue at a constant address, as in
// &((struct S *)0)->m, the old offsetof.
func constAddr(e expr) (int64, bool) {
	switch e := e.(type) {
	case *deref:
		if c, ok := e.x.(*intConst); ok {
			return c.v, true
		}
	case *member:
		if v, ok := constAddr(e.x); ok {
			return v + e.f.off, true
		}
	}
	return 0, false
}

func (p *parser) addressOf(x expr, at pos) expr {
	if v, ok := constAddr(x); ok {
		if m, isMember := x.(*member); !isMember || m.f.bits < 0 {
			return &intConst{node{ptrTo(x.ty()), at}, v}
		}
	}
	switch x := x.(type) {
	case *varRef:
		if x.sym.kind == symFunc {
			return &addrOf{node{ptrTo(x.sym.t), at}, x}
		}
		if x.sym.kind != symVar {
			p.errorAt(at, "lvalue required as unary & operand")
		}
		if x.sym.storage == sRegister {
			p.errorAt(at, "address of register variable %q requested", x.sym.name)
		}
		x.sym.addr = true
	case *deref:
		// &*e is e, which needn't be valid to dereference.
		if x.x.ty().isPtr() {
			return &cast{node{ptrTo(x.t), at}, x.x}
		}
	case *member:
		if x.f.bits >= 0 {
			p.errorAt(at, "cannot take address of bit-field %q", x.f.name)
		}
		markAddr(x)
		if !isLvalue(x) && !isStructValue(x.x) {
			p.errorAt(at, "lvalue required as unary & operand")
		}
	case *compoundLit, *strLit:
	case *stmtExpr, *call, *cond, *assign:
		if x.ty().kind == kStruct {
			break // GNU: the address of a temporary
		}
		p.errorAt(at, "lvalue required as unary & operand")
	default:
		p.errorAt(at, "lvalue required as unary & operand")
	}
	return &addrOf{node{ptrTo(x.ty()), at}, x}
}

// markAddr marks the variable a member expression is in as being in
// memory.
func markAddr(e expr) {
	for {
		switch x := e.(type) {
		case *member:
			e = x.x
		case *varRef:
			x.sym.addr = true
			return
		default:
			return
		}
	}
}

func (p *parser) derefOf(x expr, at pos) expr {
	t := x.ty()
	if !t.isPtr() {
		p.errorAt(at, "invalid type argument of unary * (have %s)", t)
	}
	if t.elem.kind == kFunc {
		// *f is f.
		return x
	}
	return &deref{node{t.elem, at}, x}
}

func (p *parser) incDecOf(x expr, op string, post bool, at pos) expr {
	p.checkModifiable(x, at)
	t := x.ty()
	if !t.isArith() && !t.isPtr() {
		p.errorAt(at, "wrong type argument to %s", op)
	}
	if t.isPtr() {
		p.checkPtrArith(t, at)
	}
	return &incDec{node{t.unqual(), at}, op, post, x}
}

func (p *parser) postfix(x expr) expr {
	for {
		at := p.here()
		switch {
		case p.accept("["):
			i := p.rvalue(p.expr())
			p.expect("]")
			b := p.rvalue(x)
			if !b.ty().isPtr() && i.ty().isPtr() {
				b, i = i, b
			}
			if !b.ty().isPtr() || !i.ty().isInteger() {
				p.errorAt(at, "subscripted value is neither array nor pointer")
			}
			x = p.derefOf(p.binop(at, "+", b, i), at)
		case p.accept("("):
			x = p.callOf(x, at)
		case p.accept("."):
			name := p.ident()
			x = p.memberOf(x, name, at)
		case p.accept("->"):
			name := p.ident()
			b := p.rvalue(x)
			if !b.ty().isPtr() || b.ty().elem.kind != kStruct {
				p.errorAt(at, "invalid type argument of -> (have %s)", b.ty())
			}
			x = p.memberOf(p.derefOf(b, at), name, at)
		case p.is("++") || p.is("--"):
			op := p.next().text
			x = p.incDecOf(x, op, true, at)
		default:
			return x
		}
	}
}

func (p *parser) memberOf(x expr, name string, at pos) expr {
	t := x.ty()
	if t.kind != kStruct {
		p.errorAt(at, "request for member %q in something not a structure or union", name)
	}
	if !t.rec.complete {
		p.errorAt(at, "invalid use of incomplete type %s", t)
	}
	path := t.rec.lookupField(name)
	if path == nil {
		p.errorAt(at, "%s has no member named %q", t, name)
	}
	for _, f := range path {
		ft := f.t
		if t.q != 0 {
			ft = qualifyDeep(ft, t.q)
		}
		x = &member{node{ft, at}, x, f}
		t = ft
	}
	return x
}

// callOf types a call of x, after its "(".
func (p *parser) callOf(x expr, at pos) expr {
	if v, ok := x.(*varRef); ok && v.sym.kind == symFunc {
		v.sym.used = true
	}
	fx := p.rvalue(x)
	ft := fx.ty()
	if ft.isPtr() && ft.elem.kind == kFunc {
		ft = ft.elem
	} else {
		p.errorAt(at, "called object is not a function or function pointer")
	}
	f := ft.fn
	var args []expr
	for !p.is(")") {
		a := p.rvalue(p.assignment())
		i := len(args)
		switch {
		case f.proto && i < len(f.params):
			a = p.assignConvert(a, f.params[i], a.at(), "argument")
		case f.proto && !f.variadic:
			p.errorAt(at, "too many arguments to function")
		case !f.proto && i < len(f.params):
			a = p.convert(a, f.params[i])
		default:
			a = p.convert(a, promoteArg(a.ty()))
		}
		args = append(args, a)
		if !p.accept(",") {
			break
		}
	}
	p.expect(")")
	if f.proto && len(args) < len(f.params) {
		p.errorAt(at, "too few arguments to function")
	}
	if f.ret.kind == kStruct && !f.ret.rec.complete {
		p.errorAt(at, "calling a function returning an incomplete type")
	}
	if p.fn != nil && f.ret.kind == kStruct {
		// The result lives in a temporary.
	}
	return &call{node{f.ret.unqual(), at}, fx, f, args}
}

func (p *parser) intc(v int64, t *ctype, at pos) expr {
	return &intConst{node{t, at}, wrap(t, v)}
}

func (p *parser) primary() expr {
	at := p.here()
	t := p.next()
	switch t.kind {
	case tkInt, tkChar:
		return &intConst{node{t.ty, at}, t.ival}
	case tkFloat:
		if t.imag {
			return &complexConst{node{complexOf(t.ty), at}, 0, t.fval}
		}
		return &floatConst{node{t.ty, at}, t.fval}
	case tkString:
		n := int64(len(t.str))/t.ty.sizeof() + 1
		return &strLit{node{arrayOf(t.ty, n), at}, t.str}
	case tkPunct:
		if t.text == "(" {
			for p.is("__extension__") && !p.isTypeStart(1) {
				p.next()
			}
			if p.is("{") {
				return p.stmtExprOf(at)
			}
			if p.isTypeStart(0) {
				ty := p.typeName()
				p.expect(")")
				return p.compoundLiteral(ty, at)
			}
			x := p.expr()
			p.expect(")")
			return x
		}
	case tkIdent:
		switch t.text {
		case "true":
			return p.intc(1, tBool, at)
		case "false":
			return p.intc(0, tBool, at)
		case "nullptr":
			return &intConst{node{tNullptr, at}, 0}
		case "_Generic":
			return p.generic(at)
		case "__func__", "__FUNCTION__", "__PRETTY_FUNCTION__":
			if s := p.lookup("__func__"); s != nil {
				return &varRef{node{s.t, at}, s}
			}
			// GCC: "" outside a function.
			return &strLit{node{arrayOf(tChar, 1), at}, ""}
		}
		if len(t.text) > 10 && t.text[:10] == "__builtin_" || len(t.text) > 12 && t.text[:12] == "__cc_atomic_" {
			return p.builtin(t.text, at)
		}
		s := p.lookup(t.text)
		if s == nil {
			if p.is("(") {
				// C89's implicit declaration: int f(), unless it is the
				// library's, whose header says what it is.
				ft := &ctype{kind: kFunc, fn: &funcType{ret: tInt}}
				if lt := libcType(t.text); lt != nil {
					ft = lt
				}
				sym := &symbol{name: t.text, kind: symFunc, t: ft, global: true, linkage: 'e', p: at}
				if g := p.u.scope[t.text]; g != nil {
					sym = g
				} else {
					p.u.scope[t.text] = sym
					p.u.syms = append(p.u.syms, sym)
				}
				return &varRef{node{sym.t, at}, sym}
			}
			p.errorAt(at, "%q undeclared", t.text)
		}
		switch s.kind {
		case symEnumConst:
			return p.intc(s.val, s.t, at)
		case symTypedef:
			p.errorAt(at, "unexpected type name %q", t.text)
		}
		if s.constx && s.cval != nil {
			// A constexpr object's value is a constant; its address
			// still an object's.
			return &varRef{node{s.t, at}, s}
		}
		s.used = true
		return &varRef{node{s.t, at}, s}
	}
	p.pos--
	p.errorf("expected an expression, found %s", t)
	return nil
}

func (p *parser) compoundLiteral(t *ctype, at pos) expr {
	var sym *symbol
	static := p.typeStorage == sStatic
	tls := p.typeTLS
	p.typeStorage, p.typeTLS = sAuto, false
	if p.fn == nil || p.staticInit > 0 || static {
		p.symN++
		sym = &symbol{name: "complit." + itoa(int64(p.symN)), kind: symVar, t: t, global: true, storage: sStatic, p: at, defined: true, tls: tls}
		// After its initializer's own compound literals, which it uses.
		defer func() { p.u.statics = append(p.u.statics, sym) }()
	} else {
		sym = p.newLocal("complit", t, at)
		sym.addr = true
	}
	if sym.global {
		p.staticInit++
		defer func() { p.staticInit-- }()
	}
	sym.init = p.initializer(sym)
	return &compoundLit{node{sym.t, at}, sym}
}

// stmtExprOf parses GNU's statement expression ({ ... }), after its "(".
func (p *parser) stmtExprOf(at pos) expr {
	if p.fn == nil {
		p.errorAt(at, "statement expression outside a function")
	}
	p.stmtExprs++
	b := p.blockBody()
	p.stmtExprs--
	p.expect(")")
	e := &stmtExpr{node{tVoid, at}, b, nil}
	if n := len(b.list); n > 0 {
		if es, ok := b.list[n-1].(*exprStmt); ok {
			e.last = p.rvalueVoid(es.x)
			b.list = b.list[:n-1]
			e.t = e.last.ty()
		}
	}
	return e
}

func (p *parser) generic(at pos) expr {
	p.expect("(")
	var ct *ctype
	if p.isTypeStart(0) {
		// C2y: _Generic(type-name, ...).
		ct = p.typeName()
	} else {
		x := p.assignment()
		if x.ty().kind == kVoid {
			ct = tVoid
		} else {
			ct = p.rvalue(x).ty().unqual()
		}
	}
	var chosen, dflt expr
	for p.accept(",") {
		if p.accept("default") {
			p.expect(":")
			dflt = p.assignment()
			continue
		}
		t := p.typeName()
		p.expect(":")
		e := p.assignment()
		if chosen == nil && compatible(t, ct) {
			chosen = e
		}
	}
	p.expect(")")
	if chosen == nil {
		chosen = dflt
	}
	if chosen == nil {
		p.errorAt(at, "_Generic selector of type %s is not compatible with any association", ct)
	}
	return chosen
}

// rvalue is e's value: lvalue conversion, and arrays and functions
// decayed to pointers.
func (p *parser) rvalue(e expr) expr {
	t := e.ty()
	switch t.kind {
	case kArray:
		if v, ok := constAddr(e); ok {
			return &intConst{node{ptrTo(t.elem), e.at()}, v}
		}
		if v, ok := e.(*varRef); ok {
			v.sym.addr = true
		}
		markAddr(e)
		return &decay{node{ptrTo(t.elem), e.at()}, e}
	case kFunc:
		return &decay{node{ptrTo(t), e.at()}, e}
	case kVoid:
		p.errorAt(e.at(), "void value not ignored as it ought to be")
	}
	if c, ok := p.constexprLoad(e); ok {
		return c
	}
	if f := bitField(e); f != nil && t.isInteger() && !t.isBitInt() && f.bits < 32 {
		// A bit-field narrower than int is an int (C23 6.3.1.1; GCC's
		// rule for its wider base types too).
		return &cast{node{tInt, e.at()}, e}
	}
	if v, ok := e.(*varRef); ok && v.sym.constx && v.sym.cval != nil {
		if c, ok := v.sym.cval.(*intConst); ok {
			return &intConst{node{t.unqual(), e.at()}, c.v}
		}
		if c, ok := v.sym.cval.(*floatConst); ok {
			return &floatConst{node{t.unqual(), e.at()}, c.v}
		}
	}
	return e
}

// convert converts e to t (C23 6.3), folding constants.
func (p *parser) convert(e expr, t *ctype) expr {
	return p.convertAt(e, t, e.at())
}

func (p *parser) convertAt(e expr, t *ctype, at pos) expr {
	et := e.ty()
	if et == t {
		return e
	}
	if compatible(et, t) && et.kind != kStruct && et.kind != kArray && et.q == t.q {
		if c, ok := e.(*intConst); ok {
			// The same value, with the (composite) type.
			return &intConst{node{t, at}, c.v}
		}
		return e
	}
	if t.kind == kStruct || et.kind == kStruct {
		if compatible(et.unqual(), t.unqual()) {
			return e
		}
		p.errorAt(at, "incompatible types: %s and %s", et, t)
	}
	if t.kind == kBool {
		if v, ok := p.foldTruth(e); ok {
			return &intConst{node{t, at}, b2i(v)}
		}
	}
	if is128(t) && !(func() bool { c, ok := e.(*intConst); return ok && c.v >= 0 })() {
		// Not folded: an int64 holds only some of its values.
		return &cast{node{t, at}, e}
	}
	if t.isInteger() || t.isPtr() && et.isInteger() || t.kind == kNullptr {
		if c, ok := e.(*intConst); ok && (c.t.isInteger() || c.t.kind == kNullptr || t.isPtr() && c.t.isPtr() || t.isInteger() && c.t.isPtr()) {
			return &intConst{node{t, at}, wrap(t, c.v)}
		}
		if c, ok := e.(*floatConst); ok && t.isInteger() {
			return &intConst{node{t, at}, wrap(t, floatToInt(c.v, t))}
		}
	}
	if t.isFloat() {
		if c, ok := e.(*intConst); ok && c.t.isInteger() {
			v := float64(c.v)
			if c.t.isUnsigned() && c.t.sizeof() == 8 {
				v = float64(uint64(c.v))
			}
			if t.kind == kFloat {
				v = float64(float32(v))
			}
			return &floatConst{node{t, at}, v}
		}
		if c, ok := e.(*floatConst); ok {
			v := c.v
			if t.kind == kFloat {
				v = float64(float32(v))
			}
			return &floatConst{node{t, at}, v}
		}
	}
	if t.isPtr() && et.isPtr() {
		if c, ok := e.(*intConst); ok {
			return &intConst{node{t, at}, c.v}
		}
	}
	return &cast{node{t, at}, e}
}

func floatToInt(f float64, t *ctype) int64 {
	if t.isUnsigned() && t.sizeof() == 8 {
		if f >= 9223372036854775808.0 {
			return int64(uint64(f))
		}
	}
	if math.IsNaN(f) {
		return 0
	}
	return int64(f)
}

// assignConvert converts e as assignment does: for =, arguments,
// return and initializers.
func (p *parser) assignConvert(e expr, t *ctype, at pos, what string) expr {
	et := e.ty()
	tu := t.unqual()
	switch {
	case tu.isArith() && et.isArith():
	case tu.kind == kBool && (et.isPtr() || et.kind == kNullptr):
	case tu.kind == kStruct && et.kind == kStruct:
		if !compatible(tu, et.unqual()) {
			p.errorAt(at, "incompatible types when %s to type %s from type %s", whatVerb(what), t, et)
		}
		return e
	case tu.isPtr() && (et.kind == kNullptr || isNullConst(e)):
		return p.convertAt(e, tu, at)
	case tu.kind == kNullptr && (et.kind == kNullptr || isNullConst(e)):
		return p.convertAt(e, tu, at)
	case tu.isPtr() && et.isPtr():
		// GCC warns about incompatible pointers; cmd/cc accepts them,
		// and keeps C's checks at run time.
	case tu.isPtr() && et.isInteger() || tu.isInteger() && et.isPtr():
		// GCC warns (an error in GCC 14), but accepts it.
	case tu.kind == kVaList && et.kind == kVaList:
		return e
	case tu.isPtr() && et.kind == kVaList:
	default:
		p.errorAt(at, "incompatible types when %s to type %s from type %s", whatVerb(what), t, et)
	}
	return p.convertAt(e, tu, at)
}

func whatVerb(what string) string {
	switch what {
	case "argument":
		return "passing an argument"
	case "return":
		return "returning"
	case "initializer":
		return "initializing"
	}
	return "assigning"
}

// Constants.

// foldInt evaluates an integer constant expression.
func (p *parser) foldInt(e expr) (int64, bool) {
	switch e := e.(type) {
	case *intConst:
		if e.t.isInteger() || e.t.kind == kNullptr {
			return e.v, true
		}
	case *cast:
		if e.t.isInteger() {
			if v, ok := p.foldInt(e.x); ok {
				return wrap(e.t, v), true
			}
			if f, ok := p.foldFloat(e.x); ok {
				return wrap(e.t, floatToInt(f, e.t)), true
			}
		}
	}
	return 0, false
}

func (p *parser) foldFloat(e expr) (float64, bool) {
	switch e := e.(type) {
	case *floatConst:
		return e.v, true
	case *intConst:
		if e.t.isUnsigned() && e.t.sizeof() == 8 {
			return float64(uint64(e.v)), true
		}
		return float64(e.v), true
	}
	return 0, false
}

func (p *parser) constInt(e expr) int64 {
	v, ok := p.foldInt(p.rvalue(e))
	if !ok {
		p.errorAt(e.at(), "expression is not an integer constant expression")
	}
	return v
}

// fold folds an operation on constants.
func (p *parser) fold(e expr) expr {
	if is128(e.ty()) {
		return e // int64's arithmetic is not __int128's
	}
	switch e := e.(type) {
	case *unary:
		if x, ok := e.x.(*intConst); ok && e.x.ty().isInteger() {
			switch e.op {
			case "-":
				return &intConst{node{e.t, e.p}, wrap(e.t, -x.v)}
			case "~":
				return &intConst{node{e.t, e.p}, wrap(e.t, ^x.v)}
			case "!":
				return &intConst{node{e.t, e.p}, b2i(x.v == 0)}
			}
		}
		if x, ok := e.x.(*floatConst); ok {
			switch e.op {
			case "-":
				return &floatConst{node{e.t, e.p}, -x.v}
			case "!":
				return &intConst{node{e.t, e.p}, b2i(x.v == 0)}
			}
		}
		if x, ok := e.x.(*intConst); ok && e.op == "!" {
			return &intConst{node{e.t, e.p}, b2i(x.v == 0)}
		}
		if eff, v, ok := constCond(e.x); ok && eff != nil && e.op == "!" {
			return &binary{node{tInt, e.p}, ",", eff, &intConst{node{tInt, e.p}, b2i(!v)}}
		}
	case *binary:
		x, xok := e.x.(*intConst)
		y, yok := e.y.(*intConst)
		if xok && yok && x.t.isInteger() && y.t.isInteger() {
			if v, ok := foldOp(e.op, x.t, x.v, y.v); ok {
				return &intConst{node{e.t, e.p}, wrap(e.t, v)}
			}
		}
		fx, fxok := e.x.(*floatConst)
		fy, fyok := e.y.(*floatConst)
		if fxok && fyok {
			if v, isBool, ok := foldFloatOp(e.op, fx.v, fy.v); ok {
				if isBool {
					return &intConst{node{e.t, e.p}, int64(v)}
				}
				if e.t.kind == kFloat {
					v = float64(float32(v))
				}
				return &floatConst{node{e.t, e.p}, v}
			}
		}
	}
	return e
}

// foldOp computes x op y in t (the operands' type), as C does.
func foldOp(op string, t *ctype, x, y int64) (int64, bool) {
	if t.isUnsigned() {
		ux, uy := uint64(x), uint64(y)
		if t.sizeof() == 4 {
			ux, uy = uint64(uint32(x)), uint64(uint32(y))
		}
		switch op {
		case "/":
			if uy == 0 {
				return 0, false
			}
			return int64(ux / uy), true
		case "%":
			if uy == 0 {
				return 0, false
			}
			return int64(ux % uy), true
		case ">>":
			if t.sizeof() == 4 {
				return int64(uint32(x) >> uint64(y)), true
			}
			return int64(ux >> uint64(y)), true
		case "<":
			return b2i(ux < uy), true
		case ">":
			return b2i(ux > uy), true
		case "<=":
			return b2i(ux <= uy), true
		case ">=":
			return b2i(ux >= uy), true
		}
	}
	switch op {
	case "+":
		return x + y, true
	case "-":
		return x - y, true
	case "*":
		return x * y, true
	case "/":
		if y == 0 {
			return 0, false
		}
		if y == -1 {
			return -x, true
		}
		return x / y, true
	case "%":
		if y == 0 {
			return 0, false
		}
		if y == -1 {
			return 0, true
		}
		return x % y, true
	case "<<":
		if y < 0 || y >= 64 {
			return 0, false
		}
		return x << uint64(y), true
	case ">>":
		if y < 0 || y >= 64 {
			return 0, false
		}
		return x >> uint64(y), true
	case "&":
		return x & y, true
	case "|":
		return x | y, true
	case "^":
		return x ^ y, true
	case "==":
		return b2i(x == y), true
	case "!=":
		return b2i(x != y), true
	case "<":
		return b2i(x < y), true
	case ">":
		return b2i(x > y), true
	case "<=":
		return b2i(x <= y), true
	case ">=":
		return b2i(x >= y), true
	}
	return 0, false
}

func foldFloatOp(op string, x, y float64) (v float64, isBool, ok bool) {
	switch op {
	case "+":
		return x + y, false, true
	case "-":
		return x - y, false, true
	case "*":
		return x * y, false, true
	case "/":
		return x / y, false, true
	case "==":
		return float64(b2i(x == y)), true, true
	case "!=":
		return float64(b2i(x != y)), true, true
	case "<":
		return float64(b2i(x < y)), true, true
	case ">":
		return float64(b2i(x > y)), true, true
	case "<=":
		return float64(b2i(x <= y)), true, true
	case ">=":
		return float64(b2i(x >= y)), true, true
	}
	return 0, false, false
}

// Built-in functions.

func (p *parser) builtin(name string, at pos) expr {
	p.expect("(")
	args := func(n int) []expr {
		var as []expr
		for !p.is(")") {
			as = append(as, p.assignment())
			if !p.accept(",") {
				break
			}
		}
		p.expect(")")
		if n >= 0 && len(as) != n {
			p.errorAt(at, "%s takes %d arguments", name, n)
		}
		return as
	}
	switch name {
	case "__builtin_va_start", "__builtin_c23_va_start":
		as := args(-1)
		if len(as) < 1 {
			p.errorAt(at, "too few arguments to va_start")
		}
		if p.fn == nil || !p.fn.sym.t.fn.variadic {
			p.errorAt(at, "va_start used in a function with fixed arguments")
		}
		p.fn.usesVA = true
		p.checkVaList(as[0], at)
		var e expr = &vaOp{node{tVoid, at}, "start", as[0], nil}
		for _, a := range as[1:] {
			// Evaluated, for its effects.
			e = &binary{node{tVoid, at}, ",", p.rvalueVoid(a), e}
		}
		return e
	case "__builtin_va_end":
		as := args(1)
		if as[0].ty().kind != kVaList {
			p.errorAt(at, "first argument to va_end is not a va_list")
		}
		return &vaOp{node{tVoid, at}, "end", as[0], nil}
	case "__builtin_va_copy":
		as := args(2)
		p.checkVaList(as[0], at)
		return &vaOp{node{tVoid, at}, "copy", as[0], p.rvalue(as[1])}
	case "__builtin_va_arg":
		ap := p.assignment()
		p.expect(",")
		t := p.typeName()
		p.expect(")")
		p.checkVaList(ap, at)
		return &vaOp{node{t.unqual(), at}, "arg", ap, nil}
	case "__builtin_offsetof":
		t := p.typeName()
		p.expect(",")
		off := int64(0)
		cur := t
		first := true
		for {
			if first || p.accept(".") {
				first = false
				name := p.ident()
				if cur.kind != kStruct {
					p.errorAt(at, "offsetof of a non-struct")
				}
				path := cur.rec.lookupField(name)
				if path == nil {
					p.errorAt(at, "%s has no member %q", cur, name)
				}
				for _, f := range path {
					off += f.off
					cur = f.t
				}
				continue
			}
			if p.accept("[") {
				i := p.constInt(p.expr())
				p.expect("]")
				if cur.kind != kArray {
					p.errorAt(at, "offsetof subscript of a non-array")
				}
				cur = cur.elem
				off += i * cur.sizeof()
				continue
			}
			break
		}
		p.expect(")")
		return p.intc(off, tULong, at)
	case "__builtin_types_compatible_p":
		a := p.typeName()
		p.expect(",")
		b := p.typeName()
		p.expect(")")
		return p.intc(b2i(compatible(a.unqual(), b.unqual())), tInt, at)
	case "__builtin_constant_p":
		as := args(1)
		_, ok := p.foldInt(as[0])
		if !ok {
			_, ok = p.foldFloat(as[0])
		}
		return p.intc(b2i(ok), tInt, at)
	case "__builtin_expect", "__builtin_expect_with_probability":
		as := args(-1)
		if len(as) < 2 {
			p.errorAt(at, "too few arguments to %s", name)
		}
		return p.convert(p.rvalue(as[0]), tLong)
	case "__builtin_unreachable", "__builtin_trap", "__builtin_abort":
		args(0)
		return &builtinCall{node{tVoid, at}, "trap", nil}
	case "__builtin_choose_expr":
		c := p.constInt(p.assignment())
		p.expect(",")
		x := p.assignment()
		p.expect(",")
		y := p.assignment()
		p.expect(")")
		if c != 0 {
			return x
		}
		return y
	case "__builtin_setjmp":
		as := args(1)
		if p.fn == nil {
			p.errorAt(at, "setjmp outside a function")
		}
		env := p.rvalue(as[0])
		if !env.ty().isPtr() {
			p.errorAt(at, "setjmp of a %s", env.ty())
		}
		p.fn.setjmps++
		return &builtinCall{node{tInt, at}, "setjmp", []expr{env, p.intc(int64(p.fn.setjmps), tInt, at)}}
	case "__cc_atomic_load", "__cc_atomic_store", "__cc_atomic_fetch", "__cc_atomic_cmpxchg", "__cc_atomic_fence":
		return p.atomicBuiltin(name[len("__cc_"):], args(-1), at)
	case "__builtin_alloca", "__builtin_alloca_with_align":
		as := args(-1)
		if len(as) < 1 {
			p.errorAt(at, "too few arguments to %s", name)
		}
		// In the heap: the collector frees it.
		n := p.convert(p.rvalue(as[0]), tULong)
		return &builtinCall{node{ptrTo(tVoid), at}, "alloca", []expr{n}}
	case "__builtin_complex":
		as := args(2)
		re, im := p.rvalue(as[0]), p.rvalue(as[1])
		if !re.ty().isFloat() {
			p.errorAt(at, "__builtin_complex of a %s", re.ty())
		}
		t := complexOf(re.ty().unqual())
		return &builtinCall{node{t, at}, "complex", []expr{re, p.convert(im, re.ty().unqual())}}
	case "__builtin_creal", "__builtin_cimag", "__builtin_crealf", "__builtin_cimagf", "__builtin_creall", "__builtin_cimagl":
		as := args(1)
		x := p.convert(p.rvalue(as[0]), complexOf(map[byte]*ctype{'f': tFloat, 'l': tLDouble}[name[len(name)-1]]))
		if c := x.ty(); c.elem == nil {
			x = p.convert(p.rvalue(as[0]), complexOf(tDouble))
		}
		return &builtinCall{node{x.ty().elem, at}, name[10:15], []expr{x}}
	case "__builtin_assert":
		// cmd/cc's assert: (cond, text, file, line).
		as := args(4)
		c := p.scalarCond(p.rvalue(as[0]), at)
		return &builtinCall{node{tVoid, at}, "assert", []expr{c, p.rvalue(as[1]), p.rvalue(as[2]), p.rvalue(as[3])}}
	}
	if e := p.bitBuiltin(name, at); e != nil {
		return e
	}
	// A library function by its other name.
	if s := p.lookup(name[10:]); s != nil && s.kind == symFunc {
		p.pos-- // back to "("
		return &varRef{node{s.t, at}, s}
	}
	if t := libcType(name[10:]); t != nil {
		// Declared by its header, included or not.
		s := p.u.scope[name[10:]]
		if s == nil || s.kind != symFunc {
			s = &symbol{name: name[10:], kind: symFunc, t: t, global: true, linkage: 'e', p: at}
			if p.u.scope[name[10:]] == nil {
				p.u.scope[name[10:]] = s
			}
			p.u.syms = append(p.u.syms, s)
		}
		p.pos--
		return &varRef{node{s.t, at}, s}
	}
	p.errorAt(at, "%s is not supported", name)
	return nil
}

// atomicBuiltin types the atomic operations stdatomic.h uses.
func (p *parser) atomicBuiltin(op string, as []expr, at pos) expr {
	if op == "atomic_fence" {
		return &builtinCall{node{tVoid, at}, op, nil}
	}
	n := map[string]int{"atomic_load": 1, "atomic_store": 2, "atomic_fetch": 3, "atomic_cmpxchg": 3}[op]
	if len(as) != n {
		p.errorAt(at, "%s takes %d arguments", op, n)
	}
	ptr := p.rvalue(as[0])
	if !ptr.ty().isPtr() || !ptr.ty().elem.isScalar() {
		p.errorAt(at, "%s of a %s", op, ptr.ty())
	}
	et := ptr.ty().elem.unqual()
	switch op {
	case "atomic_load":
		return &builtinCall{node{et, at}, op, []expr{ptr}}
	case "atomic_store":
		v := p.assignConvert(p.rvalue(as[1]), et, at, "assignment")
		return &builtinCall{node{tVoid, at}, op, []expr{ptr, v}}
	case "atomic_fetch":
		code, ok := p.foldInt(as[1])
		if !ok {
			p.errorAt(at, "atomic operation not constant")
		}
		var v expr
		if et.isPtr() && code != '=' {
			v = p.convert(p.rvalue(as[2]), tLong)
		} else {
			v = p.assignConvert(p.rvalue(as[2]), et, at, "assignment")
		}
		return &builtinCall{node{et, at}, op, []expr{ptr, p.intc(code, tInt, at), v}}
	}
	exp := p.rvalue(as[1])
	if !exp.ty().isPtr() {
		p.errorAt(at, "atomic_compare_exchange's expected is not a pointer")
	}
	d := p.assignConvert(p.rvalue(as[2]), et, at, "assignment")
	return &builtinCall{node{tBool, at}, op, []expr{ptr, exp, d}}
}

// bitBuiltin parses GCC's bit and overflow builtins, after "(": nil if
// name is not one.
func (p *parser) bitBuiltin(name string, at pos) expr {
	base := name[len("__builtin_"):]
	type bitFn struct {
		arg, res *ctype
		fn       string
	}
	fns := map[string]bitFn{
		"clz": {tUInt, tInt, "Clz32"}, "clzl": {tULong, tInt, "Clz64"}, "clzll": {tULLong, tInt, "Clz64"},
		"ctz": {tUInt, tInt, "Ctz32"}, "ctzl": {tULong, tInt, "Ctz64"}, "ctzll": {tULLong, tInt, "Ctz64"},
		"popcount": {tUInt, tInt, "Popcount32"}, "popcountl": {tULong, tInt, "Popcount64"}, "popcountll": {tULLong, tInt, "Popcount64"},
		"parity": {tUInt, tInt, "Parity32"}, "parityl": {tULong, tInt, "Parity64"}, "parityll": {tULLong, tInt, "Parity64"},
		"ffs": {tInt, tInt, "Ffs32"}, "ffsl": {tLong, tInt, "Ffs64"}, "ffsll": {tLLong, tInt, "Ffs64"},
		"clrsb": {tInt, tInt, "Clrsb32"}, "clrsbl": {tLong, tInt, "Clrsb64"}, "clrsbll": {tLLong, tInt, "Clrsb64"},
		"bswap16": {tUShort, tUShort, "Bswap16"}, "bswap32": {tUInt, tUInt, "Bswap32"}, "bswap64": {tULong, tULong, "Bswap64"},
	}
	if f, ok := fns[base]; ok {
		x := p.assignConvert(p.rvalue(p.assignment()), f.arg, at, "argument")
		p.expect(")")
		return &builtinCall{node{f.res, at}, "bits:" + f.fn, []expr{x}}
	}
	switch base {
	case "prefetch":
		var as []expr
		for !p.is(")") {
			as = append(as, p.rvalueVoid(p.assignment()))
			if !p.accept(",") {
				break
			}
		}
		p.expect(")")
		var e expr = &cast{node{tVoid, at}, p.intc(0, tInt, at)}
		if len(as) > 0 {
			e = &binary{node{tVoid, at}, ",", as[0], e}
		}
		return e
	case "classify_type":
		x := p.assignment()
		p.expect(")")
		t := x.ty()
		c := int64(-1)
		switch {
		case t.kind == kVoid:
			c = 0
		case t.enum != nil:
			c = 3
		case t.kind == kBool:
			c = 4
		case t.isInteger():
			c = 1
		case t.isPtr():
			c = 5
		case t.isFloat():
			c = 8
		case t.kind == kStruct && t.rec.union:
			c = 13
		case t.kind == kStruct:
			c = 12
		case t.kind == kArray:
			c = 14
		case t.kind == kFunc:
			c = 10
		}
		return p.intc(c, tInt, at)
	case "add_overflow", "sub_overflow", "mul_overflow", "add_overflow_p", "sub_overflow_p", "mul_overflow_p":
		a := p.rvalue(p.assignment())
		p.expect(",")
		b := p.rvalue(p.assignment())
		p.expect(",")
		r := p.rvalue(p.assignment())
		p.expect(")")
		if !a.ty().isInteger() || !b.ty().isInteger() {
			p.errorAt(at, "%s of non-integers", name)
		}
		var rt *ctype
		if base[len(base)-1] == 'p' {
			rt = r.ty() // the third's type; it is not stored to
		} else {
			if !r.ty().isPtr() || !r.ty().elem.isInteger() {
				p.errorAt(at, "%s's result is not a pointer to an integer", name)
			}
			rt = r.ty().elem
		}
		op := map[byte]int64{'a': '+', 's': '-', 'm': '*'}[base[0]]
		return &builtinCall{node{tBool, at}, "overflow", []expr{p.intc(op, tInt, at), a, b, r, &cast{node{rt, at}, p.intc(0, tInt, at)}, p.intc(b2i(base[len(base)-1] == 'p'), tInt, at)}}
	}
	return nil
}

func (p *parser) checkVaList(e expr, at pos) {
	if e.ty().kind != kVaList {
		p.errorAt(at, "first argument to va_ macro is not a va_list")
	}
	if !isLvalue(e) {
		p.errorAt(at, "va_list is not an lvalue")
	}
}
