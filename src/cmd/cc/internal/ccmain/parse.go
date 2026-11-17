// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"fmt"
)

// Expressions.
type expr interface{ line() int }

type (
	intLit struct {
		l int
		v int64
		t *ctype
	}
	floatLit struct {
		l int
		v float64
	}
	strLit struct {
		l int
		s string
	}
	ident struct {
		l    int
		name string
	}
	unary struct {
		l  int
		op string
		x  expr
	}
	incDec struct {
		l    int
		op   string
		post bool
		x    expr
	}
	binary struct {
		l    int
		op   string
		x, y expr
	}
	assign struct {
		l    int
		op   string
		x, y expr
	} // op: "=", "+=", ...
	condExpr struct {
		l       int
		c, x, y expr
	}
	call struct {
		l    int
		fn   string
		args []expr
	}
	index struct {
		l    int
		x, i expr
	}
	member struct {
		l     int
		x     expr
		name  string
		arrow bool
	}
	cast struct {
		l int
		t *ctype
		x expr
	}
	sizeofT struct {
		l int
		t *ctype
	}
	sizeofE struct {
		l int
		x expr
	}
	initList struct {
		l     int
		elems []expr
	}
)

func (e *intLit) line() int   { return e.l }
func (e *floatLit) line() int { return e.l }
func (e *strLit) line() int   { return e.l }
func (e *ident) line() int    { return e.l }
func (e *unary) line() int    { return e.l }
func (e *incDec) line() int   { return e.l }
func (e *binary) line() int   { return e.l }
func (e *assign) line() int   { return e.l }
func (e *condExpr) line() int { return e.l }
func (e *call) line() int     { return e.l }
func (e *index) line() int    { return e.l }
func (e *member) line() int   { return e.l }
func (e *cast) line() int     { return e.l }
func (e *sizeofT) line() int  { return e.l }
func (e *sizeofE) line() int  { return e.l }
func (e *initList) line() int { return e.l }

// Statements.
type stmt interface{}

type (
	exprStmt struct{ x expr }
	declStmt struct{ vars []*varDecl }
	ifStmt   struct {
		c         expr
		then, els stmt
	}
	whileStmt struct {
		c    expr
		body stmt
	}
	doStmt struct {
		body stmt
		c    expr
	}
	forStmt struct {
		init    stmt
		c, post expr
		body    stmt
	}
	returnStmt struct {
		x expr
		l int
	}
	breakStmt  struct{}
	contStmt   struct{}
	blockStmt  struct{ list []stmt }
	switchStmt struct {
		x    expr
		body *blockStmt
	}
	caseLabel struct{ x expr } // nil: default
	emptyStmt struct{}
)

type varDecl struct {
	name   string
	t      *ctype
	init   expr
	static bool
	extern bool
	line   int
}

type funcDecl struct {
	name   string
	t      *ctype
	body   *blockStmt
	static bool
	addr   map[string]bool // locals whose address is taken
	line   int
}

type unit struct {
	enums    map[string]int64
	tags     map[string]*ctype
	globals  []*varDecl
	funcs    []*funcDecl
	protos   map[string]*ctype
	structs  []*ctype
	typedefs []typedef
}

type typedef struct {
	name string
	t    *ctype
}

type parser struct {
	file     string
	toks     []token
	pos      int
	typedefs []map[string]*ctype
	tags     map[string]*ctype
	enums    map[string]int64
	u        *unit
	addr     map[string]bool
	anon     int
}

type parseError struct{ msg string }

func (p *parser) errorf(format string, args ...any) {
	file := p.file
	if f := p.peek().file; f != "" {
		file = f
	}
	panic(parseError{fmt.Sprintf("%s:%d: %s", file, p.peek().line, fmt.Sprintf(format, args...))})
}

func parse(file string, toks []token) (u *unit, err error) {
	p := &parser{file: file, toks: toks, tags: map[string]*ctype{}, enums: map[string]int64{},
		u: &unit{protos: map[string]*ctype{}}}
	p.typedefs = []map[string]*ctype{builtinTypedefs()}
	defer func() {
		if r := recover(); r != nil {
			pe, ok := r.(parseError)
			if !ok {
				panic(r)
			}
			err = fmt.Errorf("%s", pe.msg)
		}
	}()
	for p.peek().kind != tkEOF {
		p.topLevel()
	}
	p.u.enums = p.enums
	p.u.tags = p.tags
	return p.u, nil
}

