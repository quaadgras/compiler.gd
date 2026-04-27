// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package walk

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
	"cmd/internal/src"
)

func walkSelect(gd *base.Invocation, sel *ir.SelectStmt) {
	lno := ir.SetPos(gd, sel)
	if sel.Walked() {
		gd.Fatalf("double walkSelect")
	}
	sel.SetWalked(true)

	init := ir.TakeInit(sel)

	init = append(init, walkSelectCases(gd, sel.Cases)...)
	sel.Cases = nil

	sel.Compiled = init
	walkStmtList(gd, sel.Compiled)

	gd.Pos = lno
}

func walkSelectCases(gd *base.Invocation, cases []*ir.CommClause) []ir.Node {
	ncas := len(cases)
	sellineno := gd.Pos

	// optimization: zero-case select
	if ncas == 0 {
		return []ir.Node{mkcallstmt(gd, "block")}
	}

	// optimization: one-case select: single op.
	if ncas == 1 {
		cas := cases[0]
		ir.SetPos(gd, cas)
		l := cas.Init()
		if cas.Comm != nil { // not default:
			n := cas.Comm
			l = append(l, ir.TakeInit(n)...)
			switch n.Op() {
			default:
				gd.Fatalf("select %v", n.Op())

			case ir.OSEND:
				// already ok

			case ir.OSELRECV2:
				r := n.(*ir.AssignListStmt)
				if ir.IsBlank(r.Lhs[0]) && ir.IsBlank(r.Lhs[1]) {
					n = r.Rhs[0]
					break
				}
				r.SetOp(ir.OAS2RECV)
			}

			l = append(l, n)
		}

		l = append(l, cas.Body...)
		l = append(l, ir.NewBranchStmt(gd, gd.Pos, ir.OBREAK, nil))
		return l
	}

	// convert case value arguments to addresses.
	// this rewrite is used by both the general code and the next optimization.
	var dflt *ir.CommClause
	for _, cas := range cases {
		ir.SetPos(gd, cas)
		n := cas.Comm
		if n == nil {
			dflt = cas
			continue
		}
		switch n.Op() {
		case ir.OSEND:
			n := n.(*ir.SendStmt)
			n.Value = typecheck.NodAddr(gd, n.Value)
			n.Value = typecheck.Expr(gd, n.Value)

		case ir.OSELRECV2:
			n := n.(*ir.AssignListStmt)
			if !ir.IsBlank(n.Lhs[0]) {
				n.Lhs[0] = typecheck.NodAddr(gd, n.Lhs[0])
				n.Lhs[0] = typecheck.Expr(gd, n.Lhs[0])
			}
		}
	}

	// optimization: two-case select but one is default: single non-blocking op.
	if ncas == 2 && dflt != nil {
		cas := cases[0]
		if cas == dflt {
			cas = cases[1]
		}

		n := cas.Comm
		ir.SetPos(gd, n)
		r := ir.NewIfStmt(gd, gd.Pos, nil, nil, nil)
		r.SetInit(cas.Init())
		var cond ir.Node
		switch n.Op() {
		default:
			gd.Fatalf("select %v", n.Op())

		case ir.OSEND:
			// if selectnbsend(c, v) { body } else { default body }
			n := n.(*ir.SendStmt)
			ch := n.Chan
			cond = mkcall1(gd, chanfn(gd, "selectnbsend", 2, ch.Type()), types.Types[types.TBOOL], r.PtrInit(), ch, n.Value)

		case ir.OSELRECV2:
			n := n.(*ir.AssignListStmt)
			recv := n.Rhs[0].(*ir.UnaryExpr)
			ch := recv.X
			elem := n.Lhs[0]
			if ir.IsBlank(elem) {
				elem = typecheck.NodNil(gd)
			}
			cond = typecheck.TempAt(gd, gd.Pos, ir.CurFunc(gd), types.Types[types.TBOOL])
			fn := chanfn(gd, "selectnbrecv", 2, ch.Type())
			call := mkcall1(gd, fn, fn.Type().ResultsTuple(), r.PtrInit(), elem, ch)
			as := ir.NewAssignListStmt(gd, r.Pos(), ir.OAS2, []ir.Node{cond, n.Lhs[1]}, []ir.Node{call})
			r.PtrInit().Append(typecheck.Stmt(gd, as))
		}

		r.Cond = typecheck.Expr(gd, cond)
		r.Body = cas.Body
		r.Else = append(dflt.Init(), dflt.Body...)
		return []ir.Node{r, ir.NewBranchStmt(gd, gd.Pos, ir.OBREAK, nil)}
	}

	if dflt != nil {
		ncas--
	}
	casorder := make([]*ir.CommClause, ncas)
	nsends, nrecvs := 0, 0

	var init []ir.Node

	// generate sel-struct
	gd.Pos = sellineno
	selv := typecheck.TempAt(gd, gd.Pos, ir.CurFunc(gd), types.NewArray(scasetype(gd), int64(ncas)))
	init = append(init, typecheck.Stmt(gd, ir.NewAssignStmt(gd, gd.Pos, selv, nil)))

	// No initialization for order; runtime.selectgo is responsible for that.
	order := typecheck.TempAt(gd, gd.Pos, ir.CurFunc(gd), types.NewArray(types.Types[types.TUINT16], 2*int64(ncas)))

	var pc0, pcs ir.Node
	if gd.Flag.Race {
		pcs = typecheck.TempAt(gd, gd.Pos, ir.CurFunc(gd), types.NewArray(types.Types[types.TUINTPTR], int64(ncas)))
		pc0 = typecheck.Expr(gd, typecheck.NodAddr(gd, ir.NewIndexExpr(gd, gd.Pos, pcs, ir.NewInt(gd, gd.Pos, 0))))
	} else {
		pc0 = typecheck.NodNil(gd)
	}

	// register cases
	for _, cas := range cases {
		ir.SetPos(gd, cas)

		init = append(init, ir.TakeInit(cas)...)

		n := cas.Comm
		if n == nil { // default:
			continue
		}

		var i int
		var c, elem ir.Node
		switch n.Op() {
		default:
			gd.Fatalf("select %v", n.Op())
		case ir.OSEND:
			n := n.(*ir.SendStmt)
			i = nsends
			nsends++
			c = n.Chan
			elem = n.Value
		case ir.OSELRECV2:
			n := n.(*ir.AssignListStmt)
			nrecvs++
			i = ncas - nrecvs
			recv := n.Rhs[0].(*ir.UnaryExpr)
			c = recv.X
			elem = n.Lhs[0]
		}

		casorder[i] = cas

		setField := func(f string, val ir.Node) {
			r := ir.NewAssignStmt(gd, gd.Pos, ir.NewSelectorExpr(gd, gd.Pos, ir.ODOT, ir.NewIndexExpr(gd, gd.Pos, selv, ir.NewInt(gd, gd.Pos, int64(i))), typecheck.Lookup(gd, f)), val)
			init = append(init, typecheck.Stmt(gd, r))
		}

		c = typecheck.ConvNop(gd, c, types.Types[types.TUNSAFEPTR])
		setField("c", c)
		if !ir.IsBlank(elem) {
			elem = typecheck.ConvNop(gd, elem, types.Types[types.TUNSAFEPTR])
			setField("elem", elem)
		}

		// TODO(mdempsky): There should be a cleaner way to
		// handle this.
		if gd.Flag.Race {
			r := mkcallstmt(gd, "selectsetpc", typecheck.NodAddr(gd, ir.NewIndexExpr(gd, gd.Pos, pcs, ir.NewInt(gd, gd.Pos, int64(i)))))
			init = append(init, r)
		}
	}
	if nsends+nrecvs != ncas {
		gd.Fatalf("walkSelectCases: miscount: %v + %v != %v", nsends, nrecvs, ncas)
	}

	// run the select
	gd.Pos = sellineno
	chosen := typecheck.TempAt(gd, gd.Pos, ir.CurFunc(gd), types.Types[types.TINT])
	recvOK := typecheck.TempAt(gd, gd.Pos, ir.CurFunc(gd), types.Types[types.TBOOL])
	r := ir.NewAssignListStmt(gd, gd.Pos, ir.OAS2, nil, nil)
	r.Lhs = []ir.Node{chosen, recvOK}
	fn := typecheck.LookupRuntime(gd, "selectgo")
	var fnInit ir.Nodes
	r.Rhs = []ir.Node{mkcall1(gd, fn, fn.Type().ResultsTuple(), &fnInit, bytePtrToIndex(gd, selv, 0), bytePtrToIndex(gd, order, 0), pc0, ir.NewInt(gd, gd.Pos, int64(nsends)), ir.NewInt(gd, gd.Pos, int64(nrecvs)), ir.NewBool(gd, gd.Pos, dflt == nil))}
	init = append(init, fnInit...)
	init = append(init, typecheck.Stmt(gd, r))

	// selv, order, and pcs (if race) are no longer alive after selectgo.

	// dispatch cases
	dispatch := func(cond ir.Node, cas *ir.CommClause) {
		var list ir.Nodes

		if n := cas.Comm; n != nil && n.Op() == ir.OSELRECV2 {
			n := n.(*ir.AssignListStmt)
			if !ir.IsBlank(n.Lhs[1]) {
				x := ir.NewAssignStmt(gd, gd.Pos, n.Lhs[1], recvOK)
				list.Append(typecheck.Stmt(gd, x))
			}
		}

		list.Append(cas.Body.Take()...)
		list.Append(ir.NewBranchStmt(gd, gd.Pos, ir.OBREAK, nil))

		var r ir.Node
		if cond != nil {
			cond = typecheck.Expr(gd, cond)
			cond = typecheck.DefaultLit(gd, cond, nil)
			r = ir.NewIfStmt(gd, gd.Pos, cond, list, nil)
		} else {
			r = ir.NewBlockStmt(gd, gd.Pos, list)
		}

		init = append(init, r)
	}

	if dflt != nil {
		ir.SetPos(gd, dflt)
		dispatch(ir.NewBinaryExpr(gd, gd.Pos, ir.OLT, chosen, ir.NewInt(gd, gd.Pos, 0)), dflt)
	}
	for i, cas := range casorder {
		ir.SetPos(gd, cas)
		if i == len(casorder)-1 {
			dispatch(nil, cas)
			break
		}
		dispatch(ir.NewBinaryExpr(gd, gd.Pos, ir.OEQ, chosen, ir.NewInt(gd, gd.Pos, int64(i))), cas)
	}

	return init
}

// bytePtrToIndex returns a Node representing "(*byte)(&n[i])".
func bytePtrToIndex(gd *base.Invocation, n ir.Node, i int64) ir.Node {
	s := typecheck.NodAddr(gd, ir.NewIndexExpr(gd, gd.Pos, n, ir.NewInt(gd, gd.Pos, i)))
	t := types.NewPtr(types.Types[types.TUINT8])
	return typecheck.ConvNop(gd, s, t)
}

var scase *types.Type

// Keep in sync with src/runtime/select.go.
func scasetype(gd *base.Invocation) *types.Type {
	if scase == nil {
		n := ir.NewDeclNameAt(gd, src.NoXPos, ir.OTYPE, ir.Pkgs(gd).Runtime.Lookup("scase"))
		scase = types.NewNamed(n)
		n.SetType(scase)
		n.SetTypecheck(1)

		scase.SetUnderlying(types.NewStruct([]*types.Field{
			types.NewField(gd.Pos, typecheck.Lookup(gd, "c"), types.Types[types.TUNSAFEPTR]),
			types.NewField(gd.Pos, typecheck.Lookup(gd, "elem"), types.Types[types.TUNSAFEPTR]),
		}))
	}
	return scase
}
