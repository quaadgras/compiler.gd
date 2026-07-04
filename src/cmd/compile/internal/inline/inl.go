// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// The inlining facility makes 2 passes: first CanInline determines which
// functions are suitable for inlining, and for those that are it
// saves a copy of the body. Then InlineCalls walks each function body to
// expand calls to inlinable functions.
//
// The Debug.l flag controls the aggressiveness. Note that main() swaps level 0 and 1,
// making 1 the default and -l disable. Additional levels (beyond -l) may be buggy and
// are not supported.
//      0: disabled
//      1: 80-nodes leaf functions, oneliners, panic, lazy typechecking (default)
//      2: (unassigned)
//      3: (unassigned)
//      4: allow non-leaf functions
//
// At some point this may get another default and become switch-offable with -N.
//
// The -d typcheckinl flag enables early typechecking of all imported bodies,
// which is useful to flush out bugs.
//
// The Debug.m flag enables diagnostic output.  a single -m is useful for verifying
// which calls get inlined or not, more is for debugging, and may go away at any point.

package inline

import (
	"fmt"
	"go/constant"
	"internal/buildcfg"
	"strconv"
	"strings"

	"cmd/compile/internal/base"
	"cmd/compile/internal/inline/inlheur"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/logopt"
	"cmd/compile/internal/pgoir"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
	"cmd/internal/obj"
	"cmd/internal/pgo"
	"cmd/internal/src"
)

// Inlining budget parameters, gathered in one place
const (
	inlineMaxBudget       = 80
	inlineExtraAppendCost = 0
	// default is to inline if there's at most one call. -l=4 overrides this by using 1 instead.
	inlineExtraCallCost  = 57              // 57 was benchmarked to provided most benefit with no bad surprises; see https://github.com/golang/go/issues/19348#issuecomment-439370742
	inlineParamCallCost  = 17              // calling a parameter only costs this much extra (inlining might expose a constant function)
	inlineExtraPanicCost = 1               // do not penalize inlining panics.
	inlineExtraThrowCost = inlineMaxBudget // with current (2018-05/1.11) code, inlining runtime.throw does not help.

	inlineBigFunctionNodes      = 5000                 // Functions with this many nodes are considered "big".
	inlineBigFunctionMaxCost    = 20                   // Max cost of inlinee when inlining into a "big" function.
	inlineClosureCalledOnceCost = 10 * inlineMaxBudget // if a closure is just called once, inline it.
)

// PGO inline tracking maps. Were package-level `var` maps; under
// concurrent host.Run invocations (cmd/go's outer parallelism over
// in-process compile), invocation A's PGOInlinePrologue would
// write candHotEdgeMap while invocation B's inlineCostOK read it,
// triggering "concurrent map read and map write". The maps now
// live on *base.Invocation; helpers below lazy-init them.

func candHotCalleeMapOf(gd *base.Invocation) map[*pgoir.IRNode]struct{} {
	m, _ := gd.InlPgoCandHotCalleeMap.(map[*pgoir.IRNode]struct{})
	if m == nil {
		m = make(map[*pgoir.IRNode]struct{})
		gd.InlPgoCandHotCalleeMap = m
	}
	return m
}

func hasHotCallOf(gd *base.Invocation) map[*ir.Func]struct{} {
	m, _ := gd.InlPgoHasHotCall.(map[*ir.Func]struct{})
	if m == nil {
		m = make(map[*ir.Func]struct{})
		gd.InlPgoHasHotCall = m
	}
	return m
}

func candHotEdgeMapOf(gd *base.Invocation) map[pgoir.CallSiteInfo]struct{} {
	m, _ := gd.InlPgoCandHotEdgeMap.(map[pgoir.CallSiteInfo]struct{})
	if m == nil {
		m = make(map[pgoir.CallSiteInfo]struct{})
		gd.InlPgoCandHotEdgeMap = m
	}
	return m
}

func inlineCDFHotCallSiteThresholdPercentOf(gd *base.Invocation) float64 {
	if gd.InlPgoCDFHotCallSiteThresholdPercent == 0 {
		gd.InlPgoCDFHotCallSiteThresholdPercent = 99
	}
	return gd.InlPgoCDFHotCallSiteThresholdPercent
}

func inlineHotMaxBudgetOf(gd *base.Invocation) int32 {
	if gd.InlPgoHotMaxBudget == 0 {
		gd.InlPgoHotMaxBudget = 2000
	}
	return gd.InlPgoHotMaxBudget
}

func IsPgoHotFunc(gd *base.Invocation, fn *ir.Func, profile *pgoir.Profile) bool {
	if profile == nil {
		return false
	}
	if n, ok := profile.WeightedCG.IRNodes[ir.LinkFuncName(fn)]; ok {
		// Read-only, nil-safe: see HasPgoHotInline. Must not use the
		// lazy-init candHotCalleeMapOf here — this runs in the parallel
		// backend (ssagen.Compile), and writing gd from a worker races.
		m, _ := gd.InlPgoCandHotCalleeMap.(map[*pgoir.IRNode]struct{})
		_, ok := m[n]
		return ok
	}
	return false
}

func HasPgoHotInline(gd *base.Invocation, fn *ir.Func) bool {
	// Read-only, nil-safe. This is called from the parallel backend
	// (ssagen.Compile → pgen.go), so it MUST NOT lazy-init the map via
	// hasHotCallOf: that reads-then-writes gd.InlPgoHasHotCall, and with
	// no PGO profile the field is nil, so every backend worker would race
	// to create it (a data race on the interface field, and concurrent
	// map creation). The map is only ever populated by mkinlcall during
	// the serial inline phase, which completes before the backend starts
	// — so a plain nil-safe read here is correct and race-free (a nil map
	// lookup yields false).
	m, _ := gd.InlPgoHasHotCall.(map[*ir.Func]struct{})
	_, has := m[fn]
	return has
}

