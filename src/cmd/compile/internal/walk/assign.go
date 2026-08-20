// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package walk

import (
	"go/constant"
	"internal/abi"

	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/reflectdata"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
	"cmd/internal/src"
)

// walkAssign walks an OAS (AssignExpr) or OASOP (AssignOpExpr) node.
func walkAssign(gd *base.Invocation, init *ir.Nodes, n ir.Node) ir.Node {
	init.Append(ir.TakeInit(n)...)

	var left, right ir.Node
	switch n.Op() {
	case ir.OAS:
		n := n.(*ir.AssignStmt)
		left, right = n.X, n.Y
	case ir.OASOP:
		n := n.(*ir.AssignOpStmt)
		left, right = n.X, n.Y
	}

	// Recognize m[k] = append(m[k], ...) so we can reuse
	// the mapassign call.
	var mapAppend *ir.CallExpr
	if left.Op() == ir.OINDEXMAP && right.Op() == ir.OAPPEND {
		left := left.(*ir.IndexExpr)
		mapAppend = right.(*ir.CallExpr)
		if !ir.SameSafeExpr(left, mapAppend.Args[0]) {
			gd.Fatalf("not same expressions: %v != %v", left, mapAppend.Args[0])
		}
	}

	left = walkExpr(gd, left, init)
	left = safeExpr(gd, left, init)
	if mapAppend != nil {
		mapAppend.Args[0] = left
	}

	if n.Op() == ir.OASOP {
		// Rewrite x op= y into x = x op y.
		n = ir.NewAssignStmt(gd, gd.Pos, left, typecheck.Expr(gd, ir.NewBinaryExpr(gd, gd.Pos, n.(*ir.AssignOpStmt).AsOp, left, right)))
	} else {
		n.(*ir.AssignStmt).X = left
	}
	as := n.(*ir.AssignStmt)

	if oaslit(gd, as, init) {
		return ir.NewBlockStmt(gd, as.Pos(), nil)
	}

	if as.Y == nil {
		// TODO(austin): Check all "implicit zeroing"
		return as
	}

	if !gd.Flag.Cfg.Instrumenting && ir.IsZero(as.Y) {
		return as
	}

	switch as.Y.Op() {
	default:
		as.Y = walkExpr(gd, as.Y, init)

	case ir.ORECV:
		// x = <-c; as.Left is x, as.Right.Left is c.
		// order.stmt made sure x is addressable.
		recv := as.Y.(*ir.UnaryExpr)
		recv.X = walkExpr(gd, recv.X, init)

		n1 := typecheck.NodAddr(gd, as.X)
		r := recv.X // the channel
		return mkcall1(gd, chanfn(gd, "chanrecv1", 2, r.Type()), nil, init, r, n1)

	case ir.OAPPEND:
		// x = append(...)
		call := as.Y.(*ir.CallExpr)
		if call.Type().Elem().NotInHeap() {
			gd.Errorf("%v can't be allocated in Go; it is incomplete (or unallocatable)", call.Type().Elem())
		}
		var r ir.Node
		switch {
		case isAppendOfMake(gd, call):
			// x = append(y, make([]T, y)...)
			r = extendSlice(gd, call, init)
		case call.IsDDD:
			r = appendSlice(gd, call, init) // also works for append(slice, string).
		default:
			r = walkAppend(gd, call, init, as)
		}
		as.Y = r
		if r.Op() == ir.OAPPEND {
			r := r.(*ir.CallExpr)
			// Left in place for back end.
			// Do not add a new write barrier.
			// Set up address of type for back end.
			r.Fun = reflectdata.AppendElemRType(gd, gd.Pos, r)
			return as
		}
		// Otherwise, lowered for race detector.
		// Treat as ordinary assignment.
	}

	if as.X != nil && as.Y != nil {
		return convas(gd, as, init)
	}
	return as
}

