// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package escape

import (
	"fmt"

	"cmd/compile/internal/ir"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
	"cmd/internal/src"
	"strings"
)

// call evaluates a call expressions, including builtin calls. ks
// should contain the holes representing where the function callee's
// results flows.
func (e *escape) call(ks []hole, call ir.Node) {
	argument := func(k hole, arg ir.Node) {
		// TODO(mdempsky): Should be "call argument".
		e.expr(k.note(e.gd, call, "call parameter"), arg)
	}

	if call.Op() != ir.OAPPEND {
		e.noteStmtCall()
	}
	switch call.Op() {
	default:
		ir.Dump("esc", call)
		e.gd.Fatalf("unexpected call op: %v", call.Op())

	case ir.OCALLFUNC, ir.OCALLINTER:
		call := call.(*ir.CallExpr)
		typecheck.AssertFixedCall(e.gd, call)

		// Pick out the function callee(s), if statically known.
		// fns collects all known callees; for a single static callee
		// it has one element. For unknown callees fns is nil.
		//
		// TODO(mdempsky): Change fns from []*ir.Name to []*ir.Func,
		// but some functions (e.g., runtime builtins, method wrappers,
		// generated eq/hash functions) don't have it set. Investigate
		// whether that's a concern.
		var fns []*ir.Name
		direct := false // fns is the function that the call is of
		switch call.Op() {
		case ir.OCALLFUNC:
			ro := e.reassignOracle(e.curfn)
			v := ro.StaticValue(call.Fun)
			if fn := ir.StaticCalleeName(v); fn != nil {
				fns = []*ir.Name{fn}
				direct = true
			} else if name, ok := v.(*ir.Name); ok {
				fns = resolveAssignedCallees(ro.FuncAssignments(name.Canonical()))
			}
		}

		fntype := call.Fun.Type()
		if len(fns) == 1 {
			fntype = fns[0].Type()
		}

		// What the caller says to a function it calls directly
		// (callbits.go).
		var cs *callSite
		if direct {
			cs, ks = e.noteCall(ks, call, fns[0], fns[0].Type())
		} else {
			cs, ks = e.noteCall(ks, call, nil, call.Fun.Type())
		}

		if cs == nil {
			// Results that nothing records: where they point is
			// not known (groups.go).
			inBatch := len(fns) != 0
			for _, f := range fns {
				if !e.inMutualBatch(f) {
					inBatch = false
				}
			}
			if !inBatch {
				for i, k := range ks {
					if fntype != nil && i < fntype.NumResults() {
						e.unknownOf(k, fntype.Result(i).Type)
					}
				}
			}
		}

		// Wire result flows for in-batch callees.
		if ks != nil {
			for _, f := range fns {
				if e.inMutualBatch(f) {
					for i, result := range f.Type().Results() {
						k := ks[i]
						k.retFlow = true
						e.expr(k, result.Nname.(*ir.Name))
					}
				}
			}
		}

		var recvArg ir.Node
		if call.Op() == ir.OCALLFUNC {
			// Evaluate callee function expression.
			calleeK := e.discardHole()
			if len(fns) == 0 { // unknown callee
				for _, k := range ks {
					if k.dst != &e.blankLoc {
						// The results flow somewhere, but we don't statically
						// know the callee function. If a closure flows here, we
						// need to conservatively assume its results might flow to
						// the heap.
						calleeK = e.calleeHole().note(e.gd, call, "callee operand")
						break
					}
				}
				if e.shareTagsOn() && e.gd.Debug.NoLocalMethVal == 0 {
					// What it returns may be what the function
					// value holds (the receiver of a method
					// value, what a closure has captured), or
					// anything that this leads to: who reaches a
					// result reaches that. It is no holder of it
					// by this.
					hs := []hole{calleeK}
					for i, k := range ks {
						if k.dst == &e.blankLoc || k.derefs < 0 || i >= fntype.NumResults() || !fntype.Result(i).Type.HasPointers() {
							continue
						}
						k = k.deref(e.gd, call, "what the function value holds, in a result")
						k.loose = true
						k.recvRes = true
						hs = append(hs, k)
					}
					calleeK = e.teeHole(hs...)
				}
			}
			e.expr(calleeK, call.Fun)
		} else {
			recvArg = call.Fun.(*ir.SelectorExpr).X
		}

		args := call.Args
		if fntype.Recv() != nil {
			if recvArg == nil {
				// Function call using method expression. Receiver argument is
				// at the front of the regular arguments list.
				recvArg, args = args[0], args[1:]
			}
		}

		if call.IsCompilerVarLive {
			// Don't escape compiler-inserted KeepAlive.
			if recvArg != nil {
				argument(e.discardHole(), recvArg)
			}
			for _, arg := range args {
				argument(e.discardHole(), arg)
			}
		} else if e.poolPut(call, fns, recvArg, args) {
			// The object goes to the heap, and what it holds to
			// where another goroutine reaches it: whether it is
			// handed over is known when the graph is solved.
			argument(e.discardHole(), recvArg)
			argument(e.handOverHole(call), args[0])
		} else if isNoEscape(fns) {
			// internal/abi.NoEscape hides its argument from escape
			// analysis, with a conversion to an integer and back
			// that would count as showing the address to everyone:
			// its argument would be shared, and with it whatever
			// reflect looks at. Only the standard library can call
			// it, and promises that what it returns goes nowhere
			// that outlives the argument. That is trusted, here as
			// it is for escaping: the argument goes nowhere.
			for _, arg := range args {
				argument(e.discardHole(), arg)
			}
		} else if isEscapeNonString(fns, fntype) {
			// internal/abi.EscapeNonString forces its argument to
			// the heap if it contains a non-string pointer. This is
			// used in hash/maphash.Comparable (where we cannot hash
			// pointers to locals whose address may change on stack
			// growth) and unique.clone (to model the data flow edge
			// with strings excluded, because strings are cloned by
			// content). The actual call we match is:
			//   internal/abi.EscapeNonString[go.shape.T](dict, go.shape.T)
			k := e.heapHole()
			if !hasNonStringPointers(fntype.Params()[1].Type) {
				k = e.discardHole()
			}
			for _, arg := range args {
				argument(k, arg)
			}
		} else {
			// The arguments of a direct call, the receiver first:
			// what the function stores through one is where that
			// one points (sharedTagHoles).
			var all []ir.Node
			first := 0
			if recvArg != nil {
				first = 1
			}
			if direct {
				if recvArg != nil {
					all = append(all, recvArg)
				}
				all = append(all, args[:len(fntype.Params())]...)
			}
			var groups []*location
			if direct && len(fns) == 1 {
				groups = e.argGroupsOf(call, fns[0], all)
				if cs != nil {
					cs.groups = groups
				}
				if f := fns[0].Func; cs != nil && f != nil && f.ResultDeep != 0 && !e.inMutualBatch(fns[0]) && e.deepOn() {
					// What it returns it has stored in what
					// those arguments reach. (Where nobody knows
					// all that an argument reaches, the call says
					// so with the argument's bit, and what is
					// returned is not local.)
					e.callArgs, e.argGroups = all, groups
					for _, h := range e.deepHolesOf(f.ResultDeep, 0, nil, true) {
						for _, r := range cs.res {
							e.flow(h, r)
						}
					}
					e.callArgs, e.argGroups = nil, nil
				}
			}
			// withGroup adds to k the membership root of the j'th
			// of all, or the dirt of the function for an argument
			// of a function that nobody knows here.
			// For a function that is not known: the call, and the
			// argument's place among its arguments (an interface's
			// receiver is not one).
			var dyn *ir.CallExpr
			dynFirst := 0
			if len(fns) == 0 {
				dyn = call
				if call.Op() == ir.OCALLFUNC && recvArg != nil {
					dynFirst = 1
				}
			}
			dynKey := ""
			if dyn != nil && dynFirst == 0 {
				dynKey = e.linkKey(call)
			}
			var stores uint16
			if direct && len(fns) == 1 && !e.inMutualBatch(fns[0]) {
				stores = calleeStores(fns[0])
			}
			// The arguments that the function passes on, with
			// others, to functions that nobody knows: those that
			// the caller's parameters do not reach are a group.
			var mixInto uint16
			var mixG *location
			if direct && len(fns) == 1 && !e.inMutualBatch(fns[0]) && fns[0].Func != nil && e.deepOn() {
				for _, ps := range fns[0].Func.ParamShares {
					for _, l := range ps.MixLinks {
						mixInto |= l.Into
					}
					for _, l := range ps.MixGroups {
						mixInto |= l.Into
					}
				}
				if pkg := e.curfn.Sym().Pkg; pkg == nil || ir.UncheckedPkg(pkg.Path) {
					mixInto = 0
				}
				for j, arg := range all {
					if j < 16 && mixInto&(1<<j) != 0 && pointsToPointers(arg.Type()) && e.deepMaskOf(arg) == 0 && mixG == nil {
						mixG = e.memberRoot(e.curfn)
						mixG.dynRoot.call, mixG.dynRoot.arg = call, -2
					}
				}
			}
			// A function that is only known when the program runs
			// may store any of its arguments in what any other
			// reaches: those that parameters of the caller reach
			// are stored in what those reach, and the others are
			// one group (groups.go).
			var dynG *location
			var dynQ uint16
			dynN := 0
			// (With no class to ask, the function mixes them.)
			dynMixKey := dynKey
			if dynMixKey == "" {
				dynMixKey = mixUnknown
			}
			dynOn := len(fns) == 0 && e.deepOn() && !call.GoDefer
			if pkg := e.curfn.Sym().Pkg; pkg == nil || ir.UncheckedPkg(pkg.Path) {
				dynOn = false
			}
			if dynOn {
				var list []ir.Node
				if recvArg != nil {
					list = append(list, recvArg)
				}
				list = append(list, args[:len(fntype.Params())]...)
				for _, arg := range list {
					if !pointsToPointers(arg.Type()) {
						continue
					}
					dynN++
					if m := e.deepMaskOf(arg); m != 0 {
						dynQ |= m
					} else if dynG == nil {
						dynG = e.memberRoot(e.curfn)
						dynG.dynRoot.call, dynG.dynRoot.arg = call, -1
					}
				}
				if cs != nil {
					cs.dynGroup = dynG
				}
			}
			withGroup := func(k hole, j int, arg ir.Node) hole {
				if k.dst == &e.blankLoc {
					return k
				}
				if dynOn {
					if !pointsToPointers(arg.Type()) {
						return k
					}
					hs := []hole{k}
					m := e.deepMaskOf(arg)
					// Its own structure, for the bit of the call.
					if m == 0 {
						g := e.memberRoot(e.curfn)
						g.dynRoot.call, g.dynRoot.arg = call, j
						if cs != nil {
							for len(cs.groups) <= j {
								cs.groups = append(cs.groups, nil)
							}
							cs.groups[j] = g
						}
						hs = append(hs, g.asHole())
					}
					// And, if a function of the class stores one
					// argument in what another reaches, all that
					// the arguments reach: the class's word says
					// whether one does. (These are no holders:
					// whether the function keeps the pointer, for
					// a hand-over, its mask says when it is
					// called.) Where there is no class, one does.
					if dynN > 1 {
						if dynG != nil {
							hs = append(hs, dynG.asHole())
						}
						place := uint8(ir.MixRecv)
						switch {
						case j == 0 && recvArg != nil && call.Op() == ir.OCALLINTER:
						case call.Op() == ir.OCALLINTER:
							place = uint8(j - 1)
						default:
							place = uint8(j)
						}
						// What is stored is where it is stored: the
						// argument itself only if the function
						// stores this one, and what it reaches, for
						// what is stored in it afterwards, if the
						// function stores any.
						hs = append(hs, e.mixRoot(dynMixKey, place, false, dynG, dynQ, false).asHole())
						hs = append(hs, e.mixRoot(dynMixKey, place, true, dynG, dynQ, false).asHole())
					}
					// Whether it may store what nobody here knows,
					// its class's word says.
					switch {
					case j == 0 && recvArg != nil && call.Op() == ir.OCALLINTER:
						hs = append(hs, e.recvClassHole(call, arg))
					case dynKey != "" && call.Op() == ir.OCALLINTER:
						hs = append(hs, e.classMarker(dynKey, j-1).asHole())
					case dynKey != "" && recvArg == nil:
						hs = append(hs, e.classMarker(dynKey, j).asHole())
					default:
						if e.gd.Flag.LowerM >= 4 {
							fmt.Printf("%s: argument %d is dirt: a dynamic call without a class\n", e.gd.FmtPos(call.Pos()), j)
						}
						hs = append(hs, e.dirtOf().asHole())
					}
					if cs != nil {
						cs.dynArgs = append(cs.dynArgs, dynArg{pos: j, mask: m})
					}
					return e.teeHole(hs...)
				}
				if !direct || len(fns) != 1 {
					if e.gd.Flag.LowerM >= 4 && pointsToPointers(arg.Type()) {
						fmt.Printf("%s: argument %d is dirt: not a direct call of one function\n", e.gd.FmtPos(call.Pos()), j)
					}
					return e.dirtyHole(k, arg)
				}
				if j < len(groups) && groups[j] != nil {
					k = e.teeHole(k, groups[j].asHole())
				}
				if mixG != nil && j < 16 && mixInto&(1<<j) != 0 && pointsToPointers(arg.Type()) && e.deepMaskOf(arg) == 0 {
					k = e.teeHole(k, mixG.asHole())
				}
				if j < 16 && stores&(1<<j) != 0 && e.deepOn() && pointsToPointers(arg.Type()) {
					k = e.teeHole(k, e.storedBy().asHole())
				}
				return k
			}
			if recvArg != nil {
				e.rewriteArgument(recvArg, call, fns)
				e.noteCallArg(cs, 0, recvArg)
				e.callArgs, e.argGroups, e.mixGroup = all, groups, mixG
				e.dynCall, e.dynArg, e.dynKey = dyn, dynFirst-1, dynKey
				e.checkedArg, e.recvArg, e.recvAnyArg, e.checkCallsArg = keptChecked(fns, 0)
				k := e.mergedTagHole(ks, fns, -1, fntype, recvArg)
				e.callArgs, e.argGroups, e.mixGroup, e.dynCall, e.dynKey, e.checkedArg, e.recvArg, e.recvAnyArg, e.checkCallsArg = nil, nil, nil, nil, "", false, false, false, false
				if e.consumedArg(call, fns, 0, recvArg) {
					k = e.handOverHole(call)
				}
				if call.Op() == ir.OCALLINTER {
					// The receiver of a method that is only known
					// when the program runs (recv.go).
					if sel, ok := call.Fun.(*ir.SelectorExpr); ok && sel.Op() == ir.ODOTINTER {
						k = e.recvHole(ks, call)
					}
				}
				argument(withGroup(k, 0, recvArg), recvArg)
			}
			for i := range fntype.Params() {
				e.rewriteArgument(args[i], call, fns)
				e.noteCallArg(cs, first+i, args[i])
				e.callArgs, e.argGroups, e.mixGroup = all, groups, mixG
				e.dynCall, e.dynArg, e.dynKey = dyn, dynFirst+i, dynKey
				e.checkedArg, e.recvArg, e.recvAnyArg, e.checkCallsArg = keptChecked(fns, first+i)
				k := e.mergedTagHole(ks, fns, i, fntype, args[i])
				e.callArgs, e.argGroups, e.mixGroup, e.dynCall, e.dynKey, e.checkedArg, e.recvArg, e.recvAnyArg, e.checkCallsArg = nil, nil, nil, nil, "", false, false, false, false
				if e.consumedArg(call, fns, first+i, args[i]) {
					k = e.handOverHole(call)
				}
				argument(withGroup(k, first+i, args[i]), args[i])
			}
		}

		// gd Phase G: for a call whose signature was extended with
		// trailing outBuf params, model the call's result as an
		// allocation at the call site. The synthetic spill loc
		// accumulates the usual heap-reaching edges from downstream
		// uses of the result; after solve, its attrEscapes bit tells
		// walk whether to pass &stackBuf (local) or nil (escapes).
		// Without this, call.Esc() stays EscUnknown and walk's
		// Phase-G call-site rewriter conservatively passes nil —
		// correct but leaves the optimization on the table.
		//
		// Restricted to single-result calls for now: multi-result
		// CallExpr.Type is a tuple struct that finalize's
		// HeapAllocReason can't size, and each result would need a
		// distinct ir.Node key for its own spill loc anyway.
		if ks != nil && fntype != nil && fntype.GdReturnOutBuf() &&
			len(fntype.Results()) == 1 {
			r := fntype.Results()[0]
			if len(ks) >= 1 && r != nil && r.Type != nil && r.Type.IsPtr() {
				// Spill creates loc(call) and flows &call → ks[0].
				// We then mark loc.param = true so the solver's
				// walkAll records paramEsc leaks (Result/Heap)
				// for the spill — this is what lets walk's call-
				// site rewriter distinguish "result flows only to
				// our k-th return" (chain-fold via parent's
				// outBuf_k) from "result truly escapes" (heap
				// fallback). loc.n is the CallExpr, not a param
				// Name; paramTag won't find it via oldLoc, so
				// this doesn't pollute exported escape tags.
				h := e.spill(ks[0], call)
				if h.dst != nil {
					h.dst.param = true
				}
			}
		}

	case ir.OINLCALL:
		call := call.(*ir.InlinedCallExpr)
		e.stmts(call.Body)
		for i, result := range call.ReturnVars {
			k := e.discardHole()
			if ks != nil {
				k = ks[i]
			}
			e.expr(k, result)
		}

	case ir.OAPPEND:
		call := call.(*ir.CallExpr)
		args := call.Args

		// Appendee slice may flow directly to the result, if
		// it has enough capacity. Alternatively, a new heap
		// slice might be allocated, and all slice elements
		// might flow to heap.
		// (If the appendee is a tracked variable, its elements stay
		// what its arrays hold, and so are the appended ones and
		// what the new array holds, below. See shares.go.)
		elemK := e.storeThrough(args[0])
		tracked := e.contentsOf(args[0]) != nil
		appendeeK := e.teeHole(ks[0], e.mutatorHole())
		if args[0].Type().Elem().HasPointers() && !tracked {
			// (The new array is in the heap, and where the
			// result goes: no other goroutine reaches it by
			// itself.)
			appendeeK = e.teeHole(appendeeK, e.appendHeapHole().deref(e.gd, call, "appendee slice"))
		}
		argument(appendeeK, args[0])

		if call.IsDDD {
			appendedK := e.discardHole()
			if args[1].Type().IsSlice() && args[1].Type().Elem().HasPointers() {
				appendedK = elemK.deref(e.gd, call, "appended slice...")
			}
			argument(appendedK, args[1])
		} else {
			for i := 1; i < len(args); i++ {
				argument(elemK, args[i])
			}
		}
		e.discard(call.RType)

		// Model the new backing store that might be allocated by append.
		// Its address flows to the result.
		// Users of escape analysis can look at the escape information for OAPPEND
		// and use that to decide where to allocate the backing store.
		backingStore := e.spill(ks[0], call)
		if c := e.contentsOf(args[0]); c != nil {
			// The new array holds what the appendee's arrays hold.
			e.flow(backingStore, c)
		}
		// As we have a boolean to prevent reuse, we can treat these allocations as outside any loops.
		backingStore.dst.loopDepth = 0

	case ir.OCOPY:
		call := call.(*ir.BinaryExpr)
		argument(e.mutatorHole(), call.X)

		copiedK := e.discardHole()
		if call.Y.Type().IsSlice() && call.Y.Type().Elem().HasPointers() {
			copiedK = e.heapHole().deref(e.gd, call, "copied slice")
		}
		argument(copiedK, call.Y)
		e.discard(call.RType)

	case ir.OPANIC:
		call := call.(*ir.UnaryExpr)
		argument(e.heapHole(), call.X)

	case ir.OCOMPLEX:
		call := call.(*ir.BinaryExpr)
		e.discard(call.X)
		e.discard(call.Y)

	case ir.ODELETE, ir.OPRINT, ir.OPRINTLN, ir.ORECOVER:
		call := call.(*ir.CallExpr)
		for _, arg := range call.Args {
			e.discard(arg)
		}
		e.discard(call.RType)
		// Note: keys used in map deletes do not need to escape.
		// See "Hashing Pointers" doc in internal/runtime/maps/map.go.

	case ir.OMIN, ir.OMAX:
		call := call.(*ir.CallExpr)
		for _, arg := range call.Args {
			argument(ks[0], arg)
		}
		e.discard(call.RType)

	case ir.OLEN, ir.OCAP, ir.OREAL, ir.OIMAG, ir.OCLOSE:
		call := call.(*ir.UnaryExpr)
		e.discard(call.X)

	case ir.OCLEAR:
		call := call.(*ir.UnaryExpr)
		argument(e.mutatorHole(), call.X)

	case ir.OUNSAFESTRINGDATA, ir.OUNSAFESLICEDATA:
		call := call.(*ir.UnaryExpr)
		argument(ks[0], call.X)

	case ir.OUNSAFEADD, ir.OUNSAFESLICE, ir.OUNSAFESTRING:
		call := call.(*ir.BinaryExpr)
		k := ks[0]
		if call.Op() == ir.OUNSAFESTRING {
			k = e.aliasHole(k)
		}
		argument(k, call.X)
		e.discard(call.Y)
		e.discard(call.RType)
	}
}

