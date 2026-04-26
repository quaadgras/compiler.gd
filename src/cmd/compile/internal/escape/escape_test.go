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

// testGd is the *base.Invocation passed to escape package functions in
// tests. We populate just the bits the package reads — Ctxt for diag
// routing — so we don't have to stand up a full compiler invocation.
var testGd = &base.Invocation{}

func TestMain(m *testing.M) {
	ssagen.Arch.LinkArch = &x86.Linkamd64
	ssagen.Arch.REGSP = x86.REGSP
	ssagen.Arch.MAXWIDTH = 1 << 50
	types.MaxWidth = ssagen.Arch.MAXWIDTH
	testGd.Ctxt = obj.Linknew(ssagen.Arch.LinkArch)
	testGd.Ctxt.DiagFunc = testGd.Errorf
	testGd.Ctxt.DiagFlush = testGd.FlushErrors
	testGd.Ctxt.Bso = bufio.NewWriter(os.Stdout)
	localPkg := types.NewPkg("p", "local")
	localPkg.Local = true
	localPkg.Prefix = "p"
	testGd.LocalPkg = localPkg
	types.PtrSize = ssagen.Arch.LinkArch.PtrSize
	types.RegSize = ssagen.Arch.LinkArch.RegSize
	typecheck.InitUniverse(testGd)
	os.Exit(m.Run())
}

// mkParam builds a *types.Field representing one call argument with
// the given type and pre-installed escape Note. Mirrors what escape
// analysis itself writes into param.Note via paramTag.
func mkParam(gd *base.Invocation, t *types.Type, esc leaks) *types.Field {
	name := typecheck.Lookup(testGd, "?")
	f := types.NewField(src.NoXPos, name, t)
	n := ir.NewNameAt(gd, src.NoXPos, name, t)
	n.Class = ir.PPARAM
	f.Nname = n
	f.Note = esc.Encode()
	return f
}

// mkNoEscapeParam returns a param whose leaks encode "does not leak"
// — canonically, the empty Note produced when Heap() == 0. (Encode
// returns "" for that case.)
func mkNoEscapeParam(gd *base.Invocation, t *types.Type) *types.Field {
	var esc leaks
	// Leave heap unset so Heap() == -1 (never-flows). Encode then
	// uses the default "" which parseLeaks will inflate back to
	// AddHeap(0) — meaning the external-function-default, heap-
	// escape. To test the "truly does not escape" branch, set
	// Mutator only: that encodes as a non-empty Note and Heap() is
	// still -1.
	esc.AddMutator(gd, 0)
	return mkParam(gd, t, esc)
}

// mkHeapEscapingParam returns a param whose leaks encode "escapes
// to heap" (Heap() == 0, the most common non-trivial case).
func mkHeapEscapingParam(gd *base.Invocation, t *types.Type) *types.Field {
	var esc leaks
	esc.AddHeap(gd, 0)
	return mkParam(gd, t, esc)
}

func TestComputeEscMask_Empty(t *testing.T) {
	// func() — no params, no bits.
	sig := types.NewSignature(testGd, nil, nil, nil)
	got := computeEscMask(testGd, sig)
	if got != 0 {
		t.Errorf("empty signature: got mask=0x%x, want 0", got)
	}
}

func TestComputeEscMask_SingleHeapEscape(t *testing.T) {
	// func(p *int) — p escapes to heap.
	intT := types.Types[types.TINT]
	ptrT := types.NewPtr(intT)
	p := mkHeapEscapingParam(testGd, ptrT)
	sig := types.NewSignature(testGd, nil, []*types.Field{p}, nil)

	got := computeEscMask(testGd, sig)
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
	p := mkNoEscapeParam(testGd, ptrT)
	sig := types.NewSignature(testGd, nil, []*types.Field{p}, nil)

	got := computeEscMask(testGd, sig)
	if got != 0 {
		t.Errorf("non-escaping p: got mask=0x%x, want 0", got)
	}
}

func TestComputeEscMask_Mixed(t *testing.T) {
	// func(a *int, b *int, c *int) — a escapes, b doesn't, c escapes.
	intT := types.Types[types.TINT]
	ptrT := types.NewPtr(intT)
	a := mkHeapEscapingParam(testGd, ptrT)
	b := mkNoEscapeParam(testGd, ptrT)
	c := mkHeapEscapingParam(testGd, ptrT)
	sig := types.NewSignature(testGd, nil, []*types.Field{a, b, c}, nil)

	got := computeEscMask(testGd, sig)
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

	recv := mkHeapEscapingParam(testGd, recvT)
	p := mkNoEscapeParam(testGd, ptrInt)
	sig := types.NewSignature(testGd, recv, []*types.Field{p}, nil)

	got := computeEscMask(testGd, sig)
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
	n := mkNoEscapeParam(testGd, intT) // Heap()==-1 tag — the scalar shape
	p := mkHeapEscapingParam(testGd, ptrT)
	sig := types.NewSignature(testGd, nil, []*types.Field{n, p}, nil)

	got := computeEscMask(testGd, sig)
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
	esc := mkHeapEscapingParam(testGd, ptrT)
	sig := types.NewSignature(testGd, nil, []*types.Field{esc}, nil)

	got := computeEscMask(testGd, sig)
	if got&1 != 0 {
		t.Errorf("got mask=0x%x, bit 0 must be reserved (clear)", got)
	}
}

func TestStackAllocatable(t *testing.T) {
	// Contract: StackAllocatable reports raw Esc-value eligibility
	// for stack allocation. Only EscNone qualifies on the raw
	// value; EscCandidate is signalled by a separate bit (see
	// ir.NodeStackAllocatable) because it rides alongside EscHeap
	// so every non-escape-bits consumer keeps treating the node
	// as heap-promoted.
	cases := []struct {
		esc  uint16
		want bool
		name string
	}{
		{ir.EscUnknown, false, "EscUnknown"},
		{ir.EscNone, true, "EscNone"},
		{ir.EscHeap, false, "EscHeap"},
		{ir.EscNever, false, "EscNever"},
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
		params[i] = mkHeapEscapingParam(testGd, ptrT)
	}
	sig := types.NewSignature(testGd, nil, params, nil)

	got := computeEscMask(testGd, sig)
	// Bits 1..63 should all be set; bits 0 and 64+ not representable.
	want := uint64(0xFFFFFFFFFFFFFFFE) // all except bit 0
	if got != want {
		t.Errorf("overflow: got mask=0x%x, want 0x%x", got, want)
	}
}