// walkAssignDotType walks an OAS2DOTTYPE node.
func walkAssignDotType(gd *base.Invocation, n *ir.AssignListStmt, init *ir.Nodes) ir.Node {
	walkExprListSafe(gd, n.Lhs, init)

	if r, ok := n.Rhs[0].(*ir.TypeAssertExpr); ok && r.Op() == ir.ODOTTYPE2 && !r.Type().IsInterface() {
		if shapeTypeAssertImpossible(gd, r.X, r.Type()) {
			init.Append(typecheck.Stmt(gd, ir.NewAssignStmt(gd, gd.Pos, ir.BlankNode, walkExpr(gd, r.X, init))))
			init.Append(typecheck.Stmt(gd, ir.NewAssignStmt(gd, gd.Pos, n.Lhs[0], ir.NewZero(gd, gd.Pos, r.Type()))))
			init.Append(typecheck.Stmt(gd, ir.NewAssignStmt(gd, gd.Pos, n.Lhs[1], ir.NewBool(gd, gd.Pos, false))))
			return ir.NewBlockStmt(gd, gd.Pos, nil)
		}
	}

	n.Rhs[0] = walkExpr(gd, n.Rhs[0], init)
	return n
}

// walkAssignFunc walks an OAS2FUNC node.
func walkAssignFunc(gd *base.Invocation, init *ir.Nodes, n *ir.AssignListStmt) ir.Node {
	init.Append(ir.TakeInit(n)...)

	r := n.Rhs[0]
	walkExprListSafe(gd, n.Lhs, init)
	r = walkExpr(gd, r, init)

	if ir.IsIntrinsicCall(gd, r.(*ir.CallExpr)) {
		n.Rhs = []ir.Node{r}
		return n
	}
	init.Append(r)

	ll := ascompatet(gd, n.Lhs, r.Type())
	return ir.NewBlockStmt(gd, src.NoXPos, ll)
}

// walkAssignList walks an OAS2 node.
func walkAssignList(gd *base.Invocation, init *ir.Nodes, n *ir.AssignListStmt) ir.Node {
	init.Append(ir.TakeInit(n)...)
	return ir.NewBlockStmt(gd, src.NoXPos, ascompatee(gd, ir.OAS, n.Lhs, n.Rhs))
}

// walkAssignMapRead walks an OAS2MAPR node.
func walkAssignMapRead(gd *base.Invocation, init *ir.Nodes, n *ir.AssignListStmt) ir.Node {
	init.Append(ir.TakeInit(n)...)

	r := n.Rhs[0].(*ir.IndexExpr)
	walkExprListSafe(gd, n.Lhs, init)
	r.X = walkExpr(gd, r.X, init)
	r.Index = walkExpr(gd, r.Index, init)
	t := r.X.Type()

	fast := mapfast(gd, t)
	key := mapKeyArg(gd, fast, r, r.Index, false)

	// from:
	//   a,b = m[i]
	// to:
	//   var,b = mapaccess2*(t, m, i)
	//   a = *var
	a := n.Lhs[0]

	var call *ir.CallExpr
	if w := t.Elem().Size(); w <= abi.ZeroValSize {
		fn := mapfn(gd, mapaccess2[fast], t, false)
		call = mkcall1(gd, fn, fn.Type().ResultsTuple(), init, reflectdata.IndexMapRType(gd, gd.Pos, r), r.X, key)
	} else {
		fn := mapfn(gd, "mapaccess2_fat", t, true)
		z := reflectdata.ZeroAddr(gd, w)
		call = mkcall1(gd, fn, fn.Type().ResultsTuple(), init, reflectdata.IndexMapRType(gd, gd.Pos, r), r.X, key, z)
	}

	// mapaccess2* returns a typed bool, but due to spec changes,
	// the boolean result of i.(T) is now untyped so we make it the
	// same type as the variable on the lhs.
	if ok := n.Lhs[1]; !ir.IsBlank(ok) && ok.Type().IsBoolean() {
		call.Type().Field(1).Type = ok.Type()
	}
	n.Rhs = []ir.Node{call}
	n.SetOp(ir.OAS2FUNC)

	// don't generate a = *var if a is _
	if ir.IsBlank(a) {
		return walkExpr(gd, typecheck.Stmt(gd, n), init)
	}

	var_ := typecheck.TempAt(gd, gd.Pos, ir.CurFunc(gd), types.NewPtr(t.Elem()))
	var_.SetTypecheck(1)
	var_.MarkNonNil() // mapaccess always returns a non-nil pointer

	n.Lhs[0] = var_
	init.Append(walkExpr(gd, n, init))

	as := ir.NewAssignStmt(gd, gd.Pos, a, ir.NewStarExpr(gd, gd.Pos, var_))
	return walkExpr(gd, typecheck.Stmt(gd, as), init)
}

