// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"fmt"
	"strings"
	"unicode/utf8"
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

	// Preprocessing tokens, which the preprocessor turns into the above.
	tkPPNum     // a preprocessing number
	tkOther     // a character that is no other token (stray \, @, `...)
	tkFileEnd   // the end of a file the preprocessor reads
	tkPlacemark // an empty macro argument, as an operand of ##
	tkPasteOp   // ## in a macro's body
)

type token struct {
	file string
	kind tokKind
	text string // as written; for tkString, the concatenation's spelling
	str  string // tkString: the encoded bytes of the array, without the 0
	ival int64  // tkInt, tkChar
	fval float64
	line int
	ty   *ctype // tkInt, tkChar, tkFloat; tkString: the element type

	space    bool  // whitespace before it
	bol      bool  // first on its line
	noexpand bool  // names a macro that was disabled when it was read
	pack     int64 // "struct" and "union": #pragma pack's alignment (0: none)
}

func (t token) String() string {
	if t.kind == tkEOF {
		return "end of file"
	}
	return fmt.Sprintf("%q", t.text)
}

// Punctuators, longest first. Digraphs are spelled as what they mean.
var puncts = []string{
	"%:%:", "<<=", ">>=", "...", "->", "++", "--", "<<", ">>", "<=", ">=", "==", "!=",
	"&&", "||", "+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "##", "::",
	"<:", ":>", "<%", "%>", "%:",
}

var digraphs = map[string]string{"<:": "[", ":>": "]", "<%": "{", "%>": "}", "%:": "#", "%:%:": "##"}

// A source is a piece of C to translate: a .c file, or a preamble.
type source struct {
	name string // path, for errors and to find "..." headers
	text string
}

// splice removes the backslash-newlines of src. breaks are the offsets
// in the result where they were, to number lines by.
func splice(src string) (string, []int) {
	if !strings.Contains(src, "\\") {
		return src, nil
	}
	var b strings.Builder
	var breaks []int
	for i := 0; i < len(src); i++ {
		c := src[i]
		if c == '\\' {
			j := i + 1
			for j < len(src) && (src[j] == ' ' || src[j] == '\t') {
				j++
			}
			if j < len(src) && src[j] == '\n' {
				i = j
				breaks = append(breaks, b.Len())
				continue
			}
			if j+1 < len(src) && src[j] == '\r' && src[j+1] == '\n' {
				i = j + 1
				breaks = append(breaks, b.Len())
				continue
			}
		}
		b.WriteByte(c)
	}
	return b.String(), breaks
}