// PGOInlinePrologue records the hot callsites from ir-graph.
func PGOInlinePrologue(gd *base.Invocation, p *pgoir.Profile) {
	if gd.Debug.PGOInlineCDFThreshold != "" {
		if s, err := strconv.ParseFloat(gd.Debug.PGOInlineCDFThreshold, 64); err == nil && s >= 0 && s <= 100 {
			gd.InlPgoCDFHotCallSiteThresholdPercent = s
		} else {
			gd.Fatalf("invalid PGOInlineCDFThreshold, must be between 0 and 100")
		}
	}
	var hotCallsites []pgo.NamedCallEdge
	gd.InlPgoHotCallSiteThresholdPercent, hotCallsites = hotNodesFromCDF(gd, p)
	if gd.Debug.PGODebug > 0 {
		gd.Logf("hot-callsite-thres-from-CDF=%v\n", gd.InlPgoHotCallSiteThresholdPercent)
	}

	if x := gd.Debug.PGOInlineBudget; x != 0 {
		gd.InlPgoHotMaxBudget = int32(x)
	}

	candHotCalleeMap := candHotCalleeMapOf(gd)
	candHotEdgeMap := candHotEdgeMapOf(gd)
	for _, n := range hotCallsites {
		// mark inlineable callees from hot edges
		if callee := p.WeightedCG.IRNodes[n.CalleeName]; callee != nil {
			candHotCalleeMap[callee] = struct{}{}
		}
		// mark hot call sites
		if caller := p.WeightedCG.IRNodes[n.CallerName]; caller != nil && caller.AST != nil {
			csi := pgoir.CallSiteInfo{LineOffset: n.CallSiteOffset, Caller: caller.AST}
			candHotEdgeMap[csi] = struct{}{}
		}
	}

	if gd.Debug.PGODebug >= 3 {
		gd.Logf("hot-cg before inline in dot format:")
		p.PrintWeightedCallGraphDOT(gd, gd.InlPgoHotCallSiteThresholdPercent)
	}
}

// hotNodesFromCDF computes an edge weight threshold and the list of hot
// nodes that make up the given percentage of the CDF. The threshold, as
// a percent, is the lower bound of weight for nodes to be considered hot
// (currently only used in debug prints) (in case of equal weights,
// comparing with the threshold may not accurately reflect which nodes are
// considered hot).
func hotNodesFromCDF(gd *base.Invocation, p *pgoir.Profile) (float64, []pgo.NamedCallEdge) {
	cum := int64(0)
	for i, n := range p.NamedEdgeMap.ByWeight {
		w := p.NamedEdgeMap.Weight[n]
		cum += w
		if pgo.WeightInPercentage(cum, p.TotalWeight) > inlineCDFHotCallSiteThresholdPercentOf(gd) {
			// nodes[:i+1] to include the very last node that makes it to go over the threshold.
			// (Say, if the CDF threshold is 50% and one hot node takes 60% of weight, we want to
			// include that node instead of excluding it.)
			return pgo.WeightInPercentage(w, p.TotalWeight), p.NamedEdgeMap.ByWeight[:i+1]
		}
	}
	return 0, p.NamedEdgeMap.ByWeight
}

// CanInlineFuncs computes whether a batch of functions are inlinable.
func CanInlineFuncs(gd *base.Invocation, funcs []*ir.Func, profile *pgoir.Profile) {
	if profile != nil {
		PGOInlinePrologue(gd, profile)
	}

	if gd.Flag.LowerL == 0 {
		return
	}

	ir.VisitFuncsBottomUp(funcs, func(funcs []*ir.Func, recursive bool) {
		for _, fn := range funcs {
			CanInline(gd, fn, profile)
			if inlheur.Enabled(gd) {
				analyzeFuncProps(gd, fn, profile)
			}
		}
	})
}

func simdCreditMultiplier(fn *ir.Func) int32 {
	for _, field := range fn.Type().RecvParamsResults() {
		if field.Type.IsSIMD() {
			return 3
		}
	}
	// Sometimes code uses closures, that do not take simd
	// parameters, to perform repetitive SIMD operations.
	// fn.  These really need to be inlined, or the anticipated
	// awesome SIMD performance will be missed.
	for _, v := range fn.ClosureVars {
		if v.Type().IsSIMD() {
			return 11 // 11 ought to be enough.
		}
	}

	return 1
}

// inlineBudget determines the max budget for function 'fn' prior to
// analyzing the hairiness of the body of 'fn'. We pass in the pgo
// profile if available (which can change the budget), also a
// 'relaxed' flag, which expands the budget slightly to allow for the
// possibility that a call to the function might have its score
// adjusted downwards. If 'verbose' is set, then print a remark where
// we boost the budget due to PGO.
// Note that inlineCostOk has the final say on whether an inline will
// happen; changes here merely make inlines possible.
func inlineBudget(gd *base.Invocation, fn *ir.Func, profile *pgoir.Profile, relaxed bool, verbose bool) int32 {
	// Update the budget for profile-guided inlining.
	budget := int32(inlineMaxBudget)

	budget *= simdCreditMultiplier(fn)

	if IsPgoHotFunc(gd, fn, profile) {
		budget = inlineHotMaxBudgetOf(gd)
		if verbose {
			gd.Logf("hot-node enabled increased budget=%v for func=%v\n", budget, ir.PkgFuncName(fn))
		}
	}
	if relaxed {
		budget += inlheur.BudgetExpansion(gd, inlineMaxBudget)
	}
	if fn.ClosureParent != nil {
		// be very liberal here, if the closure is only called once, the budget is large
		budget = max(budget, inlineClosureCalledOnceCost)
	}

	return budget
}

// CanInline determines whether fn is inlineable.
// If so, CanInline saves copies of fn.Body and fn.Dcl in fn.Inl.
// fn and fn.Body will already have been typechecked.
func CanInline(gd *base.Invocation, fn *ir.Func, profile *pgoir.Profile) {
	if fn.Nname == nil {
		gd.Fatalf("CanInline no nname %+v", fn)
	}

	var reason string // reason, if any, that the function was not inlined
	if gd.Flag.LowerM > 1 || logopt.Enabled() {
		defer func() {
			if reason != "" {
				if gd.Flag.LowerM > 1 {
					gd.Logf("%v: cannot inline %v: %s\n", ir.Line(gd, fn), fn.Nname, reason)
				}
				if logopt.Enabled() {
					logopt.LogOpt(gd, fn.Pos(), "cannotInlineFunction", "inline", ir.FuncName(fn), reason)
				}
			}
		}()
	}

	reason = InlineImpossible(gd, fn)
	if reason != "" {
		return
	}
	if fn.Typecheck() == 0 {
		gd.Fatalf("CanInline on non-typechecked function %v", fn)
	}

	n := fn.Nname
	if n.Func.InlinabilityChecked() {
		return
	}
	defer n.Func.SetInlinabilityChecked(true)

	cc := int32(inlineExtraCallCost)
	if gd.Flag.LowerL == 4 {
		cc = 1 // this appears to yield better performance than 0.
	}

	// Used a "relaxed" inline budget if the new inliner is enabled.
	relaxed := inlheur.Enabled(gd)

	// Compute the inline budget for this func.
	budget := inlineBudget(gd, fn, profile, relaxed, gd.Debug.PGODebug > 0)

	// At this point in the game the function we're looking at may
	// have "stale" autos, vars that still appear in the Dcl list, but
	// which no longer have any uses in the function body (due to
	// elimination by deadcode). We'd like to exclude these dead vars
	// when creating the "Inline.Dcl" field below; to accomplish this,
	// the hairyVisitor below builds up a map of used/referenced
	// locals, and we use this map to produce a pruned Inline.Dcl
	// list. See issue 25459 for more context.

	visitor := hairyVisitor{
		gd:            gd,
		curFunc:       fn,
		debug:         isDebugFn(fn),
		isBigFunc:     IsBigFunc(fn),
		budget:        budget,
		maxBudget:     budget,
		extraCallCost: cc,
		profile:       profile,
	}
	if visitor.tooHairy(fn) {
		reason = visitor.reason
		return
	}

	n.Func.Inl = &ir.Inline{
		Cost:            budget - visitor.budget,
		Dcl:             pruneUnusedAutos(gd, n.Func.Dcl, &visitor),
		HaveDcl:         true,
		CanDelayResults: canDelayResults(fn),
	}
	if gd.Flag.LowerM != 0 || logopt.Enabled() {
		noteInlinableFunc(gd, n, fn, budget-visitor.budget)
	}
}

