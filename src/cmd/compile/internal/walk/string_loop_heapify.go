// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package walk

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
	"cmd/internal/src"
	"fmt"
	"os"
)

// autoHeapifyForLoop hoists the SSO inline-vs-heap dispatch out of
// for-loop bodies that byte-index a non-escaping string. The intended
// transformation is:
//
//	s = unsafe.String(unsafe.StringData(s), len(s))
//
// at the loop's init list, which (a) for heap-rep s reconstructs the
// same heap-rep header (no-op at runtime, but the compiler now sees
// an OpStringMake with a Const64 zero-tag word2 — the existing
// StringWord2(StringMake)→Const64 rewrite chain folds the per-access
// dispatch out at every in-loop indexing site), and (b) for inline-rep
// s, OUNSAFESTRINGDATA spills the inline payload to a stack autotmp
// once and OUNSAFESTRING constructs a heap-rep header pointing at it.
//
// Status (2026-05-02): WIP. The pass mechanism works end-to-end —
// the analysis identifies candidates, generates the assignment,
// walks the synthesized IR through walk's OUNSAFESTRING / OUNSAFESTRINGDATA
// lowering. Disabled-by-default, gated on GOAUTOHEAPIFY=1.
//
// Open issue: when the pass fires on a real candidate, SSA construction
// fails with "value <name> incorrectly live at entry" (phi.go:486).
// The synthesized `s = unsafe.String(...)` reassignment introduces
// forward references at SSA construction that don't resolve cleanly
// at the function entry block, even when wrapped in a BlockStmt and
// when an intermediate temp separates the read of s from the write.
// Reproducible against internal/cpu.processOptions where indexByte's
// inlined `s` parameter triggers it.
//
// The fix is likely one of: (a) introduce a NEW name shadowing the
// original throughout the loop body, requiring a use-rewrite pass;
// (b) emit the assignment via a direct OCALLFUNC to a runtime helper
// rather than the OUNSAFESTRING / OUNSAFESTRINGDATA pair, which
// might avoid whatever extra control flow phi insertion is choking
// on; (c) move this transformation into noder/typecheck so the IR
// is clean before SSA construction sees it.
//
// Saved as scaffolding for a dedicated follow-up. The internal/abi
// primitives (Heapify, StringBytes, StringIsInline, StringInlineWords,
// StringHeapBytes) are the runtime targets the working version will
// emit calls to.
//
// Safety: the inline-rep case aliases a stack autotmp in the current
// function's frame. The pass is correct iff no byte-pointer derived
// from s outlives the function — i.e., s does not escape. Escape
// analysis has run before walk; we gate on n.Esc()==EscNone.

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

// heapifyEnabled gates the pass. Off by default; enable with
// GOAUTOHEAPIFY=1. Currently DOES NOT WORK — see the doc comment at
// the top of the file for the open SSA-liveness issue.
func heapifyEnabled(gd *base.Invocation) bool {
	return os.Getenv("GOAUTOHEAPIFY") == "1"
}

// autoHeapifyForLoop returns a replacement node — either the original
// ForStmt unchanged, or a BlockStmt wrapping it with Heapify
// assignments prepended.
func autoHeapifyForLoop(gd *base.Invocation, n *ir.ForStmt) ir.Node {
	hlog("ENTER walkFor in %v at %v", funcName(gd), n.Pos())
	if !heapifyEnabled(gd) {
		hlog("  disabled")
		return n
	}
	cands := findHeapifyCandidates(gd, n)
	hlog("  %d candidates: %v", len(cands), cands)
	if len(cands) == 0 {
		return n
	}

	pos := n.Pos()
	stmts := make([]ir.Node, 0, 2*len(cands)+1)
	for _, name := range cands {
		stmts = append(stmts, mkHeapifyAssigns(gd, pos, name)...)
	}
	typecheck.Stmts(gd, stmts)
	// Walk the synthesized stmts so OUNSAFESTRING / OUNSAFESTRINGDATA
	// get lowered before SSA-gen sees them.
	walkStmtList(gd, stmts)
	stmts = append(stmts, n)
	hlog("  rewrote: wrapped loop with %d prefix stmts", len(stmts)-1)
	return ir.NewBlockStmt(gd, pos, stmts)
}

func funcName(gd *base.Invocation) string {
	fn := ir.CurFunc(gd)
	if fn == nil {
		return "<nil>"
	}
	return fn.Sym().Name
}