func builtinTypedefs() map[string]*ctype {
	return map[string]*ctype{
		"size_t": tULong, "ssize_t": tLong, "ptrdiff_t": tLong, "intptr_t": tLong, "uintptr_t": tULong,
		"int8_t": tChar, "uint8_t": tUChar, "int16_t": tShort, "uint16_t": tUShort,
		"int32_t": tInt, "uint32_t": tUInt, "int64_t": tLong, "uint64_t": tULong,
	}
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) peekAt(i int) token {
	if p.pos+i < len(p.toks) {
		return p.toks[p.pos+i]
	}
	return p.toks[len(p.toks)-1]
}
func (p *parser) next() token { t := p.toks[p.pos]; p.pos++; return t }
func (p *parser) is(s string) bool {
	t := p.peek()
	return (t.kind == tkPunct || t.kind == tkIdent) && t.text == s
}
func (p *parser) accept(s string) bool {
	if p.is(s) {
		p.pos++
		return true
	}
	return false
}
func (p *parser) expect(s string) {
	if !p.accept(s) {
		p.errorf("expected %q, found %s", s, p.peek())
	}
}
func (p *parser) ident() string {
	t := p.next()
	if t.kind != tkIdent {
		p.pos--
		p.errorf("expected identifier, found %s", t)
	}
	return t.text
}

func (p *parser) lookupTypedef(name string) *ctype {
	for i := len(p.typedefs) - 1; i >= 0; i-- {
		if t, ok := p.typedefs[i][name]; ok {
			return t
		}
	}
	return nil
}

var typeWords = map[string]bool{
	"void": true, "char": true, "short": true, "int": true, "long": true, "float": true, "double": true,
	"signed": true, "unsigned": true, "struct": true, "union": true, "enum": true, "const": true,
	"volatile": true, "static": true, "extern": true, "typedef": true, "inline": true,
	"register": true, "auto": true, "restrict": true, "_Bool": true,
}

func (p *parser) isTypeStart() bool {
	t := p.peek()
	return t.kind == tkIdent && (typeWords[t.text] || p.lookupTypedef(t.text) != nil)
}

type specs struct {
	t                       *ctype
	typedef, static, extern bool
}

func (p *parser) declSpecs() specs {
	var s specs
	var base string
	nlong, unsigned, signed, short := 0, false, false, false
	seen := false
	for {
		t := p.peek()
		if t.kind != tkIdent {
			break
		}
		switch t.text {
		case "typedef", "static", "extern", "const", "volatile", "inline", "register", "auto", "restrict":
			s.typedef = s.typedef || t.text == "typedef"
			s.static = s.static || t.text == "static"
			s.extern = s.extern || t.text == "extern"
			p.next()
			continue
		case "unsigned":
			unsigned = true
		case "signed":
			signed = true
		case "short":
			short = true
		case "long":
			nlong++
		case "void", "char", "int", "float", "double":
			base = t.text
		case "_Bool":
			p.errorf("_Bool is not supported")
		case "struct", "union":
			p.next()
			s.t = p.structSpec(t.text == "union")
			seen = true
			continue
		case "enum":
			p.next()
			p.enumSpec()
			s.t = tInt
			seen = true
			continue
		default:
			if base == "" && !seen && !unsigned && !signed && !short && nlong == 0 {
				if td := p.lookupTypedef(t.text); td != nil {
					s.t = td
					seen = true
					p.next()
					continue
				}
			}
			goto done
		}
		seen = true
		p.next()
	}
done:
	if s.t != nil {
		return s
	}
	if !seen {
		p.errorf("expected a type, found %s", p.peek())
	}
	switch {
	case base == "void":
		s.t = tVoid
	case base == "float":
		s.t = tFloat
	case base == "double":
		s.t = tDouble
	case base == "char":
		s.t = tChar
		if unsigned {
			s.t = tUChar
		}
	case short:
		s.t = tShort
		if unsigned {
			s.t = tUShort
		}
	case nlong > 0:
		s.t = tLong
		if unsigned {
			s.t = tULong
		}
	default:
		s.t = tInt
		if unsigned {
			s.t = tUInt
		}
	}
	return s
}

