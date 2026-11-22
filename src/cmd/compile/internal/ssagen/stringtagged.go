// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssagen

import (
	"cmd/compile/internal/ir"
	"cmd/compile/internal/ssa"
	"cmd/compile/internal/staticdata"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
	"cmd/internal/sys"
)

// Strings of two words (types.StringTwoWords, doc/gd/sso-string.md): w0
// and w1, the address of the bytes and the length. With 8-byte words
// (types.StringTagged) a string of 1 to 15 bytes is inline instead: w0
// has bit 63 set, the length in bits 56..62 and bytes 8..14 below, w1
// is bytes 0..7, the unused bytes zero. In SSA a string is these two
// values, w0 a pointer (OpStringPtr), w1 an int (OpStringWord1).

// stringW0Bits returns w0 of str as an integer.
func (s *state) stringW0Bits(str *ssa.Value) *ssa.Value {
	w0 := s.newValue1(ssa.OpStringPtr, s.f.Config.Types.BytePtr, str)
	return s.newValue1(ssa.OpPtrBits, types.Types[types.TUINTPTR], w0)
}

// stringTaggedInline reports whether str is inline: w0's bit 63.
func (s *state) stringTaggedInline(str *ssa.Value) *ssa.Value {
	i64 := types.Types[types.TINT64]
	return s.newValue2(ssa.OpLess64, types.Types[types.TBOOL], s.stringW0Bits(str), s.constInt64(i64, 0))
}

// stringTaggedBuf stores the bytes of the inline string str to a fresh
// 16-byte temporary of the frame and returns its address as resultT:
// bytes 0..7 are w1, 8..14 w0 below its tag (as loads of the target's
// byte order have them).
func (s *state) stringTaggedBuf(str *ssa.Value, resultT *types.Type) *ssa.Value {
	u64 := types.Types[types.TUINT64]
	tmp := typecheck.TempAt(s.gd, s.peekPos(), s.curfn, types.Types[types.TCOMPLEX128])
	tmp.SetAddrtaken(true)
	tmpAddr := s.addr(tmp)
	w1 := s.newValue1(ssa.OpCopy, u64, s.newValue1(ssa.OpStringWord1, types.Types[types.TINT], str))
	w0 := s.newValue1(ssa.OpCopy, u64, s.stringW0Bits(str))
	if s.config.BigEndian {
		w0 = s.newValue2(ssa.OpLsh64x64, u64, w0, s.constInt64(u64, 8))
	}
	s.store(u64, s.newValue1I(ssa.OpOffPtr, types.NewPtr(u64), 0, tmpAddr), w1)
	s.store(u64, s.newValue1I(ssa.OpOffPtr, types.NewPtr(u64), 8, tmpAddr), w0)
	return s.newValue1I(ssa.OpOffPtr, resultT, 0, tmpAddr)
}

// stringTaggedData is stringBytesTransient: w0, or for an inline string
// the address of a copy of its bytes in the frame. A constant's are in
// its go:string symbol, inline or not.
func (s *state) stringTaggedData(str *ssa.Value, resultT *types.Type) *ssa.Value {
	if str.Op == ssa.OpConstString {
		if c := ssa.ConstStringValue(str); c != "" {
			return s.entryNewValue1A(ssa.OpAddr, resultT, staticdata.StringSym(s.gd, s.peekPos(), c), s.sb)
		}
		return s.constNil(resultT)
	}
	cond := s.stringTaggedInline(str)
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

	s.startBlock(bInline)
	s.vars[marker] = s.stringTaggedBuf(str, resultT)
	s.endBlock().AddEdgeTo(bEnd)

	s.startBlock(bEnd)
	result := s.variable(marker, resultT)
	delete(s.vars, marker)
	return result
}

