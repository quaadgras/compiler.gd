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
// A declaration of a file every host builds is shared only if what it
// means does not depend on the host: it may call host functions
// (through dispatch) and hold host values whose layout it does not look
// at, but a declaration that looks into a host type (a field, a
// composite literal, its size), uses a host variable, or uses a constant
// whose value differs between hosts where Go needs a constant is copied
// per host like the host files are (see split.go). Copying a
// declaration can make more of what the package declares differ between
// hosts, so this repeats until nothing changes.
func (g *pkgGen) prepare() {
	if g.prepared {
		return
	}
	g.prepared = true
	for {
		g.findDecls()
		g.findDedup()
		g.findSharedVars()
		g.findUnion()
		if !g.demote() {
			break
		}
	}
	// The package's own tests are not copied per host: those that
	// depend on the host do not build for ape.
	g.testOff = map[string]bool{}
	for changed := true; changed; {
		changed = false
		for name := range g.sharedTests {
			if g.testOff[name] {
				continue
			}
			if g.testDepends(name) {
				changed = true
			}
		}
	}
}

// testDepends leaves out the shared test file name if it depends on the
// host or on a test left out, and reports whether it did.
func (g *pkgGen) testDepends(name string) bool {
	{
		for _, hp := range g.hs {
			why := g.dependsOnHost(hp, hp.files[name])
			if why == "" {
				why = g.usesHostTests(hp, hp.files[name])
			}
			if why != "" {
				if *verbose {
					g.reportf("test left out: %s (%s)", name, why)
				}
				g.testOff[name] = true
				return true
			}
		}
	}
	return false
}

// usesHostTests says why f uses what only some hosts' tests declare,
// or "".
func (g *pkgGen) usesHostTests(hp *hostPkg, f *ast.File) string {
	why := ""
	ast.Inspect(f, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || why != "" {
			return why == ""
		}
		o := hp.info.Uses[id]
		if o == nil || o.Pkg() != hp.pkg {
			return true
		}
		file := fileName(o.Pos())
		if strings.HasSuffix(file, "_test.go") && (!g.sharedTests[file] || g.testOff[file]) {
			why = o.Name() + " of " + file
		}
		return true
	})
	return why
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
			if !ok || !g.shared[fileName(tn.Pos())] {
				continue
			}
			if st, ok := tn.Type().Underlying().(*types.Struct); ok {
				for f := range st.Fields() {
					// (Embedded by pointer, it is a handle.)
					if _, isPtr := types.Unalias(f.Type()).(*types.Pointer); isPtr {
						continue
					}
					if en := g.hostNamed(hp, f.Type()); f.Embedded() && en != nil {
						g.union[en.Obj().Name()] = true
					}
				}
			}
		}
	}
}

