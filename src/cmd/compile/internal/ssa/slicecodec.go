// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa

import (
	"cmd/compile/internal/types"
	"cmd/internal/obj"
	"cmd/internal/src"
)

// Packed slices (types.SlicePacked, doc/gd/slice16.md).
//
// In memory and in registers a slice is two words: w0, the address of
// its array, and w1, its length and capacity as uint32s (the length in
// the bytes at offset 8, the capacity in those at 12: as a uint64,
// len | cap<<32 little-endian, len<<32 | cap big-endian). A slice whose
// capacity is types.SliceHugeCap or more is huge: w0 is the address of
// an immutable header {ptr, len, cap} in the heap, which the collector
// keeps alive through w0 as it would an array, and w1 is the marker, a
// length of 0 and a capacity of 0xFFFFFFFF, which no other slice has.
//
// In SSA a slice is three values, as in stock Go. Where it is stored or
// passed in registers it is packed (OpSlicePack, OpSlicePackW1), and
// where it is loaded it is unpacked (OpSliceUnpackPtr, Len, Cap). The
// sliceCodec pass expands these ops to their branches:
//
//	unpack: if cap field == 0xFFFFFFFF { ptr, len, cap = *w0 } else { ptr, len, cap = w0, w1's }
//	pack:   if cap < 0xFFFFFFFF { w0, w1 = ptr, len|cap } else { w0, w1 = runtime.slicehdr(ptr, len, cap), marker }

const (
	sliceMarkerCap = 0xFFFFFFFF // the capacity field of a huge slice's w1
	sliceHdrLen    = 8          // the offsets in a huge slice's header
	sliceHdrCap    = 16
)

// sliceMarker returns the marker, w1 of a huge slice.
func sliceMarker(c *Config) int64 {
	if c.BigEndian {
		return sliceMarkerCap
	}
	return -1 << 32 // 0xFFFFFFFF_00000000
}

// slicePack returns the words w0 and w1 of the slice a, and the memory
// after the pack (which may allocate the header of a huge slice).
func slicePack(b *Block, pos src.XPos, a, mem *Value) (w0, w1, m *Value) {
	typs := &b.Func.Config.Types
	p := b.NewValue1(pos, OpSlicePtrUnchecked, a.Type.Elem().PtrTo(), a)
	l := b.NewValue1(pos, OpSliceLen, typs.Int, a)
	c := b.NewValue1(pos, OpSliceCap, typs.Int, a)
	return slicePackParts(b, pos, a.Type, p, l, c, mem)
}

// slicePackParts is slicePack for the slice of pointer p, length l and
// capacity c.
// t is the slice's type: w0 is typed as its pointer (a huge one's is
// its header's address).
func slicePackParts(b *Block, pos src.XPos, t *types.Type, p, l, c, mem *Value) (w0, w1, m *Value) {
	typs := &b.Func.Config.Types
	pt := t.Elem().PtrTo()
	pk := b.NewValue4(pos, OpSlicePack, types.NewTuple(pt, types.TypeMem), p, l, c, mem)
	w0 = b.NewValue1(pos, OpSelect0, pt, pk)
	m = b.NewValue1(pos, OpSelect1, types.TypeMem, pk)
	w1 = b.NewValue3(pos, OpSlicePackW1, typs.UInt64, l, c, w0)
	return
}

// sliceUnpackParts returns the pointer, the length and the capacity of
// the slice of type t whose words are w0 and w1, loaded at mem.
func sliceUnpackParts(b *Block, pos src.XPos, t *types.Type, w0, w1, mem *Value) (p, l, c *Value) {
	typs := &b.Func.Config.Types
	p = b.NewValue3(pos, OpSliceUnpackPtr, t.Elem().PtrTo(), w0, w1, mem)
	l = b.NewValue3(pos, OpSliceUnpackLen, typs.Int, w0, w1, mem)
	c = b.NewValue3(pos, OpSliceUnpackCap, typs.Int, w0, w1, mem)
	return
}

