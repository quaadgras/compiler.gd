// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package noder

import (
	"go/constant"
	"internal/buildcfg"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"cmd/compile/internal/syntax"
	"cmd/compile/internal/types2"
	"cmd/internal/objabi"
)

// apeVariantLinkname links a function without a body in a GOOS=ape host
// variant of a package (ape/linux/syscall is syscall as Linux builds it)
// to the package's own name for it (syscall.f), which other packages
// push it to by linkname, unless the variant's assembly defines it.
func (pw *pkgWriter) apeVariantLinkname(obj *types2.Func) {
	path := pw.gd.Ctxt.Pkgpath
	orig := objabi.APEVariantOf(path)
	if orig == path {
		return
	}
	// A one-argument //go:linkname names the function by its own path.
	if l, ok := pw.linknames[obj]; ok && l.remote != objabi.PathToPrefix(path)+"."+obj.Name() {
		return
	}
	if pw.apeAsmDefs == nil {
		pw.apeAsmDefs = map[string]bool{}
		if file := pw.gd.Flag.SymABIs; file != "" {
			data, _ := os.ReadFile(file)
			for _, line := range strings.Split(string(data), "\n") {
				if f := strings.Fields(line); len(f) == 3 && f[0] == "def" {
					pw.apeAsmDefs[f[1]] = true
				}
			}
		}
	}
	if pw.apeAsmDefs[path+"."+obj.Name()] {
		return
	}
	pw.linknames[obj] = struct {
		remote string
		std    bool
	}{orig + "." + obj.Name(), false}
}

// Under GOOS=ape, runtime.GOOS is a constant to the type checker ("ape",
// so that it may be used wherever a constant may), but its value is the
// host's, known at run time: an expression that uses it, or a constant
// computed from it, is compiled as a run-time value. (A constant another
// package computes from it is that package's constant.)

// hostDependent reports whether the constant expression x depends on
// runtime.GOOS.
func (pw *pkgWriter) hostDependent(x syntax.Expr) bool {
	if buildcfg.GOOS != "ape" {
		return false
	}
	found := false
	syntax.Inspect(x, func(n syntax.Node) bool {
		if found {
			return false
		}
		if name, ok := n.(*syntax.Name); ok {
			if c, ok := pw.info.Uses[name].(*types2.Const); ok && pw.hostConst(c) {
				found = true
			}
		}
		return true
	})
	return found
}

// So is a constant whose value differs between the hosts: one whose
// package declares, for each host, a constant of the host's name for it
// (cmd/apegen's host copies: PathSeparator has Linux_PathSeparator,
// Freebsd_PathSeparator and Windows_PathSeparator). The type checker
// sees the value the package gives it; a use of it is compiled as the
// element runtime.apeHost() of the hosts' values.

// hostValues returns the constants that are c on each host, or nil.
func hostValues(c *types2.Const) []*types2.Const {
	if buildcfg.GOOS != "ape" || c.Pkg() == nil || c.Parent() != c.Pkg().Scope() {
		return nil
	}
	// (By the runtime's numbers for the hosts: the value of a host
	// the program is not for is never used.)
	out := make([]*types2.Const, len(buildcfg.APEAllHosts[buildcfg.GOARCH]))
	var first *types2.Const
	for _, h := range buildcfg.APEHosts(buildcfg.GOARCH) {
		hc, ok := c.Pkg().Scope().Lookup(apeHostName(h, c.Name())).(*types2.Const)
		if !ok || hc == c {
			return nil
		}
		out[buildcfg.APEHostIndex(buildcfg.GOARCH, h)] = hc
		if first == nil {
			first = hc
		}
	}
	if first == nil {
		return nil
	}
	for i := range out {
		if out[i] == nil {
			out[i] = first
		}
	}
	return out
}

// apeHostName is cmd/apegen's name of a host's copy of name.
func apeHostName(host, name string) string {
	if r, _ := utf8.DecodeRuneInString(name); unicode.IsUpper(r) {
		return strings.ToUpper(host[:1]) + host[1:] + "_" + name
	}
	return host + "_" + name
}