// stringTaggedIndex is stringIndex: the byte at i (in bounds) of str,
// through w0, or for an inline string shifted out of w1 or w0.
func (s *state) stringTaggedIndex(str, i *ssa.Value) *ssa.Value {
	u8 := types.Types[types.TUINT8]
	u64 := types.Types[types.TUINT64]
	ptrT := s.f.Config.Types.BytePtr

	cond := s.stringTaggedInline(str)
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

	// Bytes 0 to 7 are w1, 8 to 14 w0: the byte is that word shifted
	// right by (i&7)*8 (big-endian: by 56-(i&7)*8, w0 shifted up by 8
	// first, over its tag).
	s.startBlock(bInline)
	w1 := s.newValue1(ssa.OpCopy, u64, s.newValue1(ssa.OpStringWord1, types.Types[types.TINT], str))
	w0 := s.newValue1(ssa.OpCopy, u64, s.stringW0Bits(str))
	if s.config.BigEndian {
		w0 = s.newValue2(ssa.OpLsh64x64, u64, w0, s.constInt64(u64, 8))
	}
	inW1 := s.newValue2(ssa.OpLess64U, types.Types[types.TBOOL], i, s.constInt64(types.Types[types.TINT], 8))
	w := s.ternary(inW1, w1, w0)
	sh := s.newValue2(ssa.OpLsh64x64, u64, i, s.constInt64(u64, 3))
	sh.AuxInt = 1 // bounded
	sh = s.newValue2(ssa.OpAnd64, u64, sh, s.constInt64(u64, 56))
	if s.config.BigEndian {
		sh = s.newValue2(ssa.OpSub64, u64, s.constInt64(u64, 56), sh)
	}
	b8 := s.newValue2(ssa.OpRsh64Ux64, u64, w, sh)
	b8.AuxInt = 1 // bounded
	s.vars[marker] = s.newValue1(ssa.OpTrunc64to8, u8, b8)
	s.endBlock().AddEdgeTo(bEnd)

	s.startBlock(bEnd)
	result := s.variable(marker, u8)
	delete(s.vars, marker)
	return result
}

// stringTaggedEq is stringEqFast: l == r.
//
//	w1 equal:    same w0                    → true
//	             both inline                → false
//	             both not inline            → memequal(w0, w0, w1)
//	             one inline                 → runtime.streqfast
//	w1 differ:   one inline, the other not  → runtime.streqfast
//	             else                       → false
//
// (A string not inline of 1 to 15 bytes, made by unsafe.String or as a
// substring of a longer one, can equal an inline one.)
func (s *state) stringTaggedEq(l, r *ssa.Value) *ssa.Value {
	if l.Op == ssa.OpConstString && r.Op != ssa.OpConstString {
		l, r = r, l
	}
	if r.Op == ssa.OpConstString {
		if c := ssa.ConstStringValue(r); c != "" {
			return s.stringTaggedEqConst(l, r, c)
		}
	}
	boolT := types.Types[types.TBOOL]
	intT := types.Types[types.TINT]
	i64 := types.Types[types.TINT64]
	ptrT := s.f.Config.Types.BytePtr

	lw0 := s.newValue1(ssa.OpStringPtr, ptrT, l)
	rw0 := s.newValue1(ssa.OpStringPtr, ptrT, r)
	lb := s.stringW0Bits(l)
	rb := s.stringW0Bits(r)
	lw1 := s.newValue1(ssa.OpStringWord1, intT, l)
	rw1 := s.newValue1(ssa.OpStringWord1, intT, r)
	zero := s.constInt64(i64, 0)
	cold := func(e *strEq) {
		e.result(s.rtcall(ir.Syms(s.gd).Streqfast, true, []*types.Type{boolT}, l, r)[0])
	}

	e := s.newStrEq()
	same, diff := e.branch(s.newValue2(ssa.OpEq64, boolT, lw1, rw1), ssa.BranchUnknown)

	s.startBlock(same)
	eq, ne := e.branch(s.newValue2(ssa.OpEqPtr, boolT, lw0, rw0), ssa.BranchUnknown)
	s.startBlock(eq)
	e.result(s.constBool(true))
	s.startBlock(ne)
	anyInl, heap := e.branch(s.newValue2(ssa.OpLess64, boolT, s.newValue2(ssa.OpOr64, i64, lb, rb), zero), ssa.BranchUnknown)
	s.startBlock(heap)
	e.result(s.memEqual(lw0, rw0, lw1))
	s.startBlock(anyInl)
	bothInl, mixed := e.branch(s.newValue2(ssa.OpLess64, boolT, s.newValue2(ssa.OpAnd64, i64, lb, rb), zero), ssa.BranchLikely)
	s.startBlock(bothInl)
	e.result(s.constBool(false))
	s.startBlock(mixed)
	cold(e)

	s.startBlock(diff)
	mixed2, no := e.branch(s.newValue2(ssa.OpLess64, boolT, s.newValue2(ssa.OpXor64, i64, lb, rb), zero), ssa.BranchUnlikely)
	s.startBlock(no)
	e.result(s.constBool(false))
	s.startBlock(mixed2)
	cold(e)
	return e.finish()
}

