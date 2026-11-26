// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import "unsafe"

// The shadow of C's memory (doc/gd/cc.md).
//
// Where the system lets a program have a part of its address space that
// it never uses (cShadowOn), the bytes that say what the words of C's
// memory are (cspan.go: the class of a word, whether it is whole in its
// object, whether it is the first of one) are not in what a span keeps
// but at an address that is the word's own, shifted: the shadow, a byte
// for every word that a program can have. Compiled code then asks what a
// word is with one load, without finding first what it is a word of
// (c23Header). The byte of a word that is in no object of C's, and in no
// variable that the shadow was told of, is zero: nothing of the shadow
// is memory until it is written.
const (
	cShadowBase  = 0x1000_0000_0000
	cShadowBytes = 1 << 44
)

// cShadowOf returns the shadow byte of the word at a.
//
//go:nosplit
func cShadowOf(a uintptr) *uint8 {
	return (*uint8)(unsafe.Pointer(uintptr(cShadowBase) + a>>3))
}

// The variables of a goroutine's stack are in the shadow too: a function
// writes the bytes of those of its frame that a pointer can be to when
// it is entered (cmd/compile/internal/ssagen's cShadowFrame), and
// nothing when it returns, since no pointer is left to a frame that
// returned and the next function there writes its own. A stack that is
// copied takes its bytes with it (copystack), and one that is freed
// leaves none (stackfree).

// cShadowVarMax is the size of the largest variable of the program's
// data that the shadow is told of: its bytes are an eighth of that.
const cShadowVarMax = 64 << 20

// cShadowVar writes the shadow of the variable of the program's data
// at base, of n bytes and of type t, or of bytes if t is nil, the first
// time that the variable is looked up (dataVarLookup): the words that
// are whole in it with what they hold, the first as the first of an
// object. ro says that nothing may be stored there.
//
//go:nosplit
func cShadowVar(base, n uintptr, t *_type, ro bool) {
	lo, hi := alignUp(base, 8), alignDown(base+n, 8)
	if lo >= hi || n > cShadowVarMax || *cShadowOf(lo) != 0 {
		return
	}
	var ptrs uintptr // the words that may hold pointers
	if t != nil && t.Pointers() {
		ptrs = base + t.PtrBytes
	}
	for a := hi - 8; ; a -= 8 {
		v := uint8(cWordIn | cScalar)
		if a < ptrs {
			v = cWordIn | cTyped
		}
		if ro {
			v |= cWordRO
		}
		if a == lo {
			// (Last: what says that the rest is written.)
			*cShadowOf(a) = v | cWordStart
			break
		}
		*cShadowOf(a) = v
	}
}
