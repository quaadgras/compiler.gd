// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package wordexp is wordexp.h's functions for cmd/cc: the shell's word
// expansions, done in Go as glibc does them in C. Command substitution
// runs /bin/sh -c, where the system has one (WRDE_CMDSUB where it
// cannot run).
package wordexp

import (
	"bytes"
	c23 "internal/c23"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"unsafe"
)

const (
	WRDE_DOOFFS  = 1
	WRDE_APPEND  = 2
	WRDE_NOCMD   = 4
	WRDE_REUSE   = 8
	WRDE_SHOWERR = 16
	WRDE_UNDEF   = 32

	WRDE_NOSPACE = 1
	WRDE_BADCHAR = 2
	WRDE_BADVAL  = 3
	WRDE_CMDSUB  = 4
	WRDE_SYNTAX  = 5
)

// A qbyte is a byte of a word and whether it was quoted: a quoted byte
// is neither split into fields nor a pattern's.
type qbyte struct {
	b byte
	q bool
}

func unq(s string, q bool) []qbyte {
	r := make([]qbyte, len(s))
	for i := range len(s) {
		r[i] = qbyte{s[i], q}
	}
	return r
}

func plain(w []qbyte) string {
	b := make([]byte, len(w))
	for i, c := range w {
		b[i] = c.b
	}
	return string(b)
}

// pattern is w as an fnmatch pattern: its quoted special bytes escaped.
func pattern(w []qbyte) string {
	var b []byte
	for _, c := range w {
		if c.q && strings.IndexByte(`*?[]\`, c.b) >= 0 {
			b = append(b, '\\')
		}
		b = append(b, c.b)
	}
	return string(b)
}

// A sink receives what the parser makes of a word.
type sink interface {
	lit(c qbyte)       // a byte of the word as written
	exp(v []qbyte)     // what an expansion gave
	mark()             // quotes: the word exists even if empty
	blank(b byte)      // an unquoted blank between words
	atWordStart() bool // whether a tilde here begins a tilde-prefix
}

// fields makes the words of wordexp: the input's words, and the fields
// unquoted expansions split into on IFS.
type fields struct {
	ifs     string
	words   [][]qbyte
	cur     []qbyte
	has     bool // cur is a word, even if empty
	wsSplit bool // the last byte of an expansion ended a field at IFS white space
}

func (f *fields) lit(c qbyte) {
	f.cur = append(f.cur, c)
	f.has, f.wsSplit = true, false
}

func (f *fields) mark() { f.has, f.wsSplit = true, false }

func (f *fields) end() {
	if f.has {
		f.words = append(f.words, f.cur)
	}
	f.cur, f.has = nil, false
}

func (f *fields) blank(byte) {
	f.end()
	f.wsSplit = false
}

func (f *fields) atWordStart() bool { return !f.has && len(f.cur) == 0 }

func (f *fields) exp(v []qbyte) {
	for _, c := range v {
		if c.q || strings.IndexByte(f.ifs, c.b) < 0 {
			f.lit(c)
			continue
		}
		if c.b == ' ' || c.b == '\t' || c.b == '\n' {
			if f.has {
				f.end()
				f.wsSplit = true
			}
			continue
		}
		if f.wsSplit {
			f.wsSplit = false
			continue
		}
		f.words = append(f.words, f.cur)
		f.cur, f.has = nil, false
	}
}

// str makes one string: a parameter expansion's word, an arithmetic
// expression.
type str struct{ w []qbyte }

func (s *str) lit(c qbyte)       { s.w = append(s.w, c) }
func (s *str) exp(v []qbyte)     { s.w = append(s.w, v...) }
func (s *str) mark()             {}
func (s *str) blank(b byte)      { s.w = append(s.w, qbyte{b, false}) }
func (s *str) atWordStart() bool { return len(s.w) == 0 }

// errWord is an error of wordexp's.
type errWord int32

type parser struct {
	flags int32
}

// quote returns v, quoted if q.
func quote(v string, q bool) []qbyte { return unq(v, q) }

// skipQuoted returns the index just past the construct that starts at
// s[i] (a quote, $( or ${, a backquote), or -1 if it does not end.
func skipQuoted(s string, i int) int {
	switch s[i] {
	case '\'':
		j := strings.IndexByte(s[i+1:], '\'')
		if j < 0 {
			return -1
		}
		return i + 1 + j + 1
	case '"':
		for j := i + 1; j < len(s); j++ {
			switch s[j] {
			case '\\':
				j++
			case '"':
				return j + 1
			case '`':
				k := skipQuoted(s, j)
				if k < 0 {
					return -1
				}
				j = k - 1
			case '$':
				if j+1 < len(s) && (s[j+1] == '(' || s[j+1] == '{') {
					k := skipQuoted(s, j)
					if k < 0 {
						return -1
					}
					j = k - 1
				}
			}
		}
		return -1
	case '`':
		for j := i + 1; j < len(s); j++ {
			switch s[j] {
			case '\\':
				j++
			case '`':
				return j + 1
			}
		}
		return -1
	case '$':
		open, close := s[i+1], byte(')')
		if open == '{' {
			close = '}'
		}
		depth := 0
		for j := i + 1; j < len(s); j++ {
			switch c := s[j]; c {
			case '\\':
				j++
			case '\'', '"', '`':
				k := skipQuoted(s, j)
				if k < 0 {
					return -1
				}
				j = k - 1
			case '$':
				if j+1 < len(s) && (s[j+1] == '(' || s[j+1] == '{') {
					k := skipQuoted(s, j)
					if k < 0 {
						return -1
					}
					j = k - 1
				}
			case open:
				depth++
			case close:
				depth--
				if depth == 0 {
					return j + 1
				}
			}
		}
		return -1
	}
	return i + 1
}