// noteInlinableFunc issues a message to the user that the specified
// function is inlinable.
func noteInlinableFunc(gd *base.Invocation, n *ir.Name, fn *ir.Func, cost int32) {
	if gd.Flag.LowerM > 1 {
		gd.Logf("%v: can inline %v with cost %d as: %v { %v }\n", ir.Line(gd, fn), n, cost, fn.Type(), fn.Body)
	} else if gd.Flag.LowerM != 0 {
		gd.Logf("%v: can inline %v\n", ir.Line(gd, fn), n)
	}
	// JSON optimization log output.
	if logopt.Enabled() {
		logopt.LogOpt(gd, fn.Pos(), "canInlineFunction", "inline", ir.FuncName(fn), fmt.Sprintf("cost: %d", cost))
	}
}

// InlineImpossible returns a non-empty reason string if fn is impossible to
// inline regardless of cost or contents.
func InlineImpossible(gd *base.Invocation, fn *ir.Func) string {
	var reason string // reason, if any, that the function can not be inlined.
	if fn.Nname == nil {
		reason = "no name"
		return reason
	}

	// If marked "go:noinline", don't inline.
	if fn.Pragma&ir.Noinline != 0 {
		reason = "marked go:noinline"
		return reason
	}

	// If marked "go:norace" and -race compilation, don't inline.
	if gd.Flag.Race && fn.Pragma&ir.Norace != 0 {
		reason = "marked go:norace with -race compilation"
		return reason
	}

	// If marked "go:nocheckptr" and -d checkptr compilation, don't inline.
	if gd.Debug.Checkptr != 0 && fn.Pragma&ir.NoCheckPtr != 0 {
		reason = "marked go:nocheckptr"
		return reason
	}

	// If marked "go:cgo_unsafe_args", don't inline, since the function
	// makes assumptions about its argument frame layout.
	if fn.Pragma&ir.CgoUnsafeArgs != 0 {
		reason = "marked go:cgo_unsafe_args"
		return reason
	}

	// If marked as "go:uintptrkeepalive", don't inline, since the keep
	// alive information is lost during inlining.
	//
	// TODO(prattmic): This is handled on calls during escape analysis,
	// which is after inlining. Move prior to inlining so the keep-alive is
	// maintained after inlining.
	if fn.Pragma&ir.UintptrKeepAlive != 0 {
		reason = "marked as having a keep-alive uintptr argument"
		return reason
	}

	// If marked as "go:uintptrescapes", don't inline, since the escape
	// information is lost during inlining.
	if fn.Pragma&ir.UintptrEscapes != 0 {
		reason = "marked as having an escaping uintptr argument"
		return reason
	}

	// The nowritebarrierrec checker currently works at function
	// granularity, so inlining yeswritebarrierrec functions can confuse it
	// (#22342). As a workaround, disallow inlining them for now.
	if fn.Pragma&ir.Yeswritebarrierrec != 0 {
		reason = "marked go:yeswritebarrierrec"
		return reason
	}

	// If a local function has no fn.Body (is defined outside of Go), cannot inline it.
	// Imported functions don't have fn.Body but might have inline body in fn.Inl.
	if len(fn.Body) == 0 && !typecheck.HaveInlineBody(gd, fn) {
		reason = "no function body"
		return reason
	}

	return ""
}

// canDelayResults reports whether inlined calls to fn can delay
// declaring the result parameter until the "return" statement.
func canDelayResults(fn *ir.Func) bool {
	// We can delay declaring+initializing result parameters if:
	// (1) there's exactly one "return" statement in the inlined function;
	// (2) it's not an empty return statement (#44355); and
	// (3) the result parameters aren't named.

	nreturns := 0
	ir.VisitList(fn.Body, func(n ir.Node) {
		if n, ok := n.(*ir.ReturnStmt); ok {
			nreturns++
			if len(n.Results) == 0 {
				nreturns++ // empty return statement (case 2)
			}
		}
	})

	if nreturns != 1 {
		return false // not exactly one return statement (case 1)
	}

	// temporaries for return values.
	for _, param := range fn.Type().Results() {
		if sym := param.Sym; sym != nil && !sym.IsBlank() {
			return false // found a named result parameter (case 3)
		}
	}

	return true
}

// hairyVisitor visits a function body to determine its inlining
// hairiness and whether or not it can be inlined.
type hairyVisitor struct {
	gd *base.Invocation

	// This is needed to access the current caller in the doNode function.
	curFunc       *ir.Func
	isBigFunc     bool
	debug         bool
	budget        int32
	maxBudget     int32
	reason        string
	extraCallCost int32
	usedLocals    ir.NameSet
	do            func(ir.Node) bool
	profile       *pgoir.Profile
}

func isDebugFn(fn *ir.Func) bool {
	// if n := fn.Nname; n != nil {
	// 	if n.Sym().Name == "Int32x8.Transpose8" && n.Sym().Pkg.Path == "simd/archsimd" {
	// 		gd.Logf("isDebugFn '%s' DOT '%s'\n", n.Sym().Pkg.Path, n.Sym().Name)
	// 		return true
	// 	}
	// }
	return false
}

func (v *hairyVisitor) tooHairy(fn *ir.Func) bool {
	v.do = v.doNode // cache closure
	if ir.DoChildren(fn, v.do) {
		return true
	}
	if v.budget < 0 {
		v.reason = fmt.Sprintf("function too complex: cost %d exceeds budget %d", v.maxBudget-v.budget, v.maxBudget)
		return true
	}
	return false
}