// sliceLoadPacked rewrites v, a load of a slice.
func sliceLoadPacked(v *Value) *Value {
	b, ptr, mem := v.Block, v.Args[0], v.Args[1]
	typs := &b.Func.Config.Types
	w0 := b.NewValue2(v.Pos, OpLoad, v.Type.Elem().PtrTo(), ptr, mem)
	w1 := b.NewValue2(v.Pos, OpLoad, typs.UInt64, b.NewValue1I(v.Pos, OpOffPtr, types.NewPtr(typs.UInt64), 8, ptr), mem)
	p, l, c := sliceUnpackParts(b, v.Pos, v.Type, w0, w1, mem)
	return b.NewValue3(v.Pos, OpSliceMake, v.Type, p, l, c)
}

// sliceStorePacked rewrites v, a store of a slice (SliceMake).
func sliceStorePacked(v *Value) *Value {
	b, dst, x, mem := v.Block, v.Args[0], v.Args[1], v.Args[2]
	typs := &b.Func.Config.Types
	t := v.Aux.(*types.Type)
	w0, w1, mem := slicePackParts(b, v.Pos, t, x.Args[0], x.Args[1], x.Args[2], mem)
	mem = b.NewValue3A(v.Pos, OpStore, types.TypeMem, typs.UInt64, b.NewValue1I(v.Pos, OpOffPtr, types.NewPtr(typs.UInt64), 8, dst), w1, mem)
	st := b.NewValue3A(v.Pos, OpStore, types.TypeMem, t.Elem().PtrTo(), dst, w0, mem)
	st.AuxInt = v.AuxInt // (nothing to publish, if so marked)
	return st
}

// hoistSlicePacks reorders the memory chain from m0 to mem, which the
// marshaling of a call's arguments made, so that its packs come first:
// a pack may call the runtime, which would write over the arguments
// already stored where the call's go. It returns the new last memory.
func hoistSlicePacks(m0, mem *Value) *Value {
	var chain []*Value
	packs := 0
	for m := mem; m != m0; {
		chain = append(chain, m)
		if m.Op == OpSelect1 && m.Args[0].Op == OpSlicePack {
			packs++
			m = m.Args[0].Args[3]
		} else {
			m = m.MemoryArg()
		}
		if m == nil {
			return mem
		}
	}
	if packs == 0 || packs == len(chain) {
		return mem
	}
	setMem := func(v, m *Value) {
		if v.Op == OpSelect1 {
			v.Args[0].SetArg(3, m)
			return
		}
		for i, a := range v.Args {
			if a.Type.IsMemory() {
				v.SetArg(i, m)
				return
			}
		}
		v.Fatalf("no memory argument: %v", v.LongString())
	}
	cur := m0
	for i := len(chain) - 1; i >= 0; i-- {
		if v := chain[i]; v.Op == OpSelect1 && v.Args[0].Op == OpSlicePack {
			setMem(v, cur)
			cur = v
		}
	}
	for i := len(chain) - 1; i >= 0; i-- {
		if v := chain[i]; !(v.Op == OpSelect1 && v.Args[0].Op == OpSlicePack) {
			setMem(v, cur)
			cur = v
		}
	}
	return cur
}