func (p *parser) structSpec(union bool) *ctype {
	tag := ""
	if p.peek().kind == tkIdent {
		tag = p.ident()
	}
	var t *ctype
	if tag != "" {
		t = p.tags[tag]
	}
	if t == nil {
		if tag == "" {
			p.anon++
			tag = fmt.Sprintf("anon%d", p.anon)
		}
		t = &ctype{kind: kStruct, tag: tag, union: union}
		p.tags[tag] = t
	}
	if t.union != union {
		p.errorf("%s used as both a struct and a union", tag)
	}
	if !p.accept("{") {
		return t
	}
	if t.complete {
		p.errorf("struct %s redefined", tag)
	}
	for !p.accept("}") {
		s := p.declSpecs()
		for {
			name, ft := p.declarator(s.t)
			if name == "" {
				p.errorf("unnamed field")
			}
			if ft.kind == kArray && ft.n < 0 {
				p.errorf("flexible array members are not supported")
			}
			t.fields = append(t.fields, &field{name: name, t: ft})
			if !p.accept(",") {
				break
			}
		}
		p.expect(";")
	}
	t.layout()
	p.u.structs = append(p.u.structs, t)
	return t
}

func (p *parser) enumSpec() {
	if p.peek().kind == tkIdent {
		p.ident()
	}
	if !p.accept("{") {
		return
	}
	v := int64(0)
	for !p.accept("}") {
		name := p.ident()
		if p.accept("=") {
			v = p.constExpr(p.conditional())
		}
		p.enums[name] = v
		v++
		if !p.accept(",") {
			p.expect("}")
			break
		}
	}
}

// declarator parses a (possibly abstract) declarator of base.
func (p *parser) declarator(base *ctype) (string, *ctype) {
	t := base
	for p.accept("*") {
		for p.accept("const") || p.accept("volatile") || p.accept("restrict") {
		}
		t = ptrTo(t)
	}
	name := ""
	if p.is("(") && p.peekAt(1).text == "*" {
		p.errorf("function pointers are not supported")
	}
	if p.peek().kind == tkIdent && !typeWords[p.peek().text] {
		name = p.ident()
	}
	return name, p.suffixes(t)
}

func (p *parser) suffixes(t *ctype) *ctype {
	if p.accept("[") {
		n := int64(-1)
		if !p.accept("]") {
			n = p.constExpr(p.expr())
			p.expect("]")
		}
		return arrayOf(p.suffixes(t), n)
	}
	if p.accept("(") {
		ft := &ctype{kind: kFunc, ret: t}
		if p.is("void") && p.peekAt(1).text == ")" {
			p.next()
		}
		for !p.accept(")") {
			if p.accept("...") {
				ft.variadic = true
				p.expect(")")
				break
			}
			s := p.declSpecs()
			name, pt := p.declarator(s.t)
			if pt.kind == kArray {
				pt = ptrTo(pt.elem)
			}
			ft.params = append(ft.params, pt)
			ft.paramNames = append(ft.paramNames, name)
			if !p.accept(",") {
				p.expect(")")
				break
			}
		}
		return ft
	}
	return t
}

func (p *parser) typeName() *ctype {
	s := p.declSpecs()
	_, t := p.declarator(s.t)
	return t
}

