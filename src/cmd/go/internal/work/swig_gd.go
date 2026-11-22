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

// gd strings are Go's two words, {ptr, len}, but with 8-byte words one
// of 15 bytes or fewer keeps them in its words (its first word has bit
// 63 set: runtime.stringStruct, doc/gd/sso-string.md). SWIG's output
// reinterprets Go strings as its own string struct, _gostring_,
// { char *p; intgo n; }, both ways. swigForGd rewrites the output of one
// swig run (the .go file) so that a string Go passes to C is first given
// its heap form, its bytes at its pointer (_swig_gdString). A string C
// passes to Go is one of that form.
//
// swigForGdVersion is in the action ID of a package with SWIG files:
// change it when swigForGd changes, so that they are built again.
const swigForGdVersion = "2"

var (
	swigGostring   = "typedef struct { char *p; intgo n; } _gostring_;"
	swigStringType = regexp.MustCompile(`typedef _gostring_ (swig_type_\d+);`)
	swigToC        = regexp.MustCompile(`\*\(\*C\.(swig_type_\d+)\)\(unsafe\.Pointer\(&(\w+)\)\)`)
)

const swigGdString = `
// _swig_gdString returns s as C reads a string, its bytes at its
// pointer: an inline one is copied to the heap.
// (The go command added this for gd's strings: see swigForGd.)
func _swig_gdString(s *string) *string {
	w0 := uint64((*[2]uintptr)(unsafe.Pointer(s))[0])
	if unsafe.Sizeof(uintptr(0)) == 4 || w0>>63 == 0 {
		return s
	}
	b := []byte(*s)
	r := unsafe.String(unsafe.SliceData(b), len(b))
	return &r
}
`

func swigForGd(goFile, cFile string) error {
	c, err := os.ReadFile(cFile)
	if err != nil {
		return err
	}
	if !strings.Contains(string(c), swigGostring) {
		return fmt.Errorf("%s: no %q: this SWIG's strings are not of the layout cmd/go knows", cFile, swigGostring)
	}

	g, err := os.ReadFile(goFile)
	if err != nil {
		return err
	}
	gs := string(g)
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
