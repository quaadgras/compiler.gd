// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package escape

import (
	"fmt"
	"go/constant"
	"go/token"
	"internal/buildcfg"
	"internal/goexperiment"

	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/logopt"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
	"cmd/internal/src"
)

// Escape analysis.
//
// Here we analyze functions to determine which Go variables
// (including implicit allocations such as calls to "new" or "make",
// composite literals, etc.) can be allocated on the stack. The two
// key invariants we have to ensure are: (1) pointers to stack objects
// cannot be stored in the heap, and (2) pointers to a stack object
// cannot outlive that object (e.g., because the declaring function
// returned and destroyed the object's stack frame, or its space is
// reused across loop iterations for logically distinct variables).
//
// We implement this with a static data-flow analysis of the AST.
// First, we construct a directed weighted graph where vertices
// (termed "locations") represent variables allocated by statements
// and expressions, and edges represent assignments between variables
// (with weights representing addressing/dereference counts).
//
// Next we walk the graph looking for assignment paths that might
// violate the invariants stated above. If a variable v's address is
// stored in the heap or elsewhere that may outlive it, then v is
// marked as requiring heap allocation.
//
// To support interprocedural analysis, we also record data-flow from
// each function's parameters to the heap and to its result
// parameters. This information is summarized as "parameter tags",
// which are used at static call sites to improve escape analysis of
// function arguments.

// Constructing the location graph.
//
// Every allocating statement (e.g., variable declaration) or
// expression (e.g., "new" or "make") is first mapped to a unique
// "location."
//
// We also model every Go assignment as a directed edges between
// locations. The number of dereference operations minus the number of
// addressing operations is recorded as the edge's weight (termed
// "derefs"). For example:
//
//     p = &q    // -1
//     p = q     //  0
//     p = *q    //  1
//     p = **q   //  2
//
//     p = **&**&q  // 2
//
// Note that the & operator can only be applied to addressable
// expressions, and the expression &x itself is not addressable, so
// derefs cannot go below -1.
//
// Every Go language construct is lowered into this representation,
// generally without sensitivity to flow, path, or context; and
// without distinguishing elements within a compound variable. For
// example:
//
//     var x struct { f, g *int }
//     var u []*int
//
//     x.f = u[0]
//
// is modeled simply as
//
//     x = *u
//
// That is, we don't distinguish x.f from x.g, or u[0] from u[1],
// u[2], etc. However, we do record the implicit dereference involved
// in indexing a slice.