// goDeferStmt analyzes a "go" or "defer" statement.
func (e *escape) goDeferStmt(n *ir.GoDeferStmt) {
	k := e.heapHole()
	if n.Op() == ir.ODEFER && e.loopDepth == 1 && n.DeferAt == nil {
		// Top-level defer arguments don't escape to the heap,
		// but they do need to last until they're invoked.
		k = e.later(e.discardHole())

		// force stack allocation of defer record, unless
		// open-coded defers are used (see ssa.go)
		n.SetEsc(ir.EscNever)
	}

	// If the function is already a zero argument/result function call,
	// just escape analyze it normally.
	//
	// Note that the runtime is aware of this optimization for
	// "go" statements that start in reflect.makeFuncStub or
	// reflect.methodValueCall.

	call, ok := n.Call.(*ir.CallExpr)
	if !ok || call.Op() != ir.OCALLFUNC {
		e.gd.FatalfAt(n.Pos(), "expected function call: %v", n.Call)
	}
	if sig := call.Fun.Type(); sig.NumParams()+sig.NumResults() != 0 {
		e.gd.FatalfAt(n.Pos(), "expected signature without parameters or results: %v", sig)
	}

	if clo, ok := call.Fun.(*ir.ClosureExpr); ok && n.Op() == ir.OGO {
		clo.IsGoWrap = true
	}

	e.expr(k, call.Fun)
}

