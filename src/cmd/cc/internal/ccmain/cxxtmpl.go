// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"os"
	"strings"
)

// C++ templates (doc/gd/cxx.md). The parser types as it parses, so a
// template's declaration means nothing to it until its arguments are
// known: a template is its tokens, and an instance is made by parsing
// them again in a scope where the parameters are the arguments. Classes
// are instantiated when they must be complete, function bodies when the
// function is used. What must be known without arguments, a function
// template's parameter types for deduction and a specialization's
// pattern, is parsed with the parameters bound to placeholder types.

type tparam struct {
	name  string
	kind  byte // 't' a type, 'v' a value, 'm' a template
	pack  bool
	vtype int // 'v': its type's first token
	vend  int // 'v': the token after its type
	dflt  int // its default argument's first token, or 0
}

// A targ is a template argument.
type targ struct {
	t      *ctype // a type
	isVal  bool   // a value: v, of type vt
	v      int64
	vt     *ctype
	tmpl   *template // a template
	isPack bool      // a pack's elements
	pack   []targ
	dep    bool // a value that depends on a template parameter (in a pattern)
	param  int  // dep: the parameter it is, or -1
}

type template struct {
	name    string
	kind    byte // 'c' class, 'f' function, 'a' alias, 'v' variable
	params  []tparam
	start   int // the first token of the declaration, after template<...>
	hasBody bool
	sc      *scope
	at      pos
	sym     *symbol

	// A class template.
	specs   []*template            // its specializations
	members map[string][]*template // its members defined outside it, by name
	insts   map[string]*tinst

	// A specialization.
	primary *template
	pattern int    // the "<" of its arguments
	patArgs []targ // them, with the parameters as placeholders

	// A function template.
	pat       *funcType // its type, with the parameters as placeholders
	patDone   bool
	nameAt    int         // the token of its name
	static    bool        // a static member function
	convPat   *ctype      // a conversion function template's type, with the parameters as placeholders
	ctorPats  []*funcType // a class template's constructors' types, as patterns
	ctorsDone bool
}

// A tinst is an instance of a template.
type tinst struct {
	tmpl *template
	args []targ
	t    *ctype  // a class's, or an alias's type
	sym  *symbol // a function's or variable's
	busy bool    // being defined
	done bool
	fail bool
}

// depType is what a kDependent type is, in a pattern: a template with
// arguments that depend on parameters, or (tmpl nil) a type that
// cannot be deduced from.
type depType struct {
	tmpl *template
	args []targ
}

// hasTParam reports whether t depends on a template parameter.
func hasTParam(t *ctype) bool {
	if t == nil {
		return false
	}
	switch t.kind {
	case kTParam, kDependent, kMemPtr:
		return true
	case kArray:
		return t.depN > 0 || hasTParam(t.elem)
	case kPtr, kComplex:
		return hasTParam(t.elem)
	case kFunc:
		if hasTParam(t.fn.ret) {
			return true
		}
		for _, pt := range t.fn.params {
			if hasTParam(pt) {
				return true
			}
		}
	}
	return false
}

func depArgs(args []targ) bool {
	for _, a := range args {
		if a.dep || a.t != nil && hasTParam(a.t) || a.isPack && depArgs(a.pack) {
			return true
		}
	}
	return false
}

// argsKey is the mangling of template arguments: an instance's key.
func argsKey(args []targ) string {
	var b strings.Builder
	for _, a := range args {
		switch {
		case a.isPack:
			b.WriteString("J" + argsKey(a.pack) + "E")
		case a.tmpl != nil:
			b.WriteString(sourceName(a.tmpl.name))
		case a.isVal:
			code := "i"
			if a.vt != nil && int(a.vt.kind) < len(builtinCodes) && builtinCodes[a.vt.kind] != "" && a.vt.enum == nil {
				code = builtinCodes[a.vt.kind]
			} else if a.vt != nil {
				code = rawType(a.vt.unqual())
			}
			v := itoa(a.v)
			if a.v < 0 {
				v = "n" + itoa(-a.v)
			}
			b.WriteString("L" + code + v + "E")
		default:
			b.WriteString(rawType(a.t))
		}
	}
	return b.String()
}

func sameArg(a, b targ) bool {
	switch {
	case a.isPack != b.isPack || a.isVal != b.isVal || (a.tmpl == nil) != (b.tmpl == nil):
		return false
	case a.isPack:
		if len(a.pack) != len(b.pack) {
			return false
		}
		for i := range a.pack {
			if !sameArg(a.pack[i], b.pack[i]) {
				return false
			}
		}
		return true
	case a.tmpl != nil:
		return a.tmpl == b.tmpl
	case a.isVal:
		return a.v == b.v
	}
	return sameType(a.t, b.t)
}

// skipAngles skips a balanced <...>, at its "<".
func (p *parser) skipAngles() {
	p.expect("<")
	depth, paren := 1, 0
	for depth > 0 {
		t := p.next()
		if t.kind == tkEOF {
			p.errorf("unbalanced template brackets")
		}
		if t.kind != tkPunct {
			continue
		}
		switch t.text {
		case "(", "[", "{":
			paren++
		case ")", "]", "}":
			paren--
		case "<":
			if paren == 0 {
				depth++
			}
		case ">":
			if paren == 0 {
				depth--
			}
		case ">>":
			if paren == 0 {
				depth -= 2
				if depth < 0 {
					// The second ">" is another's.
					p.pos--
					p.gtHalf = p.pos
					depth = 0
				}
			}
		}
	}
}

// skipTemplateArg skips a template argument or a parameter's default:
// to the "," or ">" that ends it.
func (p *parser) skipTemplateArg() {
	paren := 0
	for {
		t := p.peek()
		if t.kind == tkEOF {
			p.errorf("unterminated template argument")
		}
		if t.kind == tkPunct {
			switch t.text {
			case "(", "[", "{":
				paren++
			case ")", "]", "}":
				paren--
			case "<":
				if paren == 0 {
					p.skipAngles()
					continue
				}
			case ",", ">", ">>":
				if paren == 0 {
					return
				}
			}
		}
		p.next()
	}
}

// templateParams parses a template's parameters, at their "<".
func (p *parser) templateParams() []tparam {
	p.expect("<")
	var ps []tparam
	for !p.is(">") {
		var tp tparam
		switch {
		case (p.is("class") || p.is("typename")) && !(p.peekAt(1).kind == tkIdent && p.isAt(2, "::")):
			p.next()
			tp.kind = 't'
			tp.pack = p.accept("...")
			if p.peek().kind == tkIdent {
				tp.name = p.ident()
			}
		case p.is("template"):
			p.next()
			p.skipAngles()
			if !p.accept("class") {
				p.accept("typename")
			}
			tp.kind = 'm'
			tp.pack = p.accept("...")
			if p.peek().kind == tkIdent {
				tp.name = p.ident()
			}
		default:
			// A value: its type, then its name if it has one.
			tp.kind = 'v'
			tp.vtype = p.pos
			for !p.is(",") && !p.is(">") && !p.is(">>") && !p.is("=") {
				switch {
				case p.is("<"):
					p.skipAngles()
				case p.is("("):
					p.skipParens()
				case p.is("..."):
					tp.pack = true
					p.next()
				default:
					p.next()
				}
			}
			tp.vend = p.pos
			if last := p.peekAt(-1); last.kind == tkIdent && p.pos-tp.vtype > 1 && !typeWords[last.text] && p.peekAt(-2).text != "::" {
				tp.name = last.text
				tp.vend--
				if p.peekAt(-2).text == "..." {
					tp.vend--
				}
			}
		}
		if p.accept("=") {
			tp.dflt = p.pos
			p.skipTemplateArg()
		}
		ps = append(ps, tp)
		if !p.accept(",") {
			break
		}
	}
	p.expectGT()
	return ps
}