// A batch holds escape analysis state that's shared across an entire
// batch of functions being analyzed at once.
type batch struct {
	gd *base.Invocation

	allLocs []*location

	// contents and sources are of the tracked variables: shares.go.
	contents        map[*ir.Name]*location
	sources         map[*ir.Name][]ir.Node
	closures        []closure
	reassignOracles map[*ir.Func]*ir.ReassignOracle

	heapLoc      location
	mutatorLoc   location
	ownHeapLoc   location // the heap, in an object that no other goroutine reaches by itself (callbits.go)
	shareLoc     location // where another goroutine reaches, without being the heap (shares.go)
	moveLoc      location // where a pointer that is handed over goes: the heap, and out of its goroutine's hands (move.go)
	convShareLoc location // shareLoc for what is shared by being converted to an interface type (recv.go): that says how it is treated, and nothing has its pointer by it (move.go)
	checkLoc     location // the heap, where a function keeps a parameter only by calls that check (ir.Func.KeptChecked, move.go)
	recvLoc      location // ownHeapLoc for the receiver of a call of an interface's method (recv.go): whether that keeps the pointer, recvHeldLoc says
	recvHeldLoc  location // where the receiver of such a call goes besides: the method may keep it, unless its type says not (edge.recvOK, move.go)
	recvAnyLoc   location // where an interface goes that is asserted to have other methods than those of its type: any method of what it holds may be called
	recvNothing  map[convKey]bool
	handTypes    map[*types.Type]bool
	moves        []moveSend
	paramClass   map[*ir.Name]*ir.Name // the variables that only hold a parameter's pointer, and that parameter (move.go)
	returns      map[*ir.Func]*returned
	deferWraps   map[*ir.Func]*deferWrap
	calleeLoc    location
	blankLoc     location
	candidateLoc location

	// gd escape-bits: curStmt identifies the statement being
	// analyzed (nextStmt numbers them), and addrStmts records, per
	// escape-candidate-shaped location and statement, how the
	// statement takes the location's address; see noteAddrTaken.
	curStmt, nextStmt int32

	// Dynamic roots (shares.go): by statement and path, the statements
	// that are one plain assignment, and those with a call.
	dynRoots       map[dynKey]*location
	noteParam      string // -m=4: the parameter that noteParamRoot is told of
	fns            []*ir.Func
	typedShares    map[any]*location // typed.go
	typedShareList []*location
	unsafeFns      map[*ir.Func]bool
	opaqueFns      map[*ir.Func]bool
	opaque         uint8
	copyOf         map[*ir.Name]*ir.Name  // deep.go: a variable that is a copy of a parameter
	walkRoot       *location              // of the walk that is going on, for messages
	walkOwnResult  bool                   // the walk came to the parameter that is noted through a result of its function
	madeAt         map[*location]src.XPos // with -m=4: where a location was made
	leads          map[any]bool
	unknownSrc     location // where a pointer comes from that the graph does not follow: a global, the result of a call that is not recorded (groups.go)
	dirt           map[*ir.Func]*location
	strays         []*strayStore
	lost           map[*ir.Func]bool // the functions that store through something that is not a variable
	resGroups      map[*ir.Func]*location
	conds          map[*location]*groupCond
	storeRoot      map[*location]bool
	infos          map[*location]*groupInfo
	memberSets     map[*location]map[*location]bool
	storeRoots     map[*ir.Func]*storeRoots
	storeTypes     map[*ir.Func]*[16]storeSet // the function's own stores (storeset.go)
	storeSets      map[*ir.Func]*[16]storeSet // the parameters' store types, solved
	relies         []reliance
	needGroups     []*location // the groups whose need is known
	reachMem       map[any]bool
	skipStoreTypes bool // storeThroughSharedArg
	unknownSrcs    map[*types.Type]*location
	varGroups      map[*ir.Name]*location
	pending        map[any]*location
	storedRoots    map[*ir.Func]*location
	reach          map[*location]map[*location]bool
	reachCrossed   map[*location]bool
	memberBad      map[*location]bool
	derived        map[*ir.Name]uint16 // the parameters that a variable is reached from (deep.go)
	derivedMask    func(ir.Node) (uint16, bool)
	derivedOf      map[*ir.Func]func(ir.Node) (uint16, bool)
	simpleStmts    map[int32]bool
	stmtCalls      map[int32]bool
	calls          []*callSite      // callbits.go
	convOKs        map[convKey]bool // recv.go
	retTags        map[*ir.Func]ir.Node
	callArgs       []ir.Node    // the arguments of the direct call whose tags are read (callbits.go)
	curSite        *callSite    // the call whose arguments are evaluated, if it is recorded
	dynCall        *ir.CallExpr // the call of an unknown function whose argument's hole is made
	dynArg         int          // that argument's place
	dynKey         string       // and the key of the class of functions that the call may be of (ir.ShareKeyFunc), or ""
	argGroups      []*location  // the membership roots of the arguments of the call whose argument's hole is made (groups.go)
	mixGroup       *location    // the group of the arguments of that call that a function which it passes them on to may mix (groups.go)
	recvArg        bool         // and that a method of its own may keep
	recvAnyArg     bool         // whichever
	checkCallsArg  bool         // and a call that checks is one of them (ir.Func.KeptCalls)
	checkedArg     bool         // the argument whose hole is made is of a parameter that is kept only by calls that check (move.go)
	addrStmts      map[addrStmtKey]uint8
}

// A closure holds a closure expression and its spill hole (i.e.,
// where the hole representing storing into its closure record).
type closure struct {
	k   hole
	clo *ir.ClosureExpr
}

// An escape holds state specific to a single function being analyzed
// within a batch.
type escape struct {
	*batch

	curfn *ir.Func // function being analyzed

	labels map[*types.Sym]labelState // known labels

	// loopDepth counts the current loop nesting depth within
	// curfn. It increments within each "for" loop and at each
	// label with a corresponding backwards "goto" (i.e.,
	// unstructured loop).
	loopDepth int

	// returning is set while a return statement's assignments to
	// the results are made (location.assignedInBody).
	returning bool
}

func Funcs(gd *base.Invocation, all []*ir.Func) {
	// Make a cache of ir.ReassignOracles. The cache is lazily populated.
	// TODO(thepudds): consider adding a field on ir.Func instead. We might also be able
	// to use that field elsewhere, like in walk. See discussion in https://go.dev/cl/688075.
	reassignOracles := make(map[*ir.Func]*ir.ReassignOracle)

	// A conversion to an interface type answers for the methods of
	// the type that is converted (recv.go), which it can only do for
	// those that are analysed: the methods first, then, and the
	// functions that they call, so that only a function that a method
	// needs is analysed before the methods it converts for. (Not as
	// callees of the function that converts: the batches would be
	// larger, and escaping in a batch is less exact.)
	if gd.Debug.NoLocalRecv == 0 && gd.Debug.NoShare == 0 && gd.Debug.NoLocal == 0 {
		ordered := make([]*ir.Func, 0, len(all))
		for _, fn := range all {
			if fn.Type().Recv() != nil {
				ordered = append(ordered, fn)
			}
		}
		for _, fn := range all {
			if fn.Type().Recv() == nil {
				ordered = append(ordered, fn)
			}
		}
		all = ordered
	}
	handTypes := handOverTypes(gd, all)
	ir.VisitFuncsBottomUp(all, func(list []*ir.Func, recursive bool) {
		Batch(gd, list, reassignOracles, handTypes)
	})
}

