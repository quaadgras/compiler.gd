// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

// ctype.h, in the C locale. A true result is glibc's: the class's bit in
// its table of character classes.

const (
	ctUpper  = 0x100
	ctLower  = 0x200
	ctAlpha  = 0x400
	ctDigit  = 0x800
	ctXdigit = 0x1000
	ctSpace  = 0x2000
	ctPrint  = 0x4000
	ctGraph  = 0x8000
	ctBlank  = 0x1
	ctCntrl  = 0x2
	ctPunct  = 0x4
	ctAlnum  = 0x8
)

var ctypeTab = func() (t [128]uint16) {
	for c := range t {
		var m uint16
		switch {
		case c >= 'A' && c <= 'Z':
			m |= ctUpper | ctAlpha | ctAlnum
		case c >= 'a' && c <= 'z':
			m |= ctLower | ctAlpha | ctAlnum
		case c >= '0' && c <= '9':
			m |= ctDigit | ctAlnum
		}
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' {
			m |= ctXdigit
		}
		if c == ' ' || c >= '\t' && c <= '\r' {
			m |= ctSpace
		}
		if c == ' ' || c == '\t' {
			m |= ctBlank
		}
		if c < ' ' || c == 0x7f {
			m |= ctCntrl
		} else {
			m |= ctPrint
			if c != ' ' {
				m |= ctGraph
				if m&ctAlnum == 0 {
					m |= ctPunct
				}
			}
		}
		t[c] = m
	}
	return
}()

func ctype(c int32, m uint16) int32 {
	if c < 0 || c >= 128 {
		return 0
	}
	return int32(ctypeTab[c] & m)
}

func Isalnum(c int32) int32  { return ctype(c, ctAlnum) }
func Isalpha(c int32) int32  { return ctype(c, ctAlpha) }
func Isblank(c int32) int32  { return ctype(c, ctBlank) }
func Iscntrl(c int32) int32  { return ctype(c, ctCntrl) }
func Isdigit(c int32) int32  { return ctype(c, ctDigit) }
func Isgraph(c int32) int32  { return ctype(c, ctGraph) }
func Islower(c int32) int32  { return ctype(c, ctLower) }
func Isprint(c int32) int32  { return ctype(c, ctPrint) }
func Ispunct(c int32) int32  { return ctype(c, ctPunct) }
func Isspace(c int32) int32  { return ctype(c, ctSpace) }
func Isupper(c int32) int32  { return ctype(c, ctUpper) }
func Isxdigit(c int32) int32 { return ctype(c, ctXdigit) }

func Tolower(c int32) int32 {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

func Toupper(c int32) int32 {
	if c >= 'a' && c <= 'z' {
		return c - ('a' - 'A')
	}
	return c
}
