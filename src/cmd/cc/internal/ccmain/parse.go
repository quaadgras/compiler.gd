// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// The parser: C23 and the GNU extensions cmd/cc supports, checked and
// typed as it goes (ast.go). Declarations are here; expressions are in
// expr.go, statements in stmt.go, initializers in init.go.

type scope struct {
	vars   map[string]*symbol
	tags   map[string]*ctype
	labels map[string]*label // GNU's local labels
	parent *scope

	// C++ (cxx.go).
	kind      scopeKind
	name      string    // a namespace's or class's
	anon      bool      // an unnamed namespace
	using     []*scope  // using directives, and inline and unnamed namespaces in it
	usedFuncs []*symbol // functions that using declarations add to sets declared here
	rec       *record   // skClass
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
	typeConstx  bool
	pendingVLA  []stmt // declarations of VLA lengths, before the next declaration

	fe              fenvPragmas // the floating-point pragmas in effect (fenv.go)
	translationTime int         // in an initializer computed as if at translation time

	// C++ (cxx.go).
	cxx           bool
	externC       int        // in extern "C"
	externSpec    bool       // in extern "C" declaration, without braces: as if extern
	noGT          int        // in template arguments: ">" ends them
	declIn        *scope     // the scope the last declarator's qualified name is in
	this          *symbol    // in a non-static member function
	cls           *ctype     // the class whose members are being declared
	temps         []*tempObj // the temporaries with destructors of the full expression being parsed
	extend        *[]*symbol // where temporaries a declared reference binds are kept, to live as long
	declTo        *scope     // the scope the next function is declared in, if not the current one
	userConv      int        // ranking a user-defined conversion's own argument
	pattern       int        // parsing a template's pattern: its parameters are placeholders
	packOn        bool       // parsing a pack expansion's pattern, for the element packI
	packI         int
	gtHalf        int                // the ">>" token whose first ">" has closed template brackets, or -1
	lambda        *lambdaInfo        // the lambda whose body is being parsed
	typeinfos     map[string]*symbol // the type_info objects typeid made
	pmfTypes      map[string]*ctype
	pmfConsts     map[*symbol]*symbol
	pmfNulls      map[*record]*symbol
	noPM          int       // parsing the right operand of .* or ->*
	castingMemPtr bool      // converting a pointer to member in a cast
	instDepth     int       // class template instances being defined, one in another
	explicitT     []targ    // the explicit template arguments of the call being resolved
	tmplSuffix    string    // the template arguments, mangled, of the function being instantiated
	instTag       *ctype    // the class template instance whose definition is being parsed
	pending       []*symbol // used, and not yet defined: template instances' functions
	draining      bool
	tids          map[tidKey]tidVal
	ctorDepth     int // constructAt calls in progress

	direct    bool          // the next call of a virtual function is not virtual: its name is qualified
	qualified map[expr]bool // the functions named with a qualified name
	condDepth int           // in an operand that is not always evaluated
	convType  *ctype        // the type of the conversion function whose name was last parsed
	exprType  int           // parsing a type specifier in an expression: T(x)
	xvalues   map[expr]bool // lvalues of the tree that are rvalues to C++: temporaries, what T && casts and calls give
}

type parseError struct{ msg string }