// Batch performs escape analysis on a minimal batch of
// functions.
func Batch(gd *base.Invocation, fns []*ir.Func, reassignOracles map[*ir.Func]*ir.ReassignOracle, handTypes map[*types.Type]bool) {
	var b batch
	b.gd = gd
	b.fns = fns
	b.unknownSrc.unknown = true
	b.heapLoc.attrs = attrEscapes | attrPersists | attrMutates | attrCalls | attrShares
	b.mutatorLoc.attrs = attrMutates
	b.shareLoc.attrs = attrShares
	b.convShareLoc.attrs = attrShares
	b.ownHeapLoc.attrs = attrEscapes | attrPersists | attrMutates | attrCalls
	b.moveLoc.attrs = attrEscapes | attrPersists | attrMutates | attrCalls
	b.checkLoc.attrs = b.heapLoc.attrs
	b.recvLoc.attrs = b.ownHeapLoc.attrs
	b.handTypes = handTypes
	b.calleeLoc.attrs = attrCalls
	// candidateLoc plays the role of heapLoc for values flowing into
	// dynamic-callee edges, but with attrCandidateEscape in place of
	// attrEscapes. At finalize, solely-candidate values become
	// EscCandidate rather than EscHeap; walk then inserts the
	// runtime.maybeEscape* wrap at the indirect call site. See
	// doc/gd/escape-bits.md.
	b.candidateLoc.attrs = attrCandidateEscape | attrPersists | attrMutates | attrCalls | attrShares
	b.reassignOracles = reassignOracles

	if gd.Debug.FieldCensus != 0 {
		fieldCensus(gd, fns)
	}

	// Construct data-flow graph from syntax trees.
	for _, fn := range fns {
		if gd.Flag.W > 1 {
			s := fmt.Sprintf("\nbefore escape %v", fn)
			ir.Dump(s, fn)
		}
		b.initFunc(fn)
		markFresh(fn)
	}
	for _, fn := range fns {
		if !fn.IsClosure() {
			b.walkFunc(fn)
		}
	}

	// We've walked the function bodies, so we've seen everywhere a
	// variable might be reassigned or have its address taken. Now we
	// can decide whether closures should capture their free variables
	// by value or reference.
	for _, closure := range b.closures {
		b.flowClosure(closure.k, closure.clo)
	}
	b.closures = nil

	for _, loc := range b.allLocs {
		// Try to replace some non-constant expressions with literals.
		b.rewriteWithLiterals(loc.n, loc.curfn)

		// Check if the node must be heap allocated for certain reasons
		// such as OMAKESLICE for a large slice.
		if why := HeapAllocReason(gd, loc.n); why != "" {
			b.flow(b.heapHole().addr(gd, loc.n, why), loc)
		}
	}

	if b.gd.Flag.LowerM >= 4 {
		// Everything that the functions do which shares a value by
		// itself: the walks only say the nearest for each value.
		for _, root := range []*location{&b.shareLoc, &b.convShareLoc} {
			for _, edge := range root.edges {
				src := edge.src
				what := "a value"
				if src.n != nil {
					what = fmt.Sprintf("%v", src.n)
				}
				for n := edge.notes; n != nil; n = n.next {
					fmt.Printf("%s: %s goes to %s: %v (%s)\n", b.gd.FmtPos(n.where.Pos()), what, b.explainLoc(root), n.where, n.why)
					break
				}
				if edge.notes == nil && src.n != nil {
					fmt.Printf("%s: %s goes to %s\n", b.gd.FmtPos(src.n.Pos()), what, b.explainLoc(root))
				}
			}
		}
	}
	b.walkAll()
	b.finish(fns)

	// Phase F3: detector for trivial forwarders. Runs after
	// finish so leaks/EscMask are populated and the matcher can
	// rely on the body being in its post-typecheck shape. F4
	// consumes ir.Func.GdForwarder to synthesise compute fns;
	// without F4 the field is inert and the wrapper continues to
	// publish its conservative static mask.
	if gd.Debug.GdForwarderDisable == 0 {
		for _, fn := range fns {
			DetectForwarders(gd, fn)
		}
	}
}

func (b *batch) with(fn *ir.Func) *escape {
	return &escape{
		batch:     b,
		curfn:     fn,
		loopDepth: 1,
	}
}

func (b *batch) initFunc(fn *ir.Func) {
	e := b.with(fn)
	if fn.Esc() != escFuncUnknown {
		b.gd.Fatalf("unexpected node: %v", fn)
	}
	fn.SetEsc(escFuncPlanned)
	if b.gd.Flag.LowerM > 3 {
		ir.Dump("escAnalyze", fn)
	}

	if usesUnsafe(fn) {
		if b.unsafeFns == nil {
			b.unsafeFns = make(map[*ir.Func]bool)
		}
		b.unsafeFns[fn] = true
	}

	// Allocate locations for local variables.
	for _, n := range fn.Dcl {
		e.newLoc(n, true)
	}

	// Also for hidden parameters (e.g., the ".this" parameter to a
	// method value wrapper).
	if fn.OClosure == nil {
		for _, n := range fn.ClosureVars {
			e.newLoc(n.Canonical(), true)
		}
	}

	// Initialize resultIndex for result parameters.
	for i, f := range fn.Type().Results() {
		e.oldLoc(f.Nname.(*ir.Name)).resultIndex = 1 + i
	}

	b.trackPointers(fn)
	b.deriveFromParams(fn)
	b.trackParams(fn)
}

