// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// gd strings are 3 words, {ptr, hash, len}, not Go's {ptr, len}, and one
// of 15 bytes or fewer keeps them in its header (its pointer is nil or a
// literal's, its last word holds a tag and bytes: runtime.stringStruct).
// SWIG's output assumes Go's layout: it declares its own string struct,
// _gostring_, as { char *p; intgo n; }, and its Go code reinterprets
// strings as that struct, both ways. swigForGd rewrites the output of
// one swig run (the .go file and the C or C++ wrapper) for the layout
// gd's cgo gives C (its _GoString_):
//
//   - _gostring_ is { char *p; size_t h; intgo n; }, h the cached hash
//     (0: none), and a _gostring_ that C declares starts zeroed;
//   - the Go side's own view of it (swig_gostring) has h too;
//   - a string Go passes to C is first given its heap form, its bytes
//     at its pointer (_swig_gdString).
//
// swigForGdVersion is in the action ID of a package with SWIG files:
// change it when swigForGd changes, so that they are built again.
const swigForGdVersion = "1"

var (
	swigGostring     = "typedef struct { char *p; intgo n; } _gostring_;"
	swigGdGostring   = "typedef struct { char *p; size_t h; intgo n; } _gostring_;"
	swigGoGostring   = "type swig_gostring struct { p uintptr; n int }"
	swigGdGoGostring = "type swig_gostring struct { p uintptr; h uintptr; n int }"
	swigCLocal       = regexp.MustCompile(`(?m)^(\s*_gostring_ \w+);`)
	swigStringType   = regexp.MustCompile(`typedef _gostring_ (swig_type_\d+);`)
	swigToC          = regexp.MustCompile(`\*\(\*C\.(swig_type_\d+)\)\(unsafe\.Pointer\(&(\w+)\)\)`)
)

const swigGdString = `
// _swig_gdString returns s as C reads a string, its bytes at its
// pointer: one whose bytes are in its header is copied to the heap.
// (The go command added this for gd's strings: see swigForGd.)
func _swig_gdString(s *string) *string {
	if (*[3]uintptr)(unsafe.Pointer(s))[2]>>60 == 0 {
		return s
	}
	b := []byte(*s)
	r := new(string)
	*(*struct {
		p    *byte
		h, n uintptr
	})(unsafe.Pointer(r)) = struct {
		p    *byte
		h, n uintptr
	}{unsafe.SliceData(b), 0, uintptr(len(b))}
	return r
}
`

func swigForGd(goFile, cFile string) error {
	c, err := os.ReadFile(cFile)
	if err != nil {
		return err
	}
	cs := string(c)
	if !strings.Contains(cs, swigGostring) {
		return fmt.Errorf("%s: no %q: this SWIG's strings are not of the layout cmd/go knows", cFile, swigGostring)
	}
	cs = strings.ReplaceAll(cs, swigGostring, swigGdGostring)
	cs = swigCLocal.ReplaceAllString(cs, "$1 = {0};")
	if err := os.WriteFile(cFile, []byte(cs), 0666); err != nil {
		return err
	}

	g, err := os.ReadFile(goFile)
	if err != nil {
		return err
	}
	gs := strings.ReplaceAll(string(g), swigGostring, swigGdGostring)
	gs = strings.ReplaceAll(gs, swigGoGostring, swigGdGoGostring)
	strs := make(map[string]bool)
	for _, m := range swigStringType.FindAllStringSubmatch(gs, -1) {
		strs[m[1]] = true
	}
	n := 0
	gs = swigToC.ReplaceAllStringFunc(gs, func(m string) string {
		sub := swigToC.FindStringSubmatch(m)
		if !strs[sub[1]] {
			return m
		}
		n++
		return "*(*C." + sub[1] + ")(unsafe.Pointer(_swig_gdString(&" + sub[2] + ")))"
	})
	if n > 0 {
		gs += swigGdString
	}
	return os.WriteFile(goFile, []byte(gs), 0666)
}
