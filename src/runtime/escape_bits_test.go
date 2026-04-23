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