// parse expands s into sk; in double quotes (dq), everything is quoted.
func (p *parser) parse(s string, sk sink, dq bool) error {
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\\':
			if i+1 >= len(s) {
				if dq {
					sk.lit(qbyte{c, true})
					i++
					continue
				}
				return errWord(WRDE_SYNTAX)
			}
			n := s[i+1]
			switch {
			case n == '\n':
			case dq && strings.IndexByte("$`\"\\", n) < 0:
				sk.lit(qbyte{'\\', true})
				sk.lit(qbyte{n, true})
			default:
				sk.lit(qbyte{n, true})
			}
			i += 2
		case c == '\'' && !dq:
			j := skipQuoted(s, i)
			if j < 0 {
				return errWord(WRDE_SYNTAX)
			}
			sk.mark()
			for _, b := range []byte(s[i+1 : j-1]) {
				sk.lit(qbyte{b, true})
			}
			i = j
		case c == '"' && !dq:
			j := skipQuoted(s, i)
			if j < 0 {
				return errWord(WRDE_SYNTAX)
			}
			sk.mark()
			if err := p.parse(s[i+1:j-1], sk, true); err != nil {
				return err
			}
			i = j
		case c == '`':
			j := skipQuoted(s, i)
			if j < 0 {
				return errWord(WRDE_SYNTAX)
			}
			// In backquotes, \ quotes only $, ` and \ (and " in
			// double quotes).
			var cmd []byte
			in := s[i+1 : j-1]
			for k := 0; k < len(in); k++ {
				if in[k] == '\\' && k+1 < len(in) && (strings.IndexByte("$`\\", in[k+1]) >= 0 || dq && in[k+1] == '"') {
					k++
				}
				cmd = append(cmd, in[k])
			}
			out, err := p.command(string(cmd))
			if err != nil {
				return err
			}
			sk.exp(quote(out, dq))
			i = j
		case c == '$':
			n, err := p.dollar(s, i, sk, dq)
			if err != nil {
				return err
			}
			i = n
		case !dq && (c == ' ' || c == '\t'):
			sk.blank(c)
			i++
		case !dq && strings.IndexByte("\n|&;<>(){}", c) >= 0:
			return errWord(WRDE_BADCHAR)
		case c == '~' && !dq && sk.atWordStart():
			j := i + 1
			for j < len(s) && s[j] != '/' && s[j] != ' ' && s[j] != '\t' && strings.IndexByte("\\'\"$`\n|&;<>(){}", s[j]) < 0 {
				j++
			}
			if home, ok := tilde(s[i+1 : j]); ok {
				sk.exp(quote(home, true))
				sk.mark()
				i = j
				continue
			}
			sk.lit(qbyte{c, false})
			i++
		default:
			sk.lit(qbyte{c, dq})
			i++
		}
	}
	return nil
}

