// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package regex is regex.h: POSIX's regular expressions, basic (BRE)
// and extended (ERE), with glibc's GNU operators (\| \+ \? in BREs,
// \w \W \s \S \b \B \< \> \` \'), back-references in both, and POSIX's
// leftmost-longest matching, each subexpression from the left matching
// the longest it can (parse.go, match.go). The locale is C's: a
// character is a byte.
package regex

import (
	c23 "internal/c23"
)

// regcomp's flags.
const (
	REG_EXTENDED = 1
	REG_ICASE    = 2
	REG_NEWLINE  = 4
	REG_NOSUB    = 8
)

// regexec's flags.
const (
	REG_NOTBOL   = 1
	REG_NOTEOL   = 2
	REG_STARTEND = 4
)

// The errors, glibc's.
const (
	REG_NOERROR  = 0
	REG_NOMATCH  = 1
	REG_BADPAT   = 2
	REG_ECOLLATE = 3
	REG_ECTYPE   = 4
	REG_EESCAPE  = 5
	REG_ESUBREG  = 6
	REG_EBRACK   = 7
	REG_EPAREN   = 8
	REG_EBRACE   = 9
	REG_BADBR    = 10
	REG_ERANGE   = 11
	REG_ESPACE   = 12
	REG_BADRPT   = 13
	REG_EEND     = 14
	REG_ESIZE    = 15
	REG_ERPAREN  = 16
)

// RE_DUP_MAX is the most an interval counts, glibc's.
const RE_DUP_MAX = 0x7fff

// regexT is what a regex_t refers to: the compiled expression.
type regexT struct {
	root    *node
	nsub    int
	icase   bool
	newline bool
	nosub   bool
}

// regex_t is { size_t re_nsub; void *__opaque; ... }: the C sees
// re_nsub, and __opaque is a regexT.
func opaque(preg *c23.Void) *c23.Void { return c23.Add(preg, int(c23.LongSize)) }

// regexOf returns preg's regexT, or nil if regcomp did not make it (or
// regfree freed it): __opaque is the pointer to a c23.Handle of it.
func regexOf(preg *c23.Void) *regexT {
	re, _ := c23.HandleOf[*regexT](c23.LoadPtr(opaque(preg)))
	return re
}

// Regcomp is regcomp.
func Regcomp(preg, pattern *c23.Void, cflags int32) int32 {
	re, code := compile(c23.GoString(pattern), cflags)
	if code != 0 {
		c23.StorePtr(opaque(preg), (*c23.Void)(nil))
		return code
	}
	c23.StoreLong(preg, int64(re.nsub))
	c23.StorePtr(opaque(preg), c23.NewHandle(re))
	return 0
}

// Regfree is regfree.
func Regfree(preg *c23.Void) {
	c23.StorePtr(opaque(preg), (*c23.Void)(nil))
}

// Regexec is regexec. With REG_STARTEND (glibc's), the string is
// string[pmatch[0].rm_so:pmatch[0].rm_eo], NULs included, and offsets
// are string's.
func Regexec(preg, str *c23.Void, nmatch uint64, pmatch *c23.Void, eflags int32) int32 {
	re := regexOf(preg)
	if re == nil {
		return REG_BADPAT
	}
	if re.nosub {
		nmatch = 0
	}
	var text []byte
	start := 0
	if eflags&REG_STARTEND != 0 {
		so, eo := int(int32(c23.Load32(pmatch))), int(int32(c23.Load32(c23.Add(pmatch, 4))))
		if so < 0 || eo < so {
			return REG_BADPAT
		}
		text = c23.BytesAt(str, uintptr(eo))
		start = so
	} else {
		text = []byte(c23.GoString(str))
	}
	m := &matcher{
		re:     re,
		text:   text,
		n:      len(text),
		notbol: eflags&REG_NOTBOL != 0,
		noteol: eflags&REG_NOTEOL != 0,
		exact:  !re.root.backrefs,
	}
	caps, ok := m.search(start, nmatch > 1 && re.root.groups)
	if !ok {
		return REG_NOMATCH
	}
	for i := 0; uint64(i) < nmatch; i++ {
		so, eo := -1, -1
		if 2*i < len(caps) {
			so, eo = caps[2*i], caps[2*i+1]
		}
		p := c23.Add(pmatch, 8*i)
		c23.Store32(p, uint32(int32(so)))
		c23.Store32(c23.Add(p, 4), uint32(int32(eo)))
	}
	return 0
}

// messages are glibc's.
var messages = [...]string{
	REG_NOERROR:  "Success",
	REG_NOMATCH:  "No match",
	REG_BADPAT:   "Invalid regular expression",
	REG_ECOLLATE: "Invalid collation character",
	REG_ECTYPE:   "Invalid character class name",
	REG_EESCAPE:  "Trailing backslash",
	REG_ESUBREG:  "Invalid back reference",
	REG_EBRACK:   "Unmatched [, [^, [:, [., or [=",
	REG_EPAREN:   "Unmatched ( or \\(",
	REG_EBRACE:   "Unmatched \\{",
	REG_BADBR:    "Invalid content of \\{\\}",
	REG_ERANGE:   "Invalid range end",
	REG_ESPACE:   "Memory exhausted",
	REG_BADRPT:   "Invalid preceding regular expression",
	REG_EEND:     "Premature end of regular expression",
	REG_ESIZE:    "Regular expression too big",
	REG_ERPAREN:  "Unmatched ) or \\)",
}

// Regerror is regerror: it writes errcode's message to errbuf (as much
// of it as errbuf_size holds, with a NUL) and returns the size the whole
// takes. An unknown code is "Unknown error" (glibc aborts).
func Regerror(errcode int32, preg, errbuf *c23.Void, errbufSize uint64) uint64 {
	msg := "Unknown error"
	if errcode >= 0 && int(errcode) < len(messages) {
		msg = messages[errcode]
	}
	if errbufSize > 0 {
		n := min(uint64(len(msg)), errbufSize-1)
		for i := uint64(0); i < n; i++ {
			c23.Store8(c23.Add(errbuf, int(i)), msg[i])
		}
		c23.Store8(c23.Add(errbuf, int(n)), 0)
	}
	return uint64(len(msg)) + 1
}
