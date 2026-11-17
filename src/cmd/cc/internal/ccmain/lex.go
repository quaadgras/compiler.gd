// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type tokKind int

const (
	tkEOF tokKind = iota
	tkIdent
	tkInt
	tkFloat
	tkChar
	tkString
	tkPunct
)

type token struct {
	file string
	kind tokKind
	text string // identifier, punctuator, or the literal as written
	str  string // tkString: the bytes
	ival int64  // tkInt, tkChar
	fval float64
	line int
}

func (t token) String() string {
	if t.kind == tkEOF {
		return "end of file"
	}
	return fmt.Sprintf("%q", t.text)
}

var puncts = []string{
	"<<=", ">>=", "...", "->", "++", "--", "<<", ">>", "<=", ">=", "==", "!=",
	"&&", "||", "+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=",
}

// A source is a piece of C to translate: a .c file, or a preamble.
type source struct {
	name string // path, for errors and to find "..." headers
	text string
}

// The system headers cmd/cc provides (cmd/go's ccHeaders matches): what
// they declare is built in.
var systemHeaders = map[string]bool{
	"assert.h": true, "stddef.h": true, "stdint.h": true, "stdio.h": true,
	"stdlib.h": true, "string.h": true,
}

// lex splits src into tokens.
func lex(file, src string) ([]token, error) {
	return lexUnit([]source{{file, src}})
}

// lexUnit splits sources into the tokens of one translation unit. The
// preprocessor is here: object-like #define and #undef; #include of the
// system headers above (nothing to read) and of "..." headers, each read
// once in the unit, relative to the including file; #if, #ifdef, #ifndef,
// #else and #endif. Other directives are ignored.
func lexUnit(srcs []source) ([]token, error) {
	null, _ := lexRaw("", "((void*)0)", 0)
	st := &pp{
		macros: map[string][]token{
			"NULL":         null,
			"EXIT_SUCCESS": {{kind: tkInt, text: "0"}},
			"EXIT_FAILURE": {{kind: tkInt, text: "1", ival: 1}},
		},
		included: map[string]bool{},
	}
	for _, s := range srcs {
		if err := st.file(s.name, s.text); err != nil {
			return nil, err
		}
	}
	var toks []token
	var expand func(ts []token, active map[string]bool)
	expand = func(ts []token, active map[string]bool) {
		for _, t := range ts {
			if t.kind == tkIdent && !active[t.text] {
				if m, ok := st.macros[t.text]; ok {
					active[t.text] = true
					var mt []token
					for _, x := range m {
						x.line, x.file = t.line, t.file
						mt = append(mt, x)
					}
					expand(mt, active)
					delete(active, t.text)
					continue
				}
			}
			toks = append(toks, t)
		}
	}
	expand(st.raw, map[string]bool{})
	last := ""
	if len(srcs) > 0 {
		last = srcs[len(srcs)-1].name
	}
	toks = append(toks, token{kind: tkEOF, file: last})
	return toks, nil
}

// pp is the preprocessor's state over a unit.
type pp struct {
	macros   map[string][]token
	included map[string]bool
	raw      []token
}