// doNode visits n and its children, updates the state in v, and returns true if
// n makes the current function too hairy for inlining.
func (v *hairyVisitor) doNode(n ir.Node) bool {
	if n == nil {
		return false
	}
	if v.debug {
		v.gd.Logf("%v: doNode %v budget is %d\n", ir.Line(v.gd, n), n.Op(), v.budget)
	}
opSwitch:
	switch n.Op() {
	// Call is okay if inlinable and we have the budget for the body.
	case ir.OCALLFUNC:
		n := n.(*ir.CallExpr)
		var cheap bool
		if n.Fun.Op() == ir.ONAME {
			name := n.Fun.(*ir.Name)
			if name.Class == ir.PFUNC {
				s := name.Sym()
				fn := s.Name
				switch s.Pkg.Path {
				case "internal/abi":
					switch fn {
					case "NoEscape":
						// Special case for internal/abi.NoEscape. It does just type
						// conversions to appease the escape analysis, and doesn't
						// generate code.
						cheap = true
					}
					if strings.HasPrefix(fn, "EscapeNonString[") {
						// internal/abi.EscapeNonString[T] is a compiler intrinsic
						// implemented in the escape analysis phase.
						cheap = true
					}
				case "internal/runtime/sys":
					switch fn {
					case "GetCallerPC", "GetCallerSP":
						// Functions that call GetCallerPC/SP can not be inlined
						// because users expect the PC/SP of the logical caller,
						// but GetCallerPC/SP returns the physical caller.
						v.reason = "call to " + fn
						return true
					}
				case "go.runtime":
					switch fn {
					case "throw":
						// runtime.throw is a "cheap call" like panic in normal code.
						v.budget -= inlineExtraThrowCost
						break opSwitch
					case "panicrangestate":
						cheap = true
					case "deferrangefunc":
						v.reason = "defer call in range func"
						return true
					}
				}
			}
			// Special case for coverage counter updates; although
			// these correspond to real operations, we treat them as
			// zero cost for the moment. This is due to the existence
			// of tests that are sensitive to inlining-- if the
			// insertion of coverage instrumentation happens to tip a
			// given function over the threshold and move it from
			// "inlinable" to "not-inlinable", this can cause changes
			// in allocation behavior, which can then result in test
			// failures (a good example is the TestAllocations in
			// crypto/ed25519).
			if isAtomicCoverageCounterUpdate(n) {
				return false
			}
		}
		if n.Fun.Op() == ir.OMETHEXPR {
			if meth := ir.MethodExprName(n.Fun); meth != nil {
				if fn := meth.Func; fn != nil {
					s := fn.Sym()
					if types.RuntimeSymName(s) == "heapBits.nextArena" {
						// Special case: explicitly allow mid-stack inlining of
						// runtime.heapBits.next even though it calls slow-path
						// runtime.heapBits.nextArena.
						cheap = true
					}
					// Special case: on architectures that can do unaligned loads,
					// explicitly mark encoding/binary methods as cheap,
					// because in practice they are, even though our inlining
					// budgeting system does not see that. See issue 42958.
					if v.gd.Ctxt.Arch.CanMergeLoads && s.Pkg.Path == "encoding/binary" {
						switch s.Name {
						case "littleEndian.Uint64", "littleEndian.Uint32", "littleEndian.Uint16",
							"bigEndian.Uint64", "bigEndian.Uint32", "bigEndian.Uint16",
							"littleEndian.PutUint64", "littleEndian.PutUint32", "littleEndian.PutUint16",
							"bigEndian.PutUint64", "bigEndian.PutUint32", "bigEndian.PutUint16",
							"littleEndian.AppendUint64", "littleEndian.AppendUint32", "littleEndian.AppendUint16",
							"bigEndian.AppendUint64", "bigEndian.AppendUint32", "bigEndian.AppendUint16":
							cheap = true
						}
					}
				}
			}
		}

		// A call to a parameter is optimistically a cheap call, if it's a constant function
		// perhaps it will inline, it also can simplify escape analysis.
		extraCost := v.extraCallCost

		if n.Fun.Op() == ir.ONAME {
			name := n.Fun.(*ir.Name)
			if name.Class == ir.PFUNC {
				// Special case: on architectures that can do unaligned loads,
				// explicitly mark internal/byteorder methods as cheap,
				// because in practice they are, even though our inlining
				// budgeting system does not see that. See issue 42958.
				if v.gd.Ctxt.Arch.CanMergeLoads && name.Sym().Pkg.Path == "internal/byteorder" {
					switch name.Sym().Name {
					case "LEUint64", "LEUint32", "LEUint16",
						"BEUint64", "BEUint32", "BEUint16",
						"LEPutUint64", "LEPutUint32", "LEPutUint16",
						"BEPutUint64", "BEPutUint32", "BEPutUint16",
						"LEAppendUint64", "LEAppendUint32", "LEAppendUint16",
						"BEAppendUint64", "BEAppendUint32", "BEAppendUint16":
						cheap = true
					}
				}
			}
			if name.Class == ir.PPARAM || name.Class == ir.PAUTOHEAP && name.IsClosureVar() {
				extraCost = min(extraCost, inlineParamCallCost)
			}
		}

		if cheap {
			if v.debug {
				if ir.IsIntrinsicCall(v.gd, n) {
					v.gd.Logf("%v: cheap call is also intrinsic, %v\n", ir.Line(v.gd, n), n)
				}
			}
			break // treat like any other node, that is, cost of 1
		}

		if ir.IsIntrinsicCall(v.gd, n) {
			if v.debug {
				v.gd.Logf("%v: intrinsic call, %v\n", ir.Line(v.gd, n), n)
			}
			break // Treat like any other node.
		}

		if callee := inlCallee(v.gd, v.curFunc, n.Fun, v.profile, false); callee != nil && typecheck.HaveInlineBody(v.gd, callee) {
			// Check whether we'd actually inline this call. Set
			// log == false since we aren't actually doing inlining
			// yet.
			if ok, _, _ := canInlineCallExpr(v.gd, v.curFunc, n, callee, v.isBigFunc, false, false); ok {
				// mkinlcall would inline this call [1], so use
				// the cost of the inline body as the cost of
				// the call, as that is what will actually
				// appear in the code.
				//
				// [1] This is almost a perfect match to the
				// mkinlcall logic, except that
				// canInlineCallExpr considers inlining cycles
				// by looking at what has already been inlined.
				// Since we haven't done any inlining yet we
				// will miss those.
				//
				// TODO: in the case of a single-call closure, the inlining budget here is potentially much, much larger.
				//
				v.budget -= callee.Inl.Cost
				break
			}
		}

		if v.debug {
			v.gd.Logf("%v: costly OCALLFUNC %v\n", ir.Line(v.gd, n), n)
		}

		// Call cost for non-leaf inlining.
		v.budget -= extraCost

	case ir.OCALLMETH:
		v.gd.FatalfAt(n.Pos(), "OCALLMETH missed by typecheck")

	// Things that are too hairy, irrespective of the budget
	case ir.OCALL, ir.OCALLINTER:
		// Call cost for non-leaf inlining.
		if v.debug {
			v.gd.Logf("%v: costly OCALL %v\n", ir.Line(v.gd, n), n)
		}
		v.budget -= v.extraCallCost

	case ir.OPANIC:
		n := n.(*ir.UnaryExpr)
		if n.X.Op() == ir.OCONVIFACE && n.X.(*ir.ConvExpr).Implicit() {
			// Hack to keep reflect.flag.mustBe inlinable for TestIntendedInlining.
			// Before CL 284412, these conversions were introduced later in the
			// compiler, so they didn't count against inlining budget.
			v.budget++
		}
		v.budget -= inlineExtraPanicCost

	case ir.ORECOVER:
		// TODO: maybe we could allow inlining of recover() now?
		v.reason = "call to recover"
		return true

	case ir.OCLOSURE:
		if v.gd.Debug.InlFuncsWithClosures == 0 {
			v.reason = "not inlining functions with closures"
			return true
		}

		// TODO(danscales): Maybe make budget proportional to number of closure
		// variables, e.g.:
		//v.budget -= int32(len(n.(*ir.ClosureExpr).Func.ClosureVars) * 3)
		// TODO(austin): However, if we're able to inline this closure into
		// v.curFunc, then we actually pay nothing for the closure captures. We
		// should try to account for that if we're going to account for captures.
		v.budget -= 15

	case ir.OGO, ir.ODEFER, ir.OTAILCALL:
		v.reason = "unhandled op " + n.Op().String()
		return true

	case ir.OAPPEND:
		v.budget -= inlineExtraAppendCost

	case ir.OADDR:
		n := n.(*ir.AddrExpr)
		// Make "&s.f" cost 0 when f's offset is zero.
		if dot, ok := n.X.(*ir.SelectorExpr); ok && (dot.Op() == ir.ODOT || dot.Op() == ir.ODOTPTR) {
			if _, ok := dot.X.(*ir.Name); ok && dot.Selection.Offset == 0 {
				v.budget += 2 // undo ir.OADDR+ir.ODOT/ir.ODOTPTR
			}
		}

	case ir.ODEREF:
		// *(*X)(unsafe.Pointer(&x)) is low-cost
		n := n.(*ir.StarExpr)

		ptr := n.X
		for ptr.Op() == ir.OCONVNOP {
			ptr = ptr.(*ir.ConvExpr).X
		}
		if ptr.Op() == ir.OADDR {
			v.budget += 1 // undo half of default cost of ir.ODEREF+ir.OADDR
		}

	case ir.OCONVNOP:
		// This doesn't produce code, but the children might.
		v.budget++ // undo default cost

	case ir.OFALL, ir.OTYPE:
		// These nodes don't produce code; omit from inlining budget.
		return false

	case ir.OIF:
		n := n.(*ir.IfStmt)
		if ir.IsConst(n.Cond, constant.Bool) {
			// This if and the condition cost nothing.
			if doList(n.Init(), v.do) {
				return true
			}
			if ir.BoolVal(n.Cond) {
				return doList(n.Body, v.do)
			} else {
				return doList(n.Else, v.do)
			}
		}

	case ir.ONAME:
		n := n.(*ir.Name)
		if n.Class == ir.PAUTO {
			v.usedLocals.Add(n)
		}

	case ir.OBLOCK:
		// The only OBLOCK we should see at this point is an empty one.
		// In any event, let the visitList(n.List()) below take care of the statements,
		// and don't charge for the OBLOCK itself. The ++ undoes the -- below.
		v.budget++

	case ir.OMETHVALUE, ir.OSLICELIT:
		v.budget-- // Hack for toolstash -cmp.

	case ir.OMETHEXPR:
		v.budget++ // Hack for toolstash -cmp.

	case ir.OAS2:
		n := n.(*ir.AssignListStmt)

		// Unified IR unconditionally rewrites:
		//
		//	a, b = f()
		//
		// into:
		//
		//	DCL tmp1
		//	DCL tmp2
		//	tmp1, tmp2 = f()
		//	a, b = tmp1, tmp2
		//
		// so that it can insert implicit conversions as necessary. To
		// minimize impact to the existing inlining heuristics (in
		// particular, to avoid breaking the existing inlinability regress
		// tests), we need to compensate for this here.
		//
		// See also identical logic in IsBigFunc.
		if len(n.Rhs) > 0 {
			if init := n.Rhs[0].Init(); len(init) == 1 {
				if _, ok := init[0].(*ir.AssignListStmt); ok {
					// 4 for each value, because each temporary variable now
					// appears 3 times (DCL, LHS, RHS), plus an extra DCL node.
					//
					// 1 for the extra "tmp1, tmp2 = f()" assignment statement.
					v.budget += 4*int32(len(n.Lhs)) + 1
				}
			}
		}

	case ir.OAS:
		// Special case for coverage counter updates and coverage
		// function registrations. Although these correspond to real
		// operations, we treat them as zero cost for the moment. This
		// is primarily due to the existence of tests that are
		// sensitive to inlining-- if the insertion of coverage
		// instrumentation happens to tip a given function over the
		// threshold and move it from "inlinable" to "not-inlinable",
		// this can cause changes in allocation behavior, which can
		// then result in test failures (a good example is the
		// TestAllocations in crypto/ed25519).
		n := n.(*ir.AssignStmt)
		if n.X.Op() == ir.OINDEX && isIndexingCoverageCounter(n.X) {
			return false
		}

	case ir.OSLICE, ir.OSLICEARR, ir.OSLICESTR, ir.OSLICE3, ir.OSLICE3ARR:
		n := n.(*ir.SliceExpr)

		// Ignore superfluous slicing.
		if n.Low != nil && n.Low.Op() == ir.OLITERAL && ir.Int64Val(n.Low) == 0 {
			v.budget++
		}
		if n.High != nil && n.High.Op() == ir.OLEN && n.High.(*ir.UnaryExpr).X == n.X {
			v.budget += 2
		}
	}

	v.budget--

	// When debugging, don't stop early, to get full cost of inlining this function
	if v.budget < 0 && v.gd.Flag.LowerM < 2 && !logopt.Enabled() && !v.debug {
		v.reason = "too expensive"
		return true
	}

	return ir.DoChildren(n, v.do)
}