// walkAssignRecv walks an OAS2RECV node.
func walkAssignRecv(gd *base.Invocation, init *ir.Nodes, n *ir.AssignListStmt) ir.Node {
	init.Append(ir.TakeInit(n)...)

	r := n.Rhs[0].(*ir.UnaryExpr) // recv
	walkExprListSafe(gd, n.Lhs, init)
	r.X = walkExpr(gd, r.X, init)
	var n1 ir.Node
	if ir.IsBlank(n.Lhs[0]) {
		n1 = typecheck.NodNil(gd)
	} else {
		n1 = typecheck.NodAddr(gd, n.Lhs[0])
	}
	fn := chanfn(gd, "chanrecv2", 2, r.X.Type())
	ok := n.Lhs[1]
	call := mkcall1(gd, fn, types.Types[types.TBOOL], init, r.X, n1)
	return walkAssign(gd, init, typecheck.Stmt(gd, ir.NewAssignStmt(gd, gd.Pos, ok, call)))
}

// walkReturn walks an ORETURN node.
func walkReturn(gd *base.Invocation, n *ir.ReturnStmt) ir.Node {
	fn := ir.CurFunc(gd)

	fn.NumReturns++
	if len(n.Results) == 0 {
		return n
	}

	results := fn.Type().Results()
	dsts := make([]ir.Node, len(results))
	for i, v := range results {
		// TODO(mdempsky): typecheck should have already checked the result variables.
		dsts[i] = typecheck.AssignExpr(gd, v.Nname.(*ir.Name))
	}

	n.Results = ascompatee(gd, n.Op(), dsts, n.Results)
	return n
}

// check assign type list to
// an expression list. called in
//
//	expr-list = func()
func ascompatet(gd *base.Invocation, nl ir.Nodes, nr *types.Type) []ir.Node {
	if len(nl) != nr.NumFields() {
		gd.Fatalf("ascompatet: assignment count mismatch: %d = %d", len(nl), nr.NumFields())
	}

	var nn ir.Nodes
	for i, l := range nl {
		if ir.IsBlank(l) {
			continue
		}
		r := nr.Field(i)

		// Order should have created autotemps of the appropriate type for
		// us to store results into.
		if tmp, ok := l.(*ir.Name); !ok || !tmp.AutoTemp() || !types.Identical(tmp.Type(), r.Type) {
			gd.FatalfAt(l.Pos(), "assigning %v to %+v", r.Type, l)
		}

		res := ir.NewResultExpr(gd, gd.Pos, nil, types.BADWIDTH)
		res.Index = int64(i)
		res.SetType(r.Type)
		res.SetTypecheck(1)

		nn.Append(ir.NewAssignStmt(gd, gd.Pos, l, res))
	}
	return nn
}

