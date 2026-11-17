// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"fmt"
	"strconv"
)

// The parser: C23 and the GNU extensions cmd/cc supports, checked and
// typed as it goes (ast.go). Declarations are here; expressions are in
// expr.go, statements in stmt.go, initializers in init.go.

type scope struct {
	vars   map[string]*symbol
	tags   map[string]*ctype
	labels map[string]*label // GNU's local labels
	parent *scope
}

type parser struct {
	file string
	toks []token
	pos  int
	sc   *scope
	u    *unit
	fn   *funcDecl

	breakOK     int // depth of loops and switches
	continueOK  int
	sw          *switchStmt
	caseN       int
	recordN     int
	symN        int
	stmtExprs   int
	paramVLA    *[]*symbol // collecting a parameter list\'s VLA lengths
	staticInit  int        // in the initializer of an object of static duration
	typeStorage storage    // the storage class in the last type name
	typeTLS     bool
	pendingVLA  []stmt // declarations of VLA lengths, before the next declaration
}

type parseError struct{ msg string }

func (p *parser) errorAt(at pos, format string, args ...any) {
	panic(parseError{fmt.Sprintf("%s:%d: %s", at.file, at.line, fmt.Sprintf(format, args...))})
}

func (p *parser) errorf(format string, args ...any) {
	p.errorAt(p.here(), format, args...)
}

func (p *parser) here() pos {
	t := p.peek()
	return pos{t.file, t.line}
}

// parse parses a translation unit.
func parse(file string, toks []token) (u *unit, err error) {
	defer func() {
		if r := recover(); r != nil {
			pe, ok := r.(parseError)
			if !ok {
				panic(r)
			}
			err = fmt.Errorf("%s", pe.msg)
		}
	}()
	p := &parser{file: file, toks: toks}
	p.u = &unit{file: file, tags: map[string]*ctype{}}
	p.push()
	p.u.scope = p.sc.vars
	p.u.tags = p.sc.tags
	for p.peek().kind != tkEOF {
		p.topLevel()
	}
	// A tentative definition with no initializer is a definition.
	for _, s := range p.u.syms {
		if s.kind == symVar && s.storage != sExtern && s.t.kind == kArray && s.t.n < 0 {
			s.t = arrayOf(s.t.elem, 1)
		}
	}
	return p.u, nil
}

func (p *parser) push() {
	p.sc = &scope{vars: map[string]*symbol{}, tags: map[string]*ctype{}, parent: p.sc}
}

func (p *parser) pop() { p.sc = p.sc.parent }

func (p *parser) lookup(name string) *symbol {
	for s := p.sc; s != nil; s = s.parent {
		if v, ok := s.vars[name]; ok {
			return v
		}
	}
	return nil
}

func (p *parser) lookupTag(name string) (*ctype, *scope) {
	for s := p.sc; s != nil; s = s.parent {
		if t, ok := s.tags[name]; ok {
			return t, s
		}
	}
	return nil, nil
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) peekAt(i int) token {
	if p.pos+i < len(p.toks) {
		return p.toks[p.pos+i]
	}
	return p.toks[len(p.toks)-1]
}
func (p *parser) next() token {
	t := p.toks[p.pos]
	if t.kind != tkEOF {
		p.pos++
	}
	return t
}
func (p *parser) is(s string) bool {
	t := p.peek()
	return (t.kind == tkPunct || t.kind == tkIdent) && t.text == s
}
func (p *parser) isAt(i int, s string) bool {
	t := p.peekAt(i)
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
		p.errorf("expected an identifier, found %s", t)
	}
	return t.text
}

// Attributes.

type attrs struct {
	aligned  int64
	packed   bool
	noreturn bool
	cleanup  string
	vector   int64
	unused   bool
	weak     bool
}

func (a *attrs) merge(b attrs) {
	a.aligned = max(a.aligned, b.aligned)
	a.packed = a.packed || b.packed
	a.weak = a.weak || b.weak
	a.noreturn = a.noreturn || b.noreturn
	if b.cleanup != "" {
		a.cleanup = b.cleanup
	}
	if b.vector != 0 {
		a.vector = b.vector
	}
}

// attributes parses attribute specifiers and the like, which cmd/cc
// mostly ignores: [[...]], __attribute__((...)), __declspec(...),
// asm labels, __extension__.
func (p *parser) attributes() attrs {
	var a attrs
	for {
		switch {
		case p.is("[") && p.isAt(1, "["):
			p.pos += 2
			for !p.is("]") {
				name := p.ident()
				if p.accept("::") {
					name = p.ident()
				}
				p.attr(&a, name)
				if !p.accept(",") {
					break
				}
			}
			p.expect("]")
			p.expect("]")
		case p.is("__attribute__") || p.is("__attribute"):
			p.next()
			p.expect("(")
			p.expect("(")
			for !p.is(")") {
				if p.accept(",") {
					continue
				}
				t := p.next()
				if t.kind != tkIdent {
					p.errorf("expected an attribute name, found %s", t)
				}
				p.attr(&a, t.text)
			}
			p.expect(")")
			p.expect(")")
		case p.is("__declspec") || p.is("__asm__") || p.is("__asm") || p.is("asm") && p.isAt(1, "("):
			p.next()
			p.skipParens()
		case p.is("__extension__"):
			p.next()
		default:
			return a
		}
	}
}

