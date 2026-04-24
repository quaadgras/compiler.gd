// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"runtime"
	"testing"
	"unsafe"
)

func TestMaterializeToHeap_PreservesBytes(t *testing.T) {
	type box struct {
		A int64
		B float64
		C [3]byte
	}
	src := box{A: 0xDEADBEEF, B: 3.14159, C: [3]byte{'a', 'b', 'c'}}

	dstPtr := runtime.MaterializeToHeapTyped(unsafe.Pointer(&src), box{})
	if dstPtr == nil {
		t.Fatal("materialize returned nil")
	}
	dst := (*box)(dstPtr)
	if *dst != src {
		t.Errorf("copy mismatch: got %+v, want %+v", *dst, src)
	}
}

func TestMaterializeToHeap_DistinctFromSrc(t *testing.T) {
	// Mutating the returned heap copy must not affect the stack src,
	// and vice versa — that's the whole point of materialise.
	type box struct{ X int }
	src := box{X: 1}

	dstPtr := runtime.MaterializeToHeapTyped(unsafe.Pointer(&src), box{})
	dst := (*box)(dstPtr)

	dst.X = 999
	if src.X != 1 {
		t.Errorf("src mutated after materialize: src.X = %d, want 1", src.X)
	}
	src.X = -1
	if dst.X != 999 {
		t.Errorf("dst mutated by src write: dst.X = %d, want 999", dst.X)
	}
}

func TestMaterializeToHeap_PointerFieldsScanned(t *testing.T) {
	// The copy must carry a live reference that the GC can trace,
	// otherwise the pointee could be collected even while the copy
	// holds it. Force-GC mid-test; if the pointee survives and is
	// still readable, write-barrier integration is working.
	type boxed struct {
		P *[32]byte
	}
	buf := [32]byte{7, 7, 7, 7, 7, 7}
	src := boxed{P: &buf}

	dstPtr := runtime.MaterializeToHeapTyped(unsafe.Pointer(&src), boxed{})
	dst := (*boxed)(dstPtr)

	// Drop the only other references to buf that would keep it alive.
	// The test var buf is still on stack (and on stack roots) so it
	// won't be collected, but the write barrier path is still
	// exercised because typedmemmove stored dst.P via typedmemmove.
	src.P = nil
	_ = src

	runtime.GC()
	runtime.GC()

	if dst.P[0] != 7 || dst.P[5] != 7 {
		t.Errorf("pointee not preserved through GC: dst.P[0]=%d dst.P[5]=%d",
			dst.P[0], dst.P[5])
	}
}

func TestMaybeEscapeArg_BitClearReturnsSrc(t *testing.T) {
	// mask=0 → no bit can be set → every call returns src unchanged
	// regardless of argIdx. No heap alloc. This is the "callee
	// doesn't escape" happy path the whole optimization chases.
	type box struct{ A, B int }
	src := box{A: 9, B: 17}
	for _, argIdx := range []int{0, 1, 2, 5, 7} {
		got := runtime.MaybeEscapeArgTyped(0, argIdx, unsafe.Pointer(&src), box{})
		if got != unsafe.Pointer(&src) {
			t.Errorf("argIdx=%d mask=0: got %v, want &src=%v", argIdx, got, &src)
		}
	}
	allocs := testing.AllocsPerRun(100, func() {
		_ = runtime.MaybeEscapeArgTyped(0, 0, unsafe.Pointer(&src), box{})
	})
	if allocs != 0 {
		t.Errorf("mask=0: got %v allocs/run, want 0", allocs)
	}
}

func TestMaybeEscapeArg_BitSetMaterializes(t *testing.T) {
	// Bit N (=argIdx+1) set → should materialize on heap.
	type box struct{ X int }
	src := box{X: 42}
	typeTag := box{}

	// argIdx=0 → bit 1. mask=0b10 targets it.
	dstPtr := runtime.MaybeEscapeArgTyped(0b10, 0, unsafe.Pointer(&src), typeTag)
	if dstPtr == unsafe.Pointer(&src) {
		t.Fatal("bit set but helper returned original src — no materialization happened")
	}
	dst := (*box)(dstPtr)
	if dst.X != 42 {
		t.Errorf("copy content wrong: got %d, want 42", dst.X)
	}

	// argIdx=2 → bit 3. mask=0b1000 targets it; mask=0b100 does not.
	if got := runtime.MaybeEscapeArgTyped(0b100, 2, unsafe.Pointer(&src), typeTag); got != unsafe.Pointer(&src) {
		t.Errorf("argIdx=2 mask=0b100: expected src passthrough")
	}
	if got := runtime.MaybeEscapeArgTyped(0b1000, 2, unsafe.Pointer(&src), typeTag); got == unsafe.Pointer(&src) {
		t.Errorf("argIdx=2 mask=0b1000: expected materialize, got src")
	}
}

