// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package noder

import (
	"strings"

	"cmd/compile/internal/reflectdata"
	"cmd/compile/internal/syntax"
	"cmd/compile/internal/types2"
	"cmd/internal/obj"
	"cmd/internal/objabi"
)

// The methods of one type, for reflection.
//
// A function that calls Method or Methods of a reflect.Type has the linker
// keep the exported methods of every type in the program, as the type is
// not known until the program runs (see usemethod in package walk). In
//
//	reflect.TypeFor[T]().Methods()
//
// it is known, so the writer calls reflect.typeForMethods[T] there instead,
// which returns the same iterator and is not such a function. The
// dictionary of each of its instances has a relocation (R_USEALLMETHODS)
// with the type argument, for which the linker keeps the exported methods of
// that type: T may be a type parameter of the function that the expression
// is in, as the dictionaries of that function refer to the dictionaries of
// the instances it calls.

// reflectTypeForMethods is the name of the function in package reflect.
const reflectTypeForMethods = "typeForMethods"

// isReflectTypeForMethods reports whether the object of the package with
// that path is reflect.typeForMethods, which the export data of reflect
// lists for the packages that import it, although it is not exported.
func isReflectTypeForMethods(path, name string) bool {
	return path == "reflect" && name == reflectTypeForMethods
}

// typeForMethods reports whether call is reflect.TypeFor[T]().Methods():
// it returns reflect.typeForMethods and its type arguments then, and the
// call of reflect.TypeFor.
func (pw *pkgWriter) typeForMethods(call *syntax.CallExpr) (*types2.Func, []types2.Type, *syntax.CallExpr) {
	if len(call.ArgList) != 0 {
		return nil, nil, nil
	}
	sel, ok := syntax.Unparen(call.Fun).(*syntax.SelectorExpr)
	if !ok || sel.Sel.Value != "Methods" {
		return nil, nil, nil
	}
	typeFor, ok := syntax.Unparen(sel.X).(*syntax.CallExpr)
	if !ok || len(typeFor.ArgList) != 0 {
		return nil, nil, nil
	}
	obj, inst := lookupObj(pw, syntax.Unparen(typeFor.Fun))
	fn, ok := obj.(*types2.Func)
	if !ok || fn.Name() != "TypeFor" || fn.Pkg() == nil || fn.Pkg().Path() != "reflect" || !isGlobal(fn) || inst.TypeArgs.Len() != 1 {
		return nil, nil, nil
	}
	helper, ok := fn.Pkg().Scope().Lookup(reflectTypeForMethods).(*types2.Func)
	if !ok {
		return nil, nil, nil
	}
	return helper, asTypeSlice(inst.TypeArgs), typeFor
}

// useAllMethods adds to the dictionary lsym of an instance of
// reflect.typeForMethods the relocation that has the linker keep the
// exported methods of its type argument.
func (pr *pkgReader) useAllMethods(dict *readerDict, lsym *obj.LSym) {
	name, _, _ := strings.Cut(dict.baseSym.Name, "[") // the type arguments follow.
	if !isReflectTypeForMethods(dict.baseSym.Pkg.Path, name) || len(dict.targs) != 1 {
		return
	}
	lsym.AddRel(pr.gd.Ctxt, obj.Reloc{
		Type: objabi.R_USEALLMETHODS,
		Sym:  reflectdata.TypeLinksym(pr.gd, dict.targs[0]),
	})
}