// findHeapifyCandidates returns the *ir.Name values used in n's body
// that are safe to Heapify. A name qualifies iff:
//   - it's a string-typed PAUTO or PPARAM
//   - escape analysis says it doesn't escape (n.Esc()==EscNone)
//   - it's not address-taken outside this pass (a hint of indirection)
//   - it's used at least once for byte-indexing in the loop body
//   - all uses fall into the recognised set (OINDEX, OLEN, ONAME-as-arg);
//     unrecognised uses bail.
func findHeapifyCandidates(gd *base.Invocation, n *ir.ForStmt) []*ir.Name {
	type useStats struct {
		index int  // OINDEX (s[i]) — a "benefit" use
		other int  // OLEN — neutral
		bail  bool // unrecognised → don't Heapify this name
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
					get(nm).other++
				}
			case ir.OSLICESTR:
				sl := x.(*ir.SliceExpr)
				if nm, ok := stringNameForLoop(sl.X); ok {
					// Self-resliceing (s = s[N:]) preserves heap-rep
					// invariant; resliceing into a different name might
					// alias-escape. Conservatively bail to keep this
					// first iteration simple.
					get(nm).bail = true
				}
			}
		})
	}

	if n.Cond != nil {
		classify(n.Cond)
	}
	for _, b := range n.Body {
		classify(b)
	}
	if n.Post != nil {
		classify(n.Post)
	}

	// Filter out names declared INSIDE the loop body. Their position
	// falls within the loop's body span; my init-prepend would try to
	// Heapify them before they exist, which crashes SSA construction.
	loopStart, loopEnd := loopBodySpan(n)
	out := make([]*ir.Name, 0, len(stats))
	for nm, s := range stats {
		if s.bail || s.index == 0 {
			continue
		}
		if isInsideRange(nm.Pos(), loopStart, loopEnd) {
			continue
		}
		out = append(out, nm)
	}
	return out
}

// loopBodySpan returns the [start, end) source-position range of n's
// body. Both are 0 (NoXPos) if the body is empty.
func loopBodySpan(n *ir.ForStmt) (start, end src.XPos) {
	if len(n.Body) == 0 {
		return src.NoXPos, src.NoXPos
	}
	start = n.Body[0].Pos()
	end = n.Body[len(n.Body)-1].Pos()
	return
}

// isInsideRange reports whether p falls in [start, end] inclusive.
// Conservative: returns true on any uncertainty so we don't Heapify
// loop-locals.
func isInsideRange(p, start, end src.XPos) bool {
	if !start.IsKnown() || !end.IsKnown() || !p.IsKnown() {
		return false
	}
	return p.SameFileAndLine(start) || p.SameFileAndLine(end) || (p.After(start) && !p.After(end))
}

// stringNameForLoop returns nm if x is an ONAME of a hoist-eligible
// string variable. Used inside findHeapifyCandidates which has the
// loop's position info to filter out loop-local declarations.
func stringNameForLoop(x ir.Node) (*ir.Name, bool) {
	nm, ok := x.(*ir.Name)
	if !ok {
		return nil, false
	}
	if nm.Type() == nil || !nm.Type().IsString() {
		return nil, false
	}
	if nm.Class != ir.PAUTO {
		return nil, false
	}
	if nm.Esc() != ir.EscNone {
		return nil, false
	}
	if nm.Addrtaken() {
		return nil, false
	}
	return nm, true
}

// mkHeapifyAssign synthesizes the IR for
//
//	name = unsafe.String(unsafe.StringData(name), len(name))
//
// using the existing OUNSAFESTRINGDATA, OLEN, and OUNSAFESTRING ops.
// Types are set explicitly because typecheck has already run by the
// time walk synthesizes new IR.
func mkHeapifyAssigns(gd *base.Invocation, pos src.XPos, name *ir.Name) []ir.Node {
	// Synthesize:
	//   __heapify_s := unsafe.String(unsafe.StringData(s), len(s))
	//   s = __heapify_s
	//
	// The intermediate temp keeps SSA's parameter-live-at-entry
	// tracking happy: a direct `s = unsafe.String(unsafe.StringData(s), len(s))`
	// reads s in the RHS and writes s on the LHS in the same stmt,
	// which confused phi insertion when s is a PPARAM (resulting in
	// "value s incorrectly live at entry").
	tmp := typecheck.TempAt(gd, pos, ir.CurFunc(gd), types.Types[types.TSTRING])
	sd := ir.NewUnaryExpr(gd, pos, ir.OUNSAFESTRINGDATA, name)
	ln := ir.NewUnaryExpr(gd, pos, ir.OLEN, name)
	us := ir.NewBinaryExpr(gd, pos, ir.OUNSAFESTRING, sd, ln)
	asTmp := ir.NewAssignStmt(gd, pos, tmp, us)
	asName := ir.NewAssignStmt(gd, pos, name, tmp)
	return []ir.Node{asTmp, asName}
}

// silence unused import errors when the file is being iterated on.
var _ = types.Types
var _ = src.NoXPos