// skipDecl skips a declaration: to after its ";" or, for a function's
// definition, its body. It reports whether it had a body.
func (p *parser) skipDecl(class bool) bool {
	body := false
	seenEq := false
	paren := 0
	for {
		t := p.peek()
		if t.kind == tkEOF {
			p.errorf("unterminated declaration")
		}
		if t.kind == tkIdent && t.text == "operator" && paren == 0 {
			// An operator's name: operator=, operator(), operator[].
			p.next()
			if p.is("(") && p.isAt(1, ")") {
				p.pos += 2
			}
			for !p.is("(") && p.peek().kind != tkEOF {
				p.next()
			}
			continue
		}
		if t.kind == tkPunct {
			switch t.text {
			case "(", "[":
				paren++
			case ")", "]":
				paren--
			case "=":
				if paren == 0 {
					seenEq = true
				}
			case ":":
				if paren == 0 && !class && !seenEq && p.peekAt(-1).text == ")" || paren == 0 && !class && !seenEq && (p.peekAt(-1).text == "noexcept" || p.peekAt(-1).text == "const") {
					// A constructor's member initializers.
					p.skipFuncBody()
					return true
				}
			case "{":
				if paren == 0 {
					p.skipBraces()
					if !class && !seenEq {
						return true
					}
					body = true
					continue
				}
			case ";":
				if paren == 0 {
					p.next()
					return body
				}
			}
		}
		p.next()
	}
}

// declName finds the name a function's or variable's declaration at the
// parser's position declares, without parsing it: the name, its token's
// offset, the offset its qualifier starts at (-1 if it has none), and
// whether a parameter list follows it.
func (p *parser) declName() (name string, at, qual int, isFunc bool) {
	paren := 0
	last, lastAt := "", -1
	for j := 0; ; j++ {
		t := p.peekAt(j)
		if t.kind == tkEOF {
			p.errorf("expected a declaration after template<...>")
		}
		if t.kind == tkIdent {
			if t.text == "operator" && paren == 0 {
				// The operator's name: to its parameters' "(".
				k := j + 1
				if p.isAt(k, "(") && p.isAt(k+1, ")") {
					k += 2
				}
				for !p.isAt(k, "(") {
					if p.peekAt(k).kind == tkEOF {
						p.errorf("expected an operator's parameters")
					}
					k++
				}
				if t1 := p.peekAt(j + 1); t1.kind == tkIdent && t1.text != "new" && t1.text != "delete" {
					// A conversion function: its type may depend on
					// the template's parameters.
					return "operator ", j, p.qualStart(j), true
				}
				save := p.pos
				p.pos += j + 1
				name = p.operatorName()
				p.pos = save
				return name, j, p.qualStart(j), true
			}
			if paren == 0 {
				switch t.text {
				case "decltype", "noexcept", "alignas", "__attribute__", "requires", "explicit":
					if p.isAt(j+1, "(") {
						continue
					}
				}
				last, lastAt = t.text, j
				if j > 0 && p.isAt(j-1, "~") {
					last, lastAt = "~"+t.text, j-1
				}
			}
			continue
		}
		if t.kind != tkPunct {
			continue
		}
		switch t.text {
		case "<":
			if paren == 0 {
				// Template arguments: skipped.
				save := p.pos
				p.pos += j
				p.skipAngles()
				j = p.pos - save - 1
				p.pos = save
			}
		case "(":
			if paren == 0 && lastAt >= 0 && (lastAt == j-1 || p.isAt(j-1, ">")) {
				return last, lastAt, p.qualStart(lastAt), true
			}
			paren++
		case ")", "]":
			paren--
		case "[":
			paren++
		case "=", ";", "{":
			if paren == 0 {
				if lastAt < 0 {
					p.errorf("expected a name in a template's declaration")
				}
				return last, lastAt, p.qualStart(lastAt), false
			}
		}
	}
}

// qualStart is the offset at which the qualifier of the name at offset
// j starts (A<T>::B:: before it), or -1.
func (p *parser) qualStart(j int) int {
	start := -1
	for j >= 2 && p.isAt(j-1, "::") {
		k := j - 2
		if p.isAt(k, ">") || p.isAt(k, ">>") {
			// Back over the template arguments.
			depth := 0
			for ; k >= 0; k-- {
				if p.isAt(k, ">") {
					depth++
				} else if p.isAt(k, ">>") {
					depth += 2
				} else if p.isAt(k, "<") {
					depth--
					if depth == 0 {
						break
					}
				}
			}
			k--
		}
		if k < 0 || p.peekAt(k).kind != tkIdent {
			break
		}
		start, j = k, k
	}
	return start
}

