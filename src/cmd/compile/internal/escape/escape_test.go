// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package escape

import (
	"bufio"
	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/ssagen"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
	"cmd/internal/obj"
	"cmd/internal/obj/x86"
	"cmd/internal/src"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	ssagen.Arch.LinkArch = &x86.Linkamd64
	ssagen.Arch.REGSP = x86.REGSP
	ssagen.Arch.MAXWIDTH = 1 << 50
	types.MaxWidth = ssagen.Arch.MAXWIDTH
	base.Ctxt = obj.Linknew(ssagen.Arch.LinkArch)
	base.Ctxt.DiagFunc = base.Errorf
	base.Ctxt.DiagFlush = base.FlushErrors
	base.Ctxt.Bso = bufio.NewWriter(os.Stdout)
	types.LocalPkg = types.NewPkg("p", "local")
	types.LocalPkg.Prefix = "p"
	types.PtrSize = ssagen.Arch.LinkArch.PtrSize
	types.RegSize = ssagen.Arch.LinkArch.RegSize
	typecheck.InitUniverse()
	os.Exit(m.Run())
}

// mkParam builds a *types.Field representing one call argument with
// the given type and pre-installed escape Note. Mirrors what escape
// analysis itself writes into param.Note via paramTag.
func mkParam(t *types.Type, esc leaks) *types.Field {
	name := typecheck.Lookup("?")
	f := types.NewField(src.NoXPos, name, t)
	n := ir.NewNameAt(src.NoXPos, name, t)
	n.Class = ir.PPARAM
	f.Nname = n
	f.Note = esc.Encode()
	return f
}

// mkNoEscapeParam returns a param whose leaks encode "does not leak"
// — canonically, the empty Note produced when Heap() == 0. (Encode
// returns "" for that case.)
func mkNoEscapeParam(t *types.Type) *types.Field {
	var esc leaks
	// Leave heap unset so Heap() == -1 (never-flows). Encode then
	// uses the default "" which parseLeaks will inflate back to
	// AddHeap(0) — meaning the external-function-default, heap-
	// escape. To test the "truly does not escape" branch, set
	// Mutator only: that encodes as a non-empty Note and Heap() is
	// still -1.
	esc.AddMutator(0)
	return mkParam(t, esc)
}

// mkHeapEscapingParam returns a param whose leaks encode "escapes
// to heap" (Heap() == 0, the most common non-trivial case).
func mkHeapEscapingParam(t *types.Type) *types.Field {
	var esc leaks
	esc.AddHeap(0)
	return mkParam(t, esc)
}

func TestComputeEscMask_Empty(t *testing.T) {
	// func() — no params, no bits.
	sig := types.NewSignature(nil, nil, nil)
	got := computeEscMask(sig)
	if got != 0 {
		t.Errorf("empty signature: got mask=0x%x, want 0", got)
	}
}

func TestComputeEscMask_SingleHeapEscape(t *testing.T) {
	// func(p *int) — p escapes to heap.
	intT := types.Types[types.TINT]
	ptrT := types.NewPtr(intT)
	p := mkHeapEscapingParam(ptrT)
	sig := types.NewSignature(nil, []*types.Field{p}, nil)

	got := computeEscMask(sig)
	// bit 0 is the discriminator (reserved).
	// bit 1 is the first argument (p). p escapes → bit 1 set.
	want := uint64(1 << 1)
	if got != want {
		t.Errorf("got mask=0x%x, want 0x%x (bit 1 for escaping p)", got, want)
	}
}

func TestComputeEscMask_SingleNonEscape(t *testing.T) {
	// func(p *int) — p does NOT escape.
	intT := types.Types[types.TINT]
	ptrT := types.NewPtr(intT)
	p := mkNoEscapeParam(ptrT)
	sig := types.NewSignature(nil, []*types.Field{p}, nil)

	got := computeEscMask(sig)
	if got != 0 {
		t.Errorf("non-escaping p: got mask=0x%x, want 0", got)
	}
}

func TestComputeEscMask_Mixed(t *testing.T) {
	// func(a *int, b *int, c *int) — a escapes, b doesn't, c escapes.
	intT := types.Types[types.TINT]
	ptrT := types.NewPtr(intT)
	a := mkHeapEscapingParam(ptrT)
	b := mkNoEscapeParam(ptrT)
	c := mkHeapEscapingParam(ptrT)
	sig := types.NewSignature(nil, []*types.Field{a, b, c}, nil)

	got := computeEscMask(sig)
	// bits 1 and 3 set (args a and c); bit 2 clear (arg b).
	want := uint64((1 << 1) | (1 << 3))
	if got != want {
		t.Errorf("mixed: got mask=0x%x, want 0x%x", got, want)
	}
}

