// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typecheck

import (
	"fmt"
	"sync"

	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/types"
	"cmd/internal/src"
)

// funcStackOf returns the per-Invocation CurFunc stack, the inverse
// of which is what gd.TypecheckFuncStack stores. Lazy-initialised.
func funcStackOf(gd *base.Invocation) []*ir.Func {
	s, _ := gd.TypecheckFuncStack.([]*ir.Func)
	return s
}

// DeclFunc declares the parameters for fn and adds it to
// Target.Funcs.
//
// Before returning, it sets CurFunc to fn. When the caller is done
// constructing fn, it must call FinishFuncBody to restore CurFunc.
func DeclFunc(gd *base.Invocation, fn *ir.Func) {
	fn.DeclareParams(gd, true)
	fn.Nname.Defn = fn
	Target(gd).Funcs = append(Target(gd).Funcs, fn)

	gd.TypecheckFuncStack = append(funcStackOf(gd), ir.CurFunc(gd))
	gd.CurFunc = fn
}

// FinishFuncBody restores ir.CurFunc to its state before the last
// call to DeclFunc.
func FinishFuncBody(gd *base.Invocation) {
	s := funcStackOf(gd)
	gd.TypecheckFuncStack, gd.CurFunc = s[:len(s)-1], s[len(s)-1]
}

func CheckFuncStack(gd *base.Invocation) {
	if s := funcStackOf(gd); len(s) != 0 {
		gd.Fatalf("funcStack is non-empty: %v", len(s))
	}
}

// TempAt makes a new Node off the books.
//
// N.B., the new Node is a function-local variable defaulting to function scope.
// It helps in some cases if an ODCL is also created and placed in a narrower scope,
// such as if the variable can be used in a loop body and potentially escape.
// TODO: Consider some mechanism to more conveniently create a block scoped temporary.
func TempAt(gd *base.Invocation, pos src.XPos, curfn *ir.Func, typ *types.Type) *ir.Name {
	if curfn == nil {
		gd.FatalfAt(pos, "no curfn for TempAt")
	}
	if typ == nil {
		gd.FatalfAt(pos, "TempAt called with nil type")
	}
	if typ.Kind() == types.TFUNC && typ.Recv() != nil {
		gd.FatalfAt(pos, "misuse of method type: %v", typ)
	}
	types.CalcSize(typ)

	sym := &types.Sym{
		Name: autotmpname(len(curfn.Dcl)),
		Pkg:  types.LocalPkg(gd),
	}
	name := curfn.NewLocal(gd, pos, sym, typ)
	name.SetEsc(ir.EscNever)
	name.SetUsed(true)
	name.SetAutoTemp(true)

	return name
}

var (
	autotmpnamesmu sync.Mutex
	autotmpnames   []string
)

// autotmpname returns the name for an autotmp variable numbered n.
func autotmpname(n int) string {
	autotmpnamesmu.Lock()
	defer autotmpnamesmu.Unlock()

	// Grow autotmpnames, if needed.
	if n >= len(autotmpnames) {
		autotmpnames = append(autotmpnames, make([]string, n+1-len(autotmpnames))...)
		autotmpnames = autotmpnames[:cap(autotmpnames)]
	}

	s := autotmpnames[n]
	if s == "" {
		// Give each tmp a different name so that they can be registerized.
		// Add a preceding . to avoid clashing with legal names.
		prefix := ".autotmp_%d"

		s = fmt.Sprintf(prefix, n)
		autotmpnames[n] = s
	}
	return s
}

// f is method type, with receiver.
// return function type, receiver as first argument (or not).
func NewMethodType(gd *base.Invocation, sig *types.Type, recv *types.Type) *types.Type {
	nrecvs := 0
	if recv != nil {
		nrecvs++
	}

	// TODO(mdempsky): Move this function to types.

	sigParams := sig.Params()

	params := make([]*types.Field, nrecvs+len(sigParams))
	if recv != nil {
		params[0] = types.NewField(gd.Pos, nil, recv)
	}
	for i, param := range sigParams {
		// Preserve Sym (unrelated to gd Phase G; mdempsky's TODO
		// about NewMethodType losing names applies here).
		d := types.NewField(gd.Pos, param.Sym, param.Type)
		d.SetIsDDD(param.IsDDD())
		// Note: escape-analysis Notes aren't copied here because
		// NewMethodType runs during typecheck, before escape
		// analysis writes them. Downstream consumers that need the
		// Notes (e.g. reflectdata's gd escape-bits itab mask) read
		// them directly from the original method signature via
		// typeSig.origType.
		params[nrecvs+i] = d
	}

	results := make([]*types.Field, sig.NumResults())
	for i, t := range sig.Results() {
		results[i] = types.NewField(gd.Pos, nil, t.Type)
	}

	return types.NewSignature(gd, nil, params, results)
}
