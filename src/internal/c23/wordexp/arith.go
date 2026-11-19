// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wordexp

import (
	"os"
	"strconv"
	"strings"
)

// arith evaluates a shell's arithmetic expression: C's integer operators
// but assignment, on longs; a name is its variable's value.
func arith(s string) (int64, bool) {
	a := &arithParser{s: s}
	v := a.ternary()
	a.space()
	if a.err || a.i != len(a.s) {
		return 0, false
	}
	return v, true
}

type arithParser struct {
	s   string
	i   int
	err bool
}

func (a *arithParser) space() {
	for a.i < len(a.s) && strings.IndexByte(" \t\n", a.s[a.i]) >= 0 {
		a.i++
	}
}

// accept consumes op if it is next (and not the start of a longer
// operator in not).
func (a *arithParser) accept(op string, not ...string) bool {
	a.space()
	if !strings.HasPrefix(a.s[a.i:], op) {
		return false
	}
	for _, n := range not {
		if strings.HasPrefix(a.s[a.i:], n) {
			return false
		}
	}
	a.i += len(op)
	return true
}

func (a *arithParser) ternary() int64 {
	c := a.binary(0)
	if a.accept("?") {
		x := a.ternary()
		if !a.accept(":") {
			a.err = true
			return 0
		}
		y := a.ternary()
		if c != 0 {
			return x
		}
		return y
	}
	return c
}

// The binary operators by precedence, lowest first, with the longer
// operators each must not be the start of.
var levels = [][]struct {
	op  string
	not []string
}{
	{{"||", nil}},
	{{"&&", nil}},
	{{"|", []string{"||"}}},
	{{"^", nil}},
	{{"&", []string{"&&"}}},
	{{"==", nil}, {"!=", nil}},
	{{"<=", nil}, {">=", nil}, {"<", []string{"<<"}}, {">", []string{">>"}}},
	{{"<<", nil}, {">>", nil}},
	{{"+", nil}, {"-", nil}},
	{{"*", nil}, {"/", nil}, {"%", nil}},
}

func (a *arithParser) binary(level int) int64 {
	if level == len(levels) {
		return a.unary()
	}
	x := a.binary(level + 1)
	for {
		matched := ""
		for _, o := range levels[level] {
			if a.accept(o.op, o.not...) {
				matched = o.op
				break
			}
		}
		if matched == "" || a.err {
			return x
		}
		y := a.binary(level + 1)
		x = apply(a, matched, x, y)
	}
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func apply(a *arithParser, op string, x, y int64) int64 {
	switch op {
	case "||":
		return b2i(x != 0 || y != 0)
	case "&&":
		return b2i(x != 0 && y != 0)
	case "|":
		return x | y
	case "^":
		return x ^ y
	case "&":
		return x & y
	case "==":
		return b2i(x == y)
	case "!=":
		return b2i(x != y)
	case "<=":
		return b2i(x <= y)
	case ">=":
		return b2i(x >= y)
	case "<":
		return b2i(x < y)
	case ">":
		return b2i(x > y)
	case "<<":
		return x << uint64(y&63)
	case ">>":
		return x >> uint64(y&63)
	case "+":
		return x + y
	case "-":
		return x - y
	case "*":
		return x * y
	case "/", "%":
		if y == 0 {
			a.err = true
			return 0
		}
		if op == "/" {
			return x / y
		}
		return x % y
	}
	return 0
}

func (a *arithParser) unary() int64 {
	switch {
	case a.accept("-"):
		return -a.unary()
	case a.accept("+"):
		return a.unary()
	case a.accept("~"):
		return ^a.unary()
	case a.accept("!"):
		return b2i(a.unary() == 0)
	case a.accept("("):
		v := a.ternary()
		if !a.accept(")") {
			a.err = true
		}
		return v
	}
	a.space()
	j := a.i
	for j < len(a.s) && (isName(a.s[j], false)) {
		j++
	}
	tok := a.s[a.i:j]
	a.i = j
	if tok == "" {
		a.err = true
		return 0
	}
	if isName(tok[0], true) {
		v := strings.TrimSpace(os.Getenv(tok))
		if v == "" {
			return 0
		}
		n, ok := number(v)
		if !ok {
			a.err = true
		}
		return n
	}
	n, ok := number(tok)
	if !ok {
		a.err = true
	}
	return n
}

// number parses a C integer constant: decimal, octal or hexadecimal.
func number(s string) (int64, bool) {
	neg := false
	if s != "" && (s[0] == '-' || s[0] == '+') {
		neg = s[0] == '-'
		s = s[1:]
	}
	base := 10
	switch {
	case len(s) > 2 && (s[:2] == "0x" || s[:2] == "0X"):
		base, s = 16, s[2:]
	case len(s) > 1 && s[0] == '0':
		base, s = 8, s[1:]
	}
	u, err := strconv.ParseUint(s, base, 64)
	if err != nil {
		return 0, false
	}
	if neg {
		return -int64(u), true
	}
	return int64(u), true
}