func (p *parser) topLevel() {
	if p.accept(";") {
		return
	}
	line := p.peek().line
	s := p.declSpecs()
	if p.accept(";") {
		return
	}
	for {
		name, t := p.declarator(s.t)
		if name == "" {
			p.errorf("expected a name")
		}
		switch {
		case s.typedef:
			p.typedefs[0][name] = t
			p.u.typedefs = append(p.u.typedefs, typedef{name, t})
		case t.kind == kFunc && p.is("{"):
			f := &funcDecl{name: name, t: t, static: s.static, line: line, addr: map[string]bool{}}
			p.u.protos[name] = t
			p.addr = f.addr
			f.body = p.block()
			p.addr = nil
			p.u.funcs = append(p.u.funcs, f)
			return
		case t.kind == kFunc:
			p.u.protos[name] = t
		default:
			v := &varDecl{name: name, t: t, static: s.static, extern: s.extern, line: line}
			if p.accept("=") {
				v.init = p.initializer()
			}
			fixArrayLen(v)
			p.u.globals = append(p.u.globals, v)
		}
		if !p.accept(",") {
			break
		}
	}
	p.expect(";")
}

// fixArrayLen sets the length of an array declared without one from its
// initializer.
func fixArrayLen(v *varDecl) {
	if v.t.kind != kArray || v.t.n >= 0 || v.init == nil {
		return
	}
	switch in := v.init.(type) {
	case *initList:
		v.t = arrayOf(v.t.elem, int64(len(in.elems)))
	case *strLit:
		v.t = arrayOf(v.t.elem, int64(len(in.s)+1))
	}
}

func (p *parser) initializer() expr {
	if p.is("{") {
		l := p.next().line
		in := &initList{l: l}
		for !p.accept("}") {
			in.elems = append(in.elems, p.initializer())
			if !p.accept(",") {
				p.expect("}")
				break
			}
		}
		return in
	}
	return p.assignment()
}

func (p *parser) block() *blockStmt {
	p.expect("{")
	p.typedefs = append(p.typedefs, map[string]*ctype{})
	b := &blockStmt{}
	for !p.accept("}") {
		b.list = append(b.list, p.statement())
	}
	p.typedefs = p.typedefs[:len(p.typedefs)-1]
	return b
}

func (p *parser) statement() stmt {
	t := p.peek()
	switch {
	case p.is("{"):
		return p.block()
	case p.accept(";"):
		return &emptyStmt{}
	case p.accept("if"):
		p.expect("(")
		c := p.expr()
		p.expect(")")
		s := &ifStmt{c: c, then: p.statement()}
		if p.accept("else") {
			s.els = p.statement()
		}
		return s
	case p.accept("while"):
		p.expect("(")
		c := p.expr()
		p.expect(")")
		return &whileStmt{c: c, body: p.statement()}
	case p.accept("do"):
		body := p.statement()
		p.expect("while")
		p.expect("(")
		c := p.expr()
		p.expect(")")
		p.expect(";")
		return &doStmt{body: body, c: c}
	case p.accept("for"):
		p.expect("(")
		p.typedefs = append(p.typedefs, map[string]*ctype{})
		defer func() { p.typedefs = p.typedefs[:len(p.typedefs)-1] }()
		s := &forStmt{}
		if p.isTypeStart() {
			s.init = p.declaration()
		} else if !p.accept(";") {
			s.init = &exprStmt{p.expr()}
			p.expect(";")
		}
		if !p.is(";") {
			s.c = p.expr()
		}
		p.expect(";")
		if !p.is(")") {
			s.post = p.expr()
		}
		p.expect(")")
		s.body = p.statement()
		return s
	case p.accept("return"):
		s := &returnStmt{l: t.line}
		if !p.is(";") {
			s.x = p.expr()
		}
		p.expect(";")
		return s
	case p.accept("break"):
		p.expect(";")
		return &breakStmt{}
	case p.accept("continue"):
		p.expect(";")
		return &contStmt{}
	case p.accept("switch"):
		p.expect("(")
		x := p.expr()
		p.expect(")")
		if !p.is("{") {
			p.errorf("switch body must be a block")
		}
		return &switchStmt{x: x, body: p.block()}
	case p.accept("case"):
		x := p.conditional()
		p.expect(":")
		return &caseLabel{x: x}
	case p.accept("default"):
		p.expect(":")
		return &caseLabel{}
	case p.is("goto"):
		p.errorf("goto is not supported")
	case p.isTypeStart():
		return p.declaration()
	}
	x := p.expr()
	p.expect(";")
	return &exprStmt{x}
}