func (b *batch) walkFunc(fn *ir.Func) {
	e := b.with(fn)
	fn.SetEsc(escFuncStarted)

	// Identify labels that mark the head of an unstructured loop.
	ir.Visit(fn, func(n ir.Node) {
		switch n.Op() {
		case ir.OLABEL:
			n := n.(*ir.LabelStmt)
			if n.Label.IsBlank() {
				break
			}
			if e.labels == nil {
				e.labels = make(map[*types.Sym]labelState)
			}
			e.labels[n.Label] = nonlooping

		case ir.OGOTO:
			// If we visited the label before the goto,
			// then this is a looping label.
			n := n.(*ir.BranchStmt)
			if e.labels[n.Label] == nonlooping {
				e.labels[n.Label] = looping
			}
		}
	})

	e.block(fn.Body)

	if len(e.labels) != 0 {
		b.gd.FatalfAt(fn.Pos(), "leftover labels after walkFunc")
	}
}

func (b *batch) flowClosure(k hole, clo *ir.ClosureExpr) {
	for _, cv := range clo.Func.ClosureVars {
		n := cv.Canonical()
		loc := b.oldLoc(cv)
		if !loc.captured {
			b.gd.FatalfAt(cv.Pos(), "closure variable never captured: %v", cv)
		}

		// Capture by value for variables <= 128 bytes that are never reassigned.
		n.SetByval(b.gd, !loc.addrtaken && !loc.reassigned && n.Type().Size() <= 128)
		if !n.Byval() {
			n.SetAddrtaken(true)
			if n.Sym().Name == typecheck.LocalDictName {
				b.gd.FatalfAt(n.Pos(), "dictionary variable not captured by value")
			}
		}

		if b.gd.Flag.LowerM > 1 {
			how := "ref"
			if n.Byval() {
				how = "value"
			}
			b.gd.WarnfAt(n.Pos(), "%v capturing by %s: %v (addr=%v assign=%v width=%d)", n.Curfn, how, n, loc.addrtaken, loc.reassigned, n.Type().Size())
		}

		// Flow captured variables to closure.
		k := k
		if !cv.Byval() {
			k = k.addr(b.gd, cv, "reference")
		}
		b.flow(k.note(b.gd, cv, "captured by a closure"), loc)
	}
}

// isCandidateLoc reports whether n is a node whose storage may safely
// be re-homed by the runtime escape-bits wrap: a local PAUTO name
// whose address is taken. Walk gives such a variable a box, a pointer
// to its storage (a stack slot at first) that every use of the
// variable goes through, so when the wrap at a dynamic call moves the
// variable to the heap, later uses agree with the callee's view.
//
// The wrap only handles the variable's address passed as an argument,
// &x (see dynamicArgHole), and only when no other pointer to the stack
// slot is kept anywhere (see location.addrHeld); finish checks that.
//
// Fresh allocations, composite literals, closures, maps, and iface
// conversions are excluded: their lifetimes interact with consumer
// patterns the wrap can't safely preserve (e.g. capturing references).
func isCandidateLoc(n ir.Node) bool {
	if n == nil {
		return false
	}
	switch n.Op() {
	case ir.ONAME:
		name := n.(*ir.Name)
		// Only user-written locals-with-address-taken need the
		// box. Params already have their own Heapaddr dance;
		// compiler-synthesised temporaries (AutoTemp, names that
		// look like ".autotmp_*", etc.) opt out because their
		// lifetime and aliasing guarantees come from the walk
		// pass that created them, not from surface-level
		// semantics.
		if name.Class != ir.PAUTO {
			return false
		}
		if !name.Addrtaken() {
			return false
		}
		if name.AutoTemp() {
			return false
		}
		// Skip types the runtime wrap can't materialize: zero-
		// sized types and not-in-heap types.
		t := name.Type()
		if t == nil || t.Size() == 0 || t.NotInHeap() {
			return false
		}
		return true
	}
	return false
}

// shouldEnableEscapeCandidate is the activation gate for the gd
// escape-bits Phase D optimization. Now that dynamicArgHole only
// routes &x arguments through candidateHole, and isCandidateLoc is
// restricted to ONAME-with-Addrtaken, the remaining known-unsafe
// shapes are all funnelled back to stock heapLoc. The gate can
// therefore open fork-wide; it stays as a kill switch in case a new
// failure mode surfaces.
func shouldEnableEscapeCandidate(fn *ir.Func) bool {
	if fn == nil || fn.Nname == nil {
		return false
	}
	if buildcfg.GOARCH == "wasm" && buildcfg.GOWASM.GC {
		// The stack is not memory that a copy could be taken from.
		return false
	}
	return true
}