func (st *pp) file(name, src string) error {
	lines := strings.Split(src, "\n")
	var seg strings.Builder
	segLine := 1
	flush := func(next int) error {
		ts, err := lexRaw(name, seg.String(), segLine)
		if err != nil {
			return err
		}
		st.raw = append(st.raw, ts...)
		seg.Reset()
		segLine = next
		return nil
	}
	errf := func(i int, format string, args ...any) error {
		return fmt.Errorf("%s:%d: %s", name, i+1, fmt.Sprintf(format, args...))
	}
	var conds []bool // whether each open #if's branch is taken
	var taken []bool // whether some branch of each open #if was
	active := func() bool {
		for _, c := range conds {
			if !c {
				return false
			}
		}
		return true
	}
	for i := 0; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(t, "#") {
			if active() {
				seg.WriteString(lines[i])
			}
			seg.WriteString("\n")
			continue
		}
		start := i
		for strings.HasSuffix(t, "\\") && i+1 < len(lines) {
			i++
			t = t[:len(t)-1] + " " + strings.TrimSpace(lines[i])
		}
		for range i - start + 1 {
			seg.WriteString("\n")
		}
		d := strings.TrimSpace(t[1:])
		word, rest, _ := strings.Cut(d, " ")
		if j := strings.IndexAny(word, "(<\""); j > 0 {
			word, rest = word[:j], word[j:]+" "+rest
		}
		rest = strings.TrimSpace(rest)
		switch word {
		case "ifdef", "ifndef":
			_, def := st.macros[rest]
			c := def == (word == "ifdef")
			conds, taken = append(conds, c), append(taken, c)
			continue
		case "if":
			c, err := st.cond(name, rest, start+1)
			if err != nil {
				return err
			}
			conds, taken = append(conds, c), append(taken, c)
			continue
		case "elif":
			if len(conds) == 0 {
				return errf(start, "#elif without #if")
			}
			c, err := st.cond(name, rest, start+1)
			if err != nil {
				return err
			}
			k := len(conds) - 1
			conds[k] = c && !taken[k]
			taken[k] = taken[k] || c
			continue
		case "else":
			if len(conds) == 0 {
				return errf(start, "#else without #if")
			}
			k := len(conds) - 1
			conds[k] = !taken[k]
			taken[k] = true
			continue
		case "endif":
			if len(conds) == 0 {
				return errf(start, "#endif without #if")
			}
			conds, taken = conds[:len(conds)-1], taken[:len(taken)-1]
			continue
		}
		if !active() {
			continue
		}
		switch word {
		case "define":
			mname, val := rest, ""
			if j := strings.IndexAny(rest, " \t("); j >= 0 {
				if rest[j] == '(' {
					return errf(start, "function-like macros are not supported")
				}
				mname, val = rest[:j], rest[j:]
			}
			mt, err := lexRaw(name, val, start+1)
			if err != nil {
				return err
			}
			st.macros[mname] = mt
		case "undef":
			delete(st.macros, rest)
		case "include":
			switch {
			case strings.HasPrefix(rest, "<"):
				h := strings.TrimSuffix(rest[1:], ">")
				if !systemHeaders[h] {
					return errf(start, "cc does not provide <%s>", h)
				}
			case strings.HasPrefix(rest, "\""):
				h := strings.Trim(rest, "\"")
				path := filepath.Join(filepath.Dir(name), h)
				if st.included[path] {
					break
				}
				st.included[path] = true
				b, err := os.ReadFile(path)
				if err != nil {
					return errf(start, "%v", err)
				}
				if err := flush(i + 2); err != nil {
					return err
				}
				if err := st.file(path, string(b)); err != nil {
					return err
				}
			default:
				return errf(start, "bad #include")
			}
		case "error":
			return errf(start, "#error %s", rest)
		}
	}
	if len(conds) > 0 {
		return fmt.Errorf("%s: #if without #endif", name)
	}
	return flush(len(lines) + 1)
}

// cond evaluates the condition of #if or #elif: an integer constant
// expression of macros and defined(name).
func (st *pp) cond(file, expr string, line int) (bool, error) {
	ts, err := lexRaw(file, expr, line)
	if err != nil {
		return false, err
	}
	var out []token
	for i := 0; i < len(ts); i++ {
		t := ts[i]
		if t.kind == tkIdent && t.text == "defined" {
			name := ""
			if i+1 < len(ts) && ts[i+1].text == "(" && i+3 < len(ts) {
				name, i = ts[i+2].text, i+3
			} else if i+1 < len(ts) {
				name, i = ts[i+1].text, i+1
			}
			_, def := st.macros[name]
			v := int64(0)
			if def {
				v = 1
			}
			out = append(out, token{kind: tkInt, text: fmt.Sprint(v), ival: v, line: line})
			continue
		}
		if t.kind == tkIdent {
			if m, ok := st.macros[t.text]; ok && len(m) == 1 && m[0].kind == tkInt {
				out = append(out, m[0])
			} else {
				out = append(out, token{kind: tkInt, text: "0", line: line})
			}
			continue
		}
		out = append(out, t)
	}
	out = append(out, token{kind: tkEOF, line: line})
	p := &parser{file: file, toks: out, tags: map[string]*ctype{}, enums: map[string]int64{}, u: &unit{protos: map[string]*ctype{}}}
	p.typedefs = []map[string]*ctype{{}}
	var v int64
	err = func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				pe, ok := r.(parseError)
				if !ok {
					panic(r)
				}
				err = fmt.Errorf("%s", pe.msg)
			}
		}()
		v = p.constExpr(p.conditional())
		return nil
	}()
	return v != 0, err
}