// sliceCodec expands the ops that pack and unpack slices to the branches
// of a huge slice.
func sliceCodec(f *Func) {
	if !f.Config.slicePacked() {
		return
	}
	selects := map[ID][2]*Value{} // a pack's Select0 and Select1
	w1s := map[ID][]*Value{}      // the packs of w1 of a pack's Select0
	todo := false
	for _, b := range f.Blocks {
		for _, v := range b.Values {
			switch v.Op {
			case OpSlicePack, OpSlicePackW1, OpSliceUnpackPtr, OpSliceUnpackLen, OpSliceUnpackCap:
				todo = true
			case OpSelect0, OpSelect1:
				if a := v.Args[0]; a.Op == OpSlicePack {
					s := selects[a.ID]
					s[v.Op-OpSelect0] = v
					selects[a.ID] = s
				}
			}
			if v.Op == OpSlicePackW1 {
				w1s[v.Args[2].ID] = append(w1s[v.Args[2].ID], v)
			}
		}
	}
	if !todo {
		return
	}
	c := sliceCodecState{f: f, selects: selects, w1s: w1s}
	// The packs that need no branch first: a pack of what is unpacked
	// is no longer one once the unpack is expanded.
	for _, b := range f.Blocks {
	again:
		for _, v := range b.Values {
			if c.trivialPack(v) {
				goto again // (v is out of b.Values)
			}
		}
	}
	// The unpacks that are now dead (of what those packed) need no
	// branch either, and the copies the packs left (of memory, between
	// stores whose write barriers go together) are not to stay.
	copyelim(f)
	for _, b := range f.Blocks {
		i := 0
		for _, v := range b.Values {
			if (v.Op == OpSliceUnpackPtr || v.Op == OpSliceUnpackLen || v.Op == OpSliceUnpackCap || v.Op == OpCopy) && v.Uses == 0 {
				v.resetArgs()
				v.reset(OpInvalid)
				continue
			}
			b.Values[i] = v
			i++
		}
		clear(b.Values[i:])
		b.Values = b.Values[:i]
	}
	for _, name := range f.Names {
		values := f.NamedValues[*name]
		j := 0
		for _, v := range values {
			if v.Op != OpInvalid {
				values[j] = v
				j++
			}
		}
		clear(values[j:])
		f.NamedValues[*name] = values[:j]
	}
	for i, n := 0, len(f.Blocks); i < n; i++ {
		c.block(f.Blocks[i])
	}
	// The packs of w1 with no pack of w0 next to them.
	var lone []*Value
	for _, b := range f.Blocks {
		for _, v := range b.Values {
			if v.Op == OpSlicePackW1 {
				lone = append(lone, v)
			}
		}
	}
	for _, v := range lone {
		c.packW1(v)
	}
}

type sliceCodecState struct {
	f       *Func
	selects map[ID][2]*Value
	w1s     map[ID][]*Value
	sp      *Value
}

func isSliceCodec(v *Value) bool {
	switch v.Op {
	case OpSlicePack, OpSliceUnpackPtr, OpSliceUnpackLen, OpSliceUnpackCap:
		return true
	}
	return false
}

// block expands the packs and unpacks of b, splitting it at each.
func (c *sliceCodecState) block(b *Block) {
	work := []*Block{b}
	for len(work) > 0 {
		b := work[len(work)-1]
		work = work[:len(work)-1]
	again:
		for _, v := range b.Values {
			if c.trivialPack(v) {
				goto again // (v is out of b.Values)
			}
			if isSliceCodec(v) {
				work = append(work, b, c.split(b, v))
				break
			}
		}
	}
}

// trivialPack expands v, if it is a pack that needs no branch, and
// reports whether it did: the pack of a slice of a constant capacity that
// fits, or of what was unpacked from words w0 and w1, which are its words.
func (c *sliceCodecState) trivialPack(v *Value) bool {
	if v.Op != OpSlicePack {
		return false
	}
	p, l, cp, mem := v.Args[0], v.Args[1], v.Args[2], v.Args[3]
	var w0, w1 *Value
	switch {
	case cp.Op == OpConst64 && cp.AuxInt >= 0 && cp.AuxInt < sliceMarkerCap:
		w0 = p
	case p.Op == OpSliceUnpackPtr && l.Op == OpSliceUnpackLen && cp.Op == OpSliceUnpackCap &&
		p.Args[0] == l.Args[0] && p.Args[0] == cp.Args[0] && p.Args[1] == l.Args[1] && p.Args[1] == cp.Args[1]:
		w0, w1 = p.Args[0], p.Args[1]
	default:
		return false
	}
	sel := c.selects[v.ID]
	if s := sel[0]; s != nil {
		for _, u := range c.w1s[s.ID] {
			if u.Op != OpSlicePackW1 || u.Args[2] != s {
				continue
			}
			if w1 != nil {
				u.copyOf(w1)
			} else {
				u.SetArg(2, p) // (packW1 expands it)
			}
		}
		s.copyOf(w0)
	}
	if s := sel[1]; s != nil {
		s.copyOf(mem)
	}
	v.resetArgs()
	v.reset(OpInvalid)
	removeValue(v)
	return true
}

// removeValue removes v from its block.
func removeValue(v *Value) {
	b := v.Block
	for i, w := range b.Values {
		if w == v {
			b.Values = append(b.Values[:i], b.Values[i+1:]...)
			return
		}
	}
}

