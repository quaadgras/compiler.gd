// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typecheck

import (
	"fmt"
	"go/constant"
	"strings"

	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/types"
	"cmd/internal/src"
)

func AssignExpr(gd *base.Invocation, n ir.Node) ir.Node {
	return typecheck(gd, n, ctxExpr|ctxAssign)
}
func Expr(gd *base.Invocation, n ir.Node) ir.Node { return typecheck(gd, n, ctxExpr) }
func Stmt(gd *base.Invocation, n ir.Node) ir.Node { return typecheck(gd, n, ctxStmt) }

func Exprs(gd *base.Invocation, exprs []ir.Node) { typecheckslice(gd, exprs, ctxExpr) }
func Stmts(gd *base.Invocation, stmts []ir.Node) { typecheckslice(gd, stmts, ctxStmt) }

func Call(gd *base.Invocation, pos src.XPos, callee ir.Node, args []ir.Node, dots bool) ir.Node {
	call := ir.NewCallExpr(gd, pos, ir.OCALL, callee, args)
	call.IsDDD = dots
	return typecheck(gd, call, ctxStmt|ctxExpr)
}

func Callee(gd *base.Invocation, n ir.Node) ir.Node {
	return typecheck(gd, n, ctxExpr|ctxCallee)
}

var traceIndent []byte

func tracePrint(gd *base.Invocation, title string, n ir.Node) func(np *ir.Node) {
	indent := traceIndent

	// guard against nil
	var pos, op string
	var tc uint8
	if n != nil {
		pos = gd.FmtPos(n.Pos())
		op = n.Op().String()
		tc = n.Typecheck()
	}

	types.SkipSizeForTracing = true
	defer func() { types.SkipSizeForTracing = false }()
	fmt.Printf("%s: %s%s %p %s %v tc=%d\n", pos, indent, title, n, op, n, tc)
	traceIndent = append(traceIndent, ". "...)

	return func(np *ir.Node) {
		traceIndent = traceIndent[:len(traceIndent)-2]

		// if we have a result, use that
		if np != nil {
			n = *np
		}

		// guard against nil
		// use outer pos, op so we don't get empty pos/op if n == nil (nicer output)
		var tc uint8
		var typ *types.Type
		if n != nil {
			pos = gd.FmtPos(n.Pos())
			op = n.Op().String()
			tc = n.Typecheck()
			typ = n.Type()
		}

		types.SkipSizeForTracing = true
		defer func() { types.SkipSizeForTracing = false }()
		fmt.Printf("%s: %s=> %p %s %v tc=%d type=%L\n", pos, indent, n, op, n, tc, typ)
	}
}

const (
	ctxStmt    = 1 << iota // evaluated at statement level
	ctxExpr                // evaluated in value context
	ctxType                // evaluated in type context
	ctxCallee              // call-only expressions are ok
	ctxMultiOK             // multivalue function returns are ok
	ctxAssign              // assigning to expression
)

// type checks the whole tree of an expression.
// calculates expression types.
// evaluates compile time constants.
// marks variables that escape the local frame.
// rewrites n.Op to be more specific in some cases.

func typecheckslice(gd *base.Invocation, l []ir.Node, top int) {
	for i := range l {
		l[i] = typecheck(gd, l[i], top)
	}
}

var _typekind = []string{
	types.TINT:        "int",
	types.TUINT:       "uint",
	types.TINT8:       "int8",
	types.TUINT8:      "uint8",
	types.TINT16:      "int16",
	types.TUINT16:     "uint16",
	types.TINT32:      "int32",
	types.TUINT32:     "uint32",
	types.TINT64:      "int64",
	types.TUINT64:     "uint64",
	types.TUINTPTR:    "uintptr",
	types.TCOMPLEX64:  "complex64",
	types.TCOMPLEX128: "complex128",
	types.TFLOAT32:    "float32",
	types.TFLOAT64:    "float64",
	types.TBOOL:       "bool",
	types.TSTRING:     "string",
	types.TPTR:        "pointer",
	types.TUNSAFEPTR:  "unsafe.Pointer",
	types.TSTRUCT:     "struct",
	types.TINTER:      "interface",
	types.TCHAN:       "chan",
	types.TMAP:        "map",
	types.TARRAY:      "array",
	types.TSLICE:      "slice",
	types.TFUNC:       "func",
	types.TNIL:        "nil",
	types.TIDEAL:      "untyped number",
}

func typekind(t *types.Type) string {
	if t.IsUntyped() {
		return fmt.Sprintf("%v", t)
	}
	et := t.Kind()
	if int(et) < len(_typekind) {
		s := _typekind[et]
		if s != "" {
			return s
		}
	}
	return fmt.Sprintf("etype=%d", et)
}