func lexRaw(file, s string, line int) (toks []token, err error) {
	defer func() {
		for i := range toks {
			toks[i].file = file
		}
	}()
	errf := func(format string, args ...any) error {
		return fmt.Errorf("%s:%d: %s", file, line, fmt.Sprintf(format, args...))
	}
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == '\n':
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r' || c == '\f' || c == '\v':
			i++
		case strings.HasPrefix(s[i:], "//"):
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case strings.HasPrefix(s[i:], "/*"):
			j := strings.Index(s[i+2:], "*/")
			if j < 0 {
				return nil, errf("unterminated comment")
			}
			line += strings.Count(s[i:i+2+j], "\n")
			i += j + 4
		case isIdentStart(c):
			j := i
			for j < len(s) && (isIdentStart(s[j]) || isDigit(s[j])) {
				j++
			}
			toks = append(toks, token{kind: tkIdent, text: s[i:j], line: line})
			i = j
		case isDigit(c) || (c == '.' && i+1 < len(s) && isDigit(s[i+1])):
			j := i
			isFloat := false
			if strings.HasPrefix(s[i:], "0x") || strings.HasPrefix(s[i:], "0X") {
				j += 2
				for j < len(s) && strings.IndexByte("0123456789abcdefABCDEF", s[j]) >= 0 {
					j++
				}
			} else {
				for j < len(s) && (isDigit(s[j]) || s[j] == '.' || s[j] == 'e' || s[j] == 'E' ||
					((s[j] == '+' || s[j] == '-') && (s[j-1] == 'e' || s[j-1] == 'E'))) {
					if !isDigit(s[j]) {
						isFloat = true
					}
					j++
				}
			}
			num := s[i:j]
			k := j
			for k < len(s) && strings.IndexByte("uUlLfF", s[k]) >= 0 {
				k++
			}
			suffix := strings.ToLower(s[j:k])
			t := token{text: s[i:k], line: line}
			if isFloat || strings.Contains(suffix, "f") {
				f, err := strconv.ParseFloat(num, 64)
				if err != nil {
					return nil, errf("bad number %s", num)
				}
				t.kind, t.fval = tkFloat, f
			} else {
				u, err := strconv.ParseUint(num, 0, 64)
				if err != nil {
					// C's octal: a leading 0.
					return nil, errf("bad number %s", num)
				}
				t.kind, t.ival = tkInt, int64(u)
			}
			toks = append(toks, t)
			i = k
		case c == '\'':
			j := i + 1
			v, n, err := unescape(s[j:])
			if err != nil {
				return nil, errf("%v", err)
			}
			j += n
			if j >= len(s) || s[j] != '\'' {
				return nil, errf("bad character literal")
			}
			toks = append(toks, token{kind: tkChar, text: s[i : j+1], ival: int64(int8(v)), line: line})
			i = j + 1
		case c == '"':
			j := i + 1
			var b strings.Builder
			for j < len(s) && s[j] != '"' {
				if s[j] == '\n' {
					return nil, errf("newline in string")
				}
				v, n, err := unescape(s[j:])
				if err != nil {
					return nil, errf("%v", err)
				}
				b.WriteByte(v)
				j += n
			}
			if j >= len(s) {
				return nil, errf("unterminated string")
			}
			toks = append(toks, token{kind: tkString, text: s[i : j+1], str: b.String(), line: line})
			i = j + 1
		default:
			p := ""
			for _, q := range puncts {
				if strings.HasPrefix(s[i:], q) {
					p = q
					break
				}
			}
			if p == "" {
				if strings.IndexByte("+-*/%&|^~!<>=?:;,.()[]{}", c) < 0 {
					return nil, errf("unexpected character %q", c)
				}
				p = s[i : i+1]
			}
			toks = append(toks, token{kind: tkPunct, text: p, line: line})
			i += len(p)
		}
	}
	return toks, nil
}

func isIdentStart(c byte) bool { return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isDigit(c byte) bool      { return c >= '0' && c <= '9' }

// unescape decodes one character of a literal.
func unescape(s string) (byte, int, error) {
	if s == "" {
		return 0, 0, fmt.Errorf("unterminated literal")
	}
	if s[0] != '\\' {
		return s[0], 1, nil
	}
	if len(s) < 2 {
		return 0, 0, fmt.Errorf("bad escape")
	}
	switch s[1] {
	case 'n':
		return '\n', 2, nil
	case 't':
		return '\t', 2, nil
	case 'r':
		return '\r', 2, nil
	case 'a':
		return 7, 2, nil
	case 'b':
		return 8, 2, nil
	case 'f':
		return 12, 2, nil
	case 'v':
		return 11, 2, nil
	case '\\', '\'', '"', '?':
		return s[1], 2, nil
	case 'x':
		j := 2
		v := 0
		for j < len(s) && strings.IndexByte("0123456789abcdefABCDEF", s[j]) >= 0 {
			d, _ := strconv.ParseInt(s[j:j+1], 16, 8)
			v = v*16 + int(d)
			j++
		}
		return byte(v), j, nil
	}
	if s[1] >= '0' && s[1] <= '7' {
		j := 1
		v := 0
		for j < len(s) && j < 4 && s[j] >= '0' && s[j] <= '7' {
			v = v*8 + int(s[j]-'0')
			j++
		}
		return byte(v), j, nil
	}
	return 0, 0, fmt.Errorf("unknown escape \\%c", s[1])
}