func (p *parser) attr(a *attrs, name string) {
	if len(name) > 4 && name[:2] == "__" && name[len(name)-2:] == "__" {
		name = name[2 : len(name)-2]
	}
	switch name {
	case "packed":
		a.packed = true
	case "noreturn", "_Noreturn":
		a.noreturn = true
	case "aligned":
		if p.accept("(") {
			a.aligned = max(a.aligned, p.constInt(p.conditional()))
			p.expect(")")
		} else {
			a.aligned = 16
		}
		return
	case "cleanup":
		p.expect("(")
		a.cleanup = p.ident()
		p.expect(")")
		return
	case "vector_size":
		p.expect("(")
		a.vector = p.constInt(p.conditional())
		p.expect(")")
		return
	case "unused", "maybe_unused":
		a.unused = true
	case "weak":
		a.weak = true
	}
	if p.is("(") {
		p.skipParens()
	}
}

func (p *parser) skipParens() {
	p.expect("(")
	depth := 1
	for depth > 0 {
		t := p.next()
		switch {
		case t.kind == tkEOF:
			p.errorf("unbalanced parentheses")
		case t.text == "(" && t.kind == tkPunct:
			depth++
		case t.text == ")" && t.kind == tkPunct:
			depth--
		}
	}
}

// Declaration specifiers.

type specs struct {
	t        *ctype
	storage  storage
	tls      bool
	inline   bool
	noreturn bool
	constx   bool
	auto     bool // C23 auto: the type is the initializer's
	q        qual // with auto: the qualifiers
	align    int64
	attrs    attrs
}

var typeWords = map[string]bool{
	"void": true, "char": true, "short": true, "int": true, "long": true, "float": true, "double": true,
	"signed": true, "unsigned": true, "_Bool": true, "bool": true, "struct": true, "union": true, "enum": true,
	"const": true, "volatile": true, "restrict": true, "_Atomic": true, "typedef": true, "extern": true,
	"static": true, "auto": true, "register": true, "inline": true, "_Noreturn": true, "_Thread_local": true,
	"thread_local": true, "_Alignas": true, "alignas": true, "typeof": true, "typeof_unqual": true,
	"__typeof__": true, "__typeof": true, "__typeof_unqual__": true, "constexpr": true, "__inline": true,
	"__inline__": true, "__restrict": true, "__restrict__": true, "__const": true, "__const__": true,
	"__volatile__": true, "__volatile": true, "__signed__": true, "__signed": true, "__auto_type": true,
	"_Complex": true, "__builtin_va_list": true, "__attribute__": true, "__attribute": true,
	"__extension__": true, "_BitInt": true, "__int128": true, "__int128__": true, "_Float32": true, "_Float64": true,
	"__thread": true, "__declspec": true, "__complex__": true, "__complex": true, "_Decimal32": true, "_Decimal64": true,
}

// isTypeStart reports whether the token at i starts declaration
// specifiers or a type name.
func (p *parser) isTypeStart(i int) bool {
	t := p.peekAt(i)
	if t.kind == tkPunct && t.text == "[" && p.isAt(i+1, "[") {
		return true
	}
	if t.kind != tkIdent {
		return false
	}
	if typeWords[t.text] {
		return true
	}
	s := p.lookup(t.text)
	return s != nil && s.kind == symTypedef
}

