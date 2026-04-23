// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"testing"
)

// The tests in this file are the end-to-end target of the gd fork's
// "per-argument escape bits on closures and interface methods"
// optimization (doc/gd/escape-bits.md). They are expected to fail
// today — Phase A is landed but the escape analyzer still returns
// heapHole() for dynamic callees — and to pass once Phases B/C/D
// land and the caller actually consults the bit at indirect-call
// sites.
//
// Running them early keeps us honest: the goal isn't "mask is
// computed," it's "heap allocations disappear on the hot path."
// If these tests pass without the optimization, one of the
// intermediate passes is already rewriting the program in a way
// that incidentally folds the alloc away and we need to double-check
// our baseline.

// escBitsSink prevents the compiler from elliding stores through the
// non-escaping closures below. Without a store visible to the rest
// of the program, the entire closure body becomes dead code.
var escBitsSink int

// escBitsEscapedPtr forces the leak-tests' arg to genuinely escape.
// Global store = standard escape edge.
var escBitsEscapedPtr *int

// nonEscapingReader stores the closure in a package-level var after
// init so its body is completely opaque to the caller-site escape
// analyzer. The escape profile is "reads *p and writes to a scalar
// global" — bit stays clear in the computed EscMask.
var nonEscapingReader func(p *int)

// escapingRetainer also sits in a package-level var. Its body stores
// its pointer arg into a global — escape bit must be set, and the
// caller must heap-alloc the arg.
var escapingRetainer func(p *int)

func init() {
	nonEscapingReader = func(p *int) {
		escBitsSink = *p
	}
	escapingRetainer = func(p *int) {
		escBitsEscapedPtr = p
	}
}

// TestEscapeBitsClosureNonEscape is the canonical zero-alloc target:
// a closure that does not let its pointer arg escape. Under stock Go
// (and today's gd fork) every call heap-allocates the int because
// the analyzer treats the indirect call as an unknown callee. After
// the escape-bit optimization lands, the arg stays on the stack.
func TestEscapeBitsClosureNonEscape(t *testing.T) {
	f := func() {
		x := 42
		nonEscapingReader(&x)
	}
	n := testing.AllocsPerRun(100, f)
	if n > 0 {
		t.Errorf("non-escaping closure arg: got %v allocs/run, want 0 (escape-bits optimization not yet landed)", n)
	}
}

// TestEscapeBitsClosureEscape is the inverse: the closure really
// does retain its arg via a global. The caller MUST heap-allocate
// every call; we lock that behaviour at 1 alloc/run so a future
// over-zealous optimizer doesn't silently corrupt memory.
func TestEscapeBitsClosureEscape(t *testing.T) {
	f := func() {
		x := 42
		escapingRetainer(&x)
	}
	n := testing.AllocsPerRun(100, f)
	if n != 1 {
		t.Errorf("escaping closure arg: got %v allocs/run, want exactly 1", n)
	}
}

// escBitsIface + its two implementations exercise the interface-
// method-dispatch side of the same optimization. One impl retains
// the arg, the other merely reads.
type escBitsIface interface {
	Consume(p *int)
}

type escBitsReader struct{}

func (escBitsReader) Consume(p *int) {
	escBitsSink = *p
}

type escBitsRetainer struct{}

func (escBitsRetainer) Consume(p *int) {
	escBitsEscapedPtr = p
}

// escBitsOpaqueReader and escBitsOpaqueRetainer are package-level
// ifaces — stops escape analysis from collapsing the concrete type
// at the call site.
var escBitsOpaqueReader escBitsIface
var escBitsOpaqueRetainer escBitsIface

func init() {
	escBitsOpaqueReader = escBitsReader{}
	escBitsOpaqueRetainer = escBitsRetainer{}
}

// TestEscapeBitsIfaceNonEscape: interface method whose implementation
// doesn't escape its arg. Post-optimization this should be 0 allocs.
func TestEscapeBitsIfaceNonEscape(t *testing.T) {
	f := func() {
		x := 42
		escBitsOpaqueReader.Consume(&x)
	}
	n := testing.AllocsPerRun(100, f)
	if n > 0 {
		t.Errorf("non-escaping iface method arg: got %v allocs/run, want 0 (escape-bits optimization not yet landed)", n)
	}
}

// TestEscapeBitsIfaceEscape: interface method implementation that
// retains the arg. Must stay at 1 alloc/run regardless of
// optimization state.
func TestEscapeBitsIfaceEscape(t *testing.T) {
	f := func() {
		x := 42
		escBitsOpaqueRetainer.Consume(&x)
	}
	n := testing.AllocsPerRun(100, f)
	if n != 1 {
		t.Errorf("escaping iface method arg: got %v allocs/run, want exactly 1", n)
	}
}