// typecheck type checks node n.
// The result of typecheck MUST be assigned back to n, e.g.
//
//	n.Left = typecheck(n.Left, top)
func typecheck(gd *base.Invocation, n ir.Node, top int) (res ir.Node) {
	if n == nil {
		return nil
	}

	// only trace if there's work to do
	if base.EnableTrace && gd.Flag.LowerT {
		defer tracePrint(gd, "typecheck", n)(&res)
	}

	lno := ir.SetPos(gd, n)
	defer func() { gd.Pos = lno }()

	// Skip typecheck if already done.
	// But re-typecheck ONAME/OTYPE/OLITERAL/OPACK node in case context has changed.
	if n.Typecheck() == 1 || n.Typecheck() == 3 {
		switch n.Op() {
		case ir.ONAME:
			break

		default:
			return n
		}
	}

	if n.Typecheck() == 2 {
		gd.FatalfAt(n.Pos(), "typechecking loop")
	}

	n.SetTypecheck(2)
	n = typecheck1(gd, n, top)
	n.SetTypecheck(1)

	t := n.Type()
	if t != nil && !t.IsFuncArgStruct() && n.Op() != ir.OTYPE {
		switch t.Kind() {
		case types.TFUNC, // might have TANY; wait until it's called
			types.TANY, types.TFORW, types.TIDEAL, types.TNIL, types.TBLANK:
			break

		default:
			types.CheckSize(gd, t)
		}
	}

	return n
}

// indexlit implements typechecking of untyped values as
// array/slice indexes. It is almost equivalent to DefaultLit
// but also accepts untyped numeric values representable as
// value of type int (see also checkmake for comparison).
// The result of indexlit MUST be assigned back to n, e.g.
//
//	n.Left = indexlit(gd, n.Left)
func indexlit(gd *base.Invocation, n ir.Node) ir.Node {
	if n != nil && n.Type() != nil && n.Type().Kind() == types.TIDEAL {
		return DefaultLit(gd, n, types.Types[types.TINT])
	}
	return n
}