// check assign expression list to
// an expression list. called in
//
//	expr-list = expr-list
func ascompatee(gd *base.Invocation, op ir.Op, nl, nr []ir.Node) []ir.Node {
	// cannot happen: should have been rejected during type checking
	if len(nl) != len(nr) {
		gd.Fatalf("assignment operands mismatch: %+v / %+v", ir.Nodes(nl), ir.Nodes(nr))
	}

	var assigned ir.NameSet
	var memWrite, deferResultWrite bool

	// affected reports whether expression n could be affected by
	// the assignments applied so far.
	affected := func(n ir.Node) bool {
		if deferResultWrite {
			return true
		}
		return ir.Any(n, func(n ir.Node) bool {
			if n.Op() == ir.ONAME && assigned.Has(n.(*ir.Name)) {
				return true
			}
			if memWrite && readsMemory(n) {
				return true
			}
			return false
		})
	}

	// If a needed expression may be affected by an
	// earlier assignment, make an early copy of that
	// expression and use the copy instead.
	var early ir.Nodes
	save := func(np *ir.Node) {
		if n := *np; affected(n) {
			*np = copyExpr(gd, n, n.Type(), &early)
		}
	}

	var late ir.Nodes
	for i, lorig := range nl {
		l, r := lorig, nr[i]

		// Do not generate 'x = x' during return. See issue 4014.
		if op == ir.ORETURN && ir.SameSafeExpr(l, r) {
			continue
		}

		// Save subexpressions needed on left side.
		// Drill through non-dereferences.
		for {
			// If an expression has init statements, they must be evaluated
			// before any of its saved sub-operands (#45706).
			// TODO(mdempsky): Disallow init statements on lvalues.
			init := ir.TakeInit(l)
			walkStmtList(gd, init)
			early.Append(init...)

			switch ll := l.(type) {
			case *ir.IndexExpr:
				if ll.X.Type().IsArray() {
					save(&ll.Index)
					l = ll.X
					continue
				}
			case *ir.ParenExpr:
				l = ll.X
				continue
			case *ir.SelectorExpr:
				if ll.Op() == ir.ODOT {
					l = ll.X
					continue
				}
			}
			break
		}

		var name *ir.Name
		switch l.Op() {
		default:
			gd.Fatalf("unexpected lvalue %v", l.Op())
		case ir.ONAME:
			name = l.(*ir.Name)
		case ir.OINDEX, ir.OINDEXMAP:
			l := l.(*ir.IndexExpr)
			save(&l.X)
			save(&l.Index)
		case ir.ODEREF:
			l := l.(*ir.StarExpr)
			save(&l.X)
		case ir.ODOTPTR:
			l := l.(*ir.SelectorExpr)
			save(&l.X)
		}

		// Save expression on right side.
		save(&r)

		appendWalkStmt(gd, &late, convas(gd, ir.NewAssignStmt(gd, gd.Pos, lorig, r), &late))

		// Check for reasons why we may need to compute later expressions
		// before this assignment happens.

		if name == nil {
			// Not a direct assignment to a declared variable.
			// Conservatively assume any memory access might alias.
			memWrite = true
			continue
		}

		if name.Class == ir.PPARAMOUT && ir.CurFunc(gd).HasDefer() {
			// Assignments to a result parameter in a function with defers
			// becomes visible early if evaluation of any later expression
			// panics (#43835).
			deferResultWrite = true
			continue
		}

		if ir.IsBlank(name) {
			// We can ignore assignments to blank or anonymous result parameters.
			// These can't appear in expressions anyway.
			continue
		}

		if name.Addrtaken() || !name.OnStack() {
			// Global variable, heap escaped, or just addrtaken.
			// Conservatively assume any memory access might alias.
			memWrite = true
			continue
		}

		// Local, non-addrtaken variable.
		// Assignments can only alias with direct uses of this variable.
		assigned.Add(name)
	}

	early.Append(late.Take()...)
	return early
}

// readsMemory reports whether the evaluation n directly reads from
// memory that might be written to indirectly.
func readsMemory(n ir.Node) bool {
	switch n.Op() {
	case ir.ONAME:
		n := n.(*ir.Name)
		if n.Class == ir.PFUNC {
			return false
		}
		return n.Addrtaken() || !n.OnStack()

	case ir.OADD,
		ir.OAND,
		ir.OANDAND,
		ir.OANDNOT,
		ir.OBITNOT,
		ir.OCONV,
		ir.OCONVIFACE,
		ir.OCONVNOP,
		ir.ODIV,
		ir.ODOT,
		ir.ODOTTYPE,
		ir.OLITERAL,
		ir.OLSH,
		ir.OMOD,
		ir.OMUL,
		ir.ONEG,
		ir.ONIL,
		ir.OOR,
		ir.OOROR,
		ir.OPAREN,
		ir.OPLUS,
		ir.ORSH,
		ir.OSUB,
		ir.OXOR:
		return false
	}

	// Be conservative.
	return true
}

