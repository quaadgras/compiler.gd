// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"go/ast"
	"go/types"
	"path/filepath"
	"strings"
)

// xtestOff decides, for each external test file every host builds,
// whether it builds for ape: not if it depends on the host the way a
// shared file must not, or uses a name of the package that ape does not
// have (one only some hosts declare, or one a left-out test declares).
func (g *pkgGen) xtestOff() map[string]bool {
	off := map[string]bool{}
	count := map[string]int{}
	for _, hp := range g.hs {
		if hp.xinfo == nil {
			return off // some host's do not type-check: leave them be
		}
		for name := range hp.xfiles {
			count[name]++
		}
	}
	shared := map[string]bool{}
	for name, n := range count {
		if n == len(g.hs) {
			shared[name] = true
			off[name] = false
		}
	}
	for changed := true; changed; {
		changed = false
		for name := range shared {
			if off[name] {
				continue
			}
			for _, hp := range g.hs {
				why := g.xtestDepends(hp, hp.xfiles[name])
				if why == "" {
					why = xtestUsesOff(hp, hp.xfiles[name], shared, off)
				}
				if why != "" {
					if *verbose {
						g.reportf("external test left out: %s (%s)", name, why)
					}
					off[name] = true
					changed = true
					break
				}
			}
		}
	}
	return off
}

// xtestUsesOff says why f uses what an external test file that does not
// build for ape declares, or "".
func xtestUsesOff(hp *hostPkg, f *ast.File, shared, off map[string]bool) string {
	why := ""
	ast.Inspect(f, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || why != "" {
			return why == ""
		}
		o := hp.xinfo.Uses[id]
		if o == nil || o.Pkg() == nil || o.Pkg().Path() != hp.bp.ImportPath+"_test" {
			return true
		}
		file := filepath.Base(filepathOf(o))
		if !shared[file] || off[file] {
			why = o.Name() + " of " + file
		}
		return true
	})
	return why
}

func (g *pkgGen) xtestDepends(hp *hostPkg, f *ast.File) string {
	// dependsOnHost reads hp.info: look at the file with the external
	// tests' instead.
	why := ""
	var stack []ast.Node
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		if why != "" {
			return false
		}
		if sel, ok := n.(*ast.SelectorExpr); ok {
			// A field of a public type that is the first host's layout
			// must be in that layout.
			if s := hp.xinfo.Selections[sel]; s != nil && s.Kind() == types.FieldVal {
				if owner := fieldOwnerOf(s); owner != nil && owner.Obj().Pkg() != nil && g.u.generate[owner.Obj().Pkg().Path()] {
					q := g.u.load(owner.Obj().Pkg().Path())
					q.prepare()
					if o, ok := q.first().decls[owner.Obj().Name()]; ok && !q.dedup[o.Name()] {
						st, _ := o.Type().Underlying().(*types.Struct)
						found := false
						for i := 0; st != nil && i < st.NumFields(); i++ {
							found = found || st.Field(i).Name() == sel.Sel.Name
						}
						if !found {
							why = "field " + sel.Sel.Name + " of " + owner.Obj().Name()
						}
					}
				}
			}
		}
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		// A constant that differs between hosts is a variable.
		if c, ok := hp.xinfo.Uses[id].(*types.Const); ok && c.Pkg() != nil && c.Parent() == c.Pkg().Scope() && g.u.hostDiffers(c.Pkg().Path(), hp.host, c) {
			if b, ok := c.Type().(*types.Basic); ok && b.Info()&types.IsUntyped != 0 || inConstContext(stack) {
				why = "constant " + c.Name()
			}
		}
		o := hp.xinfo.Uses[id]
		if o == nil || o.Pkg() != hp.pkg || o.Parent() != hp.pkg.Scope() {
			return true
		}
		file := filepath.Base(filepathOf(o))
		switch {
		case strings.HasSuffix(file, "_test.go"):
			if !g.sharedTests[file] || g.testOff[file] {
				why = o.Name() + " of " + file
			}
		case hp.inOnly(o.Pos()) && !g.dedup[o.Name()]:
			// A host name: ape has it if every host declares it.
			for _, h := range g.hs {
				if _, ok := h.decls[o.Name()]; !ok {
					why = o.Name() + ", which only some hosts declare"
				}
			}
		}
		return true
	})
	return why
}

func filepathOf(o types.Object) string { return fset.Position(o.Pos()).Filename }