// templateDecl parses a template's declaration, at "template": it is
// kept, not parsed.
func (p *parser) templateDecl() {
	at := p.here()
	p.next()
	if !p.is("<") {
		p.skipDecl(p.is("class") || p.is("struct") || p.is("union")) // an explicit instantiation
		return
	}
	params := p.templateParams()
	if p.is("template") {
		// A member template of a class template, defined outside
		// both: not supported. It is an error only if it is used.
		p.next()
		p.skipAngles()
		p.skipDecl(false)
		return
	}
	tm := &template{params: params, start: p.pos, sc: p.sc, at: at}
	j := 0
	friend := false
	for p.isAt(j, "inline") || p.isAt(j, "static") || p.isAt(j, "constexpr") || p.isAt(j, "friend") || p.isAt(j, "extern") {
		friend = friend || p.isAt(j, "friend")
		tm.static = tm.static || p.isAt(j, "static")
		j++
	}
	classKey := p.isAt(j, "class") || p.isAt(j, "struct") || p.isAt(j, "union")
	// Attributes after the class key.
	for classKey {
		switch {
		case p.isAt(j+1, "[") && p.isAt(j+2, "["):
			k := j + 3
			for !(p.isAt(k, "]") && p.isAt(k+1, "]")) && p.peekAt(k).kind != tkEOF {
				k++
			}
			j = k + 1
			continue
		case p.isAt(j+1, "__attribute__") || p.isAt(j+1, "alignas"):
			depth, k := 0, j+2
			for {
				if p.isAt(k, "(") {
					depth++
				} else if p.isAt(k, ")") {
					depth--
					if depth == 0 {
						break
					}
				} else if p.peekAt(k).kind == tkEOF {
					break
				}
				k++
			}
			j = k
			continue
		}
		break
	}
	if classKey && p.peekAt(j+1).kind == tkIdent {
		name := p.peekAt(j + 1).text
		k := j + 2
		spec := -1
		if p.isAt(k, "<") {
			spec = p.pos + k
			save := p.pos
			p.pos += k
			p.skipAngles()
			k = p.pos - save
			p.pos = save
		}
		if p.isAt(k, "{") || p.isAt(k, ":") || p.isAt(k, ";") || p.isAt(k, "final") {
			tm.name, tm.kind = name, 'c'
			tm.hasBody = p.skipDecl(true)
			if friend {
				return
			}
			old := p.sc.vars[name]
			if spec >= 0 {
				// A specialization.
				if old == nil {
					old = p.lookup(name)
				}
				if old == nil || old.kind != symTemplate || old.tmpl.kind != 'c' {
					p.errorAt(at, "%q is not a class template", name)
				}
				tm.primary, tm.pattern = old.tmpl, spec
				if tm.hasBody {
					old.tmpl.specs = append(old.tmpl.specs, tm)
				}
				return
			}
			if old != nil && old.kind == symTemplate && old.tmpl.kind == 'c' {
				// Declared before: the definition is the template, with
				// the defaults that either gives.
				ot := old.tmpl
				for i := range params {
					if i < len(ot.params) && params[i].dflt == 0 {
						params[i].dflt = ot.params[i].dflt
					}
				}
				if tm.hasBody || !ot.hasBody {
					ot.params, ot.start, ot.hasBody, ot.at = params, tm.start, tm.hasBody, at
				} else {
					for i := range ot.params {
						if i < len(params) && ot.params[i].dflt == 0 {
							ot.params[i].dflt = params[i].dflt
						}
					}
				}
				return
			}
			tm.insts = map[string]*tinst{}
			tm.members = map[string][]*template{}
			tm.sym = &symbol{name: name, src: name, kind: symTemplate, tmpl: tm, sc: p.sc, p: at}
			p.sc.vars[name] = tm.sym
			return
		}
	}
	if p.is("using") {
		tm.name, tm.kind = p.peekAt(1).text, 'a'
		p.skipDecl(false)
		tm.insts = map[string]*tinst{}
		tm.sym = &symbol{name: tm.name, src: tm.name, kind: symTemplate, tmpl: tm, sc: p.sc, p: at}
		p.sc.vars[tm.name] = tm.sym
		return
	}
	name, nameAt, qual, isFunc := p.declName()
	tm.name, tm.nameAt = name, p.pos+nameAt
	tm.insts = map[string]*tinst{}
	ctor := false
	if cs := p.sc; cs.kind == skClass && name == cs.rec.tag && isFunc {
		ctor = true
		for k := 0; k < nameAt; k++ {
			switch p.peekAt(k).text {
			case "explicit", "inline", "constexpr", "friend":
			default:
				ctor = false
			}
		}
	}
	if qual >= 0 {
		// A member defined outside its class.
		save := p.pos
		first := p.lookup(p.peekAt(qual).text)
		tm.hasBody = p.skipDecl(false)
		end := p.pos
		if first != nil && first.kind == symTemplate && first.tmpl.kind == 'c' {
			// Of a class template: instantiated with the class's member.
			key := name
			if strings.HasPrefix(name, "operator ") {
				key = "operator "
			}
			first.tmpl.members[key] = append(first.tmpl.members[key], tm)
			return
		}
		// A member template of a class: its definition is the template.
		p.pos = save + qual
		in, n := p.nested(0)
		p.pos = end
		_ = n
		if in != nil {
			key := name
			if in.kind == skClass && name == in.rec.tag {
				key = "{ctor}"
			}
			for o := in.vars[key]; o != nil; o = o.next {
				if o.tmpl != nil && len(o.tmpl.params) == len(params) && !o.tmpl.hasBody {
					for i := range params {
						if params[i].dflt == 0 {
							params[i].dflt = o.tmpl.params[i].dflt // the declaration's default
						}
					}
					o.tmpl.params, o.tmpl.start, o.tmpl.hasBody, o.tmpl.nameAt = params, tm.start, true, tm.nameAt
					o.tmpl.patDone, o.tmpl.pat = false, nil
					return
				}
			}
		}
		return
	}
	tm.hasBody = p.skipDecl(false)
	if !isFunc {
		tm.kind = 'v'
		tm.sym = &symbol{name: name, src: name, kind: symTemplate, tmpl: tm, sc: p.sc, p: at}
		p.sc.vars[name] = tm.sym
		return
	}
	tm.kind = 'f'
	sc := p.sc
	if friend {
		sc = p.nsScope()
	}
	key := name
	if ctor {
		key = "{ctor}"
	}
	sym := &symbol{name: name, src: name, kind: symFunc, t: funcOf(tVoid), tmpl: tm, sc: sc, p: at, global: true}
	tm.sym = sym
	head := sc.vars[key]
	if head != nil && head.kind != symFunc {
		if !head.tagName {
			p.errorAt(at, "%q redeclared as a function template", name)
		}
		head = nil
	}
	if head == nil {
		sc.vars[key] = sym
		return
	}
	last := head
	for o := head; o != nil; o = o.next {
		last = o
		if o.tmpl != nil && len(o.tmpl.params) == len(params) && !o.tmpl.hasBody && tm.hasBody && p.sameDeclTokens(o.tmpl, tm) {
			// Declared before: this is its definition.
			o.tmpl.params, o.tmpl.start, o.tmpl.hasBody, o.tmpl.nameAt = params, tm.start, true, tm.nameAt
			o.tmpl.patDone, o.tmpl.pat = false, nil
			return
		}
	}
	last.next = sym
}

// sameDeclTokens reports whether two function templates' declarations
// have parameter lists of the same number of tokens: the same function,
// declared and then defined.
func (p *parser) sameDeclTokens(a, b *template) bool {
	n := func(t *template) int {
		i := t.nameAt
		for !(p.toks[i].kind == tkPunct && p.toks[i].text == "(") {
			i++
		}
		depth, k := 0, i
		for {
			tk := p.toks[k]
			if tk.kind == tkPunct && tk.text == "(" {
				depth++
			} else if tk.kind == tkPunct && tk.text == ")" {
				depth--
				if depth == 0 {
					return k - i
				}
			} else if tk.kind == tkEOF {
				return -1
			}
			k++
		}
	}
	return n(a) == n(b)
}

// The parser's state where something else is parsed for a while.
type replayState struct {
	pos      int
	fs       funcState
	cls      *ctype
	declIn   *scope
	declTo   *scope
	noGT     int
	exprType int
	externC  int
	pattern  int
	explicit []targ
	tmplSufx string
	instTag  *ctype
	packOn   bool
	packI    int
}

// replay parses from the token start in the scope sc, outside any
// function, with f; then the parser is where it was.
func (p *parser) replay(start int, sc *scope, f func()) {
	saveGT := p.gtHalf
	defer func() { p.gtHalf = saveGT }()
	st := replayState{p.pos, p.saveFunc(), p.cls, p.declIn, p.declTo, p.noGT, p.exprType, p.externC, p.pattern, p.explicitT, p.tmplSuffix, p.instTag, p.packOn, p.packI}
	defer func() {
		p.pos = st.pos
		p.restoreFunc(st.fs)
		p.cls, p.declIn, p.declTo, p.noGT, p.exprType, p.externC, p.pattern = st.cls, st.declIn, st.declTo, st.noGT, st.exprType, st.externC, st.pattern
		p.explicitT, p.tmplSuffix, p.instTag = st.explicit, st.tmplSufx, st.instTag
		p.packOn, p.packI = st.packOn, st.packI
	}()
	p.pos, p.sc = start, sc
	p.packOn = false
	p.gtHalf = -1
	p.cls, p.declIn, p.declTo, p.noGT, p.exprType, p.externC, p.explicitT, p.tmplSuffix, p.instTag = nil, nil, nil, 0, 0, 0, nil, "", nil
	f()
}

