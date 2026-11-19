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
const EscMaskArgs = 21

// After the bits that say which arguments the function keeps, an escape
// mask has, for the same arguments, the bits that say whether another
// goroutine may reach what the function keeps (doc/gd/sharing.md):
//
//   - bit EscMaskShared+k: what argument k points to may be reached by
//     another goroutine, or go where the caller cannot follow: an object
//     that is local must not be passed.
//   - bit EscMaskViaRecv+k, if the first is clear: argument k is stored
//     in what the receiver points to, and is where that object is.
//
// With both clear, what the function keeps of the argument stays with
// the goroutine that calls it. A mask with all bits set (abi.ITabEscAll)
// says that everything is shared.
const (
	EscMaskShared  = 1 + EscMaskArgs
	EscMaskViaRecv = 1 + 2*EscMaskArgs
)

// The byte that follows the eight of the leaks in an escape note, if the
// function says where the parameter goes for sharing: without it, what
// the function keeps is shared.
const (
	EscNoteShareByte = 8
	EscNoteStays     = 0x81 // what is kept is not reached by another goroutine by itself
	EscNoteViaRecv   = 0x02 // and it is stored through the receiver
)

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
			switch fl := escNoteShare(f.Note); {
			case fl&EscNoteStays != EscNoteStays:
				mask |= 1<<uint(EscMaskShared+i) | 1<<uint(EscMaskViaRecv+i)
			case fl&EscNoteViaRecv != 0:
				mask |= 1 << uint(EscMaskViaRecv+i)
			}
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
	for i := escNoteResult0; i < len(b) && i < EscNoteShareByte; i++ {
		if b[i] != 0 {
			return true
		}
	}
	return false
}

// escNoteShare returns the byte of an escape note that says where the
// parameter goes for sharing, or zero.
func escNoteShare(note string) byte {
	if !strings.HasPrefix(note, "esc:") {
		return 0
	}
	if b := note[len("esc:"):]; len(b) > EscNoteShareByte {
		return b[EscNoteShareByte]
	}
	return 0
}
