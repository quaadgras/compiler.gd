// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package walk

import (
	"cmd/compile/internal/ir"
)

// wrapEscapeCandidateArgs rewrites each pointer arg of n (a closure
// call OCALLFUNC with a dynamic callee, or an OCALLINTER) whose
// Esc() == ir.EscCandidate so that, at runtime, the arg is either
// passed as a stack pointer (when the callee's escape-mask bit is
// clear) or copied to the heap first (when the bit is set).
//
// Phase C.2 skeleton: the function recognises candidate args and
// returns without rewriting them. Phase D will land the full IR
// transformation once escape analysis starts flagging args as
// EscCandidate. Keeping the dispatch point here so Phase D is a
// focused change — body-only, no new call sites.
//
// See doc/gd/escape-bits.md.
func wrapEscapeCandidateArgs(n *ir.CallExpr, init *ir.Nodes) {
	if n == nil {
		return
	}
	switch n.Op() {
	case ir.OCALLFUNC:
		if ir.StaticCalleeName(n.Fun) != nil {
			// Direct call; escape analysis used the callee's
			// per-param escape tag directly — nothing to wrap.
			return
		}
	case ir.OCALLINTER:
		// Always indirect.
	default:
		return
	}

	// Scan for any candidate arg. When none present, this is the
	// common case and we exit immediately without cost.
	hasCandidate := false
	for _, arg := range n.Args {
		if arg.Esc() == ir.EscCandidate {
			hasCandidate = true
			break
		}
	}
	if !hasCandidate {
		return
	}

	// TODO(gd Phase D): for each candidate arg i, synthesise the IR
	// that loads the mask from either the closure header (offset
	// PtrSize from &closure) or the itab's mask tail (offset
	// abi.ITabEscMaskOff(PtrSize, nmethods, methodIdx)) and wraps the
	// arg in a runtime.maybeEscapeArg call. For now this branch is
	// unreachable because no escape-analysis edge produces
	// EscCandidate yet — Phase D flips that in cmd/compile/internal/
	// escape/call.go tagHole's fn==nil path.
}
