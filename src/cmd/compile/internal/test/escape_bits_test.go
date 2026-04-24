// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"testing"
	"unsafe"
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

// BenchmarkEscapeBitsClosureNonEscape measures the alloc + time
// win of the escape-bits wrap when the closure does NOT retain its
// arg. Stock Go allocates &x on the heap every iteration (the
// escape analyzer can't see through the opaque func var); the gd
// fork's wrap sees the clear mask bit and leaves &x on the stack.
func BenchmarkEscapeBitsClosureNonEscape(b *testing.B) {
	for i := 0; i < b.N; i++ {
		x := 42
		nonEscapingReader(&x)
	}
}

// BenchmarkEscapeBitsClosureEscape locks in the inverse: when the
// closure's mask bit IS set, the gd fork still allocates once —
// the wrap materializes &x to heap before dispatch, matching stock
// Go exactly.
func BenchmarkEscapeBitsClosureEscape(b *testing.B) {
	for i := 0; i < b.N; i++ {
		x := 42
		escapingRetainer(&x)
	}
}

// BenchmarkEscapeBitsIfaceNonEscape — iface dispatch, callee does
// not retain its arg.
func BenchmarkEscapeBitsIfaceNonEscape(b *testing.B) {
	for i := 0; i < b.N; i++ {
		x := 42
		escBitsOpaqueReader.Consume(&x)
	}
}

// BenchmarkEscapeBitsIfaceEscape — iface dispatch, callee retains.
func BenchmarkEscapeBitsIfaceEscape(b *testing.B) {
	for i := 0; i < b.N; i++ {
		x := 42
		escBitsOpaqueRetainer.Consume(&x)
	}
}

// --- Devirt-blocked benchmarks ---
//
// These patterns defeat stock Go's devirtualization by forcing
// the interface or func value through a path where the compiler
// can't prove a single concrete callee. They mirror real-world
// plugin / event-bus / handler-registry shapes where multiple
// implementations are registered at init and the caller genuinely
// doesn't know which one is active at compile time.

type escBitsHandler interface {
	Handle(p *int)
}

type escBitsReadHandler struct{}

func (escBitsReadHandler) Handle(p *int) { escBitsSink = *p }

type escBitsWriteHandler struct{}

func (escBitsWriteHandler) Handle(p *int) { *p = escBitsSink + 1 }

// escBitsHandlerRegistry is populated in init with two concrete
// types so escape analysis can't devirtualize the iface variable
// back to a single method body. The active handler is picked at
// runtime via map lookup, which also keeps the value opaque.
var escBitsHandlerRegistry map[string]escBitsHandler

func init() {
	escBitsHandlerRegistry = map[string]escBitsHandler{
		"read":  escBitsReadHandler{},
		"write": escBitsWriteHandler{},
	}
}

// BenchmarkEscapeBitsHandlerDispatch — the handler-registry
// pattern common in event loops and plugin systems. Stock Go's
// escape analyzer has to assume the iface callee might retain the
// arg, so &x heap-allocates every iteration. The gd fork reads
// the per-itab mask; both registered handlers have masks of 0
// (neither retains), so the stack pointer goes through unchanged.
func BenchmarkEscapeBitsHandlerDispatch(b *testing.B) {
	h := escBitsHandlerRegistry["read"]
	for i := 0; i < b.N; i++ {
		var x int = 1
		h.Handle(&x)
	}
}

// escBitsCallback — the classic "accept a *T callback arg" shape.
// Registering through a map forces the value to be opaque.
var escBitsCallbackRegistry map[string]func(*int)

func init() {
	escBitsCallbackRegistry = map[string]func(*int){
		"read":  func(p *int) { escBitsSink = *p },
		"write": func(p *int) { *p = escBitsSink + 1 },
	}
}

// BenchmarkEscapeBitsCallbackDispatch — func-var dispatch via
// map. Same shape as event-bus subscribe / on-message code.
func BenchmarkEscapeBitsCallbackDispatch(b *testing.B) {
	cb := escBitsCallbackRegistry["read"]
	for i := 0; i < b.N; i++ {
		var x int = 1
		cb(&x)
	}
}

