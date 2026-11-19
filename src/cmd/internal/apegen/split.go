// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package apegen

import (
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A shared file is copied per host a declaration at a time: the
// declarations that depend on the host move to the host copies (the
// file's "host part", named name+"#"), the rest stay shared, in a copy
// of the file without them that builds for ape in its place.
//
// A declaration's part of the file runs from the end of the declaration
// before it, so that its doc comment and directives go with it.

// splits maps a split file (as some host parsed it) to the byte ranges
// of the declarations it copies per host.
var splits = map[*token.File][][2]int{}

const hostPart = "#"

// fileName is the name of the file pos is in, or of its host part.
func fileName(pos token.Pos) string {
	tf := fset.File(pos)
	if tf == nil {
		return ""
	}
	name := filepath.Base(tf.Name())
	off := tf.Offset(pos)
	for _, r := range splits[tf] {
		if r[0] <= off && off < r[1] {
			return name + hostPart
		}
	}
	return name
}

// splitsByName is splits by the files' names: for positions of another
// parse of the files (an importer's), whose split is the same.
var splitsByName = map[string][][2]int{}

// fileNameAnyParse is fileName for a position in any parse of the file.
func fileNameAnyParse(pos token.Pos) string {
	p := fset.Position(pos)
	name := filepath.Base(p.Filename)
	for _, r := range splitsByName[p.Filename] {
		if r[0] <= p.Offset && p.Offset < r[1] {
			return name + hostPart
		}
	}
	return name
}

func realName(name string) string { return strings.TrimSuffix(name, hostPart) }

// declRanges returns the byte range of each of f's declarations; imports
// have none.
func declRanges(f *ast.File) [][2]int {
	tf := fset.File(f.Pos())
	prev := tf.Offset(f.Name.End())
	var out [][2]int
	for _, d := range f.Decls {
		end := tf.Offset(d.End())
		if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.IMPORT {
			out = append(out, [2]int{-1, -1})
		} else {
			out = append(out, [2]int{prev, end})
		}
		prev = end
	}
	return out
}

// declPart returns d with the comments of its part of the file, as a
// file of its own, for dependsOnHost.
func declPart(f *ast.File, i int) *ast.File {
	r := declRanges(f)[i]
	part := &ast.File{Name: f.Name, Decls: []ast.Decl{f.Decls[i]}}
	tf := fset.File(f.Pos())
	for _, cg := range f.Comments {
		if o := tf.Offset(cg.Pos()); r[0] <= o && o < r[1] {
			part.Comments = append(part.Comments, cg)
		}
	}
	return part
}

// splitDecl moves the i'th declaration of the shared file name to its
// host part.
func (g *pkgGen) splitDecl(name string, i int) {
	if g.moved == nil {
		g.moved = map[string]map[int]bool{}
	}
	if g.moved[name] == nil {
		g.moved[name] = map[int]bool{}
		for _, hp := range g.hs {
			if hp.orig == nil {
				hp.orig = map[string]*ast.File{}
			}
			hp.orig[name] = hp.files[name]
		}
	}
	g.moved[name][i] = true
	f0 := g.first().orig[name]
	ranges := declRanges(f0)
	var moved [][2]int
	for j := range f0.Decls {
		if g.moved[name][j] {
			moved = append(moved, ranges[j])
		}
	}
	for _, hp := range g.hs {
		splits[fset.File(hp.orig[name].Pos())] = moved
	}
	splitsByName[fset.Position(f0.Pos()).Filename] = moved
	for _, hp := range g.hs {
		f := hp.orig[name]
		shared := &ast.File{Doc: f.Doc, Package: f.Package, Name: f.Name, Imports: f.Imports, FileStart: f.FileStart, FileEnd: f.FileEnd, GoVersion: f.GoVersion}
		host := &ast.File{Package: f.Package, Name: f.Name, Imports: f.Imports, FileStart: f.FileStart, FileEnd: f.FileEnd, GoVersion: f.GoVersion}
		for j, d := range f.Decls {
			if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.IMPORT {
				shared.Decls = append(shared.Decls, d)
				host.Decls = append(host.Decls, d)
			} else if g.moved[name][j] {
				host.Decls = append(host.Decls, d)
			} else {
				shared.Decls = append(shared.Decls, d)
			}
		}
		for _, cg := range f.Comments {
			if fileName(cg.Pos()) == name {
				shared.Comments = append(shared.Comments, cg)
			} else {
				host.Comments = append(host.Comments, cg)
			}
		}
		hp.files[name] = shared
		hp.files[name+hostPart] = host
		hp.only[name+hostPart] = true
	}
}

// demoteDeclAt moves the declaration of the shared file name at pos
// (as any host parsed it) to the file's host part, and reports whether
// it did.
func (g *pkgGen) demoteDeclAt(name string, pos token.Pos) bool {
	off := fset.Position(pos).Offset
	f := g.first().files[name]
	if g.moved[name] != nil {
		f = g.first().orig[name]
	}
	for i, r := range declRanges(f) {
		if r[0] <= off && off < r[1] {
			if g.moved[name][i] {
				return false
			}
			g.splitDecl(name, i)
			return true
		}
	}
	return false
}

// writeShared writes the shared part of the split file name, which
// builds for ape in its place.
func (g *pkgGen) writeShared(name string) {
	src, err := os.ReadFile(filepath.Join(g.dir, name))
	if err != nil {
		panic(err)
	}
	f := g.first().orig[name]
	var edits []edit
	for i, r := range declRanges(f) {
		if g.moved[name][i] {
			edits = append(edits, edit{r[0], r[1], ""})
		}
	}
	g.sharedOut[name] = g.finishGo(src, edits, name, name)
}

// hostPartEdits deletes from the host part of the split file name what
// stays shared.
func (g *pkgGen) hostPartEdits(name string) []edit {
	f := g.first().orig[name]
	var edits []edit
	for i, r := range declRanges(f) {
		if r[0] >= 0 && !g.moved[name][i] {
			edits = append(edits, edit{r[0], r[1], ""})
		}
	}
	return edits
}

// splitNames returns the names of the split files.
func (g *pkgGen) splitNames() []string {
	var names []string
	for name := range g.moved {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