// hostConst reports whether c is runtime.GOOS, differs between hosts,
// or is a constant of this package computed from one that does.
func (pw *pkgWriter) hostConst(c *types2.Const) bool {
	if buildcfg.GOOS != "ape" || c.Pkg() == nil {
		return false
	}
	if c.Pkg().Path() == "runtime" && c.Name() == "GOOS" && c.Parent() == c.Pkg().Scope() {
		return true
	}
	if hostValues(c) != nil {
		return true
	}
	if c.Pkg() != pw.curpkg {
		return false
	}
	if pw.hostConsts == nil {
		pw.hostConsts = map[*types2.Const]int8{}
		pw.constInits = map[*types2.Const]syntax.Expr{}
		for _, f := range pw.files {
			pw.collectConstInits(f)
		}
	}
	switch pw.hostConsts[c] {
	case 1:
		return true
	case 2, 3: // no, or being decided (a cycle)
		return false
	}
	pw.hostConsts[c] = 3
	init, ok := pw.constInits[c]
	v := ok && pw.hostDependent(init)
	if v {
		pw.hostConsts[c] = 1
	} else {
		pw.hostConsts[c] = 2
	}
	return v
}

// collectConstInits records the expression that defines each constant
// of f.
func (pw *pkgWriter) collectConstInits(f *syntax.File) {
	var group *syntax.Group
	var values []syntax.Expr
	syntax.Inspect(f, func(n syntax.Node) bool {
		d, ok := n.(*syntax.ConstDecl)
		if !ok {
			return true
		}
		if d.Values != nil || d.Group == nil || d.Group != group {
			values = syntax.UnpackListExpr(d.Values)
		}
		group = d.Group
		for i, name := range d.NameList {
			if c, ok := pw.info.Defs[name].(*types2.Const); ok && i < len(values) {
				pw.constInits[c] = values[i]
			}
		}
		return true
	})
}

// hostConstExpr writes the use expr of the host constant c.
func (w *writer) hostConstExpr(expr syntax.Expr, c *types2.Const) {
	tv := w.p.typeAndValue(expr)
	typ := idealType(w.gd, tv)
	if c.Pkg().Path() == "runtime" && c.Name() == "GOOS" {
		w.Code(exprAPEGOOS)
		w.pos(expr)
		w.typ(typ)
		return
	}
	if hs := hostValues(c); hs != nil {
		w.Code(exprAPEConst)
		w.pos(expr)
		w.typ(typ)
		w.Len(len(hs))
		for _, h := range hs {
			w.Value(convertConst(h.Val(), typ))
		}
		return
	}
	init := w.p.constInits[c]
	if types2.Identical(typ, idealType(w.gd, w.p.typeAndValue(init))) {
		w.expr(init)
		return
	}
	w.convertExpr(typ, init, false)
}

// convertConst returns v as a value of typ (an integer constant used as
// a float, say).
func convertConst(v constant.Value, typ types2.Type) constant.Value {
	b, ok := typ.Underlying().(*types2.Basic)
	if !ok {
		return v
	}
	switch {
	case b.Info()&types2.IsInteger != 0:
		return constant.ToInt(v)
	case b.Info()&types2.IsFloat != 0:
		return constant.ToFloat(v)
	case b.Info()&types2.IsComplex != 0:
		return constant.ToComplex(v)
	}
	return v
}

// apeTypeDown records t as the type of the untyped operands of the
// constant expression x of type t computed from host constants: the type
// checker types only the whole expression, and they are computed at run
// time as it is.
func (pw *pkgWriter) apeTypeDown(x syntax.Expr, t types2.Type) {
	set := func(e syntax.Expr) {
		if tv := e.GetTypeInfo(); tv.Type != nil && isUntyped(tv.Type) {
			if pw.apeUntypedAs == nil {
				pw.apeUntypedAs = map[syntax.Expr]types2.Type{}
			}
			pw.apeUntypedAs[e] = t
			pw.apeTypeDown(e, t)
		}
	}
	switch x := x.(type) {
	case *syntax.ParenExpr:
		set(x.X)
	case *syntax.Operation:
		switch {
		case x.Y == nil:
			set(x.X)
		case x.Op == syntax.Shl || x.Op == syntax.Shr:
			set(x.X)
		case x.Op == syntax.Eql || x.Op == syntax.Neq || x.Op == syntax.Lss || x.Op == syntax.Leq ||
			x.Op == syntax.Gtr || x.Op == syntax.Geq || x.Op == syntax.AndAnd || x.Op == syntax.OrOr:
		default:
			set(x.X)
			set(x.Y)
		}
	}
}