// tilde is the home directory of a tilde-prefix's user: the program's
// (HOME, or the user database's) for an empty one or its own name; the
// others' are not known.
func tilde(user string) (string, bool) {
	if user == "" {
		if h, ok := os.LookupEnv("HOME"); ok {
			return h, true
		}
	}
	if user == "" || user == os.Getenv("USER") || user == os.Getenv("LOGNAME") {
		if h, err := os.UserHomeDir(); err == nil {
			return h, true
		}
	}
	return "", false
}

func isName(c byte, first bool) bool {
	return c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || !first && '0' <= c && c <= '9'
}

// lookup returns the value of a parameter: a variable of the environment
// or a special parameter (there are no positional ones).
func lookup(name string) (string, bool) {
	switch name {
	case "$":
		return strconv.Itoa(os.Getpid()), true
	case "#", "?":
		return "0", true
	case "@", "*", "-":
		return "", true
	case "0":
		if len(os.Args) > 0 {
			return os.Args[0], true
		}
		return "", true
	case "!":
		return "", false
	}
	return os.LookupEnv(name)
}

// dollar expands the $ expansion at s[i] into sk, and returns the index
// after it.
func (p *parser) dollar(s string, i int, sk sink, dq bool) (int, error) {
	if i+1 >= len(s) {
		sk.lit(qbyte{'$', dq})
		return i + 1, nil
	}
	switch c := s[i+1]; {
	case c == '(' && i+2 < len(s) && s[i+2] == '(':
		// $((arithmetic)), if the expression ends with )).
		if j := skipQuoted(s, i); j > 0 && s[j-2] == ')' {
			inner := s[i+3 : j-2]
			var st str
			if err := p.parse(inner, &st, true); err != nil {
				return 0, err
			}
			v, ok := arith(plain(st.w))
			if !ok {
				return 0, errWord(WRDE_SYNTAX)
			}
			sk.exp(quote(strconv.FormatInt(v, 10), dq))
			return j, nil
		}
		fallthrough
	case c == '(':
		j := skipQuoted(s, i)
		if j < 0 {
			return 0, errWord(WRDE_SYNTAX)
		}
		out, err := p.command(s[i+2 : j-1])
		if err != nil {
			return 0, err
		}
		sk.exp(quote(out, dq))
		return j, nil
	case c == '{':
		j := skipQuoted(s, i)
		if j < 0 {
			return 0, errWord(WRDE_SYNTAX)
		}
		v, err := p.brace(s[i+2:j-1], dq)
		if err != nil {
			return 0, err
		}
		sk.exp(v)
		return j, nil
	case isName(c, true):
		j := i + 1
		for j < len(s) && isName(s[j], false) {
			j++
		}
		v, err := p.value(s[i+1:j], dq)
		if err != nil {
			return 0, err
		}
		sk.exp(v)
		return j, nil
	case '0' <= c && c <= '9' || strings.IndexByte("$#?@*-!", c) >= 0:
		v, err := p.value(s[i+1:i+2], dq)
		if err != nil {
			return 0, err
		}
		sk.exp(v)
		return i + 2, nil
	}
	sk.lit(qbyte{'$', dq})
	return i + 1, nil
}

// value is the value of the parameter name: WRDE_BADVAL if it is not set
// and WRDE_UNDEF.
func (p *parser) value(name string, dq bool) ([]qbyte, error) {
	v, ok := lookup(name)
	if !ok && p.flags&WRDE_UNDEF != 0 {
		return nil, errWord(WRDE_BADVAL)
	}
	return quote(v, dq), nil
}

