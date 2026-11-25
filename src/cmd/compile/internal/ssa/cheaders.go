// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa

import (
	"cmd/compile/internal/ir"
	"cmd/compile/internal/types"
	"cmd/internal/src"
	"sort"
)

// The checks of a C23Slow.
const (
	CSlowArith       = iota // arg0 is the result of arithmetic on arg1: runtime.checkptrArithmetic
	CSlowConv               // arg0 is converted to a pointer to a type: runtime.checkptrAlignmentC, checkptrView
	CSlowConvScalar         // the same, to a type of one word or less that has no pointer and is not bytes
	CSlowConvUntyped        // the same, to unsafe.Pointer or a pointer to what has no size
	CSlowConvClaim          // the same, to any other type: the runtime's, if arg0 is in an object of C's
	CSlowStoreByte          // bytes are stored at arg0 through a pointer to bytes: runtime.c23ByteStore
	CSlowStorePtr           // a pointer is stored at arg0 through a pointer to a pointer to anything: runtime.c23PtrStore
)

// CStoreWords is the number of words of the largest store of bytes
// whose words' classes are looked at in the compiled code.
const CStoreWords = 16

// CHeaders reports whether the checks of C's pointers, and the stores of
// C's that the runtime is told of, are made in the compiled code where
// it knows what the pointers point into (cheaders), with a call only for
// what it does not let through.
func (c *Config) CHeaders() bool {
	return c.PtrSize == 8 && c.gd.Flag.N == 0 && c.gd.Debug.CHeaders == 0
}

// CClassScalar is the class of a word of C's that is claimed for no
// pointer: runtime's cScalar.
const CClassScalar = 1

// CConvWords is the number of words of the largest type whose
// conversion in an object of C's is compared with the classes of the
// words in the compiled code.
const CConvWords = 16

// CConvMax is the size of the largest type that a C23Slow of a
// conversion is for: runtime's cConvMax.
const CConvMax = 4096

// CEnd is the bit of a pointer of C's to the end of its object, or to a
// type that does not fit in what is left of it: runtime's cEnd. No
// address has it, so a load or a store through the pointer faults.
const CEnd = 1 << 54

// CSlowAux returns the auxint of a C23Slow: the check, the mask of the
// alignment that the pointer is to have, and the size of what it is
// converted to.
func CSlowAux(check int, alignMask, size int64) int64 {
	return int64(check) | alignMask<<8 | size<<16
}

