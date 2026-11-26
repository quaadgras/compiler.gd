// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"fmt"
	"go/ast"
	gotoken "go/token"
	"regexp"
	"strings"

	"cmd/internal/edit"
)

// The Go spelling of cgo's names for C's basic types.
var cgoBasic = map[string]string{
	"char": "int8", "schar": "int8", "uchar": "uint8",
	"short": "int16", "ushort": "uint16", "int": "int32", "uint": "uint32",
	"long": "int64", "ulong": "uint64", "longlong": "int64", "ulonglong": "uint64",
	"float": "float32", "double": "float64",
}

// rewrite rewrites a Go file that imports "C" as cgo does: its references
// to C's names (C.f, C.struct_node, C.int, C.CString...) become the Go that
// g generated for them, and the import of "C" becomes one of internal/c23.
//
// C's types are the Go types that mirror them and its pointers are Go's,
// so a call of a C function is a call of the Go function it was
// translated to, with the arguments as they are: but that void * is
// *c23.Void where cgo has unsafe.Pointer, which an argument is
// converted from and a result to.
//
// C.uintptr_t and C.intptr_t are uintptr and int, as with cgo (a
// cgo.Handle is passed as one), and an argument or a result of such a
// type is converted to and from what C has for it. A field of such a
// type in a struct of C's is not an integer of Go's: it is a
// c23.Intcap, which Go code does not convert.
func (g *gen) rewrite(fset *gotoken.FileSet, f *ast.File, src []byte) (out []byte, err error) {
	defer catch(&err)
	tf := fset.File(f.Pos())
	g.out = &strings.Builder{}
	off := func(p gotoken.Pos) int { return tf.Offset(p) }
	line := func(p gotoken.Pos) int { return fset.Position(p).Line }
	b := edit.NewBuffer(src)

	for _, imp := range f.Imports {
		if imp.Path.Value == `"C"` {
			b.Replace(off(imp.Path.Pos()), off(imp.Path.End()), `_c23 "internal/c23"`)
		}
	}
	isC := func(e ast.Expr) (string, bool) {
		sel, ok := e.(*ast.SelectorExpr)
		if !ok {
			return "", false
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok || id.Name != "C" || id.Obj != nil {
			return "", false
		}
		return sel.Sel.Name, true
	}
	done := map[ast.Expr]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			name, ok := isC(n.Fun)
			if !ok {
				return true
			}
			done[n.Fun] = true
			g.rewriteCall(b, off, line(n.Pos()), n, name)
		case *ast.SelectorExpr:
			name, ok := isC(n)
			if !ok || done[n] {
				return true
			}
			b.Replace(off(n.Pos()), off(n.End()), g.cName(line(n.Pos()), name))
		}
		return true
	})
	b.Insert(len(src), "\nvar _ = _c23.Addr\n")
	name := tf.Name()
	return []byte(fmt.Sprintf("//line %s:1\n%s", name, b.Bytes())), nil
}

// cgoHelpers are the functions of cgo's that C.name may name.
var cgoHelpers = map[string]bool{"CString": true, "GoString": true, "free": true, "malloc": true}

// cDefined reports whether C.name means something in u: what cName
// and rewriteCall accept.
func cDefined(u *unit, name string) bool {
	if _, ok := cgoBasic[name]; ok || cgoHelpers[name] || u.scope[name] != nil {
		return true
	}
	for _, kw := range []string{"struct_", "union_", "enum_"} {
		if t := u.tags[strings.TrimPrefix(name, kw)]; strings.HasPrefix(name, kw) && t != nil {
			if kw == "enum_" {
				return t.enum != nil
			}
			return t.kind == kStruct && t.rec.union == (kw == "union_")
		}
	}
	return false
}

// c23Name is c23 in the Go that g generates, where the Go file that
// imports "C" has _c23.
var c23Name = regexp.MustCompile(`\bc23\.`)

func cgoQual(s string) string { return c23Name.ReplaceAllString(s, "_c23.") }