// outBufUse is a function's outBuf, by ordinal.
type outBufUse struct {
	fn  *ir.Func
	ord int
}

func (b *batch) finish(fns []*ir.Func) {
	b.solveGroups(fns)
	b.solveStoreSets(fns)
	b.markUnshared(fns)
	b.markMoves()
	b.markKeptChecks(fns)
	b.markReturnedOther()
	// gd Phase G: the allocations to place in a caller's buffer for
	// a result (phaseg.go), decided while locations are still bound.
	phaseG := b.phaseGTags(fns)
	usedOutBufs := make(map[outBufUse]bool)

	// Record parameter tags for package export data.
	for _, fn := range fns {
		fn.SetEsc(escFuncTagged)

		b.paramShares(fn)
		for i, param := range fn.Type().RecvParams() {
			param.Note = b.paramTag(fn, 1+i, param)
		}

		// gd escape-bits: fold each param's leaks into a compact
		// per-argument heap-escape mask. Consumers (closure/itab
		// carriers) check bit k+1 to decide whether to materialize
		// arg k before an indirect call. Bit 0 is reserved.
		fn.EscMask = computeEscMask(b.gd, fn.Type())
	}

	for _, loc := range b.allLocs {
		n := loc.n
		if n == nil {
			continue
		}

		if n.Op() == ir.ONAME {
			n := n.(*ir.Name)
			n.Opt = nil
		}

		// Update n.Esc based on escape analysis results.

		// Omit escape diagnostics for go/defer wrappers, at least for now.
		// Historically, we haven't printed them, and test cases don't expect them.
		// TODO(mdempsky): Update tests to expect this.
		goDeferWrapper := n.Op() == ir.OCLOSURE && n.(*ir.ClosureExpr).Func.Wrapper()

		// gd Phase G synthesises a spill loc for every pointer-returning
		// call expression so the solver can decide whether to forward a
		// caller stack buffer or fall through to heap. That makes call
		// exprs appear in the loc set — but stock Go never had them
		// here, so emitting "X escapes to heap" / "X does not escape"
		// for the call itself is purely fork-internal noise. Suppress
		// the user-facing diagnostic for these synthetic spills; the
		// existing diagnostics on the receiving sink (if any) already
		// communicate whether the result escapes.
		phaseGCallSpill := n.Op() == ir.OCALLFUNC || n.Op() == ir.OCALLINTER || n.Op() == ir.OCALLMETH

		// gd escape-bits: a loc whose address reaches the heap only
		// as an argument of dynamic calls (attrCandidateEscape: the
		// solver routed its escape through candidateHole) becomes an
		// escape candidate if walk's wrap can re-home it: it stays on
		// the stack, behind a box, and the wrap at each such call
		// moves it to the heap when the callee's escape mask says the
		// argument escapes. Any other such loc escapes, as it would
		// in stock Go.
		escapes := loc.hasAttr(attrEscapes)
		candidate := false
		if !escapes && loc.hasAttr(attrCandidateEscape) {
			if !b.gd.Flag.CompilingRuntime && b.gd.Debug.GdEscBitsOff == 0 &&
				isCandidateLoc(n) && !loc.addrHeld &&
				shouldEnableEscapeCandidate(loc.curfn) {
				candidate = true
			} else {
				escapes = true
			}
		}

		if candidate {
			n.SetEsc(ir.EscHeap)
			n.SetEscCandidate(true)
		} else if escapes {
			if n.Op() == ir.ONAME {
				if b.gd.Flag.CompilingRuntime {
					b.gd.ErrorfAt(n.Pos(), 0, "%v escapes to heap, not allowed in runtime", n)
				}
				if b.gd.Flag.LowerM != 0 {
					b.gd.WarnfAt(n.Pos(), "moved to heap: %v", n)
				}
			} else {
				if b.gd.Flag.LowerM != 0 && !goDeferWrapper && !phaseGCallSpill {
					if n.Op() == ir.OAPPEND {
						b.gd.WarnfAt(n.Pos(), "append escapes to heap")
					} else {
						b.gd.WarnfAt(n.Pos(), "%v escapes to heap", n)
					}
				}
				if logopt.Enabled() {
					var e_curfn *ir.Func // TODO(mdempsky): Fix.
					logopt.LogOpt(b.gd, n.Pos(), "escape", "escape", ir.FuncName(e_curfn))
				}
			}
			n.SetEsc(ir.EscHeap)
			loc.heap = true
			b.markLocal(loc)
			// gd Phase G: an allocation phaseGTags placed in the
			// caller's buffer for a result: a local's storage
			// (ssagen's newHeapaddr), new or &T{} (walk), or a
			// call's result, made in the buffer the function was
			// passed (chain-fold: walk's forwardParentOutBuf).
			if ord, ok := phaseG[loc]; ok {
				usedOutBufs[outBufUse{loc.curfn, ord}] = true
				switch n := n.(type) {
				case *ir.Name:
					n.OutBufResultIdx = uint8(ord + 1)
				case *ir.CallExpr:
					n.GdForwardOutBufResult = uint8(ord + 1)
				default:
					allocs, _ := b.gd.PhaseGOutBufAllocs.(map[ir.Node]uint8)
					if allocs == nil {
						allocs = make(map[ir.Node]uint8)
						b.gd.PhaseGOutBufAllocs = allocs
					}
					allocs[n] = uint8(ord + 1)
				}
			}
		} else {
			if b.gd.Flag.LowerM != 0 && n.Op() != ir.ONAME && !goDeferWrapper && !phaseGCallSpill {
				if n.Op() == ir.OAPPEND {
					b.gd.WarnfAt(n.Pos(), "append does not escape")
				} else {
					b.gd.WarnfAt(n.Pos(), "%v does not escape", n)
				}
			}
			n.SetEsc(ir.EscNone)
			if !loc.hasAttr(attrPersists) {
				switch n.Op() {
				case ir.OCLOSURE:
					n := n.(*ir.ClosureExpr)
					n.SetTransient(true)
				case ir.OMETHVALUE:
					n := n.(*ir.SelectorExpr)
					n.SetTransient(true)
				case ir.OSLICELIT:
					n := n.(*ir.CompLitExpr)
					n.SetTransient(true)
				}
			}
		}

		// If the result of a string->[]byte conversion is never mutated,
		// then it can simply reuse the string's memory directly.
		if b.gd.Debug.ZeroCopy != 0 {
			if n, ok := n.(*ir.ConvExpr); ok && n.Op() == ir.OSTR2BYTES && !loc.hasAttr(attrMutates) {
				if b.gd.Flag.LowerM >= 1 {
					b.gd.WarnfAt(n.Pos(), "zero-copy string->[]byte conversion")
				}
				n.SetOp(ir.OSTR2BYTESTMP)
			}
		}
	}

	b.callBits()

	// gd Phase G: an outBuf param says whether the function places
	// anything in that buffer; callers that know it doesn't pass none
	// (ir.CalleeOutBufUnused). Nothing flows from the param during
	// escape analysis (walk makes the uses), so this replaces its tag;
	// "mutated" leaves the escape mask alone. Passing a buffer, or not,
	// is always sound: without one, the function allocates as stock.
	var used leaks
	used.AddMutator(b.gd, 0)
	for _, fn := range fns {
		k := 0
		for _, p := range fn.Type().Params() {
			if !p.IsOutBufParam() {
				continue
			}
			if usedOutBufs[outBufUse{fn, k}] {
				p.Note = used.Encode()
			} else {
				p.Note = ir.OutBufUnusedNote
			}
			k++
		}
	}

	if goexperiment.RuntimeFreegc {
		// Look for specific patterns of usage, such as appends
		// to slices that we can prove are not aliased.
		for _, fn := range fns {
			a := aliasAnalysis{gd: b.gd}
			a.analyze(fn)
		}
	}

	for _, fn := range fns {
		if ir.MatchAstDump(b.gd, fn, "escape") {
			ir.AstDump(b.gd, fn, "escape, "+ir.FuncName(fn))
		}
	}
}