// demote moves the declarations of shared Go files that depend on the
// host to the files' host parts. It reports whether it moved any.
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
				file := fileName(fn.Pos())
				if !g.shared[file] {
					return true
				}
				for _, a := range call.Args {
					var refs []*types.Named
					refsOf(hp.info.Types[a].Type, map[types.Type]bool{}, &refs)
					for _, r := range refs {
						if g.differsType(hp, r) && token.IsExported(r.Obj().Name()) {
							if g.demoteDeclAt(file, fn.Pos()) {
								if *verbose {
									g.reportf("copied per host: %s: %s (%s passes a %s)", file, fn.Name(), hp.host, r.Obj().Name())
								}
								moved = true
							}
							return false
						}
					}
				}
				return true
			})
		}
	}
	// (Copying is for good, so a pass may copy all it finds.)
	for _, name := range names {
		if !g.shared[name] {
			continue
		}
		f0 := g.first().files[name]
		if g.moved[name] != nil {
			f0 = g.first().orig[name]
		}
		for i, r := range declRanges(f0) {
			if r[0] < 0 || g.moved[name][i] {
				continue
			}
			for _, hp := range g.hs {
				f := hp.files[name]
				if g.moved[name] != nil {
					f = hp.orig[name]
				}
				if why := g.dependsOnHost(hp, declPart(f, i)); why != "" {
					if *verbose {
						g.reportf("copied per host: %s: %s (%s)", name, declName(f.Decls[i]), why)
					}
					g.splitDecl(name, i)
					moved = true
					break
				}
			}
		}
	}
	if g.sharedMethodsOfHostIfaces(names) {
		moved = true
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
		return !q.dedup[o.Name()] && !q.sharedVar[o.Name()]
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
func (g *pkgGen) dependsOnHost(hp *hostPkg, node ast.Node) string {
	why := ""
	f, _ := node.(*ast.File)
	if f == nil {
		f = &ast.File{}
	}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if fs := strings.Fields(c.Text); len(fs) >= 2 && (fs[0] == "//go:linkname" || fs[0] == "//go:linknamestd") {
				// (A one-argument linkname of a host name marks the
				// dispatch function, which has the name.)
				if o, ok := hp.decls[fs[1]]; ok && !g.dedup[o.Name()] && len(fs) == 3 {
					return "linkname of " + fs[1]
				}
				// A pull of another generated package's host name.
				fn, isFunc := hp.pkg.Scope().Lookup(fs[1]).(*types.Func)
				if len(fs) == 3 && (!isFunc || !hp.hasBody(fn)) {
					if pkg, sym, ok := cutLast(fs[2], "."); ok && pkg != g.path && g.u.hostOnly(pkg, hp.host, sym) {
						return "linkname to " + fs[2]
					}
				}
			}
		}
	}
	var stack []ast.Node
	ast.Inspect(node, func(n ast.Node) bool {
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
			// Code that asks internal/goos which system it runs on:
			// under ape the answer is the host's, which a host copy
			// knows.
			if x, ok := n.X.(*ast.Ident); ok && !g.isRuntime() && !strings.HasSuffix(fset.Position(n.Pos()).Filename, "_test.go") {
				if pn, ok := hp.info.Uses[x].(*types.PkgName); ok {
					p := pn.Imported().Path()
					// (runtime.GOOS is the host's: the compiler makes
					// it a run-time value under ape.)
					if p == "internal/goos" && n.Sel.Name != "IsUnix" {
						why = p + "." + n.Sel.Name
						return false
					}
				}
			}
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
		case *ast.TypeAssertExpr:
			// A type assertion to a host type: a host's value is never
			// the ape type's.
			if n.Type != nil {
				if t, ok := types.Unalias(hp.info.Types[n.Type].Type).(*types.Pointer); ok {
					if nt, ok := types.Unalias(t.Elem()).(*types.Named); ok && g.differsType(hp, nt) {
						why = "type assertion to *" + nt.Obj().Name()
					}
				} else if nt, ok := types.Unalias(hp.info.Types[n.Type].Type).(*types.Named); ok && g.differsType(hp, nt) {
					why = "type assertion to " + nt.Obj().Name()
				}
			}
		case *ast.CaseClause:
			// So is a type switch's case.
			if len(stack) >= 3 {
				if _, ok := stack[len(stack)-3].(*ast.TypeSwitchStmt); ok {
					for _, e := range n.List {
						t := types.Unalias(hp.info.Types[e].Type)
						if p, ok := t.(*types.Pointer); ok {
							t = types.Unalias(p.Elem())
						}
						if nt, ok := t.(*types.Named); ok && g.differsType(hp, nt) {
							why = "type switch case " + nt.Obj().Name()
						}
					}
				}
			}
		case *ast.TypeSpec:
			// A type that holds another package's host values: the ape
			// package cannot.
			// (A pointer to a handle is the ape type's.)
			if tn, ok := hp.info.Defs[n.Name].(*types.TypeName); ok && !tn.IsAlias() {
				if r := g.otherHostHeld(hp, tn.Type().Underlying()); r != nil {
					why = tn.Name() + " holds a " + r.Obj().Pkg().Name() + "." + r.Obj().Name()
				}
			}
		case *ast.ValueSpec:
			// A variable of functions of host values.
			for _, name := range n.Names {
				if v, ok := hp.info.Defs[name].(*types.Var); ok && v.Parent() == hp.pkg.Scope() && g.funcOfHost(hp, v.Type()) {
					why = "variable " + v.Name() + " of functions of host values"
				}
			}
		case *ast.CompositeLit:
			if t, ok := types.Unalias(hp.info.Types[n].Type).(*types.Named); ok && g.differsType(hp, t) {
				why = "composite literal of " + t.Obj().Name()
			}
		case *ast.CallExpr:
			// make or new of a host type.
			if id, ok := ast.Unparen(n.Fun).(*ast.Ident); ok && len(n.Args) > 0 {
				if b, ok := hp.info.Uses[id].(*types.Builtin); ok && (b.Name() == "make" || b.Name() == "new") {
					if t, ok := types.Unalias(hp.info.Types[n.Args[0]].Type).(*types.Named); ok && g.differsType(hp, t) {
						why = b.Name() + " of " + t.Obj().Name()
					}
				}
			}
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
				if local && hp.inOnly(o.Pos()) && !g.dedup[o.Name()] && !g.sharedVar[o.Name()] || !local && g.u.hostDiffers(o.Pkg().Path(), hp.host, o) {
					why = "variable " + o.Name()
				}
			case *types.Const:
				if o.Parent() != o.Pkg().Scope() {
					break
				}
				if local && hp.inOnly(o.Pos()) && !g.sameConst(o.Name()) && !g.isMax(o.Name()) && (inConstContext(stack) || g.multiTyped(o.Name())) {
					why = "constant " + o.Name() + " where Go needs a constant (or of several types)"
				}
				// Another package's constant that differs is a variable
				// of one type, whatever the use.
				// A typed one is a variable of its type; an untyped one
				// a variable of one type, whatever the use.
				if !local && g.u.hostDiffers(o.Pkg().Path(), hp.host, o) {
					if b, ok := o.Type().(*types.Basic); ok && b.Info()&types.IsUntyped != 0 || inConstContext(stack) {
						why = "constant " + o.Pkg().Name() + "." + o.Name()
					}
				}
			case *types.TypeName:
				// A host alias has no ape type.
				if local && o.IsAlias() && hp.inOnly(o.Pos()) && !g.dedup[o.Name()] {
					why = "alias " + o.Name()
				}
				// Nor a host interface.
				if _, ok := o.Type().Underlying().(*types.Interface); ok && local && !o.IsAlias() && hp.inOnly(o.Pos()) && !g.dedup[o.Name()] {
					why = "interface " + o.Name()
				}
			case *types.Func:
				// A generic host function is not dispatched.
				if local && hp.inOnly(o.Pos()) && o.Signature().TypeParams().Len() > 0 {
					why = "generic " + o.Name()
					break
				}
				// A host function or method of another package's host
				// values is not dispatched.
				if local && hp.inOnly(o.Pos()) {
					if r := g.otherHostRef(hp, o.Signature()); r != nil {
						why = o.Name() + " takes a " + r.Obj().Pkg().Name() + "." + r.Obj().Name()
					} else if r := g.hostIfaceRef(hp, o.Signature()); r != nil {
						why = o.Name() + " takes a " + r.Obj().Name() + ", a host interface"
					}
					break
				}
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

// multiTyped reports whether shared code uses the untyped constant
// name, which differs between hosts, as values of more than one type:
// no one variable can stand for it.
func (g *pkgGen) multiTyped(name string) bool {
	if g.multi == nil {
		g.multi = map[string]bool{}
		used := map[string]map[string]bool{}
		hp := g.first()
		for id, obj := range hp.info.Uses {
			c, ok := obj.(*types.Const)
			if !ok || c.Pkg() != hp.pkg || !hp.inOnly(c.Pos()) || !g.shared[fileName(id.Pos())] {
				continue
			}
			if b, ok := c.Type().(*types.Basic); !ok || b.Info()&types.IsUntyped == 0 {
				continue
			}
			if t := hp.useType(id); t != nil {
				if used[c.Name()] == nil {
					used[c.Name()] = map[string]bool{}
				}
				used[c.Name()][types.TypeString(t, nil)] = true
			}
		}
		for n, ts := range used {
			g.multi[n] = len(ts) > 1
		}
	}
	return g.multi[name]
}

// isMax reports whether the constant is the largest host's.
func (g *pkgGen) isMax(name string) bool {
	_, ok := flagHas(*maxConsts, g.path, name)
	return ok
}

// inConstContext reports whether the innermost node of stack is in a
// constant declaration or an array length. (A case of a switch may be a
// variable.)
func inConstContext(stack []ast.Node) bool {
	for i := len(stack) - 1; i > 0; i-- {
		switch p := stack[i-1].(type) {
		case *ast.GenDecl:
			return p.Tok == token.CONST
		case *ast.ArrayType:
			return p.Len == stack[i]
		case *ast.FuncDecl, *ast.FuncLit:
			return false
		}
	}
	return false
}

// refsByValue returns the named types t holds other than through a
// pointer: those of its fields, elements and so on.
func refsByValue(t types.Type, seen map[types.Type]bool, out *[]*types.Named) {
	if t == nil || seen[t] {
		return
	}
	seen[t] = true
	switch t := types.Unalias(t).(type) {
	case *types.Named:
		*out = append(*out, t)
	case *types.Slice:
		refsByValue(t.Elem(), seen, out)
	case *types.Array:
		refsByValue(t.Elem(), seen, out)
	case *types.Struct:
		for f := range t.Fields() {
			refsByValue(f.Type(), seen, out)
		}
	case *types.Map:
		refsByValue(t.Key(), seen, out)
		refsByValue(t.Elem(), seen, out)
	}
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
	case *types.Interface:
		for m := range t.ExplicitMethods() {
			refsOf(m.Type(), seen, out)
		}
	case *types.Chan:
		refsOf(t.Elem(), seen, out)
	}
}

// otherHostRef returns a type of another generated package that differs
// between hosts which t refers to, or nil.
func (g *pkgGen) otherHostRef(hp *hostPkg, t types.Type) *types.Named {
	var refs []*types.Named
	refsOf(t, map[types.Type]bool{}, &refs)
	for _, r := range refs {
		if r.Obj().Pkg() != nil && r.Obj().Pkg() != hp.pkg && g.u.hostDiffers(r.Obj().Pkg().Path(), hp.host, r.Obj()) {
			return r
		}
	}
	return nil
}

// declName names a declaration for reports.
func declName(d ast.Decl) string {
	switch d := d.(type) {
	case *ast.FuncDecl:
		if d.Recv != nil && len(d.Recv.List) == 1 {
			return recvName0(d.Recv.List[0].Type) + "." + d.Name.Name
		}
		return d.Name.Name
	case *ast.GenDecl:
		for _, sp := range d.Specs {
			switch sp := sp.(type) {
			case *ast.TypeSpec:
				return sp.Name.Name
			case *ast.ValueSpec:
				return sp.Names[0].Name
			}
		}
	}
	return "?"
}

func recvName0(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.StarExpr:
		return recvName0(e.X)
	case *ast.IndexExpr:
		return recvName0(e.X)
	case *ast.IndexListExpr:
		return recvName0(e.X)
	case *ast.Ident:
		return e.Name
	}
	return "?"
}