// cheaders makes of each C23Slow, which ssagen puts where the runtime
// checked a pointer of C's or stored one for it (doc/gd/cc.md), what
// the compiled code can find out for itself.
//
// What a pointer points into does not change while that is alive: the
// bounds of the object, and where the bits of its words are if it is one
// of C's. The runtime says them once for a pointer (c23Header), and they
// hold for every pointer that is made of it by an offset or an index,
// which a check has kept in the object or a comparison here will find
// out of it. So the pointers of a function are taken back to the ones
// they are made of, their roots; a root is looked up once, before a
// loop if it is from before the loop, or where it is first checked if
// more checks follow; and each C23Slow is then a comparison with what
// was looked up, true only for what the runtime is still to see. A
// C23Slow whose root is not worth a call of its own is true, and its
// check is the call that it was before.
//
// A root that is the address of a variable of the frame needs no call:
// its size is known here.
//
// This runs before anything has moved the blocks that ssagen made: a
// C23Slow is the last thing its block does before it branches on it,
// and the memory after both branches is a phi.
func cheaders(f *Func) {
	var sites []*Value
	for _, b := range f.Blocks {
		for _, v := range b.Values {
			if v.Op != OpC23Slow {
				continue
			}
			sites = append(sites, v)
		}
	}
	if len(sites) == 0 {
		return
	}
	if f.Config.PtrSize != 8 {
		for _, v := range sites {
			cSlowAlways(v)
		}
		return
	}

	root := cRoots(f)

	sdom := f.Sdom()
	ln := f.loopnest()
	po := f.postorder()
	order := make([]int32, f.NumBlocks())
	for i, b := range po {
		order[b.ID] = int32(len(po) - i)
	}
	sort.SliceStable(sites, func(i, j int) bool {
		return order[sites[i].Block.ID] < order[sites[j].Block.ID]
	})
	byRoot := map[*Value][]*Value{}
	for _, v := range sites {
		if r := root(v.Args[1]); r != nil {
			byRoot[r] = append(byRoot[r], v)
		}
	}

	uintptrT := f.Config.Types.Uintptr
	boolT := f.Config.Types.Bool

	// The calls of runtime.c23Header, by root.
	type header struct {
		call *Value
		sel  [6]*Value
		fits map[int64]*Value // by the size of a type: see fit
	}
	headers := map[*Value][]*header{}
	var aux *AuxCall
	lookup := func(b *Block, pos src.XPos, r, mem *Value) (*header, *Value) {
		if aux == nil {
			results := make([]*types.Type, 6)
			for i := range results {
				results[i] = uintptrT
			}
			aux = StaticAuxCall(f.fe.Syslook("c23Header"), f.ABIDefault.ABIAnalyzeTypes([]*types.Type{types.Types[types.TUNSAFEPTR]}, results))
		}
		call := b.NewValue0A(pos, OpStaticLECall, aux.LateExpansionResultType(), aux)
		call.AddArgs(r, mem)
		h := &header{call: call}
		headers[r] = append(headers[r], h)
		return h, b.NewValue1I(pos, OpSelectN, types.TypeMem, 6, call)
	}
	result := func(h *header, i int) *Value {
		if h.sel[i] == nil {
			h.sel[i] = h.call.Block.NewValue1I(h.call.Pos, OpSelectN, uintptrT, int64(i), h.call)
		}
		return h.sel[i]
	}

	// fit returns the offset from which a value of size bytes does
	// not fit in what h is of: max(m+1, size) - size, where m is the
	// header's third result, for a conversion, or its second, for a
	// store, which is what was converted.
	fit := func(h *header, size int64, store bool) *Value {
		key := size << 1
		if store {
			key |= 1
		}
		if v := h.fits[key]; v != nil {
			return v
		}
		b, pos := h.call.Block, h.call.Pos
		m := result(h, 2)
		if store {
			m = result(h, 1)
		}
		c := f.ConstInt64(uintptrT, size)
		// all is all ones if size <= m, and 0 otherwise.
		all := b.NewValue2(pos, OpSub64, uintptrT, f.ConstInt64(uintptrT, 0), b.NewValue1(pos, OpZeroExt8to64, uintptrT, b.NewValue1(pos, OpCvtBoolToUint8, f.Config.Types.UInt8, b.NewValue2(pos, OpLeq64U, boolT, c, m))))
		v := b.NewValue2(pos, OpAnd64, uintptrT, b.NewValue2(pos, OpAdd64, uintptrT, b.NewValue2(pos, OpSub64, uintptrT, m, c), f.ConstInt64(uintptrT, 1)), all)
		if h.fits == nil {
			h.fits = map[int64]*Value{}
		}
		h.fits[key] = v
		return v
	}

	inLoop := func(b *Block, l *loop) bool {
		for x := ln.b2l[b.ID]; x != nil; x = x.outer {
			if x == l {
				return true
			}
		}
		return false
	}
	// before returns the block before the outermost loop that b is in
	// and r is not from, if the loop has one that only leads to it and
	// r is there to look up: with the phi of the loop's memory, and
	// the argument of it that is the block's.
	before := func(r *Value, b *Block) (pre *Block, phi *Value, arg int) {
		if ln.hasIrreducible {
			return nil, nil, 0
		}
		for l := ln.b2l[b.ID]; l != nil && !inLoop(r.Block, l); l = l.outer {
			var p *Block
			n, i := 0, 0
			for j, e := range l.header.Preds {
				if !inLoop(e.b, l) {
					p, i = e.b, j
					n++
				}
			}
			if n != 1 || len(p.Succs) != 1 || !sdom.IsAncestorEq(r.Block, p) {
				continue
			}
			for _, v := range l.header.Values {
				if v.Op == OpPhi && v.Type.IsMemory() {
					pre, phi, arg = p, v, i
				}
			}
		}
		return pre, phi, arg
	}

	// The uses of each memory, for a call that is put where a C23Slow
	// is: made when the first one is.
	type use struct {
		v *Value
		i int
	}
	var users map[*Value][]use
	// anchor looks r up in the block of the C23Slow v, in the memory
	// that v is in: everything that the block leads to uses the
	// call's memory after.
	anchor := func(v, r *Value) *header {
		if users == nil {
			users = map[*Value][]use{}
			for _, b := range f.Blocks {
				for _, u := range b.Values {
					for i, a := range u.Args {
						if a.Type.IsMemory() {
							users[a] = append(users[a], use{u, i})
						}
					}
				}
			}
		}
		b, mem := v.Block, v.Args[2]
		// What r is made of in this block stays where it is.
		deps := map[*Value]bool{}
		var mark func(x *Value)
		mark = func(x *Value) {
			if x.Block != b || x.Op == OpPhi || deps[x] {
				return
			}
			deps[x] = true
			for _, a := range x.Args {
				mark(a)
			}
		}
		mark(r)
		h, after := lookup(b, v.Pos, r, mem)
		var stay []use
		for _, u := range users[mem] {
			at := u.v.Block
			if u.v.Op == OpPhi {
				at = at.Preds[u.i].b
			}
			if !sdom.IsAncestorEq(b, at) || deps[u.v] {
				stay = append(stay, u)
				continue
			}
			u.v.SetArg(u.i, after)
			users[after] = append(users[after], u)
		}
		users[mem] = append(stay, use{h.call, 1})
		return h
	}

	for _, v := range sites {
		b := v.Block
		r := root(v.Args[1])
		if r == nil || !r.Type.IsPtrShaped() || r.Op == OpConstNil {
			cSlowAlways(v)
			continue
		}
		if r.Op == OpLocalAddr {
			cSlowVariable(f, v, r)
			continue
		}
		if !sdom.IsAncestorEq(r.Block, b) {
			cSlowAlways(v)
			continue
		}
		var h *header
		for _, x := range headers[r] {
			if x.call.Block == b || sdom.IsAncestorEq(x.call.Block, b) {
				h = x
				break
			}
		}
		if h == nil {
			if pre, phi, arg := before(r, b); pre != nil {
				var mem *Value
				h, mem = lookup(pre, v.Pos, r, phi.Args[arg])
				phi.SetArg(arg, mem)
				if users != nil {
					users[mem] = append(users[mem], use{phi, arg})
					users[h.call.Args[1]] = append(users[h.call.Args[1]], use{h.call, 1})
				}
			}
		}
		if h == nil {
			// Worth a call here if another check follows.
			for _, o := range byRoot[r] {
				if o != v && sdom.IsAncestorEq(b, o.Block) {
					h = anchor(v, r)
					break
				}
			}
		}
		if h == nil {
			cSlowAlways(v)
			continue
		}

		pos, mem := v.Pos, v.Args[2]
		check := v.AuxInt & 0xff
		mask := v.AuxInt >> 8 & 0xff
		q := b.NewValue2(pos, OpConvert, uintptrT, v.Args[0], mem)
		// off is where q is in what r points into: r may be a
		// pointer to its end, with the bit of one. (A q that has
		// the bit is out of it, and the runtime's.)
		ra := b.NewValue2(pos, OpAnd64, uintptrT, b.NewValue2(pos, OpConvert, uintptrT, r, mem), f.ConstInt64(uintptrT, ^CEnd))
		off := b.NewValue2(pos, OpAdd64, uintptrT, b.NewValue2(pos, OpSub64, uintptrT, q, ra), result(h, 0))
		switch check {
		case CSlowArith:
			v.reset(OpLeq64U)
			v.AddArg2(result(h, 1), off)
		case CSlowConv, CSlowConvScalar, CSlowConvUntyped, CSlowConvClaim:
			out := b.NewValue2(pos, OpLeq64U, boolT, fit(h, v.AuxInt>>16, false), off)
			slow := out
			if mask != 0 {
				odd := b.NewValue2(pos, OpNeq64, boolT, b.NewValue2(pos, OpAnd64, uintptrT, q, f.ConstInt64(uintptrT, mask)), f.ConstInt64(uintptrT, 0))
				slow = b.NewValue2(pos, OpOrB, boolT, slow, odd)
			}
			if check != CSlowConv {
				// The words of an object of C's are what they
				// were claimed for (runtime/cclaim.go): the
				// conversion is the runtime's unless the word is
				// claimed for what the type has there. c is all
				// ones for an object of C's, whose shift is 3.
				c := b.NewValue2(pos, OpSub64, uintptrT, b.NewValue2(pos, OpAnd64, uintptrT, b.NewValue2(pos, OpRsh64Ux64, uintptrT, result(h, 4), f.ConstInt64(uintptrT, 2)), f.ConstInt64(uintptrT, 1)), f.ConstInt64(uintptrT, 1))
				differ := b.NewValue2(pos, OpNeq64, boolT, c, f.ConstInt64(uintptrT, 0))
				if check != CSlowConvClaim {
					// The word, or the first one if q is not in
					// the object: its class is then not looked
					// at, but it is loaded.
					w := b.NewValue2(pos, OpRsh64Ux64, uintptrT, b.NewValue2(pos, OpAdd64, uintptrT, off, result(h, 3)), result(h, 4))
					w.AuxInt = 1 // the shift is less than 64
					in := b.NewValue2(pos, OpSub64, uintptrT, b.NewValue1(pos, OpZeroExt8to64, uintptrT, b.NewValue1(pos, OpCvtBoolToUint8, f.Config.Types.UInt8, out)), f.ConstInt64(uintptrT, 1))
					w = b.NewValue2(pos, OpAnd64, uintptrT, w, in)
					at := b.NewValue2(pos, OpAdd64, uintptrT, result(h, 5), b.NewValue2(pos, OpLsh64x64, uintptrT, b.NewValue2(pos, OpRsh64Ux64, uintptrT, w, f.ConstInt64(uintptrT, 4)), f.ConstInt64(uintptrT, 2)))
					u32T, u64T := f.Config.Types.UInt32, f.Config.Types.UInt64
					// The classes of the 16 words that w is one
					// of, and of the 16 after them: a type of
					// CConvWords words or less has all of its
					// words there.
					lo := b.NewValue2(pos, OpLoad, u32T, b.NewValue2(pos, OpConvert, u32T.PtrTo(), at, mem), mem)
					hi := b.NewValue2(pos, OpLoad, u32T, b.NewValue2(pos, OpConvert, u32T.PtrTo(), b.NewValue2(pos, OpAdd64, uintptrT, at, f.ConstInt64(uintptrT, 4)), mem), mem)
					both := b.NewValue2(pos, OpOr64, u64T, b.NewValue1(pos, OpZeroExt32to64, u64T, lo), b.NewValue2(pos, OpLsh64x64, u64T, b.NewValue1(pos, OpZeroExt32to64, u64T, hi), f.ConstInt64(uintptrT, 32)))
					shift := b.NewValue2(pos, OpLsh64x64, uintptrT, b.NewValue2(pos, OpAnd64, uintptrT, w, f.ConstInt64(uintptrT, 15)), f.ConstInt64(uintptrT, 1))
					class := b.NewValue2(pos, OpRsh64Ux64, u64T, both, shift)
					class.AuxInt = 1
					// The bits of the classes that are to be as
					// want has them: both for no pointer, in every
					// word of the type, the high one for a pointer
					// to anything.
					size := v.AuxInt >> 16
					words := (size + 7) / 8
					bits := int64(uint64(1)<<(2*words) - 1)
					want := 0x5555555555555555 * CClassScalar & bits
					if check == CSlowConvUntyped {
						bits, want = 2, 2
					}
					class = b.NewValue2(pos, OpXor64, u64T, b.NewValue2(pos, OpAnd64, u64T, class, f.ConstInt64(u64T, bits)), f.ConstInt64(u64T, want))
					class = b.NewValue2(pos, OpAnd64, u64T, class, c)
					differ = b.NewValue2(pos, OpNeq64, boolT, class, f.ConstInt64(u64T, 0))
					if check == CSlowConvScalar && size > 1 {
						// What is in a word more than its size
						// has is the runtime's.
						more := b.NewValue2(pos, OpLess64U, boolT, f.ConstInt64(uintptrT, words*8-size), b.NewValue2(pos, OpAnd64, uintptrT, q, f.ConstInt64(uintptrT, 7)))
						differ = b.NewValue2(pos, OpOrB, boolT, differ, more)
					}
				}
				slow = b.NewValue2(pos, OpOrB, boolT, slow, differ)
			}
			v.copyOf(slow)
		case CSlowStoreByte, CSlowStorePtr:
			// The store is the runtime's unless what is stored is
			// whole in what r points into, and the words it is
			// stored in have the class that lets it through: none
			// of a pointer for bytes, that of a pointer to
			// anything for one (runtime/cclaim.go's c23ByteStore).
			size := v.AuxInt >> 16
			out := b.NewValue2(pos, OpLeq64U, boolT, fit(h, size, true), off)
			u32T, u64T := f.Config.Types.UInt32, f.Config.Types.UInt64
			w := b.NewValue2(pos, OpRsh64Ux64, uintptrT, b.NewValue2(pos, OpAdd64, uintptrT, off, result(h, 3)), result(h, 4))
			w.AuxInt = 1 // the shift is less than 64
			in := b.NewValue2(pos, OpSub64, uintptrT, b.NewValue1(pos, OpZeroExt8to64, uintptrT, b.NewValue1(pos, OpCvtBoolToUint8, f.Config.Types.UInt8, out)), f.ConstInt64(uintptrT, 1))
			w = b.NewValue2(pos, OpAnd64, uintptrT, w, in)
			at := b.NewValue2(pos, OpAdd64, uintptrT, result(h, 5), b.NewValue2(pos, OpLsh64x64, uintptrT, b.NewValue2(pos, OpRsh64Ux64, uintptrT, w, f.ConstInt64(uintptrT, 4)), f.ConstInt64(uintptrT, 2)))
			if check == CSlowStorePtr {
				// (What is not C's has the classes for this
				// store in the uint32 after.)
				at = b.NewValue2(pos, OpAdd64, uintptrT, at, b.NewValue2(pos, OpAnd64, uintptrT, b.NewValue2(pos, OpRsh64Ux64, uintptrT, result(h, 4), f.ConstInt64(uintptrT, 3)), f.ConstInt64(uintptrT, 4)))
			}
			lo := b.NewValue2(pos, OpLoad, u32T, b.NewValue2(pos, OpConvert, u32T.PtrTo(), at, mem), mem)
			both := b.NewValue1(pos, OpZeroExt32to64, u64T, lo)
			if check == CSlowStoreByte && size > 1 {
				// (One word's class is in the one uint32.)
				hi := b.NewValue2(pos, OpLoad, u32T, b.NewValue2(pos, OpConvert, u32T.PtrTo(), b.NewValue2(pos, OpAdd64, uintptrT, at, f.ConstInt64(uintptrT, 4)), mem), mem)
				both = b.NewValue2(pos, OpOr64, u64T, both, b.NewValue2(pos, OpLsh64x64, u64T, b.NewValue1(pos, OpZeroExt32to64, u64T, hi), f.ConstInt64(uintptrT, 32)))
			}
			shift := b.NewValue2(pos, OpLsh64x64, uintptrT, b.NewValue2(pos, OpAnd64, uintptrT, w, f.ConstInt64(uintptrT, 15)), f.ConstInt64(uintptrT, 1))
			class := b.NewValue2(pos, OpRsh64Ux64, u64T, both, shift)
			class.AuxInt = 1
			var bad *Value
			if check == CSlowStorePtr {
				bad = b.NewValue2(pos, OpNeq64, boolT, b.NewValue2(pos, OpAnd64, u64T, class, f.ConstInt64(u64T, 3)), f.ConstInt64(u64T, 2))
			} else {
				// The words that the bytes are in: one more than
				// their size has if they do not begin at a word.
				words := (size+7)/8 + 1
				if size == 1 {
					words = 1
				}
				ptrs := int64(-0x5555555555555556) & int64(uint64(1)<<(2*words)-1) // the high bit of each class
				bad = b.NewValue2(pos, OpNeq64, boolT, b.NewValue2(pos, OpAnd64, u64T, class, f.ConstInt64(u64T, ptrs)), f.ConstInt64(u64T, 0))
			}
			v.copyOf(b.NewValue2(pos, OpOrB, boolT, out, bad))
		default:
			f.Fatalf("bad C23Slow %s", v.LongString())
		}
	}
}