func (p *parser) declaration() stmt {
	line := p.peek().line
	s := p.declSpecs()
	d := &declStmt{}
	if p.accept(";") {
		return d
	}
	for {
		name, t := p.declarator(s.t)
		if s.typedef {
			p.typedefs[len(p.typedefs)-1][name] = t
		} else {
			v := &varDecl{name: name, t: t, static: s.static, extern: s.extern, line: line}
			if t.kind == kFunc {
				p.u.protos[name] = t
			} else {
				if p.accept("=") {
					v.init = p.initializer()
				}
				fixArrayLen(v)
				d.vars = append(d.vars, v)
			}
		}
		if !p.accept(",") {
			break
		}
	}
	p.expect(";")
	return d
}

// Expressions, by precedence.

func (p *parser) expr() expr {
	x := p.assignment()
	for p.is(",") {
		l := p.next().line
		x = &binary{l: l, op: ",", x: x, y: p.assignment()}
	}
	return x
}

var assignOps = map[string]bool{"=": true, "+=": true, "-=": true, "*=": true, "/=": true, "%=": true,
	"&=": true, "|=": true, "^=": true, "<<=": true, ">>=": true}

func (p *parser) assignment() expr {
	x := p.conditional()
	if t := p.peek(); t.kind == tkPunct && assignOps[t.text] {
		p.next()
		return &assign{l: t.line, op: t.text, x: x, y: p.assignment()}
	}
	return x
}

func (p *parser) conditional() expr {
	c := p.binaryExpr(0)
	if p.is("?") {
		l := p.next().line
		x := p.expr()
		p.expect(":")
		y := p.conditional()
		return &condExpr{l: l, c: c, x: x, y: y}
	}
	return c
}

var precs = [][]string{
	{"||"}, {"&&"}, {"|"}, {"^"}, {"&"}, {"==", "!="}, {"<", ">", "<=", ">="},
	{"<<", ">>"}, {"+", "-"}, {"*", "/", "%"},
}

func (p *parser) binaryExpr(level int) expr {
	if level == len(precs) {
		return p.castExpr()
	}
	x := p.binaryExpr(level + 1)
	for {
		t := p.peek()
		found := false
		if t.kind == tkPunct {
			for _, op := range precs[level] {
				if t.text == op {
					found = true
				}
			}
		}
		if !found {
			return x
		}
		p.next()
		x = &binary{l: t.line, op: t.text, x: x, y: p.binaryExpr(level + 1)}
	}
}

func (p *parser) isTypeAt(i int) bool {
	t := p.peekAt(i)
	return t.kind == tkIdent && (typeWords[t.text] || p.lookupTypedef(t.text) != nil)
}

func (p *parser) castExpr() expr {
	if p.is("(") && p.isTypeAt(1) {
		l := p.next().line
		t := p.typeName()
		p.expect(")")
		if p.is("{") {
			p.errorf("compound literals are not supported")
		}
		return &cast{l: l, t: t, x: p.castExpr()}
	}
	return p.unaryExpr()
}

func (p *parser) unaryExpr() expr {
	t := p.peek()
	if t.kind == tkPunct {
		switch t.text {
		case "++", "--":
			p.next()
			return &incDec{l: t.line, op: t.text, x: p.unaryExpr()}
		case "-", "+", "~", "!", "*", "&":
			p.next()
			x := p.castExpr()
			if t.text == "&" && p.addr != nil {
				if id, ok := x.(*ident); ok {
					p.addr[id.name] = true
				}
			}
			return &unary{l: t.line, op: t.text, x: x}
		}
	}
	if p.accept("sizeof") {
		if p.is("(") && p.isTypeAt(1) {
			p.next()
			ty := p.typeName()
			p.expect(")")
			return &sizeofT{l: t.line, t: ty}
		}
		return &sizeofE{l: t.line, x: p.unaryExpr()}
	}
	return p.postfix(p.primary())
}

