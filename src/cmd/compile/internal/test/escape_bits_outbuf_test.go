// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"testing"
)

// The transformation the compiler would apply transparently under
// Phase G:
//
//	// Before (stock shape):
//	func makeT() *T { return new(T) }
//
//	// After (outBuf rewrite):
//	func makeT(outBuf *T) *T {
//	    if outBuf != nil {
//	        *outBuf = T{}    // zero for fresh-storage invariant
//	        return outBuf
//	    }
//	    return new(T)
//	}
//
// At a call site the caller allocates a stack buffer (if its own
// escape analysis says the result doesn't need heap lifetime) and
// passes &buf; otherwise it passes nil and the callee falls back
// to heap.
//
// We hand-write both shapes here to prove the alloc delta and
// validate runtime.maybeInPlace's ergonomics. The compiler
// rewrite is a separate piece of work — this file locks down
// the end-to-end plumbing.
//
// (The callee body here zeroes via a struct literal assignment
// rather than calling runtime.maybeInPlace directly; a
// //go:linkname escape hatch is restricted for most packages,
// and the inlined form demonstrates the same semantics.)

// escBitsPayload is the kind of small struct that a factory
// function routinely allocates and returns — 32 bytes, no
// pointer fields so the compiler can't devirt the alloc away on
// its own.
type escBitsPayload struct {
	A, B, C, D int64
}

// escBitsMakeStock is the baseline: the stock `return new(T)`
// shape. Stock Go must heap-allocate because the result escapes
// the callee frame by definition.
//
//go:noinline
func escBitsMakeStock() *escBitsPayload {
	p := new(escBitsPayload)
	p.A, p.B, p.C, p.D = 1, 2, 3, 4
	return p
}

// escBitsMakeOutBuf is the Phase-G shape. The caller passes
// either a stack buffer (non-nil → zero-alloc reuse) or nil (→
// fall back to heap). The callee fills the returned pointer
// identically either way.
//
//go:noinline
func escBitsMakeOutBuf(outBuf *escBitsPayload) *escBitsPayload {
	var p *escBitsPayload
	if outBuf != nil {
		*outBuf = escBitsPayload{}
		p = outBuf
	} else {
		p = new(escBitsPayload)
	}
	p.A, p.B, p.C, p.D = 1, 2, 3, 4
	return p
}

// escBitsPayloadScalarSink captures the SUM of a payload's
// fields so the loop body has an observable effect without
// retaining the pointer past the iteration. The sum is a scalar
// escape — it bypasses the heap-promotion the escape analyzer
// applies to pointer-typed sinks.
var escBitsPayloadScalarSink int64

// consumePayload reads p's fields into the scalar sink without
// escaping p itself. Gives the benchmark loop a visible
// side-effect while letting escape analysis keep &buf on the
// stack.
//
//go:noinline
func consumePayload(p *escBitsPayload) {
	escBitsPayloadScalarSink = p.A + p.B + p.C + p.D
}

// TestOutBufStockMakeAllocates: the stock shape allocates exactly
// once per call, regardless of caller context. Locks in the
// baseline we're trying to beat.
func TestOutBufStockMakeAllocates(t *testing.T) {
	f := func() { consumePayload(escBitsMakeStock()) }
	n := testing.AllocsPerRun(100, f)
	if n != 1 {
		t.Errorf("stock make: got %v allocs/run, want exactly 1", n)
	}
}

// TestOutBufStackBufferZeroAllocs: the Phase-G shape with a
// caller-provided stack buffer allocates zero times. The buffer
// lives in the caller's frame; the callee fills it in place and
// returns it.
func TestOutBufStackBufferZeroAllocs(t *testing.T) {
	f := func() {
		var buf escBitsPayload
		consumePayload(escBitsMakeOutBuf(&buf))
	}
	n := testing.AllocsPerRun(100, f)
	if n != 0 {
		t.Errorf("outbuf stack: got %v allocs/run, want 0", n)
	}
}

// TestOutBufNilFallsBackToHeap: when the caller passes nil the
// callee must allocate on the heap — same cost as the stock
// shape, proving nil is a correct fallback.
func TestOutBufNilFallsBackToHeap(t *testing.T) {
	f := func() { consumePayload(escBitsMakeOutBuf(nil)) }
	n := testing.AllocsPerRun(100, f)
	if n != 1 {
		t.Errorf("outbuf nil: got %v allocs/run, want exactly 1", n)
	}
}

// TestOutBufCorrectness: the callee fills the returned pointer
// the same way regardless of how storage was chosen.
func TestOutBufCorrectness(t *testing.T) {
	stock := escBitsMakeStock()
	if stock.A != 1 || stock.B != 2 || stock.C != 3 || stock.D != 4 {
		t.Errorf("stock make: got %+v, want {1 2 3 4}", *stock)
	}

	var buf escBitsPayload
	withBuf := escBitsMakeOutBuf(&buf)
	if withBuf.A != 1 || withBuf.B != 2 || withBuf.C != 3 || withBuf.D != 4 {
		t.Errorf("outbuf stack: got %+v, want {1 2 3 4}", *withBuf)
	}
	if withBuf != &buf {
		t.Errorf("outbuf stack: returned pointer %v, want &buf=%v", withBuf, &buf)
	}

	heap := escBitsMakeOutBuf(nil)
	if heap.A != 1 || heap.B != 2 || heap.C != 3 || heap.D != 4 {
		t.Errorf("outbuf heap: got %+v, want {1 2 3 4}", *heap)
	}
}

// --- Benchmarks: stock vs Phase-G shape ---

func BenchmarkOutBufStock(b *testing.B) {
	for i := 0; i < b.N; i++ {
		consumePayload(escBitsMakeStock())
	}
}

func BenchmarkOutBufStack(b *testing.B) {
	for i := 0; i < b.N; i++ {
		var buf escBitsPayload
		consumePayload(escBitsMakeOutBuf(&buf))
	}
}

func BenchmarkOutBufHeapFallback(b *testing.B) {
	for i := 0; i < b.N; i++ {
		consumePayload(escBitsMakeOutBuf(nil))
	}
}
