// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"
)

// prepare finds the files every host builds that are the same for every
// host, the host files' declarations, and the types and variables every
// host declares the same way.
//
// A file every host builds is shared only if what it means does not
// depend on the host: it may call host functions (through dispatch) and
// hold host values whose layout it does not look at, but a file that
// looks into a host type (a field, a composite literal, its size), uses
// a host variable, or uses a constant whose value differs between hosts
// where Go needs a constant is copied per host like the host files are.
// Copying a file can make more of what it declares differ between
// hosts, so this repeats until nothing changes.
func (g *pkgGen) prepare() {
	if g.prepared {
		return
	}
	g.prepared = true
	for {
		g.findDecls()
		g.findDedup()
		g.findUnion()
		if !g.demote() {
			break
		}
	}
}

func (g *pkgGen) findDecls() {
	for _, hp := range g.hs {
		hp.decls = map[string]types.Object{}
		scope := hp.pkg.Scope()
		for _, name := range scope.Names() {
			if obj := scope.Lookup(name); hp.inOnly(obj.Pos()) {
				hp.decls[name] = obj
			}
		}
	}
}

// findUnion finds the host types that shared structs embed.
func (g *pkgGen) findUnion() {
	g.union = map[string]bool{}
	for _, hp := range g.hs {
		scope := hp.pkg.Scope()
		for _, name := range scope.Names() {
			tn, ok := scope.Lookup(name).(*types.TypeName)
			if !ok || hp.inOnly(tn.Pos()) {
				continue
			}
			if st, ok := tn.Type().Underlying().(*types.Struct); ok {
				for f := range st.Fields() {
					if en := g.hostNamed(hp, f.Type()); f.Embedded() && en != nil {
						g.union[en.Obj().Name()] = true
					}
				}
			}
		}
	}
}

// demote moves the shared Go files that depend on the host to the host
// files. It reports whether it moved any.
func (g *pkgGen) demote() bool {
	var names []string
	for name := range g.shared {
		if filepath.Ext(name) == ".go" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	moved := false
	// Shared functions host files pass host values to.
	for _, hp := range g.hs {
		for name := range hp.only {
			ast.Inspect(hp.files[name], func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				id, ok := ast.Unparen(call.Fun).(*ast.Ident)
				if !ok {
					return true
				}
				fn, ok := hp.info.Uses[id].(*types.Func)
				if !ok || fn.Pkg() != hp.pkg || hp.inOnly(fn.Pos()) {
					return true
				}
				file := filepath.Base(fset.Position(fn.Pos()).Filename)
				if !g.shared[file] {
					return true
				}
				for _, a := range call.Args {
					var refs []*types.Named
					refsOf(hp.info.Types[a].Type, map[types.Type]bool{}, &refs)
					for _, r := range refs {
						if g.differsType(hp, r) && token.IsExported(r.Obj().Name()) {
							if *verbose {
								g.reportf("copied per host: %s (%s passes a %s to %s)", file, hp.host, r.Obj().Name(), fn.Name())
							}
							delete(g.shared, file)
							for _, hp := range g.hs {
								hp.only[file] = true
							}
							moved = true
							return false
						}
					}
				}
				return true
			})
		}
	}
	for _, name := range names {
		if !g.shared[name] {
			continue
		}
		for _, hp := range g.hs {
			if why := g.dependsOnHost(hp, hp.files[name]); why != "" {
				if *verbose {
					g.reportf("copied per host: %s (%s)", name, why)
				}
				delete(g.shared, name)
				for _, hp := range g.hs {
					hp.only[name] = true
				}
				// What it declares may now differ: look again.
				return true
			}
		}
	}
	return moved
}

// differsType reports whether the named type t, as hp sees it, is a host
// type: one hosts declare differently.
func (g *pkgGen) differsType(hp *hostPkg, n *types.Named) bool {
	if n == nil || n.Obj().Pkg() == nil {
		return false
	}
	if n.Obj().Pkg() == hp.pkg {
		return g.hostNamed(hp, n) != nil && !g.union[n.Obj().Name()]
	}
	return g.u.hostDiffers(n.Obj().Pkg().Path(), hp.host, n.Obj())
}