// IsBigFunc reports whether fn is a "big" function.
//
// Note: The criteria for "big" is heuristic and subject to change.
func IsBigFunc(fn *ir.Func) bool {
	budget := inlineBigFunctionNodes
	return ir.Any(fn, func(n ir.Node) bool {
		// See logic in hairyVisitor.doNode, explaining unified IR's
		// handling of "a, b = f()" assignments.
		if n, ok := n.(*ir.AssignListStmt); ok && n.Op() == ir.OAS2 && len(n.Rhs) > 0 {
			if init := n.Rhs[0].Init(); len(init) == 1 {
				if _, ok := init[0].(*ir.AssignListStmt); ok {
					budget += 4*len(n.Lhs) + 1
				}
			}
		}

		budget--
		return budget <= 0
	})
}

// inlineCallCheck returns whether a call will never be inlineable
// for basic reasons, and whether the call is an intrinisic call.
// The intrinsic result singles out intrinsic calls for debug logging.
func inlineCallCheck(gd *base.Invocation, callerfn *ir.Func, call *ir.CallExpr) (bool, bool) {
	if gd.Flag.LowerL == 0 {
		return false, false
	}
	if call.Op() != ir.OCALLFUNC {
		return false, false
	}
	if call.GoDefer || call.NoInline {
		return false, false
	}

	// Prevent inlining some reflect.Value methods when using checkptr,
	// even when package reflect was compiled without it (#35073).
	if gd.Debug.Checkptr != 0 && call.Fun.Op() == ir.OMETHEXPR {
		if method := ir.MethodExprName(call.Fun); method != nil {
			switch types.ReflectSymName(method.Sym()) {
			case "Value.UnsafeAddr", "Value.Pointer":
				return false, false
			}
		}
	}

	// internal/abi.EscapeNonString[T] is a compiler intrinsic implemented
	// in the escape analysis phase.
	if fn := ir.StaticCalleeName(call.Fun); fn != nil && fn.Sym().Pkg.Path == "internal/abi" &&
		strings.HasPrefix(fn.Sym().Name, "EscapeNonString[") {
		return false, true
	}

	if ir.IsIntrinsicCall(gd, call) {
		return false, true
	}
	return true, false
}