// split splits b at v, a pack or an unpack, which it expands: what
// depends on v, and for an unpack the memory after v's, goes to a new
// block, the end of v's branches, which it returns.
func (c *sliceCodecState) split(b *Block, v *Value) *Block {
	f := c.f
	var roots []*Value
	if v.Op == OpSlicePack {
		// (Its w1 depends on its w0.)
		roots = append(roots, v)
	} else {
		m := v.Args[2]
		for _, u := range b.Values {
			if isSliceCodec(u) && u.Op != OpSlicePack && u.Args[0] == v.Args[0] && u.Args[1] == v.Args[1] && u.Args[2] == m {
				roots = append(roots, u)
			} else if u.Op != OpPhi && u.Type.IsMemory() && u.MemoryArg() == m {
				roots = append(roots, u) // the stores after v's memory
			}
		}
	}
	// What depends on the roots, in b.
	users := map[*Value][]*Value{}
	for _, u := range b.Values {
		if u.Op == OpPhi {
			continue
		}
		for _, a := range u.Args {
			if a.Block == b {
				users[a] = append(users[a], u)
			}
		}
	}
	moved := map[*Value]bool{}
	for len(roots) > 0 {
		u := roots[len(roots)-1]
		roots = roots[:len(roots)-1]
		if moved[u] {
			continue
		}
		moved[u] = true
		roots = append(roots, users[u]...)
	}
	var before, after []*Value
	for _, u := range b.Values {
		if moved[u] {
			after = append(after, u)
		} else {
			before = append(before, u)
		}
	}

	bSlow := f.NewBlock(BlockPlain)
	bEnd := f.NewBlock(b.Kind)
	bSlow.Pos = v.Pos
	bEnd.Pos = b.Pos
	bEnd.CopyControls(b)
	bEnd.Aux, bEnd.AuxInt = b.Aux, b.AuxInt
	bEnd.Likely = b.Likely
	for _, e := range b.Succs {
		bEnd.Succs = append(bEnd.Succs, e)
		e.b.Preds[e.i].b = bEnd
	}
	b.Succs = b.Succs[:0]
	b.Aux, b.AuxInt = nil, 0
	b.Kind = BlockIf
	b.Values = before

	var phis []*Value
	if v.Op == OpSlicePack {
		phis, after = c.pack(b, bSlow, bEnd, v, after)
	} else {
		phis, after = c.unpack(b, bSlow, bEnd, v, after)
	}
	bEnd.Values = append(bEnd.Values, phis...)
	for _, w := range after {
		w.Block = bEnd
	}
	bEnd.Values = append(bEnd.Values, after...)
	return bEnd
}

// toPhi makes u, a value of b or after, a phi of bEnd of a0 (from b) and
// a1 (from bSlow).
func toPhi(u, a0, a1 *Value, bEnd *Block) *Value {
	if u.Block != bEnd {
		blk := u.Block
		for j, w := range blk.Values {
			if w == u {
				blk.Values = append(blk.Values[:j], blk.Values[j+1:]...)
				break
			}
		}
	}
	u.reset(OpPhi)
	u.AddArg2(a0, a1)
	u.Aux, u.AuxInt = nil, 0
	u.Block = bEnd
	return u
}

// remove returns after without u.
func remove(after []*Value, u *Value) []*Value {
	for j, w := range after {
		if w == u {
			return append(after[:j:j], after[j+1:]...)
		}
	}
	return after
}