func (p *parser) declSpecs() specs {
	var s specs
	var q qual
	nlong, nint := 0, 0
	bitN := int64(0)
	cplx := false
	unsigned, signed, short := false, false, false
	base := ""
	seen := false
	for {
		a := p.attributes()
		s.attrs.merge(a)
		t := p.peek()
		if t.kind != tkIdent {
			break
		}
		w := t.text
		switch w {
		case "typedef":
			s.storage = sTypedef
		case "extern":
			s.storage = sExtern
		case "static":
			s.storage = sStatic
		case "register":
			s.storage = sRegister
		case "auto":
			// C23: with no type, the type is inferred.
			s.auto = true
		case "__auto_type":
			s.auto = true
		case "_Thread_local", "thread_local", "__thread":
			s.tls = true
		case "inline", "__inline", "__inline__":
			s.inline = true
		case "_Noreturn":
			s.noreturn = true
		case "constexpr":
			s.constx = true
			q |= qConst
		case "const", "__const", "__const__":
			q |= qConst
		case "volatile", "__volatile", "__volatile__":
			q |= qVolatile
		case "restrict", "__restrict", "__restrict__":
			q |= qRestrict
		case "_Atomic":
			if p.isAt(1, "(") {
				p.next()
				p.next()
				s.t = p.typeName()
				p.expect(")")
				seen = true
				continue
			}
			q |= qAtomic
		case "_Alignas", "alignas":
			p.next()
			p.expect("(")
			var a int64
			if p.isTypeStart(0) {
				a = p.typeName().alignof()
			} else {
				a = p.constInt(p.conditional())
			}
			p.expect(")")
			s.align = max(s.align, a)
			continue
		case "unsigned":
			unsigned = true
		case "signed", "__signed", "__signed__":
			signed = true
		case "short":
			short = true
		case "long":
			nlong++
		case "int":
			nint++
		case "void", "char", "float", "double", "_Bool", "bool", "__builtin_va_list", "_Float32", "_Float64":
			if base != "" || s.t != nil {
				p.errorf("two or more data types in declaration specifiers")
			}
			base = w
		case "_Complex", "__complex__", "__complex":
			cplx = true
		case "_BitInt":
			p.next()
			p.expect("(")
			n := p.constInt(p.conditional())
			p.expect(")")
			if n > 64 {
				p.errorf("_BitInt(%d) is wider than BITINT_MAXWIDTH (64)", n)
			}
			s.t = bitInt(n, false) // signedness below
			bitN = n
			seen = true
			continue
		case "__int128", "__int128__":
			if base != "" || s.t != nil {
				p.errorf("two or more data types in declaration specifiers")
			}
			base = "__int128"
		case "_Decimal32", "_Decimal64":
			p.errorf("decimal floating types are not supported")
		case "struct", "union":
			p.next()
			s.t = p.recordSpec(w == "union", t)
			seen = true
			continue
		case "enum":
			p.next()
			s.t = p.enumSpec()
			seen = true
			continue
		case "typeof", "typeof_unqual", "__typeof__", "__typeof", "__typeof_unqual__":
			p.next()
			p.expect("(")
			var ty *ctype
			if p.isTypeStart(0) {
				ty = p.typeName()
			} else {
				ty = p.expr().ty()
			}
			p.expect(")")
			if w == "typeof_unqual" || w == "__typeof_unqual__" {
				ty = ty.unqual()
			}
			s.t = ty
			seen = true
			continue
		default:
			if base == "" && s.t == nil && !unsigned && !signed && !short && nlong == 0 && nint == 0 {
				if sym := p.lookup(w); sym != nil && sym.kind == symTypedef {
					// A typedef name, unless it is the declarator: auto
					// T = 1; declares T.
					if !(s.auto && (p.isAt(1, "=") || p.isAt(1, ";") || p.isAt(1, ","))) {
						s.t = sym.t
						sym.used = true
						seen = true
						p.next()
						continue
					}
				}
			}
			goto done
		}
		seen = true
		p.next()
	}
done:
	if cplx {
		switch {
		case base == "float":
			s.t = complexOf(tFloat)
		case base == "double" && nlong > 0:
			s.t = complexOf(tLDouble)
		case base == "double" || base == "":
			s.t = complexOf(tDouble)
		default:
			p.errorf("_Complex %s is not supported", base)
		}
	}
	if bitN > 0 {
		s.t = bitInt(bitN, unsigned)
		if bitN < 1 || bitN < 2 && !unsigned {
			p.errorf("_BitInt(%d) is too narrow", bitN)
		}
	}
	if s.t == nil && base == "__int128" {
		s.t = &ctype{kind: kInt128}
		if unsigned {
			s.t = &ctype{kind: kUInt128}
		}
	}
	if s.t == nil {
		switch {
		case base == "void":
			s.t = tVoid
		case base == "_Bool" || base == "bool":
			s.t = tBool
		case base == "__builtin_va_list":
			s.t = tVaList
		case base == "float" || base == "_Float32":
			s.t = tFloat
		case base == "double" || base == "_Float64":
			s.t = tDouble
			if nlong > 0 {
				s.t = tLDouble
			}
		case base == "char":
			s.t = tChar
			if unsigned {
				s.t = tUChar
			} else if signed {
				s.t = tSChar
			}
		case short:
			s.t = tShort
			if unsigned {
				s.t = tUShort
			}
		case nlong == 1:
			s.t = tLong
			if unsigned {
				s.t = tULong
			}
		case nlong >= 2:
			s.t = tLLong
			if unsigned {
				s.t = tULLong
			}
		case unsigned:
			s.t = tUInt
		case signed || nint > 0:
			s.t = tInt
		case s.auto:
			s.t = nil // inferred
		default:
			if !seen {
				p.errorf("expected a type, found %s", p.peek())
			}
			// C89's implicit int.
			s.t = tInt
		}
	}
	if s.t != nil {
		s.t = s.t.qualified(q)
	} else if q != 0 {
		s.auto = true
	}
	s.q = q
	return s
}

// recordSpec parses a struct or union specifier, after its keyword.
func (p *parser) recordSpec(union bool, kw token) *ctype {
	a := p.attributes()
	tag := ""
	if p.peek().kind == tkIdent && !p.is("__attribute__") {
		tag = p.ident()
	}
	a.merge(p.attributes())
	if tag != "" && !p.is("{") {
		if t, _ := p.lookupTag(tag); t != nil && !p.is(";") {
			if t.kind != kStruct || t.rec.union != union {
				p.errorf("%q defined as the wrong kind of tag", tag)
			}
			return t
		}
		if t, ok := p.sc.tags[tag]; ok {
			return t
		}
		// A new incomplete type.
		t := &ctype{kind: kStruct, rec: &record{tag: tag, union: union, id: p.nextRecord()}}
		p.sc.tags[tag] = t
		return t
	}
	if !p.is("{") {
		p.errorf("expected a struct or union body")
	}
	p.next()
	var t *ctype
	if tag != "" {
		if old, ok := p.sc.tags[tag]; ok && !old.rec.complete {
			if old.rec.union != union {
				p.errorf("%q defined as the wrong kind of tag", tag)
			}
			t = old
		}
	}
	var prev *ctype // a complete type of the same tag in this scope
	if t == nil {
		t = &ctype{kind: kStruct, rec: &record{tag: tag, union: union, id: p.nextRecord()}}
		if tag != "" {
			prev = p.sc.tags[tag]
			p.sc.tags[tag] = t // in scope in its own body
		}
	}
	r := t.rec
	if kw.pack > 0 {
		r.packed = kw.pack
	}
	var fields []*field
	for !p.accept("}") {
		if p.is("_Static_assert") || p.is("static_assert") {
			p.staticAssert()
			continue
		}
		if p.accept(";") {
			continue
		}
		s := p.declSpecs()
		if s.t == nil {
			p.errorf("expected a member type")
		}
		if p.accept(";") {
			// An anonymous struct or union.
			if s.t.kind == kStruct && (s.t.rec.tag == "" || true) {
				fields = append(fields, &field{t: s.t, bits: -1})
			}
			continue
		}
		for {
			f := &field{bits: -1}
			if p.is(":") {
				f.t = s.t
			} else {
				f.name, f.t, _ = p.declarator(s.t)
			}
			fa := p.attributes()
			if p.accept(":") {
				f.bits = int(p.constInt(p.conditional()))
				if !f.t.isInteger() {
					p.errorf("bit-field %q has an invalid type", f.name)
				}
				if f.bits < 0 || int64(f.bits) > f.t.sizeof()*8 {
					p.errorf("width of bit-field %q exceeds its type", f.name)
				}
				if f.bits == 0 && f.name != "" {
					p.errorf("zero width for bit-field %q", f.name)
				}
			}
			fa.merge(p.attributes())
			al := max(s.align, fa.aligned, s.attrs.aligned)
			if al > 0 {
				c := *f.t
				c.align = max(c.align, al)
				f.t = &c
			}
			if fa.packed || s.attrs.packed {
				// A packed member: alignment 1.
				f.packed = true
			}
			fields = append(fields, f)
			if !p.accept(",") {
				break
			}
		}
		p.expect(";")
	}
	a.merge(p.attributes())
	for _, f := range fields {
		if f.t.kind == kArray && f.t.n < 0 {
			// GCC: not last, it is an array of none.
			f.t = arrayOf(f.t.elem, 0)
			r.flex = true
		} else if !f.t.isComplete() && !(f.t.kind == kArray && f.t.n == 0) {
			p.errorf("field %q has incomplete type", f.name)
		}
	}
	r.fields = fields
	if a.packed {
		r.packed = 1
	}
	r.layout()
	if a.aligned > 0 {
		r.align = max(r.align, a.aligned)
		r.size = (r.size + r.align - 1) / r.align * r.align
	}
	if prev != nil {
		// C23: the same tag and contents again is the same type.
		if prev.kind != kStruct || !sameRecord(prev.rec, r, nil) {
			p.errorf("redefinition of %q", tag)
		}
		p.sc.tags[tag] = prev
		return prev
	}
	p.u.records = append(p.u.records, t)
	return t
}