// cAddend returns the pointer that the integer v is made of by adding
// to it and taking from it: the p of uintptr(p)&^CEnd + n. nil if there
// is none.
func cAddend(v *Value) *Value {
	for depth := 0; depth < 8; depth++ {
		switch v.Op {
		case OpAdd64:
			// The pointer is on one side: the first that is one.
			if x := cAddend(v.Args[0]); x != nil {
				return x
			}
			v = v.Args[1]
		case OpSub64:
			v = v.Args[0]
		case OpAnd64:
			if c := v.Args[1]; c.Op == OpConst64 && c.AuxInt == ^CEnd {
				v = v.Args[0]
			} else if c := v.Args[0]; c.Op == OpConst64 && c.AuxInt == ^CEnd {
				v = v.Args[1]
			} else {
				return nil
			}
		case OpConvert:
			if v.Args[0].Type.IsPtrShaped() {
				return v.Args[0]
			}
			return nil
		default:
			return nil
		}
	}
	return nil
}

// cSlowAlways makes v, a C23Slow, true: the runtime makes the check.
func cSlowAlways(v *Value) {
	v.reset(OpConstBool)
	v.AuxInt = 1
}

// cSlowVariable makes v, a C23Slow whose root r is the address of a
// variable of the frame, a comparison with the variable's size: a
// pointer into the variable or to its end is one that the runtime
// would let through, as it lets through every pointer into the stack
// that is made of another.
func cSlowVariable(f *Func, v, r *Value) {
	b, pos, mem := v.Block, v.Pos, v.Args[2]
	uintptrT := f.Config.Types.Uintptr
	boolT := f.Config.Types.Bool
	varSize := r.Aux.(*ir.Name).Type().Size()
	mask, size := v.AuxInt>>8&0xff, v.AuxInt>>16
	q := b.NewValue2(pos, OpConvert, uintptrT, v.Args[0], mem)
	off := b.NewValue2(pos, OpSub64, uintptrT, q, b.NewValue2(pos, OpConvert, uintptrT, r, mem))
	switch v.AuxInt & 0xff {
	case CSlowArith:
		v.reset(OpLeq64U)
		v.AddArg2(f.ConstInt64(uintptrT, varSize), off)
	case CSlowConv, CSlowConvScalar, CSlowConvUntyped, CSlowConvClaim:
		// (Not past the end of the variable, where the runtime
		// asks only that it is in the stack. A variable's words
		// have no claims.)
		if size > varSize {
			cSlowAlways(v)
			return
		}
		out := b.NewValue2(pos, OpLess64U, boolT, f.ConstInt64(uintptrT, varSize-size), off)
		if mask == 0 {
			v.copyOf(out)
			return
		}
		odd := b.NewValue2(pos, OpNeq64, boolT, b.NewValue2(pos, OpAnd64, uintptrT, q, f.ConstInt64(uintptrT, mask)), f.ConstInt64(uintptrT, 0))
		v.reset(OpOrB)
		v.AddArg2(out, odd)
	case CSlowStoreByte, CSlowStorePtr:
		// What is stored in a variable that has no pointer is
		// bytes, and a pointer to anything goes where the variable
		// has one: the rest is the runtime's.
		t := r.Aux.(*ir.Name).Type()
		ok := !t.HasPointers()
		if v.AuxInt&0xff == CSlowStorePtr {
			for t.IsArray() {
				t = t.Elem()
			}
			ok = t.IsUnsafePtr() || t.IsPtr() && (t.Elem().Size() == 0 || t.Elem().Size() == 1 && !t.Elem().HasPointers())
		}
		if !ok || size > varSize {
			cSlowAlways(v)
			return
		}
		v.reset(OpLess64U)
		v.AddArg2(f.ConstInt64(uintptrT, varSize-size), off)
	default:
		cSlowAlways(v)
	}
}

