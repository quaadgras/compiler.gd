// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package escape

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/types"
	"math"
	"strings"
)

// A leaks represents a set of assignment flows from a parameter to
// the heap, mutator, callee, or to any of its function's (first
// numEscResults) result parameters.
type leaks [8]uint8

const (
	leakHeap = iota
	leakMutator
	leakCallee
	leakResult0
)

const numEscResults = len(leaks{}) - leakResult0

// Heap returns the minimum deref count of any assignment flow from l
// to the heap. If no such flows exist, Heap returns -1.
func (l leaks) Heap() int { return l.get(leakHeap) }

// Mutator returns the minimum deref count of any assignment flow from
// l to the pointer operand of an indirect assignment statement. If no
// such flows exist, Mutator returns -1.
func (l leaks) Mutator() int { return l.get(leakMutator) }

// Callee returns the minimum deref count of any assignment flow from
// l to the callee operand of call expression. If no such flows exist,
// Callee returns -1.
func (l leaks) Callee() int { return l.get(leakCallee) }

// Result returns the minimum deref count of any assignment flow from
// l to its function's i'th result parameter. If no such flows exist,
// Result returns -1.
func (l leaks) Result(i int) int { return l.get(leakResult0 + i) }

// AddHeap adds an assignment flow from l to the heap.
func (l *leaks) AddHeap(derefs int) { l.add(leakHeap, derefs) }

// AddMutator adds a flow from l to the mutator (i.e., a pointer
// operand of an indirect assignment statement).
func (l *leaks) AddMutator(derefs int) { l.add(leakMutator, derefs) }

// AddCallee adds an assignment flow from l to the callee operand of a
// call expression.
func (l *leaks) AddCallee(derefs int) { l.add(leakCallee, derefs) }

// AddResult adds an assignment flow from l to its function's i'th
// result parameter.
func (l *leaks) AddResult(i, derefs int) { l.add(leakResult0+i, derefs) }

func (l leaks) get(i int) int { return int(l[i]) - 1 }

func (l *leaks) add(i, derefs int) {
	if old := l.get(i); old < 0 || derefs < old {
		l.set(i, derefs)
	}
}

func (l *leaks) set(i, derefs int) {
	v := derefs + 1
	if v < 0 {
		base.Fatalf("invalid derefs count: %v", derefs)
	}
	if v > math.MaxUint8 {
		v = math.MaxUint8
	}

	l[i] = uint8(v)
}

// Optimize removes result flow paths that are equal in length or
// longer than the shortest heap flow path.
func (l *leaks) Optimize() {
	// If we have a path to the heap, then there's no use in
	// keeping equal or longer paths elsewhere.
	if x := l.Heap(); x >= 0 {
		for i := 1; i < len(*l); i++ {
			if l.get(i) >= x {
				l.set(i, -1)
			}
		}
	}
}

var leakTagCache = map[leaks]string{}

// Encode converts l into a binary string for export data.
func (l leaks) Encode() string {
	if l.Heap() == 0 {
		// Space optimization: empty string encodes more
		// efficiently in export data.
		return ""
	}
	if s, ok := leakTagCache[l]; ok {
		return s
	}

	n := len(l)
	for n > 0 && l[n-1] == 0 {
		n--
	}
	s := "esc:" + string(l[:n])
	leakTagCache[l] = s
	return s
}

// parseLeaks parses a binary string representing a leaks.
func parseLeaks(s string) leaks {
	var l leaks
	if !strings.HasPrefix(s, "esc:") {
		l.AddHeap(0)
		return l
	}
	copy(l[:], s[4:])
	return l
}

func ParseLeaks(s string) leaks {
	return parseLeaks(s)
}

// ResultAliasesParam reports whether any param (or receiver) of
// the function signature sig could flow to result k via the
// escape tags installed on sig's fields. Used by gd Phase G's
// call-site rewriter: when any input aliases result k, a
// caller-supplied stack buffer for that result would be unsafe
// because the callee may return an existing heap pointer (the
// input) rather than a fresh allocation targeted at the buffer.
//
// Conservatively returns true when the tag is missing or unknown
// — parseLeaks returns an all-heap leak for any Note that doesn't
// carry the "esc:" prefix (body-less functions, or params that
// escape analysis hadn't reached). "Unknown" means "might alias";
// the rewriter declines to stack-buffer under that uncertainty.
func ResultAliasesParam(sig *types.Type, k int) bool {
	if sig == nil || sig.Kind() != types.TFUNC {
		return false
	}
	if k < 0 || k >= numEscResults {
		// Beyond the tag's capacity — be conservative.
		return true
	}
	// aliasOrUnknown: the tag asserts a result-k leak, OR we can't
	// definitively rule out aliasing. Anything that COULD carry a
	// pointer into the callee (pointer, slice, map, chan, iface,
	// func, string, struct-with-pointers) is potentially capable
	// of flowing to a pointer-typed result; if we don't have an
	// explicit "esc:" tag that clears that input, we bail out.
	//
	// Scalar-only params (int, float, bool, etc.) cannot alias
	// any pointer-typed result regardless of tag state.
	aliasOrUnknown := func(f *types.Field) bool {
		if f == nil {
			return false
		}
		if f.Type != nil && !f.Type.HasPointers() {
			return false
		}
		note := f.Note
		if !strings.HasPrefix(note, "esc:") {
			// No tag — assume it might reach result k.
			return true
		}
		esc := parseLeaks(note)
		return esc.Result(k) >= 0
	}
	if aliasOrUnknown(sig.Recv()) {
		return true
	}
	for _, f := range sig.Params() {
		if f == nil {
			continue
		}
		// Skip synthesised outBuf params — by construction they
		// don't alias results; including them in the check would
		// produce a false positive whenever outBuf's default tag
		// is missing.
		if f.IsOutBufParam() {
			continue
		}
		if aliasOrUnknown(f) {
			return true
		}
	}
	return false
}

// Any reports whether the value flows anywhere at all.
func (l leaks) Any() bool {
	// TODO: do mutator/callee matter?
	if l.Heap() >= 0 || l.Mutator() >= 0 || l.Callee() >= 0 {
		return true
	}
	for i := range numEscResults {
		if l.Result(i) >= 0 {
			return true
		}
	}
	return false
}