// inMutualBatch reports whether function fn is in the batch of
// mutually recursive functions being analyzed. When this is true,
// fn has not yet been analyzed, so its parameters and results
// should be incorporated directly into the flow graph instead of
// relying on its escape analysis tagging.
func (b *batch) inMutualBatch(fn *ir.Name) bool {
	if fn.Defn != nil && fn.Defn.Esc() < escFuncTagged {
		if fn.Defn.Esc() == escFuncUnknown {
			b.gd.FatalfAt(fn.Pos(), "graph inconsistency: %v", fn)
		}
		return true
	}
	return false
}

const (
	escFuncUnknown = 0 + iota
	escFuncPlanned
	escFuncStarted
	escFuncTagged
)

// Mark labels that have no backjumps to them as not increasing e.loopdepth.
type labelState int

const (
	looping labelState = 1 + iota
	nonlooping
)

// computeEscMask returns the escape mask of a function with signature
// sig, from the leaks tags already installed on its params: see
// ir.EscMaskOf, which every carrier of a mask uses.
func computeEscMask(gd *base.Invocation, sig *types.Type) uint64 {
	if sig == nil || sig.Kind() != types.TFUNC {
		return 0
	}
	return ir.EscMaskOf(sig.RecvParams())
}

// ir.EscMaskOf reads the notes that leaks.Encode writes.
var (
	_ [leakResult0 - ir.EscNoteResult0]struct{}
	_ [ir.EscNoteResult0 - leakResult0]struct{}
	_ [-leakHeap]struct{}
	_ [escFuncTagged - ir.EscFuncTagged]struct{}
	_ [ir.EscFuncTagged - escFuncTagged]struct{}
)