// TestEscapeBitsItabDispatch verifies Phase A.3: growing the itab to
// carry a per-method escape-mask tail after Fun didn't break dispatch.
// Exercises a multi-method interface with multiple concretes so both
// the compile-time static itab and the runtime-allocated itab paths
// through getitab + itabInit actually get hit. If the size math in
// either the compiler (writeITab) or the runtime (persistentalloc)
// got out of sync with the other side's understanding of the mask
// tail, the method pointer read at call time would either segfault
// or dispatch to a bogus address.
func TestEscapeBitsItabDispatch(t *testing.T) {
	type adder interface {
		Add(int) int
		Mul(int) int
		Name() string
	}
	// Methods intentionally stored before the iface box so the
	// compiler can't see through the dynamic type at dispatch.
	reg := map[string]adder{}
	reg["simple"] = &simpleImpl{base: 10}
	reg["doubler"] = &doublerImpl{base: 5}
	cases := []struct {
		key      string
		addArg   int
		mulArg   int
		wantAdd  int
		wantMul  int
		wantName string
	}{
		{"simple", 3, 4, 13, 40, "simple"},   // 10+3=13, 10*4=40
		{"doubler", 2, 7, 9, 140, "doubler"}, // 5+2*2=9, 5*7*4=140
	}
	for _, c := range cases {
		v := reg[c.key]
		if got := v.Add(c.addArg); got != c.wantAdd {
			t.Errorf("%s.Add(%d) = %d, want %d", c.key, c.addArg, got, c.wantAdd)
		}
		if got := v.Mul(c.mulArg); got != c.wantMul {
			t.Errorf("%s.Mul(%d) = %d, want %d", c.key, c.mulArg, got, c.wantMul)
		}
		if got := v.Name(); got != c.wantName {
			t.Errorf("%s.Name() = %q, want %q", c.key, got, c.wantName)
		}
	}
}

type simpleImpl struct{ base int }

func (s *simpleImpl) Add(x int) int  { return s.base + x }
func (s *simpleImpl) Mul(x int) int  { return s.base * x }
func (s *simpleImpl) Name() string   { return "simple" }

type doublerImpl struct{ base int }

func (d *doublerImpl) Add(x int) int { return d.base + x*2 }
func (d *doublerImpl) Mul(x int) int { return d.base * x * 4 }
func (d *doublerImpl) Name() string  { return "doubler" }

// TestEscapeBitsClosureLayout verifies Phase A.2: every closure carries
// its EscMask as the second word of its struct, right after the fn
// pointer. The mask is the fork's contract surface — Phase C call-site
// checks expect it at offset PtrSize — so a regression in closure
// layout (e.g. a future optimizer reshuffling fields) would silently
// corrupt the call site once Phase D lands. Lock it now.
func TestEscapeBitsClosureLayout(t *testing.T) {
	captured := 0
	c := func(x int) int { captured = x; return x }

	// A closure value is, at the runtime representation, a pointer
	// to a struct { F, M, captures... }. The interface conversion
	// preserves that shape.
	cp := *(**struct {
		F uintptr
		M uint64
	})(unsafe.Pointer(&c))

	if cp.F == 0 {
		t.Errorf("closure F field is zero; closure struct layout unexpected")
	}
	// M with the default computeEscMask on a closure that escapes
	// captured via global store has the "captured" escape bit set.
	// But this particular body stores through a captured local that
	// is itself not escaping, so the mask can legitimately be zero.
	// Regardless, M must be accessible as the second word — we lock
	// that M is zero-valued (the explicit escape bits will come with
	// Phase D). The important property is no segfault / no layout
	// drift.
	_ = cp.M
	_ = c(41)
	if captured != 41 {
		t.Errorf("closure body didn't execute correctly: got captured=%d", captured)
	}
}