// try runs f, and reports whether it parsed without an error: one is
// not reported (a substitution failure is not an error).
func (p *parser) try(f func()) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			pe, isParse := r.(parseError)
			if !isParse {
				panic(r)
			}
			if debugSFINAE {
				println("substitution failure:", pe.msg)
			}
			ok = false
		}
	}()
	f()
	return true
}

// bindParams makes the scope in which a template's parameters are the
// arguments args.
func (p *parser) bindParams(tm *template, args []targ) *scope {
	sc := &scope{vars: map[string]*symbol{}, tags: map[string]*ctype{}, parent: tm.sc, kind: skTemplate}
	for i, tp := range tm.params {
		if tp.name == "" || i >= len(args) {
			continue
		}
		p.bindParam(sc, tm, i, args[i])
	}
	return sc
}

func (p *parser) bindParam(sc *scope, tm *template, i int, a targ) {
	tp := tm.params[i]
	if tp.name == "" {
		return
	}
	switch {
	case a.isPack:
		sc.vars[tp.name] = &symbol{name: tp.name, src: tp.name, kind: symPack, pack: a.pack, sc: sc}
	case a.tmpl != nil:
		sc.vars[tp.name] = a.tmpl.sym
	case a.isVal:
		vt := a.vt
		if vt == nil {
			vt = tInt
		}
		sc.vars[tp.name] = &symbol{name: tp.name, src: tp.name, kind: symEnumConst, t: vt, val: a.v, sc: sc, depVal: a.dep, depParam: a.param}
	default:
		sc.vars[tp.name] = &symbol{name: tp.name, src: tp.name, kind: symTypedef, t: a.t, sc: sc}
	}
}

// placeholders are the arguments that stand for a template's own
// parameters, in a pattern.
func placeholders(tm *template) []targ {
	var args []targ
	for i, tp := range tm.params {
		var a targ
		switch tp.kind {
		case 't', 'm':
			a = targ{t: &ctype{kind: kTParam, n: int64(i)}}
		default:
			a = targ{isVal: true, vt: tInt, dep: true, param: i}
		}
		if tp.pack {
			a.t = &ctype{kind: kTParam, n: int64(i), isPackT: true}
		}
		args = append(args, a)
	}
	return args
}

// templateArgs parses template arguments, at their "<".
func (p *parser) templateArgs() []targ {
	p.expect("<")
	saveGT, saveET := p.noGT, p.exprType
	p.noGT, p.exprType = 1, 0
	var args []targ
	for !p.is(">") && !p.is(">>") {
		var a targ
		if n, dots, end, ok := p.packAhead(true); ok && dots == end-1 && p.pattern == 0 {
			// A pack expansion: an argument for each element.
			p.expand(n, end, func() {
				var a targ
				if p.typeArgAhead() {
					a.t = p.typeName()
				} else {
					e := p.rvalue(p.conditional())
					v, ok := p.foldInt(e)
					if !ok {
						p.errorAt(e.at(), "a template argument is not a constant")
					}
					a.isVal, a.v, a.vt = true, v, e.ty().unqual()
				}
				args = append(args, a)
			})
			if !p.accept(",") {
				break
			}
			continue
		}
		if p.typeArgAhead() {
			a.t = p.typeName()
		} else if s := p.lookupAt0(); s != nil && s.kind == symTemplate && s.tmpl.kind == 'c' && !p.isAt(1, "<") && !p.isAt(1, "::") {
			a.tmpl = s.tmpl // a template as an argument
			p.next()
		} else if s != nil && s.kind == symPack && p.isAt(1, "...") {
			// A pack, expanded.
			p.pos += 2
			args = append(args, s.pack...)
			if !p.accept(",") {
				break
			}
			continue
		} else {
			e := p.rvalue(p.conditional())
			a.isVal, a.vt = true, e.ty().unqual()
			if d, ok := p.depValue(e); ok {
				a.dep, a.param = true, d
			} else if v, ok := p.foldInt(e); ok {
				a.v = v
			} else if p.pattern > 0 {
				a.dep, a.param = true, -1
			} else {
				p.errorAt(e.at(), "a template argument is not a constant")
			}
		}
		if p.accept("...") {
			// A pack expansion of a type that names a pack.
			if a.t != nil && a.t.kind == kTParam {
				a.t = &ctype{kind: kTParam, n: a.t.n, isPackT: true}
			}
		}
		args = append(args, a)
		if !p.accept(",") {
			break
		}
	}
	p.noGT, p.exprType = saveGT, saveET
	p.expectGT()
	return args
}

// lookupAt0 is the symbol the identifier next names, if it is one.
func (p *parser) lookupAt0() *symbol {
	if p.peek().kind != tkIdent {
		return nil
	}
	return p.lookup(p.peek().text)
}

// depValue reports whether e is a value template parameter's
// placeholder, and which.
func (p *parser) depValue(e expr) (int, bool) {
	if d, ok := e.(*depConst); ok {
		return d.param, true
	}
	return 0, false
}

// A depConst is a value that depends on a template parameter, in a
// pattern.
type depConst struct {
	intConst
	param int
}

// fullArgs completes the arguments given for a template with its
// parameters' defaults, and gathers a pack's.
func (p *parser) fullArgs(tm *template, given []targ, at pos) []targ {
	var args []targ
	for i, tp := range tm.params {
		switch {
		case tp.pack:
			var pack []targ
			if i < len(given) {
				pack = append(pack, given[i:]...)
			}
			args = append(args, targ{isPack: true, pack: pack})
			return args
		case i < len(given):
			a := given[i]
			if tp.kind == 'v' && a.isVal && !a.dep {
				// Its value as the parameter's type.
				if vt := p.valueParamType(tm, i, args); vt != nil {
					a.vt = vt
					a.v = wrap(vt, a.v)
				}
			}
			args = append(args, a)
		case tp.dflt != 0:
			sc := p.bindParams(tm, args)
			var a targ
			p.replay(tp.dflt, sc, func() {
				p.noGT = 1
				if p.typeArgAhead() {
					a.t = p.typeName()
					return
				}
				e := p.rvalue(p.conditional())
				v, ok := p.foldInt(e)
				if !ok {
					p.errorAt(at, "the default argument of the template parameter %s is not a constant", tp.name)
				}
				a.isVal, a.v, a.vt = true, v, e.ty().unqual()
			})
			if tp.kind == 'v' && a.isVal {
				if vt := p.valueParamType(tm, i, args); vt != nil {
					a.vt = vt
					a.v = wrap(vt, a.v)
				}
			}
			args = append(args, a)
		default:
			p.errorAt(at, "too few arguments for the template %s", tm.name)
		}
	}
	if len(given) > len(tm.params) {
		p.errorAt(at, "too many arguments for the template %s", tm.name)
	}
	return args
}

// valueParamType is the type of the value parameter i of tm, given the
// arguments before it: nil if it cannot be parsed (it is deduced).
func (p *parser) valueParamType(tm *template, i int, before []targ) *ctype {
	tp := tm.params[i]
	if tp.vend <= tp.vtype || p.toks[tp.vtype].text == "auto" {
		return nil
	}
	var t *ctype
	sc := p.bindParams(tm, before)
	p.try(func() {
		p.replay(tp.vtype, sc, func() {
			s := p.declSpecs()
			if s.t == nil {
				return
			}
			_, vt, _ := p.declarator(s.t)
			if p.pos >= tp.vend && (vt.isInteger() || vt.isPtr()) {
				t = vt.unqual()
			}
		})
	})
	return t
}