func (p *parser) errorAt(at pos, format string, args ...any) {
	if debugPanic {
		panic(fmt.Sprintf("%s:%d: %s", at.file, at.line, fmt.Sprintf(format, args...)))
	}
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
	p := &parser{file: file, toks: toks, fe: defaultPragmas, cxx: isCxxFile(file)}
	p.u = &unit{file: file, tags: map[string]*ctype{}}
	p.push()
	p.sc.kind = skNamespace
	p.gtHalf = -1
	p.u.scope = p.sc.vars
	p.u.tags = p.sc.tags
	for p.peek().kind != tkEOF {
		p.topLevel()
		if p.cxx {
			p.drain(false)
		}
	}
	if p.cxx {
		p.drain(true)
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
	if p.cxx {
		for s := p.sc; s != nil; s = s.parent {
			if v := s.find(name); v != nil {
				return v
			}
		}
		return nil
	}
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
	mode     int64 // GCC's mode: the size it gives an integer or floating type
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
	if b.mode != 0 {
		a.mode = b.mode
	}
}

// withMode is t of GCC's mode: an integer or floating type of that size.
func withMode(t *ctype, mode int64) *ctype {
	var m *ctype
	switch {
	case mode == 0 || t.enum != nil:
		return t
	case t.isInteger():
		u := t.isUnsigned()
		switch mode {
		case 1:
			m = tSChar
			if u {
				m = tUChar
			}
		case 2:
			m = tShort
			if u {
				m = tUShort
			}
		case 4:
			m = tInt
			if u {
				m = tUInt
			}
		case 8:
			m = tLong
			if u {
				m = tULong
			}
		case 16:
			m = &ctype{kind: kInt128}
			if u {
				m = &ctype{kind: kUInt128}
			}
		}
	case t.isFloat():
		switch mode {
		case 4:
			m = tFloat
		case 8:
			m = tDouble
		case 16:
			m = tLDouble
		}
	}
	if m == nil {
		return t
	}
	return m.qualified(t.q)
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
				if p.accept(",") {
					continue // C23: an attribute may be empty, [[a,,b]]
				}
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
		case p.cxx && p.is("alignas") && p.isAt(1, "(") && p.exprType == 0 && p.afterDeclarator():
			// C++: alignas after a declarator's name.
			p.pos += 2
			var n int64
			if p.isTypeStart(0) {
				n = p.typeName().alignof()
			} else {
				n = p.constInt(p.conditional())
			}
			p.expect(")")
			a.aligned = max(a.aligned, n)
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
	case "mode":
		p.expect("(")
		switch strings.Trim(p.ident(), "_") {
		case "QI", "byte":
			a.mode = 1
		case "HI":
			a.mode = 2
		case "SI", "SF":
			a.mode = 4
		case "DI", "DF", "word", "pointer":
			a.mode = 8
		case "TI", "TF", "XF":
			a.mode = 16
		}
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

	// C++.
	virtual  bool
	explicit bool
	friend   bool
	mutable  bool
	noType   bool // no type specifier: a constructor's, destructor's or conversion function's declaration
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
	"__extension__": true, "_BitInt": true, "__int128": true, "__intcap": true, "__int128__": true, "_Float32": true, "_Float64": true,
	"__thread": true, "__declspec": true, "__complex__": true, "__complex": true, "_Decimal32": true, "_Decimal64": true,
}

// isTypeStart reports whether the token at i starts declaration
// specifiers or a type name.
func (p *parser) isTypeStart(i int) bool {
	t := p.peekAt(i)
	if t.kind == tkPunct && t.text == "[" && p.isAt(i+1, "[") {
		return true
	}
	if p.cxx && t.kind == tkPunct && t.text == "::" {
		s, _ := p.cxxTypeAt(i)
		return s != nil
	}
	if t.kind != tkIdent {
		return false
	}
	if typeWords[t.text] {
		return true
	}
	if p.cxx {
		if cxxTypeWords[t.text] {
			return true
		}
		if s, _ := p.cxxTypeAt(i); s != nil {
			return true
		}
		tm, _ := p.ctadAt(i)
		return tm != nil
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
		if p.cxx && t.kind == tkPunct && t.text == "::" && base == "" && s.t == nil && !unsigned && !signed && !short && nlong == 0 && nint == 0 {
			if sym, n := p.cxxTypeAt(0); sym != nil {
				s.t = sym.t
				sym.used = true
				seen = true
				p.pos += n
				continue
			}
		}
		if t.kind != tkIdent {
			break
		}
		w := t.text
		if p.cxx {
			switch w {
			case "class":
				w = "struct"
			case "virtual":
				s.virtual = true
				seen = true
				p.next()
				continue
			case "explicit":
				s.explicit = true
				seen = true
				p.next()
				if p.is("(") {
					p.skipParens()
				}
				continue
			case "friend":
				s.friend = true
				seen = true
				p.next()
				continue
			case "mutable":
				s.mutable = true
				seen = true
				p.next()
				continue
			case "typename":
				p.next()
				if _, n := p.cxxTypeAt(0); (n == 0 || p.isAt(n, "::")) && p.pattern > 0 && s.t == nil {
					// In a pattern: a type that cannot be known.
					p.skipQualName()
					s.t = &ctype{kind: kDependent, dep: &depType{}}
					seen = true
				}
				continue
			case "constinit":
				p.next()
				continue
			case "consteval":
				w = "constexpr"
			case "wchar_t":
				p.next()
				s.t, seen = tWChar, true
				continue
			case "char8_t":
				p.next()
				s.t, seen = tUChar, true
				continue
			case "char16_t":
				p.next()
				s.t, seen = tChar16, true
				continue
			case "char32_t":
				p.next()
				s.t, seen = tChar32, true
				continue
			case "decltype":
				w = "typeof"
			case "__underlying_type", "__make_unsigned", "__make_signed":
				p.next()
				p.expect("(")
				et := p.typeName()
				p.expect(")")
				c := *et
				c.enum = nil
				switch {
				case w == "__underlying_type":
					c.q = 0
				case !c.isInteger():
				case w == "__make_unsigned":
					c = *c.unsignedOf().qualified(et.q)
				default:
					for _, st := range []*ctype{tSChar, tShort, tInt, tLong, tLLong} {
						if st.sizeof() == c.sizeof() && (st.kind != kLong || c.kind == kULong || c.kind == kLong) {
							c = *st.qualified(et.q)
							break
						}
					}
				}
				s.t, seen = &c, true
				continue
			}
		}
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
			s.constx = true // the object const (constexprType)
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
		case "__intcap":
			if base != "" || s.t != nil {
				p.errorf("two or more data types in declaration specifiers")
			}
			base = "__intcap"
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
			if p.cxx && p.pattern > 0 {
				// In a pattern: a type that may not be known.
				start, sc := p.pos, p.sc
				var dt *ctype
				if !p.try(func() {
					p.expect("(")
					e := p.expr()
					p.expect(")")
					if pr, ok := e.(*classPR); ok {
						dt = pr.t
					} else {
						dt = e.ty()
					}
				}) || dt == nil || hasTParam(dt) {
					p.pos, p.sc = start, sc
					p.skipParens()
					dt = &ctype{kind: kDependent, dep: &depType{}}
				}
				s.t = dt
				seen = true
				continue
			}
			p.expect("(")
			var ty *ctype
			if p.isTypeStart(0) {
				ty = p.typeName()
			} else {
				e := p.expr()
				ty = e.ty()
				if ty.isVM() && p.fn != nil || p.paramVLA != nil {
					// A variably modified operand is evaluated
					// (C23 6.7.3.6), where the declaration is.
					p.evalVLA(e)
				}
			}
			p.expect(")")
			if w == "typeof_unqual" || w == "__typeof_unqual__" {
				ty = unqualDeep(ty)
			}
			s.t = ty
			seen = true
			continue
		default:
			if p.cxx && base == "" && s.t == nil && !unsigned && !signed && !short && nlong == 0 && nint == 0 {
				if tm, n := p.ctadAt(0); tm != nil {
					// A class template's name: its arguments are deduced
					// from the initializer.
					p.pos += n
					s.t = &ctype{kind: kInt, isAuto: true, ctad: tm}
					seen = true
					continue
				}
				if sym, n := p.cxxTypeAt(0); sym != nil && !(s.auto && (p.isAt(n, "=") || p.isAt(n, ";") || p.isAt(n, ","))) && !p.isCtorName(sym, n) {
					s.t = sym.t
					sym.used = true
					seen = true
					p.pos += n
					continue
				}
				goto done
			}
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
	if s.t == nil && base == "__intcap" {
		s.t = tIntCap
		if unsigned {
			s.t = tUIntCap
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
		case p.cxx:
			// No implicit int: a constructor, destructor or conversion
			// function, or an error the caller reports.
			s.noType = true
			s.q = q
			return s
		default:
			if !seen {
				p.errorf("expected a type, found %s", p.peek())
			}
			// C89's implicit int.
			s.t = tInt
		}
	}
	if s.t != nil {
		// An array type's qualifiers are its elements' (C23 6.7.4).
		s.t = qualifyDeep(s.t, q)
	} else if q != 0 {
		s.auto = true
	}
	s.q = q
	return s
}

// recordSpec parses a struct or union specifier, after its keyword.
func (p *parser) recordSpec(union bool, kw token) *ctype {
	a := p.attributes()
	if p.cxx && p.is("alignas") && p.isAt(1, "(") {
		p.pos += 2
		if p.isTypeStart(0) {
			a.aligned = max(a.aligned, p.typeName().alignof())
		} else {
			a.aligned = max(a.aligned, p.constInt(p.conditional()))
		}
		p.expect(")")
		a.merge(p.attributes())
	}
	if p.cxx {
		// A class that is named, perhaps with a qualified name.
		if sym, n := p.cxxTypeAt(0); p.instTag == nil && sym != nil && sym.t.kind == kStruct && (n > 1 || !sym.tagName) && !p.isAt(n, "{") && !p.isAt(n, ":") && !p.isAt(n, ";") && !p.isAt(n, "final") {
			p.pos += n
			return sym.t
		}
	}
	if p.cxx && p.instTag == nil && p.peek().kind == tkIdent && p.isAt(1, "::") {
		// A member class defined outside its class: struct A::B {...}.
		if in, j := p.nested(0); in != nil && p.peekAt(j).kind == tkIdent && (p.isAt(j+1, "{") || p.isAt(j+1, ":") || p.isAt(j+1, "final")) {
			p.pos += j
			save := p.sc
			p.sc = in
			defer func() { p.sc = save }()
		}
	}
	tag := ""
	if p.peek().kind == tkIdent && !p.is("__attribute__") {
		tag = p.ident()
	}
	if p.cxx && p.is("<") && p.instTag != nil && p.instTag.rec.tag == tag {
		p.skipAngles() // a specialization's arguments
	}
	p.instTag = nil
	a.merge(p.attributes())
	if p.cxx && p.is("final") && (p.isAt(1, "{") || p.isAt(1, ":")) {
		p.next()
	}
	var bases []*ctype
	if p.cxx && p.is(":") && tag != "" {
		p.next()
		if _, ok := p.sc.tags[tag]; !ok {
			// Its name is declared in its bases: class D : Base<D>.
			t := &ctype{kind: kStruct, rec: &record{tag: tag, union: union, id: p.nextRecord(), sc: p.sc}}
			p.sc.tags[tag] = t
			p.declareTag(p.sc, tag, t)
		}
		bases = p.parseBases()
		if !p.is("{") {
			p.errorf("expected a class's body")
		}
	}
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
		t := &ctype{kind: kStruct, rec: &record{tag: tag, union: union, id: p.nextRecord(), sc: p.sc}}
		p.sc.tags[tag] = t
		p.declareTag(p.sc, tag, t)
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
		t = &ctype{kind: kStruct, rec: &record{tag: tag, union: union, id: p.nextRecord(), sc: p.sc}}
		if tag != "" {
			prev = p.sc.tags[tag]
			p.sc.tags[tag] = t // in scope in its own body
			p.declareTag(p.sc, tag, t)
		}
	}
	r := t.rec
	if kw.pack > 0 {
		r.packed = kw.pack
	}
	var fields []*field
	var saveSc *scope
	var saveCls *ctype
	if p.cxx {
		saveSc, saveCls = p.beginClass(t)
		for _, b := range bases {
			r.cls.bases = append(r.cls.bases, &baseClass{t: b})
		}
	}
	for !p.accept("}") {
		if p.is("_Static_assert") || p.is("static_assert") {
			p.staticAssert()
			continue
		}
		if p.accept(";") {
			continue
		}
		if p.cxx {
			p.classMember(t, &fields)
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
			var fa attrs // the member's, as GNU and C23 have it
			if p.is(":") {
				f.t = s.t
			} else {
				f.name, f.t, fa = p.declarator(s.t)
			}
			fa.merge(p.attributes())
			if p.accept(":") {
				f.bits = int(p.constInt(p.conditional()))
				if !f.t.isInteger() || f.t.isCap() {
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
			f.t = withMode(f.t, max(fa.mode, s.attrs.mode))
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
	if p.cxx {
		p.sc, p.cls = saveSc, saveCls
		fields = append(p.leadingFields(t, fields), fields...)
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
	if p.cxx && len(r.fields) == 0 && r.size == 0 {
		r.size = 1 // C++: every object has a size
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
	if p.cxx {
		p.completeClass(t)
	}
	return t
}

func (p *parser) nextRecord() int {
	p.recordN++
	return p.recordN
}

// enumSpec parses an enum specifier, after its keyword.
func (p *parser) enumSpec() *ctype {
	scoped := p.cxx && (p.accept("class") || p.accept("struct"))
	t := p.enumSpec1(scoped)
	if p.cxx && t.enum.sc == nil {
		t.enum.sc = p.sc
		if t.enum.names == nil {
			t.enum.names = &scope{vars: map[string]*symbol{}, tags: map[string]*ctype{}, kind: skEnum, name: t.enum.tag}
		}
		p.declareTag(p.sc, t.enum.tag, t)
	}
	return t
}

func (p *parser) enumSpec1(scoped bool) *ctype {
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
	if scoped && fixed == nil {
		fixed = tInt
	}
	var names *scope
	if p.cxx {
		names = &scope{vars: map[string]*symbol{}, tags: map[string]*ctype{}, kind: skEnum, name: tag}
	}
	if !p.is("{") {
		if tag == "" {
			p.errorf("expected an enum body")
		}
		if t, _ := p.lookupTag(tag); t != nil && (fixed == nil || p.sc.tags[tag] == t) {
			return t
		}
		// enum E : T; declares a new type in this scope (C23 6.7.2.3).
		// A forward reference: int, until the body says.
		t := &ctype{kind: kUInt, enum: &enumType{tag: tag}}
		if fixed != nil {
			t = &ctype{kind: fixed.kind, n: fixed.n, enum: &enumType{tag: tag, complete: true, fixed: true}}
		}
		p.sc.tags[tag] = t
		return t
	}
	p.next()
	var t *ctype
	if tag != "" {
		if old, ok := p.sc.tags[tag]; ok && old.enum != nil && old.enum.consts == nil {
			t = old // declared before, enum E : T; or not
		}
	}
	// A complete enum of the same tag in this scope: C23 lets it be
	// defined again, the same (6.7.2.3).
	var prev *ctype
	if old, ok := p.sc.tags[tag]; ok && tag != "" && t == nil && old.enum != nil {
		prev = old
	}
	if t == nil {
		t = &ctype{kind: kInt, enum: &enumType{tag: tag}}
	}
	if scoped {
		// Its earlier constants are in scope in its body.
		names.parent = p.sc
		p.sc = names
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
			} else if v > 0x7fffffff || v < -0x80000000 || nextUnsigned && v < 0 {
				// C23 6.7.2.2: the expression's type, if int can't
				// hold the value.
				ct = e.ty().unqual()
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
		if fixed == nil {
			ct = long64(ct)
		}
		sym := &symbol{name: name, kind: symEnumConst, t: ct, val: v, p: at}
		if names != nil {
			names.vars[name] = sym
		}
		if scoped {
			// Named only as E::name.
		} else if old, ok := p.sc.vars[name]; ok && prev != nil && old.kind == symEnumConst && old.val == v && slices.Contains(prev.enum.consts, old) {
			// The same constant of the same enum, again.
		} else {
			p.declare(sym, at)
		}
		consts = append(consts, sym)
		next = v + 1
		if !p.accept(",") {
			p.expect("}")
			break
		}
	}
	if scoped {
		p.sc = names.parent
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
	if fixed == nil {
		kind = long64(&ctype{kind: kind}).kind
	}
	t.kind = kind
	if fixed != nil {
		t.n = fixed.n // _BitInt(N)'s N
	}
	t.enum.complete = true
	t.enum.fixed = fixed != nil
	t.enum.consts = consts
	t.enum.scoped = scoped
	t.enum.names = names
	if prev != nil {
		if prev.kind != t.kind || !sameEnum(prev.enum, t.enum) {
			p.errorf("redefinition of enum %q", tag)
		}
		return prev
	}
	// C23: after the body, the constants have the enum's type if int
	// can't hold them all.
	fitInt := true
	for _, c := range consts {
		if c.val > 0x7fffffff || c.val < -0x80000000 || c.t.isUnsigned() && c.t.sizeof() == 8 && c.val < 0 {
			fitInt = false
		}
	}
	for _, c := range consts {
		if fixed != nil || !fitInt || p.cxx {
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
	for {
		if p.cxx {
			if cls, n := p.memPtrAhead(0); n > 0 {
				// C::*: a pointer to member.
				p.pos += n
				t = p.memPtrTo(cls, t)
				for p.is("const") || p.is("volatile") {
					if p.next().text == "const" {
						t = t.qualified(qConst)
					}
				}
				continue
			}
		}
		if !(p.is("*") || p.is("^") || p.cxx && (p.is("&") || p.is("&&"))) {
			break
		}
		if op := p.next().text; op[0] == '&' {
			t = refTo(t, uint8(len(op)))
			a.merge(p.attributes())
			continue
		}
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
	if p.cxx && p.is("...") && (p.packOn || p.pattern > 0) {
		p.next() // a parameter pack: its elements are declared one by one
	}
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
		save := p.sc
		if p.cxx {
			// (*C::m)(params): the parameters are in C's scope.
			after := p.pos
			p.pos = start
			if in := p.innerDeclScope(); in != nil {
				p.sc = in
			}
			p.pos = after
		}
		t = p.suffixes(t, &a)
		p.sc = save
		end := p.pos
		p.pos = start
		name, it, ia := p.declarator(t)
		p.expect(")")
		p.pos = end
		a.merge(ia)
		return name, it, a
	}
	name := ""
	if p.cxx {
		var in *scope
		name, in = p.declaratorID()
		a.merge(p.attributes())
		save := p.sc
		if in != nil && !within(p.sc, in) {
			// What follows the name is in its scope.
			p.sc = in
		}
		if !(name != "" && p.is("(") && p.directInitAhead()) {
			t = p.suffixes(t, &a)
		}
		p.sc = save
		a.merge(p.attributes())
		p.declIn = in
		return name, t, a
	}
	if p.peek().kind == tkIdent && !p.isTypeStartWord() {
		name = p.ident()
	}
	a.merge(p.attributes())
	t = p.suffixes(t, &a)
	a.merge(p.attributes())
	return name, t, a
}

// within reports whether the scope sc is in, or is nested in it.
func within(sc, in *scope) bool {
	for ; sc != nil; sc = sc.parent {
		if sc == in {
			return true
		}
	}
	return false
}

// innerDeclScope is the scope that the qualified name declared by the
// parenthesized declarator at the parser's position is in: nil if its
// name is not qualified.
func (p *parser) innerDeclScope() *scope {
	k := 0
	for {
		if _, n := p.memPtrAhead(k); n > 0 {
			k += n
			continue
		}
		if p.isAt(k, "*") || p.isAt(k, "&") || p.isAt(k, "&&") || p.isAt(k, "(") || p.isAt(k, "const") || p.isAt(k, "volatile") {
			k++
			continue
		}
		break
	}
	if !(p.peekAt(k).kind == tkIdent && p.isAt(k+1, "::")) && !p.isAt(k, "::") {
		return nil
	}
	in, j := p.nested(k)
	if in == nil || p.peekAt(j).kind != tkIdent {
		return nil
	}
	return in
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
		return t.text == "*" || t.text == "(" || t.text == "^" || t.text == "[" || p.cxx && (t.text == "&" || t.text == "&&")
	}
	if p.cxx && p.typePackAt(k) {
		return false // a pack of parameter types
	}
	if p.cxx {
		if _, n := p.memPtrAhead(k); n > 0 {
			return true // (C::*name)
		}
	}
	return !p.isTypeStart(k)
}

// isTypeStartWord reports whether the next identifier is a keyword that
// cannot be a declarator's name.
func (p *parser) isTypeStartWord() bool {
	t := p.peek()
	return t.kind == tkIdent && typeWords[t.text]
}

// suffixes parses array and function declarator suffixes. In C23 the
// attributes after each, [N] [[...]] and (params) [[...]], appertain to
// the array or function type; cmd/cc gives them, and GNU attributes
// there, to the declarator, adding them to a.
func (p *parser) suffixes(t *ctype, a *attrs) *ctype {
	switch {
	case p.is("["):
		p.next()
		// A parameter's qualifiers, of the pointer it becomes (C23 6.7.6.3).
		var q qual
		for p.is("static") || p.is("const") || p.is("restrict") || p.is("volatile") || p.is("__restrict") || p.is("_Atomic") {
			switch p.next().text {
			case "const":
				q |= qConst
			case "volatile":
				q |= qVolatile
			case "restrict", "__restrict":
				q |= qRestrict
			case "_Atomic":
				q |= qAtomic
			}
		}
		n := int64(-1)
		var vla expr
		if p.is("*") && p.isAt(1, "]") {
			p.next()
			// [*]: a VLA of unspecified size, in a prototype: its size
			// is never needed (sizeof *p in a later parameter's).
			p.expect("]")
			a.merge(p.attributes())
			inner := p.suffixes(t, a)
			at := arrayOf(inner, -1)
			at.q = q
			at.vla = &intConst{node{tULong, p.here()}, 0}
			return at
		}
		depN := 0
		if !p.is("]") {
			e := p.assignment()
			if d, ok := e.(*depConst); ok && p.pattern > 0 {
				// In a pattern: a length to deduce.
				n, depN = 0, d.param+1
			} else if v, ok := p.foldInt(e); ok {
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
		a.merge(p.attributes())
		inner := p.suffixes(t, a)
		if inner.kind == kFunc {
			p.errorf("declaration of an array of functions")
		}
		at := arrayOf(inner, n)
		at.q = q
		at.depN = depN
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
		if p.cxx {
			p.funcQuals(ft)
		}
		a.merge(p.attributes())
		ret := p.suffixes(t, a)
		if p.cxx && ret.isAuto && p.is("->") && p.pattern > 0 {
			// In a pattern: nothing is deduced from it.
			depth := 0
			for !(depth == 0 && (p.is("{") || p.is(";") || p.is("=") || p.is(",") || p.is(")"))) {
				if p.is("(") || p.is("[") {
					depth++
				} else if p.is(")") || p.is("]") {
					depth--
				}
				if p.next().kind == tkEOF {
					p.errorf("unterminated return type")
				}
			}
			ret = &ctype{kind: kDependent, dep: &depType{}}
		} else if p.cxx && ret.isAuto && p.accept("->") {
			// A trailing return type: the parameters are in scope.
			p.push()
			for i, name := range ft.names {
				if name == "" || i >= len(ft.params) {
					continue
				}
				for len(ft.psyms) <= i {
					ft.psyms = append(ft.psyms, nil)
				}
				if ft.psyms[i] == nil {
					ft.psyms[i] = &symbol{name: name, kind: symVar, t: ft.params[i]}
				}
				p.sc.vars[name] = ft.psyms[i]
			}
			for _, pk := range ft.packs {
				if pk.name != "" {
					p.sc.vars[pk.name] = &symbol{name: pk.name, src: pk.name, kind: symPack, isParamPack: true, packSyms: ft.psyms[pk.start : pk.start+pk.n], sc: p.sc}
				}
			}
			ret = p.typeName()
			p.pop()
		}
		if ret.kind == kFunc || ret.kind == kArray {
			p.errorf("function returning an array or function")
		}
		ft.ret = ret.unqual() // C23 6.7.6.3: the unqualified version
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
	sym.inExpr = true
	sym.init = &initializer{scalar: p.convert(e, tULong)}
	p.pendingVLA = append(p.pendingVLA, &declStmt{syms: []*symbol{sym}})
	return &varRef{node{tULong, e.at()}, sym}
}

// evalVLA evaluates e where the declaration being parsed is, with its
// variable length arrays' lengths.
func (p *parser) evalVLA(e expr) {
	if !e.ty().isVM() {
		return
	}
	if p.paramVLA != nil {
		p.symN++
		sym := &symbol{name: fmt.Sprintf("vla.%d", p.symN), kind: symVar, t: e.ty().unqual(), p: e.at()}
		sym.init = &initializer{scalar: p.rvalue(e)}
		*p.paramVLA = append(*p.paramVLA, sym)
		return
	}
	p.pendingVLA = append(p.pendingVLA, &exprStmt{p.rvalueVoid(e)})
}

// newLocal makes an unnamed local of the current function.
func (p *parser) newLocal(what string, t *ctype, at pos) *symbol {
	p.symN++
	sym := &symbol{name: fmt.Sprintf("%s.%d", what, p.symN), kind: symVar, t: t, p: at, owner: p.fn}
	p.fn.locals = append(p.fn.locals, sym)
	return sym
}

// params parses a function's parameter list, after its "(".
func (p *parser) params() *funcType {
	ft := &funcType{proto: true}
	if p.accept(")") {
		ft.proto = p.cxx // C++: no parameters
		return ft
	}
	if p.is("void") && p.isAt(1, ")") {
		p.pos += 2
		return ft
	}
	if p.peek().kind == tkIdent && !p.isTypeStart(0) && !p.cxx {
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
		if p.cxx && p.pattern == 0 {
			if n, _, end, ok := p.packAhead(false); ok {
				// A parameter pack: a parameter for each element.
				pk := paramPack{start: len(ft.params), n: n}
				i := 0
				p.expand(n, end, func() {
					s := p.declSpecs()
					if s.t == nil {
						p.errorf("expected a parameter type, found %s", p.peek())
					}
					name, t, _ := p.declarator(s.t)
					pk.name = name
					ft.params = append(ft.params, adjustParam(t))
					ft.names = append(ft.names, name+"#"+itoa(int64(i)))
					i++
				})
				if n == 0 {
					// Its name, with no parameter to give it.
					if t := p.toks[end-1]; t.kind == tkIdent && p.toks[end-2].text == "..." {
						pk.name = t.text
					}
				}
				ft.packs = append(ft.packs, pk)
				if !p.accept(",") {
					break
				}
				continue
			}
		}
		var name string
		var t *ctype
		one := func() {
			s := p.declSpecs()
			if s.t == nil {
				p.errorf("expected a parameter type, found %s", p.peek())
			}
			name, t, _ = p.declarator(s.t)
		}
		if p.pattern > 0 {
			// In a pattern: a parameter that cannot be parsed deduces
			// nothing.
			start, sc := p.pos, p.sc
			if !p.try(one) {
				p.pos, p.sc = start, sc
				p.skipDefaultArg()
				name, t = "", &ctype{kind: kDependent, dep: &depType{}}
			}
		} else {
			one()
		}
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
		if p.cxx && p.accept("=") {
			// A default argument: parsed where it is used.
			for len(ft.defaults) < len(ft.params) {
				ft.defaults = append(ft.defaults, defArg{})
			}
			ft.defaults[len(ft.params)-1] = defArg{p.pos, p.sc}
			p.skipDefaultArg()
		}
		if !p.accept(",") {
			break
		}
	}
	if ft.defaults != nil {
		for len(ft.defaults) < len(ft.params) {
			ft.defaults = append(ft.defaults, defArg{})
		}
	}
	ft.tags = p.sc.tags
	p.pop()
	p.expect(")")
	if len(ft.params) == 0 && !ft.variadic && len(ft.packs) == 0 {
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
	p.typeConstx = s.constx
	if s.constx {
		t = constexprType(t)
	}
	if s.align > 0 {
		c := *t
		c.align = s.align
		t = &c
	}
	return t
}

// Declarations.

func (p *parser) declare(s *symbol, at pos) {
	name := s.name
	if s.src != "" {
		name = s.src
	}
	if old, ok := p.sc.vars[name]; ok && !old.tagName {
		if old.kind == symTypedef && s.kind == symTypedef && compatible(old.t, s.t) {
			return
		}
		if (old.kind == symVar || old.kind == symFunc) && old.kind == s.kind && old.global && s.global {
			return
		}
		p.errorAt(at, "redefinition of %q", name)
	}
	p.sc.vars[name] = s
}

func (p *parser) staticAssert() {
	at := p.here()
	p.next()
	p.expect("(")
	e := p.conditional()
	if p.cxx && e.ty().kind == kStruct {
		e = p.boolOf(e, at)
	}
	v, ok := p.foldInt(e)
	if !ok && p.cxx {
		// A constant that cmd/cc does not evaluate (a constexpr
		// function's call): not checked.
		v = 1
	} else if !ok {
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
	if p.peek().kind == tkPragma {
		p.stdcPragma(p.next())
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
	if p.cxx {
		switch {
		case p.is("namespace"):
			p.namespaceDef(false)
			return
		case p.is("inline") && p.isAt(1, "namespace"):
			p.next()
			p.namespaceDef(true)
			return
		case p.is("using"):
			p.usingDecl()
			return
		case p.is("extern") && p.peekAt(1).kind == tkString:
			p.linkageSpec()
			return
		case p.is("template"):
			p.templateDecl()
			return
		case p.is("extern") && p.isAt(1, "template"):
			p.next()
			p.templateDecl()
			return
		}
	}
	if p.is("[") && p.isAt(1, "[") {
		// C23's attribute declaration, [[...]];, or a declaration's
		// attributes.
		save := p.pos
		p.attributes()
		if p.accept(";") {
			return
		}
		p.pos = save
	}
	at := p.here()
	s := p.declSpecs()
	if s.noType {
		p.specialMember(s, at)
		return
	}
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
	if s.t == nil && p.cxx {
		return &ctype{kind: kInt, isAuto: true, q: s.q}
	}
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
	if !p.cxx {
		return p.declarator2C(name, t, s, a, at)
	}
	if in := p.declIn; in != nil && t.kind != kFunc {
		// A static member's definition: in its class's scope.
		save := p.sc
		p.sc = in
		defer func() { p.sc = save }()
	}
	p.complete(baseElem(t))
	mark := len(p.temps)
	sym := p.declarator2C(name, t, s, a, at)
	if sym.kind == symVar {
		p.finishVar(sym, mark, at)
	}
	return sym
}

func (p *parser) declarator2C(name string, t *ctype, s specs, a attrs, at pos) *symbol {
	t = withMode(t, a.mode)
	if s.storage == sTypedef {
		if s.align > 0 || a.aligned > 0 {
			c := *t
			c.align = max(s.align, a.aligned)
			t = &c
		}
		if a.vector > 0 {
			p.errorAt(at, "vector types are not supported")
		}
		if p.cxx && t.kind == kStruct && t.rec.tag == "" && t.q == 0 {
			// C++: an unnamed class has, for linkage, the name of the
			// first typedef that names it.
			t.rec.tag = name
			if t.rec.cls != nil {
				t.rec.cls.scope.name = name
			}
		} else if p.cxx && t.enum != nil && t.enum.tag == "" && t.q == 0 {
			t.enum.tag = name
		} else if !p.cxx && t.kind == kStruct && t.rec.tag == "" && t.rec.tdName == "" && t.q == 0 {
			// C: still a type with no tag, but named by this in the Go.
			t.rec.tdName = name
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
	if s.constx {
		t = constexprType(t)
	}
	sym := &symbol{name: name, kind: symVar, t: t, storage: s.storage, global: global, tls: s.tls, p: at, constx: s.constx}
	if p.fn == nil {
		sym.linkage = 'e'
		if s.storage == sStatic {
			sym.linkage = 'i'
		}
	} else if s.storage == sExtern {
		sym.linkage = 'e'
	}
	if p.cxx {
		p.cxxVarName(sym, name, s, at)
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
			sym.owner = p.fn
			p.fn.locals = append(p.fn.locals, sym)
		}
	}
	inited := false
	if p.cxx {
		p.extending(sym, func() { inited = p.cxxInit(sym, s, at) })
	}
	if inited {
		// Initialized as C++ initializes.
	} else if p.accept("=") {
		if s.storage == sExtern && p.fn != nil {
			p.errorAt(at, "%q has both extern and an initializer", name)
		}
		if sym.defined && sym.init != nil {
			p.errorAt(at, "redefinition of %q", name)
		}
		if s.auto && s.t == nil {
			// C23 auto: the initializer's type, after lvalue conversion.
			if s.constx {
				p.translationTime++
			}
			e := p.rvalue(p.initExpr())
			if s.constx {
				p.translationTime--
			}
			sym.t = e.ty().unqual().qualified(s.q)
			if s.constx {
				sym.t = constexprType(sym.t)
			}
			sym.init = p.initFor(sym.t, e)
		} else {
			if sym.global {
				p.staticInit++
			}
			if sym.global || s.constx {
				p.translationTime++
			}
			if p.cxx {
				p.extending(sym, func() { sym.init = p.initializer(sym) })
			} else {
				sym.init = p.initializer(sym)
			}
			if sym.global || s.constx {
				p.translationTime--
			}
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
	if s.constx && p.cxx && (sym.init == nil && baseElem(sym.t).kind == kStruct || sym.init != nil && !p.constInit(sym.init)) {
		// A constexpr object that a constructor or a constexpr
		// function's call makes: not a constant that cmd/cc knows. It is
		// initialized when the program starts.
		sym.constx = false
	} else if s.constx {
		p.constexprValue(sym, at)
	} else if p.cxx && !sym.constx && sym.t.q&qConst != 0 && sym.t.isArith() && sym.init != nil && sym.init.scalar != nil && p.constInit(sym.init) {
		// C++: a const object of arithmetic type with a constant
		// initializer is a constant.
		sym.constx = true
		p.constexprValue(sym, at)
	}
	if !global && !sym.t.isComplete() && sym.t.vla == nil {
		p.errorAt(at, "storage size of %q isn't known", name)
	}
	return sym
}

func (p *parser) declareFunc(name string, t *ctype, s specs, at pos) *symbol {
	if p.cxx {
		return p.cxxDeclareFunc(name, t, s, at)
	}
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
	if in := p.declIn; p.cxx && in != nil {
		// Defined with a qualified name: in that scope.
		save := p.sc
		defer func() { p.sc = save }()
		if in.kind == skClass && t.fn.recv == nil {
			// A member function: static if it was declared so.
			key := name
			if name == in.rec.tag && s.noType {
				key = "{ctor}"
			}
			// Static if the overload with these parameters is, or if
			// none of the name's functions has a this.
			s.storage = sStatic
			exact := false
			for o := in.vars[key]; o != nil && !exact; o = o.next {
				if o.kind != symFunc || o.tmpl != nil {
					continue
				}
				ops := o.t.fn.params
				if o.t.fn.recv != nil {
					ops = ops[1:]
				}
				same := len(ops) == len(t.fn.params) && o.t.fn.variadic == t.fn.variadic
				for i := 0; same && i < len(ops); i++ {
					x, y := ops[i], t.fn.params[i]
					if x.ref == 0 && y.ref == 0 {
						x, y = x.unqual(), y.unqual()
					}
					same = sameType(x, y)
				}
				switch {
				case same:
					exact = true
					s.storage = sAuto
					if o.t.fn.recv == nil {
						s.storage = sStatic
					}
				case o.t.fn.recv != nil:
					s.storage = sAuto
				}
			}
			t = p.memberType(in.rec.cls.t, name, t, s)
		}
	}
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
	if p.cxx && (s.inline || s.constx || sym.comdat) && sym.src != "main" && p.fn == nil {
		// An inline function is defined if it is used: its body is
		// kept until then.
		sym.comdat = true
		sym.late = &lateFunc{sym, ft, name, p.pos, at}
		if p.declIn != nil {
			sym.lateSc = p.declIn
		}
		p.skipFuncBody()
		if sym.used {
			p.use(sym)
		}
		return
	}
	if p.cxx {
		st := p.saveFunc()
		if p.declIn != nil {
			p.sc = p.declIn
		}
		p.defineFunc(sym, ft, name, at, s.inline)
		p.restoreFunc(st)
		return
	}
	p.defineFunc(sym, ft, name, at, s.inline)
}

// defineFunc parses the body of the function sym, declared with the
// type ft (its parameters' names) as name.
func (p *parser) defineFunc(sym *symbol, ft *funcType, name string, at pos, inline bool) {
	sym.defined = true
	f := &funcDecl{sym: sym, labels: map[string]*label{}, inline: inline}
	sym.fn = f
	p.fn = f
	p.pushParams(ft)
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
		if p.cxx && nontrivialABI(pt) {
			ps.noCopy, ps.addr = true, true
		}
		ps.owner = f
		f.params = append(f.params, ps)
	}
	if ft.recv != nil {
		p.this = f.params[0]
	}
	for _, pk := range ft.packs {
		if pk.name != "" {
			p.sc.vars[pk.name] = &symbol{name: pk.name, src: pk.name, kind: symPack, isParamPack: true, packSyms: f.params[pk.start : pk.start+pk.n], sc: p.sc}
		}
	}
	// __func__.
	fs := &symbol{name: "__func__", kind: symVar, t: arrayOf(tChar.qualified(qConst), int64(len(name)+1)), global: true, storage: sStatic, p: at}
	fs.init = &initializer{items: []initItem{{t: fs.t, x: &strLit{node{arrayOf(tChar, int64(len(name)+1)), at}, name}}}}
	fs.defined = true
	p.sc.vars["__func__"] = fs
	p.sc.vars["__FUNCTION__"] = fs // an object of the unit once it is named (primary)
	f.locals = append(f.locals, ft.vlas...)
	if ft.ctor || ft.dtor {
		f.body = p.ctorBody(sym, at)
	} else {
		f.body = p.funcBody()
	}
	if p.cxx {
		p.nrvo(f)
		if sym.t.fn.ret.isAuto {
			sym.t.fn.ret = tVoid // no return statement gave it a type
		}
	}
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

// constInit reports whether in is all constants, as a constexpr
// object's must be (constexprValue says what is not).
func (p *parser) constInit(in *initializer) bool {
	if in == nil {
		return false
	}
	isConst := func(x expr) bool {
		if _, ok := p.foldInt(x); ok {
			return true
		}
		_, ok := p.foldFloat(x)
		return ok
	}
	if in.scalar != nil {
		return in.scalar.ty().isPtr() || in.scalar.ty().kind == kNullptr || isConst(in.scalar)
	}
	for _, it := range in.items {
		if _, ok := it.x.(*strLit); it.x == nil || ok || isConstexprObj(it.x) || isConst(it.x) {
			continue
		}
		return false
	}
	return true
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// constexprType is the type of a constexpr object declared t: const
// (C23 6.7.1), an array's elements.
func constexprType(t *ctype) *ctype { return qualifyDeep(t, qConst) }

// isConstexprObj reports whether e is (a member of) a constexpr object.
func isConstexprObj(e expr) bool {
	switch e := e.(type) {
	case *varRef:
		return e.sym.constx
	case *compoundLit:
		return e.sym.constx
	case *member:
		return isConstexprObj(e.x)
	}
	return false
}
