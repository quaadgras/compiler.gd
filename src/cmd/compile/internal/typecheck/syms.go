// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typecheck

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/types"
	"cmd/internal/obj"
)

// LookupRuntime returns a function or variable declared in
// _builtin/runtime.go. If types_ is non-empty, successive occurrences
// of the "any" placeholder type will be substituted.
func LookupRuntime(gd *base.Invocation, name string, types_ ...*types.Type) *ir.Name {
	s := ir.Pkgs(gd).Runtime.Lookup(name)
	if s == nil || s.Def == nil {
		gd.Fatalf("LookupRuntime: can't find runtime.%s", name)
	}
	n := s.Def.(*ir.Name)
	if len(types_) != 0 {
		n = substArgTypes(gd, n, types_...)
	}
	return n
}

// SubstArgTypes substitutes the given list of types for
// successive occurrences of the "any" placeholder in the
// type syntax expression n.Type.
func substArgTypes(gd *base.Invocation, old *ir.Name, types_ ...*types.Type) *ir.Name {
	for _, t := range types_ {
		types.CalcSize(t)
	}
	n := ir.NewNameAt(gd, old.Pos(), old.Sym(), types.SubstAny(old.Type(), &types_))
	n.Class = old.Class
	n.Func = old.Func
	if len(types_) > 0 {
		gd.Fatalf("SubstArgTypes: too many argument types")
	}
	return n
}

// AutoLabel generates a new Name node for use with
// an automatically generated label.
// prefix is a short mnemonic (e.g. ".s" for switch)
// to help with debugging.
// It should begin with "." to avoid conflicts with
// user labels.
func AutoLabel(gd *base.Invocation, prefix string) *types.Sym {
	if prefix[0] != '.' {
		gd.Fatalf("autolabel prefix must start with '.', have %q", prefix)
	}
	fn := ir.CurFunc(gd)
	if ir.CurFunc(gd) == nil {
		gd.Fatalf("autolabel outside function")
	}
	n := fn.Label
	fn.Label++
	return LookupNum(gd, prefix, int(n))
}

func Lookup(gd *base.Invocation, name string) *types.Sym {
	return types.LocalPkg(gd).Lookup(name)
}

// InitRuntime loads the definitions for the low-level runtime functions,
// so that the compiler can generate calls to them,
// but does not make them visible to user code.
func InitRuntime(gd *base.Invocation) {
	gd.Timer.Start("fe", "loadsys")

	typs := runtimeTypes(gd)
	for _, d := range &runtimeDecls {
		sym := ir.Pkgs(gd).Runtime.Lookup(d.name)
		typ := typs[d.typ]
		switch d.tag {
		case funcTag:
			importfunc(gd, sym, typ)
		case varTag:
			importvar(gd, sym, typ)
		default:
			gd.Fatalf("unhandled declaration tag %v", d.tag)
		}
	}
}

// LookupRuntimeFunc looks up Go function name in package runtime. This function
// must follow the internal calling convention.
func LookupRuntimeFunc(gd *base.Invocation, name string) *obj.LSym {
	return LookupRuntimeABI(gd, name, obj.ABIInternal)
}

// LookupRuntimeVar looks up a variable (or assembly function) name in package
// runtime. If this is a function, it may have a special calling
// convention.
func LookupRuntimeVar(gd *base.Invocation, name string) *obj.LSym {
	return LookupRuntimeABI(gd, name, obj.ABI0)
}

// LookupRuntimeABI looks up a name in package runtime using the given ABI.
func LookupRuntimeABI(gd *base.Invocation, name string, abi obj.ABI) *obj.LSym {
	return gd.PkgLinksym("runtime", name, abi)
}

// InitCoverage loads the definitions for routines called
// by code coverage instrumentation (similar to InitRuntime above).
func InitCoverage(gd *base.Invocation) {
	typs := coverageTypes(gd)
	for _, d := range &coverageDecls {
		sym := ir.Pkgs(gd).Coverage.Lookup(d.name)
		typ := typs[d.typ]
		switch d.tag {
		case funcTag:
			importfunc(gd, sym, typ)
		case varTag:
			importvar(gd, sym, typ)
		default:
			gd.Fatalf("unhandled declaration tag %v", d.tag)
		}
	}
}

// LookupCoverage looks up the Go function 'name' in package
// runtime/coverage. This function must follow the internal calling
// convention.
func LookupCoverage(gd *base.Invocation, name string) *ir.Name {
	sym := ir.Pkgs(gd).Coverage.Lookup(name)
	if sym == nil {
		gd.Fatalf("LookupCoverage: can't find runtime/coverage.%s", name)
	}
	return sym.Def.(*ir.Name)
}
