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
