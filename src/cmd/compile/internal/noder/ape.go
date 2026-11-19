// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package noder

import (
	"internal/buildcfg"
	"os"
	"strings"

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

// hostConst reports whether c is runtime.GOOS or a constant of this
// package computed from it.
func (pw *pkgWriter) hostConst(c *types2.Const) bool {
	if buildcfg.GOOS != "ape" || c.Pkg() == nil {
		return false
	}
	if c.Pkg().Path() == "runtime" && c.Name() == "GOOS" && c.Parent() == c.Pkg().Scope() {
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
	init := w.p.constInits[c]
	if types2.Identical(typ, idealType(w.gd, w.p.typeAndValue(init))) {
		w.expr(init)
		return
	}
	w.convertExpr(typ, init, false)
}