// templateID resolves name<args> for the template tm, whose arguments
// start at the token offset j: the symbol it names and the offset after
// it. A class or an alias gives a type's name, a variable its symbol.
func (p *parser) templateID(tm *template, j int, at pos) (*symbol, int) {
	save := p.pos
	p.pos += j
	given := p.templateArgs()
	end := p.pos - save
	p.pos = save
	if tm.primary != nil {
		tm = tm.primary
	}
	if p.pattern > 0 && depArgs(given) {
		// In a pattern: not instantiated.
		t := &ctype{kind: kDependent, dep: &depType{tmpl: tm, args: given}}
		if tm.kind != 'c' {
			t.dep = &depType{}
		}
		if tm.kind == 'v' {
			return &symbol{name: tm.name, src: tm.name, kind: symEnumConst, t: tBool, depVal: true, depParam: -1}, end
		}
		return &symbol{name: tm.name, src: tm.name, kind: symTypedef, t: t}, end
	}
	in := p.instance(tm, p.fullArgs(tm, given, at))
	switch tm.kind {
	case 'c', 'a':
		if in.t == nil {
			p.errorAt(at, "the template %s cannot be instantiated here", tm.name)
		}
		return &symbol{name: tm.name, src: tm.name, kind: symTypedef, t: in.t, sc: tm.sc}, end
	case 'v':
		if in.sym == nil {
			p.errorAt(at, "the variable template %s cannot be instantiated here", tm.name)
		}
		return in.sym, end
	}
	p.errorAt(at, "%s is not a class, alias or variable template", tm.name)
	return nil, end
}

// complete defines the class t, if it is a template's instance that is
// not defined yet.
func (p *parser) complete(t *ctype) {
	if t == nil || t.kind != kStruct || t.rec.complete || t.rec.inst == nil {
		return
	}
	in := t.rec.inst
	if in.busy || in.done {
		return
	}
	in.busy = true
	tm, args := p.selectSpec(in.tmpl, in.args)
	if tm.hasBody {
		sc := p.bindParams(tm, args)
		sc.tags[tm.name] = in.t
		sc.vars[tm.name] = &symbol{name: tm.name, src: tm.name, kind: symTypedef, t: in.t, sc: sc, instOf: in.tmpl}
		p.replay(tm.start, sc, func() {
			for !p.is("class") && !p.is("struct") && !p.is("union") {
				p.next() // inline, static, ...
			}
			in.t.rec.union = p.is("union")
			p.instTag = in.t
			p.declSpecs()
		})
	}
	in.busy, in.done = false, true
}

// selectSpec chooses the specialization of the class template tm that
// the arguments args match best, or tm itself: with the arguments of
// its own parameters.
func (p *parser) selectSpec(tm *template, args []targ) (*template, []targ) {
	var best *template
	var bestArgs []targ
	for _, sp := range tm.specs {
		if sp.patArgs == nil {
			// The pattern, with the specialization's parameters as
			// placeholders.
			sc := p.bindParams(sp, placeholders(sp))
			ok := p.try(func() {
				p.replay(sp.pattern, sc, func() {
					p.pattern = 1
					sp.patArgs = p.templateArgs()
					if len(sp.patArgs) < len(tm.params) {
						// The primary's defaults complete it.
						sp.patArgs = flatArgs(p.fullArgs(tm, sp.patArgs, sp.at))
					}
				})
			})
			if !ok || sp.patArgs == nil {
				sp.patArgs = []targ{}
				continue
			}
		}
		// Match the pattern's arguments, the primary's packs spread.
		flat := flatArgs(args)
		pat := sp.patArgs
		bound := make([]targ, len(sp.params))
		set := make([]bool, len(sp.params))
		if !p.unifyArgs(pat, flat, bound, set) {
			continue
		}
		ok := true
		for i, s := range set {
			if !s {
				if sp.params[i].pack {
					bound[i] = targ{isPack: true}
				} else {
					ok = false
				}
			}
		}
		if !ok {
			continue
		}
		// The pattern with what was deduced must be the arguments: its
		// parts that deduce nothing are checked so, and a failure to
		// substitute is no match.
		var subst []targ
		ssc := p.bindParams(sp, bound)
		if !p.try(func() {
			p.replay(sp.pattern, ssc, func() {
				subst = p.templateArgs()
				if len(subst) < len(tm.params) {
					subst = p.fullArgs(tm, subst, sp.at)
				}
			})
		}) {
			continue
		}
		subst = flatArgs(subst)
		same := len(subst) == len(flat)
		for i := 0; same && i < len(subst); i++ {
			same = sameArg(subst[i], flat[i])
		}
		if !same {
			continue
		}
		if best == nil || len(sp.params) <= len(best.params) {
			best, bestArgs = sp, bound
		}
	}
	if best != nil {
		return best, bestArgs
	}
	return tm, args
}

// flatArgs is args with its packs' elements in their place.
func flatArgs(args []targ) []targ {
	var out []targ
	for _, a := range args {
		if a.isPack {
			out = append(out, flatArgs(a.pack)...)
		} else {
			out = append(out, a)
		}
	}
	return out
}

// unifyArgs matches the pattern arguments pat against the arguments
// args, deducing the parameters they name into bound.
func (p *parser) unifyArgs(pat, args []targ, bound []targ, set []bool) bool {
	for i, pa := range pat {
		if pa.t != nil && pa.t.kind == kTParam && pa.t.isPackT {
			// A pack: the rest.
			k := int(pa.t.n)
			if k < len(bound) {
				bound[k] = targ{isPack: true, pack: append([]targ(nil), args[min(i, len(args)):]...)}
				set[k] = true
			}
			return true
		}
		if i >= len(args) {
			return false
		}
		a := args[i]
		switch {
		case pa.isVal:
			if !a.isVal {
				return false
			}
			if pa.dep {
				if pa.param < 0 {
					continue
				}
				if set[pa.param] && bound[pa.param].v != a.v {
					return false
				}
				bound[pa.param], set[pa.param] = a, true
			} else if pa.v != a.v {
				return false
			}
		case pa.tmpl != nil:
			if a.tmpl != pa.tmpl {
				return false
			}
		default:
			if a.t == nil || !p.unify(pa.t, a.t, bound, set) {
				return false
			}
		}
	}
	return len(pat) == len(args)
}