// hostIfaceRef returns a host interface of the package that t refers to,
// or nil.
func (g *pkgGen) hostIfaceRef(hp *hostPkg, t types.Type) *types.Named {
	var refs []*types.Named
	refsOf(t, map[types.Type]bool{}, &refs)
	for _, r := range refs {
		if _, ok := r.Underlying().(*types.Interface); ok && g.hostNamed(hp, r) != nil {
			return r
		}
	}
	return nil
}

// otherHostHeld returns a type of another generated package that differs
// between hosts which t holds by value, or nil. (Through a pointer it is
// the ape type's, which host code converts.)
func (g *pkgGen) otherHostHeld(hp *hostPkg, t types.Type) *types.Named {
	// (An interface's methods are what it is.)
	if _, ok := t.Underlying().(*types.Interface); ok {
		return g.otherHostRef(hp, t)
	}
	var refs []*types.Named
	refsByValue(t, map[types.Type]bool{}, &refs)
	for _, r := range refs {
		if r.Obj().Pkg() != nil && r.Obj().Pkg() != hp.pkg && g.u.hostDiffers(r.Obj().Pkg().Path(), hp.host, r.Obj()) {
			return r
		}
	}
	return nil
}

// isHandle reports whether the ape package path has the type name as a
// handle to each host's, not as the first host's data.
func (u *universe) isHandle(path, name string) bool {
	q := u.load(path)
	q.prepare()
	return !q.dataStructForAll(name)
}

