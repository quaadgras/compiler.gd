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
	m := map[string]bool{}
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
	return strings.HasPrefix(fn, "c23.") || strings.HasPrefix(fn, "c23_")
}