// InlineCallTarget returns the resolved-for-inlining target of a call.
// It does not necessarily guarantee that the target can be inlined, though
// obvious exclusions are applied.
func InlineCallTarget(gd *base.Invocation, callerfn *ir.Func, call *ir.CallExpr, profile *pgoir.Profile) *ir.Func {
	if mightInline, _ := inlineCallCheck(gd, callerfn, call); !mightInline {
		return nil
	}
	return inlCallee(gd, callerfn, call.Fun, profile, true)
}

// TryInlineCall returns an inlined call expression for call, or nil
// if inlining is not possible.
func TryInlineCall(gd *base.Invocation, callerfn *ir.Func, call *ir.CallExpr, bigCaller bool, profile *pgoir.Profile, closureCalledOnce bool) *ir.InlinedCallExpr {
	mightInline, isIntrinsic := inlineCallCheck(gd, callerfn, call)

	// Preserve old logging behavior
	if (mightInline || isIntrinsic) && gd.Flag.LowerM > 3 {
		gd.Logf("%v:call to func %+v\n", ir.Line(gd, call), call.Fun)
	}
	if !mightInline {
		return nil
	}

	if fn := inlCallee(gd, callerfn, call.Fun, profile, false); fn != nil && typecheck.HaveInlineBody(gd, fn) {
		return mkinlcall(gd, callerfn, call, fn, bigCaller, closureCalledOnce)
	}
	return nil
}

// inlCallee takes a function-typed expression and returns the underlying function ONAME
// that it refers to if statically known. Otherwise, it returns nil.
// resolveOnly skips cost-based inlineability checks for closures; the result may not actually be inlineable.
func inlCallee(gd *base.Invocation, caller *ir.Func, fn ir.Node, profile *pgoir.Profile, resolveOnly bool) (res *ir.Func) {
	fn = ir.StaticValue(fn)
	switch fn.Op() {
	case ir.OMETHEXPR:
		fn := fn.(*ir.SelectorExpr)
		n := ir.MethodExprName(fn)
		// Check that receiver type matches fn.X.
		// TODO(mdempsky): Handle implicit dereference
		// of pointer receiver argument?
		if n == nil || !types.Identical(n.Type().Recv().Type, fn.X.Type()) {
			return nil
		}
		return n.Func
	case ir.ONAME:
		fn := fn.(*ir.Name)
		if fn.Class == ir.PFUNC {
			return fn.Func
		}
	case ir.OCLOSURE:
		fn := fn.(*ir.ClosureExpr)
		c := fn.Func
		if len(c.ClosureVars) != 0 && c.ClosureVars[0].Outer.Curfn != caller {
			return nil // inliner doesn't support inlining across closure frames
		}
		if !resolveOnly {
			CanInline(gd, c, profile)
		}
		return c
	}
	return nil
}

// SSADumpInline gives the SSA back end a chance to dump the function
// when producing output for debugging the compiler itself.
var SSADumpInline = func(*ir.Func) {}

// InlineCall allows the inliner implementation to be overridden.
// If it returns nil, the function will not be inlined.
var InlineCall = func(gd *base.Invocation, callerfn *ir.Func, call *ir.CallExpr, fn *ir.Func, inlIndex int) *ir.InlinedCallExpr {
	gd.Fatalf("inline.InlineCall not overridden")
	panic("unreachable")
}

// inlineCostOK returns true if call n from caller to callee is cheap enough to
// inline. bigCaller indicates that caller is a big function.
//
// In addition to the "cost OK" boolean, it also returns
//   - the "max cost" limit used to make the decision (which may differ depending on func size)
//   - the score assigned to this specific callsite
//   - whether the inlined function is "hot" according to PGO.
func inlineCostOK(gd *base.Invocation, n *ir.CallExpr, caller, callee *ir.Func, bigCaller, closureCalledOnce bool) (bool, int32, int32, bool) {
	maxCost := int32(inlineMaxBudget)

	if bigCaller {
		// We use this to restrict inlining into very big functions.
		// See issue 26546 and 17566.
		maxCost = inlineBigFunctionMaxCost
	}

	simdMaxCost := simdCreditMultiplier(callee) * maxCost

	if callee.ClosureParent != nil {
		maxCost *= 2           // favor inlining closures
		if closureCalledOnce { // really favor inlining the one call to this closure
			maxCost = max(maxCost, inlineClosureCalledOnceCost)
		}
	}

	maxCost = max(maxCost, simdMaxCost)

	metric := callee.Inl.Cost
	if inlheur.Enabled(gd) {
		score, ok := inlheur.GetCallSiteScore(gd, caller, n)
		if ok {
			metric = int32(score)
		}
	}

	lineOffset := pgoir.NodeLineOffset(gd, n, caller)
	csi := pgoir.CallSiteInfo{LineOffset: lineOffset, Caller: caller}
	_, hot := candHotEdgeMapOf(gd)[csi]

	if metric <= maxCost {
		// Simple case. Function is already cheap enough.
		return true, 0, metric, hot
	}

	// We'll also allow inlining of hot functions below inlineHotMaxBudget,
	// but only in small functions.

	if !hot {
		// Cold
		return false, maxCost, metric, false
	}

	// Hot

	if bigCaller {
		if gd.Debug.PGODebug > 0 {
			gd.Logf("hot-big check disallows inlining for call %s (cost %d) at %v in big function %s\n", ir.PkgFuncName(callee), callee.Inl.Cost, ir.Line(gd, n), ir.PkgFuncName(caller))
		}
		return false, maxCost, metric, false
	}

	if metric > inlineHotMaxBudgetOf(gd) {
		return false, inlineHotMaxBudgetOf(gd), metric, false
	}

	if !base.PGOHash.MatchPosWithInfoCtxt(gd.Ctxt, n.Pos(), "inline", nil) {
		// De-selected by PGO Hash.
		return false, maxCost, metric, false
	}

	if gd.Debug.PGODebug > 0 {
		gd.Logf("hot-budget check allows inlining for call %s (cost %d) at %v in function %s\n", ir.PkgFuncName(callee), callee.Inl.Cost, ir.Line(gd, n), ir.PkgFuncName(caller))
	}

	return true, 0, metric, hot
}