func TestComputeEscMask_Method(t *testing.T) {
	// type T struct{}
	// func (recv *T) M(p *int) — recv escapes, p doesn't.
	intT := types.Types[types.TINT]
	ptrInt := types.NewPtr(intT)
	tstruct := types.NewStruct(nil)
	recvT := types.NewPtr(tstruct)

	recv := mkHeapEscapingParam(recvT)
	p := mkNoEscapeParam(ptrInt)
	sig := types.NewSignature(recv, []*types.Field{p}, nil)

	got := computeEscMask(sig)
	// With receiver counted as arg 0: bit 1 = recv (escapes), bit 2 = p (no).
	want := uint64(1 << 1)
	if got != want {
		t.Errorf("method recv/p: got mask=0x%x, want 0x%x", got, want)
	}
}

func TestComputeEscMask_ScalarsIgnored(t *testing.T) {
	// func(n int, p *int) — n is a scalar (escape analysis encodes
	// Heap()==-1 for it, since scalars can't reach heap); p escapes.
	intT := types.Types[types.TINT]
	ptrT := types.NewPtr(intT)
	n := mkNoEscapeParam(intT) // Heap()==-1 tag — the scalar shape
	p := mkHeapEscapingParam(ptrT)
	sig := types.NewSignature(nil, []*types.Field{n, p}, nil)

	got := computeEscMask(sig)
	// Arg 0 is n (scalar, no bit). Arg 1 is p (escapes → bit 2).
	want := uint64(1 << 2)
	if got != want {
		t.Errorf("scalar+ptr: got mask=0x%x, want 0x%x", got, want)
	}
}

func TestComputeEscMask_BitZeroReserved(t *testing.T) {
	// For Phase F we reserve bit 0 as the dynamic-mask-fn
	// discriminator. The static-mask path must never set it, no
	// matter how we populate the signature.
	intT := types.Types[types.TINT]
	ptrT := types.NewPtr(intT)
	esc := mkHeapEscapingParam(ptrT)
	sig := types.NewSignature(nil, []*types.Field{esc}, nil)

	got := computeEscMask(sig)
	if got&1 != 0 {
		t.Errorf("got mask=0x%x, bit 0 must be reserved (clear)", got)
	}
}

func TestStackAllocatable(t *testing.T) {
	// Locks the contract Phase C relies on: both EscNone and the new
	// EscCandidate pick stack allocation at emission time. EscHeap and
	// EscUnknown force heap. EscNever is a separate "never escapes"
	// marker used for compiler-known-safe objects; it's grouped with
	// stack-allocatable because the underlying object lives in a fixed
	// spot (rodata or statically-allocated).
	cases := []struct {
		esc  uint16
		want bool
		name string
	}{
		{ir.EscUnknown, false, "EscUnknown"},
		{ir.EscNone, true, "EscNone"},
		{ir.EscHeap, false, "EscHeap"},
		{ir.EscNever, false, "EscNever"},
		{ir.EscCandidate, true, "EscCandidate"},
	}
	for _, c := range cases {
		if got := ir.StackAllocatable(c.esc); got != c.want {
			t.Errorf("StackAllocatable(%s=%d) = %v, want %v", c.name, c.esc, got, c.want)
		}
	}
}

func TestComputeEscMask_OverflowGoesConservative(t *testing.T) {
	// With 63 heap-escaping params we fill bits 1..63. The 64th and
	// beyond are dropped — callers treat unset bits as "maybe
	// conservative heap-alloc" in a future phase; this test just
	// locks that the computer doesn't panic and doesn't scribble
	// into bit 0.
	intT := types.Types[types.TINT]
	ptrT := types.NewPtr(intT)
	const N = 70
	params := make([]*types.Field, N)
	for i := range params {
		params[i] = mkHeapEscapingParam(ptrT)
	}
	sig := types.NewSignature(nil, params, nil)

	got := computeEscMask(sig)
	// Bits 1..63 should all be set; bits 0 and 64+ not representable.
	want := uint64(0xFFFFFFFFFFFFFFFE) // all except bit 0
	if got != want {
		t.Errorf("overflow: got mask=0x%x, want 0x%x", got, want)
	}
}