// typecheck1 should ONLY be called from typecheck.
func typecheck1(gd *base.Invocation, n ir.Node, top int) ir.Node {
	// Skip over parens.
	for n.Op() == ir.OPAREN {
		n = n.(*ir.ParenExpr).X
	}

	switch n.Op() {
	default:
		ir.Dump("typecheck", n)
		gd.Fatalf("typecheck %v", n.Op())
		panic("unreachable")

	case ir.ONAME:
		n := n.(*ir.Name)
		if n.BuiltinOp != 0 {
			if top&ctxCallee == 0 {
				gd.Errorf("use of builtin %v not in function call", n.Sym())
				n.SetType(nil)
				return n
			}
			return n
		}
		if top&ctxAssign == 0 {
			// not a write to the variable
			if ir.IsBlank(n) {
				gd.Errorf("cannot use _ as value")
				n.SetType(nil)
				return n
			}
			n.SetUsed(true)
		}
		return n

	// type or expr
	case ir.ODEREF:
		n := n.(*ir.StarExpr)
		return tcStar(gd, n, top)

	// x op= y
	case ir.OASOP:
		n := n.(*ir.AssignOpStmt)
		n.X, n.Y = Expr(gd, n.X), Expr(gd, n.Y)
		checkassign(gd, n.X)
		if n.IncDec && !okforarith[n.X.Type().Kind()] {
			gd.Errorf("invalid operation: %v (non-numeric type %v)", n, n.X.Type())
			return n
		}
		switch n.AsOp {
		case ir.OLSH, ir.ORSH:
			n.X, n.Y, _ = tcShift(gd, n, n.X, n.Y)
		case ir.OADD, ir.OAND, ir.OANDNOT, ir.ODIV, ir.OMOD, ir.OMUL, ir.OOR, ir.OSUB, ir.OXOR:
			n.X, n.Y, _ = tcArith(gd, n, n.AsOp, n.X, n.Y)
		default:
			gd.Fatalf("invalid assign op: %v", n.AsOp)
		}
		return n

	// logical operators
	case ir.OANDAND, ir.OOROR:
		n := n.(*ir.LogicalExpr)
		n.X, n.Y = Expr(gd, n.X), Expr(gd, n.Y)
		if n.X.Type() == nil || n.Y.Type() == nil {
			n.SetType(nil)
			return n
		}
		// For "x == x && len(s)", it's better to report that "len(s)" (type int)
		// can't be used with "&&" than to report that "x == x" (type untyped bool)
		// can't be converted to int (see issue #41500).
		if !n.X.Type().IsBoolean() {
			gd.Errorf("invalid operation: %v (operator %v not defined on %s)", n, n.Op(), typekind(n.X.Type()))
			n.SetType(nil)
			return n
		}
		if !n.Y.Type().IsBoolean() {
			gd.Errorf("invalid operation: %v (operator %v not defined on %s)", n, n.Op(), typekind(n.Y.Type()))
			n.SetType(nil)
			return n
		}
		l, r, t := tcArith(gd, n, n.Op(), n.X, n.Y)
		n.X, n.Y = l, r
		n.SetType(t)
		return n

	// shift operators
	case ir.OLSH, ir.ORSH:
		n := n.(*ir.BinaryExpr)
		n.X, n.Y = Expr(gd, n.X), Expr(gd, n.Y)
		l, r, t := tcShift(gd, n, n.X, n.Y)
		n.X, n.Y = l, r
		n.SetType(t)
		return n

	// comparison operators
	case ir.OEQ, ir.OGE, ir.OGT, ir.OLE, ir.OLT, ir.ONE:
		n := n.(*ir.BinaryExpr)
		n.X, n.Y = Expr(gd, n.X), Expr(gd, n.Y)
		l, r, t := tcArith(gd, n, n.Op(), n.X, n.Y)
		if t != nil {
			n.X, n.Y = l, r
			n.SetType(types.UntypedBool)
			n.X, n.Y = defaultlit2(gd, l, r, true)
		}
		return n

	// binary operators
	case ir.OADD, ir.OAND, ir.OANDNOT, ir.ODIV, ir.OMOD, ir.OMUL, ir.OOR, ir.OSUB, ir.OXOR:
		n := n.(*ir.BinaryExpr)
		n.X, n.Y = Expr(gd, n.X), Expr(gd, n.Y)
		l, r, t := tcArith(gd, n, n.Op(), n.X, n.Y)
		if t != nil && t.Kind() == types.TSTRING && n.Op() == ir.OADD {
			// create or update OADDSTR node with list of strings in x + y + z + (w + v) + ...
			var add *ir.AddStringExpr
			if l.Op() == ir.OADDSTR {
				add = l.(*ir.AddStringExpr)
				add.SetPos(n.Pos())
			} else {
				add = ir.NewAddStringExpr(gd, n.Pos(), []ir.Node{l})
			}
			if r.Op() == ir.OADDSTR {
				r := r.(*ir.AddStringExpr)
				add.List.Append(r.List.Take()...)
			} else {
				add.List.Append(r)
			}
			add.SetType(t)
			return add
		}
		n.X, n.Y = l, r
		n.SetType(t)
		return n

	case ir.OBITNOT, ir.ONEG, ir.ONOT, ir.OPLUS:
		n := n.(*ir.UnaryExpr)
		return tcUnaryArith(gd, n)

	// exprs
	case ir.OCOMPLIT:
		return tcCompLit(gd, n.(*ir.CompLitExpr))

	case ir.OXDOT, ir.ODOT:
		n := n.(*ir.SelectorExpr)
		return tcDot(gd, n, top)

	case ir.ODOTTYPE:
		n := n.(*ir.TypeAssertExpr)
		return tcDotType(gd, n)

	case ir.OINDEX:
		n := n.(*ir.IndexExpr)
		return tcIndex(gd, n)

	case ir.ORECV:
		n := n.(*ir.UnaryExpr)
		return tcRecv(gd, n)

	case ir.OSEND:
		n := n.(*ir.SendStmt)
		return tcSend(gd, n)

	case ir.OSLICEHEADER:
		n := n.(*ir.SliceHeaderExpr)
		return tcSliceHeader(gd, n)

	case ir.OSTRINGHEADER:
		n := n.(*ir.StringHeaderExpr)
		return tcStringHeader(gd, n)

	case ir.OMAKESLICECOPY:
		n := n.(*ir.MakeExpr)
		return tcMakeSliceCopy(gd, n)

	case ir.OSLICE, ir.OSLICE3:
		n := n.(*ir.SliceExpr)
		return tcSlice(gd, n)

	// call and call like
	case ir.OCALL:
		n := n.(*ir.CallExpr)
		return tcCall(gd, n, top)

	case ir.OCAP, ir.OLEN:
		n := n.(*ir.UnaryExpr)
		return tcLenCap(gd, n)

	case ir.OMIN, ir.OMAX:
		n := n.(*ir.CallExpr)
		return tcMinMax(gd, n)

	case ir.OREAL, ir.OIMAG:
		n := n.(*ir.UnaryExpr)
		return tcRealImag(gd, n)

	case ir.OCOMPLEX:
		n := n.(*ir.BinaryExpr)
		return tcComplex(gd, n)

	case ir.OCLEAR:
		n := n.(*ir.UnaryExpr)
		return tcClear(gd, n)

	case ir.OCLOSE:
		n := n.(*ir.UnaryExpr)
		return tcClose(gd, n)

	case ir.ODELETE:
		n := n.(*ir.CallExpr)
		return tcDelete(gd, n)

	case ir.OAPPEND:
		n := n.(*ir.CallExpr)
		return tcAppend(gd, n)

	case ir.OCOPY:
		n := n.(*ir.BinaryExpr)
		return tcCopy(gd, n)

	case ir.OCONV:
		n := n.(*ir.ConvExpr)
		return tcConv(gd, n)

	case ir.OMAKE:
		n := n.(*ir.CallExpr)
		return tcMake(gd, n)

	case ir.ONEW:
		n := n.(*ir.UnaryExpr)
		return tcNew(gd, n)

	case ir.OPRINT, ir.OPRINTLN:
		n := n.(*ir.CallExpr)
		return tcPrint(gd, n)

	case ir.OPANIC:
		n := n.(*ir.UnaryExpr)
		return tcPanic(gd, n)

	case ir.ORECOVER:
		n := n.(*ir.CallExpr)
		return tcRecover(gd, n)

	case ir.OUNSAFEADD:
		n := n.(*ir.BinaryExpr)
		return tcUnsafeAdd(gd, n)

	case ir.OUNSAFESLICE:
		n := n.(*ir.BinaryExpr)
		return tcUnsafeSlice(gd, n)

	case ir.OUNSAFESLICEDATA:
		n := n.(*ir.UnaryExpr)
		return tcUnsafeData(gd, n)

	case ir.OUNSAFESTRING:
		n := n.(*ir.BinaryExpr)
		return tcUnsafeString(gd, n)

	case ir.OUNSAFESTRINGDATA:
		n := n.(*ir.UnaryExpr)
		return tcUnsafeData(gd, n)

	case ir.OITAB:
		n := n.(*ir.UnaryExpr)
		return tcITab(gd, n)

	case ir.OIDATA:
		// Whoever creates the OIDATA node must know a priori the concrete type at that moment,
		// usually by just having checked the OITAB.
		n := n.(*ir.UnaryExpr)
		gd.Fatalf("cannot typecheck interface data %v", n)
		panic("unreachable")

	case ir.OSPTR:
		n := n.(*ir.UnaryExpr)
		return tcSPtr(gd, n)

	case ir.OCFUNC:
		n := n.(*ir.UnaryExpr)
		n.X = Expr(gd, n.X)
		n.SetType(types.Types[types.TUINTPTR])
		return n

	case ir.OGETCALLERSP:
		n := n.(*ir.CallExpr)
		if len(n.Args) != 0 {
			gd.FatalfAt(n.Pos(), "unexpected arguments: %v", n)
		}
		n.SetType(types.Types[types.TUINTPTR])
		return n

	case ir.OCONVNOP:
		n := n.(*ir.ConvExpr)
		n.X = Expr(gd, n.X)
		return n

	// statements
	case ir.OAS:
		n := n.(*ir.AssignStmt)
		tcAssign(gd, n)

		// Code that creates temps does not bother to set defn, so do it here.
		if n.X.Op() == ir.ONAME && ir.IsAutoTmp(n.X) {
			n.X.Name().Defn = n
		}
		return n

	case ir.OAS2:
		tcAssignList(gd, n.(*ir.AssignListStmt))
		return n

	case ir.OBREAK,
		ir.OCONTINUE,
		ir.ODCL,
		ir.OGOTO,
		ir.OFALL:
		return n

	case ir.OBLOCK:
		n := n.(*ir.BlockStmt)
		Stmts(gd, n.List)
		return n

	case ir.OLABEL:
		if n.Sym().IsBlank() {
			// Empty identifier is valid but useless.
			// Eliminate now to simplify life later.
			// See issues 7538, 11589, 11593.
			n = ir.NewBlockStmt(gd, n.Pos(), nil)
		}
		return n

	case ir.ODEFER, ir.OGO:
		n := n.(*ir.GoDeferStmt)
		n.Call = typecheck(gd, n.Call, ctxStmt|ctxExpr)
		tcGoDefer(gd, n)
		return n

	case ir.OFOR:
		n := n.(*ir.ForStmt)
		return tcFor(gd, n)

	case ir.OIF:
		n := n.(*ir.IfStmt)
		return tcIf(gd, n)

	case ir.ORETURN:
		n := n.(*ir.ReturnStmt)
		return tcReturn(gd, n)

	case ir.OTAILCALL:
		n := n.(*ir.TailCallStmt)
		n.Call = typecheck(gd, n.Call, ctxStmt|ctxExpr).(*ir.CallExpr)
		return n

	case ir.OCHECKNIL:
		n := n.(*ir.UnaryExpr)
		return tcCheckNil(gd, n)

	case ir.OSELECT:
		tcSelect(gd, n.(*ir.SelectStmt))
		return n

	case ir.OSWITCH:
		tcSwitch(gd, n.(*ir.SwitchStmt))
		return n

	case ir.ORANGE:
		tcRange(gd, n.(*ir.RangeStmt))
		return n

	case ir.OTYPESW:
		n := n.(*ir.TypeSwitchGuard)
		gd.Fatalf("use of .(type) outside type switch")
		return n

	case ir.ODCLFUNC:
		tcFunc(gd, n.(*ir.Func))
		return n
	}

	// No return n here!
	// Individual cases can type-assert n, introducing a new one.
	// Each must execute its own return n.
}