// expand append(l1, l2...) to
//
//	init {
//	  s := l1
//	  newLen := s.len + l2.len
//	  // Compare as uint so growslice can panic on overflow.
//	  if uint(newLen) <= uint(s.cap) {
//	    s = s[:newLen]
//	  } else {
//	    s = growslice(s.ptr, s.len, s.cap, l2.len, T)
//	  }
//	  memmove(&s[s.len-l2.len], &l2[0], l2.len*sizeof(T))
//	}
//	s
//
// l2 is allowed to be a string.
func appendSlice(gd *base.Invocation, n *ir.CallExpr, init *ir.Nodes) ir.Node {
	walkAppendArgs(gd, n, init)

	l1 := n.Args[0]
	l2 := n.Args[1]
	l2 = cheapExpr(gd, l2, init)
	n.Args[1] = l2

	var nodes ir.Nodes

	// var s []T
	s := typecheck.TempAt(gd, gd.Pos, ir.CurFunc(gd), l1.Type())
	nodes.Append(ir.NewAssignStmt(gd, gd.Pos, s, l1)) // s = l1

	elemtype := s.Type().Elem()

	// Decompose slice.
	oldPtr := ir.NewUnaryExpr(gd, gd.Pos, ir.OSPTR, s)
	oldLen := ir.NewUnaryExpr(gd, gd.Pos, ir.OLEN, s)
	oldCap := ir.NewUnaryExpr(gd, gd.Pos, ir.OCAP, s)

	// Number of elements we are adding
	num := ir.NewUnaryExpr(gd, gd.Pos, ir.OLEN, l2)

	// newLen := oldLen + num
	newLen := typecheck.TempAt(gd, gd.Pos, ir.CurFunc(gd), types.Types[types.TINT])
	nodes.Append(ir.NewAssignStmt(gd, gd.Pos, newLen, ir.NewBinaryExpr(gd, gd.Pos, ir.OADD, oldLen, num)))

	// if uint(newLen) <= uint(oldCap)
	nif := ir.NewIfStmt(gd, gd.Pos, nil, nil, nil)
	nuint := typecheck.Conv(gd, newLen, types.Types[types.TUINT])
	scapuint := typecheck.Conv(gd, oldCap, types.Types[types.TUINT])
	nif.Cond = ir.NewBinaryExpr(gd, gd.Pos, ir.OLE, nuint, scapuint)
	nif.Likely = true

	// then { s = s[:newLen] }
	slice := ir.NewSliceExpr(gd, gd.Pos, ir.OSLICE, s, nil, newLen, nil)
	slice.SetBounded(true)
	nif.Body = []ir.Node{ir.NewAssignStmt(gd, gd.Pos, s, slice)}

	// else { s = growslice(oldPtr, newLen, oldCap, num, T) }
	call := walkGrowslice(gd, s, nif.PtrInit(), oldPtr, newLen, oldCap, num)
	nif.Else = []ir.Node{ir.NewAssignStmt(gd, gd.Pos, s, call)}

	nodes.Append(nif)

	// Index to start copying into s.
	//   idx = newLen - len(l2)
	// We use this expression instead of oldLen because it avoids
	// a spill/restore of oldLen.
	// Note: this doesn't work optimally currently because
	// the compiler optimizer undoes this arithmetic.
	idx := ir.NewBinaryExpr(gd, gd.Pos, ir.OSUB, newLen, ir.NewUnaryExpr(gd, gd.Pos, ir.OLEN, l2))

	var ncopy ir.Node
	if elemtype.HasPointers() {
		// copy(s[idx:], l2)
		slice := ir.NewSliceExpr(gd, gd.Pos, ir.OSLICE, s, idx, nil, nil)
		slice.SetType(s.Type())
		slice.SetBounded(true)

		ir.CurFunc(gd).SetWBPos(gd, n.Pos())

		// instantiate typedslicecopy(typ *type, dstPtr *any, dstLen int, srcPtr *any, srcLen int) int
		fn := typecheck.LookupRuntime(gd, "typedslicecopy", l1.Type().Elem(), l2.Type().Elem())
		ptr1, len1 := backingArrayPtrLen(gd, cheapExpr(gd, slice, &nodes))
		ptr2, len2 := backingArrayPtrLen(gd, l2)
		ncopy = mkcall1(gd, fn, types.Types[types.TINT], &nodes, reflectdata.AppendElemRType(gd, gd.Pos, n), ptr1, len1, ptr2, len2)
	} else if gd.Flag.Cfg.Instrumenting && !gd.Flag.CompilingRuntime {
		// rely on runtime to instrument:
		//  copy(s[idx:], l2)
		// l2 can be a slice or string.
		slice := ir.NewSliceExpr(gd, gd.Pos, ir.OSLICE, s, idx, nil, nil)
		slice.SetType(s.Type())
		slice.SetBounded(true)

		ptr1, len1 := backingArrayPtrLen(gd, cheapExpr(gd, slice, &nodes))
		ptr2, len2 := backingArrayPtrLen(gd, l2)

		fn := typecheck.LookupRuntime(gd, "slicecopy", ptr1.Type().Elem(), ptr2.Type().Elem())
		ncopy = mkcall1(gd, fn, types.Types[types.TINT], &nodes, ptr1, len1, ptr2, len2, ir.NewInt(gd, gd.Pos, elemtype.Size()))
	} else {
		// memmove(&s[idx], &l2[0], len(l2)*sizeof(T))
		ix := ir.NewIndexExpr(gd, gd.Pos, s, idx)
		ix.SetBounded(true)
		addr := typecheck.NodAddr(gd, ix)

		sptr := ir.NewUnaryExpr(gd, gd.Pos, ir.OSPTR, l2)

		nwid := cheapExpr(gd, typecheck.Conv(gd, ir.NewUnaryExpr(gd, gd.Pos, ir.OLEN, l2), types.Types[types.TUINTPTR]), &nodes)
		nwid = ir.NewBinaryExpr(gd, gd.Pos, ir.OMUL, nwid, ir.NewInt(gd, gd.Pos, elemtype.Size()))

		// instantiate func memmove(to *any, frm *any, length uintptr)
		fn := typecheck.LookupRuntime(gd, "memmove", elemtype, elemtype)
		ncopy = mkcall1(gd, fn, nil, &nodes, addr, sptr, nwid)
	}
	ln := append(nodes, ncopy)

	typecheck.Stmts(gd, ln)
	walkStmtList(gd, ln)
	init.Append(ln...)
	return s
}

