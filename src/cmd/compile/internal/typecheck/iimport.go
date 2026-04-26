// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Indexed package import.
// See iexport.go for the export data format.

package typecheck

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/types"
)

// HaveInlineBody reports whether we have fn's inline body available
// for inlining.
//
// It's a function literal so that it can be overridden for
// GOEXPERIMENT=unified. Takes gd so noder's implementation can read
// its per-Invocation bodyReader/importBodyReader maps without a
// package-level fallback.
var HaveInlineBody = func(gd *base.Invocation, fn *ir.Func) bool {
	panic("HaveInlineBody not overridden")
}

func SetBaseTypeIndex(gd *base.Invocation, t *types.Type, i, pi int64) {
	if t.Obj() == nil {
		gd.Fatalf("SetBaseTypeIndex on non-defined type %v", t)
	}
	if i != -1 && pi != -1 {
		m, _ := gd.TypecheckTypeSymIdx.(map[*types.Type][2]int64)
		if m == nil {
			m = make(map[*types.Type][2]int64)
			gd.TypecheckTypeSymIdx = m
		}
		m[t] = [2]int64{i, pi}
	}
}

// BaseTypeIndex returns the imported type's descriptor index, or -1 if
// not registered. The map is per-Invocation (gd.TypecheckTypeSymIdx)
// so concurrent compiles don't share descriptor indices.
// TODO(mdempsky): Store this information directly in the Type's Name.
func BaseTypeIndex(gd *base.Invocation, t *types.Type) int64 {
	tbase := t
	if t.IsPtr() && t.Sym() == nil && t.Elem().Sym() != nil {
		tbase = t.Elem()
	}
	m, _ := gd.TypecheckTypeSymIdx.(map[*types.Type][2]int64)
	i, ok := m[tbase]
	if !ok {
		return -1
	}
	if t != tbase {
		return i[1]
	}
	return i[0]
}
