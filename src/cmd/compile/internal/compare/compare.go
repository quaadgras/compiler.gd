// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package compare contains code for generating comparison
// routines for structs, strings and interfaces.
package compare

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
	"fmt"
	"math/bits"
	"sort"
)

// IsRegularMemory reports whether t can be compared/hashed as regular memory.
func IsRegularMemory(gd *base.Invocation, t *types.Type) bool {
	return types.AlgType(gd, t) == types.AMEM
}

// Memrun finds runs of struct fields for which memory-only algs are appropriate.
// t is the parent struct type, and start is the field index at which to start the run.
// size is the length in bytes of the memory included in the run.
// next is the index just after the end of the memory run.
func Memrun(gd *base.Invocation, t *types.Type, start int) (size int64, next int) {
	next = start
	for {
		next++
		if next == t.NumFields() {
			break
		}
		// Stop run after a padded field.
		if types.IsPaddedField(t, next-1) {
			break
		}
		// Also, stop before a blank or non-memory field.
		if f := t.Field(next); f.Sym.IsBlank() || !IsRegularMemory(gd, f.Type) {
			break
		}
		// For issue 46283, don't combine fields if the resulting load would
		// require a larger alignment than the component fields.
		if gd.Ctxt.Arch.Alignment > 1 {
			align := t.Alignment()
			if off := t.Field(start).Offset; off&(align-1) != 0 {
				// Offset is less aligned than the containing type.
				// Use offset to determine alignment.
				align = 1 << uint(bits.TrailingZeros64(uint64(off)))
			}
			size := t.Field(next).End() - t.Field(start).Offset
			if size > align {
				break
			}
		}
	}
	return t.Field(next-1).End() - t.Field(start).Offset, next
}

// EqCanPanic reports whether == on type t could panic (has an interface somewhere).
// t must be comparable.
func EqCanPanic(t *types.Type) bool {
	switch t.Kind() {
	default:
		return false
	case types.TINTER:
		return true
	case types.TARRAY:
		return EqCanPanic(t.Elem())
	case types.TSTRUCT:
		for _, f := range t.Fields() {
			if !f.Sym.IsBlank() && EqCanPanic(f.Type) {
				return true
			}
		}
		return false
	}
}

// EqStructCost returns the cost of an equality comparison of two structs.
//
// The cost is determined using an algorithm which takes into consideration
// the size of the registers in the current architecture and the size of the
// memory-only fields in the struct.
func EqStructCost(gd *base.Invocation, t *types.Type) int64 {
	cost := int64(0)

	for i, fields := 0, t.Fields(); i < len(fields); {
		f := fields[i]

		// Skip blank-named fields.
		if f.Sym.IsBlank() {
			i++
			continue
		}

		n, _, next := eqStructFieldCost(gd, t, i)

		cost += n
		i = next
	}

	return cost
}

// eqStructFieldCost returns the cost of an equality comparison of two struct fields.
// t is the parent struct type, and i is the index of the field in the parent struct type.
// eqStructFieldCost may compute the cost of several adjacent fields at once. It returns
// the cost, the size of the set of fields it computed the cost for (in bytes), and the
// index of the first field not part of the set of fields for which the cost
// has already been calculated.
func eqStructFieldCost(gd *base.Invocation, t *types.Type, i int) (int64, int64, int) {
	var (
		cost    = int64(0)
		regSize = int64(types.RegSize)

		size int64
		next int
	)

	if gd.Ctxt.Arch.CanMergeLoads {
		// If we can merge adjacent loads then we can calculate the cost of the
		// comparison using the size of the memory run and the size of the registers.
		size, next = Memrun(gd, t, i)
		cost = size / regSize
		if size%regSize != 0 {
			cost++
		}
		return cost, size, next
	}

	// If we cannot merge adjacent loads then we have to use the size of the
	// field and take into account the type to determine how many loads and compares
	// are needed.
	ft := t.Field(i).Type
	size = ft.Size()
	next = i + 1

	return calculateCostForType(gd, ft), size, next
}

func calculateCostForType(gd *base.Invocation, t *types.Type) int64 {
	var cost int64
	switch t.Kind() {
	case types.TSTRUCT:
		return EqStructCost(gd, t)
	case types.TSLICE:
		// Slices are not comparable.
		gd.Fatalf("calculateCostForType: unexpected slice type")
	case types.TARRAY:
		elemCost := calculateCostForType(gd, t.Elem())
		cost = t.NumElem() * elemCost
	case types.TSTRING, types.TINTER, types.TCOMPLEX64, types.TCOMPLEX128:
		cost = 2
	case types.TINT64, types.TUINT64:
		cost = 8 / int64(types.RegSize)
	default:
		cost = 1
	}
	return cost
}