// rewriteArgument rewrites the argument arg of the given call expression.
// fns is the list of statically known callees, if any.
func (e *escape) rewriteArgument(arg ir.Node, call *ir.CallExpr, fns []*ir.Name) {
	var pragma ir.PragmaFlag
	for _, fn := range fns {
		if fn.Func != nil {
			pragma |= fn.Func.Pragma
		}
	}
	if pragma&(ir.UintptrKeepAlive|ir.UintptrEscapes) == 0 {
		return
	}

	// unsafeUintptr rewrites "uintptr(ptr)" arguments to syscall-like
	// functions, so that ptr is kept alive and/or escaped as
	// appropriate. unsafeUintptr also reports whether it modified arg0.
	unsafeUintptr := func(arg ir.Node) {
		// If the argument is really a pointer being converted to uintptr,
		// arrange for the pointer to be kept alive until the call
		// returns, by copying it into a temp and marking that temp still
		// alive when we pop the temp stack.
		conv, ok := arg.(*ir.ConvExpr)
		if !ok || conv.Op() != ir.OCONVNOP {
			return // not a conversion
		}
		if !conv.X.Type().IsUnsafePtr() || !conv.Type().IsUintptr() {
			return // not an unsafe.Pointer->uintptr conversion
		}

		// Create and declare a new pointer-typed temp variable.
		//
		// TODO(mdempsky): This potentially violates the Go spec's order
		// of evaluations, by evaluating arg.X before any other
		// operands.
		tmp := e.copyExpr(conv.Pos(), conv.X, call.PtrInit())
		conv.X = tmp

		k := e.mutatorHole()
		if pragma&ir.UintptrEscapes != 0 {
			k = e.heapHole().note(e.gd, conv, "//go:uintptrescapes")
		}
		e.flow(k, e.oldLoc(tmp))

		if pragma&ir.UintptrKeepAlive != 0 {
			tmp.SetAddrtaken(true) // ensure SSA keeps the tmp variable
			call.KeepAlive = append(call.KeepAlive, tmp)
		}
	}

	// For variadic functions, the compiler has already rewritten:
	//
	//     f(a, b, c)
	//
	// to:
	//
	//     f([]T{a, b, c}...)
	//
	// So we need to look into slice elements to handle uintptr(ptr)
	// arguments to variadic syscall-like functions correctly.
	if arg.Op() == ir.OSLICELIT {
		list := arg.(*ir.CompLitExpr).List
		for _, el := range list {
			if el.Op() == ir.OKEY {
				el = el.(*ir.KeyExpr).Value
			}
			unsafeUintptr(el)
		}
	} else {
		unsafeUintptr(arg)
	}
}