// unify matches the pattern type pt against the type at, deducing the
// template parameters pt names into bound. A type that does not depend
// on a parameter matches only itself.
func (p *parser) unify(pt, at *ctype, bound []targ, set []bool) bool {
	switch pt.kind {
	case kTParam:
		k := int(pt.n)
		if k >= len(bound) {
			return false
		}
		d := at
		if pt.q != 0 {
			c := *at
			c.q &^= pt.q
			d = &c
		}
		if set[k] {
			return bound[k].t != nil && sameType(bound[k].t, d)
		}
		bound[k], set[k] = targ{t: d}, true
		return true
	case kDependent:
		if pt.dep == nil || pt.dep.tmpl == nil {
			return true // not deduced
		}
		if at.kind != kStruct || at.q&pt.q != pt.q {
			return false
		}
		in := at.rec.inst
		if in == nil || in.tmpl != pt.dep.tmpl {
			return false
		}
		return p.unifyArgs(pt.dep.args, flatArgs(in.args), bound, set)
	case kMemPtr:
		amp := memPtrOf(at)
		return amp != nil && p.unify(pt.mp.cls, amp.cls, bound, set) && p.unify(pt.elem, amp.t, bound, set)
	case kPtr:
		return at.kind == kPtr && at.ref == pt.ref && at.q&pt.q == pt.q && p.unify(pt.elem, at.elem, bound, set)
	case kArray:
		if at.kind != kArray {
			return false
		}
		if k := pt.depN - 1; k >= 0 && k < len(bound) {
			if set[k] && bound[k].v != at.n {
				return false
			}
			bound[k], set[k] = targ{isVal: true, v: at.n, vt: tInt}, true
		}
		return p.unify(pt.elem, at.elem, bound, set)
	case kFunc:
		if at.kind != kFunc || len(at.fn.params) != len(pt.fn.params) && !hasPackParam(pt.fn) || at.fn.thisQ != pt.fn.thisQ {
			return false
		}
		if !p.unify(pt.fn.ret, at.fn.ret, bound, set) {
			return false
		}
		for i, pp := range pt.fn.params {
			if pp.kind == kTParam && pp.isPackT {
				k := int(pp.n)
				var pack []targ
				for _, rest := range at.fn.params[min(i, len(at.fn.params)):] {
					pack = append(pack, targ{t: rest})
				}
				bound[k], set[k] = targ{isPack: true, pack: pack}, true
				return true
			}
			if i >= len(at.fn.params) || !p.unify(pp, at.fn.params[i], bound, set) {
				return false
			}
		}
		return true
	}
	if hasTParam(pt) {
		return false
	}
	return sameType(pt, at)
}

func hasPackParam(ft *funcType) bool {
	for _, pt := range ft.params {
		if pt.kind == kTParam && pt.isPackT {
			return true
		}
	}
	return false
}

// Function templates.

// pattern is the function type of the function template tm with its
// parameters as placeholders, for deduction: nil if it cannot be
// parsed so.
func (p *parser) funcPattern(tm *template) *funcType {
	if tm.patDone {
		return tm.pat
	}
	tm.patDone = true
	sc := p.bindParams(tm, placeholders(tm))
	p.try(func() {
		p.replay(tm.start, sc, func() {
			p.pattern = 1
			p.cls = p.enclosingClass(tm.sc)
			// The return type, if it can be parsed: nothing is deduced
			// from it.
			var base *ctype
			start := p.pos
			if !p.try(func() {
				s := p.declSpecs()
				base = tVoid
				if !s.noType {
					base = p.baseOr(s)
				}
			}) || p.pos > tm.nameAt {
				p.pos, p.sc = start, sc
				for p.pos < tm.nameAt && !p.is("*") && !p.is("&") && !p.is("&&") {
					p.next()
				}
				base = &ctype{kind: kDependent, dep: &depType{}}
			}
			name, t, _ := p.declarator(base)
			if t.kind == kFunc {
				tm.pat = t.fn
				if strings.HasPrefix(name, "operator ") {
					tm.convPat = p.convType
				}
			}
		})
	})
	return tm.pat
}

// enclosingClass is the class whose scope sc is, or is a template's
// scope in.
func (p *parser) enclosingClass(sc *scope) *ctype {
	for sc != nil && sc.kind == skTemplate {
		sc = sc.parent
	}
	if sc != nil && sc.kind == skClass && sc.rec.cls != nil {
		return sc.rec.cls.t
	}
	return nil
}

// deduce deduces the arguments of the function template tm for a call
// with args, after the explicit arguments given.
func (p *parser) deduce(tm *template, given []targ, args []expr, at pos) ([]targ, bool) {
	n := len(tm.params)
	bound := make([]targ, n)
	set := make([]bool, n)
	for i, a := range given {
		if i >= n {
			return nil, false
		}
		if tm.params[i].pack {
			bound[i], set[i] = targ{isPack: true, pack: append([]targ(nil), given[i:]...)}, true
			break
		}
		bound[i], set[i] = a, true
	}
	if pat := p.funcPattern(tm); pat != nil {
		for i, pt := range pat.params {
			if pt.kind == kTParam && pt.isPackT || pt.isRef() && pt.elem.kind == kTParam && pt.elem.isPackT {
				// A function parameter pack: each remaining argument.
				inner := pt
				if pt.isRef() {
					inner = pt.elem
				}
				k := int(inner.n)
				var pack []targ
				for _, a := range args[min(i, len(args)):] {
					one := []targ{{}}
					ok := []bool{false}
					single := &ctype{kind: kTParam, n: 0, q: inner.q}
					var spt *ctype = single
					if pt.isRef() {
						spt = &ctype{kind: kPtr, ref: pt.ref, elem: single}
					}
					if !p.deduceArg(spt, a, one, ok) || !ok[0] {
						return nil, false
					}
					pack = append(pack, one[0])
				}
				if set[k] && bound[k].isPack {
					pack = append(bound[k].pack[:len(bound[k].pack):len(bound[k].pack)], pack[min(len(bound[k].pack), len(pack)):]...)
				}
				bound[k], set[k] = targ{isPack: true, pack: pack}, true
				break
			}
			if i >= len(args) {
				break
			}
			if !p.deduceArg(pt, args[i], bound, set) {
				return nil, false
			}
		}
	}
	// What is not deduced: a default, or an empty pack.
	for i, tp := range tm.params {
		if set[i] {
			continue
		}
		switch {
		case tp.pack:
			bound[i] = targ{isPack: true}
		case tp.dflt != 0:
			sc := p.bindParams(tm, bound[:i])
			var a targ
			ok := p.try(func() {
				p.replay(tp.dflt, sc, func() {
					p.noGT = 1
					if p.typeArgAhead() {
						a.t = p.typeName()
						return
					}
					e := p.rvalue(p.conditional())
					v, ok := p.foldInt(e)
					if !ok {
						p.errorAt(at, "not a constant")
					}
					a.isVal, a.v, a.vt = true, v, e.ty().unqual()
				})
			})
			if !ok {
				return nil, false // a substitution failure
			}
			bound[i] = a
		default:
			return nil, false
		}
		set[i] = true
	}
	for i, tp := range tm.params {
		if tp.kind != 'v' || tp.vend <= tp.vtype {
			continue
		}
		// The parameter's type, with the arguments: if it cannot be
		// made, a substitution failure.
		sc := p.bindParams(tm, bound[:i])
		if !p.try(func() {
			p.replay(tp.vtype, sc, func() {
				if s := p.declSpecs(); s.t != nil {
					p.declarator(s.t)
				}
			})
		}) {
			return nil, false
		}
	}
	return bound, true
}

