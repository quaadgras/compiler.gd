// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package loopheapify hoists the SSO inline-vs-heap dispatch out of
// for-loop bodies that byte-index a string. For each eligible string
// candidate s used in a for loop's body, the pass emits:
//
//	__heap := unsafe.String(unsafe.StringData(s), len(s))
//
// before the loop and rewrites every OINDEX(s, …) and OLEN(s) inside
// the loop body to use __heap instead. The original s is left
// untouched — no reassignment, no post-loop restore — so escape
// analysis on s is unperturbed and any non-rewritten uses of s (call
// args, returns, slices) keep their original semantics.
//
// (a) For heap-rep s, unsafe.String reconstructs the same heap-rep
// header (no-op at runtime, but the compiler now sees an OpStringMake
// with a Const64 zero-tag word2 — the existing
// StringWord2(StringMake)→Const64 rewrite chain folds the per-access
// dispatch out at every in-loop indexing site).
// (b) For inline-rep s, OUNSAFESTRINGDATA spills the inline payload
// once and OUNSAFESTRING constructs a heap-rep header pointing at
// it. Whether the spill lands on the stack or the heap is decided
// by escape analysis: __heap is only ever used in OINDEX/OLEN reads
// (those are the only sites the rewrite produces), so its byte-
// pointer doesn't flow to any escape sink, and the spill stays on
// the stack — no heap allocation introduced by the optimization.
//
// Pipeline placement: this pass runs BEFORE escape analysis. The
// synthesized OUNSAFESTRINGDATA participates in the escape solver's
// flow graph, so escape sees the byte-pointer flowing into __heap
// and into in-loop reads only — never to a return, send, or
// goroutine — and tags the spill loc EscNone. Walk then routes
// through the cheap OSPTR / stack-autotmp path with no runtime
// stringDataHeap call.
//
// Bail criteria for the candidate s in the loop body:
//   - Any LHS occurrence (OAS / OASOP / OAS2*) — the rewrite would
//     skip those reads, but the underlying value of s changes and
//     __heap's bytes can become stale; rather than refresh __heap
//     mid-loop, bail.
//   - OSLICESTR(s, …) — slicing into a different *ir.Name introduces
//     an alias we don't track. Conservative bail.
//
// Names declared inside the loop body (or at the loop header) are
// not heapified — declaredStrictlyBefore filters them out so the
// __heap definition site can dominate every read.
package loopheapify

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
	"cmd/internal/src"
	"fmt"
	"os"
	"sort"
	"strings"
)

// heapifyDebug reports the pass log file path; logging is disabled
// when empty. Set via the GOHEAPIFYDEBUG environment variable when
// debugging the pass. Production leaves it unset.
var heapifyDebug = os.Getenv("GOHEAPIFYDEBUG")