// EqStruct compares two structs np and nq for equality.
// It works by building a list of boolean conditions to satisfy.
// Conditions must be evaluated in the returned order and
// properly short-circuited by the caller.
// The first return value is the flattened list of conditions,
// the second value is a boolean indicating whether any of the
// comparisons could panic.
func EqStruct(gd *base.Invocation, t *types.Type, np, nq ir.Node) ([]ir.Node, bool) {
	// The conditions are a list-of-lists. Conditions are reorderable
	// within each inner list. The outer lists must be evaluated in order.
	var conds [][]ir.Node
	conds = append(conds, []ir.Node{})
	and := func(n ir.Node) {
		i := len(conds) - 1
		conds[i] = append(conds[i], n)
	}

	// Walk the struct using memequal for runs of AMEM
	// and calling specific equality tests for the others.
	for i, fields := 0, t.Fields(); i < len(fields); {
		f := fields[i]

		// Skip blank-named fields.
		if f.Sym.IsBlank() {
			i++
			continue
		}

		typeCanPanic := EqCanPanic(f.Type)

		// Compare non-memory fields with field equality.
		if !IsRegularMemory(gd, f.Type) {
			if typeCanPanic {
				// Enforce ordering by starting a new set of reorderable conditions.
				conds = append(conds, []ir.Node{})
			}
			switch {
			case f.Type.IsString():
				p := typecheck.DotField(gd, gd.Pos, typecheck.Expr(gd, np), i)
				q := typecheck.DotField(gd, gd.Pos, typecheck.Expr(gd, nq), i)
				eqlen, eqmem := EqString(gd, p, q)
				and(eqlen)
				and(eqmem)
			default:
				and(eqfield(gd, np, nq, i))
			}
			if typeCanPanic {
				// Also enforce ordering after something that can panic.
				conds = append(conds, []ir.Node{})
			}
			i++
			continue
		}

		cost, size, next := eqStructFieldCost(gd, t, i)
		if cost <= 4 {
			// Cost of 4 or less: use plain field equality.
			for j := i; j < next; j++ {
				and(eqfield(gd, np, nq, j))
			}
		} else {
			// Higher cost: use memequal.
			cc := eqmem(gd, np, nq, i, size)
			and(cc)
		}
		i = next
	}

	// Sort conditions to put runtime calls last.
	// Preserve the rest of the ordering.
	var flatConds []ir.Node
	for _, c := range conds {
		isCall := func(n ir.Node) bool {
			return n.Op() == ir.OCALL || n.Op() == ir.OCALLFUNC
		}
		sort.SliceStable(c, func(i, j int) bool {
			return !isCall(c[i]) && isCall(c[j])
		})
		flatConds = append(flatConds, c...)
	}
	return flatConds, len(conds) > 1
}

// EqString returns the nodes
//
//	len(s) == len(t)
//
// and
//
//	memequal(s.ptr, t.ptr, len(s))
//
// which can be used to construct string equality comparison.
// eqlen must be evaluated before eqmem, and shortcircuiting is required.
func EqString(gd *base.Invocation, s, t ir.Node) (eqlen *ir.BinaryExpr, eqmem *ir.CallExpr) {
	s = typecheck.Conv(gd, s, types.Types[types.TSTRING])
	t = typecheck.Conv(gd, t, types.Types[types.TSTRING])
	sptr := ir.NewConvExpr(gd, gd.Pos, ir.OCONVNOP, types.Types[types.TUNSAFEPTR], ir.NewUnaryExpr(gd, gd.Pos, ir.OSPTR, s))
	tptr := ir.NewConvExpr(gd, gd.Pos, ir.OCONVNOP, types.Types[types.TUNSAFEPTR], ir.NewUnaryExpr(gd, gd.Pos, ir.OSPTR, t))
	slen := typecheck.Conv(gd, ir.NewUnaryExpr(gd, gd.Pos, ir.OLEN, s), types.Types[types.TUINTPTR])
	tlen := typecheck.Conv(gd, ir.NewUnaryExpr(gd, gd.Pos, ir.OLEN, t), types.Types[types.TUINTPTR])

	// Pick the 3rd arg to memequal. Both slen and tlen are fine to use, because we short
	// circuit the memequal call if they aren't the same. But if one is a constant some
	// memequal optimizations are easier to apply.
	probablyConstant := func(n ir.Node) bool {
		if n.Op() == ir.OCONVNOP {
			n = n.(*ir.ConvExpr).X
		}
		if n.Op() == ir.OLITERAL {
			return true
		}
		if n.Op() != ir.ONAME {
			return false
		}
		name := n.(*ir.Name)
		if name.Class != ir.PAUTO {
			return false
		}
		if def := name.Defn; def == nil {
			// n starts out as the empty string
			return true
		} else if def.Op() == ir.OAS && (def.(*ir.AssignStmt).Y == nil || def.(*ir.AssignStmt).Y.Op() == ir.OLITERAL) {
			// n starts out as a constant string
			return true
		}
		return false
	}
	cmplen := slen
	if probablyConstant(t) && !probablyConstant(s) {
		cmplen = tlen
	}

	fn := typecheck.LookupRuntime(gd, "memequal")
	call := typecheck.Call(gd, gd.Pos, fn, []ir.Node{sptr, tptr, ir.Copy(cmplen)}, false).(*ir.CallExpr)

	cmp := ir.NewBinaryExpr(gd, gd.Pos, ir.OEQ, slen, tlen)
	cmp = typecheck.Expr(gd, cmp).(*ir.BinaryExpr)
	cmp.SetType(types.Types[types.TBOOL])
	return cmp, call
}