func (b *batch) paramTag(fn *ir.Func, narg int, f *types.Field) string {
	name := func() string {
		if f.Nname != nil {
			return f.Nname.Sym().Name
		}
		return fmt.Sprintf("arg#%d", narg)
	}

	// Only report diagnostics for user code;
	// not for wrappers generated around them.
	// TODO(mdempsky): Generalize this.
	diagnose := b.gd.Flag.LowerM != 0 && !(fn.Wrapper() || fn.Dupok())

	if len(fn.Body) == 0 {
		// Assume that uintptr arguments must be held live across the call.
		// This is most important for syscall.Syscall.
		// See golang.org/issue/13372.
		// This really doesn't have much to do with escape analysis per se,
		// but we are reusing the ability to annotate an individual function
		// argument and pass those annotations along to importing code.
		fn.Pragma |= ir.UintptrKeepAlive

		if f.Type.IsUintptr() {
			if diagnose {
				b.gd.WarnfAt(f.Pos, "assuming %v is unsafe uintptr", name())
			}
			return ""
		}

		if !f.Type.HasPointers() { // don't bother tagging for scalars
			return ""
		}

		var esc leaks

		// External functions are assumed unsafe, unless
		// //go:noescape is given before the declaration.
		if fn.Pragma&ir.Noescape != 0 {
			if diagnose && f.Sym != nil {
				b.gd.WarnfAt(f.Pos, "%v does not escape", name())
			}
			esc.AddMutator(b.gd, 0)
			esc.AddCallee(b.gd, 0)
		} else {
			if diagnose && f.Sym != nil {
				b.gd.WarnfAt(f.Pos, "leaking param: %v", name())
			}
			esc.AddHeap(b.gd, 0)
		}

		return esc.Encode()
	}

	if fn.Pragma&ir.UintptrEscapes != 0 {
		if f.Type.IsUintptr() {
			if diagnose {
				b.gd.WarnfAt(f.Pos, "marking %v as escaping uintptr", name())
			}
			return ""
		}
		if f.IsDDD() && f.Type.Elem().IsUintptr() {
			// final argument is ...uintptr.
			if diagnose {
				b.gd.WarnfAt(f.Pos, "marking %v as escaping ...uintptr", name())
			}
			return ""
		}
	}

	if !f.Type.HasPointers() { // don't bother tagging for scalars
		return ""
	}

	// A parameter without a variable is unused and therefore does
	// not escape. (One without a name has a variable in a wrapper
	// made from an interface method's signature, K.M or k.M-fm for
	// interface { M(*T) }, which passes it on; in stock Go nothing
	// reads such a wrapper's tags, here its escape mask is made of
	// them.)
	if f.Nname == nil {
		var esc leaks
		return esc.Encode()
	}

	n := f.Nname.(*ir.Name)
	loc := b.oldLoc(n)
	esc := loc.paramEsc
	esc.Optimize(b.gd)

	if diagnose && !loc.hasAttr(attrEscapes) && f.Sym != nil && !f.Sym.IsBlank() {
		b.reportLeaks(f.Pos, name(), esc, fn.Type())
	}

	return b.shareNote(fn, narg-1, esc)
}

func (b *batch) reportLeaks(pos src.XPos, name string, esc leaks, sig *types.Type) {
	warned := false
	if x := esc.Heap(); x >= 0 {
		if x == 0 {
			b.gd.WarnfAt(pos, "leaking param: %v", name)
		} else {
			// TODO(mdempsky): Mention level=x like below?
			b.gd.WarnfAt(pos, "leaking param content: %v", name)
		}
		warned = true
	}
	for i := 0; i < numEscResults; i++ {
		if x := esc.Result(i); x >= 0 {
			res := sig.Result(i).Nname.Sym().Name
			b.gd.WarnfAt(pos, "leaking param: %v to result %v level=%d", name, res, x)
			warned = true
		}
	}

	if b.gd.Debug.EscapeMutationsCalls <= 0 {
		if !warned {
			b.gd.WarnfAt(pos, "%v does not escape", name)
		}
		return
	}

	if x := esc.Mutator(); x >= 0 {
		b.gd.WarnfAt(pos, "mutates param: %v derefs=%v", name, x)
		warned = true
	}
	if x := esc.Callee(); x >= 0 {
		b.gd.WarnfAt(pos, "calls param: %v derefs=%v", name, x)
		warned = true
	}

	if !warned {
		b.gd.WarnfAt(pos, "%v does not escape, mutate, or call", name)
	}
}

