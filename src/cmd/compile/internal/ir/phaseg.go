// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ir

// OutBufUnusedNote is the escape note of a function's outBuf param
// (gd Phase G) when the function never places anything in that
// buffer: escape's encoding of "flows nowhere". A function that does
// use it notes it as mutated instead (escape.finish).
const OutBufUnusedNote = "esc:"

// CalleeOutBufUnused reports whether call is a static call of a
// function that never places anything in the buffer passed for its
// k-th pointer result (0-based outBuf ordinal), so the caller needn't
// make one: the result is never in the caller's frame. It is false
// when that isn't known yet (a function of the batch escape analysis
// is working on).
func CalleeOutBufUnused(call *CallExpr, k int) bool {
	if call == nil || call.Fun == nil {
		return false
	}
	fn := StaticCalleeName(call.Fun)
	if fn == nil || fn.Type() == nil {
		return false
	}
	i := 0
	for _, p := range fn.Type().Params() {
		if !p.IsOutBufParam() {
			continue
		}
		if i == k {
			return p.Note == OutBufUnusedNote
		}
		i++
	}
	return false
}
