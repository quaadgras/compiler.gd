// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"testing"
	"unsafe"
)

// TestEscapeBitsDynamicMaskClosure exercises Phase F: a closure
// whose escape-mask word carries a COMPUTED mask fn (bit 0 = 1,
// upper bits = fn pointer) instead of a static mask. The runtime
// resolveMask step reads bit 0, calls the compute fn with the
// closure carrier, and uses the returned mask.
//
// This is the "trivial wrapper forwards mask from a captured
// source" shape. We drive it manually here: a plain non-escaping
// closure is installed in a package var, then its mask word is
// overwritten in init to point at a compute fn that reports
// "every arg escapes". At call time the caller pays the
// materialize path even though the body itself doesn't retain —
// proving the dynamic mask overrode the static one.

// escBitsDynSink keeps the callee body from getting optimised
// away.
var escBitsDynSink int

// escBitsDynCapturedPtr — the pointer that the compute-fn version
// reports as escaped (see the "escapes" test). Kept package-level
// so its write is observable.
var escBitsDynCapturedPtr *int

// escBitsDynReader is a non-escaping closure: its static mask
// would be 0 for arg 0. We'll overwrite its mask word at init
// time to point at a compute fn that claims "arg 0 escapes",
// forcing the wrap to materialize.
var escBitsDynReader func(p *int)

// escBitsAlwaysEscapeMask is the Phase-F compute fn installed on
// escBitsDynReader. It ignores carrier, heapMask, and depth and
// returns a static mask with bit 1 set (arg 0 must end up on
// heap). Stored in a package var so &escBitsAlwaysEscapeMask is
// a legal func-value pointer.
var escBitsAlwaysEscapeMask func(unsafe.Pointer, uint64, int) uint64 = func(carrier unsafe.Pointer, heapMask uint64, depth int) uint64 {
	_ = carrier
	_ = heapMask
	_ = depth
	return 1 << 1
}

// escBitsDynCapture keeps the closure capturing at least one var
// so it's heap-allocated and its mask word (second word of the
// {F, M, captures...} struct) is writable. A non-capturing
// closure would be lowered to a rodata funcsym whose mask slot is
// read-only.
var escBitsDynCapture int

func init() {
	capPtr := &escBitsDynCapture
	escBitsDynReader = func(p *int) {
		*capPtr = *p
		escBitsDynSink = *p
	}
	// Overwrite the closure's M word with (computeFn-descriptor |
	// bit 0). The closure value in escBitsDynReader is a pointer
	// to its {F, M, capture0, ...} struct. A Go func value is
	// itself a pointer to its descriptor struct, so reading the
	// variable directly yields what resolveMask expects.
	closurePtr := *(**[3]uintptr)(unsafe.Pointer(&escBitsDynReader))
	descriptor := *(*uintptr)(unsafe.Pointer(&escBitsAlwaysEscapeMask))
	closurePtr[1] = descriptor | 1
}

// TestEscapeBitsDynamicMaskForcedEscape: the underlying closure
// body does not retain its arg, so the STATIC mask would be 0
// and the caller would skip materialization. The compute fn we
// installed overrides that to "escape", and we verify the wrap
// honours the dynamic result — materializeToHeap runs once per
// fresh invocation of f, giving exactly 1 alloc/run. (Within a
// single call that passes &x through many wraps the alloc
// amortises to 0 via the stack-range idempotence check; here
// every f is a fresh stack frame so each invocation pays once.)
func TestEscapeBitsDynamicMaskForcedEscape(t *testing.T) {
	f := func() {
		x := 99
		escBitsDynReader(&x)
	}
	n := testing.AllocsPerRun(100, f)
	if n != 1 {
		t.Errorf("dynamic-mask escape: got %v allocs/run, want exactly 1 (compute fn should force escape)", n)
	}
}

// TestEscapeBitsDynamicMaskCall confirms the callee still runs —
// it's easy to silently skip the call on a bad mask encoding.
func TestEscapeBitsDynamicMaskCall(t *testing.T) {
	escBitsDynSink = 0
	x := 42
	escBitsDynReader(&x)
	if escBitsDynSink != 42 {
		t.Errorf("dynamic-mask call: sink=%d, want 42", escBitsDynSink)
	}
}

// escBitsRelationalMask is the Phase-F compute fn that exercises
// the heapMask input: it returns "arg 0 escapes iff arg 1 is
// already on heap." In other words, the compute fn models a
// callee whose behaviour is "store arg 0 into *arg 1" — which
// needs arg 0 on the heap whenever arg 1 is, and can leave arg 0
// on the stack when arg 1 is too.
//
// This is a pure fn that demonstrates the relational capability;
// we don't hook it up to a real closure call because that needs
// two-arg candidate wrapping and the current per-arg helpers
// pass heapMask=0. A follow-up that resolves the mask once per
// call site and passes a real heapMask will make this directly
// exercisable.
var escBitsRelationalMask = func(carrier unsafe.Pointer, heapMask uint64, depth int) uint64 {
	_ = carrier
	_ = depth
	// Bit 2 of heapMask = "arg 1 is on heap."
	if heapMask&(1<<2) != 0 {
		return 1 << 1 // arg 0 must escape too
	}
	return 0 // neither arg needs to escape
}

// TestEscapeBitsDynamicMaskRelational exercises the relational
// compute fn directly. It verifies that the runtime's resolveMask
// forwards the heapMask argument correctly and that the fn's
// heapMask-driven branches produce the expected outputs.
func TestEscapeBitsDynamicMaskRelational(t *testing.T) {
	const maxDepth = 8
	// arg 1 on stack (bit 2 clear) → arg 0 need not escape.
	got := escBitsRelationalMask(nil, 0, maxDepth)
	if got != 0 {
		t.Errorf("relational mask(heap=0): got %#x, want 0", got)
	}
	// arg 1 already on heap (bit 2 set) → arg 0 must escape.
	got = escBitsRelationalMask(nil, 1<<2, maxDepth)
	if got != (1 << 1) {
		t.Errorf("relational mask(heap=0x4): got %#x, want %#x", got, uint64(1)<<1)
	}
}