// copyExpr creates and returns a new temporary variable within fn;
// appends statements to init to declare and initialize it to expr;
// and escape analyzes the data flow.
func (e *escape) copyExpr(pos src.XPos, expr ir.Node, init *ir.Nodes) *ir.Name {
	if ir.HasUniquePos(e.gd, expr) {
		pos = expr.Pos()
	}

	tmp := typecheck.TempAt(e.gd, pos, e.curfn, expr.Type())

	stmts := []ir.Node{
		ir.NewDecl(e.gd, pos, ir.ODCL, tmp),
		ir.NewAssignStmt(e.gd, pos, tmp, expr),
	}
	typecheck.Stmts(e.gd, stmts)
	init.Append(stmts...)

	e.newLoc(tmp, true)
	e.stmts(stmts)

	return tmp
}

// dynamicArgHole returns the hole for arg, an argument (or the
// receiver) of a call whose callee is not statically known.
//
// gd escape-bits: walk's wrap (wrapEscapeCandidateArgs) re-homes one
// shape of argument, &x for a local x that isCandidateLoc admits: when
// the callee's escape mask says the parameter escapes, it copies x to
// the heap, points x's box at the copy and passes that instead. So
// only such an argument goes to the candidate location. Any other one
// reaches the callee as it is: the address of a field or element
// (&x.f, &a[i]), a pointer variable, a conversion (unsafe.Pointer(&x)),
// a struct, slice or interface holding pointers, an interface
// receiver. Those flow to the heap, as every argument to an unknown
// callee does in stock Go. See doc/gd/escape-bits.md.
//
// argIdx is at least the argument's position among the call's
// arguments. One the escape mask has no bit for (ir.EscMaskArgs) is
// not re-homed either.
func (e *escape) dynamicArgHole(arg ir.Node, argIdx int) hole {
	if argIdx < ir.EscMaskArgs && arg.Op() == ir.OADDR && isCandidateLoc(arg.(*ir.AddrExpr).X) {
		return e.candidateHole()
	}
	// The heap, where the function that is called says whether
	// another goroutine reaches it (callbits.go).
	if e.dynCall != nil && e.dynArg >= 0 {
		if r := e.callArgRoot(e.dynCall, e.dynArg); r != nil {
			e.noteKeptCheck(r, e.dynCall, arg)
			return r.asHole()
		}
		// Or what the linker knows of all the functions that it may
		// be (sharelink.go).
		if r := e.linkRoot(e.dynKey, e.dynArg); r != nil {
			return r.asHole()
		}
	}
	return e.heapHole()
}