func (p *parser) nextRecord() int {
	p.recordN++
	return p.recordN
}

// enumSpec parses an enum specifier, after its keyword.
func (p *parser) enumSpec() *ctype {
	p.attributes()
	tag := ""
	if p.peek().kind == tkIdent {
		tag = p.ident()
	}
	p.attributes()
	var fixed *ctype
	if p.is(":") && p.isTypeStart(1) {
		p.next()
		fixed = p.specQualType()
	}
	if !p.is("{") {
		if tag == "" {
			p.errorf("expected an enum body")
		}
		if t, _ := p.lookupTag(tag); t != nil {
			return t
		}
		// A forward reference: int, until the body says.
		t := &ctype{kind: kUInt, enum: &enumType{tag: tag}}
		if fixed != nil {
			t = &ctype{kind: fixed.kind, enum: &enumType{tag: tag, complete: true, fixed: true}}
		}
		p.sc.tags[tag] = t
		return t
	}
	p.next()
	var t *ctype
	if tag != "" {
		if old, ok := p.sc.tags[tag]; ok && !old.enum.complete {
			t = old
		}
	}
	if t == nil {
		t = &ctype{kind: kInt, enum: &enumType{tag: tag}}
	}
	var consts []*symbol
	next := int64(0)
	nextUnsigned := false
	minV, maxV := int64(0), int64(0)
	anyNeg, big := false, false
	for !p.accept("}") {
		at := p.here()
		name := p.ident()
		p.attributes()
		ct := tInt
		if p.accept("=") {
			e := p.conditional()
			v, ok := p.foldInt(e)
			if !ok {
				p.errorAt(at, "enumerator value for %q is not an integer constant", name)
			}
			next = v
			nextUnsigned = e.ty().isUnsigned() && e.ty().sizeof() == 8
			if fixed != nil {
				ct = fixed
			} else if e.ty().isInteger() && promote(e.ty()).sizeof() > 4 {
				ct = promote(e.ty())
			}
		} else if fixed == nil && (next > 0x7fffffff || next < -0x80000000) {
			ct = tLong
			if nextUnsigned {
				ct = tULong
			}
		}
		v := next
		if fixed != nil {
			ct = fixed
		} else if ct == tInt && (v > 0x7fffffff || v < -0x80000000) {
			ct = tLong
		}
		if v < 0 && !nextUnsigned {
			anyNeg = true
		}
		if v > 0xffffffff || v < -0x80000000 || nextUnsigned && v < 0 {
			big = true
		}
		if len(consts) == 0 || v < minV {
			minV = v
		}
		if len(consts) == 0 || v > maxV {
			maxV = v
		}
		sym := &symbol{name: name, kind: symEnumConst, t: ct, val: v, p: at}
		p.declare(sym, at)
		consts = append(consts, sym)
		next = v + 1
		if !p.accept(",") {
			p.expect("}")
			break
		}
	}
	// The type: as GCC chooses it.
	kind := kUInt
	switch {
	case fixed != nil:
		kind = fixed.kind
	case big && anyNeg:
		kind = kLong
	case big:
		kind = kULong
		if maxV <= 0x7fffffffffffffff && !nextUnsigned {
			kind = kLong
			if minV >= 0 {
				kind = kULong
			}
		}
	case anyNeg:
		kind = kInt
		if maxV > 0x7fffffff {
			kind = kLong
		}
	case maxV > 0xffffffff:
		kind = kULong
	}
	t.kind = kind
	t.enum.complete = true
	t.enum.fixed = fixed != nil
	// C23: after the body, the constants have the enum's type if int
	// can't hold them all.
	fitInt := true
	for _, c := range consts {
		if c.val > 0x7fffffff || c.val < -0x80000000 || c.t.isUnsigned() && c.t.sizeof() == 8 && c.val < 0 {
			fitInt = false
		}
	}
	for _, c := range consts {
		if fixed != nil || !fitInt {
			c.t = t
		} else {
			c.t = tInt
		}
	}
	if tag != "" {
		p.sc.tags[tag] = t
	}
	return t
}