func typecheckargs(gd *base.Invocation, n ir.InitNode) {
	var list []ir.Node
	switch n := n.(type) {
	default:
		gd.Fatalf("typecheckargs %+v", n.Op())
	case *ir.CallExpr:
		list = n.Args
		if n.IsDDD {
			Exprs(gd, list)
			return
		}
	case *ir.ReturnStmt:
		list = n.Results
	}
	if len(list) != 1 {
		Exprs(gd, list)
		return
	}

	typecheckslice(gd, list, ctxExpr|ctxMultiOK)
	t := list[0].Type()
	if t == nil || !t.IsFuncArgStruct() {
		return
	}

	// Rewrite f(g()) into t1, t2, ... = g(); f(t1, t2, ...).
	RewriteMultiValueCall(gd, n, list[0])
}

// RewriteNonNameCall replaces non-Name call expressions with temps,
// rewriting f()(...) to t0 := f(); t0(...).
func RewriteNonNameCall(gd *base.Invocation, n *ir.CallExpr) {
	np := &n.Fun
	if dot, ok := (*np).(*ir.SelectorExpr); ok && (dot.Op() == ir.ODOTMETH || dot.Op() == ir.ODOTINTER || dot.Op() == ir.OMETHVALUE) {
		np = &dot.X // peel away method selector
	}

	// Check for side effects in the callee expression.
	// We explicitly special case new(T) though, because it doesn't have
	// observable side effects, and keeping it in place allows better escape analysis.
	if !ir.Any(*np, func(n ir.Node) bool { return n.Op() != ir.ONEW && callOrChan(n) }) {
		return
	}

	tmp := TempAt(gd, gd.Pos, ir.CurFunc(gd), (*np).Type())
	as := ir.NewAssignStmt(gd, gd.Pos, tmp, *np)
	as.PtrInit().Append(Stmt(gd, ir.NewDecl(gd, n.Pos(), ir.ODCL, tmp)))
	*np = tmp

	n.PtrInit().Append(Stmt(gd, as))
}