func TestMaybeEscapeArg_Bit0Ignored(t *testing.T) {
	// Bit 0 is reserved as the dynamic-mask-fn discriminator. If the
	// caller hands us a mask word with bit 0 set and everything else
	// clear, every argIdx should still pass through — argIdx+1 >= 1.
	type box struct{ X int }
	src := box{X: 99}
	got := runtime.MaybeEscapeArgTyped(1, 0, unsafe.Pointer(&src), box{})
	if got != unsafe.Pointer(&src) {
		t.Errorf("bit 0 only: expected src passthrough, got heap copy")
	}
}

func TestMaterializeToHeap_AllocsOnce(t *testing.T) {
	// The helper is allowed exactly one heap allocation per call.
	// A future "small-type shortcut" could regress this, so lock it.
	type small struct{ A, B int }
	src := small{A: 1, B: 2}

	f := func() {
		_ = runtime.MaterializeToHeapTyped(unsafe.Pointer(&src), small{})
	}
	allocs := testing.AllocsPerRun(100, f)
	if allocs != 1 {
		t.Errorf("got %v allocs/run, want exactly 1", allocs)
	}
}

// --- Phase F: dynamic-mask resolution ---
//
// The tests below exercise resolveMask, the Phase-F helper that
// decides whether a raw mask word is itself a static mask or a
// compute-fn pointer. They cover the happy path (static
// passthrough, straight-line dynamic resolution), the
// bit-0-clamp invariant, the depth-limit cycle fallback, and
// the heapMask argument's passthrough to the compute fn.

// dynMaskFn is the external-test-visible alias of the runtime's
// internal computeMaskFn signature.
type dynMaskFn func(carrier unsafe.Pointer, heapMask uint64, depth int) uint64

// encodeMaskFn takes a dynMaskFn variable address and returns
// its descriptor pointer ORed with bit 0 — the wire form
// ResolveMask expects in a raw mask word.
func encodeMaskFn(fn *dynMaskFn) uint64 {
	descriptor := *(*uintptr)(unsafe.Pointer(fn))
	return uint64(descriptor) | 1
}

func TestResolveMask_StaticPassthrough(t *testing.T) {
	raw := uint64(0b1010)
	got := runtime.ResolveMask(raw, nil, 0, runtime.MaxComputeMaskDepth)
	if got != raw {
		t.Errorf("static passthrough: got %#x, want %#x", got, raw)
	}
}

func TestResolveMask_Bit0ClearedOnOutput(t *testing.T) {
	// A compute fn returning bit 0 set gets it stripped before
	// the caller sees the mask — bit 0 is reserved exclusively
	// as the rawMask discriminator.
	var fn dynMaskFn = func(unsafe.Pointer, uint64, int) uint64 {
		return 0b111
	}
	raw := encodeMaskFn(&fn)
	got := runtime.ResolveMask(raw, nil, 0, runtime.MaxComputeMaskDepth)
	if got != 0b110 {
		t.Errorf("bit-0 clamp: got %#x, want %#x", got, uint64(0b110))
	}
}

func TestResolveMask_CycleTerminates(t *testing.T) {
	// a → b → a. Without the depth cap this would stack-overflow;
	// with the cap resolveMask returns the conservative mask.
	var a, b dynMaskFn
	a = func(carrier unsafe.Pointer, heapMask uint64, depth int) uint64 {
		return runtime.ResolveMask(encodeMaskFn(&b), carrier, heapMask, depth)
	}
	b = func(carrier unsafe.Pointer, heapMask uint64, depth int) uint64 {
		return runtime.ResolveMask(encodeMaskFn(&a), carrier, heapMask, depth)
	}
	got := runtime.ResolveMask(encodeMaskFn(&a), nil, 0, runtime.MaxComputeMaskDepth)
	if got != runtime.ConservativeAllEscapeMask {
		t.Errorf("cycle: got %#x, want %#x", got, uint64(runtime.ConservativeAllEscapeMask))
	}
}

func TestResolveMask_SelfCycleTerminates(t *testing.T) {
	// Single-fn cycle: fn always resolves itself.
	var self dynMaskFn
	self = func(carrier unsafe.Pointer, heapMask uint64, depth int) uint64 {
		return runtime.ResolveMask(encodeMaskFn(&self), carrier, heapMask, depth)
	}
	got := runtime.ResolveMask(encodeMaskFn(&self), nil, 0, runtime.MaxComputeMaskDepth)
	if got != runtime.ConservativeAllEscapeMask {
		t.Errorf("self-cycle: got %#x, want %#x",
			got, uint64(runtime.ConservativeAllEscapeMask))
	}
}