// cName returns the Go for C.name used as a type or a value.
func (g *gen) cName(line int, name string) string {
	if t, ok := cgoBasic[name]; ok {
		return t
	}
	for _, kw := range []string{"struct_", "union_", "enum_"} {
		if len(name) > len(kw) && name[:len(kw)] == kw {
			if t := g.u.tags[name[len(kw):]]; t != nil {
				if t.kind == kStruct && t.rec.union == (kw == "union_") && kw != "enum_" {
					return g.mirror(t)
				}
				if kw == "enum_" && t.enum != nil {
					return cgoQual(goType(t))
				}
			}
		}
	}
	s := g.u.scope[name]
	if s != nil && s.kind == symFunc && !s.defined && cgoHelpers[name] {
		// <stdlib.h>'s free or malloc: cgo's, on unsafe.Pointer.
		s = nil
	}
	if s != nil {
		switch s.kind {
		case symTypedef:
			if s.t.isCap() {
				// uintptr_t and intptr_t are integers of Go's, as
				// with cgo: see rewriteCall.
				if s.t.kind == kUIntCap {
					return "uintptr"
				}
				return "int"
			}
			return cgoQual(g.mirror(s.t))
		case symEnumConst:
			return fmt.Sprintf("(%d)", s.val)
		case symVar:
			// The Go variable that is the object.
			at := pos{g.u.file, line}
			return "(" + cgoQual(g.tlv(&varRef{node{s.t, at}, s}, false).x) + ")"
		case symFunc:
			g.errorf(pos{g.u.file, line}, "C.%s used as a value", name)
		}
	}
	switch name {
	case "CString":
		return "_c23.CStringGo"
	case "GoString":
		return "_c23.GoStringGo"
	case "free":
		return "_c23.FreeGo"
	case "malloc":
		return "_c23.MallocGo"
	}
	g.errorf(pos{g.u.file, line}, "C.%s is not defined", name)
	return ""
}

// rewriteCall rewrites a call of C.name.
func (g *gen) rewriteCall(b *edit.Buffer, off func(gotoken.Pos) int, line int, call *ast.CallExpr, name string) {
	s := g.u.scope[name]
	if s != nil && s.kind == symFunc && s.linkage == 'e' && g.ext[name] != nil {
		s = g.ext[name]
	}
	if s == nil || s.kind != symFunc || !s.defined {
		// A type conversion (C.int(x)) or a helper (C.CString(s)).
		b.Replace(off(call.Fun.Pos()), off(call.Fun.End()), g.cName(line, name))
		return
	}
	ft := s.t.fn
	at := pos{g.u.file, line}
	if ft.variadic {
		g.errorf(at, "C.%s: variadic functions are not supported", name)
	}
	if len(call.Args) != len(ft.params) {
		g.errorf(at, "C.%s takes %d arguments", name, len(ft.params))
	}
	for _, pt := range ft.params {
		if pt.kind == kStruct {
			g.errorf(at, "C.%s: struct parameters are not supported", name)
		}
	}
	switch {
	case ft.ret.isPtr() && g.gt(ft.ret) == voidPtr:
		// void *: unsafe.Pointer, as with cgo.
		b.Insert(off(call.Pos()), "_c23.ToGoUnsafe(")
		b.Insert(off(call.End()), ")")
	case ft.ret.kind == kStruct:
		g.errorf(at, "C.%s: struct results are not supported", name)
	case ft.ret.kind == kBool:
		b.Insert(off(call.Pos()), "(")
		b.Insert(off(call.End()), " != 0)")
	case ft.ret.kind == kUIntCap:
		// uintptr_t: a uintptr, as with cgo. What C made of a pointer
		// is its address.
		b.Insert(off(call.Pos()), "_c23.CapUint(")
		b.Insert(off(call.End()), ")")
	case ft.ret.kind == kIntCap:
		b.Insert(off(call.Pos()), "_c23.CapSintOf(")
		b.Insert(off(call.End()), ")")
	}
	b.Replace(off(call.Fun.Pos()), off(call.Fun.End()), g.names[s])
	for i, a := range call.Args {
		if pt := ft.params[i]; pt.isCap() {
			// An integer of Go's: to C it is an integer, which is no
			// pointer to anything if C converts it to one.
			conv := "_c23.IntToCap(int64("
			if pt.kind == kUIntCap {
				conv = "_c23.UintToCap(uint64("
			}
			b.Insert(off(a.Pos()), conv)
			b.Insert(off(a.End()), "))")
			continue
		}
		if !ft.params[i].isPtr() || g.gt(ft.params[i]) != voidPtr {
			continue // a Go pointer of the parameter's type, or no pointer
		}
		if id, ok := a.(*ast.Ident); ok && id.Name == "nil" {
			continue
		}
		// void *, or a pointer to a function: an unsafe.Pointer.
		b.Insert(off(a.Pos()), "(*_c23.Void)(")
		b.Insert(off(a.End()), ")")
	}
}
