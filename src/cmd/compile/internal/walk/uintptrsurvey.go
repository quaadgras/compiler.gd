// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package walk

import (
	"go/constant"

	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/types"
)

// surveyUintptr reports, for -d=uintptrsurvey=1, what fn does with
// values of type uintptr that would not be the same if a uintptr were
// what C's uintptr_t is (doc/gd/cc.md, "Integers that are pointers"): a
// word that the collector looks at, with a bit that says that it is a
// pointer, whose integers have 63 bits. One line for each:
//
//	const        a constant with bit 63 set
//	from-u64     a conversion of a uint64 or a uint to uintptr: its bit 63 is lost
//	from-i64     of an int64 or an int: a negative one is 63 bits
//	to-64        a conversion of a uintptr to a 64-bit integer
//	from-ptr     of an unsafe.Pointer to uintptr: a pointer still
//	to-ptr       of a uintptr to unsafe.Pointer
//	keep         +, -, &, |, &^: a pointer still if the result is in its object, which is to be looked at
//	shift-right  >>: the bits that come in are of 63
//	overflow     *, <<, ^, unary ^ and -: the result may have bit 63
//	divide       / and %
func surveyUintptr(gd *base.Invocation, fn *ir.Func) {
	report := func(n ir.Node, kind string) {
		gd.WarnfAt(n.Pos(), "uintptrsurvey: %s", kind)
	}
	isU := func(t *types.Type) bool { return t != nil && t.Kind() == types.TUINTPTR }
	op := func(n ir.Node, o ir.Op) {
		switch o {
		case ir.OADD, ir.OSUB, ir.OAND, ir.OOR, ir.OANDNOT:
			report(n, "keep")
		case ir.ORSH:
			report(n, "shift-right")
		case ir.OMUL, ir.OLSH, ir.OXOR, ir.OBITNOT, ir.ONEG:
			report(n, "overflow")
		case ir.ODIV, ir.OMOD:
			report(n, "divide")
		}
	}
	ir.Visit(fn, func(n ir.Node) {
		switch n := n.(type) {
		case *ir.BasicLit:
			if isU(n.Type()) && n.Val().Kind() == constant.Int {
				if v, ok := constant.Uint64Val(n.Val()); ok && v>>63 != 0 {
					report(n, "const")
				}
			}
		case *ir.ConvExpr:
			if n.Op() != ir.OCONV && n.Op() != ir.OCONVNOP || n.X.Type() == nil {
				return
			}
			from, to := n.X.Type(), n.Type()
			switch {
			case isU(to) && isU(from):
			case isU(to) && from.IsUnsafePtr():
				report(n, "from-ptr")
			case isU(from) && to.IsUnsafePtr():
				report(n, "to-ptr")
			case isU(to) && from.IsInteger() && from.Size() == 8:
				if ir.IsConst(n.X, constant.Int) {
					return
				}
				if from.IsSigned() {
					report(n, "from-i64")
				} else {
					report(n, "from-u64")
				}
			case isU(from) && to.IsInteger() && to.Size() == 8:
				report(n, "to-64")
			}
		case *ir.BinaryExpr:
			if isU(n.Type()) {
				op(n, n.Op())
			}
		case *ir.UnaryExpr:
			if isU(n.Type()) {
				op(n, n.Op())
			}
		case *ir.AssignOpStmt:
			if isU(n.X.Type()) {
				op(n, n.AsOp)
			}
		}
	})
}