// deduceArg deduces template parameters from the function parameter
// pattern pt and the argument a.
func (p *parser) deduceArg(pt *ctype, a expr, bound []targ, set []bool) bool {
	if !hasTParam(pt) {
		return true
	}
	if _, ok := a.(*initList); ok {
		return true // nothing is deduced from a braced list
	}
	at := a.ty()
	if pt.isRef() {
		inner := pt.elem
		if pt.ref == 2 && inner.kind == kTParam && inner.q == 0 && p.isLvalueCxx(a) && at.kind != kFunc {
			// A forwarding reference bound to an lvalue: T is A &.
			k := int(inner.n)
			d := refTo(at, 1)
			if set[k] {
				return sameType(bound[k].t, d)
			}
			bound[k], set[k] = targ{t: d}, true
			return true
		}
		if at.kind == kArray && inner.kind != kArray && inner.kind != kTParam {
			at = ptrTo(at.elem)
		}
		if inner.q != 0 && inner.kind != kTParam {
			// const X<T> &: the argument's own qualifiers do not matter.
			u := *inner
			u.q = 0
			return p.unifyBase(&u, at.unqual(), bound, set)
		}
		if inner.kind == kTParam {
			return p.unify(inner, withQual(at, at.q|inner.q), bound, set)
		}
		return p.unifyBase(inner, at, bound, set)
	}
	vt := valueType(a)
	if pt.kind == kTParam {
		c := *pt
		c.q = 0
		return p.unify(&c, vt, bound, set)
	}
	if vt.kind == kNullptr && pt.kind == kPtr {
		return true
	}
	return p.unifyBase(pt.unqual(), vt, bound, set)
}

func withQual(t *ctype, q qual) *ctype {
	if t.q == q {
		return t
	}
	c := *t
	c.q = q
	return &c
}

// unifyBase is unify, where an argument of a class derived from an
// instance of the pattern's template matches too.
func (p *parser) unifyBase(pt, at *ctype, bound []targ, set []bool) bool {
	save, saveSet := append([]targ(nil), bound...), append([]bool(nil), set...)
	if p.unify(pt, at, bound, set) {
		return true
	}
	copy(bound, save)
	copy(set, saveSet)
	// X<T> from a class derived from an X<...>, or a pointer to one.
	pp, aa := pt, at
	if pp.kind == kPtr && aa.kind == kPtr && pp.ref == 0 && aa.ref == 0 {
		pp, aa = pp.elem, aa.elem
	}
	if pp.kind == kDependent && pp.dep != nil && pp.dep.tmpl != nil && aa.kind == kStruct {
		p.complete(aa)
		if ci := aa.rec.cls; ci != nil {
			for _, b := range ci.bases {
				if p.unifyBase(pp, b.t, bound, set) {
					return true
				}
			}
		}
	}
	return false
}

// instFunc is the instance of the function template tm with the
// arguments args: nil if its declaration cannot be made with them (a
// substitution failure).
func (p *parser) instFunc(tm *template, args []targ, at pos) *symbol {
	key := argsKey(args)
	if in := tm.insts[key]; in != nil {
		if in.fail || in.busy {
			return nil
		}
		return in.sym
	}
	in := &tinst{tmpl: tm, args: args, busy: true}
	tm.insts[key] = in
	sc := p.bindParams(tm, args)
	ok := p.try(func() {
		p.replay(tm.start, sc, func() {
			ct := p.enclosingClass(tm.sc)
			p.cls = ct
			p.tmplSuffix = "I" + key + "E"
			dat := p.here()
			s := p.declSpecs()
			base := tVoid
			if !s.noType {
				if s.t == nil && !s.auto {
					p.errorAt(dat, "expected a function template's declaration")
				}
				base = p.baseOr(s)
			}
			name, t, _ := p.declarator(base)
			if t.kind != kFunc {
				p.errorAt(dat, "expected a function template's declaration")
			}
			if tm.static {
				s.storage = sStatic
			}
			if ct != nil && !s.friend {
				t = p.memberType(ct, name, t, s)
			}
			p.declIn = nil
			sym := p.cxxDeclareFunc(name, t, s, dat)
			sym.comdat = true
			sym.explicit = s.explicit
			sym.inst = in
			if p.accept("=") {
				if p.accept("delete") {
					sym.deleted = true
				}
			} else if (p.is("{") || p.is(":")) && !sym.defined && sym.late == nil {
				sym.late = &lateFunc{sym, t.fn, name, p.pos, dat}
			}
			in.sym = sym
		})
	})
	in.busy = false
	if !ok || in.sym == nil {
		in.fail = true
		return nil
	}
	return in.sym
}

// instantiateFor is the instance of the function template sym for a
// call with args: nil if there is none.
func (p *parser) instantiateFor(sym *symbol, args []expr, at pos) *symbol {
	targs, ok := p.deduce(sym.tmpl, p.explicitT, args, at)
	if !ok {
		return nil
	}
	return p.instFunc(sym.tmpl, targs, at)
}

// use notes that the function or variable sym is used: if it is a
// template's or a class template's member, it is defined, later.
func (p *parser) use(sym *symbol) {
	if sym == nil || sym.defined || sym.queued {
		return
	}
	if sym.late == nil && sym.synth == nil && !p.instMember(sym) && !(sym.kind == symFunc && sym.inst != nil) {
		return
	}
	sym.queued = true
	p.pending = append(p.pending, sym)
}

// instMember reports whether sym is a member of a class template's
// instance, declared in the class and perhaps defined outside it.
func (p *parser) instMember(sym *symbol) bool {
	sc := sym.sc
	for sc != nil && sc.kind == skTemplate {
		sc = sc.parent
	}
	return sc != nil && sc.kind == skClass && sc.rec.inst != nil
}

// drain defines the functions and variables that are used and not yet
// defined. At the end of the unit, final, what has no definition stays
// undefined; before, it waits for one.
func (p *parser) drain(final bool) {
	if p.draining || p.fn != nil || p.cls != nil {
		return
	}
	p.draining = true
	defer func() { p.draining = false }()
	var wait []*symbol
	for len(p.pending) > 0 {
		sym := p.pending[0]
		p.pending = p.pending[1:]
		if sym.defined {
			continue
		}
		switch {
		case sym.late != nil:
			lf := sym.late
			sym.late = nil
			p.replay(lf.start, sym.sc, func() {
				if sym.sc.kind != skClass && sym.sc.kind != skTemplate {
					p.sc = sym.sc
				}
				if sym.lateSc != nil {
					p.sc = sym.lateSc
				}
				p.defineFunc(sym, lf.ft, lf.name, lf.at, true)
			})
		case sym.synth != nil:
			f := sym.synth
			sym.synth = nil
			f()
		case sym.kind == symFunc && sym.inst != nil && sym.inst.tmpl.kind == 'f':
			// A function template's instance that was made of the
			// template's declaration: its body is the definition's,
			// once that is seen.
			tm := sym.inst.tmpl
			if !tm.hasBody {
				sym.queued = false
				if !final {
					wait = append(wait, sym)
				}
				continue
			}
			sc := p.bindParams(tm, sym.inst.args)
			p.try(func() {
				p.replay(tm.start, sc, func() {
					p.cls = p.enclosingClass(tm.sc)
					s := p.declSpecs()
					base := tVoid
					if !s.noType {
						base = p.baseOr(s)
					}
					name, t, _ := p.declarator(base)
					if t.kind != kFunc || !p.is("{") && !p.is(":") {
						return
					}
					ft := *sym.t.fn
					ft.names, ft.psyms = t.fn.names, t.fn.psyms
					if sym.t.fn.recv != nil {
						ft.names = append([]string{"this"}, t.fn.names...)
						ft.psyms = nil
					}
					p.sc = sc
					p.defineFunc(sym, &ft, name, pos{p.peek().file, p.peek().line}, true)
				})
			})
		default:
			// A class template's member defined outside the class: the
			// definitions of that name, with the instance's arguments.
			sc := sym.sc
			for sc.kind == skTemplate {
				sc = sc.parent
			}
			in := sc.rec.inst
			key := sym.src
			if sym.kind == symFunc && sym.t.fn.conv != nil {
				key = "operator "
			}
			defs := in.tmpl.members[key]
			for _, def := range defs {
				dsc := p.bindParams(def, flatToParams(def, in.args))
				p.try(func() {
					p.replay(def.start, dsc, func() { p.topLevel() })
				})
			}
			if !sym.defined {
				sym.queued = false
				if !final {
					wait = append(wait, sym)
				}
			}
		}
	}
	for _, sym := range wait {
		sym.queued = true
	}
	p.pending = wait
}