// specQualType parses a specifier-qualifier list as a type.
func (p *parser) specQualType() *ctype {
	s := p.declSpecs()
	if s.t == nil {
		p.errorf("expected a type")
	}
	return s.t
}

// Declarators.

// declarator parses a declarator for base: the name ("" if abstract),
// the type, and the attributes on the declarator.
func (p *parser) declarator(base *ctype) (string, *ctype, attrs) {
	a := p.attributes()
	t := base
	for p.is("*") || p.is("^") {
		p.next()
		t = ptrTo(t)
		for {
			a.merge(p.attributes())
			switch {
			case p.is("const") || p.is("__const") || p.is("__const__"):
				t = t.qualified(qConst)
			case p.is("volatile") || p.is("__volatile__") || p.is("__volatile"):
				t = t.qualified(qVolatile)
			case p.is("restrict") || p.is("__restrict") || p.is("__restrict__"):
				t = t.qualified(qRestrict)
			case p.is("_Atomic"):
				t = t.qualified(qAtomic)
			default:
				goto ptrDone
			}
			p.next()
		}
	ptrDone:
	}
	a.merge(p.attributes())
	if p.is("(") && p.nestedDeclarator() {
		// ( declarator ): parse what follows it first, then the inner
		// declarator with that type.
		p.next()
		start := p.pos
		depth := 1
		for depth > 0 {
			tk := p.next()
			if tk.kind == tkEOF {
				p.errorf("unbalanced parentheses in declarator")
			}
			if tk.kind == tkPunct && tk.text == "(" {
				depth++
			} else if tk.kind == tkPunct && tk.text == ")" {
				depth--
			}
		}
		t = p.suffixes(t)
		end := p.pos
		p.pos = start
		name, it, ia := p.declarator(t)
		p.expect(")")
		p.pos = end
		a.merge(ia)
		return name, it, a
	}
	name := ""
	if p.peek().kind == tkIdent && !p.isTypeStartWord() {
		name = p.ident()
	}
	a.merge(p.attributes())
	t = p.suffixes(t)
	a.merge(p.attributes())
	return name, t, a
}

// nestedDeclarator reports whether the "(" next starts a parenthesized
// declarator, not a function's parameters: (*p), (ATTR *p).
func (p *parser) nestedDeclarator() bool {
	k := 1
	for {
		switch {
		case p.isAt(k, "__attribute__") || p.isAt(k, "__attribute"):
			// __attribute__((...))
			depth := 0
			k++
			for {
				t := p.peekAt(k)
				if t.kind == tkEOF {
					return false
				}
				if t.text == "(" {
					depth++
				} else if t.text == ")" {
					depth--
					if depth == 0 {
						k++
						break
					}
				}
				k++
			}
			continue
		case p.isAt(k, "[") && p.isAt(k+1, "["):
			depth := 0
			for {
				t := p.peekAt(k)
				if t.kind == tkEOF {
					return false
				}
				if t.text == "[" {
					depth++
				} else if t.text == "]" {
					depth--
					if depth == 0 {
						k++
						break
					}
				}
				k++
			}
			continue
		}
		break
	}
	if p.isAt(k, ")") {
		return false
	}
	t := p.peekAt(k)
	if t.kind == tkPunct {
		return t.text == "*" || t.text == "(" || t.text == "^" || t.text == "["
	}
	return !p.isTypeStart(k)
}

// isTypeStartWord reports whether the next identifier is a keyword that
// cannot be a declarator's name.
func (p *parser) isTypeStartWord() bool {
	t := p.peek()
	return t.kind == tkIdent && typeWords[t.text]
}

// suffixes parses array and function declarator suffixes.
func (p *parser) suffixes(t *ctype) *ctype {
	switch {
	case p.is("["):
		p.next()
		for p.is("static") || p.is("const") || p.is("restrict") || p.is("volatile") || p.is("__restrict") {
			p.next()
		}
		n := int64(-1)
		var vla expr
		if p.accept("*") {
			// [*]: a VLA of unspecified size, in a prototype.
			p.expect("]")
			inner := p.suffixes(t)
			return arrayOf(inner, -1)
		}
		if !p.is("]") {
			e := p.assignment()
			if v, ok := p.foldInt(e); ok {
				if v < 0 {
					p.errorf("size of array is negative")
				}
				n = v
			} else {
				if !e.ty().isInteger() {
					p.errorf("size of array has non-integer type")
				}
				vla = e
			}
		}
		p.expect("]")
		p.attributes()
		inner := p.suffixes(t)
		if inner.kind == kFunc {
			p.errorf("declaration of an array of functions")
		}
		at := arrayOf(inner, n)
		if n > 0 && inner.sizeof() > 0 && n > (1<<47)/inner.sizeof() {
			p.errorf("size of array is too large")
		}
		if vla != nil {
			at.vla = p.vlaLength(vla, inner)
		}
		return at
	case p.is("("):
		p.next()
		ft := p.params()
		ret := p.suffixes(t)
		if ret.kind == kFunc || ret.kind == kArray {
			p.errorf("function returning an array or function")
		}
		ft.ret = ret
		return &ctype{kind: kFunc, fn: ft}
	}
	return t
}