func (c *sliceCodecState) unpack(b, bSlow, bEnd *Block, v *Value, after []*Value) (phis, rest []*Value) {
	f := c.f
	typs := &f.Config.Types
	pos := v.Pos
	w0, w1, mem := v.Args[0], v.Args[1], v.Args[2]
	if w1.Op == OpArgIntReg || w1.Op == OpArg {
		// A parameter's: unpacked on entry.
		pos = f.fe.Func().Pos().WithNotStmt()
	}
	var group []*Value
	for _, u := range after {
		if isSliceCodec(u) && u.Op != OpSlicePack && u.Args[0] == w0 && u.Args[1] == w1 && u.Args[2] == mem {
			group = append(group, u)
		} else {
			rest = append(rest, u)
		}
	}

	// The fields of w1.
	c32 := f.ConstInt64(typs.UInt64, 32)
	lo := b.NewValue1(pos, OpZeroExt32to64, typs.UInt64, b.NewValue1(pos, OpTrunc64to32, typs.UInt32, w1))
	hi := b.NewValue2(pos, OpRsh64Ux64, typs.UInt64, w1, c32)
	lenF, capF := lo, hi
	if f.Config.BigEndian {
		lenF, capF = hi, lo
	}
	huge := b.NewValue2(pos, OpEq64, typs.Bool, capF, f.ConstInt64(typs.UInt64, sliceMarkerCap))
	b.SetControl(huge)
	b.Likely = BranchUnlikely
	b.AddEdgeTo(bSlow)
	b.AddEdgeTo(bEnd)
	bSlow.AddEdgeTo(bEnd)

	// A huge slice: its header.
	var ptrS, lenS, capS *Value
	for _, u := range group {
		var a0, a1 *Value
		switch u.Op {
		case OpSliceUnpackPtr:
			if ptrS == nil {
				ptrS = bSlow.NewValue2(pos, OpLoad, u.Type, w0, mem)
			}
			a0, a1 = w0, ptrS
		case OpSliceUnpackLen:
			if lenS == nil {
				lenS = bSlow.NewValue2(pos, OpLoad, typs.Int, bSlow.NewValue1I(pos, OpOffPtr, typs.IntPtr, sliceHdrLen, w0), mem)
			}
			a0, a1 = lenF, lenS
		case OpSliceUnpackCap:
			if capS == nil {
				capS = bSlow.NewValue2(pos, OpLoad, typs.Int, bSlow.NewValue1I(pos, OpOffPtr, typs.IntPtr, sliceHdrCap, w0), mem)
			}
			a0, a1 = capF, capS
		}
		phis = append(phis, toPhi(u, a0, a1, bEnd))
	}
	return phis, rest
}

func (c *sliceCodecState) pack(b, bSlow, bEnd *Block, v *Value, after []*Value) (phis, rest []*Value) {
	f := c.f
	typs := &f.Config.Types
	pos := v.Pos
	p, l, cp, mem := v.Args[0], v.Args[1], v.Args[2], v.Args[3]
	sel := c.selects[v.ID]
	for _, u := range after {
		if u == v || u.Op == OpSlicePackW1 && sel[0] != nil && u.Args[2] == sel[0] || u == sel[0] || u == sel[1] {
			continue
		}
		rest = append(rest, u)
	}

	c32 := f.ConstInt64(typs.UInt64, 32)
	fits := b.NewValue2(pos, OpLess64U, typs.Bool, cp, f.ConstInt64(typs.UInt64, sliceMarkerCap))
	lenW, capW := l, b.NewValue2(pos, OpLsh64x64, typs.UInt64, cp, c32)
	if f.Config.BigEndian {
		lenW, capW = b.NewValue2(pos, OpLsh64x64, typs.UInt64, l, c32), cp
	}
	w1F := b.NewValue2(pos, OpOr64, typs.UInt64, lenW, capW)
	b.SetControl(fits)
	b.Likely = BranchLikely
	b.AddEdgeTo(bEnd)
	b.AddEdgeTo(bSlow)
	bSlow.AddEdgeTo(bEnd)

	// A huge slice: a new header.
	fn := f.fe.Syslook("slicehdr")
	if pt := sel0Type(v); pt.IsPtr() && pt.Elem().NotInHeap() {
		// No write barrier for w0: it may be no header (it throws).
		fn = f.fe.Syslook("slicehdrnotinheap")
	}
	hdr, memS := c.call(bSlow, pos, fn, mem, p, l, cp)

	for _, u := range after {
		if u.Op == OpSlicePackW1 && sel[0] != nil && u.Args[2] == sel[0] {
			phis = append(phis, toPhi(u, w1F, f.ConstInt64(typs.UInt64, sliceMarker(f.Config)), bEnd))
		}
	}
	if s := sel[0]; s != nil {
		phis = append(phis, toPhi(s, p, hdr, bEnd))
	}
	if s := sel[1]; s != nil {
		phis = append(phis, toPhi(s, mem, memS, bEnd))
	}
	v.resetArgs()
	v.reset(OpInvalid)
	return phis, rest
}