func (p *parser) postfix(x expr) expr {
	for {
		t := p.peek()
		switch {
		case p.accept("["):
			i := p.expr()
			p.expect("]")
			x = &index{l: t.line, x: x, i: i}
		case p.accept("("):
			id, ok := x.(*ident)
			if !ok {
				p.errorf("only named functions can be called")
			}
			c := &call{l: t.line, fn: id.name}
			for !p.accept(")") {
				c.args = append(c.args, p.assignment())
				if !p.accept(",") {
					p.expect(")")
					break
				}
			}
			x = c
		case p.accept("."):
			x = &member{l: t.line, x: x, name: p.ident()}
		case p.accept("->"):
			x = &member{l: t.line, x: x, name: p.ident(), arrow: true}
		case p.is("++") || p.is("--"):
			p.next()
			x = &incDec{l: t.line, op: t.text, post: true, x: x}
		default:
			return x
		}
	}
}

func (p *parser) primary() expr {
	t := p.next()
	switch t.kind {
	case tkInt:
		ty := tInt
		suffix := ""
		for i := len(t.text) - 1; i >= 0 && (t.text[i] == 'u' || t.text[i] == 'U' || t.text[i] == 'l' || t.text[i] == 'L'); i-- {
			suffix += string(t.text[i])
		}
		u := false
		l := false
		for _, c := range suffix {
			if c == 'u' || c == 'U' {
				u = true
			} else {
				l = true
			}
		}
		switch {
		case l || t.ival > 0x7fffffff && !u:
			ty = tLong
			if u || uint64(t.ival) > 0x7fffffffffffffff {
				ty = tULong
			}
		case u:
			ty = tUInt
			if uint64(t.ival) > 0xffffffff {
				ty = tULong
			}
		}
		return &intLit{l: t.line, v: t.ival, t: ty}
	case tkChar:
		return &intLit{l: t.line, v: t.ival, t: tInt}
	case tkFloat:
		return &floatLit{l: t.line, v: t.fval}
	case tkString:
		s := t.str
		for p.peek().kind == tkString {
			s += p.next().str
		}
		return &strLit{l: t.line, s: s}
	case tkIdent:
		if v, ok := p.enums[t.text]; ok {
			return &intLit{l: t.line, v: v, t: tInt}
		}
		return &ident{l: t.line, name: t.text}
	case tkPunct:
		if t.text == "(" {
			x := p.expr()
			p.expect(")")
			return x
		}
	}
	p.pos--
	p.errorf("unexpected %s", t)
	return nil
}

// constExpr evaluates an integer constant expression at parse time.
func (p *parser) constExpr(e expr) int64 {
	switch e := e.(type) {
	case *intLit:
		return e.v
	case *sizeofT:
		return e.t.sizeof()
	case *cast:
		if !e.t.isInteger() {
			break
		}
		return wrap(e.t, p.constExpr(e.x))
	case *unary:
		x := p.constExpr(e.x)
		switch e.op {
		case "-":
			return -x
		case "+":
			return x
		case "~":
			return ^x
		case "!":
			if x == 0 {
				return 1
			}
			return 0
		}
	case *binary:
		x, y := p.constExpr(e.x), p.constExpr(e.y)
		b := func(c bool) int64 {
			if c {
				return 1
			}
			return 0
		}
		switch e.op {
		case "+":
			return x + y
		case "-":
			return x - y
		case "*":
			return x * y
		case "/":
			if y != 0 {
				return x / y
			}
		case "%":
			if y != 0 {
				return x % y
			}
		case "<<":
			return x << y
		case ">>":
			return x >> y
		case "&":
			return x & y
		case "|":
			return x | y
		case "^":
			return x ^ y
		case "==":
			return b(x == y)
		case "!=":
			return b(x != y)
		case "<":
			return b(x < y)
		case ">":
			return b(x > y)
		case "<=":
			return b(x <= y)
		case ">=":
			return b(x >= y)
		case "&&":
			return b(x != 0 && y != 0)
		case "||":
			return b(x != 0 || y != 0)
		}
	case *condExpr:
		if p.constExpr(e.c) != 0 {
			return p.constExpr(e.x)
		}
		return p.constExpr(e.y)
	}
	p.errorf("not an integer constant expression")
	return 0
}