// RewriteMultiValueCall rewrites multi-valued f() to use temporaries,
// so the backend wouldn't need to worry about tuple-valued expressions.
func RewriteMultiValueCall(gd *base.Invocation, n ir.InitNode, call ir.Node) {
	as := ir.NewAssignListStmt(gd, gd.Pos, ir.OAS2, nil, []ir.Node{call})
	results := call.Type().Fields()
	list := make([]ir.Node, len(results))
	for i, result := range results {
		tmp := TempAt(gd, gd.Pos, ir.CurFunc(gd), result.Type)
		as.PtrInit().Append(ir.NewDecl(gd, gd.Pos, ir.ODCL, tmp))
		as.Lhs.Append(tmp)
		list[i] = tmp
	}

	n.PtrInit().Append(Stmt(gd, as))

	switch n := n.(type) {
	default:
		gd.Fatalf("RewriteMultiValueCall %+v", n.Op())
	case *ir.CallExpr:
		n.Args = list
	case *ir.ReturnStmt:
		n.Results = list
	case *ir.AssignListStmt:
		if n.Op() != ir.OAS2FUNC {
			gd.Fatalf("RewriteMultiValueCall: invalid op %v", n.Op())
		}
		as.SetOp(ir.OAS2FUNC)
		n.SetOp(ir.OAS2)
		n.Rhs = make([]ir.Node, len(list))
		for i, tmp := range list {
			n.Rhs[i] = AssignConv(gd, tmp, n.Lhs[i].Type(), "assignment")
		}
	}
}

func checksliceindex(gd *base.Invocation, r ir.Node) bool {
	t := r.Type()
	if t == nil {
		return false
	}
	if !t.IsInteger() {
		gd.Errorf("invalid slice index %v (type %v)", r, t)
		return false
	}
	return true
}

// The result of implicitstar MUST be assigned back to n, e.g.
//
//	n.Left = implicitstar(gd, n.Left)
func implicitstar(gd *base.Invocation, n ir.Node) ir.Node {
	// insert implicit * if needed for fixed array
	t := n.Type()
	if t == nil || !t.IsPtr() {
		return n
	}
	t = t.Elem()
	if t == nil {
		return n
	}
	if !t.IsArray() {
		return n
	}
	star := ir.NewStarExpr(gd, gd.Pos, n)
	star.SetImplicit(true)
	return Expr(gd, star)
}

func needOneArg(gd *base.Invocation, n *ir.CallExpr, f string, args ...any) (ir.Node, bool) {
	if len(n.Args) == 0 {
		p := fmt.Sprintf(f, args...)
		gd.Errorf("missing argument to %s: %v", p, n)
		return nil, false
	}

	if len(n.Args) > 1 {
		p := fmt.Sprintf(f, args...)
		gd.Errorf("too many arguments to %s: %v", p, n)
		return n.Args[0], false
	}

	return n.Args[0], true
}

func needTwoArgs(gd *base.Invocation, n *ir.CallExpr) (ir.Node, ir.Node, bool) {
	if len(n.Args) != 2 {
		if len(n.Args) < 2 {
			gd.Errorf("not enough arguments in call to %v", n)
		} else {
			gd.Errorf("too many arguments in call to %v", n)
		}
		return nil, nil, false
	}
	return n.Args[0], n.Args[1], true
}

// Lookdot1 looks up the specified method s in the list fs of methods, returning
// the matching field or nil. If dostrcmp is 0, it matches the symbols. If
// dostrcmp is 1, it matches by name exactly. If dostrcmp is 2, it matches names
// with case folding.
func Lookdot1(gd *base.Invocation, errnode ir.Node, s *types.Sym, t *types.Type, fs []*types.Field, dostrcmp int) *types.Field {
	var r *types.Field
	for _, f := range fs {
		if dostrcmp != 0 && f.Sym.Name == s.Name {
			return f
		}
		if dostrcmp == 2 && strings.EqualFold(f.Sym.Name, s.Name) {
			return f
		}
		// gd in-process: tolerate cross-invocation Sym pointers for
		// exported names (any pkg's "Error" matches an interface's
		// "Error"); strict pointer compare is the upstream norm but
		// fails when the lookup target Sym is locked to invocation
		// 1's LocalPkg via a shared universe type. Falls back to
		// name+pkgpath for unexported (matching Go encapsulation).
		if !methodSymEqual(f.Sym, s) {
			continue
		}
		if r != nil {
			if errnode != nil {
				gd.Errorf("ambiguous selector %v", errnode)
			} else if t.IsPtr() {
				gd.Errorf("ambiguous selector (%v).%v", t, s)
			} else {
				gd.Errorf("ambiguous selector %v.%v", t, s)
			}
			break
		}

		r = f
	}

	return r
}

