// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typecheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"internal/testenv"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"cmd/compile/internal/types"
)

// ptrResults counts the *T results of ft, the results Phase G
// extends a signature for (unsafe.Pointer is not one).
func ptrResults(ft *ast.FuncType) int {
	n := 0
	if ft.Results == nil {
		return 0
	}
	for _, f := range ft.Results.List {
		if _, ok := f.Type.(*ast.StarExpr); ok {
			n += max(len(f.Names), 1)
		}
	}
	return n
}

// TestBuiltinOutBufMatchesRuntime checks that the compiler's view of
// each runtime function takes a Phase G outBuf exactly when the
// runtime's own compile of it does: builtin declarations with *T
// results whose runtime definition returns unsafe.Pointer must be
// listed in types.RuntimeResultUnextended, and nothing else may be.
// A mismatch shifts result slots on stack ABIs such as wasm.
func TestBuiltinOutBufMatchesRuntime(t *testing.T) {
	src := filepath.Join(testenv.GOROOT(t), "src")
	fset := token.NewFileSet()

	// Runtime definitions, including linknamed ones in
	// internal/runtime packages.
	real := map[string][]int{}
	for _, dir := range []string{"runtime", "internal/runtime/maps", "internal/coverage/cfile"} {
		files, err := filepath.Glob(filepath.Join(src, dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Recv != nil {
					continue
				}
				name := ""
				if dir == "runtime" {
					name = fd.Name.Name
				}
				if fd.Doc != nil {
					for _, c := range fd.Doc.List {
						fs := strings.Fields(c.Text)
						if len(fs) == 3 && fs[0] == "//go:linkname" && strings.HasPrefix(fs[2], "runtime.") {
							name = strings.TrimPrefix(fs[2], "runtime.")
						}
					}
				}
				if name != "" {
					real[name] = append(real[name], ptrResults(fd.Type))
				}
			}
		}
	}

	check := func(name string, compiler int) {
		for _, r := range real[name] {
			unext := types.RuntimeResultUnextended(name)
			switch {
			case unext && r != 0:
				t.Errorf("%s: listed in RuntimeResultUnextended, but the runtime returns %d *T results", name, r)
			case !unext && r != compiler:
				t.Errorf("%s: compiler has %d *T results, runtime has %d; add it to RuntimeResultUnextended or fix the declarations", name, compiler, r)
			}
		}
	}
	for _, b := range []string{"runtime.go", "coverage.go"} {
		f, err := parser.ParseFile(fset, filepath.Join("_builtin", b), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok {
				check(fd.Name.Name, ptrResults(fd.Type))
			}
		}
	}
	// Called through ssagen's rtcall with a *T result type.
	check("maybeInPlace", 1)
	for name := range real {
		if strings.HasPrefix(name, "mallocgc") {
			check(name, 1)
		}
	}
}

// TestLinknameOutBufAgree checks that both sides of every
// //go:linkname pair in std declare the same number of *T results, so
// that both take the same Phase G outBufs. unsafe.Pointer and *T are
// interchangeable across a linkname in stock Go, but not here: on
// stack ABIs (wasm) the extra outBuf shifts the results.
func TestLinknameOutBufAgree(t *testing.T) {
	src := filepath.Join(testenv.GOROOT(t), "src")
	fset := token.NewFileSet()
	type decl struct {
		pos  string
		ptrs int
	}
	decls := map[string][]decl{}   // pkgpath.name -> declarations
	links := map[string][]string{} // pkgpath.name -> linkname partners
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || path == filepath.Join(src, "cmd") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil || f.Name.Name == "main" {
			return nil
		}
		rel, _ := filepath.Rel(src, filepath.Dir(path))
		pkg := filepath.ToSlash(rel)
		linkname := map[string]string{}
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				if fs := strings.Fields(c.Text); len(fs) == 3 && fs[0] == "//go:linkname" {
					linkname[fs[1]] = fs[2]
				}
			}
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv != nil {
				continue
			}
			name := pkg + "." + fd.Name.Name
			decls[name] = append(decls[name], decl{fset.Position(fd.Pos()).String(), ptrResults(fd.Type)})
			if target, ok := linkname[fd.Name.Name]; ok {
				links[name] = append(links[name], target)
				links[target] = append(links[target], name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for start := range links {
		if seen[start] {
			continue
		}
		var group []decl
		stack := []string{start}
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[n] {
				continue
			}
			seen[n] = true
			group = append(group, decls[n]...)
			stack = append(stack, links[n]...)
		}
		if slices.ContainsFunc(group, func(d decl) bool { return d.ptrs != group[0].ptrs }) {
			var b strings.Builder
			for _, d := range group {
				b.WriteString("\n\t" + d.pos + ": " + strconv.Itoa(d.ptrs) + " *T results")
			}
			t.Errorf("linkname partners disagree on *T results; return unsafe.Pointer or uintptr on both sides:%s", b.String())
		}
	}
}