// vlaLength makes a variable length array's length a local, computed
// where the declaration is, and returns a reference to it.
func (p *parser) vlaLength(e expr, elem *ctype) expr {
	if p.paramVLA != nil {
		// A parameter's: computed on entry, if the function is defined.
		p.symN++
		sym := &symbol{name: fmt.Sprintf("vla.%d", p.symN), kind: symVar, t: tULong, p: e.at()}
		sym.init = &initializer{scalar: p.convert(e, tULong)}
		*p.paramVLA = append(*p.paramVLA, sym)
		return &varRef{node{tULong, e.at()}, sym}
	}
	if p.fn == nil {
		// File scope: a pointer anyway.
		return &intConst{node{tULong, e.at()}, 0}
	}
	sym := p.newLocal("vla", tULong, e.at())
	sym.init = &initializer{scalar: p.convert(e, tULong)}
	p.pendingVLA = append(p.pendingVLA, &declStmt{syms: []*symbol{sym}})
	return &varRef{node{tULong, e.at()}, sym}
}

// newLocal makes an unnamed local of the current function.
func (p *parser) newLocal(what string, t *ctype, at pos) *symbol {
	p.symN++
	sym := &symbol{name: fmt.Sprintf("%s.%d", what, p.symN), kind: symVar, t: t, p: at}
	p.fn.locals = append(p.fn.locals, sym)
	return sym
}

// params parses a function's parameter list, after its "(".
func (p *parser) params() *funcType {
	ft := &funcType{proto: true}
	if p.accept(")") {
		ft.proto = false
		return ft
	}
	if p.is("void") && p.isAt(1, ")") {
		p.pos += 2
		return ft
	}
	if p.peek().kind == tkIdent && !p.isTypeStart(0) {
		// K&R: a list of names.
		ft.proto = false
		for {
			ft.names = append(ft.names, p.ident())
			if !p.accept(",") {
				break
			}
		}
		p.expect(")")
		return ft
	}
	p.push()
	saveVLA := p.paramVLA
	var vlas []*symbol
	p.paramVLA = &vlas
	defer func() {
		ft.vlas = vlas
		p.paramVLA = saveVLA
	}()
	for {
		if p.accept("...") {
			ft.variadic = true
			break
		}
		p.attributes()
		s := p.declSpecs()
		if s.t == nil {
			p.errorf("expected a parameter type")
		}
		name, t, _ := p.declarator(s.t)
		t = adjustParam(t)
		ft.params = append(ft.params, t)
		ft.names = append(ft.names, name)
		if name != "" {
			// In scope for later parameters' sizes.
			ps := &symbol{name: name, kind: symVar, t: t}
			p.sc.vars[name] = ps
			for len(ft.psyms) < len(ft.params)-1 {
				ft.psyms = append(ft.psyms, nil)
			}
			ft.psyms = append(ft.psyms, ps)
		}
		if !p.accept(",") {
			break
		}
	}
	p.pop()
	p.expect(")")
	if len(ft.params) == 0 && !ft.variadic {
		p.errorf("expected a parameter")
	}
	return ft
}

// adjustParam is a parameter's type: arrays and functions become
// pointers.
func adjustParam(t *ctype) *ctype {
	switch t.kind {
	case kArray:
		pt := ptrTo(t.elem)
		pt.q = t.q
		return pt
	case kFunc:
		return ptrTo(t)
	}
	return t
}

// typeName parses a type name: specifiers and an abstract declarator.
func (p *parser) typeName() *ctype {
	s := p.declSpecs()
	if s.t == nil {
		p.errorf("expected a type")
	}
	name, t, _ := p.declarator(s.t)
	if name != "" {
		p.errorf("unexpected name %q in a type name", name)
	}
	// For a compound literal: C23's storage classes.
	p.typeStorage = s.storage
	p.typeTLS = s.tls
	if s.align > 0 {
		c := *t
		c.align = s.align
		t = &c
	}
	return t
}

// Declarations.

func (p *parser) declare(s *symbol, at pos) {
	if old, ok := p.sc.vars[s.name]; ok {
		if old.kind == symTypedef && s.kind == symTypedef && compatible(old.t, s.t) {
			return
		}
		if (old.kind == symVar || old.kind == symFunc) && old.kind == s.kind && old.global && s.global {
			return
		}
		p.errorAt(at, "redefinition of %q", s.name)
	}
	p.sc.vars[s.name] = s
}

func (p *parser) staticAssert() {
	at := p.here()
	p.next()
	p.expect("(")
	e := p.conditional()
	v, ok := p.foldInt(e)
	if !ok {
		p.errorAt(at, "static assertion is not an integer constant expression")
	}
	msg := ""
	if p.accept(",") {
		t := p.next()
		if t.kind != tkString {
			p.errorAt(at, "expected a string literal")
		}
		msg = t.str
	}
	p.expect(")")
	p.expect(";")
	if v == 0 {
		p.errorAt(at, "static assertion failed: %q", msg)
	}
}

// topLevel parses an external declaration.
func (p *parser) topLevel() {
	if p.accept(";") {
		return
	}
	if p.is("_Static_assert") || p.is("static_assert") {
		p.staticAssert()
		return
	}
	if p.is("asm") || p.is("__asm__") {
		p.next()
		p.skipParens()
		p.expect(";")
		return
	}
	at := p.here()
	s := p.declSpecs()
	if s.t == nil && !s.auto {
		p.errorf("expected a declaration")
	}
	if p.accept(";") {
		return
	}
	first := true
	for {
		name, t, a := p.declarator(p.baseOr(s))
		a.merge(s.attrs)
		if name == "" {
			p.errorf("expected a declarator")
		}
		if t.kind == kFunc && first && (p.is("{") || p.isKRDecls(t)) {
			p.funcDef(name, t, s, a, at)
			return
		}
		first = false
		p.declarator2(name, t, s, a, at)
		if !p.accept(",") {
			break
		}
	}
	p.expect(";")
}

