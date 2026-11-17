// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

// The preprocessor (C23 6.10): macros, disabled as GCC disables them,
// conditional inclusion, #include of files and of the headers cmd/cc
// provides, #embed, #line, #pragma and _Pragma.

import (
	"fmt"
	"math/bits"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type macro struct {
	fn       bool     // function-like
	params   []string // the variadic one last, if variadic
	variadic bool
	body     []token
	builtin  func(t token) []token // __LINE__ and the like
	ifOnly   bool                  // defined, __has_include...: only in #if
}

type ppCond struct {
	taken  bool // a group of this #if was included
	inElse bool
}

type pp struct {
	macros  map[string]*macro
	once    map[string]bool
	in      []ppCtx // a stack; the next token is at the front of the top
	outer   []ppCtx // in an argument's expansion: the contexts outside it
	conds   []ppCond
	out     []token
	files   []string // the files being read, innermost last
	incDirs []string
	counter int
	pack    int64
	packs   []int64
	inIf    bool // expanding a controlling expression
	pushed  map[string][]*macro
}

// A ppCtx is a list of tokens to read: a file, or a macro's expansion.
// While its context is on the stack, a macro is disabled: an identifier
// that names it does not expand, then or ever (C23 6.10.5.4). As in GCC,
// a context leaves the stack when a token is read after its last.
type ppCtx struct {
	toks  []token
	macro string
}

type ppError struct{ msg string }

func (p *pp) errorf(t token, format string, args ...any) {
	panic(ppError{fmt.Sprintf("%s:%d: %s", t.file, t.line, fmt.Sprintf(format, args...))})
}

func (p *pp) next() token {
	for len(p.in) > 0 {
		top := &p.in[len(p.in)-1]
		if len(top.toks) > 0 {
			t := top.toks[0]
			top.toks = top.toks[1:]
			return t
		}
		p.in = p.in[:len(p.in)-1]
	}
	return token{kind: tkEOF}
}

func (p *pp) peek() token {
	for i := len(p.in) - 1; i >= 0; i-- {
		if len(p.in[i].toks) > 0 {
			return p.in[i].toks[0]
		}
	}
	return token{kind: tkEOF}
}

// push makes ts the next tokens.
func (p *pp) push(ts []token) {
	if len(ts) > 0 {
		p.in = append(p.in, ppCtx{toks: ts})
	}
}

// pushMacro makes ts, the expansion of macro, the next tokens.
func (p *pp) pushMacro(macro string, ts []token) {
	p.in = append(p.in, ppCtx{toks: ts, macro: macro})
}

// disabled reports whether a macro is being expanded.
func (p *pp) disabled(name string) bool {
	for _, c := range p.in {
		if c.macro == name {
			return true
		}
	}
	for _, c := range p.outer {
		if c.macro == name {
			return true
		}
	}
	return false
}

// line returns the rest of a directive's line.
func (p *pp) line() []token {
	var ts []token
	for {
		t := p.peek()
		if t.bol || t.kind == tkEOF || t.kind == tkFileEnd {
			return ts
		}
		ts = append(ts, p.next())
	}
}

// lexUnit preprocesses sources, one translation unit, into tokens for
// the parser.
func lexUnit(srcs []source) (toks []token, err error) {
	return lexUnitDirs(srcs, nil)
}

func lexUnitDirs(srcs []source, incDirs []string) ([]token, error) {
	pt, err := preprocess(srcs, incDirs)
	if err != nil {
		return nil, err
	}
	toks, err := convert(pt)
	if err != nil {
		return nil, err
	}
	last := ""
	if len(srcs) > 0 {
		last = srcs[len(srcs)-1].name
	}
	return append(toks, token{kind: tkEOF, file: last}), nil
}

// preprocess preprocesses srcs into preprocessing tokens.
func preprocess(srcs []source, incDirs []string) (toks []token, err error) {
	defer func() {
		if r := recover(); r != nil {
			pe, ok := r.(ppError)
			if !ok {
				panic(r)
			}
			err = fmt.Errorf("%s", pe.msg)
		}
	}()
	p := &pp{macros: map[string]*macro{}, once: map[string]bool{}, incDirs: incDirs}
	p.builtins()
	for i := len(srcs) - 1; i >= 0; i-- {
		p.pushFile(srcs[i].name, srcs[i].text, token{file: srcs[i].name})
	}
	pre, err := lexRaw("<built-in>", predefinedMacros(), 1)
	if err != nil {
		return nil, err
	}
	p.push(append(pre, token{kind: tkFileEnd, file: "<built-in>"}))
	// Each file's end pops it.
	for i := len(srcs) - 1; i >= 0; i-- {
		p.files = append(p.files, srcs[i].name)
	}
	p.files = append(p.files, "<built-in>")
	p.run()
	var out []token
	space := false
	for _, t := range p.out {
		if t.kind == tkPlacemark {
			space = space || t.space
			continue
		}
		if space {
			t.space, space = true, false
		}
		out = append(out, t)
	}
	return out, nil
}

// pushFile makes the tokens of a file the next tokens.
func (p *pp) pushFile(name, text string, at token) {
	ts, err := lexRaw(name, text, 1)
	if err != nil {
		panic(ppError{err.Error()})
	}
	if len(p.files) > 200 {
		p.errorf(at, "#include nested too deeply")
	}
	ts = append(ts, token{kind: tkFileEnd, file: name, ival: int64(len(p.conds))})
	p.push(ts)
}

func (p *pp) run() {
	for {
		t := p.next()
		switch {
		case t.kind == tkEOF:
			if len(p.conds) > 0 {
				p.errorf(t, "unterminated #if")
			}
			return
		case t.kind == tkFileEnd:
			if int64(len(p.conds)) != t.ival {
				p.errorf(t, "unterminated conditional directive")
			}
			if len(p.files) > 0 {
				p.files = p.files[:len(p.files)-1]
			}
			continue
		case t.kind == tkPunct && t.text == "#" && t.bol:
			p.directive(t)
			continue
		case t.kind == tkIdent:
			if t.text == "_Pragma" && p.peek().text == "(" {
				p.pragmaOp(t)
				continue
			}
			if p.expand(&t) {
				continue
			}
			if t.text == "struct" || t.text == "union" {
				t.pack = p.pack
			}
		}
		p.out = append(p.out, t)
	}
}

// curFile is the file being read, for "..." includes.
func (p *pp) curFile() string {
	if len(p.files) == 0 {
		return ""
	}
	return p.files[len(p.files)-1]
}

// expand expands t if it names a macro, making the expansion the next
// tokens, and reports whether it did. A t that names a disabled macro is
// painted, never to expand.
func (p *pp) expand(t *token) bool {
	m := p.macros[t.text]
	if m == nil || t.noexpand || m.ifOnly {
		return false
	}
	if p.disabled(t.text) {
		t.noexpand = true
		return false
	}
	at := *t
	fix := func(ts []token) []token {
		for i := range ts {
			ts[i].file, ts[i].line, ts[i].bol = at.file, at.line, false
		}
		if len(ts) > 0 {
			ts[0].space = at.space
		} else {
			// Something, to keep the spacing of what follows.
			ts = []token{{kind: tkPlacemark, file: at.file, line: at.line, space: at.space}}
		}
		return ts
	}
	if m.builtin != nil {
		p.pushMacro(at.text, fix(m.builtin(at)))
		return true
	}
	if !m.fn {
		p.pushMacro(at.text, fix(append([]token(nil), m.body...)))
		return true
	}
	if q := p.peek(); q.kind != tkPunct || q.text != "(" {
		return false
	}
	p.next()
	args := p.args(at, m)
	p.pushMacro(at.text, fix(p.subst(at, m, args)))
	return true
}

// args reads the arguments of an invocation of m, after its "(".
func (p *pp) args(name token, m *macro) (args [][]token) {
	var cur []token
	depth := 0
	for {
		t := p.next()
		switch {
		case t.kind == tkEOF || t.kind == tkFileEnd:
			p.errorf(name, "unterminated argument list invoking macro %q", name.text)
		case t.kind == tkPunct && t.text == "#" && t.bol:
			// A directive among the arguments: GCC runs it.
			p.directive(t)
			continue
		case t.kind == tkPlacemark:
			continue
		case t.kind == tkPunct && t.text == "(":
			depth++
		case t.kind == tkPunct && t.text == ")":
			if depth == 0 {
				if cur == nil && (len(args) > 0 || len(m.params) > 0) {
					cur = []token{}
				}
				args = append(args, cur)
				goto done
			}
			depth--
		case t.kind == tkPunct && t.text == "," && depth == 0 && !(m.variadic && len(args) == len(m.params)-1):
			if cur == nil {
				cur = []token{}
			}
			args = append(args, cur)
			cur = nil
			continue
		}
		if t.bol {
			t.bol, t.space = false, true
		}
		if t.kind == tkIdent && p.disabled(t.text) {
			t.noexpand = true
		}
		if cur == nil {
			cur = []token{} // present, if empty
		}
		cur = append(cur, t)
	}
done:
	if len(m.params) == 0 && len(args) == 1 && len(args[0]) == 0 {
		args = nil
	} else if m.variadic && len(m.params) == 1 && len(args) == 1 && len(args[0]) == 0 {
		// F() of F(...): the variable arguments are left out.
		args = nil
	}
	if m.variadic && len(args) == len(m.params)-1 {
		args = append(args, nil)
	}
	if len(args) != len(m.params) {
		p.errorf(name, "macro %q passed %d arguments, but takes %d", name.text, len(args), len(m.params))
	}
	return args
}

// subst returns m's body with args substituted and ## applied.
func (p *pp) subst(name token, m *macro, args [][]token) []token {
	param := func(t token) int {
		if t.kind != tkIdent {
			return -1
		}
		for i, n := range m.params {
			if n == t.text {
				return i
			}
		}
		return -1
	}
	expanded := make([][]token, len(args))
	exp := func(i int) []token {
		if expanded[i] == nil {
			expanded[i] = p.expandList(args[i])
			if expanded[i] == nil {
				expanded[i] = []token{}
			}
		}
		return expanded[i]
	}
	vaPresent := func() bool {
		for _, t := range exp(len(args) - 1) {
			if t.kind != tkPlacemark {
				return true
			}
		}
		return false
	}
	var replace func(b []token) []token
	body := func(b []token) []token {
		var out []token
		for i := 0; i < len(b); i++ {
			t := b[i]
			pasteNext := i+1 < len(b) && b[i+1].kind == tkPasteOp
			pastePrev := i > 0 && b[i-1].kind == tkPasteOp
			if t.kind == tkPunct && t.text == "#" && i+1 < len(b) {
				n := b[i+1]
				if n.kind == tkIdent && n.text == "__VA_OPT__" && m.variadic {
					group, j := p.vaOptGroup(name, b, i+2)
					var ts []token
					if vaPresent() {
						ts = replace(group)
					}
					s := stringize(ts)
					s.space = t.space
					out = append(out, s)
					i = j
					continue
				}
				k := param(n)
				if k < 0 {
					p.errorf(name, "'#' is not followed by a macro parameter")
				}
				s := stringize(args[k])
				s.space = t.space
				out = append(out, s)
				i++
				continue
			}
			if t.kind == tkIdent && t.text == "__VA_OPT__" && m.variadic {
				group, j := p.vaOptGroup(name, b, i+1)
				pasteNext = j+1 < len(b) && b[j+1].kind == tkPasteOp
				var ts []token
				if vaPresent() {
					ts = replace(group)
				}
				if len(ts) == 0 {
					ts = []token{{kind: tkPlacemark, space: t.space}}
				} else {
					ts[0].space = t.space
				}
				out = append(out, ts...)
				i = j
				continue
			}
			if t.kind == tkPasteOp && m.variadic && i+1 < len(b) && param(b[i+1]) == len(m.params)-1 &&
				len(out) > 0 && out[len(out)-1].kind == tkPunct && out[len(out)-1].text == "," {
				// GNU's , ## __VA_ARGS__: no comma if the variable
				// arguments are left out, and no pasting.
				if args[len(args)-1] == nil {
					out = out[:len(out)-1]
				}
				va := args[len(args)-1]
				out = append(out, va...)
				if len(va) == 0 {
					out = append(out, token{kind: tkPlacemark})
				}
				i++
				continue
			}
			if k := param(t); k >= 0 {
				var a []token
				if pasteNext || pastePrev {
					a = args[k]
				} else {
					a = exp(k)
				}
				if len(a) == 0 {
					out = append(out, token{kind: tkPlacemark, space: t.space})
					continue
				}
				start := len(out)
				out = append(out, a...)
				out[start].space = t.space
				continue
			}
			out = append(out, t)
		}
		return out
	}
	// A __VA_OPT__'s tokens keep their placemarkers, for the ## around it.
	replace = func(b []token) []token {
		return p.pastes(name, body(b), true)
	}
	return p.pastes(name, body(m.body), false)
}

// pastes applies the ## of ts and, unless keep, drops its placemarkers.
func (p *pp) pastes(name token, ts []token, keep bool) []token {
	var res []token
	for i := 0; i < len(ts); i++ {
		t := ts[i]
		if t.kind != tkPasteOp {
			res = append(res, t)
			continue
		}
		if len(res) == 0 || i+1 >= len(ts) {
			p.errorf(name, "'##' cannot appear at either end of a macro expansion")
		}
		for i+1 < len(ts) && ts[i+1].kind == tkPasteOp {
			i++ // ## ## is ##
		}
		if i+1 >= len(ts) {
			p.errorf(name, "'##' cannot appear at either end of a macro expansion")
		}
		l := res[len(res)-1]
		r := ts[i+1]
		i++
		res[len(res)-1] = p.paste(name, l, r)
	}
	if keep {
		return res
	}
	var out []token
	space := false
	for _, t := range res {
		if t.kind == tkPlacemark {
			space = space || t.space
			continue
		}
		if space {
			t.space, space = true, false
		}
		out = append(out, t)
	}
	return out
}

// vaOptGroup returns the tokens between the parentheses of __VA_OPT__,
// whose "(" is at b[i], and the index of the ")".
func (p *pp) vaOptGroup(name token, b []token, i int) ([]token, int) {
	if i >= len(b) || b[i].text != "(" {
		p.errorf(name, "__VA_OPT__ must be followed by (")
	}
	depth := 0
	for j := i + 1; j < len(b); j++ {
		switch b[j].text {
		case "(":
			depth++
		case ")":
			if depth == 0 {
				return b[i+1 : j], j
			}
			depth--
		}
	}
	p.errorf(name, "unterminated __VA_OPT__")
	return nil, 0
}

func (p *pp) paste(at, l, r token) token {
	if l.kind == tkPlacemark {
		if r.kind == tkPlacemark {
			return l
		}
		r.space = l.space
		return r
	}
	if r.kind == tkPlacemark {
		return l
	}
	text := l.text + r.text
	ts, err := lexRaw(l.file, text, l.line)
	if err != nil || len(ts) != 1 {
		p.errorf(at, "pasting %q and %q does not give a valid preprocessing token", l.text, r.text)
	}
	t := ts[0]
	t.file, t.line, t.space, t.bol = l.file, l.line, l.space, false
	return t
}

func stringize(ts []token) token {
	var b strings.Builder
	first, space := true, false
	for _, t := range ts {
		if t.kind == tkPlacemark {
			space = space || t.space
			continue
		}
		if !first && (t.space || t.bol || space) {
			b.WriteByte(' ')
		}
		first, space = false, false
		if t.kind == tkString || t.kind == tkChar {
			for i := 0; i < len(t.text); i++ {
				if c := t.text[i]; c == '"' || c == '\\' {
					b.WriteByte('\\')
				}
				b.WriteByte(t.text[i])
			}
			continue
		}
		b.WriteString(t.text)
	}
	return token{kind: tkString, text: `"` + b.String() + `"`}
}

// expandList returns ts fully macro-expanded, on their own: the
// expansion of an argument, or of a directive's line.
func (p *pp) expandList(ts []token) []token {
	saveIn, saveOuter := p.in, p.outer
	p.outer = append(append([]ppCtx(nil), p.outer...), p.in...)
	end := token{kind: tkEOF}
	p.in = []ppCtx{{toks: append(append([]token(nil), ts...), end)}}
	var out []token
	space := false
	for {
		t := p.next()
		if t.kind == tkEOF {
			break
		}
		if p.inIf && t.kind == tkIdent && t.text == "defined" {
			// defined's operand is not expanded, even when a macro
			// expands to defined.
			out = append(out, t)
			if q := p.peek(); q.text == "(" {
				out = append(out, p.next(), p.next(), p.next())
			} else if q.kind == tkIdent {
				out = append(out, p.next())
			}
			continue
		}
		if t.kind == tkIdent && p.expand(&t) {
			continue
		}
		if t.kind == tkPlacemark {
			space = space || t.space
			continue
		}
		if space {
			t.space, space = true, false
		}
		out = append(out, t)
	}
	p.in, p.outer = saveIn, saveOuter
	return out
}

// directive runs the directive whose # is hash.
func (p *pp) directive(hash token) {
	d := p.peek()
	if d.bol || d.kind == tkEOF || d.kind == tkFileEnd {
		return // the null directive
	}
	p.next()
	if d.kind == tkPPNum {
		// GNU's line marker: # 200 "file" flags.
		p.lineDirective(hash, append([]token{d}, p.line()...))
		return
	}
	if d.kind != tkIdent {
		p.errorf(d, "invalid preprocessing directive")
	}
	switch d.text {
	case "define":
		p.define(d)
	case "undef":
		ts := p.line()
		if len(ts) == 0 || ts[0].kind != tkIdent {
			p.errorf(d, "no macro name given in #undef directive")
		}
		delete(p.macros, ts[0].text)
	case "include", "include_next", "import":
		p.include(d, p.line())
	case "embed":
		p.push(p.embed(d, p.line()))
	case "if":
		c := p.cond(d, p.line())
		p.conds = append(p.conds, ppCond{taken: c})
		if !c {
			p.skip()
		}
	case "ifdef", "ifndef":
		ts := p.line()
		if len(ts) == 0 || ts[0].kind != tkIdent {
			p.errorf(d, "no macro name given in #%s directive", d.text)
		}
		c := p.defined(ts[0].text) == (d.text == "ifdef")
		p.conds = append(p.conds, ppCond{taken: c})
		if !c {
			p.skip()
		}
	case "elif", "elifdef", "elifndef":
		if len(p.conds) == 0 || p.conds[len(p.conds)-1].inElse {
			p.errorf(d, "#%s without #if", d.text)
		}
		top := &p.conds[len(p.conds)-1]
		ts := p.line()
		if top.taken {
			p.skip()
			return
		}
		var c bool
		if d.text == "elif" {
			c = p.cond(d, ts)
		} else {
			if len(ts) == 0 || ts[0].kind != tkIdent {
				p.errorf(d, "no macro name given in #%s directive", d.text)
			}
			c = p.defined(ts[0].text) == (d.text == "elifdef")
		}
		if c {
			top.taken = true
		} else {
			p.skip()
		}
	case "else":
		if len(p.conds) == 0 || p.conds[len(p.conds)-1].inElse {
			p.errorf(d, "#else without #if")
		}
		p.line()
		top := &p.conds[len(p.conds)-1]
		top.inElse = true
		if top.taken {
			p.skip()
		} else {
			top.taken = true
		}
	case "endif":
		if len(p.conds) == 0 {
			p.errorf(d, "#endif without #if")
		}
		p.line()
		p.conds = p.conds[:len(p.conds)-1]
	case "line":
		p.lineDirective(d, p.expandList(p.line()))
	case "pragma":
		p.pragma(d, p.line())
	case "error":
		p.errorf(d, "#error %s", spell(p.line()))
	case "warning", "ident", "sccs", "assert", "unassert":
		p.line()
	default:
		p.errorf(d, "invalid preprocessing directive #%s", d.text)
	}
}

// skip skips a group whose condition is false: up to the #elif, #else
// or #endif that ends it, which it leaves to read next.
func (p *pp) skip() {
	depth := 0
	for {
		t := p.next()
		if t.kind == tkEOF || t.kind == tkFileEnd {
			p.push([]token{t})
			return
		}
		if !(t.kind == tkPunct && t.text == "#" && t.bol) {
			continue
		}
		d := p.peek()
		if d.bol || d.kind != tkIdent {
			continue
		}
		switch d.text {
		case "if", "ifdef", "ifndef":
			depth++
		case "elif", "elifdef", "elifndef", "else":
			if depth == 0 {
				p.push([]token{t})
				return
			}
		case "endif":
			if depth == 0 {
				p.push([]token{t})
				return
			}
			depth--
		}
	}
}

func (p *pp) defined(name string) bool {
	_, ok := p.macros[name]
	return ok
}

func (p *pp) define(d token) {
	ts := p.line()
	if len(ts) == 0 || ts[0].kind != tkIdent {
		p.errorf(d, "macro names must be identifiers")
	}
	name := ts[0].text
	if name == "defined" {
		p.errorf(d, "\"defined\" cannot be used as a macro name")
	}
	m := &macro{}
	ts = ts[1:]
	if len(ts) > 0 && ts[0].text == "(" && !ts[0].space {
		m.fn = true
		i := 1
		for {
			if i >= len(ts) {
				p.errorf(d, "missing ')' in macro parameter list")
			}
			t := ts[i]
			if t.text == ")" && len(m.params) == 0 {
				i++
				break
			}
			if t.text == "..." {
				m.params = append(m.params, "__VA_ARGS__")
				m.variadic = true
				i++
			} else if t.kind == tkIdent {
				m.params = append(m.params, t.text)
				i++
				if i < len(ts) && ts[i].text == "..." {
					m.variadic = true // GNU's named variable arguments
					i++
				}
			} else {
				p.errorf(t, "expected parameter name, found %q", t.text)
			}
			if i >= len(ts) {
				p.errorf(d, "missing ')' in macro parameter list")
			}
			if ts[i].text == ")" {
				i++
				break
			}
			if ts[i].text != "," || m.variadic {
				p.errorf(ts[i], "expected ',' or ')', found %q", ts[i].text)
			}
			i++
		}
		ts = ts[i:]
	}
	body := make([]token, len(ts))
	for i, t := range ts {
		if t.kind == tkPunct && t.text == "##" {
			t.kind = tkPasteOp
		}
		t.bol = false
		body[i] = t
	}
	if len(body) > 0 {
		body[0].space = false
	}
	p.macros[name] = m
	m.body = body
}

// headerName reads the name in #include, #embed or __has_include: the
// file, whether it is a <...> name, and the tokens after it.
func (p *pp) headerName(d token, ts []token, expanded bool) (string, bool, []token) {
	if len(ts) > 0 {
		t := ts[0]
		if t.kind == tkString && strings.HasPrefix(t.text, `"`) {
			return t.text[1 : len(t.text)-1], false, ts[1:]
		}
		if t.kind == tkPunct && t.text == "<" {
			var b strings.Builder
			for i := 1; i < len(ts); i++ {
				if ts[i].text == ">" {
					return b.String(), true, ts[i+1:]
				}
				if i > 1 && ts[i].space {
					b.WriteByte(' ')
				}
				b.WriteString(ts[i].text)
			}
			p.errorf(d, "missing terminating > character")
		}
	}
	if !expanded {
		return p.headerName(d, p.expandList(ts), true)
	}
	p.errorf(d, "#%s expects \"FILENAME\" or <FILENAME>", d.text)
	return "", false, nil
}

// find finds a header or resource: its path, or "<name>" for one that
// cmd/cc provides, and its contents.
func (p *pp) find(name string, angle bool) (string, string, bool) {
	var dirs []string
	if !angle {
		dirs = append(dirs, filepath.Dir(p.curFile()))
	}
	dirs = append(dirs, p.incDirs...)
	if filepath.IsAbs(name) {
		dirs = []string{""}
	}
	for _, dir := range dirs {
		path := filepath.Join(dir, name)
		if b, err := os.ReadFile(path); err == nil {
			return path, string(b), true
		}
	}
	if h, ok := sysHeaders[name]; ok {
		return "<" + name + ">", h, true
	}
	return "", "", false
}

func (p *pp) include(d token, ts []token) {
	name, angle, _ := p.headerName(d, ts, false)
	path, text, ok := p.find(name, angle)
	if !ok {
		if angle {
			p.errorf(d, "cc does not provide <%s>", name)
		}
		p.errorf(d, "%s: no such file", name)
	}
	if p.once[path] {
		return
	}
	p.files = append(p.files, path)
	p.pushFile(path, text, d)
}

// embed returns the tokens #embed makes: the bytes of a resource as a
// list of integers (C23 6.10.4).
func (p *pp) embed(d token, ts []token) []token {
	name, angle, rest := p.headerName(d, ts, false)
	path, data, ok := p.find(name, angle)
	if !ok || strings.HasPrefix(path, "<") {
		p.errorf(d, "%s: no such file", name)
	}
	prm, ok := p.embedParams(d, rest)
	if !ok {
		p.errorf(d, "unsupported #embed parameter")
	}
	if prm.limit >= 0 && int64(len(data)) > prm.limit {
		data = data[:prm.limit]
	}
	if len(data) == 0 {
		return prm.ifEmpty
	}
	out := append([]token(nil), prm.prefix...)
	for i := 0; i < len(data); i++ {
		if i > 0 {
			out = append(out, token{kind: tkPunct, text: ","})
		}
		out = append(out, token{kind: tkPPNum, text: strconv.Itoa(int(data[i])), space: i > 0})
	}
	out = append(out, prm.suffix...)
	for i := range out {
		out[i].file, out[i].line = d.file, d.line
	}
	return out
}

type embedParams struct {
	limit                   int64
	prefix, suffix, ifEmpty []token
}

func (p *pp) embedParams(d token, ts []token) (prm embedParams, ok bool) {
	prm.limit = -1
	for i := 0; i < len(ts); {
		if ts[i].kind != tkIdent {
			return prm, false
		}
		name := ts[i].text
		i++
		if i+1 < len(ts) && ts[i].text == "::" && ts[i+1].kind == tkIdent {
			name += "::" + ts[i+1].text
			i += 2
		}
		name = strings.TrimSuffix(strings.TrimPrefix(name, "__"), "__")
		var arg []token
		if i < len(ts) && ts[i].text == "(" {
			depth := 0
			j := i + 1
			for ; j < len(ts); j++ {
				if ts[j].text == "(" {
					depth++
				} else if ts[j].text == ")" {
					if depth == 0 {
						break
					}
					depth--
				}
			}
			if j >= len(ts) {
				p.errorf(d, "unterminated #embed parameter")
			}
			arg = ts[i+1 : j]
			i = j + 1
		}
		switch name {
		case "limit":
			prm.limit = p.eval(d, p.expandList(arg))
		case "prefix":
			prm.prefix = arg
		case "suffix":
			prm.suffix = arg
		case "if_empty":
			prm.ifEmpty = arg
		default:
			return prm, false
		}
	}
	return prm, true
}

func (p *pp) lineDirective(d token, ts []token) {
	if len(ts) == 0 || ts[0].kind != tkPPNum {
		p.errorf(d, "#line directive requires a positive integer argument")
	}
	n, err := strconv.ParseInt(ts[0].text, 10, 64)
	if err != nil {
		p.errorf(d, "%q after #line is not a positive integer", ts[0].text)
	}
	file := ""
	if len(ts) > 1 {
		if ts[1].kind != tkString {
			p.errorf(d, "invalid filename %q", ts[1].text)
		}
		s, err := strconv.Unquote(ts[1].text)
		if err != nil {
			s = ts[1].text[1 : len(ts[1].text)-1]
		}
		file = s
	}
	// Renumber the rest of the file.
	if len(p.in) == 0 {
		return
	}
	top := p.in[len(p.in)-1].toks
	if len(top) == 0 {
		return
	}
	delta := n - int64(d.line+1)
	orig := d.file
	for i := range top {
		if top[i].kind == tkFileEnd {
			break
		}
		if top[i].file != orig {
			continue
		}
		top[i].line += int(delta)
		if file != "" {
			top[i].file = file
		}
	}
}

func (p *pp) pragma(d token, ts []token) {
	if len(ts) == 0 {
		return
	}
	switch ts[0].text {
	case "once":
		p.once[p.curFile()] = true
	case "push_macro", "pop_macro":
		// push_macro("name"): save the macro's definition, or that it
		// has none; pop_macro("name") restores it.
		if len(ts) < 3 || ts[2].kind != tkString {
			return
		}
		name := ts[2].text[1 : len(ts[2].text)-1]
		if p.pushed == nil {
			p.pushed = map[string][]*macro{}
		}
		if ts[0].text == "push_macro" {
			p.pushed[name] = append(p.pushed[name], p.macros[name])
			return
		}
		st := p.pushed[name]
		if len(st) == 0 {
			return
		}
		m := st[len(st)-1]
		p.pushed[name] = st[:len(st)-1]
		if m == nil {
			delete(p.macros, name)
		} else {
			p.macros[name] = m
		}
	case "pack":
		// pack(n), pack(), pack(push[, n]), pack(pop).
		var args []string
		for _, t := range ts[1:] {
			if t.text != "(" && t.text != ")" && t.text != "," {
				args = append(args, t.text)
			}
		}
		n := func(s string) int64 {
			v, _ := strconv.ParseInt(s, 0, 64)
			return v
		}
		switch {
		case len(args) == 0:
			p.pack = 0
		case args[0] == "push":
			p.packs = append(p.packs, p.pack)
			if len(args) > 1 {
				p.pack = n(args[1])
			}
		case args[0] == "pop":
			if len(p.packs) > 0 {
				p.pack = p.packs[len(p.packs)-1]
				p.packs = p.packs[:len(p.packs)-1]
			}
		default:
			p.pack = n(args[0])
		}
	}
}

// pragmaOp runs _Pragma("..."), whose name is t.
func (p *pp) pragmaOp(t token) {
	p.next()
	s := p.next()
	if s.kind != tkString || p.next().text != ")" {
		p.errorf(t, "_Pragma takes a parenthesized string literal")
	}
	text := s.text[strings.IndexByte(s.text, '"')+1 : len(s.text)-1]
	text = strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(text)
	ts, err := lexRaw(t.file, text, t.line)
	if err != nil {
		panic(ppError{err.Error()})
	}
	p.pragma(t, ts)
}

// cond evaluates the condition of #if or #elif.
func (p *pp) cond(d token, ts []token) bool {
	if len(ts) == 0 {
		p.errorf(d, "#%s with no expression", d.text)
	}
	// defined and the __has_ operators, before expansion. A macro that
	// expands to them works too, as in GCC.
	var pre func(ts []token) []token
	pre = func(ts []token) []token {
		var out []token
		for i := 0; i < len(ts); i++ {
			t := ts[i]
			if t.kind != tkIdent {
				out = append(out, t)
				continue
			}
			switch t.text {
			case "defined":
				var name token
				if i+1 < len(ts) && ts[i+1].text == "(" {
					if i+3 >= len(ts) || ts[i+3].text != ")" {
						p.errorf(d, "missing ')' after \"defined\"")
					}
					name, i = ts[i+2], i+3
				} else if i+1 < len(ts) {
					name, i = ts[i+1], i+1
				}
				if name.kind != tkIdent {
					p.errorf(d, "operator \"defined\" requires an identifier")
				}
				out = append(out, boolNum(p.defined(name.text)))
			case "__has_include", "__has_include_next", "__has_embed", "__has_c_attribute",
				"__has_attribute", "__has_builtin", "__has_feature", "__has_extension", "__has_cpp_attribute":
				if i+1 >= len(ts) || ts[i+1].text != "(" {
					p.errorf(d, "missing '(' after %s", t.text)
				}
				depth, j := 0, i+2
				for ; j < len(ts); j++ {
					if ts[j].text == "(" {
						depth++
					} else if ts[j].text == ")" {
						if depth == 0 {
							break
						}
						depth--
					}
				}
				if j >= len(ts) {
					p.errorf(d, "missing ')' after %s", t.text)
				}
				out = append(out, p.has(d, t.text, ts[i+2:j]))
				i = j
			default:
				out = append(out, t)
			}
		}
		return out
	}
	ts = pre(ts)
	p.inIf = true
	ts = p.expandList(ts)
	p.inIf = false
	ts = pre(ts)
	return p.eval(d, ts) != 0
}

func boolNum(b bool) token {
	if b {
		return token{kind: tkPPNum, text: "1"}
	}
	return token{kind: tkPPNum, text: "0"}
}

// has evaluates the __has_ operators.
func (p *pp) has(d token, op string, arg []token) token {
	switch op {
	case "__has_include", "__has_include_next":
		name, angle, _ := p.headerName(d, arg, false)
		_, _, ok := p.find(name, angle)
		return boolNum(ok)
	case "__has_embed":
		name, angle, rest := p.headerName(d, arg, false)
		path, data, ok := p.find(name, angle)
		if !ok || strings.HasPrefix(path, "<") {
			return boolNum(false)
		}
		prm, ok := p.embedParams(d, rest)
		if !ok {
			return boolNum(false)
		}
		if prm.limit >= 0 && int64(len(data)) > prm.limit {
			data = data[:prm.limit]
		}
		if len(data) == 0 {
			return token{kind: tkPPNum, text: "2"}
		}
		return token{kind: tkPPNum, text: "1"}
	case "__has_c_attribute":
		s := spell(arg)
		s = strings.ReplaceAll(s, " ", "")
		if v, ok := c23Attrs[strings.TrimSuffix(strings.TrimPrefix(s, "__"), "__")]; ok {
			return token{kind: tkPPNum, text: v}
		}
	case "__has_builtin":
		if len(arg) == 1 && hasBuiltin[arg[0].text] {
			return boolNum(true)
		}
	case "__has_attribute":
		s := strings.ReplaceAll(spell(arg), " ", "")
		s = strings.TrimPrefix(strings.TrimPrefix(s, "gnu::"), "__gnu__::")
		return boolNum(gnuAttrs[strings.TrimSuffix(strings.TrimPrefix(s, "__"), "__")])
	}
	return boolNum(false)
}

// The standard attributes, which cmd/cc accepts (and may ignore).
var c23Attrs = map[string]string{
	"deprecated": "201904L", "fallthrough": "201904L", "maybe_unused": "201904L",
	"nodiscard": "202003L", "noreturn": "202202L", "_Noreturn": "202202L",
	"unsequenced": "202207L", "reproducible": "202207L",
}

// The __builtin_ functions cmd/cc implements, for __has_builtin.
var hasBuiltin = map[string]bool{
	"__builtin_offsetof": true, "__builtin_va_start": true, "__builtin_va_arg": true,
	"__builtin_va_end": true, "__builtin_va_copy": true, "__builtin_expect": true,
	"__builtin_unreachable": true, "__builtin_types_compatible_p": true,
	"__builtin_constant_p": true,
}

// The GNU attributes cmd/cc implements or may ignore, for __has_attribute.
var gnuAttrs = map[string]bool{
	"packed": true, "aligned": true, "unused": true, "used": true, "noreturn": true,
	"const": true, "pure": true, "deprecated": true, "maybe_unused": true,
	"fallthrough": true, "always_inline": true, "noinline": true, "cold": true,
	"hot": true, "format": true, "nonnull": true, "warn_unused_result": true,
	"nodiscard": true, "visibility": true,
}

// spell is ts as written.
func spell(ts []token) string {
	var b strings.Builder
	for i, t := range ts {
		if i > 0 && t.space {
			b.WriteByte(' ')
		}
		b.WriteString(t.text)
	}
	return b.String()
}

// eval evaluates a controlling expression in intmax_t and uintmax_t
// (C23 6.10.2).
func (p *pp) eval(d token, ts []token) int64 {
	e := &ppExpr{p: p, d: d}
	for _, t := range ts {
		if t.kind == tkPlacemark {
			continue
		}
		if t.kind == tkIdent {
			v := "0"
			if t.text == "true" {
				v = "1"
			}
			t = token{kind: tkPPNum, text: v}
		}
		e.ts = append(e.ts, t)
	}
	v := e.cond(true)
	if e.i < len(e.ts) {
		p.errorf(d, "missing binary operator before token %q", e.ts[e.i].text)
	}
	return v.v
}

type ppVal struct {
	v        int64
	unsigned bool
}

type ppExpr struct {
	p  *pp
	d  token
	ts []token
	i  int
}

func (e *ppExpr) peek() string {
	if e.i < len(e.ts) {
		return e.ts[e.i].text
	}
	return ""
}

// cond parses a conditional expression; eval is false where its value
// does not matter (division by zero is no error there).
func (e *ppExpr) cond(eval bool) ppVal {
	c := e.binary(0, eval)
	if e.peek() != "?" {
		return c
	}
	e.i++
	x := e.cond(eval && c.v != 0)
	if e.peek() != ":" {
		e.p.errorf(e.d, "'?' without following ':'")
	}
	e.i++
	y := e.cond(eval && c.v == 0)
	u := x.unsigned || y.unsigned
	if c.v != 0 {
		return ppVal{x.v, u}
	}
	return ppVal{y.v, u}
}

var ppPrec = map[string]int{
	"||": 1, "&&": 2, "|": 3, "^": 4, "&": 5, "==": 6, "!=": 6,
	"<": 7, ">": 7, "<=": 7, ">=": 7, "<<": 8, ">>": 8, "+": 9, "-": 9, "*": 10, "/": 10, "%": 10,
}

func (e *ppExpr) binary(min int, eval bool) ppVal {
	x := e.unary(eval)
	for {
		op := e.peek()
		prec, ok := ppPrec[op]
		if !ok || prec <= min {
			return x
		}
		e.i++
		yEval := eval
		if op == "&&" && x.v == 0 || op == "||" && x.v != 0 {
			yEval = false
		}
		y := e.binary(prec, yEval)
		x = e.apply(op, x, y, eval)
	}
}

func b2v(b bool) ppVal {
	if b {
		return ppVal{v: 1}
	}
	return ppVal{}
}

func (e *ppExpr) apply(op string, x, y ppVal, eval bool) ppVal {
	u := x.unsigned || y.unsigned
	switch op {
	case "||":
		return b2v(x.v != 0 || y.v != 0)
	case "&&":
		return b2v(x.v != 0 && y.v != 0)
	case "<<":
		return ppVal{x.v << uint64(y.v), x.unsigned}
	case ">>":
		if x.unsigned {
			return ppVal{int64(uint64(x.v) >> uint64(y.v)), true}
		}
		return ppVal{x.v >> uint64(y.v), false}
	case "==":
		return b2v(x.v == y.v)
	case "!=":
		return b2v(x.v != y.v)
	case "<", ">", "<=", ">=":
		var lt, gt bool
		if u {
			lt, gt = uint64(x.v) < uint64(y.v), uint64(x.v) > uint64(y.v)
		} else {
			lt, gt = x.v < y.v, x.v > y.v
		}
		switch op {
		case "<":
			return b2v(lt)
		case ">":
			return b2v(gt)
		case "<=":
			return b2v(!gt)
		}
		return b2v(!lt)
	case "/", "%":
		if y.v == 0 {
			if eval {
				e.p.errorf(e.d, "division by zero in #if")
			}
			return ppVal{0, u}
		}
		if u {
			if op == "/" {
				return ppVal{int64(uint64(x.v) / uint64(y.v)), true}
			}
			return ppVal{int64(uint64(x.v) % uint64(y.v)), true}
		}
		if op == "/" {
			return ppVal{x.v / y.v, false}
		}
		return ppVal{x.v % y.v, false}
	case "+":
		return ppVal{x.v + y.v, u}
	case "-":
		return ppVal{x.v - y.v, u}
	case "*":
		return ppVal{x.v * y.v, u}
	case "&":
		return ppVal{x.v & y.v, u}
	case "|":
		return ppVal{x.v | y.v, u}
	case "^":
		return ppVal{x.v ^ y.v, u}
	}
	return ppVal{}
}

func (e *ppExpr) unary(eval bool) ppVal {
	if e.i >= len(e.ts) {
		e.p.errorf(e.d, "#if with no expression")
	}
	t := e.ts[e.i]
	e.i++
	switch t.text {
	case "+":
		return e.unary(eval)
	case "-":
		x := e.unary(eval)
		return ppVal{-x.v, x.unsigned}
	case "~":
		x := e.unary(eval)
		return ppVal{^x.v, x.unsigned}
	case "!":
		return b2v(e.unary(eval).v == 0)
	case "(":
		x := e.cond(eval)
		if e.peek() != ")" {
			e.p.errorf(e.d, "missing ')' in expression")
		}
		e.i++
		return x
	}
	switch t.kind {
	case tkPPNum:
		c, err := convertNumber(t)
		if err != nil || c.kind != tkInt {
			e.p.errorf(e.d, "invalid integer %q in #if", t.text)
		}
		return ppVal{c.ival, c.ty.isUnsigned()}
	case tkChar:
		c, err := convertChar(t)
		if err != nil {
			e.p.errorf(e.d, "%v", err)
		}
		return ppVal{c.ival, false}
	}
	e.p.errorf(e.d, "token %q is not valid in preprocessor expressions", t.text)
	return ppVal{}
}

func (p *pp) builtins() {
	str := func(s string) []token {
		return []token{{kind: tkString, text: strconv.Quote(s)}}
	}
	num := func(n int) []token {
		return []token{{kind: tkPPNum, text: strconv.Itoa(n)}}
	}
	p.macros["__FILE__"] = &macro{builtin: func(t token) []token { return str(t.file) }}
	p.macros["__BASE_FILE__"] = &macro{builtin: func(t token) []token { return str(t.file) }}
	p.macros["__LINE__"] = &macro{builtin: func(t token) []token { return num(t.line) }}
	p.macros["__COUNTER__"] = &macro{builtin: func(t token) []token { p.counter++; return num(p.counter - 1) }}
	p.macros["__INCLUDE_LEVEL__"] = &macro{builtin: func(t token) []token { return num(max(len(p.files)-2, 0)) }}
	// Fixed, so that a translation depends only on its input.
	p.macros["__DATE__"] = &macro{builtin: func(t token) []token { return str("Jan  1 1970") }}
	p.macros["__TIME__"] = &macro{builtin: func(t token) []token { return str("00:00:00") }}
	p.macros["__TIMESTAMP__"] = &macro{builtin: func(t token) []token { return str("Thu Jan  1 00:00:00 1970") }}
	for _, name := range []string{"__has_include", "__has_include_next", "__has_embed", "__has_c_attribute", "__has_builtin", "__has_attribute"} {
		p.macros[name] = &macro{ifOnly: true}
	}
}

// predefined are the macros cmd/cc predefines: C23's, and the shape of
// its target, LP64 and little-endian everywhere (doc/gd/cc.md).
const predefined = `
#define __STDC__ 1
#define __STDC_VERSION__ 202311L
#define __STDC_HOSTED__ 1
#define __STDC_UTF_16__ 1
#define __STDC_UTF_32__ 1
#define __STDC_IEC_559__ 1
#define __STDC_NO_ATOMICS__ 1
#define __STDC_NO_THREADS__ 1
#define __STDC_NO_VLA__ 1
#define __STDC_EMBED_NOT_FOUND__ 0
#define __STDC_EMBED_FOUND__ 1
#define __STDC_EMBED_EMPTY__ 2
#define __GD_CC__ 1
#define __SIZEOF_INT128__ 16
#define __int128_t __int128
#define __uint128_t unsigned __int128
#define __LP64__ 1
#define _LP64 1
#define __CHAR_BIT__ 8
#define __SIZEOF_SHORT__ 2
#define __SIZEOF_INT__ 4
#define __SIZEOF_LONG__ 8
#define __SIZEOF_LONG_LONG__ 8
#define __SIZEOF_POINTER__ 8
#define __SIZEOF_FLOAT__ 4
#define __SIZEOF_DOUBLE__ 8
#define __SIZEOF_LONG_DOUBLE__ 16
#define __SIZEOF_SIZE_T__ 8
#define __SIZEOF_PTRDIFF_T__ 8
#define __SIZEOF_WCHAR_T__ 4
#define __ORDER_LITTLE_ENDIAN__ 1234
#define __ORDER_BIG_ENDIAN__ 4321
#define __BYTE_ORDER__ __ORDER_LITTLE_ENDIAN__
#define __SCHAR_MAX__ 0x7f
#define __SHRT_MAX__ 0x7fff
#define __INT_MAX__ 0x7fffffff
#define __LONG_MAX__ 0x7fffffffffffffffL
#define __LONG_LONG_MAX__ 0x7fffffffffffffffLL
#define __WCHAR_MAX__ 0x7fffffff
#define __SIZE_MAX__ 0xffffffffffffffffUL
#define __PTRDIFF_MAX__ 0x7fffffffffffffffL
#define __INTMAX_MAX__ 0x7fffffffffffffffL
#define __UINTMAX_MAX__ 0xffffffffffffffffUL
#define __INTPTR_MAX__ 0x7fffffffffffffffL
#define __UINTPTR_MAX__ 0xffffffffffffffffUL
#define __SIZE_TYPE__ unsigned long
#define __PTRDIFF_TYPE__ long
#define __WCHAR_TYPE__ int
#define __WINT_TYPE__ unsigned int
#define __INTMAX_TYPE__ long
#define __UINTMAX_TYPE__ unsigned long
#define __INTPTR_TYPE__ long
#define __UINTPTR_TYPE__ unsigned long
#define __CHAR16_TYPE__ unsigned short
#define __CHAR32_TYPE__ unsigned int
#define __INT_LEAST8_TYPE__ signed char
#define __INT_LEAST16_TYPE__ short
#define __INT_LEAST32_TYPE__ int
#define __INT_LEAST64_TYPE__ long
#define __UINT_LEAST8_TYPE__ unsigned char
#define __UINT_LEAST16_TYPE__ unsigned short
#define __UINT_LEAST32_TYPE__ unsigned int
#define __UINT_LEAST64_TYPE__ unsigned long
#define __INT_FAST8_TYPE__ signed char
#define __INT_FAST16_TYPE__ long
#define __INT_FAST32_TYPE__ long
#define __INT_FAST64_TYPE__ long
#define __UINT_FAST8_TYPE__ unsigned char
#define __UINT_FAST16_TYPE__ unsigned long
#define __UINT_FAST32_TYPE__ unsigned long
#define __UINT_FAST64_TYPE__ unsigned long
#define __FLT_MIN__ 1.17549435082228750796873653722224568e-38F
#define __FLT_MAX__ 3.40282346638528859811704183484516925e+38F
#define __FLT_EPSILON__ 1.19209289550781250000000000000000000e-7F
#define __FLT_DENORM_MIN__ 1.40129846432481707092372958328991613e-45F
#define __FLT_MANT_DIG__ 24
#define __FLT_DIG__ 6
#define __FLT_MIN_EXP__ (-125)
#define __FLT_MAX_EXP__ 128
#define __FLT_RADIX__ 2
#define __DBL_MIN__ ((double)2.22507385850720138309023271733240406e-308L)
#define __DBL_MAX__ ((double)1.79769313486231570814527423731704357e+308L)
#define __DBL_EPSILON__ ((double)2.22044604925031308084726333618164062e-16L)
#define __DBL_DENORM_MIN__ ((double)4.94065645841246544176568792868221372e-324L)
#define __DBL_MANT_DIG__ 53
#define __DBL_DIG__ 15
#define __DBL_MIN_EXP__ (-1021)
#define __DBL_MAX_EXP__ 1024
#define __LDBL_MIN__ 2.22507385850720138309023271733240406e-308L
#define __LDBL_MAX__ 1.79769313486231570814527423731704357e+308L
#define __LDBL_EPSILON__ 2.22044604925031308084726333618164062e-16L
#define __LDBL_MANT_DIG__ 53
#define __LDBL_DIG__ 15
#define __INT8_TYPE__ signed char
#define __INT16_TYPE__ short
#define __INT32_TYPE__ int
#define __INT64_TYPE__ long
#define __UINT8_TYPE__ unsigned char
#define __UINT16_TYPE__ unsigned short
#define __UINT32_TYPE__ unsigned int
#define __UINT64_TYPE__ unsigned long
`

// convert turns preprocessing tokens into the parser's: numbers and
// literals get their values and types; adjacent string literals are
// concatenated.
func convert(in []token) ([]token, error) {
	var out []token
	for i := 0; i < len(in); i++ {
		t := in[i]
		var err error
		switch t.kind {
		case tkPlacemark, tkFileEnd:
			continue
		case tkIdent:
			t.text = identName(t.text)
		case tkPPNum:
			t, err = convertNumber(t)
		case tkChar:
			t, err = convertChar(t)
		case tkString:
			j := i + 1
			for j < len(in) && (in[j].kind == tkString || in[j].kind == tkPlacemark || in[j].kind == tkFileEnd) {
				j++
			}
			t, err = convertString(in[i:j])
			i = j - 1
		case tkOther:
			err = fmt.Errorf("%s:%d: stray %q in program", t.file, t.line, t.text)
		case tkPunct:
			if t.text == "#" || t.text == "##" {
				err = fmt.Errorf("%s:%d: stray %q in program", t.file, t.line, t.text)
			}
		}
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func convertNumber(t token) (token, error) {
	errf := func(format string, args ...any) (token, error) {
		return t, fmt.Errorf("%s:%d: %s", t.file, t.line, fmt.Sprintf(format, args...))
	}
	s := strings.ReplaceAll(t.text, "'", "")
	low := strings.ToLower(s)
	// GNU's imaginary constants: an i or j among the suffix's letters.
	if k := strings.LastIndexAny(low, "ij"); k > 0 && !strings.HasPrefix(low, "0x") || k > 1 && strings.HasPrefix(low, "0x") && k == len(low)-1 {
		if rest := low[k+1:]; strings.Trim(rest, "fl") == "" {
			c, err := convertNumber(token{file: t.file, line: t.line, text: s[:k] + s[k+1:], kind: tkPPNum})
			if err != nil {
				return t, err
			}
			if c.kind == tkInt {
				c.kind, c.fval, c.ty = tkFloat, float64(c.ival), tDouble
			}
			c.imag = true
			return c, nil
		}
	}
	hex := strings.HasPrefix(low, "0x")
	isFloat := strings.Contains(s, ".") || hex && strings.Contains(low, "p") ||
		!hex && strings.ContainsAny(low, "e") && !strings.HasPrefix(low, "0b")
	if isFloat {
		t.kind, t.ty = tkFloat, tDouble
		num := low
		switch {
		case strings.HasSuffix(low, "f32") || strings.HasSuffix(low, "f64"):
			if strings.HasSuffix(low, "f32") {
				t.ty = tFloat
			}
			num = low[:len(low)-3]
		case strings.HasSuffix(low, "f"):
			t.ty, num = tFloat, low[:len(low)-1]
		case strings.HasSuffix(low, "l"):
			t.ty, num = tLDouble, low[:len(low)-1]
		}
		if hex && !strings.Contains(num, "p") {
			return errf("hexadecimal floating constant %q requires an exponent", t.text)
		}
		f, err := strconv.ParseFloat(num, 64)
		if err != nil {
			if ne, ok := err.(*strconv.NumError); !ok || ne.Err != strconv.ErrRange {
				return errf("invalid floating constant %q", t.text)
			}
		}
		if t.ty == tFloat {
			f = float64(float32(f))
		}
		t.fval = f
		return t, nil
	}
	base, digits := 10, low
	switch {
	case hex:
		base, digits = 16, low[2:]
	case strings.HasPrefix(low, "0b"):
		base, digits = 2, low[2:]
	case len(low) > 1 && low[0] == '0':
		base, digits = 8, low[1:]
	}
	j := 0
	for j < len(digits) && (isDigit(digits[j]) || base == 16 && isHex(digits[j])) {
		j++
	}
	suffix := digits[j:]
	digits = digits[:j]
	if digits == "" {
		if base == 8 {
			digits = "0"
		} else {
			return errf("invalid integer constant %q", t.text)
		}
	}
	v, err := strconv.ParseUint(digits, base, 64)
	if err != nil {
		if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
			return errf("integer constant %q is too large", t.text)
		}
		return errf("invalid integer constant %q", t.text)
	}
	var u, l bool
	switch suffix {
	case "":
	case "u":
		u = true
	case "l", "ll":
		l = true
	case "ul", "lu", "ull", "llu":
		u, l = true, true
	case "z":
		l = true
	case "uz", "zu":
		u, l = true, true
	case "wb", "uwb", "wbu":
		// C23's _BitInt literals: the narrowest that holds v.
		n := int64(bits.Len64(v))
		if suffix == "wb" {
			n++
			if n < 2 {
				n = 2
			}
			if n > 64 {
				return errf("integer constant %q is too large for _BitInt(64)", t.text)
			}
		}
		t.kind, t.ival, t.ty = tkInt, int64(v), bitInt(max(n, 1), suffix != "wb")
		return t, nil
	default:
		return errf("invalid suffix %q on integer constant", t.text[len(t.text)-len(suffix):])
	}
	// C23 6.4.4.1: the first type that can represent v.
	switch {
	case !u && !l && v <= 0x7fffffff:
		t.ty = tInt
	case !l && (u || base != 10) && v <= 0xffffffff:
		t.ty = tUInt
	case !u && v <= 0x7fffffffffffffff:
		t.ty = tLong
	case u || base != 10:
		t.ty = tULong
	default:
		// Too big for long long: GCC makes it unsigned.
		t.ty = tULong
	}
	if target.ilp32() {
		t.ty = ilp32IntLit(t.ty, v, suffix, base == 10)
	}
	t.kind, t.ival = tkInt, int64(v)
	return t, nil
}

// literalBody returns a literal's encoding prefix and its text between
// the quotes.
func literalBody(text string) (prefix, body string) {
	q := strings.IndexAny(text, `"'`)
	return text[:q], text[q+1 : len(text)-1]
}

func prefixType(prefix string) *ctype {
	switch prefix {
	case "u8":
		return tUChar
	case "u":
		return tUShort
	case "U":
		return tUInt
	case "L":
		return tInt
	}
	return tChar
}

// codeUnits decodes a literal's body into code units of the prefix's
// encoding.
func codeUnits(prefix, body string) ([]uint32, error) {
	var units []uint32
	for i := 0; i < len(body); {
		v, n, ucn, err := unescape(body[i:])
		if err != nil {
			return nil, err
		}
		i += n
		if !ucn {
			units = append(units, v)
			continue
		}
		switch prefix {
		case "", "u8":
			var b [4]byte
			k := encodeRune(b[:], rune(v))
			for _, c := range b[:k] {
				units = append(units, uint32(c))
			}
		case "u":
			if v >= 0x10000 {
				v -= 0x10000
				units = append(units, 0xd800+(v>>10), 0xdc00+(v&0x3ff))
			} else {
				units = append(units, v)
			}
		default:
			units = append(units, v)
		}
	}
	return units, nil
}

func encodeRune(b []byte, r rune) int {
	s := string(r)
	return copy(b, s)
}

func convertChar(t token) (token, error) {
	prefix, body := literalBody(t.text)
	units, err := codeUnits(prefix, body)
	if err != nil {
		return t, fmt.Errorf("%s:%d: %v", t.file, t.line, err)
	}
	if len(units) == 0 {
		return t, fmt.Errorf("%s:%d: empty character constant", t.file, t.line)
	}
	t.kind = tkChar
	switch prefix {
	case "":
		// An int, of a char's value; several make one int, as in GCC.
		t.ty = tInt
		if len(units) == 1 {
			t.ival = int64(int8(units[0]))
		} else {
			var v int32
			for _, c := range units {
				v = v<<8 | int32(uint8(c))
			}
			t.ival = int64(v)
		}
	case "u8":
		t.ty, t.ival = tUChar, int64(uint8(units[0]))
	case "u":
		t.ty, t.ival = tUShort, int64(uint16(units[0]))
	case "U":
		t.ty, t.ival = tUInt, int64(units[0])
	case "L":
		t.ty, t.ival = tInt, int64(int32(units[0]))
	}
	return t, nil
}

func convertString(ts []token) (token, error) {
	t := ts[0]
	prefix := ""
	var texts []string
	for _, s := range ts {
		if s.kind != tkString {
			continue
		}
		texts = append(texts, s.text)
		p, _ := literalBody(s.text)
		if p != "" {
			if prefix != "" && prefix != p {
				return t, fmt.Errorf("%s:%d: concatenation of string literals with different encoding prefixes", s.file, s.line)
			}
			prefix = p
		}
	}
	elem := prefixType(prefix)
	size := elem.sizeof()
	var b []byte
	for _, s := range texts {
		_, body := literalBody(s)
		units, err := codeUnits(prefix, body)
		if err != nil {
			return t, fmt.Errorf("%s:%d: %v", t.file, t.line, err)
		}
		for _, u := range units {
			for k := int64(0); k < size; k++ {
				b = append(b, byte(u>>(8*k)))
			}
		}
	}
	t.kind, t.str, t.ty, t.text = tkString, string(b), elem, strings.Join(texts, " ")
	return t, nil
}

// lex preprocesses one file.
func lex(file, src string) ([]token, error) {
	return lexUnit([]source{{file, src}})
}
