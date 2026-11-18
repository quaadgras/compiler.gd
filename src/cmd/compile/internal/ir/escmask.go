// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ir

import (
	"strings"

	"cmd/compile/internal/types"
)

// EscMaskArgs is the number of arguments an escape mask (gd
// escape-bits, doc/gd/escape-bits.md) has a bit for: argument k's is
// bit k+1, bit 0 being reserved. An argument past them is never an
// escape candidate (escape's dynamicArgHole).
const EscMaskArgs = 63

// The bytes of a parameter's escape note after "esc:" (escape's leaks
// encoding): the flow to the heap, to a mutation, to a callee, and to
// each result, each zero if there is none. Trailing zeroes are
// omitted.
const (
	escNoteHeap    = 0
	escNoteResult0 = 3
)

// EscNoteResult0 is the index, after "esc:", of an escape note's
// first result byte, for escape to check its encoding against.
const EscNoteResult0 = escNoteResult0

// EscFuncTagged is the least Func.Esc of a function that escape
// analysis is done with: its parameters have their notes and it has
// its EscMask. (escape's escFuncTagged.)
const EscFuncTagged = 3

// EscMaskOf returns the escape mask of a function with the given
// parameters (its receiver first, if a caller passes one), from their
// escape notes: bit k+1 is set if the function may keep what argument
// k points to beyond the call. That is so when it flows to the heap,
// and when it flows to a result: the wrap at a dynamic call site
// doesn't know what the caller does with the results. A parameter
// with no note (of a function without one: assembly, or not analysed)
// is kept. Flows to mutations and callees only use the argument
// during the call.
//
// Every mask of a function, in its function values, closures, itabs
// and method tables, in the package that defines it and those that
// import it, comes from here, so that they agree.
func EscMaskOf(params []*types.Field) uint64 {
	var mask uint64
	for i, f := range params {
		if i >= EscMaskArgs {
			break
		}
		if escNoteKeeps(f.Note) {
			mask |= 1 << uint(i+1)
		}
	}
	return mask
}

func escNoteKeeps(note string) bool {
	if !strings.HasPrefix(note, "esc:") {
		return true
	}
	b := note[len("esc:"):]
	if len(b) > escNoteHeap && b[escNoteHeap] != 0 {
		return true
	}
	for i := escNoteResult0; i < len(b); i++ {
		if b[i] != 0 {
			return true
		}
	}
	return false
}