// mergedTagHole returns the hole for arg, the argument at paramIdx (-1
// for the receiver) of a call whose statically-known callees are fns.
// With no known callee it defers to dynamicArgHole, the gd escape-bits
// candidate path; fntype is the call's signature.
func (e *escape) mergedTagHole(ks []hole, fns []*ir.Name, paramIdx int, fntype *types.Type, arg ir.Node) hole {
	nParams := len(fntype.Params())
	if len(fns) == 0 {
		// (paramIdx+1 is the argument's position when the first
		// argument is a method expression's receiver.)
		return e.dynamicArgHole(arg, paramIdx+1)
	}
	holes := make([]hole, 0, len(fns))
	for _, f := range fns {
		offset := nParams - len(f.Type().Params())
		j := paramIdx - offset
		var p *types.Field
		if j >= 0 {
			p = f.Type().Params()[j]
		} else {
			p = f.Type().Recv()
		}
		holes = append(holes, e.tagHole(ks, f, p))
	}
	return e.teeHole(holes...)
}

// tagHole returns a hole for evaluating an argument passed to param.
// ks should contain the holes representing where the function
// callee's results flows. fn is the statically-known callee function,
// if any.
func (e *escape) tagHole(ks []hole, fn *ir.Name, param *types.Field) hole {
	// If this is a dynamic call, we can't rely on param.Note. (A
	// call's arguments to an unknown callee go through
	// dynamicArgHole instead.)
	if fn == nil {
		return e.heapHole()
	}

	if e.inMutualBatch(fn) {
		if param.Nname == nil {
			return e.discardHole()
		}
		k := e.addr(param.Nname.(*ir.Name))
		k.argFlow = true
		return k
	}

	// Call to previously tagged function.

	var tagKs []hole
	esc := parseLeaks(e.gd, param.Note)

	if x := esc.Heap(); x >= 0 {
		tagKs = append(tagKs, e.sharedTagHoles(ks, fn, param, esc, x)...)
	} else {
		// It need not be in the heap, and another goroutine may
		// reach it all the same: the function converts it to an
		// interface type whose methods are not known to keep it,
		// and returns that.
		tagKs = append(tagKs, e.shareOnlyHoles(fn, param)...)
	}
	if x := esc.Mutator(); x >= 0 {
		tagKs = append(tagKs, e.mutatorHole().shift(e.gd, x))
	}
	if x := esc.Callee(); x >= 0 {
		tagKs = append(tagKs, e.calleeHole().shift(e.gd, x))
	}

	if ks != nil {
		for i := 0; i < numEscResults; i++ {
			if x := esc.Result(i); x >= 0 {
				k := ks[i].shift(e.gd, x)
				// A function that keeps the parameter only by
				// calls that check does not return it either,
				// but as what a method of its own returned.
				k.recvRes = x == 0 && e.checkedArg
				k.loose = true
				tagKs = append(tagKs, k)
			}
		}
	}

	return e.teeHole(tagKs...)
}