// brace expands ${in}.
func (p *parser) brace(in string, dq bool) ([]qbyte, error) {
	length := false
	if len(in) > 1 && in[0] == '#' {
		length = true
		in = in[1:]
	}
	n := 0
	switch {
	case in == "":
		return nil, errWord(WRDE_SYNTAX)
	case isName(in[0], true):
		for n < len(in) && isName(in[n], false) {
			n++
		}
	case '0' <= in[0] && in[0] <= '9':
		for n < len(in) && '0' <= in[n] && in[n] <= '9' {
			n++
		}
	case strings.IndexByte("$#?@*-!", in[0]) >= 0:
		n = 1
	default:
		return nil, errWord(WRDE_SYNTAX)
	}
	name, rest := in[:n], in[n:]
	v, set := lookup(name)
	if length {
		if rest != "" {
			return nil, errWord(WRDE_SYNTAX)
		}
		if !set && p.flags&WRDE_UNDEF != 0 {
			return nil, errWord(WRDE_BADVAL)
		}
		return quote(strconv.Itoa(len(v)), dq), nil
	}
	if rest == "" {
		return p.value(name, dq)
	}
	colon := rest[0] == ':'
	if colon {
		rest = rest[1:]
	}
	if rest == "" {
		return nil, errWord(WRDE_SYNTAX)
	}
	op := rest[:1]
	if !colon && len(rest) > 1 && (rest[:2] == "%%" || rest[:2] == "##") {
		op = rest[:2]
	}
	word := rest[len(op):]
	if colon && strings.IndexByte("-=?+", op[0]) < 0 {
		return nil, errWord(WRDE_SYNTAX)
	}
	// expand expands the word of the operator.
	expand := func() ([]qbyte, error) {
		var st str
		err := p.parse(word, &st, dq)
		return st.w, err
	}
	null := !set || colon && v == ""
	switch op {
	case "-":
		if null {
			return expand()
		}
	case "=":
		if null {
			w, err := expand()
			if err != nil {
				return nil, err
			}
			os.Setenv(name, plain(w))
			return quote(plain(w), dq), nil
		}
	case "?":
		if null {
			w, err := expand()
			if err != nil {
				return nil, err
			}
			msg := plain(w)
			if msg == "" {
				msg = "parameter null or not set"
			}
			c23.FileOf(c23.Stdio(2)).Write([]byte(name + ": " + msg + "\n"))
			return nil, errWord(WRDE_BADVAL)
		}
	case "+":
		if null {
			return nil, nil
		}
		return expand()
	case "%", "%%", "#", "##":
		if !set && p.flags&WRDE_UNDEF != 0 {
			return nil, errWord(WRDE_BADVAL)
		}
		w, err := expand()
		if err != nil {
			return nil, err
		}
		return quote(trim(v, pattern(w), op), dq), nil
	default:
		return nil, errWord(WRDE_SYNTAX)
	}
	if !set && p.flags&WRDE_UNDEF != 0 {
		return nil, errWord(WRDE_BADVAL)
	}
	return quote(v, dq), nil
}

// trim removes from v the suffix (%, %% for the longest) or prefix (#,
// ##) that pat matches.
func trim(v, pat, op string) string {
	match := func(s string) bool { return c23.Fnmatch(pat, s, 0) }
	switch op {
	case "%":
		for i := len(v); i >= 0; i-- {
			if match(v[i:]) {
				return v[:i]
			}
		}
	case "%%":
		for i := 0; i <= len(v); i++ {
			if match(v[i:]) {
				return v[:i]
			}
		}
	case "#":
		for i := 0; i <= len(v); i++ {
			if match(v[:i]) {
				return v[i:]
			}
		}
	case "##":
		for i := len(v); i >= 0; i-- {
			if match(v[:i]) {
				return v[i:]
			}
		}
	}
	return v
}

// command runs a command substitution's command with /bin/sh: its
// output, without its trailing newlines.
func (p *parser) command(cmd string) (string, error) {
	if p.flags&WRDE_NOCMD != 0 {
		return "", errWord(WRDE_CMDSUB)
	}
	c23.Fflush((*c23.Void)(nil))
	sh := exec.Command("/bin/sh", "-c", cmd)
	sh.Stdin = os.Stdin
	if p.flags&WRDE_SHOWERR != 0 {
		sh.Stderr = os.Stderr
	}
	out, err := sh.Output()
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			// No shell to run it.
			return "", errWord(WRDE_CMDSUB)
		}
	}
	return string(bytes.TrimRight(out, "\n")), nil
}

func (e errWord) Error() string { return "wordexp error " + strconv.Itoa(int(e)) }

// expandWords is wordexp's expansion of words into fields, each globbed.
func expandWords(words string, flags int32) ([]string, int32) {
	ifs, ok := os.LookupEnv("IFS")
	if !ok {
		ifs = " \t\n"
	}
	f := &fields{ifs: ifs}
	p := &parser{flags: flags}
	if err := p.parse(words, f, false); err != nil {
		return nil, int32(err.(errWord))
	}
	f.end()
	var out []string
	for _, w := range f.words {
		glob := false
		for _, c := range w {
			if !c.q && (c.b == '*' || c.b == '?' || c.b == '[') {
				glob = true
				break
			}
		}
		if glob {
			if m := globPattern(pattern(w)); len(m) > 0 {
				out = append(out, m...)
				continue
			}
		}
		out = append(out, plain(w))
	}
	return out, 0
}

