// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package regex

import (
	"fmt"
	"strings"
	"testing"
)

// run compiles pattern and matches text, giving the positions of the
// whole and the subexpressions, or the error.
func run(pattern string, cflags int32, text string, eflags int32) string {
	re, code := compile(pattern, cflags)
	if code != 0 {
		return fmt.Sprintf("error %d", code)
	}
	m := &matcher{re: re, text: []byte(text), n: len(text),
		notbol: eflags&REG_NOTBOL != 0, noteol: eflags&REG_NOTEOL != 0, exact: !re.root.backrefs}
	caps, ok := m.search(0, re.root.groups)
	if !ok {
		return "nomatch"
	}
	var b strings.Builder
	for i := 0; i < len(caps); i += 2 {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "(%d,%d)", caps[i], caps[i+1])
	}
	return b.String()
}

func TestRegex(t *testing.T) {
	const E, I, N = REG_EXTENDED, REG_ICASE, REG_NEWLINE
	for _, tt := range []struct {
		pat    string
		cflags int32
		text   string
		eflags int32
		want   string
	}{
		{"abc", 0, "xabcy", 0, "(1,4)"},
		{"a*", 0, "baaa", 0, "(0,0)"},
		{"a*", 0, "aaab", 0, "(0,3)"},
		{"a|ab", E, "abc", 0, "(0,2)"},
		{"(a|ab)(c|bcd)(d*)", E, "abcd", 0, "(0,4) (0,2) (2,3) (3,4)"},
		{"(a*)(b|abc)", E, "abc", 0, "(0,3) (0,0) (0,3)"},
		{"(a*)+", E, "b", 0, "(0,0) (0,0)"},
		{"(a*)+", E, "aa", 0, "(0,2) (0,2)"},
		{"(a|b)*", E, "abab", 0, "(0,4) (3,4)"},
		{"(wee|week)(knights|night)", E, "weeknights", 0, "(0,10) (0,3) (3,10)"},
		{"\\(a*\\)\\1", 0, "aaaa", 0, "(0,4) (0,2)"},
		{"\\(a*\\)b\\1", 0, "aabaa", 0, "(0,5) (0,2)"},
		{"\\(.\\)\\1", 0, "abccd", 0, "(2,4) (2,3)"},
		{"(.)\\1", E, "abccd", 0, "(2,4) (2,3)"},
		{"\\(ab*\\)\\{2\\}", 0, "abbab", 0, "(0,5) (3,5)"},
		{"a\\{2,3\\}", 0, "aaaa", 0, "(0,3)"},
		{"a{2,}", E, "aaaa", 0, "(0,4)"},
		{"[[:digit:]]+", E, "ab123c", 0, "(2,5)"},
		{"[^[:alpha:]]", 0, "ab1", 0, "(2,3)"},
		{"ABC", I, "xabc", 0, "(1,4)"},
		{"[a-c]+", E | I, "xABCd", 0, "(1,4)"},
		{"^b", N, "a\nb", 0, "(2,3)"},
		{"^b", 0, "a\nb", 0, "nomatch"},
		{"a$", N, "a\nb", 0, "(0,1)"},
		{"a.b", N, "a\nb", 0, "nomatch"},
		{"a.b", 0, "a\nb", 0, "(0,3)"},
		{"^a", 0, "a", REG_NOTBOL, "nomatch"},
		{"a$", 0, "a", REG_NOTEOL, "nomatch"},
		{"*a", 0, "x*a", 0, "(1,3)"},
		{"\\(*a\\)", 0, "*a", 0, "(0,2) (0,2)"},
		{"a\\|b", 0, "b", 0, "(0,1)"},
		{"a\\+", 0, "aa", 0, "(0,2)"},
		{"\\<foo\\>", 0, "afoo foo", 0, "(5,8)"},
		{"(a)|b", E, "b", 0, "(0,1) (-1,-1)"},
		{"x(a|ab)(bc|c)?", E, "xabc", 0, "(0,4) (1,3) (3,4)"},
		{"a)", E, "a)", 0, "(0,2)"},
		{"(ab|a)(bc|c)", E, "abc", 0, "(0,3) (0,2) (2,3)"},
		{"(a+|b+)*c", E, strings.Repeat("ab", 2000) + "c", 0, "(0,4001) (3999,4000)"},
		{"(a*)*b", E, strings.Repeat("a", 3000), 0, "nomatch"},
		{".*x", 0, strings.Repeat("y", 50000) + "x", 0, "(0,50001)"},
		{"([a-z]+ )*x", E, strings.Repeat("ab ", 3000) + "x", 0, "(0,9001) (8997,9000)"},
		{"(a)*b", E, strings.Repeat("a", 10000) + "b", 0, "(0,10001) (9999,10000)"},
		{"\\(a\\)*\\1", 0, strings.Repeat("a", 200), 0, "(0,200) (198,199)"},
		// Errors.
		{"a\\", 0, "", 0, "error 5"},
		{"\\(a", 0, "", 0, "error 8"},
		{"a\\)", 0, "", 0, "error 8"},
		{"(a", E, "", 0, "error 8"},
		{"[a", 0, "", 0, "error 7"},
		{"[z-a]", 0, "", 0, "error 11"},
		{"[[:foo:]]", 0, "", 0, "error 4"},
		{"\\1", 0, "", 0, "error 6"},
		{"\\(a\\1\\)", 0, "", 0, "error 6"},
		{"a\\{1", 0, "", 0, "error 9"},
		{"a\\{2,1\\}", 0, "", 0, "error 10"},
		{"*a", E, "", 0, "error 13"},
		{"a|*b", E, "", 0, "error 13"},
		{"[[.ab.]]", 0, "", 0, "error 3"},
	} {
		if got := run(tt.pat, tt.cflags, tt.text, tt.eflags); got != tt.want {
			t.Errorf("%q (%d) on %.20q: %s, want %s", tt.pat, tt.cflags, tt.text, got, tt.want)
		}
	}
}