func (p *parser) baseOr(s specs) *ctype {
	if s.t == nil {
		return tInt // replaced by the initializer's type
	}
	return s.t
}

// isKRDecls reports whether K&R parameter declarations follow.
func (p *parser) isKRDecls(t *ctype) bool {
	return !t.fn.proto && len(t.fn.names) > 0 && p.isTypeStart(0)
}

// declarator2 declares name, of type t, at file or block scope, and
// parses its initializer.
func (p *parser) declarator2(name string, t *ctype, s specs, a attrs, at pos) *symbol {
	if s.storage == sTypedef {
		if s.align > 0 || a.aligned > 0 {
			c := *t
			c.align = max(s.align, a.aligned)
			t = &c
		}
		if a.vector > 0 {
			p.errorAt(at, "vector types are not supported")
		}
		sym := &symbol{name: name, kind: symTypedef, t: t, p: at}
		p.declare(sym, at)
		return sym
	}
	if t.kind == kFunc {
		if a.noreturn || s.noreturn {
			ft := *t.fn
			ft.noreturn = true
			t = &ctype{kind: kFunc, fn: &ft, q: t.q}
		}
		sym := p.declareFunc(name, t, s, at)
		sym.weak = sym.weak || a.weak
		return sym
	}
	global := p.fn == nil || s.storage == sStatic || s.storage == sExtern
	sym := &symbol{name: name, kind: symVar, t: t, storage: s.storage, global: global, tls: s.tls, p: at, constx: s.constx}
	if p.fn == nil {
		sym.linkage = 'e'
		if s.storage == sStatic {
			sym.linkage = 'i'
		}
	} else if s.storage == sExtern {
		sym.linkage = 'e'
	}
	if s.align > 0 || a.aligned > 0 {
		c := *t
		c.align = max(s.align, a.aligned)
		sym.t = &c
	}
	if a.cleanup != "" {
		f := p.lookup(a.cleanup)
		if f == nil || f.kind != symFunc {
			p.errorAt(at, "cleanup argument not a function")
		}
		if global {
			p.errorAt(at, "the cleanup attribute applies only to automatic variables")
		}
		sym.cleanup = f
		sym.addr = true // f(&it)
	}
	// The name is in scope in its initializer.
	if old := p.sc.vars[name]; old != nil && old.global && sym.global && old.kind == symVar {
		if !compatible(old.t.unqual(), sym.t.unqual()) && !(old.t.kind == kArray && sym.t.kind == kArray && compatible(old.t.elem, sym.t.elem)) {
			p.errorAt(at, "conflicting types for %q", name)
		}
		if old.t.kind == kArray && old.t.n < 0 {
			old.t = sym.t
		}
		if s.storage != sExtern {
			old.storage = s.storage
			if old.storage == sStatic {
				old.linkage = 'i'
			}
		}
		sym = old
	} else if p.fn != nil && s.storage == sExtern {
		// A block-scope extern: the file-scope object.
		if g := p.u.scope[name]; g != nil && g.kind == symVar {
			p.sc.vars[name] = g
			return g
		}
		p.declare(sym, at)
		p.u.syms = append(p.u.syms, sym)
		return sym
	} else {
		p.declare(sym, at)
		if p.fn == nil {
			p.u.syms = append(p.u.syms, sym)
		} else if global {
			p.u.statics = append(p.u.statics, sym)
		} else {
			p.fn.locals = append(p.fn.locals, sym)
		}
	}
	if p.accept("=") {
		if s.storage == sExtern && p.fn != nil {
			p.errorAt(at, "%q has both extern and an initializer", name)
		}
		if sym.defined && sym.init != nil {
			p.errorAt(at, "redefinition of %q", name)
		}
		if s.auto && s.t == nil {
			// C23 auto: the initializer's type, after lvalue conversion.
			e := p.rvalue(p.initExpr())
			sym.t = e.ty().unqual().qualified(s.q)
			sym.init = p.initFor(sym.t, e)
		} else {
			if sym.global {
				p.staticInit++
			}
			sym.init = p.initializer(sym)
			if sym.global {
				p.staticInit--
			}
		}
		sym.defined = true
		if sym.storage == sExtern {
			sym.storage = sAuto
		}
	} else if s.auto && s.t == nil {
		p.errorAt(at, "%q declared auto without an initializer", name)
	} else if s.storage != sExtern {
		sym.defined = true
	}
	if s.constx {
		p.constexprValue(sym, at)
	}
	if !global && !sym.t.isComplete() && sym.t.vla == nil {
		p.errorAt(at, "storage size of %q isn't known", name)
	}
	return sym
}

func (p *parser) declareFunc(name string, t *ctype, s specs, at pos) *symbol {
	if old := p.lookupFunc(name); old != nil {
		if !compatible(old.t, t) {
			p.errorAt(at, "conflicting types for %q", name)
		}
		old.t = composite(old.t, t)
		if s.storage == sStatic {
			old.linkage = 'i'
		}
		p.sc.vars[name] = old
		return old
	}
	sym := &symbol{name: name, kind: symFunc, t: t, global: true, linkage: 'e', p: at}
	if s.storage == sStatic {
		sym.linkage = 'i'
	}
	if p.fn != nil {
		p.sc.vars[name] = sym
		if g := p.u.scope[name]; g == nil {
			p.u.scope[name] = sym
			p.u.syms = append(p.u.syms, sym)
		} else {
			p.sc.vars[name] = g
			return g
		}
		return sym
	}
	p.declare(sym, at)
	p.u.syms = append(p.u.syms, sym)
	return sym
}

