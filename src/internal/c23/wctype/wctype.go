// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wctype

import (
	c23 "internal/c23"
	"unicode"
)

func wclass(c uint32, f func(rune) bool) int32 {
	if c > unicode.MaxRune {
		return 0
	}
	return c23.B2I(f(rune(c)))
}

func iswalpha(r rune) bool { return unicode.IsLetter(r) }

func iswdigit(r rune) bool { return r >= '0' && r <= '9' }

func iswalnum(r rune) bool { return iswalpha(r) || iswdigit(r) }

func iswblank(r rune) bool { return r == '\t' || r == ' ' || unicode.Is(unicode.Zs, r) && !nbsp(r) }

func iswcntrl(r rune) bool { return unicode.IsControl(r) || r == 0x2028 || r == 0x2029 }

func iswspace(r rune) bool { return unicode.IsSpace(r) && !nbsp(r) || r == 0x2028 || r == 0x2029 }

func iswprint(r rune) bool { return unicode.IsPrint(r) || unicode.Is(unicode.Zs, r) }

func iswgraph(r rune) bool { return iswprint(r) && !iswspace(r) && r != ' ' }

func iswpunct(r rune) bool { return iswgraph(r) && !iswalnum(r) }

func iswlower(r rune) bool { return unicode.IsLower(r) }

func iswupper(r rune) bool { return unicode.IsUpper(r) }

func iswxdigit(r rune) bool {
	return iswdigit(r) || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F'
}

// nbsp reports whether r is a space that does not break, which is not
// white space (as glibc has it).
func nbsp(r rune) bool { return r == 0xa0 || r == 0x2007 || r == 0x202f }

func Iswalnum(c uint32) int32 { return wclass(c, iswalnum) }

func Iswalpha(c uint32) int32 { return wclass(c, iswalpha) }

func Iswblank(c uint32) int32 { return wclass(c, iswblank) }

func Iswcntrl(c uint32) int32 { return wclass(c, iswcntrl) }

func Iswdigit(c uint32) int32 { return wclass(c, iswdigit) }

func Iswgraph(c uint32) int32 { return wclass(c, iswgraph) }

func Iswlower(c uint32) int32 { return wclass(c, iswlower) }

func Iswprint(c uint32) int32 { return wclass(c, iswprint) }

func Iswpunct(c uint32) int32 { return wclass(c, iswpunct) }

func Iswspace(c uint32) int32 { return wclass(c, iswspace) }

func Iswupper(c uint32) int32 { return wclass(c, iswupper) }

func Iswxdigit(c uint32) int32 { return wclass(c, iswxdigit) }

var wctypes = []struct {
	name string
	f    func(rune) bool
}{
	{"", nil},
	{"alnum", iswalnum}, {"alpha", iswalpha}, {"blank", iswblank}, {"cntrl", iswcntrl},
	{"digit", iswdigit}, {"graph", iswgraph}, {"lower", iswlower}, {"print", iswprint},
	{"punct", iswpunct}, {"space", iswspace}, {"upper", iswupper}, {"xdigit", iswxdigit},
}

func Wctype(name *c23.Void) uint32 {
	n := c23.GoString(name)
	for i, t := range wctypes {
		if i > 0 && t.name == n {
			return uint32(i)
		}
	}
	return 0
}

func Iswctype(c, desc uint32) int32 {
	if desc == 0 || desc >= uint32(len(wctypes)) {
		return 0
	}
	return wclass(c, wctypes[desc].f)
}

func wcase(c uint32, f func(rune) rune) uint32 {
	if c > unicode.MaxRune {
		return c
	}
	return uint32(f(rune(c)))
}

func Towlower(c uint32) uint32 { return wcase(c, unicode.ToLower) }

func Towupper(c uint32) uint32 { return wcase(c, unicode.ToUpper) }

func Wctrans(name *c23.Void) uint32 {
	switch c23.GoString(name) {
	case "tolower":
		return 1
	case "toupper":
		return 2
	}
	return 0
}

func Towctrans(c, desc uint32) uint32 {
	switch desc {
	case 1:
		return Towlower(c)
	case 2:
		return Towupper(c)
	}
	return c
}