// rewriteWithLiterals attempts to replace certain non-constant expressions
// within n with a literal if possible.
func (b *batch) rewriteWithLiterals(n ir.Node, fn *ir.Func) {
	if n == nil || fn == nil {
		return
	}

	assignTemp := func(pos src.XPos, n ir.Node, init *ir.Nodes) {
		// Preserve any side effects of n by assigning it to an otherwise unused temp.
		tmp := typecheck.TempAt(b.gd, pos, fn, n.Type())
		init.Append(typecheck.Stmt(b.gd, ir.NewDecl(b.gd, pos, ir.ODCL, tmp)))
		init.Append(typecheck.Stmt(b.gd, ir.NewAssignStmt(b.gd, pos, tmp, n)))
	}

	switch n.Op() {
	case ir.OMAKESLICE:
		// Check if we can replace a non-constant argument to make with
		// a literal to allow for this slice to be stack allocated if otherwise allowed.
		n := n.(*ir.MakeExpr)

		r := &n.Cap
		if n.Cap == nil {
			r = &n.Len
		}

		if (*r).Op() != ir.OLITERAL {
			// Look up a cached ReassignOracle for the function, lazily computing one if needed.
			ro := b.reassignOracle(fn)
			if ro == nil {
				b.gd.Fatalf("no ReassignOracle for function %v with closure parent %v", fn, fn.ClosureParent)
			}

			s := ro.StaticValue(*r)
			switch s.Op() {
			case ir.OLITERAL:
				lit, ok := s.(*ir.BasicLit)
				if !ok || lit.Val().Kind() != constant.Int {
					b.gd.Fatalf("unexpected BasicLit Kind")
				}
				if constant.Compare(lit.Val(), token.GEQ, constant.MakeInt64(0)) {
					if !b.gd.LiteralAllocHash.MatchPosCtxt(b.gd.Ctxt, n.Pos(), nil) {
						// De-selected by literal alloc optimizations debug hash.
						return
					}
					// Preserve any side effects of the original expression, then replace it.
					assignTemp(n.Pos(), *r, n.PtrInit())
					*r = ir.NewBasicLit(b.gd, n.Pos(), (*r).Type(), lit.Val())
				}
			case ir.OLEN:
				x := ro.StaticValue(s.(*ir.UnaryExpr).X)
				if x.Op() == ir.OSLICELIT {
					x := x.(*ir.CompLitExpr)
					// Preserve any side effects of the original expression, then update the value.
					assignTemp(n.Pos(), *r, n.PtrInit())
					*r = ir.NewBasicLit(b.gd, n.Pos(), types.Types[types.TINT], constant.MakeInt64(x.Len))
				}
			}
		}
	case ir.OCONVIFACE:
		// Check if we can replace a non-constant expression in an interface conversion with
		// a literal to avoid heap allocating the underlying interface value.
		conv := n.(*ir.ConvExpr)
		if conv.X.Op() != ir.OLITERAL && !conv.X.Type().IsInterface() {
			// TODO(thepudds): likely could avoid some work by tightening the check of conv.X's type.
			// Look up a cached ReassignOracle for the function, lazily computing one if needed.
			ro := b.reassignOracle(fn)
			if ro == nil {
				b.gd.Fatalf("no ReassignOracle for function %v with closure parent %v", fn, fn.ClosureParent)
			}
			v := ro.StaticValue(conv.X)
			if v != nil && v.Op() == ir.OLITERAL && ir.ValidTypeForConst(b.gd, conv.X.Type(), v.Val()) {
				if !b.gd.LiteralAllocHash.MatchPosCtxt(b.gd.Ctxt, n.Pos(), nil) {
					// De-selected by literal alloc optimizations debug hash.
					return
				}
				if b.gd.Debug.EscapeDebug >= 3 {
					b.gd.WarnfAt(n.Pos(), "rewriting OCONVIFACE value from %v (%v) to %v (%v)", conv.X, conv.X.Type(), v, v.Type())
				}
				// Preserve any side effects of the original expression, then replace it.
				assignTemp(conv.Pos(), conv.X, conv.PtrInit())
				v := v.(*ir.BasicLit)
				conv.X = ir.NewBasicLit(b.gd, conv.Pos(), conv.X.Type(), v.Val())
				typecheck.Expr(b.gd, conv)
			}
		}
	}
}

// reassignOracle returns an initialized *ir.ReassignOracle for fn.
// If fn is a closure, it returns the ReassignOracle for the ultimate parent.
//
// A new ReassignOracle is initialized lazily if needed, and the result
// is cached to reduce duplicative work of preparing a ReassignOracle.
func (b *batch) reassignOracle(fn *ir.Func) *ir.ReassignOracle {
	if ro, ok := b.reassignOracles[fn]; ok {
		return ro // Hit.
	}

	// For closures, we want the ultimate parent's ReassignOracle,
	// so walk up the parent chain, if any.
	f := fn
	for f.ClosureParent != nil && !f.ClosureParent.IsPackageInit() {
		f = f.ClosureParent
	}

	if f != fn {
		// We found a parent.
		ro := b.reassignOracles[f]
		if ro != nil {
			// Hit, via a parent. Before returning, store this ro for the original fn as well.
			b.reassignOracles[fn] = ro
			return ro
		}
	}

	// Miss. We did not find a ReassignOracle for fn or a parent, so lazily create one.
	ro := &ir.ReassignOracle{}
	ro.Init(f)

	// Cache the answer for the original fn.
	b.reassignOracles[fn] = ro
	if f != fn {
		// Cache for the parent as well.
		b.reassignOracles[f] = ro
	}
	return ro
}