// EqInterface returns the nodes
//
//	s.tab == t.tab (or s.typ == t.typ, as appropriate)
//
// and
//
//	ifaceeq(s.tab, s.data, t.data) (or efaceeq(s.typ, s.data, t.data), as appropriate)
//
// which can be used to construct interface equality comparison.
// eqtab must be evaluated before eqdata, and shortcircuiting is required.
func EqInterface(gd *base.Invocation, s, t ir.Node) (eqtab *ir.BinaryExpr, eqdata *ir.CallExpr) {
	if !types.Identical(s.Type(), t.Type()) {
		gd.Fatalf("EqInterface %v %v", s.Type(), t.Type())
	}
	// gd fat-iface: efaceeq / ifaceeq take pointers to the full iface
	// headers (tab + data + inline) so the runtime can dispatch on the
	// concrete type's storage mode (direct / inline / spread / boxed).
	// Under Phase D a spread type's value spans data + inline, so the
	// old {itab, data, data} signature couldn't reach the inline half.
	// func ifaceeq(tab *uintptr, x, y *iface) (ret bool)
	// func efaceeq(typ *uintptr, x, y *eface) (ret bool)
	var fn ir.Node
	if s.Type().IsEmptyInterface() {
		fn = typecheck.LookupRuntime(gd, "efaceeq")
	} else {
		fn = typecheck.LookupRuntime(gd, "ifaceeq")
	}

	stab := ir.NewUnaryExpr(gd, gd.Pos, ir.OITAB, s)
	ttab := ir.NewUnaryExpr(gd, gd.Pos, ir.OITAB, t)
	saddr := typecheck.NodAddr(gd, s)
	taddr := typecheck.NodAddr(gd, t)
	saddr.SetType(types.Types[types.TUNSAFEPTR])
	taddr.SetType(types.Types[types.TUNSAFEPTR])
	saddr.SetTypecheck(1)
	taddr.SetTypecheck(1)

	call := typecheck.Call(gd, gd.Pos, fn, []ir.Node{stab, saddr, taddr}, false).(*ir.CallExpr)

	cmp := ir.NewBinaryExpr(gd, gd.Pos, ir.OEQ, stab, ttab)
	cmp = typecheck.Expr(gd, cmp).(*ir.BinaryExpr)
	cmp.SetType(types.Types[types.TBOOL])
	return cmp, call
}

// eqfield returns the node
//
//	p.field == q.field
func eqfield(gd *base.Invocation, p, q ir.Node, field int) ir.Node {
	nx := typecheck.DotField(gd, gd.Pos, typecheck.Expr(gd, p), field)
	ny := typecheck.DotField(gd, gd.Pos, typecheck.Expr(gd, q), field)
	return typecheck.Expr(gd, ir.NewBinaryExpr(gd, gd.Pos, ir.OEQ, nx, ny))
}

// eqmem returns the node
//
//	memequal(&p.field, &q.field, size)
func eqmem(gd *base.Invocation, p, q ir.Node, field int, size int64) ir.Node {
	nx := typecheck.Expr(gd, typecheck.NodAddr(gd, typecheck.DotField(gd, gd.Pos, p, field)))
	ny := typecheck.Expr(gd, typecheck.NodAddr(gd, typecheck.DotField(gd, gd.Pos, q, field)))

	fn, needsize := eqmemfunc(gd, size, nx.Type().Elem())
	call := ir.NewCallExpr(gd, gd.Pos, ir.OCALL, fn, nil)
	call.Args.Append(nx)
	call.Args.Append(ny)
	if needsize {
		call.Args.Append(ir.NewInt(gd, gd.Pos, size))
	}

	return call
}

func eqmemfunc(gd *base.Invocation, size int64, t *types.Type) (fn *ir.Name, needsize bool) {
	if !gd.Ctxt.Arch.CanMergeLoads && t.Alignment() < int64(gd.Ctxt.Arch.Alignment) && t.Alignment() < t.Size() {
		// We can't use larger comparisons if the value might not be aligned
		// enough for the larger comparison. See issues 46283 and 67160.
		size = 0
	}
	switch size {
	case 1, 2, 4, 8, 16:
		buf := fmt.Sprintf("memequal%d", int(size)*8)
		return typecheck.LookupRuntime(gd, buf, t, t), false
	}

	return typecheck.LookupRuntime(gd, "memequal", t, t), true
}
