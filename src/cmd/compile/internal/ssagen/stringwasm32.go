// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssagen

import (
	"cmd/compile/internal/ssa"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
)

// The string of wasm32 (types.StringWasm32, doc/gd/sso-string.md): 16
// bytes, A at offset 0 and B at offset 8 (two uint64s). A string of 1
// to 15 bytes is A and B as words 1 and 2 of 8-byte words are: its
// bytes from offset 0, the length in the top 4 bits of B (the tag).
// Any other has the address of its bytes in word 0, its length in word
// 1 (the high half of A) and B zero but for a cached hash. In SSA a
// string is word 0 as a pointer (nil for an inline string), A and B.

// stringWasm32Tag returns the tag of str: its length if it is inline,
// else 0 (as a uint64).
func (s *state) stringWasm32Tag(str *ssa.Value) *ssa.Value {
	u64 := types.Types[types.TUINT64]
	b := s.newValue1(ssa.OpStringWord2, u64, str)
	return s.newValue2(ssa.OpRsh64Ux64, u64, b, s.constInt64(u64, 60))
}

// stringWasm32Inline reports whether str is kept in its words.
func (s *state) stringWasm32Inline(str *ssa.Value) *ssa.Value {
	u64 := types.Types[types.TUINT64]
	return s.newValue2(ssa.OpNeq64, types.Types[types.TBOOL], s.stringWasm32Tag(str), s.constInt64(u64, 0))
}

// sMakeWasm32 is sMake: the string of the length bytes at ptr, which
// is not inline (A is the address and the length).
func (s *state) sMakeWasm32(t *types.Type, ptr, length *ssa.Value) *ssa.Value {
	u64 := types.Types[types.TUINT64]
	addr := s.newValue1(ssa.OpZeroExt32to64, u64, s.newValue1(ssa.OpPtrBits, types.Types[types.TUINT32], ptr))
	n := s.newValue2(ssa.OpLsh64x64, u64, s.newValue1(ssa.OpZeroExt32to64, u64, length), s.constInt64(u64, 32))
	a := s.newValue2(ssa.OpOr64, u64, addr, n)
	return s.curBlock.NewValue3(s.peekPos(), ssa.OpStringMake, t, ptr, a, s.constInt64(u64, 0))
}

// stringWasm32Data is stringBytesTransient: word 0, or for an inline
// string the address of a copy of it in the frame.
func (s *state) stringWasm32Data(str *ssa.Value, resultT *types.Type) *ssa.Value {
	cond := s.stringWasm32Inline(str)
	bInline := s.f.NewBlock(ssa.BlockPlain)
	bHeap := s.f.NewBlock(ssa.BlockPlain)
	bEnd := s.f.NewBlock(ssa.BlockPlain)
	b := s.endBlock()
	b.Kind = ssa.BlockIf
	b.SetControl(cond)
	b.Likely = ssa.BranchUnlikely
	b.AddEdgeTo(bInline)
	b.AddEdgeTo(bHeap)

	marker := ssaMarker("stringBytes")

	s.startBlock(bHeap)
	s.vars[marker] = s.newValue1(ssa.OpStringPtr, resultT, str)
	s.endBlock().AddEdgeTo(bEnd)

	// (A and B are all the bytes: a temporary with no pointer.)
	s.startBlock(bInline)
	u64 := types.Types[types.TUINT64]
	tmp := typecheck.TempAt(s.gd, s.peekPos(), s.curfn, types.Types[types.TCOMPLEX128])
	tmp.SetAddrtaken(true)
	s.vars[memVar] = s.newValue1A(ssa.OpVarDef, types.TypeMem, tmp, s.mem())
	tmpAddr := s.addr(tmp)
	s.store(u64, s.newValue1I(ssa.OpOffPtr, types.NewPtr(u64), 0, tmpAddr), s.newValue1(ssa.OpStringHash, u64, str))
	s.store(u64, s.newValue1I(ssa.OpOffPtr, types.NewPtr(u64), 8, tmpAddr), s.newValue1(ssa.OpStringWord2, u64, str))
	s.vars[marker] = s.newValue1I(ssa.OpOffPtr, resultT, 0, tmpAddr)
	s.endBlock().AddEdgeTo(bEnd)

	s.startBlock(bEnd)
	result := s.variable(marker, resultT)
	delete(s.vars, marker)
	return result
}

// stringWasm32Index is stringIndex: the byte at i (in bounds) of str,
// through word 0, or for an inline string shifted out of A or B.
func (s *state) stringWasm32Index(str, i *ssa.Value) *ssa.Value {
	u8 := types.Types[types.TUINT8]
	u64 := types.Types[types.TUINT64]
	intT := types.Types[types.TINT]
	ptrT := s.f.Config.Types.BytePtr

	cond := s.stringWasm32Inline(str)
	bInline := s.f.NewBlock(ssa.BlockPlain)
	bHeap := s.f.NewBlock(ssa.BlockPlain)
	bEnd := s.f.NewBlock(ssa.BlockPlain)
	b := s.endBlock()
	b.Kind = ssa.BlockIf
	b.SetControl(cond)
	b.Likely = ssa.BranchUnlikely
	b.AddEdgeTo(bInline)
	b.AddEdgeTo(bHeap)

	marker := ssaMarker("stringIndex")

	s.startBlock(bHeap)
	ptr := s.newValue1(ssa.OpStringPtr, ptrT, str)
	s.vars[marker] = s.load(u8, s.newValue2(ssa.OpAddPtr, ptrT, ptr, i))
	s.endBlock().AddEdgeTo(bEnd)

	// Bytes 0 to 7 are A, 8 to 14 B: the byte is that word shifted
	// right by (i&7)*8.
	s.startBlock(bInline)
	a := s.newValue1(ssa.OpStringHash, u64, str)
	bw := s.newValue1(ssa.OpStringWord2, u64, str)
	inA := s.newValue2(ssa.OpLess32U, types.Types[types.TBOOL], i, s.constInt32(intT, 8))
	w := s.newValue3(ssa.OpCondSelect, u64, a, bw, inA)
	i64 := s.newValue1(ssa.OpZeroExt32to64, u64, i)
	sh := s.newValue2(ssa.OpLsh64x64, u64, i64, s.constInt64(u64, 3))
	sh.AuxInt = 1 // bounded
	sh = s.newValue2(ssa.OpAnd64, u64, sh, s.constInt64(u64, 56))
	b8 := s.newValue2(ssa.OpRsh64Ux64, u64, w, sh)
	b8.AuxInt = 1 // bounded
	s.vars[marker] = s.newValue1(ssa.OpTrunc64to8, u8, b8)
	s.endBlock().AddEdgeTo(bEnd)

	s.startBlock(bEnd)
	result := s.variable(marker, u8)
	delete(s.vars, marker)
	return result
}