// isAppendOfMake reports whether n is of the form append(x, make([]T, y)...).
// isAppendOfMake assumes n has already been typechecked.
func isAppendOfMake(gd *base.Invocation, n ir.Node) bool {
	if gd.Flag.N != 0 || gd.Flag.Cfg.Instrumenting {
		return false
	}

	if n.Typecheck() == 0 {
		gd.Fatalf("missing typecheck: %+v", n)
	}

	if n.Op() != ir.OAPPEND {
		return false
	}
	call := n.(*ir.CallExpr)
	if !call.IsDDD || len(call.Args) != 2 || call.Args[1].Op() != ir.OMAKESLICE {
		return false
	}

	mk := call.Args[1].(*ir.MakeExpr)
	if mk.Cap != nil {
		return false
	}

	// y must be either an integer constant or the largest possible positive value
	// of variable y needs to fit into a uint.

	// typecheck made sure that constant arguments to make are not negative and fit into an int.

	// The care of overflow of the len argument to make will be handled by an explicit check of int(len) < 0 during runtime.
	y := mk.Len
	if !ir.IsConst(y, constant.Int) && y.Type().Size() > types.Types[types.TUINT].Size() {
		return false
	}

	return true
}

// extendSlice rewrites append(l1, make([]T, l2)...) to
//
//	init {
//	  if l2 >= 0 { // Empty if block here for more meaningful node.SetLikely(true)
//	  } else {
//	    panicmakeslicelen()
//	  }
//	  s := l1
//	  if l2 != 0 {
//	    n := len(s) + l2
//	    // Compare n and s as uint so growslice can panic on overflow of len(s) + l2.
//	    // cap is a positive int and n can become negative when len(s) + l2
//	    // overflows int. Interpreting n when negative as uint makes it larger
//	    // than cap(s). growslice will check the int n arg and panic if n is
//	    // negative. This prevents the overflow from being undetected.
//	    if uint(n) <= uint(cap(s)) {
//	      s = s[:n]
//	    } else {
//	      s = growslice(T, s.ptr, n, s.cap, l2, T)
//	    }
//	    // clear the new portion of the underlying array.
//	    hp := &s[len(s)-l2]
//	    hn := l2 * sizeof(T)
//	    memclr(hp, hn)
//	  }
//	}
//	s
//
//	if T has pointers, the final memclr can go inside the "then" branch, as
//	growslice will have done the clearing for us.

