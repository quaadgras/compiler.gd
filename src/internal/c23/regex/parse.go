// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package regex

import "strings"

// An op is what a node matches.
type op uint8

const (
	opChar      op = iota // a byte of set
	opEmpty               // the empty string
	opBOL                 // ^
	opEOL                 // $
	opWordB               // \b
	opNotWordB            // \B
	opWordStart           // \<
	opWordEnd             // \>
	opBufStart            // \`
	opBufEnd              // \'
	opCat                 // sub[0] sub[1] ...
	opAlt                 // sub[0] | sub[1] | ...
	opRep                 // sub[0]{min,max}, max -1 if unbounded
	opGroup               // ( sub[0] ), subexpression n
	opBackref             // \n
)

// A charSet is a set of bytes.
type charSet [4]uint64

func (s *charSet) has(c byte) bool { return s[c>>6]&(1<<(c&63)) != 0 }
func (s *charSet) add(c byte)      { s[c>>6] |= 1 << (c & 63) }

// A node is a regular expression's syntax tree.
type node struct {
	op       op
	set      charSet
	sub      []*node
	min, max int
	n        int
	groups   bool // the tree has a subexpression
	backrefs bool // the tree has a back-reference
}

func mk(o op, sub ...*node) *node {
	n := &node{op: o, sub: sub}
	for _, s := range sub {
		n.groups = n.groups || s.groups
		n.backrefs = n.backrefs || s.backrefs
	}
	n.groups = n.groups || o == opGroup
	n.backrefs = n.backrefs || o == opBackref
	return n
}

// regError is a parse error: regcomp's result.
type regError int32

// parser parses a pattern.
type parser struct {
	s       string
	i       int
	ere     bool
	icase   bool
	newline bool
	nsub    int
	closed  []bool // which subexpressions are closed, for \n
	depth   int
}

// compile compiles pattern.
func compile(pattern string, cflags int32) (re *regexT, code int32) {
	p := &parser{
		s:       pattern,
		ere:     cflags&REG_EXTENDED != 0,
		icase:   cflags&REG_ICASE != 0,
		newline: cflags&REG_NEWLINE != 0,
		closed:  []bool{true},
	}
	defer func() {
		if e := recover(); e != nil {
			err, ok := e.(regError)
			if !ok {
				panic(e)
			}
			re, code = nil, int32(err)
		}
	}()
	root := p.regex()
	if p.i < len(p.s) {
		// A close parenthesis with none open.
		p.fail(REG_EPAREN)
	}
	return &regexT{root: root, nsub: p.nsub, icase: p.icase, newline: p.newline, nosub: cflags&REG_NOSUB != 0}, 0
}

func (p *parser) fail(code int32) { panic(regError(code)) }

func (p *parser) peek(s string) bool { return strings.HasPrefix(p.s[p.i:], s) }

// isAlt and isClose report whether | (\| in a BRE) or a subexpression's
// close is next.
func (p *parser) isAlt() bool {
	if p.ere {
		return p.peek("|")
	}
	return p.peek(`\|`)
}

func (p *parser) isClose() bool {
	if p.ere {
		return p.depth > 0 && p.peek(")")
	}
	return p.peek(`\)`)
}

// regex parses alternatives.
func (p *parser) regex() *node {
	alts := []*node{p.branch()}
	for p.isAlt() {
		if p.ere {
			p.i++
		} else {
			p.i += 2
		}
		alts = append(alts, p.branch())
	}
	if len(alts) == 1 {
		return alts[0]
	}
	return mk(opAlt, alts...)
}

// branch parses a concatenation.
func (p *parser) branch() *node {
	var items []*node
	first := true // the branch's first atom, where a BRE's ^ is an anchor
	start := true // where a BRE's * is literal, an ERE's an error
	for p.i < len(p.s) && !p.isAlt() && !(p.isClose() && p.depth > 0) {
		a, anchor := p.atom(first, start)
		if !anchor {
			a = p.dups(a)
		}
		items = append(items, a)
		first = false
		start = anchor
	}
	switch len(items) {
	case 0:
		return mk(opEmpty)
	case 1:
		return items[0]
	}
	return mk(opCat, items...)
}