// cRoots returns the function that says what a pointer of f is made of
// by offsets and indexes: its root. A phi of pointers that all have one
// root, the phi's own aside, has that root, as a pointer that a loop
// steps has; any other phi is a root itself.
//
// The phis that are arguments of one another, through offsets, are
// taken together (the strongly connected components of the phis, those
// that others are made of first): if what comes into them from outside
// has one root, they all have it. If not, some of them are roots: a phi
// is one when its arguments' roots differ, and the others are found
// again with that known, until none differ.
func cRoots(f *Func) func(*Value) *Value {
	base := func(v *Value) *Value {
		for {
			switch v.Op {
			case OpOffPtr, OpAddPtr, OpPtrIndex, OpCopy, OpNilCheck:
				v = v.Args[0]
				continue
			case OpConvert:
				// What walk makes of unsafe.Add in a package of
				// C's: the pointer as an integer, less the bit of
				// a pointer to an end, and the sum as a pointer.
				if v.Type.IsPtrShaped() {
					if x := cAddend(v.Args[0]); x != nil {
						v = x
						continue
					}
				}
			case OpSelectN:
				// The pointer that the runtime's check gives is
				// the one it is given, with that bit or without,
				// or a pointer to a union's value, which is
				// elsewhere. (What a root is for is where a
				// pointer is looked for first: one that is not
				// in what its root points into is the runtime's
				// to check, as any that a comparison stops.)
				if c := v.Args[0]; v.AuxInt == 0 && c.Op == OpStaticLECall {
					switch c.Aux.(*AuxCall).Fn {
					case f.fe.Syslook("checkptrArithmeticC"), f.fe.Syslook("checkptrConvC"), f.fe.Syslook("checkptrConvCLoad"):
						v = c.Args[0]
						continue
					}
				}
			}
			return v
		}
	}
	phiRoot := map[*Value]*Value{}
	root := func(v *Value) *Value {
		v = base(v)
		if v.Op == OpPhi {
			return phiRoot[v] // nil if not known yet
		}
		return v
	}

	// Tarjan's algorithm.
	index := map[*Value]int{}
	low := map[*Value]int{}
	onStack := map[*Value]bool{}
	var stack []*Value
	var visit func(p *Value)
	visit = func(p *Value) {
		index[p] = len(index) + 1
		low[p] = index[p]
		stack = append(stack, p)
		onStack[p] = true
		for _, a := range p.Args {
			a = base(a)
			if a.Op != OpPhi {
				continue
			}
			if index[a] == 0 {
				visit(a)
				low[p] = min(low[p], low[a])
			} else if onStack[a] {
				low[p] = min(low[p], index[a])
			}
		}
		if low[p] != index[p] {
			return
		}
		var scc []*Value
		for {
			x := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[x] = false
			scc = append(scc, x)
			if x == p {
				break
			}
		}
		// The phis of scc that are roots: none at first.
		self := map[*Value]bool{}
		for {
			for _, x := range scc {
				delete(phiRoot, x)
				if self[x] || len(scc) > 64 {
					phiRoot[x] = x
				}
			}
			var differs *Value
			for changed := true; changed && differs == nil; {
				changed = false
				for _, x := range scc {
					if self[x] {
						continue
					}
					var m *Value
					for _, a := range x.Args {
						r := root(a)
						if r == nil {
							continue
						}
						if m == nil {
							m = r
						} else if m != r {
							differs = x
						}
					}
					if differs != nil {
						break
					}
					if m != nil && phiRoot[x] != m {
						phiRoot[x] = m
						changed = true
					}
				}
			}
			if differs == nil {
				break
			}
			self[differs] = true
		}
		for _, x := range scc {
			if phiRoot[x] == nil {
				phiRoot[x] = x
			}
		}
	}
	for _, b := range f.Blocks {
		for _, v := range b.Values {
			if v.Op == OpPhi && v.Type.IsPtrShaped() && index[v] == 0 {
				visit(v)
			}
		}
	}
	return root
}