// funcOfHost reports whether t has functions whose signatures have host
// types.
func (g *pkgGen) funcOfHost(hp *hostPkg, t types.Type) bool {
	found := false
	var walk func(t types.Type, depth int)
	walk = func(t types.Type, depth int) {
		if depth > 6 || found {
			return
		}
		switch t := types.Unalias(t).(type) {
		case *types.Signature:
			var refs []*types.Named
			refsOf(t, map[types.Type]bool{}, &refs)
			for _, r := range refs {
				if g.differsType(hp, r) {
					found = true
				}
			}
		case *types.Pointer:
			walk(t.Elem(), depth+1)
		case *types.Slice:
			walk(t.Elem(), depth+1)
		case *types.Map:
			walk(t.Elem(), depth+1)
		case *types.Chan:
			walk(t.Elem(), depth+1)
		}
	}
	walk(t, 0)
	return found
}

// sharedMethodsOfHostIfaces copies per host the shared files' methods
// that a host interface's method, renamed in host copies, stands for.
func (g *pkgGen) sharedMethodsOfHostIfaces(names []string) bool {
	hp := g.first()
	ifaceMethods := map[string]bool{}
	for name, obj := range hp.decls {
		tn, ok := obj.(*types.TypeName)
		if !ok || g.dedup[name] {
			continue
		}
		if it, ok := tn.Type().Underlying().(*types.Interface); ok {
			for m := range it.ExplicitMethods() {
				ifaceMethods[m.Name()] = true
			}
		}
	}
	// Names of host methods on shared types.
	hostMethods := map[string]bool{}
	for _, name := range hp.pkg.Scope().Names() {
		tn, ok := hp.pkg.Scope().Lookup(name).(*types.TypeName)
		if !ok || hp.inOnly(tn.Pos()) && !g.dedup[name] {
			continue
		}
		if nt, ok := tn.Type().(*types.Named); ok {
			for m := range nt.Methods() {
				if hp.inOnly(m.Pos()) && ifaceMethods[m.Name()] {
					hostMethods[m.Name()] = true
				}
			}
		}
	}
	moved := false
	for _, name := range names {
		if !g.shared[name] {
			continue
		}
		for _, d := range hp.files[name].Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || !hostMethods[fd.Name.Name] {
				continue
			}
			if g.demoteDeclAt(name, fd.Pos()) {
				if *verbose {
					g.reportf("copied per host: %s: %s (a host interface's method)", name, declName(fd))
				}
				moved = true
			}
		}
	}
	return moved
}