// lexRaw splits s into preprocessing tokens. The first is at line.
func lexRaw(file, s string, line int) (toks []token, err error) {
	s, breaks := splice(s)
	errf := func(format string, args ...any) error {
		return fmt.Errorf("%s:%d: %s", file, line, fmt.Sprintf(format, args...))
	}
	bol, space := true, false
	i := 0
	add := func(kind tokKind, text string) {
		for len(breaks) > 0 && breaks[0] <= i {
			line++
			breaks = breaks[1:]
		}
		toks = append(toks, token{file: file, kind: kind, text: text, line: line, bol: bol, space: space})
		bol, space = false, false
	}
	for i < len(s) {
		c := s[i]
		switch {
		case c == '\n':
			line++
			i++
			bol, space = true, false
			continue
		case c == ' ' || c == '\t' || c == '\r' || c == '\f' || c == '\v':
			i++
			space = true
			continue
		case strings.HasPrefix(s[i:], "//"):
			for i < len(s) && s[i] != '\n' {
				i++
			}
			space = true
			continue
		case strings.HasPrefix(s[i:], "/*"):
			j := strings.Index(s[i+2:], "*/")
			if j < 0 {
				return nil, errf("unterminated comment")
			}
			line += strings.Count(s[i:i+2+j], "\n")
			i += j + 4
			space = true
			continue
		}
		// Literals, with an encoding prefix or none.
		if n := quotePrefix(s[i:]); n >= 0 {
			q := s[i+n]
			j := i + n + 1
			for j < len(s) && s[j] != q {
				if s[j] == '\n' {
					break
				}
				if s[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(s) || s[j] != q {
				// A lone quote: a token of its own, an error only if
				// it survives preprocessing.
				add(tkOther, s[i:i+n+1])
				i += n + 1
				continue
			}
			kind := tkString
			if q == '\'' {
				kind = tkChar
			}
			add(kind, s[i:j+1])
			i = j + 1
			continue
		}
		switch {
		case isIdentStart(c) || c >= 0x80 || c == '\\' && i+1 < len(s) && (s[i+1] == 'u' || s[i+1] == 'U'):
			j := i
			for j < len(s) {
				if isIdentStart(s[j]) || isDigit(s[j]) || s[j] >= 0x80 {
					j++
				} else if s[j] == '\\' && j+1 < len(s) && (s[j+1] == 'u' || s[j+1] == 'U') {
					j += 2
					if j < len(s) && s[j] == '{' {
						for j < len(s) && s[j] != '}' {
							j++
						}
						j++
					}
				} else {
					break
				}
			}
			add(tkIdent, s[i:j])
			i = j
		case isDigit(c) || c == '.' && i+1 < len(s) && isDigit(s[i+1]):
			// A preprocessing number: digits, letters, _, ., ' between
			// digits, and signs after e, E, p, P.
			j := i + 1
		num:
			for j < len(s) {
				d := s[j]
				switch {
				case (d == '+' || d == '-') && strings.IndexByte("eEpP", s[j-1]) >= 0:
					j++
				case isDigit(d) || isIdentStart(d) || d == '.' || d >= 0x80:
					j++
				case d == '\'' && j+1 < len(s) && (isDigit(s[j+1]) || isIdentStart(s[j+1])):
					j += 2
				default:
					break num
				}
			}
			add(tkPPNum, s[i:j])
			i = j
		default:
			p := ""
			for _, q := range puncts {
				if strings.HasPrefix(s[i:], q) {
					p = q
					break
				}
			}
			if p == "" {
				if strings.IndexByte("+-*/%&|^~!<>=?:;,.()[]{}#", c) < 0 {
					add(tkOther, s[i:i+1])
					i++
					continue
				}
				p = s[i : i+1]
			}
			if d, ok := digraphs[p]; ok {
				add(tkPunct, d)
			} else {
				add(tkPunct, p)
			}
			i += len(p)
		}
	}
	return toks, nil
}

// quotePrefix returns the length of the encoding prefix of a character
// constant or string literal at the start of s, or -1 if none starts there.
func quotePrefix(s string) int {
	for _, p := range []string{"u8", "u", "U", "L", ""} {
		if strings.HasPrefix(s, p) && len(s) > len(p) && (s[len(p)] == '"' || s[len(p)] == '\'') {
			return len(p)
		}
	}
	return -1
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}
func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isHex(c byte) bool   { return isDigit(c) || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }

func hexVal(c byte) int {
	switch {
	case isDigit(c):
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	}
	return int(c-'A') + 10
}

// unescape decodes one character of a literal's body: a source character
// (a whole UTF-8 sequence), or an escape. ucn reports a \u or \U escape
// or a multibyte character, whose value is a code point; other escapes
// give the value of one code unit.
func unescape(s string) (v uint32, n int, ucn bool, err error) {
	if s == "" {
		return 0, 0, false, fmt.Errorf("unterminated literal")
	}
	if s[0] != '\\' {
		if s[0] < 0x80 {
			return uint32(s[0]), 1, false, nil
		}
		r, size := utf8.DecodeRuneInString(s)
		if r == utf8.RuneError && size == 1 {
			return uint32(s[0]), 1, false, nil // a raw byte, not UTF-8
		}
		return uint32(r), size, true, nil
	}
	if len(s) < 2 {
		return 0, 0, false, fmt.Errorf("bad escape")
	}
	switch s[1] {
	case 'n':
		return '\n', 2, false, nil
	case 't':
		return '\t', 2, false, nil
	case 'r':
		return '\r', 2, false, nil
	case 'a':
		return 7, 2, false, nil
	case 'b':
		return 8, 2, false, nil
	case 'f':
		return 12, 2, false, nil
	case 'v':
		return 11, 2, false, nil
	case 'e', 'E':
		return 27, 2, false, nil
	case '\\', '\'', '"', '?':
		return uint32(s[1]), 2, false, nil
	case 'x', 'u', 'U', 'o':
		if len(s) > 2 && s[2] == '{' {
			// C2y's delimited escapes: \x{...}, \u{...}, \o{...}.
			j := 3
			var v uint32
			base := uint32(16)
			if s[1] == 'o' {
				base = 8
			}
			for j < len(s) && s[j] != '}' {
				d := uint32(hexVal(s[j]))
				if !isHex(s[j]) || d >= base {
					return 0, 0, false, fmt.Errorf("invalid digit in delimited escape")
				}
				v = v*base + d
				j++
			}
			if j >= len(s) || j == 3 {
				return 0, 0, false, fmt.Errorf("unterminated delimited escape")
			}
			return v, j + 1, s[1] == 'u' || s[1] == 'U', nil
		}
	}
	switch s[1] {
	case 'x':
		j := 2
		var v uint32
		for j < len(s) && isHex(s[j]) {
			v = v<<4 | uint32(hexVal(s[j]))
			j++
		}
		if j == 2 {
			return 0, 0, false, fmt.Errorf("\\x used with no following hex digits")
		}
		return v, j, false, nil
	case 'u', 'U':
		n := 4
		if s[1] == 'U' {
			n = 8
		}
		if len(s) < 2+n {
			return 0, 0, false, fmt.Errorf("incomplete universal character name")
		}
		var v uint32
		for j := 2; j < 2+n; j++ {
			if !isHex(s[j]) {
				return 0, 0, false, fmt.Errorf("incomplete universal character name")
			}
			v = v<<4 | uint32(hexVal(s[j]))
		}
		return v, 2 + n, true, nil
	}
	if s[1] >= '0' && s[1] <= '7' {
		j := 1
		var v uint32
		for j < len(s) && j < 4 && s[j] >= '0' && s[j] <= '7' {
			v = v*8 + uint32(s[j]-'0')
			j++
		}
		return v, j, false, nil
	}
	// An unknown escape: the character, as GCC does.
	return uint32(s[1]), 2, false, nil
}

// identName decodes the universal character names of an identifier, so
// that é and é are the same name.
func identName(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\\' {
			v, n, _, err := unescape(s[i:])
			if err == nil {
				b.WriteRune(rune(v))
				i += n
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