// parsePos returns all the inlining positions and the innermost position.
func parsePos(gd *base.Invocation, pos src.XPos, posTmp []src.Pos) ([]src.Pos, src.Pos) {
	ctxt := gd.Ctxt
	ctxt.AllPos(pos, func(p src.Pos) {
		posTmp = append(posTmp, p)
	})
	l := len(posTmp) - 1
	return posTmp[:l], posTmp[l]
}

// canInlineCallExpr returns true if the call n from caller to callee
// can be inlined, plus the score computed for the call expr in question,
// and whether the callee is hot according to PGO.
// bigCaller indicates that caller is a big function. log
// indicates that the 'cannot inline' reason should be logged.
//
// Preconditions: CanInline(callee) has already been called.
func canInlineCallExpr(gd *base.Invocation, callerfn *ir.Func, n *ir.CallExpr, callee *ir.Func, bigCaller, closureCalledOnce bool, log bool) (bool, int32, bool) {
	if callee.Inl == nil {
		// callee is never inlinable.
		if log && logopt.Enabled() {
			logopt.LogOpt(gd, n.Pos(), "cannotInlineCall", "inline", ir.FuncName(callerfn),
				fmt.Sprintf("%s cannot be inlined", ir.PkgFuncName(callee)))
		}
		return false, 0, false
	}

	ok, maxCost, callSiteScore, hot := inlineCostOK(gd, n, callerfn, callee, bigCaller, closureCalledOnce)
	if !ok {
		// callee cost too high for this call site.
		if log && logopt.Enabled() {
			logopt.LogOpt(gd, n.Pos(), "cannotInlineCall", "inline", ir.FuncName(callerfn),
				fmt.Sprintf("cost %d of %s exceeds max caller cost %d", callee.Inl.Cost, ir.PkgFuncName(callee), maxCost))
		}
		return false, 0, false
	}

	callees, calleeInner := parsePos(gd, n.Pos(), make([]src.Pos, 0, 10))

	for _, p := range callees {
		if p.Line() == calleeInner.Line() && p.Col() == calleeInner.Col() && p.AbsFilename() == calleeInner.AbsFilename() {
			if log && logopt.Enabled() {
				logopt.LogOpt(gd, n.Pos(), "cannotInlineCall", "inline", fmt.Sprintf("recursive call to %s", ir.FuncName(callerfn)))
			}
			return false, 0, false
		}
	}

	if gd.Flag.Cfg.Instrumenting && types.IsNoInstrumentPkg(callee.Sym().Pkg) {
		// Runtime package must not be instrumented.
		// Instrument skips runtime package. However, some runtime code can be
		// inlined into other packages and instrumented there. To avoid this,
		// we disable inlining of runtime functions when instrumenting.
		// The example that we observed is inlining of LockOSThread,
		// which lead to false race reports on m contents.
		if log && logopt.Enabled() {
			logopt.LogOpt(gd, n.Pos(), "cannotInlineCall", "inline", ir.FuncName(callerfn),
				fmt.Sprintf("call to runtime function %s in instrumented build", ir.PkgFuncName(callee)))
		}
		return false, 0, false
	}

	if gd.Flag.Race && types.IsNoRacePkg(callee.Sym().Pkg) {
		if log && logopt.Enabled() {
			logopt.LogOpt(gd, n.Pos(), "cannotInlineCall", "inline", ir.FuncName(callerfn),
				fmt.Sprintf(`call to into "no-race" package function %s in race build`, ir.PkgFuncName(callee)))
		}
		return false, 0, false
	}

	if gd.Debug.Checkptr != 0 && types.IsRuntimePkg(callee.Sym().Pkg) {
		// We don't instrument runtime packages for checkptr (see base/flag.go).
		if log && logopt.Enabled() {
			logopt.LogOpt(gd, n.Pos(), "cannotInlineCall", "inline", ir.FuncName(callerfn),
				fmt.Sprintf(`call to into runtime package function %s in -d=checkptr build`, ir.PkgFuncName(callee)))
		}
		return false, 0, false
	}

	// Check if we've already inlined this function at this particular
	// call site, in order to stop inlining when we reach the beginning
	// of a recursion cycle again. We don't inline immediately recursive
	// functions, but allow inlining if there is a recursion cycle of
	// many functions. Most likely, the inlining will stop before we
	// even hit the beginning of the cycle again, but this catches the
	// unusual case.
	parent := gd.Ctxt.PosTable.Pos(n.Pos()).Base().InliningIndex()
	sym := callee.Linksym()
	for inlIndex := parent; inlIndex >= 0; inlIndex = gd.Ctxt.InlTree.Parent(inlIndex) {
		if gd.Ctxt.InlTree.InlinedFunction(inlIndex) == sym {
			if log {
				if gd.Flag.LowerM > 1 {
					gd.Logf("%v: cannot inline %v into %v: repeated recursive cycle\n", ir.Line(gd, n), callee, ir.FuncName(callerfn))
				}
				if logopt.Enabled() {
					logopt.LogOpt(gd, n.Pos(), "cannotInlineCall", "inline", ir.FuncName(callerfn),
						fmt.Sprintf("repeated recursive cycle to %s", ir.PkgFuncName(callee)))
				}
			}
			return false, 0, false
		}
	}

	return true, callSiteScore, hot
}

