// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"strings"
)

// fnmatch.h and glob.h.

const (
	FNM_PATHNAME    = 0x1
	FNM_NOESCAPE    = 0x2
	FNM_PERIOD      = 0x4
	FNM_LEADING_DIR = 0x8
	FNM_CASEFOLD    = 0x10
)

func lowerByte(c byte) byte { return byte(LowerASCII(c)) }

func upperASCII(c byte) byte {
	if 'a' <= c && c <= 'z' {
		return c - ('a' - 'A')
	}
	return c
}

// fnmatch matches s with the pattern p: *, ? and bracket expressions,
// with \ quoting the next byte unless FNM_NOESCAPE. With FNM_PATHNAME, a /
// is matched only by a / in p; with FNM_PERIOD, a leading . (at the
// start, or after a / with FNM_PATHNAME) only by a . in p.
func Fnmatch(p, s string, flags int32) bool {
	pathname := flags&FNM_PATHNAME != 0
	noescape := flags&FNM_NOESCAPE != 0
	fold := flags&FNM_CASEFOLD != 0
	leadingPeriod := func(i int) bool {
		return flags&FNM_PERIOD != 0 && s[i] == '.' && (i == 0 || pathname && s[i-1] == '/')
	}
	// special reports whether s[i] is one a wildcard cannot match.
	special := func(i int) bool { return pathname && s[i] == '/' || leadingPeriod(i) }
	eq := func(a, b byte) bool { return a == b || fold && lowerByte(a) == lowerByte(b) }

	px, sx := 0, 0
	starPx, starSx := -1, -1 // where to go back to: the last *, and where it would match on
	for px < len(p) || sx < len(s) {
		if px < len(p) {
			switch c := p[px]; c {
			case '*':
				for px < len(p) && p[px] == '*' {
					px++
				}
				starPx, starSx = px, sx
				continue
			case '?':
				if sx < len(s) && !special(sx) {
					px++
					sx++
					continue
				}
			case '[':
				if sx < len(s) {
					if end, ok := matchBracket(p, px, s[sx], noescape, fold); end > 0 {
						if ok && !special(sx) {
							px = end
							sx++
							continue
						}
						break
					}
				}
				// An unterminated bracket is a literal [.
				if sx < len(s) && s[sx] == '[' && end0(p, px, noescape) {
					px++
					sx++
					continue
				}
			default:
				if c == '\\' && !noescape && px+1 < len(p) {
					c = p[px+1]
					if sx < len(s) && eq(c, s[sx]) {
						px += 2
						sx++
						continue
					}
					break
				}
				if sx < len(s) && eq(c, s[sx]) {
					px++
					sx++
					continue
				}
			}
		} else if flags&FNM_LEADING_DIR != 0 && s[sx] == '/' {
			return true
		}
		// A mismatch: the last * matches one more byte, if it can.
		if starSx < 0 || starSx >= len(s) || special(starSx) {
			return false
		}
		starSx++
		px, sx = starPx, starSx
	}
	return true
}

// end0 reports whether the bracket at p[i] has no end, so that it is a
// literal [.
func end0(p string, i int, noescape bool) bool {
	end, _ := matchBracket(p, i, 0, noescape, false)
	return end < 0
}

// charClass reports whether c is in the class named name (alpha, digit
// and the others of ctype.h), in the C locale.
func charClass(name string, c byte) (in, ok bool) {
	switch name {
	case "alnum":
		return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9', true
	case "alpha":
		return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z', true
	case "blank":
		return c == ' ' || c == '\t', true
	case "cntrl":
		return c < 32 || c == 127, true
	case "digit":
		return '0' <= c && c <= '9', true
	case "graph":
		return c > 32 && c < 127, true
	case "lower":
		return 'a' <= c && c <= 'z', true
	case "print":
		return c >= 32 && c < 127, true
	case "punct":
		return c > 32 && c < 127 && !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'), true
	case "space":
		return c == ' ' || c >= '\t' && c <= '\r', true
	case "upper":
		return 'A' <= c && c <= 'Z', true
	case "xdigit":
		return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F', true
	}
	return false, false
}

// matchBracket matches c with the bracket expression at p[i] ('['): it
// returns the index after it, or -1 if it has no end, and whether c is
// in it.
func matchBracket(p string, i int, c byte, noescape, fold bool) (int, bool) {
	i++
	neg := false
	if i < len(p) && (p[i] == '!' || p[i] == '^') {
		neg = true
		i++
	}
	match := false
	inRange := func(lo, hi byte) bool {
		if lo <= c && c <= hi {
			return true
		}
		if fold {
			l, u := lowerByte(c), upperASCII(c)
			return lo <= l && l <= hi || lo <= u && u <= hi
		}
		return false
	}
	first := true
	for {
		if i >= len(p) {
			return -1, false
		}
		if p[i] == ']' && !first {
			i++
			break
		}
		first = false
		var lo byte
		if p[i] == '[' && i+1 < len(p) && (p[i+1] == ':' || p[i+1] == '=' || p[i+1] == '.') {
			kind := p[i+1]
			end := strings.Index(p[i+2:], string(kind)+"]")
			if end < 0 {
				return -1, false
			}
			name := p[i+2 : i+2+end]
			i += 2 + end + 2
			if kind == ':' {
				in, ok := charClass(name, c)
				if !ok {
					return -1, false
				}
				if !in && fold && (name == "upper" || name == "lower") {
					in, _ = charClass(name, lowerByte(c))
					if !in {
						in, _ = charClass(name, upperASCII(c))
					}
				}
				match = match || in
				continue
			}
			if len(name) != 1 {
				return -1, false
			}
			lo = name[0]
		} else {
			if p[i] == '\\' && !noescape && i+1 < len(p) {
				i++
			}
			lo = p[i]
			i++
		}
		hi := lo
		if i+1 < len(p) && p[i] == '-' && p[i+1] != ']' {
			i++
			if p[i] == '\\' && !noescape && i+1 < len(p) {
				i++
			}
			hi = p[i]
			i++
		}
		match = match || inRange(lo, hi)
	}
	return i, match != neg
}

// glob.h

const (
	GLOB_NOSPACE = 1
)
