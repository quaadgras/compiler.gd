// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"fmt"
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

// lex splits src into tokens. Preprocessor lines are handled here: object-
// like #define is expanded, every other directive is ignored.
func lex(file, src string) ([]token, error) {
	var toks []token
	macros := map[string][]token{
		"NULL":         nil, // filled below
		"EXIT_SUCCESS": {{kind: tkInt, text: "0"}},
		"EXIT_FAILURE": {{kind: tkInt, text: "1", ival: 1}},
	}
	null, _ := lexRaw(file, "((void*)0)", 0)
	macros["NULL"] = null
	lines := strings.Split(src, "\n")
	var body strings.Builder
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "#") {
			d := strings.TrimSpace(t[1:])
			if strings.HasPrefix(d, "define") {
				rest := strings.TrimSpace(d[len("define"):])
				name := rest
				val := ""
				if j := strings.IndexAny(rest, " \t("); j >= 0 {
					if rest[j] == '(' {
						return nil, fmt.Errorf("%s:%d: function-like macros are not supported", file, i+1)
					}
					name, val = rest[:j], rest[j:]
				}
				mt, err := lexRaw(file, val, i+1)
				if err != nil {
					return nil, err
				}
				macros[name] = mt
			}
			body.WriteString("\n")
			continue
		}
		body.WriteString(l)
		body.WriteString("\n")
	}
	raw, err := lexRaw(file, body.String(), 1)
	if err != nil {
		return nil, err
	}
	var expand func(ts []token, active map[string]bool)
	expand = func(ts []token, active map[string]bool) {
		for _, t := range ts {
			if t.kind == tkIdent && !active[t.text] {
				if m, ok := macros[t.text]; ok {
					active[t.text] = true
					var mt []token
					for _, x := range m {
						x.line = t.line
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
	expand(raw, map[string]bool{})
	toks = append(toks, token{kind: tkEOF, line: len(lines)})
	return toks, nil
}

func lexRaw(file, s string, line int) ([]token, error) {
	var toks []token
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