// lookupFunc finds an earlier declaration of a function at file scope.
func (p *parser) lookupFunc(name string) *symbol {
	if p.fn == nil {
		if s := p.sc.vars[name]; s != nil && s.kind == symFunc {
			return s
		}
		return nil
	}
	if s := p.lookup(name); s != nil && s.kind == symFunc {
		return s
	}
	return nil
}

// funcDef parses a function definition after its declarator.
func (p *parser) funcDef(name string, t *ctype, s specs, a attrs, at pos) {
	if a.noreturn || s.noreturn {
		ft := *t.fn
		ft.noreturn = true
		t = &ctype{kind: kFunc, fn: &ft}
	}
	ft := t.fn
	// K&R parameter declarations.
	if !ft.proto && len(ft.names) > 0 {
		types := map[string]*ctype{}
		for p.isTypeStart(0) {
			ds := p.declSpecs()
			for {
				pn, pt, _ := p.declarator(ds.t)
				types[pn] = adjustParam(pt)
				if !p.accept(",") {
					break
				}
			}
			p.expect(";")
		}
		nft := *ft
		nft.params = nil
		for _, n := range ft.names {
			pt := types[n]
			if pt == nil {
				pt = tInt
			}
			nft.params = append(nft.params, promoteArg(pt))
		}
		ft = &nft
		t = &ctype{kind: kFunc, fn: ft}
	}
	sym := p.declareFunc(name, t, s, at)
	if sym.defined {
		p.errorAt(at, "redefinition of %q", name)
	}
	sym.defined = true
	f := &funcDecl{sym: sym, labels: map[string]*label{}, inline: s.inline}
	sym.fn = f
	p.fn = f
	p.push()
	for i, pt := range ft.params {
		pn := ""
		if i < len(ft.names) {
			pn = ft.names[i]
		}
		ps := &symbol{name: pn, kind: symVar, t: pt, p: at, isParam: true}
		if i < len(ft.psyms) && ft.psyms[i] != nil {
			// The one its parameters' VLA lengths refer to.
			ps = ft.psyms[i]
			ps.t, ps.p, ps.isParam = pt, at, true
		}
		if pn == "" {
			ps.name = fmt.Sprintf("_%d", i)
		} else {
			p.declare(ps, at)
		}
		f.params = append(f.params, ps)
	}
	// __func__.
	fs := &symbol{name: "__func__", kind: symVar, t: arrayOf(tChar.qualified(qConst), int64(len(name)+1)), global: true, storage: sStatic, p: at}
	fs.init = &initializer{items: []initItem{{t: fs.t, x: &strLit{node{arrayOf(tChar, int64(len(name)+1)), at}, name}}}}
	fs.defined = true
	p.sc.vars["__func__"] = fs
	p.sc.vars["__FUNCTION__"] = fs
	p.u.statics = append(p.u.statics, fs)
	f.locals = append(f.locals, ft.vlas...)
	f.body = p.blockBody()
	if len(ft.vlas) > 0 {
		// The parameters' VLA lengths, on entry, in order.
		f.body.list = append([]stmt{&declStmt{syms: ft.vlas}}, f.body.list...)
	}
	p.pop()
	p.fn = nil
	for _, l := range f.labels {
		if l.p.line == 0 {
			p.errorAt(l.p, "label %q used but not defined", l.name)
		}
	}
	p.u.funcs = append(p.u.funcs, f)
}

// promoteArg is the default argument promotion of t.
func promoteArg(t *ctype) *ctype {
	if t.kind == kFloat {
		return tDouble
	}
	if t.isInteger() {
		return promote(t)
	}
	return t
}

// constexprValue checks a constexpr object's initializer and keeps its
// value, for constant expressions.
func (p *parser) constexprValue(sym *symbol, at pos) {
	if sym.init == nil {
		p.errorAt(at, "constexpr %q has no initializer", sym.name)
	}
	if sym.init.scalar != nil {
		if (sym.t.isPtr() || sym.t.kind == kNullptr) && isConstExpr(sym.init.scalar) {
			sym.cval = sym.init.scalar
		} else if v, ok := p.foldInt(sym.init.scalar); ok {
			sym.val = v
			sym.cval = &intConst{node{sym.t, at}, v}
		} else if f, ok := p.foldFloat(sym.init.scalar); ok {
			sym.cval = &floatConst{node{sym.t, at}, f}
		} else if sym.t.isPtr() || sym.t.kind == kNullptr {
			sym.cval = sym.init.scalar
		} else {
			p.errorAt(at, "constexpr %q is not initialized with a constant", sym.name)
		}
		return
	}
	for _, it := range sym.init.items {
		if it.x == nil {
			continue
		}
		if _, ok := it.x.(*strLit); ok {
			continue
		}
		if isConstexprObj(it.x) {
			continue
		}
		if _, ok := p.foldInt(it.x); ok {
			continue
		}
		if _, ok := p.foldFloat(it.x); ok {
			continue
		}
		p.errorAt(at, "constexpr %q is not initialized with constants", sym.name)
	}
	if len(sym.init.items) == 1 && sym.init.items[0].off == 0 && sym.init.items[0].t.isScalar() && sym.t.isScalar() {
		sym.init = &initializer{scalar: sym.init.items[0].x}
		p.constexprValue(sym, at)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// isConstexprObj reports whether e is (a member of) a constexpr object.
func isConstexprObj(e expr) bool {
	switch e := e.(type) {
	case *varRef:
		return e.sym.constx
	case *member:
		return isConstexprObj(e.x)
	}
	return false
}