// dups parses a's repetitions.
func (p *parser) dups(a *node) *node {
	for p.i < len(p.s) {
		lo, hi := 0, -1
		switch c := p.s[p.i]; {
		case c == '*':
			p.i++
		case p.ere && c == '+', !p.ere && p.peek(`\+`):
			lo = 1
			p.i += len(`\+`) - btoi(p.ere)
		case p.ere && c == '?', !p.ere && p.peek(`\?`):
			hi = 1
			p.i += len(`\?`) - btoi(p.ere)
		case p.ere && c == '{', !p.ere && p.peek(`\{`):
			p.i += len(`\{`) - btoi(p.ere)
			lo, hi = p.interval()
		default:
			return a
		}
		r := mk(opRep, a)
		r.min, r.max = lo, hi
		a = r
	}
	return a
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// interval parses an interval's m,n} (or m\} and so on), as glibc does:
// {,n} is {0,n}.
func (p *parser) interval() (lo, hi int) {
	num := func() int {
		n := -1
		for p.i < len(p.s) && '0' <= p.s[p.i] && p.s[p.i] <= '9' {
			n = min(max(n, 0)*10+int(p.s[p.i]-'0'), RE_DUP_MAX+1)
			p.i++
		}
		return n
	}
	end := func() bool {
		if p.ere && p.peek("}") {
			p.i++
			return true
		}
		if !p.ere && p.peek(`\}`) {
			p.i += 2
			return true
		}
		return false
	}
	bad := func() {
		if p.i >= len(p.s) {
			p.fail(REG_EBRACE)
		}
		p.fail(REG_BADBR)
	}
	lo = num()
	if lo < 0 {
		if !p.peek(",") {
			bad()
		}
		lo = 0
	}
	hi = lo
	if p.peek(",") {
		p.i++
		hi = num()
	}
	if !end() {
		bad()
	}
	if hi >= 0 && hi < lo {
		p.fail(REG_BADBR)
	}
	if max(lo, hi) > RE_DUP_MAX {
		p.fail(REG_ESIZE)
	}
	return lo, hi
}

// lit is the node of the byte c.
func (p *parser) lit(c byte) *node {
	n := mk(opChar)
	n.set.add(c)
	if p.icase {
		n.set.add(lower(c))
		n.set.add(upper(c))
	}
	return n
}

func lower(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

func upper(c byte) byte {
	if 'a' <= c && c <= 'z' {
		return c - ('a' - 'A')
	}
	return c
}

// atom parses an atom; anchor reports whether it is one (which no
// repetition follows).
func (p *parser) atom(first, start bool) (n *node, anchor bool) {
	c := p.s[p.i]
	if p.ere && start && (c == '*' || c == '+' || c == '?') {
		p.fail(REG_BADRPT)
	}
	if !p.ere && start && p.peek(`\{`) {
		p.fail(REG_BADRPT)
	}
	switch {
	case c == '^' && (p.ere || first):
		p.i++
		return mk(opBOL), true
	case c == '$' && (p.ere || p.i+1 == len(p.s) || strings.HasPrefix(p.s[p.i+1:], `\)`) || strings.HasPrefix(p.s[p.i+1:], `\|`)):
		p.i++
		return mk(opEOL), true
	case c == '.':
		p.i++
		n := mk(opChar)
		for b := 0; b < 256; b++ {
			if b != 0 && !(p.newline && b == '\n') {
				n.set.add(byte(b))
			}
		}
		return n, false
	case c == '[':
		p.i++
		return p.bracket(), false
	case p.ere && c == '(':
		p.i++
		return p.group(), false
	case c == '\\':
		break
	default:
		p.i++
		return p.lit(c), false
	}
	// An escape.
	if p.i+1 >= len(p.s) {
		p.fail(REG_EESCAPE)
	}
	c = p.s[p.i+1]
	p.i += 2
	switch c {
	case '(':
		if !p.ere {
			return p.group(), false
		}
	case ')':
		if !p.ere {
			p.fail(REG_EPAREN)
		}
	case '1', '2', '3', '4', '5', '6', '7', '8', '9':
		k := int(c - '0')
		if k > p.nsub || !p.closed[k] {
			p.fail(REG_ESUBREG)
		}
		n := mk(opBackref)
		n.n = k
		return n, false
	case 'w', 'W', 's', 'S':
		n := mk(opChar)
		for b := 1; b < 256; b++ {
			var in bool
			if c == 'w' || c == 'W' {
				in = isWord(byte(b))
			} else {
				in = isClass("space", byte(b))
			}
			if in == (c == 'w' || c == 's') {
				n.set.add(byte(b))
			}
		}
		return n, false
	case 'b':
		return mk(opWordB), true
	case 'B':
		return mk(opNotWordB), true
	case '<':
		return mk(opWordStart), true
	case '>':
		return mk(opWordEnd), true
	case '`':
		return mk(opBufStart), true
	case '\'':
		return mk(opBufEnd), true
	}
	return p.lit(c), false
}

// group parses a subexpression after its (.
func (p *parser) group() *node {
	p.nsub++
	k := p.nsub
	p.closed = append(p.closed, false)
	p.depth++
	sub := p.regex()
	if !p.isClose() {
		p.fail(REG_EPAREN)
	}
	if p.ere {
		p.i++
	} else {
		p.i += 2
	}
	p.depth--
	p.closed[k] = true
	n := mk(opGroup, sub)
	n.n = k
	return n
}

func isWord(c byte) bool { return c == '_' || isClass("alnum", c) }

// isClass reports whether c is in the character class name, C's locale's.
func isClass(name string, c byte) bool {
	alpha := 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
	digit := '0' <= c && c <= '9'
	switch name {
	case "alpha":
		return alpha
	case "digit":
		return digit
	case "alnum":
		return alpha || digit
	case "upper":
		return 'A' <= c && c <= 'Z'
	case "lower":
		return 'a' <= c && c <= 'z'
	case "space":
		return c == ' ' || '\t' <= c && c <= '\r'
	case "blank":
		return c == ' ' || c == '\t'
	case "punct":
		return 0x21 <= c && c <= 0x7e && !alpha && !digit
	case "print":
		return 0x20 <= c && c <= 0x7e
	case "graph":
		return 0x21 <= c && c <= 0x7e
	case "cntrl":
		return c < 0x20 || c == 0x7f
	case "xdigit":
		return digit || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
	}
	return false
}

var classes = []string{"alpha", "digit", "alnum", "upper", "lower", "space", "blank", "punct", "print", "graph", "cntrl", "xdigit"}

// bracket parses a bracket expression after its [.
func (p *parser) bracket() *node {
	n := mk(opChar)
	neg := false
	if p.peek("^") {
		neg = true
		p.i++
	}
	// elem parses a collating element [.c.] or [=c=] (one byte), or a
	// byte.
	elem := func() (c byte, class string, equiv bool) {
		if p.i >= len(p.s) {
			p.fail(REG_EBRACK)
		}
		if p.peek("[.") || p.peek("[=") || p.peek("[:") {
			delim := p.s[p.i+1]
			end := strings.Index(p.s[p.i+2:], string(delim)+"]")
			if end < 0 {
				p.fail(REG_EBRACK)
			}
			name := p.s[p.i+2 : p.i+2+end]
			p.i += 2 + end + 2
			if delim == ':' {
				for _, c := range classes {
					if c == name {
						return 0, name, false
					}
				}
				p.fail(REG_ECTYPE)
			}
			if len(name) != 1 {
				p.fail(REG_ECOLLATE)
			}
			return name[0], "", delim == '='
		}
		c = p.s[p.i]
		p.i++
		return c, "", false
	}
	first := true
	for {
		if p.i >= len(p.s) {
			p.fail(REG_EBRACK)
		}
		if p.s[p.i] == ']' && !first {
			p.i++
			break
		}
		first = false
		lo, class, equiv := elem()
		if class != "" {
			for b := 1; b < 256; b++ {
				if isClass(class, byte(b)) {
					n.set.add(byte(b))
				}
			}
			if p.peek("-") && !p.peek("-]") {
				p.fail(REG_ERANGE)
			}
			continue
		}
		if !equiv && p.peek("-") && !p.peek("-]") && p.i+1 < len(p.s) {
			p.i++
			if p.peek("[:") || p.peek("[=") {
				p.fail(REG_ERANGE)
			}
			hi, _, _ := elem()
			if hi < lo {
				p.fail(REG_ERANGE)
			}
			for b := int(lo); b <= int(hi); b++ {
				n.set.add(byte(b))
			}
			continue
		}
		n.set.add(lo)
	}
	if p.icase {
		for b := 0; b < 256; b++ {
			if n.set.has(byte(b)) {
				n.set.add(lower(byte(b)))
				n.set.add(upper(byte(b)))
			}
		}
	}
	if neg {
		for i := range n.set {
			n.set[i] = ^n.set[i]
		}
		if p.newline {
			n.set[0] &^= 1 << '\n'
		}
	}
	n.set[0] &^= 1 // NUL ends the string
	return n
}