// differs reports whether obj of the generated package path is one the
// hosts declare differently, as host sees it.
func (u *universe) hostDiffers(path, host string, obj types.Object) bool {
	if !u.generate[path] {
		return false
	}
	q := u.load(path)
	if q.preparing {
		return false
	}
	q.preparing = true
	q.prepare()
	q.preparing = false
	var hp *hostPkg
	for _, h := range q.hs {
		if h.host == host {
			hp = h
		}
	}
	if hp == nil {
		return false
	}
	o, ok := hp.decls[obj.Name()]
	if !ok {
		return false
	}
	switch o := o.(type) {
	case *types.TypeName:
		return !q.dedup[o.Name()] && !q.union[o.Name()]
	case *types.Var:
		return !q.dedup[o.Name()]
	case *types.Const:
		return !q.sameConst(o.Name())
	}
	return false
}

// sameConst reports whether every host declares the constant with the
// same value.
func (g *pkgGen) sameConst(name string) bool {
	var v string
	for i, hp := range g.hs {
		c, ok := hp.decls[name].(*types.Const)
		if !ok {
			return false
		}
		s := c.Val().ExactString() + " " + types.TypeString(c.Type(), func(p *types.Package) string { return p.Path() })
		if i == 0 {
			v = s
		} else if s != v {
			return false
		}
	}
	return true
}

// dependsOnHost says why the shared file f depends on hp, or "".
func (g *pkgGen) dependsOnHost(hp *hostPkg, f *ast.File) string {
	why := ""
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if fs := strings.Fields(c.Text); len(fs) >= 2 && fs[0] == "//go:linkname" {
				if o, ok := hp.decls[fs[1]]; ok && !g.dedup[o.Name()] {
					return "linkname of " + fs[1]
				}
			}
		}
	}
	var stack []ast.Node
	ast.Inspect(f, func(n ast.Node) bool {
		if why != "" {
			return false
		}
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		switch n := n.(type) {
		case *ast.SelectorExpr:
			sel := hp.info.Selections[n]
			if sel == nil || sel.Kind() != types.FieldVal {
				break
			}
			// The struct that declares the field.
			cur := sel.Recv()
			var owner *types.Named
			for _, idx := range sel.Index() {
				t := types.Unalias(cur)
				if p, ok := t.(*types.Pointer); ok {
					t = types.Unalias(p.Elem())
				}
				owner, _ = t.(*types.Named)
				st, ok := t.Underlying().(*types.Struct)
				if !ok {
					break
				}
				cur = st.Field(idx).Type()
			}
			if g.differsType(hp, owner) {
				why = "field " + n.Sel.Name + " of " + owner.Obj().Name()
			}
		case *ast.FuncDecl:
			// A function of host values.
			if fn, ok := hp.info.Defs[n.Name].(*types.Func); ok {
				var refs []*types.Named
				refsOf(fn.Signature(), map[types.Type]bool{}, &refs)
				for _, r := range refs {
					if g.differsType(hp, r) {
						why = fn.Name() + " takes a " + r.Obj().Name()
						return false
					}
				}
			}
			// A method of a host type.
			if n.Recv != nil && len(n.Recv.List) == 1 {
				if fn, ok := hp.info.Defs[n.Name].(*types.Func); ok {
					t := types.Unalias(fn.Signature().Recv().Type())
					if p, ok := t.(*types.Pointer); ok {
						t = types.Unalias(p.Elem())
					}
					if nt, ok := t.(*types.Named); ok && g.differsType(hp, nt) {
						why = "method of " + nt.Obj().Name()
					}
				}
			}
		case *ast.CompositeLit:
			if t, ok := types.Unalias(hp.info.Types[n].Type).(*types.Named); ok && g.differsType(hp, t) {
				why = "composite literal of " + t.Obj().Name()
			}
		case *ast.CallExpr:
			if id, ok := ast.Unparen(n.Fun).(*ast.SelectorExpr); ok {
				x, _ := id.X.(*ast.Ident)
				if pn, ok := hp.info.Uses[x].(*types.PkgName); ok && x != nil && pn.Imported().Path() == "unsafe" && id.Sel.Name != "Pointer" && len(n.Args) == 1 {
					var refs []*types.Named
					refsOf(hp.info.Types[n.Args[0]].Type, map[types.Type]bool{}, &refs)
					for _, r := range refs {
						if g.differsType(hp, r) {
							why = "unsafe." + id.Sel.Name + " of " + r.Obj().Name()
						}
					}
				}
			}
		case *ast.Ident:
			obj := hp.info.Uses[n]
			if obj == nil || obj.Pkg() == nil {
				break
			}
			local := obj.Pkg() == hp.pkg
			switch o := obj.(type) {
			case *types.Var:
				if o.IsField() || o.Parent() != o.Pkg().Scope() {
					break
				}
				if local && hp.inOnly(o.Pos()) && !g.dedup[o.Name()] || !local && g.u.hostDiffers(o.Pkg().Path(), hp.host, o) {
					why = "variable " + o.Name()
				}
			case *types.Const:
				if o.Parent() != o.Pkg().Scope() {
					break
				}
				if local && hp.inOnly(o.Pos()) && !g.sameConst(o.Name()) && !g.isMax(o.Name()) && inConstContext(stack) {
					why = "constant " + o.Name() + " where Go needs a constant"
				}
				// Another package's constant that differs is a variable
				// of one type, whatever the use.
				if !local && g.u.hostDiffers(o.Pkg().Path(), hp.host, o) {
					why = "constant " + o.Pkg().Name() + "." + o.Name()
				}
			case *types.Func:
				// Another package's function of host values is not
				// dispatched.
				if local || o.Signature().Recv() != nil || !g.u.generate[o.Pkg().Path()] {
					break
				}
				var refs []*types.Named
				refsOf(o.Signature(), map[types.Type]bool{}, &refs)
				for _, r := range refs {
					if r.Obj().Pkg() != nil && g.u.hostDiffers(r.Obj().Pkg().Path(), hp.host, r.Obj()) {
						why = o.Pkg().Name() + "." + o.Name() + " takes a " + r.Obj().Name()
					}
				}
			}
		}
		return true
	})
	return why
}

