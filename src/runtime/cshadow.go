// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/runtime/atomic"
	"unsafe"
)

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

// cShadowClear makes the n bytes at a memory that the shadow says
// nothing of.
//
//go:nosplit
func cShadowClear(a, n uintptr) {
	memclrNoHeapPointers(unsafe.Pointer(cShadowOf(a)), n/8)
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
	if part := (base + n) & 7; part != 0 {
		// The word that the variable ends in, with as many
		// bytes of it as are the variable's.
		if p := cShadowOf(hi); *p == 0 {
			*p = uint8(part) << cWordPartShift
		}
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

// cShadowScan is the most words of a variable that cShadowStackVar
// looks at to find where it begins, and where it ends.
const cShadowScan = 1024

// cShadowStackVar returns the variable of a goroutine's stack that the
// address a is in, as the shadow has it: where it begins and its size.
// ok is false if the shadow does not say: the word at a is not whole in
// a variable that a function wrote when it was entered, or is far from
// where the variable begins or ends. The stack walk is what says then
// (checkptrVar).
//
//go:nosplit
func cShadowStackVar(a uintptr) (base, size uintptr, ok bool) {
	if !cShadowOn {
		return
	}
	base = a &^ 7
	for n := 0; ; n++ {
		f := *cShadowOf(base)
		if f&cWordIn == 0 || n == cShadowScan {
			return 0, 0, false
		}
		if f&cWordStart != 0 {
			break
		}
		base -= 8
	}
	for e, n := a&^7+8, 0; n < cShadowScan; e, n = e+8, n+1 {
		f := *cShadowOf(e)
		if f&cWordIn == 0 {
			// The word after the variable, or the one that it
			// ends in.
			return base, e - base + uintptr(f&cWordPart)>>cWordPartShift, true
		}
		if f&cWordStart != 0 {
			return base, e - base, true
		}
	}
	return 0, 0, false
}

// cShadowClaimScalars claims for no pointer the words that the size
// bytes at a are over, as cClaimScalars does, if the shadow says that
// each of them is whole in an object, may be stored in, and is claimed
// so or by nothing, and reports whether it did: the first conversion of
// what malloc gave to integers, which is most of the conversions that
// the compiled code leaves to the runtime. The words' bytes are in one
// uint64 of the shadow, which is swapped for one that has the claims
// if nothing changed it meanwhile. Not while the collector is marking
// or the runtime is only asking what a conversion would do,
// and not for a pointer that has a bit that no address has: those are
// checkptrConvC1's.
//
//go:nosplit
func cShadowClaimScalars(a, size uintptr) bool {
	if a>>47 != 0 || a < minLegalPointer || writeBarrier.enabled || size == 0 || getg().m.cprobe {
		return false
	}
	p := uintptr(unsafe.Pointer(cShadowOf(a)))
	words := (a&7 + size + 7) / 8
	if p&7+words > 8 {
		return false
	}
	shift := p & 7 * 8
	mask := uint64(1)<<(8*words) - 1
	if words == 8 {
		mask = ^uint64(0)
	}
	g := (*uint64)(unsafe.Pointer(p &^ 7))
	old := atomic.Load64(g)
	if old>>shift&(cClassLows*(cWordIn|cWordRO|cUntyped))&mask != cClassLows*cWordIn&mask {
		return false
	}
	return atomic.Cas64(g, old, old|cClassLows*cScalar&mask<<shift)
}