// stringTaggedEqConst is stringTaggedEq against a non-empty constant c
// (r), whose value is in rodata, but which a string made at run time
// holds inline if it is 1 to 15 bytes long: l equals it if its words are
// those c's would be inline, or if l is not inline, has the length of
// c, and its bytes are c's (exact-width loads: no call); a longer one is
// a length compare and memequal.
func (s *state) stringTaggedEqConst(l, r *ssa.Value, c string) *ssa.Value {
	boolT := types.Types[types.TBOOL]
	intT := types.Types[types.TINT]
	i64 := types.Types[types.TINT64]
	ptrT := s.f.Config.Types.BytePtr

	lw0 := s.newValue1(ssa.OpStringPtr, ptrT, l)
	lb := s.stringW0Bits(l)
	lw1 := s.newValue1(ssa.OpStringWord1, intT, l)
	n := s.constInt64(intT, int64(len(c)))
	zero := s.constInt64(i64, 0)
	e := s.newStrEq()

	if int64(len(c)) <= types.StringInlineCap {
		c0, c1 := ssa.StringTaggedWords(c, s.config.BigEndian)
		inl, other := e.branch(s.newValue2(ssa.OpEq64, boolT, lb, s.constInt64(i64, c0)), ssa.BranchUnknown)
		s.startBlock(inl)
		e.result(s.newValue2(ssa.OpEq64, boolT, lw1, s.constInt64(intT, c1)))
		s.startBlock(other)
		heapLen := s.newValue2(ssa.OpAndB, boolT,
			s.newValue2(ssa.OpEq64, boolT, lw1, n),
			s.newValue2(ssa.OpLeq64, boolT, zero, lb))
		heap, no := e.branch(heapLen, ssa.BranchUnlikely)
		s.startBlock(no)
		e.result(s.constBool(false))
		s.startBlock(heap)
		e.result(s.stringBytesEqConst(lw0, c))
		return e.finish()
	}

	heapLen := s.newValue2(ssa.OpAndB, boolT,
		s.newValue2(ssa.OpEq64, boolT, lw1, n),
		s.newValue2(ssa.OpLeq64, boolT, zero, lb))
	ok, no := e.branch(heapLen, ssa.BranchUnknown)
	s.startBlock(no)
	e.result(s.constBool(false))
	s.startBlock(ok)
	rw0 := s.newValue1(ssa.OpStringPtr, ptrT, r)
	if Arch.LinkArch.Family == sys.ARM64 {
		e.result(s.memEqual(lw0, rw0, n))
		return e.finish()
	}
	same, other := e.branch(s.newValue2(ssa.OpEqPtr, boolT, lw0, rw0), ssa.BranchUnknown)
	s.startBlock(same)
	e.result(s.constBool(true))
	s.startBlock(other)
	e.result(s.memEqual(lw0, rw0, n))
	return e.finish()
}