// NewMethodExpr returns an OMETHEXPR node representing method
// expression "recv.sym".
func NewMethodExpr(gd *base.Invocation, pos src.XPos, recv *types.Type, sym *types.Sym) *ir.SelectorExpr {
	// Compute the method set for recv.
	var ms []*types.Field
	if recv.IsInterface() {
		ms = recv.AllMethods()
	} else {
		mt := types.ReceiverBaseType(recv)
		if mt == nil {
			gd.FatalfAt(pos, "type %v has no receiver base type", recv)
		}
		CalcMethods(mt)
		ms = mt.AllMethods()
	}

	m := Lookdot1(gd, nil, sym, recv, ms, 0)
	if m == nil {
		gd.FatalfAt(pos, "type %v has no method %v", recv, sym)
	}

	if !types.IsMethodApplicable(recv, m) {
		gd.FatalfAt(pos, "invalid method expression %v.%v (needs pointer receiver)", recv, sym)
	}

	n := ir.NewSelectorExpr(gd, pos, ir.OMETHEXPR, ir.TypeNode(gd, recv), sym)
	n.Selection = m
	n.SetType(NewMethodType(gd, m.Type, recv))
	n.SetTypecheck(1)
	return n
}

func derefall(t *types.Type) *types.Type {
	for t != nil && t.IsPtr() {
		t = t.Elem()
	}
	return t
}

// Lookdot looks up field or method n.Sel in the type t and returns the matching
// field. It transforms the op of node n to ODOTINTER or ODOTMETH, if appropriate.
// It also may add a StarExpr node to n.X as needed for access to non-pointer
// methods. If dostrcmp is 0, it matches the field/method with the exact symbol
// as n.Sel (appropriate for exported fields). If dostrcmp is 1, it matches by name
// exactly. If dostrcmp is 2, it matches names with case folding.
func Lookdot(gd *base.Invocation, n *ir.SelectorExpr, t *types.Type, dostrcmp int) *types.Field {
	s := n.Sel

	types.CalcSize(gd, t)
	var f1 *types.Field
	if t.IsStruct() {
		f1 = Lookdot1(gd, n, s, t, t.Fields(), dostrcmp)
	} else if t.IsInterface() {
		f1 = Lookdot1(gd, n, s, t, t.AllMethods(), dostrcmp)
	}

	var f2 *types.Field
	if n.X.Type() == t || n.X.Type().Sym() == nil {
		mt := types.ReceiverBaseType(t)
		if mt != nil {
			f2 = Lookdot1(gd, n, s, mt, mt.Methods(), dostrcmp)
		}
	}

	if f1 != nil {
		if dostrcmp > 1 {
			// Already in the process of diagnosing an error.
			return f1
		}
		if f2 != nil {
			gd.Errorf("%v is both field and method", n.Sel)
		}
		if f1.Offset == types.BADWIDTH {
			gd.Fatalf("Lookdot badwidth t=%v, f1=%v@%p", t, f1, f1)
		}
		n.Selection = f1
		n.SetType(f1.Type)
		if t.IsInterface() {
			if n.X.Type().IsPtr() {
				star := ir.NewStarExpr(gd, gd.Pos, n.X)
				star.SetImplicit(true)
				n.X = Expr(gd, star)
			}

			n.SetOp(ir.ODOTINTER)
		}
		return f1
	}

	if f2 != nil {
		if dostrcmp > 1 {
			// Already in the process of diagnosing an error.
			return f2
		}
		orig := n.X
		tt := n.X.Type()
		types.CalcSize(gd, tt)
		rcvr := f2.Type.Recv().Type
		if !types.Identical(rcvr, tt) {
			if rcvr.IsPtr() && types.Identical(rcvr.Elem(), tt) {
				checklvalue(gd, n.X, "call pointer method on")
				addr := NodAddr(gd, n.X)
				addr.SetImplicit(true)
				n.X = typecheck(gd, addr, ctxType|ctxExpr)
			} else if tt.IsPtr() && (!rcvr.IsPtr() || rcvr.IsPtr() && rcvr.Elem().NotInHeap()) && types.Identical(tt.Elem(), rcvr) {
				star := ir.NewStarExpr(gd, gd.Pos, n.X)
				star.SetImplicit(true)
				n.X = typecheck(gd, star, ctxType|ctxExpr)
			} else if tt.IsPtr() && tt.Elem().IsPtr() && types.Identical(derefall(tt), derefall(rcvr)) {
				gd.Errorf("calling method %v with receiver %L requires explicit dereference", n.Sel, n.X)
				for tt.IsPtr() {
					// Stop one level early for method with pointer receiver.
					if rcvr.IsPtr() && !tt.Elem().IsPtr() {
						break
					}
					star := ir.NewStarExpr(gd, gd.Pos, n.X)
					star.SetImplicit(true)
					n.X = typecheck(gd, star, ctxType|ctxExpr)
					tt = tt.Elem()
				}
			} else {
				gd.Fatalf("method mismatch: %v for %v", rcvr, tt)
			}
		}

		// Check that we haven't implicitly dereferenced any defined pointer types.
		for x := n.X; ; {
			var inner ir.Node
			implicit := false
			switch x := x.(type) {
			case *ir.AddrExpr:
				inner, implicit = x.X, x.Implicit()
			case *ir.SelectorExpr:
				inner, implicit = x.X, x.Implicit()
			case *ir.StarExpr:
				inner, implicit = x.X, x.Implicit()
			}
			if !implicit {
				break
			}
			if inner.Type().Sym() != nil && (x.Op() == ir.ODEREF || x.Op() == ir.ODOTPTR) {
				// Found an implicit dereference of a defined pointer type.
				// Restore n.X for better error message.
				n.X = orig
				return nil
			}
			x = inner
		}

		n.Selection = f2
		n.SetType(f2.Type)
		n.SetOp(ir.ODOTMETH)

		return f2
	}

	return nil
}