// hlog appends a debug line to heapifyDebug; no-op when unset.
func hlog(format string, args ...any) {
	if heapifyDebug == "" {
		return
	}
	f, err := os.OpenFile(heapifyDebug, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	fmt.Fprintf(f, format+"\n", args...)
	f.Close()
}

// enabled reports whether the pass should run for the current
// invocation. On by default; disable with GOAUTOHEAPIFY=0. For
// bisection, set GOHEAPIFY_PKGS to a comma-separated list of package
// path prefixes; only matching packages get heapified.
func enabled(gd *base.Invocation) bool {
	if os.Getenv("GOAUTOHEAPIFY") == "0" {
		return false
	}
	if pkgs := os.Getenv("GOHEAPIFY_PKGS"); pkgs != "" {
		for _, p := range strings.Split(pkgs, ",") {
			if p != "" && strings.HasPrefix(gd.Ctxt.Pkgpath, p) {
				return true
			}
		}
		return false
	}
	return true
}

// Funcs applies the loop-heapify pass to fns. Runs before escape
// analysis so the synthesized OUNSAFESTRINGDATA participates in the
// escape solver's flow graph.
func Funcs(gd *base.Invocation, fns []*ir.Func) {
	if !enabled(gd) {
		return
	}
	// Skip the runtime package entirely — write-barrier checks are
	// transitive (Nowritebarrierrec propagates through callees), and
	// our synthesized unsafe.String emits a panicunsafestringlen call
	// chain that eventually reaches code with a write barrier. The
	// runtime is also a special-case enough environment that blanket
	// skipping is the right move; the optimisation targets user code
	// byte-scan loops anyway.
	if gd.Ctxt.Pkgpath == "runtime" || strings.HasPrefix(gd.Ctxt.Pkgpath, "internal/runtime/") {
		return
	}
	for _, fn := range fns {
		if fn == nil {
			continue
		}
		if fn.Pragma&(ir.Nowritebarrier|ir.Nowritebarrierrec) != 0 {
			continue
		}
		processFunc(gd, fn)
	}
}

// processFunc walks fn's body and rewrites every eligible OFOR.
func processFunc(gd *base.Invocation, fn *ir.Func) {
	ir.WithFunc(gd, fn, func() {
		rewriteStmtList(gd, fn, fn.Body)
	})
}

// rewriteStmtList rewrites each statement in stmts in place. For
// OFOR statements that match the heapify pattern, replaces the slot
// with a BlockStmt wrapping the for. Recurses into nested control
// structures so nested for loops also get a chance.
func rewriteStmtList(gd *base.Invocation, fn *ir.Func, stmts []ir.Node) {
	for i, s := range stmts {
		stmts[i] = rewriteStmt(gd, fn, s)
	}
}

func rewriteStmt(gd *base.Invocation, fn *ir.Func, n ir.Node) ir.Node {
	if n == nil {
		return nil
	}
	switch n.Op() {
	case ir.OFOR:
		// Heapify only the outermost loop in a nest. Each __heap
		// definition is emitted just before its loop; if a nested for
		// also heapified, its __heap = unsafe.String(...) would run
		// per-iteration of the outer loop — and with escape analysis
		// possibly forcing heap on the spill (loc lives inside a loop),
		// that becomes a per-iteration allocation. Stopping at the
		// outermost gives one __heap definition per loop nest.
		fs := n.(*ir.ForStmt)
		return tryHeapifyForLoop(gd, fn, fs)
	case ir.ORANGE:
		rs := n.(*ir.RangeStmt)
		rewriteStmtList(gd, fn, rs.Body)
	case ir.OIF:
		is := n.(*ir.IfStmt)
		rewriteStmtList(gd, fn, is.Body)
		rewriteStmtList(gd, fn, is.Else)
	case ir.OBLOCK:
		bs := n.(*ir.BlockStmt)
		rewriteStmtList(gd, fn, bs.List)
	case ir.OSWITCH:
		ss := n.(*ir.SwitchStmt)
		for _, cas := range ss.Cases {
			rewriteStmtList(gd, fn, cas.Body)
		}
	case ir.OSELECT:
		ss := n.(*ir.SelectStmt)
		for _, cas := range ss.Cases {
			rewriteStmtList(gd, fn, cas.Body)
		}
	}
	return n
}

// tryHeapifyForLoop applies the rewrite to fs if the loop has a
// candidate, otherwise returns fs unchanged.
func tryHeapifyForLoop(gd *base.Invocation, fn *ir.Func, fs *ir.ForStmt) ir.Node {
	hlog("ENTER for in %v at %v", fn.Sym().Name, fs.Pos())
	// Skip labeled loops — wrapping in a BlockStmt would attach the
	// label to the block rather than the for, breaking
	// `continue Label` / `break Label` semantics from inner loops.
	if fs.Label != nil {
		hlog("  skipped: labeled loop")
		return fs
	}
	cands := findCandidates(fs)
	hlog("  %d candidates: %v", len(cands), cands)
	if len(cands) == 0 {
		return fs
	}

	// For each candidate, allocate a fresh __heap local and rewrite
	// every OINDEX(cand, …) and OLEN(cand) in the body to use it.
	pos := fs.Pos()
	pre := make([]ir.Node, 0, len(cands))
	subst := make(map[*ir.Name]*ir.Name, len(cands))
	for _, name := range cands {
		heap := typecheck.TempAt(gd, pos, fn, types.Types[types.TSTRING])
		subst[name] = heap
		pre = append(pre, mkHeapAssign(gd, pos, heap, name))
	}
	rewriteReads(fs, subst)
	typecheck.Stmts(gd, pre)
	stmts := make([]ir.Node, 0, len(pre)+1)
	stmts = append(stmts, pre...)
	stmts = append(stmts, fs)
	hlog("  rewrote: hoisted %d candidate(s)", len(cands))
	return ir.NewBlockStmt(gd, pos, stmts)
}

// findCandidates returns the *ir.Name values used in fs's body that
// are safe to heapify.
func findCandidates(fs *ir.ForStmt) []*ir.Name {
	type useStats struct {
		index int  // OINDEX / OLEN — a "benefit" use
		bail  bool // unrecognised → don't heapify this name
	}
	stats := map[*ir.Name]*useStats{}
	get := func(nm *ir.Name) *useStats {
		s, ok := stats[nm]
		if !ok {
			s = &useStats{}
			stats[nm] = s
		}
		return s
	}

	bailIfNamed := func(node ir.Node) {
		if nm, ok := node.(*ir.Name); ok {
			if _, ok := stringNameForLoop(nm); ok {
				get(nm).bail = true
			}
		}
	}

	classify := func(node ir.Node) {
		ir.Visit(node, func(x ir.Node) {
			switch x.Op() {
			case ir.OINDEX:
				ix := x.(*ir.IndexExpr)
				if nm, ok := stringNameForLoop(ix.X); ok {
					get(nm).index++
				}
			case ir.OLEN:
				un := x.(*ir.UnaryExpr)
				if nm, ok := stringNameForLoop(un.X); ok {
					get(nm).index++
				}
			case ir.OSLICESTR:
				sl := x.(*ir.SliceExpr)
				if nm, ok := stringNameForLoop(sl.X); ok {
					// Self-resliceing produces a different *ir.Name we
					// don't track. Conservative bail.
					get(nm).bail = true
				}
			case ir.OAS:
				as := x.(*ir.AssignStmt)
				bailIfNamed(as.X)
			case ir.OASOP:
				as := x.(*ir.AssignOpStmt)
				bailIfNamed(as.X)
			case ir.OAS2, ir.OAS2FUNC, ir.OAS2RECV, ir.OAS2DOTTYPE, ir.OAS2MAPR:
				as := x.(*ir.AssignListStmt)
				for _, lhs := range as.Lhs {
					bailIfNamed(lhs)
				}
			}
		})
	}

	if fs.Cond != nil {
		classify(fs.Cond)
	}
	for _, b := range fs.Body {
		classify(b)
	}
	if fs.Post != nil {
		classify(fs.Post)
	}

	candNames := map[*ir.Name]bool{}
	for nm, s := range stats {
		if s.bail || s.index == 0 {
			continue
		}
		if !declaredStrictlyBefore(nm, fs) {
			continue
		}
		candNames[nm] = true
	}
	out := make([]*ir.Name, 0, len(candNames))
	for nm := range candNames {
		out = append(out, nm)
	}
	// Sort for determinism: candNames is iterated as a Go map, so its
	// range order is randomized. Caller (tryHeapifyForLoop) creates a
	// fresh __heap autotmp per candidate via typecheck.TempAt, which
	// uses an auto-incrementing suffix — so the order of `out` decides
	// which candidate gets which suffix, and any non-deterministic
	// order leaks into the function's symbol table and machine code.
	// Reproducible builds require a stable order.
	sort.Slice(out, func(i, j int) bool {
		return out[i].Sym().Name < out[j].Sym().Name
	})
	return out
}

// declaredStrictlyBefore reports whether nm's declaration position
// strictly precedes loop's position. Conservative: any uncertainty
// (unknown positions, same-line declarations) returns false so we
// don't heapify a name whose scope might include or follow the loop.
func declaredStrictlyBefore(nm *ir.Name, loop *ir.ForStmt) bool {
	np := nm.Pos()
	lp := loop.Pos()
	if !np.IsKnown() || !lp.IsKnown() {
		return false
	}
	return np.Before(lp) && !np.SameFileAndLine(lp)
}

// stringNameForLoop returns nm if x is an *ir.Name of a heapify-eligible
// string variable.
func stringNameForLoop(x ir.Node) (*ir.Name, bool) {
	nm, ok := x.(*ir.Name)
	if !ok {
		return nil, false
	}
	if nm.Type() == nil || nm.Type() != types.Types[types.TSTRING] {
		return nil, false
	}
	if nm.Class != ir.PAUTO && nm.Class != ir.PPARAM {
		return nil, false
	}
	if nm.Addrtaken() {
		return nil, false
	}
	if nm.InlFormal() || nm.InlLocal() {
		return nil, false
	}
	return nm, true
}

// mkHeapAssign synthesizes:
//
//	heap = unsafe.String(unsafe.StringData(name), len(name))
//
// The OUNSAFESTRINGDATA is left without an explicit Esc setting —
// escape analysis (which runs after this pass) decides whether the
// spill goes to the heap or to a stack autotmp based on whether the
// byte-pointer flows to an escape sink. Since heap is only used in
// OINDEX/OLEN reads inside the loop, escape will tag the spill
// EscNone in the typical case.
func mkHeapAssign(gd *base.Invocation, pos src.XPos, heap, name *ir.Name) ir.Node {
	sd := ir.NewUnaryExpr(gd, pos, ir.OUNSAFESTRINGDATA, name)
	sd.SetEsc(ir.EscNone)
	ln := ir.NewUnaryExpr(gd, pos, ir.OLEN, name)
	us := ir.NewBinaryExpr(gd, pos, ir.OUNSAFESTRING, sd, ln)
	us.SetEsc(ir.EscNone)
	return ir.NewAssignStmt(gd, pos, heap, us)
}

// rewriteReads walks fs's body and rewrites every OINDEX(name, …)
// and OLEN(name) where name is a key in subst to use the
// corresponding value. Other uses of name are left alone.
func rewriteReads(fs *ir.ForStmt, subst map[*ir.Name]*ir.Name) {
	swap := func(x ir.Node) ir.Node {
		if nm, ok := x.(*ir.Name); ok {
			if heap, ok := subst[nm]; ok {
				return heap
			}
		}
		return x
	}
	var rewrite func(ir.Node)
	rewrite = func(n ir.Node) {
		if n == nil {
			return
		}
		ir.EditChildren(n, func(x ir.Node) ir.Node {
			switch x.Op() {
			case ir.OINDEX:
				ix := x.(*ir.IndexExpr)
				ix.X = swap(ix.X)
				rewrite(ix.X)
				rewrite(ix.Index)
				return ix
			case ir.OLEN:
				un := x.(*ir.UnaryExpr)
				un.X = swap(un.X)
				rewrite(un.X)
				return un
			}
			rewrite(x)
			return x
		})
	}
	if fs.Cond != nil {
		rewrite(fs.Cond)
	}
	for _, b := range fs.Body {
		rewrite(b)
	}
	if fs.Post != nil {
		rewrite(fs.Post)
	}
}
