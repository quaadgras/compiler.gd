// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package walk

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/reflectdata"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
)

// phaseGOutBufAlloc returns, for a new(t) or &t{...} (n) that escape
// analysis placed in the current function's caller's buffer for one
// of its results (escape/phaseg.go), the allocation
// (*t)(runtime.maybeInPlace(outBuf_k, t)): the caller's buffer,
// zeroed, or a new object when it passed none. Else it returns nil.
func phaseGOutBufAlloc(gd *base.Invocation, n ir.Node, t *types.Type, init *ir.Nodes) ir.Node {
	allocs, _ := gd.PhaseGOutBufAllocs.(map[ir.Node]uint8)
	ord := allocs[n]
	if ord == 0 {
		return nil
	}
	outBuf := findOutBufParamName(ir.CurFunc(gd), int(ord)-1)
	if outBuf == nil {
		return nil
	}
	pos := n.Pos()
	var call *ir.CallExpr
	if gd.CObjects {
		// An object of C's on the heap has a byte after it.
		call = mkcall(gd, "maybeInPlaceC", types.Types[types.TUNSAFEPTR], init, outBuf, reflectdata.TypePtrAt(gd, pos, t), reflectdata.TypePtrAt(gd, pos, ir.CObjType(gd, t)))
	} else {
		call = mkcall(gd, "maybeInPlace", types.Types[types.TUNSAFEPTR], init, outBuf, reflectdata.TypePtrAt(gd, pos, t))
	}
	conv := ir.NewConvExpr(gd, pos, ir.OCONVNOP, types.NewPtr(t), call)
	conv.SetTypecheck(1)
	conv.MarkNonNil()
	// The compiler's own conversion, of memory it just made.
	conv.SetCheckPtr(true)
	return typecheck.Expr(gd, conv)
}
