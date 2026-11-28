// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"slices"
	"strings"
)

// C's library is internal/c23, the run-time support of the Go that
// cmd/cc generates, and a package under it for each header (or a few
// headers that share their code): internal/c23/stdio, internal/c23/
// sys/socket and so on. The Go imports the packages whose functions it
// calls, so a program links only the library it uses. libc's values say
// where a function is: "Malloc" is internal/c23's Malloc, "stdio.Printf"
// internal/c23/stdio's Printf, "sys/socket.Socket" internal/c23/
// sys/socket's Socket.

// libcSplit returns the package path, under internal/c23 ("" for
// internal/c23 itself), and the Go name of a libc value.
func libcSplit(v string) (path, name string) {
	i := strings.LastIndex(v, ".")
	if i < 0 {
		return "", v
	}
	return v[:i], v[i+1:]
}

// libcAlias is the name the generated Go imports internal/c23/path as:
// c23_ and the path, with _ for /. No C name becomes it, as C's are
// c_name, g_name and so on (nameGlobal), and a C name cannot begin
// with a digit.
func libcAlias(path string) string {
	return "c23_" + strings.ReplaceAll(path, "/", "_")
}

// libcFunc is the Go of the libc value v, whose package it notes for
// the imports.
func (g *gen) libcFunc(v string) string {
	path, name := libcSplit(v)
	if path == "" {
		return "c23." + name
	}
	if g.libcPkgs == nil {
		g.libcPkgs = map[string]bool{}
	}
	g.libcPkgs[path] = true
	return libcAlias(path) + "." + name
}

// LibcPackages returns the import paths of internal/c23's packages of
// headers, which the Go cmd/cc generates may import.
func LibcPackages() []string {
	m := map[string]bool{"internal/c23/" + cxxPkg: true}
	for _, v := range libc {
		if path, _ := libcSplit(v); path != "" {
			m["internal/c23/"+path] = true
		}
	}
	var paths []string
	for p := range m {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	return paths
}

// isLibcGo reports whether fn, Go that calls a function, calls the
// library's: internal/c23's (c23.Name) or a header's (c23_path.Name).
func isLibcGo(fn string) bool {
	if strings.HasPrefix(fn, libcAlias(cxxPkg)+".") {
		// The C++ library's: generic, with the types that C++ has.
		return false
	}
	return strings.HasPrefix(fn, "c23.") || strings.HasPrefix(fn, "c23_")
}

// libcTyped is the functions of the library, by their names in C, whose
// Go takes and returns pointers of the types that C gives them, where
// the others take any pointer (*c23.Void): a pointer to a scalar or to
// a pointer has that type, int * is *c23.Int and char ** is **c23.Char
// (libcPtr). Such a function loads and stores through its pointer as Go
// does, with nothing looked up: what the pointer points to is what its
// type says, as it is for the C that calls it. A string is still looked
// for the end of, in what its pointer points into.
var libcTyped = map[string]bool{
	"frexp": true, "frexpf": true, "frexpl": true, "modf": true, "modff": true, "modfl": true,
	"remquo": true, "remquof": true, "remquol": true, "time": true,
	"printf": true, "fprintf": true, "sprintf": true, "snprintf": true, "puts": true, "fputs": true,
}

// libcPtr is the Go type of a pointer of C type t in the signature of a
// function of libcTyped.
func (g *gen) libcPtr(t *ctype) string {
	if !t.isPtr() {
		return voidPtr
	}
	switch e := t.elem; {
	case e.isPtr():
		return "*" + g.libcPtr(e)
	case (e.isInteger() || e.kind == kFloat || e.kind == kDouble || e.kind == kLDouble) && !e.isCap() && !is128(e) && !e.isBitInt():
		return "*" + goType(e.unqual())
	}
	return voidPtr
}

// cxxPkg is the package, under internal/c23, of what the C++ library
// headers call that is Go: a function that a header declares, and
// nothing defines, with a name that begins with __go_ is that package's
// function of the rest of the name (std::__go_SliceLen is
// cxx.SliceLen), and is generic where the header's is a template. The
// arguments are passed as they are to any function.
const cxxPkg = "cxx"

// goFunc returns the Go function of the C++ library that the function s
// names, and whether it names one.
func goFunc(s *symbol) (string, bool) {
	n := s.src
	if n == "" {
		n = s.name
	}
	if !strings.HasPrefix(n, "__go_") || len(n) == 5 {
		return "", false
	}
	return cxxPkg + "." + n[5:], true
}

// goTypes are the class templates of the C++ library headers whose
// instances are Go's types: the Go type, with $0 and $1 for the Go
// types of the template's arguments. The header declares each with
// members that give it the size and the alignment of the Go type, and
// never names them.
var goTypes = map[string]string{
	"__go_slice": "[]$0",
}

// goType returns the Go type that the class t is, if it is one of
// goTypes.
func (g *gen) goType(t *ctype) (string, bool) {
	if t.kind != kStruct || t.rec == nil || t.rec.inst == nil || t.rec.inst.tmpl == nil {
		return "", false
	}
	tm := t.rec.inst.tmpl
	if tm.primary != nil {
		tm = tm.primary
	}
	pat, ok := goTypes[tm.name]
	if !ok {
		return "", false
	}
	for i, a := range t.rec.inst.args {
		if a.t != nil {
			pat = strings.ReplaceAll(pat, "$"+string(rune('0'+i)), g.mirror(a.t))
		}
	}
	return pat, true
}

// isGoType reports whether t is a class that goType gives a Go type.
func isGoType(t *ctype) bool {
	if t.kind != kStruct || t.rec == nil || t.rec.inst == nil || t.rec.inst.tmpl == nil {
		return false
	}
	tm := t.rec.inst.tmpl
	if tm.primary != nil {
		tm = tm.primary
	}
	_, ok := goTypes[tm.name]
	return ok
}

// goAlign16 reports whether Go aligns the mirror of t to 16 bytes, more
// than it aligns anything of C's: t is, or has in it, a slice of Go's,
// which is two words aligned to 16 where a pointer is 8 bytes
// (doc/gd/slice16.md). The header gives the class that alignment.
func goAlign16(t *ctype) bool {
	if target.ilp32() {
		return false
	}
	for t.kind == kArray {
		t = t.elem
	}
	if t.kind != kStruct || t.rec == nil || t.rec.align != 16 {
		return false
	}
	if isGoType(t) {
		return true
	}
	if t.rec.union {
		return false
	}
	for _, f := range t.rec.fields {
		if f.bits < 0 && goAlign16(f.t) {
			return true
		}
	}
	return false
}