// sel0Type is the type of w0 of the pack v: its slice's pointer.
func sel0Type(v *Value) *types.Type {
	return v.Type.FieldType(0)
}

// call calls fn(p, l, c) in b at memory mem, a function of the runtime
// that returns a pointer; it returns the pointer and the memory after.
func (c *sliceCodecState) call(b *Block, pos src.XPos, fn *obj.LSym, mem, p, l, cp *Value) (res, m *Value) {
	f := c.f
	config := f.Config
	typs := &config.Types
	if c.sp == nil {
		c.sp, _ = f.spSb()
	}
	argTypes := []*types.Type{typs.BytePtr, typs.Int, typs.Int}
	abi := f.ABIDefault.ABIAnalyzeTypes(argTypes, []*types.Type{typs.BytePtr})
	aux := StaticAuxCall(fn, abi)
	args := []*Value{p, l, cp}
	inRegs := f.ABIDefault == f.ABI1 && len(config.intParamRegs) >= 3
	if !inRegs {
		for i, a := range args {
			off := aux.OffsetOfArg(int64(i)) // (from SP: FixedFrameSize is in it)
			addr := b.NewValue1I(pos, OpOffPtr, argTypes[i].PtrTo(), off, c.sp)
			mem = b.NewValue3A(pos, OpStore, types.TypeMem, argTypes[i], addr, a, mem)
		}
		args = args[:0]
	}
	args = append(args, mem)
	var call *Value
	if inRegs {
		call = b.NewValue0A(pos, OpStaticCall, types.NewResults([]*types.Type{typs.BytePtr, types.TypeMem}), aux)
		call.AddArgs(args...)
		res = b.NewValue1I(pos, OpSelectN, typs.BytePtr, 0, call)
		m = b.NewValue1I(pos, OpSelectN, types.TypeMem, 1, call)
	} else {
		call = b.NewValue0A(pos, OpStaticCall, types.TypeResultMem, aux)
		call.AddArgs(args...)
		m = b.NewValue1I(pos, OpSelectN, types.TypeMem, 0, call)
		off := aux.OffsetOfResult(0)
		res = b.NewValue2(pos, OpLoad, typs.BytePtr, b.NewValue1I(pos, OpOffPtr, typs.BytePtrPtr, off, c.sp), m)
	}
	call.AuxInt = abi.ArgWidth()
	return res, m
}

// packW1 expands v, a pack of w1 with no pack of w0 next to it, without
// a branch: the marker if the capacity is too large, else the fields.
func (c *sliceCodecState) packW1(v *Value) {
	f := c.f
	typs := &f.Config.Types
	b, pos := v.Block, v.Pos
	l, cp := v.Args[0], v.Args[1]
	c32 := f.ConstInt64(typs.UInt64, 32)
	lenW, capW := l, b.NewValue2(pos, OpLsh64x64, typs.UInt64, cp, c32)
	if f.Config.BigEndian {
		lenW, capW = b.NewValue2(pos, OpLsh64x64, typs.UInt64, l, c32), cp
	}
	w1 := b.NewValue2(pos, OpOr64, typs.UInt64, lenW, capW)
	// mask = -(cap >= 0xFFFFFFFF)
	huge := b.NewValue2(pos, OpLeq64U, typs.Bool, f.ConstInt64(typs.UInt64, sliceMarkerCap), cp)
	mask := b.NewValue1(pos, OpNeg64, typs.UInt64, b.NewValue1(pos, OpZeroExt8to64, typs.UInt64, b.NewValue1(pos, OpCvtBoolToUint8, typs.UInt8, huge)))
	keep := b.NewValue2(pos, OpAnd64, typs.UInt64, w1, b.NewValue1(pos, OpCom64, typs.UInt64, mask))
	marker := b.NewValue2(pos, OpAnd64, typs.UInt64, f.ConstInt64(typs.UInt64, sliceMarker(f.Config)), mask)
	v.reset(OpOr64)
	v.AddArg2(keep, marker)
}