// resolveAssignedCallees resolves all assignment RHS values to static
// callee names, skipping zero-value assignments since nil panics on
// call and can't cause escape.
func resolveAssignedCallees(assigns []*ir.AssignStmt) []*ir.Name {
	fns := make([]*ir.Name, 0, len(assigns))
	for _, as := range assigns {
		if ir.IsZero(as.Y) {
			continue // zero value panics on call; skip
		}
		callee := ir.StaticCalleeName(as.Y)
		if callee == nil {
			return nil
		}
		if callee.Func != nil && callee.Func.Pragma&(ir.UintptrKeepAlive|ir.UintptrEscapes) != 0 {
			return nil
		}
		fns = append(fns, callee)
	}
	return fns
}

func isNoEscape(fns []*ir.Name) bool {
	if len(fns) != 1 || fns[0].Sym().Pkg.Path != "internal/abi" {
		return false
	}
	// (Address makes an integer that is never a pointer again.)
	return fns[0].Sym().Name == "NoEscape" || fns[0].Sym().Name == "Address"
}

func isEscapeNonString(fns []*ir.Name, fntype *types.Type) bool {
	return len(fns) == 1 &&
		fns[0].Sym().Pkg.Path == "internal/abi" &&
		strings.HasPrefix(fns[0].Sym().Name, "EscapeNonString[") &&
		len(fntype.Params()) == 2 && fntype.Params()[1].Type.IsShape()
}

func hasNonStringPointers(t *types.Type) bool {
	if !t.HasPointers() {
		return false
	}
	switch t.Kind() {
	case types.TSTRING:
		return false
	case types.TSTRUCT:
		for _, f := range t.Fields() {
			if hasNonStringPointers(f.Type) {
				return true
			}
		}
		return false
	case types.TARRAY:
		return hasNonStringPointers(t.Elem())
	}
	return true
}