// flatToParams are the arguments args of a class template's instance as
// the arguments of the parameters of def, a member's definition outside
// the class, which names them anew.
func flatToParams(def *template, args []targ) []targ {
	if len(def.params) == len(args) {
		return args
	}
	flat := flatArgs(args)
	var out []targ
	for i, tp := range def.params {
		if tp.pack {
			out = append(out, targ{isPack: true, pack: append([]targ(nil), flat[min(i, len(flat)):]...)})
			break
		}
		if i < len(flat) {
			out = append(out, flat[i])
		}
	}
	return out
}

// completeFor defines the class that t is, or points or refers to, if
// it is a template's instance: a conversion looks at its bases and
// members.
func (p *parser) completeFor(t *ctype) {
	for t.kind == kPtr || t.kind == kArray {
		t = t.elem
	}
	p.complete(t)
}

// typeArgAhead reports whether a type, not an expression, is next: where
// either may be (a template argument). T(x) and T{x} are expressions.
func (p *parser) typeArgAhead() bool {
	if !p.isTypeStart(0) {
		return false
	}
	n := p.typeSpecEnd(0)
	switch {
	case p.isAt(n, "{"):
		return false
	case p.isAt(n, "::") && p.pattern > 0 && !p.is("typename"):
		return false // a member of a type that depends on a parameter: a value
	case p.isAt(n, "("):
		if _, m := p.memPtrAhead(n + 1); m > 0 {
			return true
		}
		return p.isAt(n+1, ")") || p.isAt(n+1, "*") || p.isAt(n+1, "&") || p.isAt(n+1, "&&") || p.isAt(n+1, "...") || p.isTypeStart(n+1) || p.typePackAt(n+1)
	}
	return true
}

// typePackAt reports whether the token at offset i names a pack of
// types.
func (p *parser) typePackAt(i int) bool {
	t := p.peekAt(i)
	if t.kind != tkIdent {
		return false
	}
	s := p.lookup(t.text)
	return s != nil && s.kind == symPack && !s.isParamPack
}

// debugSFINAE prints the errors that are not reported because they are
// substitution failures: CCDEBUG=sfinae.
var debugSFINAE = os.Getenv("CCDEBUG") == "sfinae"

// debugPanic makes a parse error a panic, with its stack: CCDEBUG=panic.
var debugPanic = os.Getenv("CCDEBUG") == "panic"

// instance is the instance of the class, alias or variable template tm
// with the arguments args, which are complete: made if it is new.
func (p *parser) instance(tm *template, args []targ) *tinst {
	key := argsKey(args)
	in := tm.insts[key]
	if in != nil {
		return in
	}
	in = &tinst{tmpl: tm, args: args}
	tm.insts[key] = in
	switch tm.kind {
	case 'c':
		in.t = &ctype{kind: kStruct, rec: &record{tag: tm.name, id: p.nextRecord(), sc: tm.sc, inst: in}}
		in.t.rec.mtag = sourceName(tm.name) + "I" + key + "E"
	case 'a':
		sc := p.bindParams(tm, args)
		p.replay(tm.start, sc, func() { p.usingDecl() })
		if s := sc.vars[tm.name]; s != nil {
			in.t = s.t
		}
	case 'v':
		sc := p.bindParams(tm, args)
		p.replay(tm.start, sc, func() { p.topLevel() })
		in.sym = sc.vars[tm.name]
		if in.sym != nil && in.sym.kind == symVar {
			in.sym.name = mangleVar(tm.sc, tm.name+"I"+key+"E")
			if len(scopeNames(tm.sc)) == 0 {
				in.sym.name = "_Z" + sourceName(tm.name) + "I" + key + "E"
			}
			in.sym.comdat = true
		}
	}
	return in
}

// ctad deduces the arguments of the class template tm from the
// arguments args of a constructor's call, T(args) without template
// arguments: the instance's type.
func (p *parser) ctad(tm *template, args []expr, at pos) *ctype {
	if !tm.ctorsDone {
		tm.ctorsDone = true
		// The constructors, found in the class's tokens: each
		// parameter list is a pattern.
		i := tm.start
		for !(p.toks[i].kind == tkPunct && (p.toks[i].text == "{" || p.toks[i].text == ";")) {
			i++
		}
		if p.toks[i].text == "{" {
			sc := p.bindParams(tm, placeholders(tm))
			self := &ctype{kind: kDependent, dep: &depType{tmpl: tm, args: placeholders(tm)}}
			sc.vars[tm.name] = &symbol{name: tm.name, src: tm.name, kind: symTypedef, t: self, sc: sc}
			depth := 0
			for i++; depth >= 0 && p.toks[i].kind != tkEOF; i++ {
				t := p.toks[i]
				if t.kind == tkPunct {
					switch t.text {
					case "{", "(", "[":
						depth++
					case "}", ")", "]":
						depth--
					}
					continue
				}
				if depth != 0 || t.kind != tkIdent || t.text != tm.name || p.toks[i+1].text != "(" || p.toks[i-1].text == "~" || p.toks[i-1].text == "::" {
					continue
				}
				start := i
				p.try(func() {
					p.replay(start, sc, func() {
						p.pattern = 1
						p.next()
						t := p.suffixes(tVoid, &attrs{})
						if t.kind == kFunc {
							tm.ctorPats = append(tm.ctorPats, t.fn)
						}
					})
				})
			}
		}
	}
	for _, pat := range tm.ctorPats {
		if len(args) > len(pat.params) {
			continue
		}
		bound, set := make([]targ, len(tm.params)), make([]bool, len(tm.params))
		ok := true
		for i, a := range args {
			if !p.deduceArg(pat.params[i], a, bound, set) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		n := countSet(set)
		full := true
		for i := n; i < len(tm.params); i++ {
			if tm.params[i].dflt == 0 && !tm.params[i].pack {
				full = false
			}
		}
		if !full {
			continue
		}
		var t *ctype
		if !p.try(func() { t = p.instance(tm, p.fullArgs(tm, bound[:n], at)).t }) || t == nil {
			continue
		}
		p.complete(t)
		if t.rec.complete && p.resolve(ctors(t), nil, args, at, "", true) != nil {
			return t
		}
	}
	p.errorAt(at, "the arguments of the class template %s cannot be deduced", tm.name)
	return nil
}

// ctadAt is the class template that the identifier at token offset i
// names as a declaration's type, without arguments: T x(args).
func (p *parser) ctadAt(i int) *template {
	t := p.peekAt(i)
	if t.kind != tkIdent || p.peekAt(i+1).kind != tkIdent || typeWords[p.peekAt(i+1).text] || cxxKeywords[p.peekAt(i+1).text] {
		return nil
	}
	s := p.lookup(t.text)
	if s == nil || s.kind != symTemplate || s.tmpl.kind != 'c' {
		return nil
	}
	return s.tmpl
}