func nokeys(l ir.Nodes) bool {
	for _, n := range l {
		if n.Op() == ir.OKEY || n.Op() == ir.OSTRUCTKEY {
			return false
		}
	}
	return true
}

func hasddd(params []*types.Field) bool {
	// TODO(mdempsky): Simply check the last param.
	for _, tl := range params {
		if tl.IsDDD() {
			return true
		}
	}

	return false
}

// typecheck assignment: type list = expression list
func typecheckaste(gd *base.Invocation, op ir.Op, call ir.Node, isddd bool, params []*types.Field, nl ir.Nodes, desc func() string) {
	var t *types.Type
	var i int

	lno := gd.Pos
	defer func() { gd.Pos = lno }()

	var n ir.Node
	if len(nl) == 1 {
		n = nl[0]
	}

	n1 := len(params)
	n2 := len(nl)
	if !hasddd(params) {
		if isddd {
			goto invalidddd
		}
		if n2 > n1 {
			goto toomany
		}
		if n2 < n1 {
			goto notenough
		}
	} else {
		if !isddd {
			if n2 < n1-1 {
				goto notenough
			}
		} else {
			if n2 > n1 {
				goto toomany
			}
			if n2 < n1 {
				goto notenough
			}
		}
	}

	i = 0
	for _, tl := range params {
		t = tl.Type
		if tl.IsDDD() {
			if isddd {
				if i >= len(nl) {
					goto notenough
				}
				if len(nl)-i > 1 {
					goto toomany
				}
				n = nl[i]
				ir.SetPos(gd, n)
				if n.Type() != nil {
					nl[i] = assignconvfn(gd, n, t, desc)
				}
				return
			}

			// TODO(mdempsky): Make into ... call with implicit slice.
			for ; i < len(nl); i++ {
				n = nl[i]
				ir.SetPos(gd, n)
				if n.Type() != nil {
					nl[i] = assignconvfn(gd, n, t.Elem(), desc)
				}
			}
			return
		}

		if i >= len(nl) {
			goto notenough
		}
		n = nl[i]
		ir.SetPos(gd, n)
		if n.Type() != nil {
			nl[i] = assignconvfn(gd, n, t, desc)
		}
		i++
	}

	if i < len(nl) {
		goto toomany
	}

invalidddd:
	if isddd {
		if call != nil {
			gd.Errorf("invalid use of ... in call to %v", call)
		} else {
			gd.Errorf("invalid use of ... in %v", op)
		}
	}
	return

notenough:
	if n == nil || n.Type() != nil {
		gd.Fatalf("not enough arguments to %v", op)
	}
	return

toomany:
	gd.Fatalf("too many arguments to %v", op)
}

// type check composite.
func fielddup(gd *base.Invocation, name string, hash map[string]bool) {
	if hash[name] {
		gd.Errorf("duplicate field name in struct literal: %s", name)
		return
	}
	hash[name] = true
}

// typecheckarraylit type-checks a sequence of slice/array literal elements.
func typecheckarraylit(gd *base.Invocation, elemType *types.Type, bound int64, elts []ir.Node, ctx string) int64 {
	// If there are key/value pairs, create a map to keep seen
	// keys so we can check for duplicate indices.
	var indices map[int64]bool
	for _, elt := range elts {
		if elt.Op() == ir.OKEY {
			indices = make(map[int64]bool)
			break
		}
	}

	var key, length int64
	for i, elt := range elts {
		ir.SetPos(gd, elt)
		r := elts[i]
		var kv *ir.KeyExpr
		if elt.Op() == ir.OKEY {
			elt := elt.(*ir.KeyExpr)
			elt.Key = Expr(gd, elt.Key)
			key = IndexConst(gd, elt.Key)
			kv = elt
			r = elt.Value
		}

		r = Expr(gd, r)
		r = AssignConv(gd, r, elemType, ctx)
		if kv != nil {
			kv.Value = r
		} else {
			elts[i] = r
		}

		if key >= 0 {
			if indices != nil {
				if indices[key] {
					gd.Errorf("duplicate index in %s: %d", ctx, key)
				} else {
					indices[key] = true
				}
			}

			if bound >= 0 && key >= bound {
				gd.Errorf("array index %d out of bounds [0:%d]", key, bound)
				bound = -1
			}
		}

		key++
		if key > length {
			length = key
		}
	}

	return length
}