// mkinlcall returns an OINLCALL node that can replace OCALLFUNC n, or
// nil if it cannot be inlined. callerfn is the function that contains
// n, and fn is the function being called.
//
// The result of mkinlcall MUST be assigned back to n, e.g.
//
//	n.Left = mkinlcall(n.Left, fn, isddd)
func mkinlcall(gd *base.Invocation, callerfn *ir.Func, n *ir.CallExpr, fn *ir.Func, bigCaller, closureCalledOnce bool) *ir.InlinedCallExpr {
	ok, score, hot := canInlineCallExpr(gd, callerfn, n, fn, bigCaller, closureCalledOnce, true)
	if !ok {
		return nil
	}
	if hot {
		hasHotCallOf(gd)[callerfn] = struct{}{}
	}
	typecheck.AssertFixedCall(gd, n)

	parent := gd.Ctxt.PosTable.Pos(n.Pos()).Base().InliningIndex()
	sym := fn.Linksym()
	inlIndex := gd.Ctxt.InlTree.Add(parent, n.Pos(), sym, ir.FuncName(fn))

	closureInitLSym := func(n *ir.CallExpr, fn *ir.Func) {
		// The linker needs FuncInfo metadata for all inlined
		// functions. This is typically handled by gc.enqueueFunc
		// calling ir.InitLSym for all function declarations in
		// typecheck.Target.Decls (ir.UseClosure adds all closures to
		// Decls).
		//
		// However, closures in Decls are ignored, and are
		// instead enqueued when walk of the calling function
		// discovers them.
		//
		// This presents a problem for direct calls to closures.
		// Inlining will replace the entire closure definition with its
		// body, which hides the closure from walk and thus suppresses
		// symbol creation.
		//
		// Explicitly create a symbol early in this edge case to ensure
		// we keep this metadata.
		//
		// TODO: Refactor to keep a reference so this can all be done
		// by enqueueFunc.

		if n.Op() != ir.OCALLFUNC {
			// Not a standard call.
			return
		}

		var nf = n.Fun
		// Skips ir.OCONVNOPs, see issue #73716.
		for nf.Op() == ir.OCONVNOP {
			nf = nf.(*ir.ConvExpr).X
		}
		if nf.Op() != ir.OCLOSURE {
			// Not a direct closure call or one with type conversion.
			return
		}

		clo := nf.(*ir.ClosureExpr)
		if !clo.Func.IsClosure() {
			// enqueueFunc will handle non closures anyways.
			return
		}

		ir.InitLSym(gd, fn, true)
	}

	closureInitLSym(n, fn)

	if gd.Flag.GenDwarfInl > 0 {
		if !sym.WasInlined() {
			gd.Ctxt.DwFixups.SetPrecursorFunc(sym, fn)
			sym.Set(obj.AttrWasInlined, true)
		}
	}

	if gd.Flag.LowerM != 0 {
		if buildcfg.Experiment.NewInliner {
			gd.Logf("%v: inlining call to %v with score %d\n",
				ir.Line(gd, n), fn, score)
		} else {
			gd.Logf("%v: inlining call to %v\n", ir.Line(gd, n), fn)
		}
	}
	if gd.Flag.LowerM > 2 {
		gd.Logf("%v: Before inlining: %+v\n", ir.Line(gd, n), n)
	}

	res := InlineCall(gd, callerfn, n, fn, inlIndex)

	if res == nil {
		gd.FatalfAt(n.Pos(), "inlining call to %v failed", fn)
	}

	if gd.Flag.LowerM > 2 {
		gd.Logf("%v: After inlining %+v\n\n", ir.Line(gd, res), res)
	}

	if inlheur.Enabled(gd) {
		inlheur.UpdateCallsiteTable(gd, callerfn, n, res)
	}

	return res
}

// CalleeEffects appends any side effects from evaluating callee to init.
func CalleeEffects(gd *base.Invocation, init *ir.Nodes, callee ir.Node) {
	for {
		init.Append(ir.TakeInit(callee)...)

		switch callee.Op() {
		case ir.ONAME, ir.OCLOSURE, ir.OMETHEXPR:
			return // done

		case ir.OCONVNOP:
			conv := callee.(*ir.ConvExpr)
			callee = conv.X

		case ir.OINLCALL:
			ic := callee.(*ir.InlinedCallExpr)
			init.Append(ic.Body.Take()...)
			callee = ic.SingleResult()

		default:
			gd.FatalfAt(callee.Pos(), "unexpected callee expression: %v", callee)
		}
	}
}

func pruneUnusedAutos(gd *base.Invocation, ll []*ir.Name, vis *hairyVisitor) []*ir.Name {
	s := make([]*ir.Name, 0, len(ll))
	for _, n := range ll {
		if n.Class == ir.PAUTO {
			if !vis.usedLocals.Has(n) {
				// TODO(mdempsky): Simplify code after confident that this
				// never happens anymore.
				gd.FatalfAt(n.Pos(), "unused auto: %v", n)
				continue
			}
		}
		s = append(s, n)
	}
	return s
}

func doList(list []ir.Node, do func(ir.Node) bool) bool {
	for _, x := range list {
		if x != nil {
			if do(x) {
				return true
			}
		}
	}
	return false
}

// isIndexingCoverageCounter returns true if the specified node 'n' is indexing
// into a coverage counter array.
func isIndexingCoverageCounter(n ir.Node) bool {
	if n.Op() != ir.OINDEX {
		return false
	}
	ixn := n.(*ir.IndexExpr)
	if ixn.X.Op() != ir.ONAME || !ixn.X.Type().IsArray() {
		return false
	}
	nn := ixn.X.(*ir.Name)
	// CoverageAuxVar implies either a coverage counter or a package
	// ID; since the cover tool never emits code to index into ID vars
	// this is effectively testing whether nn is a coverage counter.
	return nn.CoverageAuxVar()
}

// isAtomicCoverageCounterUpdate examines the specified node to
// determine whether it represents a call to sync/atomic.AddUint32 to
// increment a coverage counter.
func isAtomicCoverageCounterUpdate(cn *ir.CallExpr) bool {
	if cn.Fun.Op() != ir.ONAME {
		return false
	}
	name := cn.Fun.(*ir.Name)
	if name.Class != ir.PFUNC {
		return false
	}
	fn := name.Sym().Name
	if name.Sym().Pkg.Path != "sync/atomic" ||
		(fn != "AddUint32" && fn != "StoreUint32") {
		return false
	}
	if len(cn.Args) != 2 || cn.Args[0].Op() != ir.OADDR {
		return false
	}
	adn := cn.Args[0].(*ir.AddrExpr)
	v := isIndexingCoverageCounter(adn.X)
	return v
}

func PostProcessCallSites(gd *base.Invocation, profile *pgoir.Profile) {
	if gd.Debug.DumpInlCallSiteScores != 0 {
		budgetCallback := func(fn *ir.Func, prof *pgoir.Profile) (int32, bool) {
			v := inlineBudget(gd, fn, prof, false, false)
			return v, v == inlineHotMaxBudgetOf(gd)
		}
		inlheur.DumpInlCallSiteScores(gd, profile, budgetCallback)
	}
}

func analyzeFuncProps(gd *base.Invocation, fn *ir.Func, p *pgoir.Profile) {
	canInline := func(fn *ir.Func) { CanInline(gd, fn, p) }
	budgetForFunc := func(fn *ir.Func) int32 {
		return inlineBudget(gd, fn, p, true, false)
	}
	inlheur.AnalyzeFunc(gd, fn, canInline, budgetForFunc, inlineMaxBudget)
}