func TestResolveMask_ShallowChainResolves(t *testing.T) {
	// a → b → c → static. Budget is more than 3; resolution
	// reaches the static leaf and returns its mask.
	const leafMask uint64 = 0b1110
	var a, b, c dynMaskFn
	c = func(unsafe.Pointer, uint64, int) uint64 { return leafMask }
	b = func(carrier unsafe.Pointer, heapMask uint64, depth int) uint64 {
		return runtime.ResolveMask(encodeMaskFn(&c), carrier, heapMask, depth)
	}
	a = func(carrier unsafe.Pointer, heapMask uint64, depth int) uint64 {
		return runtime.ResolveMask(encodeMaskFn(&b), carrier, heapMask, depth)
	}
	got := runtime.ResolveMask(encodeMaskFn(&a), nil, 0, runtime.MaxComputeMaskDepth)
	if got != leafMask {
		t.Errorf("3-hop chain: got %#x, want %#x", got, leafMask)
	}
}

func TestResolveMask_HeapMaskForwarded(t *testing.T) {
	// heapMask reaches the compute fn unchanged.
	const sentinel uint64 = 0xFFFFFFFFFFFFFFFE // bit 0 clear
	var got uint64
	var fn dynMaskFn = func(_ unsafe.Pointer, heapMask uint64, _ int) uint64 {
		got = heapMask
		return heapMask
	}
	resolved := runtime.ResolveMask(encodeMaskFn(&fn), nil, sentinel, runtime.MaxComputeMaskDepth)
	if got != sentinel {
		t.Errorf("compute fn saw heapMask %#x, want %#x", got, sentinel)
	}
	if resolved != sentinel {
		t.Errorf("resolved %#x, want %#x", resolved, sentinel)
	}
}

// --- maybeInPlace (Phase G return-value optimisation) ---

func TestMaybeInPlace_NilBufferHeapAllocates(t *testing.T) {
	// outBuf == nil falls back to mallocgc — exactly one alloc per
	// call, matching the stock `new(T)` path.
	type box struct{ A, B int }
	f := func() {
		p := runtime.MaybeInPlaceTyped(nil, box{})
		if p == nil {
			t.Fatal("nil outBuf path returned nil")
		}
	}
	allocs := testing.AllocsPerRun(100, f)
	if allocs != 1 {
		t.Errorf("nil outBuf: got %v allocs/run, want exactly 1", allocs)
	}
}

func TestMaybeInPlace_NonNilBufferZeroAllocs(t *testing.T) {
	// outBuf != nil means the caller reserved storage; we return
	// it as-is (after zeroing). Zero allocations.
	type box struct{ A, B int }
	f := func() {
		var buf box
		p := runtime.MaybeInPlaceTyped(unsafe.Pointer(&buf), box{})
		if p != unsafe.Pointer(&buf) {
			t.Errorf("expected pointer passthrough to outBuf, got different pointer")
		}
	}
	allocs := testing.AllocsPerRun(100, f)
	if allocs != 0 {
		t.Errorf("non-nil outBuf: got %v allocs/run, want 0", allocs)
	}
}

func TestMaybeInPlace_NonNilBufferZeroes(t *testing.T) {
	// The caller's buffer can hold stale bytes from a prior use;
	// the callee must see it zeroed so its fill logic behaves
	// exactly as it would for a fresh new(T).
	type box struct {
		A int
		B int
	}
	buf := box{A: 99, B: -42}
	p := runtime.MaybeInPlaceTyped(unsafe.Pointer(&buf), box{})
	got := (*box)(p)
	if got.A != 0 || got.B != 0 {
		t.Errorf("expected zeroed buffer, got %+v", *got)
	}
}

func TestMaybeInPlace_PointerFieldsHandledSafely(t *testing.T) {
	// A type with pointer fields must be zeroed via typedmemclr so
	// the GC never observes stale pointer words. We can't directly
	// test the barrier path here, but we can verify that
	// maybeInPlace works on such types and that the buffer's
	// pointer fields are nil after the call.
	type box struct {
		P *[4]byte
		I int
	}
	stale := [4]byte{0xAA, 0xBB, 0xCC, 0xDD}
	buf := box{P: &stale, I: 7}
	p := runtime.MaybeInPlaceTyped(unsafe.Pointer(&buf), box{})
	got := (*box)(p)
	if got.P != nil {
		t.Errorf("pointer field not cleared: got %v", got.P)
	}
	if got.I != 0 {
		t.Errorf("scalar field not cleared: got %d", got.I)
	}
	runtime.GC()
	runtime.GC()
}

func TestResolveMask_DepthBudgetDecrements(t *testing.T) {
	// Record the depth the compute fn was called with. The
	// caller seeds maxDepth; the fn should see maxDepth-1 on
	// first entry.
	var seenDepth int
	var fn dynMaskFn = func(_ unsafe.Pointer, _ uint64, depth int) uint64 {
		seenDepth = depth
		return 0
	}
	runtime.ResolveMask(encodeMaskFn(&fn), nil, 0, runtime.MaxComputeMaskDepth)
	if want := runtime.MaxComputeMaskDepth - 1; seenDepth != want {
		t.Errorf("first-level compute fn depth: got %d, want %d", seenDepth, want)
	}
}
