// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"fmt"
	"go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A File is a Go file that Package writes.
type File struct {
	Name string
	Data []byte
}

// Package builds the C of a Go package that imports "C" with cmd/cc
// instead of cgo, as cmd/go does when cgo is off (doc/gd/cc.md). goFiles
// are the package's files that import "C", cFiles its C files, both in
// dir. It returns the Go to compile with the package's other files: the C
// (preambles first, then the C files, as one unit) translated, and each
// of goFiles rewritten as cgo rewrites them.
func Package(dir, pkg string, goFiles, cFiles []string) (out []File, err error) {
	fset := gotoken.NewFileSet()
	type goFile struct {
		name string
		src  []byte
		f    *ast.File
		unit *unit
	}
	var gos []*goFile
	var units []*unit
	for _, name := range goFiles {
		path := filepath.Join(dir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		f, err := goparser.ParseFile(fset, path, src, goparser.ParseComments)
		if err != nil {
			return nil, err
		}
		gf := &goFile{name: name, src: src, f: f}
		gos = append(gos, gf)
		// The file's preamble, one translation unit, as in cgo.
		var pre strings.Builder
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != gotoken.IMPORT {
				continue
			}
			for _, sp := range gd.Specs {
				is := sp.(*ast.ImportSpec)
				if is.Path.Value != `"C"` {
					continue
				}
				doc := is.Doc
				if doc == nil && len(gd.Specs) == 1 {
					doc = gd.Doc
				}
				if doc == nil {
					continue
				}
				// Blank lines keep the preamble's lines where they are in
				// the Go file, for errors.
				line := fset.Position(doc.Pos()).Line
				pre.WriteString(strings.Repeat("\n", max(line-1-strings.Count(pre.String(), "\n"), 0)))
				pre.WriteString(preambleText(doc))
			}
		}
		u, err := parseUnit(path, pre.String())
		if err != nil {
			return nil, err
		}
		gf.unit = u
		units = append(units, u)
	}
	cFiles = append([]string(nil), cFiles...)
	sort.Strings(cFiles)
	for _, name := range cFiles {
		path := filepath.Join(dir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		u, err := parseUnit(path, string(src))
		if err != nil {
			return nil, err
		}
		units = append(units, u)
	}
	o := options{pkg: pkg, prefix: "_C23"}
	c, pg, err := generate(dir, units, o)
	if err != nil {
		return nil, err
	}
	out = append(out, File{"_c23_c.go", c})
	for _, gf := range gos {
		g := &gen{pkgGen: pg, u: gf.unit}
		data, err := g.rewrite(fset, gf.f, gf.src)
		if err != nil {
			return nil, err
		}
		out = append(out, File{strings.TrimSuffix(gf.name, ".go") + ".c23.go", data})
	}
	return out, nil
}

// parseUnit preprocesses and parses one translation unit.
func parseUnit(path, src string) (*unit, error) {
	toks, err := lexUnit([]source{{path, src}})
	if err != nil {
		return nil, err
	}
	return parse(path, toks)
}

// preambleText is a preamble's C: the comment's text, with its line
// structure (comment markers become spaces).
func preambleText(cg *ast.CommentGroup) string {
	var b strings.Builder
	for i, c := range cg.List {
		if i > 0 {
			b.WriteString("\n")
		}
		t := c.Text
		switch {
		case strings.HasPrefix(t, "//"):
			b.WriteString(t[2:])
		case strings.HasPrefix(t, "/*"):
			b.WriteString(strings.TrimSuffix(t[2:], "*/"))
		default:
			fmt.Fprint(&b, t)
		}
	}
	return b.String()
}