// hasMeta reports whether a pattern's element has a wildcard that is
// not escaped.
func hasMeta(p string) bool {
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '\\':
			i++
		case '*', '?', '[':
			return true
		}
	}
	return false
}

func unescape(p string) string {
	var b []byte
	for i := 0; i < len(p); i++ {
		if p[i] == '\\' && i+1 < len(p) {
			i++
		}
		b = append(b, p[i])
	}
	return string(b)
}

// globPattern returns the names of the files that pat matches, sorted.
// A wildcard matches no leading period.
func globPattern(pat string) []string {
	elems := strings.Split(pat, "/")
	var found []string
	var expand func(dir string, elems []string)
	expand = func(dir string, elems []string) {
		if len(elems) == 0 {
			if _, err := os.Lstat(dir); err == nil {
				found = append(found, dir)
			}
			return
		}
		e, rest := elems[0], elems[1:]
		join := func(name string) string {
			if dir == "" {
				return name
			}
			if strings.HasSuffix(dir, "/") {
				return dir + name
			}
			return dir + "/" + name
		}
		if e == "" {
			switch {
			case dir == "":
				expand("/", rest)
			case len(rest) == 0:
				if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
					found = append(found, dir+"/")
				}
			default:
				expand(dir+"/", rest)
			}
			return
		}
		if !hasMeta(e) {
			expand(join(unescape(e)), rest)
			return
		}
		d := dir
		if d == "" {
			d = "."
		}
		ents, err := os.ReadDir(d)
		if err != nil {
			return
		}
		for _, ent := range ents {
			if c23.Fnmatch(e, ent.Name(), c23.FNM_PERIOD) {
				expand(join(ent.Name()), rest)
			}
		}
	}
	expand("", elems)
	slices.Sort(found)
	return found
}

// wordexpT is C's wordexp_t.
type wordexpT struct {
	wordc uintptr
	wordv unsafe.Pointer
	offs  uintptr
}

// Wordexp is wordexp: tilde expansion (of the program's own user only),
// parameter expansion (the environment's variables, and $$, $#, $?,
// $0), command substitution with /bin/sh, arithmetic expansion, field
// splitting on IFS, pathname expansion and quote removal, as a POSIX
// shell does; a word whose pattern matches no file is kept. we is not
// changed on an error.
func Wordexp(words, we *c23.Void, flags int32) int32 {
	found, e := expandWords(c23.GoString(words), flags)
	if e != 0 {
		return e
	}
	var t wordexpT
	offWordv, offOffs := int(unsafe.Offsetof(t.wordv)), int(unsafe.Offsetof(t.offs))
	ws := int(c23.WordSize)
	offs := 0
	if flags&WRDE_DOOFFS != 0 {
		offs = int(c23.LoadLong(c23.Add(we, offOffs)))
	}
	var old []*c23.Void
	if flags&WRDE_APPEND != 0 {
		n := int(c23.LoadLong(we))
		if pv := c23.LoadPtr(c23.Add(we, offWordv)); c23.Addr(pv) != 0 {
			for i := range n {
				old = append(old, c23.LoadPtr(c23.Add(pv, (offs+i)*ws)))
			}
		}
	}
	n := len(old) + len(found)
	pv := c23.NewArray[unsafe.Pointer](uintptr((offs + n + 1) * ws))
	for i, p := range old {
		c23.StorePtr(c23.Add(pv, (offs+i)*ws), p)
	}
	for i, f := range found {
		c23.StorePtr(c23.Add(pv, (offs+len(old)+i)*ws), c23.CString(f))
	}
	c23.StoreLong(we, int64(n))
	c23.StorePtr(c23.Add(we, offWordv), pv)
	if flags&WRDE_DOOFFS == 0 {
		c23.StoreLong(c23.Add(we, offOffs), 0)
	}
	return 0
}

// Wordfree is wordfree: the collector frees the words.
func Wordfree(we *c23.Void) {
	if c23.Addr(we) == 0 {
		return
	}
	var t wordexpT
	c23.StoreLong(we, 0)
	c23.StorePtr(c23.Add(we, int(unsafe.Offsetof(t.wordv))), (*c23.Void)(nil))
}
