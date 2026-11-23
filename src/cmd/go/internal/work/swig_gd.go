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
// { char *p; intgo n; }, both ways. A view of a string as its words is a
// fatal error (doc/gd/unsafe.md), and an inline string has no address in
// them. So swigForGd rewrites the output of one swig run (the .go file):
// a string Go passes to C is made into that struct from unsafe.StringData,
// which is the address of its bytes in either form, and its length. A
// string C passes to Go is of the form with an address.
//
// swigForGdVersion is in the action ID of a package with SWIG files:
// change it when swigForGd changes, so that they are built again.
const swigForGdVersion = "4"

var (
	swigGostring   = "typedef struct { char *p; intgo n; } _gostring_;"
	swigStringType = regexp.MustCompile(`typedef _gostring_ (swig_type_\d+);`)
	swigToC        = regexp.MustCompile(`\*\(\*C\.(swig_type_\d+)\)\(unsafe\.Pointer\(&(\w+)\)\)`)
)

// SWIG's swigCopyString, which frees the C string it copies, takes the
// string's words the same way.
const (
	swigCopyStringWords = "p := *(*swig_gostring)(unsafe.Pointer(&s))"
	swigCopyStringWhole = "p := swig_gostring{uintptr(unsafe.Pointer(unsafe.StringData(s))), len(s)}"
)

// swigGoslice is SWIG's slice, which with 8-byte words but on wasm is
// gd's packed slice, its length and capacity uint32s (doc/gd/slice16.md):
// swigForGd rewrites the C file's typedef.
var (
	swigGoslice       = "typedef struct { void* array; intgo len; intgo cap; } _goslice_;"
	swigGoslicePacked = "typedef struct { void* array; unsigned int len; unsigned int cap; } _goslice_;"
)

func swigForGd(goFile, cFile string, slicePacked bool) error {
	c, err := os.ReadFile(cFile)
	if err != nil {
		return err
	}
	if slicePacked && strings.Contains(string(c), swigGoslice) {
		c = []byte(strings.ReplaceAll(string(c), swigGoslice, swigGoslicePacked))
		if err := os.WriteFile(cFile, c, 0666); err != nil {
			return err
		}
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
	gs = swigToC.ReplaceAllStringFunc(gs, func(m string) string {
		sub := swigToC.FindStringSubmatch(m)
		if !strs[sub[1]] {
			return m
		}
		return "*(*C." + sub[1] + ")(unsafe.Pointer(&struct {\n\tp *byte\n\tn int\n}{unsafe.StringData(" + sub[2] + "), len(" + sub[2] + ")}))"
	})
	gs = strings.ReplaceAll(gs, swigCopyStringWords, swigCopyStringWhole)
	return os.WriteFile(goFile, []byte(gs), 0666)
}