// isMax reports whether the constant is the largest host's.
func (g *pkgGen) isMax(name string) bool {
	_, ok := flagHas(*maxConsts, g.path, name)
	return ok
}

// inConstContext reports whether the innermost node of stack is in a
// constant declaration, an array length or a case of a switch.
func inConstContext(stack []ast.Node) bool {
	for i := len(stack) - 1; i > 0; i-- {
		switch p := stack[i-1].(type) {
		case *ast.GenDecl:
			return p.Tok == token.CONST
		case *ast.ArrayType:
			return p.Len == stack[i]
		case *ast.CaseClause:
			return true
		case *ast.FuncDecl, *ast.FuncLit:
			return false
		}
	}
	return false
}

// refsOf returns the named types t refers to.
func refsOf(t types.Type, seen map[types.Type]bool, out *[]*types.Named) {
	if t == nil || seen[t] {
		return
	}
	seen[t] = true
	switch t := types.Unalias(t).(type) {
	case *types.Named:
		*out = append(*out, t)
	case *types.Pointer:
		refsOf(t.Elem(), seen, out)
	case *types.Slice:
		refsOf(t.Elem(), seen, out)
	case *types.Array:
		refsOf(t.Elem(), seen, out)
	case *types.Struct:
		for f := range t.Fields() {
			refsOf(f.Type(), seen, out)
		}
	case *types.Tuple:
		for v := range t.Variables() {
			refsOf(v.Type(), seen, out)
		}
	case *types.Map:
		refsOf(t.Key(), seen, out)
		refsOf(t.Elem(), seen, out)
	case *types.Signature:
		refsOf(t.Params(), seen, out)
		refsOf(t.Results(), seen, out)
	}
}