// findSharedVars finds the variables every host declares, of
// the same type but initialized differently: the ape package has one,
// initialized from the host's, which host copies use too.
func (g *pkgGen) findSharedVars() {
	g.sharedVar = map[string]bool{}
	if g.isRuntime() {
		return
	}
	for name, obj := range g.first().decls {
		if g.dedup[name] {
			continue
		}
		if _, ok := obj.(*types.Var); !ok {
			continue
		}
		same := true
		var t string
		for i, hp := range g.hs {
			o, ok := hp.decls[name].(*types.Var)
			if !ok || hp.linkname(o) != "" || holdsLock(o.Type(), 0) {
				// (One the ape package would initialize by copying a
				// lock is not.)
				same = false
				break
			}
			var refs []*types.Named
			refsOf(o.Type(), map[types.Type]bool{}, &refs)
			for _, r := range refs {
				if g.differsType(hp, r) {
					same = false
				}
			}
			s := types.TypeString(o.Type(), func(p *types.Package) string { return p.Path() })
			if i == 0 {
				t = s
			} else if s != t {
				same = false
			}
		}
		if same {
			g.sharedVar[name] = true
		}
	}
}

// holdsLock reports whether a value of t holds a lock: something not to
// be copied.
func holdsLock(t types.Type, depth int) bool {
	if depth > 8 {
		return false
	}
	switch u := types.Unalias(t).Underlying().(type) {
	case *types.Struct:
		if ms := types.NewMethodSet(types.NewPointer(t)); ms.Lookup(nil, "Lock") != nil && ms.Lookup(nil, "Unlock") != nil {
			return true
		}
		for f := range u.Fields() {
			if holdsLock(f.Type(), depth+1) {
				return true
			}
		}
	case *types.Array:
		return holdsLock(u.Elem(), depth+1)
	}
	return false
}