// visible reports whether sym is exported or locally defined.
func visible(gd *base.Invocation, sym *types.Sym) bool {
	return sym != nil && (types.IsExported(sym.Name) || sym.Pkg == types.LocalPkg(gd))
}

// nonexported reports whether sym is an unexported field.
func nonexported(sym *types.Sym) bool {
	return sym != nil && !types.IsExported(sym.Name)
}

func checklvalue(gd *base.Invocation, n ir.Node, verb string) {
	if !ir.IsAddressable(n) {
		gd.Errorf("cannot %s %v", verb, n)
	}
}

func checkassign(gd *base.Invocation, n ir.Node) {
	// have already complained about n being invalid
	if n.Type() == nil {
		if gd.Errors() == 0 {
			gd.Fatalf("expected an error about %v", n)
		}
		return
	}

	if ir.IsAddressable(n) {
		return
	}
	if n.Op() == ir.OINDEXMAP {
		n := n.(*ir.IndexExpr)
		n.Assigned = true
		return
	}

	defer n.SetType(nil)

	switch {
	case n.Op() == ir.ODOT && n.(*ir.SelectorExpr).X.Op() == ir.OINDEXMAP:
		gd.Errorf("cannot assign to struct field %v in map", n)
	case (n.Op() == ir.OINDEX && n.(*ir.IndexExpr).X.Type().IsString()) || n.Op() == ir.OSLICESTR:
		gd.Errorf("cannot assign to %v (strings are immutable)", n)
	case n.Op() == ir.OLITERAL && n.Sym() != nil && ir.IsConstNode(n):
		gd.Errorf("cannot assign to %v (declared const)", n)
	default:
		gd.Errorf("cannot assign to %v", n)
	}
}

func checkassignto(gd *base.Invocation, src *types.Type, dst ir.Node) {
	// TODO(mdempsky): Handle all untyped types correctly.
	if src == types.UntypedBool && dst.Type().IsBoolean() {
		return
	}

	if op, why := assignOp(gd, src, dst.Type()); op == ir.OXXX {
		gd.Errorf("cannot assign %v to %L in multiple assignment%s", src, dst, why)
		return
	}
}

// The result of stringtoruneslit MUST be assigned back to n, e.g.
//
//	n.Left = stringtoruneslit(gd, n.Left)
func stringtoruneslit(gd *base.Invocation, n *ir.ConvExpr) ir.Node {
	if n.X.Op() != ir.OLITERAL || n.X.Val().Kind() != constant.String {
		gd.Fatalf("stringtoarraylit %v", n)
	}

	var l []ir.Node
	i := 0
	for _, r := range ir.StringVal(n.X) {
		l = append(l, ir.NewKeyExpr(gd, gd.Pos, ir.NewInt(gd, gd.Pos, int64(i)), ir.NewInt(gd, gd.Pos, int64(r))))
		i++
	}

	return Expr(gd, ir.NewCompLitExpr(gd, gd.Pos, ir.OCOMPLIT, n.Type(), l))
}

func checkmake(gd *base.Invocation, t *types.Type, arg string, np *ir.Node) bool {
	n := *np
	if !n.Type().IsInteger() && n.Type().Kind() != types.TIDEAL {
		gd.Errorf("non-integer %s argument in make(%v) - %v", arg, t, n.Type())
		return false
	}

	// DefaultLit is necessary for non-constants too: n might be 1.1<<k.
	// TODO(gri) The length argument requirements for (array/slice) make
	// are the same as for index expressions. Factor the code better;
	// for instance, indexlit might be called here and incorporate some
	// of the bounds checks done for make.
	n = DefaultLit(gd, n, types.Types[types.TINT])
	*np = n

	return true
}

// checkunsafesliceorstring is like checkmake but for unsafe.{Slice,String}.
func checkunsafesliceorstring(gd *base.Invocation, op ir.Op, np *ir.Node) bool {
	n := *np
	if !n.Type().IsInteger() && n.Type().Kind() != types.TIDEAL {
		gd.Errorf("non-integer len argument in %v - %v", op, n.Type())
		return false
	}

	// DefaultLit is necessary for non-constants too: n might be 1.1<<k.
	n = DefaultLit(gd, n, types.Types[types.TINT])
	*np = n

	return true
}

func Conv(gd *base.Invocation, n ir.Node, t *types.Type) ir.Node {
	if types.IdenticalStrict(n.Type(), t) {
		return n
	}
	n = ir.NewConvExpr(gd, gd.Pos, ir.OCONV, nil, n)
	n.SetType(t)
	n = Expr(gd, n)
	return n
}

// ConvNop converts node n to type t using the OCONVNOP op
// and typechecks the result with ctxExpr.
func ConvNop(gd *base.Invocation, n ir.Node, t *types.Type) ir.Node {
	if types.IdenticalStrict(n.Type(), t) {
		return n
	}
	n = ir.NewConvExpr(gd, gd.Pos, ir.OCONVNOP, nil, n)
	n.SetType(t)
	n = Expr(gd, n)
	return n
}