func extendSlice(gd *base.Invocation, n *ir.CallExpr, init *ir.Nodes) ir.Node {
	// isAppendOfMake made sure all possible positive values of l2 fit into a uint.
	// The case of l2 overflow when converting from e.g. uint to int is handled by an explicit
	// check of l2 < 0 at runtime which is generated below.
	l2 := typecheck.Conv(gd, n.Args[1].(*ir.MakeExpr).Len, types.Types[types.TINT])
	l2 = typecheck.Expr(gd, l2)
	n.Args[1] = l2 // walkAppendArgs expects l2 in n.List.Second().

	walkAppendArgs(gd, n, init)

	l1 := n.Args[0]
	l2 = n.Args[1] // re-read l2, as it may have been updated by walkAppendArgs

	var nodes []ir.Node

	// if l2 >= 0 (likely happens), do nothing
	nifneg := ir.NewIfStmt(gd, gd.Pos, ir.NewBinaryExpr(gd, gd.Pos, ir.OGE, l2, ir.NewInt(gd, gd.Pos, 0)), nil, nil)
	nifneg.Likely = true

	// else panicmakeslicelen()
	nifneg.Else = []ir.Node{mkcall(gd, "panicmakeslicelen", nil, init)}
	nodes = append(nodes, nifneg)

	// s := l1
	s := typecheck.TempAt(gd, gd.Pos, ir.CurFunc(gd), l1.Type())
	nodes = append(nodes, ir.NewAssignStmt(gd, gd.Pos, s, l1))

	// if l2 != 0 {
	// Avoid work if we're not appending anything. But more importantly,
	// avoid allowing hp to be a past-the-end pointer when clearing. See issue 67255.
	nifnz := ir.NewIfStmt(gd, gd.Pos, ir.NewBinaryExpr(gd, gd.Pos, ir.ONE, l2, ir.NewInt(gd, gd.Pos, 0)), nil, nil)
	nifnz.Likely = true
	nodes = append(nodes, nifnz)

	elemtype := s.Type().Elem()

	// n := s.len + l2
	nn := typecheck.TempAt(gd, gd.Pos, ir.CurFunc(gd), types.Types[types.TINT])
	nifnz.Body = append(nifnz.Body, ir.NewAssignStmt(gd, gd.Pos, nn, ir.NewBinaryExpr(gd, gd.Pos, ir.OADD, ir.NewUnaryExpr(gd, gd.Pos, ir.OLEN, s), l2)))

	// if uint(n) <= uint(s.cap)
	nuint := typecheck.Conv(gd, nn, types.Types[types.TUINT])
	capuint := typecheck.Conv(gd, ir.NewUnaryExpr(gd, gd.Pos, ir.OCAP, s), types.Types[types.TUINT])
	nif := ir.NewIfStmt(gd, gd.Pos, ir.NewBinaryExpr(gd, gd.Pos, ir.OLE, nuint, capuint), nil, nil)
	nif.Likely = true

	// then { s = s[:n] }
	nt := ir.NewSliceExpr(gd, gd.Pos, ir.OSLICE, s, nil, nn, nil)
	nt.SetBounded(true)
	nif.Body = []ir.Node{ir.NewAssignStmt(gd, gd.Pos, s, nt)}

	// else { s = growslice(s.ptr, n, s.cap, l2, T) }
	nif.Else = []ir.Node{
		ir.NewAssignStmt(gd, gd.Pos, s, walkGrowslice(gd, s, nif.PtrInit(),
			ir.NewUnaryExpr(gd, gd.Pos, ir.OSPTR, s),
			nn,
			ir.NewUnaryExpr(gd, gd.Pos, ir.OCAP, s),
			l2)),
	}

	nifnz.Body = append(nifnz.Body, nif)

	// hp := &s[s.len - l2]
	// TODO: &s[s.len] - hn?
	ix := ir.NewIndexExpr(gd, gd.Pos, s, ir.NewBinaryExpr(gd, gd.Pos, ir.OSUB, ir.NewUnaryExpr(gd, gd.Pos, ir.OLEN, s), l2))
	ix.SetBounded(true)
	hp := typecheck.ConvNop(gd, typecheck.NodAddr(gd, ix), types.Types[types.TUNSAFEPTR])

	// hn := l2 * sizeof(elem(s))
	hn := typecheck.Conv(gd, ir.NewBinaryExpr(gd, gd.Pos, ir.OMUL, l2, ir.NewInt(gd, gd.Pos, elemtype.Size())), types.Types[types.TUINTPTR])

	clrname := "memclrNoHeapPointers"
	hasPointers := elemtype.HasPointers()
	if hasPointers {
		clrname = "memclrHasPointers"
		ir.CurFunc(gd).SetWBPos(gd, n.Pos())
	}

	var clr ir.Nodes
	clrfn := mkcall(gd, clrname, nil, &clr, hp, hn)
	clr.Append(clrfn)
	if hasPointers {
		// growslice will have cleared the new entries, so only
		// if growslice isn't called do we need to do the zeroing ourselves.
		nif.Body = append(nif.Body, clr...)
	} else {
		nifnz.Body = append(nifnz.Body, clr...)
	}

	typecheck.Stmts(gd, nodes)
	walkStmtList(gd, nodes)
	init.Append(nodes...)
	return s
}
