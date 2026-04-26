// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typecheck

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/types"
	"cmd/internal/src"
)

// importfunc declares symbol s as an imported function with type t.
func importfunc(gd *base.Invocation, s *types.Sym, t *types.Type) {
	fn := ir.NewFunc(gd, src.NoXPos, src.NoXPos, s, t)
	importsym(gd, fn.Nname)
}

// importvar declares symbol s as an imported variable with type t.
func importvar(gd *base.Invocation, s *types.Sym, t *types.Type) {
	n := ir.NewNameAt(gd, src.NoXPos, s, t)
	n.Class = ir.PEXTERN
	importsym(gd, n)
}

func importsym(gd *base.Invocation, name *ir.Name) {
	sym := name.Sym()
	if sym.Def != nil {
		gd.Fatalf("importsym of symbol that already exists: %v", sym.Def)
	}
	sym.Def = name
}
