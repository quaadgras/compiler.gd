// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa

import (
	"cmd/compile/internal/ir"
	"cmd/compile/internal/types"
	"cmd/internal/obj"
	"cmd/internal/objabi"
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
	// (The classes of words are bytes that are loaded several at a
	// time, wherever they are.)
	return c.PtrSize == 8 && !c.BigEndian && c.unalignedOK && c.gd.Flag.N == 0 && c.gd.Debug.CHeaders == 0
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

// CPoison is the bit of a pointer of C's that the program was not
// given, or that arithmetic took out of its object: runtime's cPoison.
// No address has it either.
const CPoison = 1 << 55

// CAddr is the mask of the address that a pointer of C's is, as an
// integer: without the bits that say nothing may be loaded through it.
// What a pointer is compared with, what is taken from it and what C
// converts it to are its address; what is added to it is checked with
// the pointer itself, which keeps what its bits say.
const CAddr = ^(CEnd | CPoison)

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
// its size is known here, and whether it has pointers.
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

	if f.Config.CShadow() {
		// A root is asked of the shadow if every check of it can
		// be: one that still needs what c23Header says keeps all
		// of its checks, which cost little after the call.
		not := map[*Value]bool{}
		for _, v := range sites {
			r := root(v.Args[1])
			if r == nil {
				continue
			}
			if r.Op == OpLocalAddr || !cShadowSite(f, v, true) {
				not[r] = true
			}
		}
		rest := sites[:0]
		for _, v := range sites {
			r := root(v.Args[1])
			if r != nil && not[r] || !cShadowSite(f, v, false) {
				rest = append(rest, v)
			}
		}
		sites = rest
	}

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
	// lastMems is what findLastMems says, until a call is put
	// somewhere (endMem).
	var lastMems []*Value
	defer func() {
		if lastMems != nil {
			f.Cache.freeValueSlice(lastMems)
		}
	}()
	lookup := func(b *Block, pos src.XPos, r, mem *Value) (*header, *Value) {
		if lastMems != nil {
			f.Cache.freeValueSlice(lastMems)
			lastMems = nil
		}
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
	var anchorAt func(b *Block, mem *Value, pos src.XPos, r *Value) *header
	anchor := func(v, r *Value) *header {
		return anchorAt(v.Block, v.Args[2], v.Pos, r)
	}
	// endMem returns the memory at the end of b: lastMems is what
	// findLastMems says, until a call is put somewhere.
	idom := f.Idom()
	endMem := func(b *Block) *Value {
		if lastMems == nil {
			lastMems = findLastMems(f)
		}
		for x := b; x != nil; x = idom[x.ID] {
			if m := lastMems[x.ID]; m != nil {
				return m
			}
		}
		return nil
	}
	anchorAt = func(b *Block, mem *Value, pos src.XPos, r *Value) *header {
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
		h, after := lookup(b, pos, r, mem)
		var stay []use
		for _, u := range users[mem] {
			if u.i >= len(u.v.Args) || u.v.Args[u.i] != mem {
				// A check that was made a comparison since: it
				// is in no memory now.
				continue
			}
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

	process := func(v *Value) {
		b := v.Block
		r := root(v.Args[1])
		if r == nil || !r.Type.IsPtrShaped() || r.Op == OpConstNil {
			cSlowAlways(v)
			return
		}
		if r.Op == OpLocalAddr {
			cSlowVariable(f, v, r)
			return
		}
		if !sdom.IsAncestorEq(r.Block, b) {
			cSlowAlways(v)
			return
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
		if h == nil && len(byRoot[r]) > 1 && r.Op == OpLoad {
			// Checks of r that none of them comes before, as
			// those of a member that is used on two ways of an
			// if: r is looked up where all of them are after,
			// if that is not in a loop that one of them is out
			// of. Only for a pointer that was loaded, which
			// is used where it was loaded for: one that a loop
			// steps is checked in the few places where it is
			// stored through, and a lookup for each step would
			// cost more than those.
			l := b
			for _, o := range byRoot[r] {
				for !sdom.IsAncestorEq(l, o.Block) {
					l = idom[l.ID]
				}
			}
			// The call is put after the memory that l ends in,
			// in the block that makes that memory: everything
			// that uses it is after that block, so nothing is
			// left to use it beside the call's.
			if l != b {
				if mem := endMem(l); mem != nil {
					l = mem.Block
				}
			}
			ok := l != b && sdom.IsAncestorEq(r.Block, l)
			for _, o := range byRoot[r] {
				if ll := ln.b2l[l.ID]; ll != nil && !inLoop(o.Block, ll) {
					ok = false
				}
			}
			if ok {
				if mem := endMem(l); mem != nil {
					h = anchorAt(l, mem, v.Pos, r)
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
			return
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
					at := b.NewValue2(pos, OpAdd64, uintptrT, result(h, 5), w)
					u64T := f.Config.Types.UInt64
					// The bits of the classes that are to be as
					// want has them: both for no pointer, in every
					// word of the type, the high one for a pointer
					// to anything. A class is a byte, and those
					// of a type of CConvWords words or less are
					// in two loads.
					size := v.AuxInt >> 16
					words := (size + 7) / 8
					unit := int64(CClassScalar)
					if check == CSlowConvUntyped {
						// (A word that is a pointer of a type
						// may be one that a pointer to anything
						// is over, or part of something else:
						// the runtime's to say.)
						words, unit = 1, 2
					}
					var class *Value
					for done := int64(0); done < words; done += 8 {
						x := b.NewValue2(pos, OpLoad, u64T, b.NewValue2(pos, OpConvert, u64T.PtrTo(), b.NewValue2(pos, OpAdd64, uintptrT, at, f.ConstInt64(uintptrT, done)), mem), mem)
						n := min(words-done, 8)
						bits := cEach(3, n)
						x = b.NewValue2(pos, OpXor64, u64T, b.NewValue2(pos, OpAnd64, u64T, x, f.ConstInt64(u64T, bits)), f.ConstInt64(u64T, cEach(unit, n)))
						if class == nil {
							class = x
						} else {
							class = b.NewValue2(pos, OpOr64, u64T, class, x)
						}
					}
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
			u8T, u64T := f.Config.Types.UInt8, f.Config.Types.UInt64
			w := b.NewValue2(pos, OpRsh64Ux64, uintptrT, b.NewValue2(pos, OpAdd64, uintptrT, off, result(h, 3)), result(h, 4))
			w.AuxInt = 1 // the shift is less than 64
			in := b.NewValue2(pos, OpSub64, uintptrT, b.NewValue1(pos, OpZeroExt8to64, uintptrT, b.NewValue1(pos, OpCvtBoolToUint8, f.Config.Types.UInt8, out)), f.ConstInt64(uintptrT, 1))
			w = b.NewValue2(pos, OpAnd64, uintptrT, w, in)
			at := b.NewValue2(pos, OpAdd64, uintptrT, result(h, 5), w)
			// (What is not C's has the classes for a store of a
			// pointer in the uint64 after, and those for a store
			// of bytes in the one after that: runtime's
			// cStoreFree.)
			other := b.NewValue2(pos, OpAnd64, uintptrT, b.NewValue2(pos, OpRsh64Ux64, uintptrT, result(h, 4), f.ConstInt64(uintptrT, 2)), f.ConstInt64(uintptrT, 8))
			if check == CSlowStoreByte {
				other = b.NewValue2(pos, OpLsh64x64, uintptrT, other, f.ConstInt64(uintptrT, 1))
			}
			at = b.NewValue2(pos, OpAdd64, uintptrT, at, other)
			var bad *Value
			if check == CSlowStorePtr {
				x := b.NewValue1(pos, OpZeroExt8to64, u64T, b.NewValue2(pos, OpLoad, u8T, b.NewValue2(pos, OpConvert, u8T.PtrTo(), at, mem), mem))
				bad = b.NewValue2(pos, OpNeq64, boolT, b.NewValue2(pos, OpAnd64, u64T, x, f.ConstInt64(u64T, 3)), f.ConstInt64(u64T, 2))
			} else {
				// The words that the bytes are in: one more than
				// their size has if they do not begin at a word.
				words := (size+7)/8 + 1
				if size == 1 {
					words = 1
				}
				var ptrs *Value
				for done := int64(0); done < words; done += 8 {
					var x *Value
					if words == 1 {
						x = b.NewValue1(pos, OpZeroExt8to64, u64T, b.NewValue2(pos, OpLoad, u8T, b.NewValue2(pos, OpConvert, u8T.PtrTo(), at, mem), mem))
					} else {
						x = b.NewValue2(pos, OpLoad, u64T, b.NewValue2(pos, OpConvert, u64T.PtrTo(), b.NewValue2(pos, OpAdd64, uintptrT, at, f.ConstInt64(uintptrT, done)), mem), mem)
					}
					// The high bit of each class.
					x = b.NewValue2(pos, OpAnd64, u64T, x, f.ConstInt64(u64T, cEach(2, min(words-done, 8))))
					if ptrs == nil {
						ptrs = x
					} else {
						ptrs = b.NewValue2(pos, OpOr64, u64T, ptrs, x)
					}
				}
				bad = b.NewValue2(pos, OpNeq64, boolT, ptrs, f.ConstInt64(u64T, 0))
			}
			v.copyOf(b.NewValue2(pos, OpOrB, boolT, out, bad))
		default:
			f.Fatalf("bad C23Slow %s", v.LongString())
		}
	}
	for _, v := range sites {
		b := v.Block
		n := len(b.Values)
		process(v)
		if users != nil {
			// What was made in the block for the check, in the
			// memory that the check was in, is after a call
			// that is put before the block later.
			for _, x := range b.Values[n:] {
				for i, a := range x.Args {
					if a.Type.IsMemory() {
						users[a] = append(users[a], use{x, i})
					}
				}
			}
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
			if c := v.Args[1]; c.Op == OpConst64 && c.AuxInt == CAddr {
				v = v.Args[0]
			} else if c := v.Args[0]; c.Op == OpConst64 && c.AuxInt == CAddr {
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
// would let through, which knows where the variables of a frame are
// (runtime/stackvar.go).
//
// A conversion is to a type that is in the variable whole, which is all
// that is asked of bytes, and of a type without pointers if the
// variable has none. Any other is compared with the variable's type by
// the runtime, as the conversion to a type that does not fit is, which
// a pointer to the variable's end may be converted to.
func cSlowVariable(f *Func, v, r *Value) {
	b, pos, mem := v.Block, v.Pos, v.Args[2]
	uintptrT := f.Config.Types.Uintptr
	boolT := f.Config.Types.Bool
	varType := r.Aux.(*ir.Name).Type()
	varSize := varType.Size()
	mask, size := v.AuxInt>>8&0xff, v.AuxInt>>16
	q := b.NewValue2(pos, OpConvert, uintptrT, v.Args[0], mem)
	off := b.NewValue2(pos, OpSub64, uintptrT, q, b.NewValue2(pos, OpConvert, uintptrT, r, mem))
	switch v.AuxInt & 0xff {
	case CSlowArith:
		v.reset(OpLeq64U)
		v.AddArg2(f.ConstInt64(uintptrT, varSize), off)
	case CSlowConv, CSlowConvScalar, CSlowConvUntyped, CSlowConvClaim:
		// (mask is not 0 for a type that has pointers: ssagen's
		// checkPtrAlignment.)
		if size > varSize || v.AuxInt&0xff != CSlowConv && (mask != 0 || varType.HasPointers()) {
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
	same := cSameLoads(f)
	base := func(v *Value) *Value {
		for {
			if v.Op == OpLoad {
				// (One that loaded the same before is the root.)
				if l := same[v]; l != nil {
					return l
				}
				return v
			}
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
					case f.fe.Syslook("checkptrArithmeticC"), f.fe.Syslook("checkptrArithmeticC1"), f.fe.Syslook("checkptrConvC"), f.fe.Syslook("checkptrConvCLoad"):
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

// cSameLoads returns, for the loads of pointers of f that load what a
// load before them loaded, that load: the same address in the same
// memory, with nothing between the two but the checks of C's pointers.
//
// C++ reads a member again each time it is used, s.size() ? s.size() - 1
// : 0, and each use is a pointer that is checked: the check may call the
// runtime, so the memory after it is not the memory before it, and the
// two loads are two values, each looked up by itself (c23Header). But a
// check stores nothing that the program loads: what the runtime's
// functions change in a pointer that is in memory is the bits that no
// address has, and the object that the pointer points into is the same.
// So the two loads have one root.
//
// The memory of a load is taken back through what a check leaves in it:
// the calls of the checks, the stores to the compiler's own temporaries
// that are made for a call (the pointers that arithmetic was on), and
// the phis that join a check's two ways. Two loads are the same if their
// addresses are the same offset from one value, and their memories go
// back to the same one; the first must be where the second can use it.
func cSameLoads(f *Func) map[*Value]*Value {
	if !f.Config.gd.CObjects {
		return nil
	}
	checks := map[*obj.LSym]bool{}
	for _, name := range []string{"checkptrArithmeticC", "checkptrArithmeticC1", "checkptrConvC", "checkptrConvCLoad"} {
		checks[f.fe.Syslook(name)] = true
	}
	// temp reports whether p is the address of a temporary of the
	// compiler's, or in one: nothing of the program's points there.
	temp := func(p *Value) bool {
		for p.Op == OpOffPtr || p.Op == OpCopy || p.Op == OpPtrIndex || p.Op == OpNilCheck {
			p = p.Args[0]
		}
		if p.Op != OpLocalAddr {
			return false
		}
		n, ok := p.Aux.(*ir.Name)
		return ok && n.AutoTemp()
	}
	// canon is what was found for a memory, if it was found without
	// meeting one that was being looked at (a loop, where the memory
	// is taken for its own): loops counts those, and steps bounds
	// the work for one load.
	canon := map[*Value]*Value{}
	doing := map[*Value]bool{}
	loops, steps := 0, 0
	var back func(m *Value, depth int) *Value
	back = func(m *Value, depth int) *Value {
		if depth == 0 {
			steps = 0
		}
		if c, ok := canon[m]; ok {
			return c
		}
		steps++
		if doing[m] || depth > 64 || steps > 2000 {
			loops++
			return m
		}
		doing[m] = true
		was := loops
		defer func() {
			delete(doing, m)
		}()
		c := m
		switch m.Op {
		case OpPhi:
			var first *Value
			for _, a := range m.Args {
				x := back(a, depth+1)
				if first == nil {
					first = x
				} else if first != x {
					first = nil
					break
				}
			}
			if first != nil {
				c = first
			}
		case OpSelectN:
			if call := m.Args[0]; call.Op == OpStaticLECall {
				fn := call.Aux.(*AuxCall).Fn
				if checks[fn] || (fn == f.fe.Syslook("c23PtrStore") || fn == f.fe.Syslook("c23ByteStore")) && temp(call.Args[0]) {
					c = back(call.Args[len(call.Args)-1], depth+1)
				}
			}
		case OpStore, OpZero:
			if temp(m.Args[0]) {
				c = back(m.MemoryArg(), depth+1)
			}
		case OpVarDef, OpVarLive:
			if n, ok := m.Aux.(*ir.Name); ok && n.AutoTemp() {
				c = back(m.MemoryArg(), depth+1)
			}
		case OpInlMark:
			// (Where a call that was inlined begins.)
			c = back(m.MemoryArg(), depth+1)
		}
		if loops == was {
			canon[m] = c
		}
		return c
	}
	type key struct {
		p   *Value
		off int64
		mem *Value
		t   *types.Type
	}
	sdom := f.Sdom()
	first := map[key][]*Value{}
	same := map[*Value]*Value{}
	for _, b := range f.Blocks {
		for _, v := range b.Values {
			if v.Op != OpLoad || !v.Type.IsPtrShaped() {
				continue
			}
			p, off := v.Args[0], int64(0)
			for p.Op == OpOffPtr || p.Op == OpCopy || p.Op == OpNilCheck {
				if p.Op == OpOffPtr {
					off += p.AuxInt
				}
				p = p.Args[0]
			}
			if temp(p) {
				continue
			}
			k := key{p, off, back(v.Args[1], 0), v.Type}
			first[k] = append(first[k], v)
		}
	}
	for _, loads := range first {
		for _, v := range loads {
			for _, l := range loads {
				// l is before v: in a block that v's is after, or
				// in the very memory that v is in.
				if l != v && same[l] == nil && (l.Block != v.Block && sdom.IsAncestorEq(l.Block, v.Block) || l.Block == v.Block && l.Args[1] == v.Args[1] && l.ID < v.ID) {
					same[v] = l
					break
				}
			}
		}
	}
	return same
}

// cEach returns x in each of the low n bytes.
func cEach(x, n int64) int64 {
	var r uint64
	for i := int64(0); i < n; i++ {
		r |= uint64(x) << (8 * i)
	}
	return int64(r)
}

// The shadow of C's memory (runtime/cshadow.go), and what the byte of a
// word says beside its class (runtime's cWordIn, cWordStart).
const (
	CShadowBase  = 0x4000_0000_0000
	CShadowIn    = 0x80
	CShadowStart = 0x40
	CShadowRO    = 0x20
)

// CShadow reports whether the compiled code asks the shadow of C's
// memory what a word is: where the runtime has one (its cShadowOn).
func (c *Config) CShadow() bool {
	return c.CHeaders() && c.arch == "amd64" && c.ctxt.Headtype == objabi.Hlinux && !c.ctxt.Flag_linkshared && c.gd.Debug.NoCShadow == 0
}

// cConstOff returns k if q is p and a constant number of bytes more.
func cConstOff(q, p *Value) (int64, bool) {
	var k int64
	for depth := 0; depth < 16; depth++ {
		if q == p {
			return k, true
		}
		switch q.Op {
		case OpOffPtr:
			k += q.AuxInt
			q = q.Args[0]
		case OpAddPtr, OpAdd64:
			if c := q.Args[1]; c.Op == OpConst64 {
				k += c.AuxInt
				q = q.Args[0]
			} else if c := q.Args[0]; c.Op == OpConst64 && q.Op == OpAdd64 {
				k += c.AuxInt
				q = q.Args[1]
			} else {
				return 0, false
			}
		case OpSub64:
			if c := q.Args[1]; c.Op == OpConst64 {
				k -= c.AuxInt
				q = q.Args[0]
			} else {
				return 0, false
			}
		case OpAnd64:
			if c := q.Args[1]; c.Op == OpConst64 && c.AuxInt == CAddr {
				q = q.Args[0]
			} else {
				return 0, false
			}
		case OpConvert, OpCopy:
			q = q.Args[0]
		default:
			return 0, false
		}
	}
	return 0, false
}

// cShadowSite makes v, a C23Slow, a comparison with the shadow of the
// words it is about, if what they are there says all that the check
// asks: it reports whether it did. What is true of v then is for the
// runtime, which may still let it through.
func cShadowSite(f *Func, v *Value, dry bool) bool {
	b, pos, mem := v.Block, v.Pos, v.Args[2]
	check, mask, size := v.AuxInt&0xff, v.AuxInt>>8&0xff, v.AuxInt>>16
	uintptrT, u64T, boolT := f.Config.Types.Uintptr, f.Config.Types.UInt64, f.Config.Types.Bool
	c := func(x int64) *Value { return f.ConstInt64(uintptrT, x) }
	// window returns the n shadow bytes from the one of the word that
	// is back words before the word of a.
	window := func(a *Value, back, n int64) *Value {
		sh := b.NewValue2(pos, OpLsh64x64, uintptrT, a, c(17))
		sh.AuxInt = 1
		idx := b.NewValue2(pos, OpRsh64Ux64, uintptrT, sh, c(20))
		idx.AuxInt = 1
		at := b.NewValue2(pos, OpAdd64, uintptrT, idx, c(CShadowBase-back))
		t := f.Config.Types.UInt8
		switch {
		case n > 4:
			t = u64T
		case n > 2:
			t = f.Config.Types.UInt32
		case n > 1:
			t = f.Config.Types.UInt16
		}
		x := b.NewValue2(pos, OpLoad, t, b.NewValue2(pos, OpConvert, t.PtrTo(), at, mem), mem)
		switch t.Size() {
		case 1:
			x = b.NewValue1(pos, OpZeroExt8to64, u64T, x)
		case 2:
			x = b.NewValue1(pos, OpZeroExt16to64, u64T, x)
		case 4:
			x = b.NewValue1(pos, OpZeroExt32to64, u64T, x)
		}
		return x
	}
	each := func(x uint64, n int64) int64 {
		var r uint64
		for i := int64(0); i < n; i++ {
			r |= x << (8 * i)
		}
		return int64(r)
	}
	words := (size + 7) / 8
	switch check {
	case CSlowArith:
		if k, ok := cConstOff(v.Args[0], v.Args[1]); !ok || k == 0 || k%8 != 0 || k < -56 || k > 56 {
			return false
		}
	case CSlowConvScalar, CSlowConv, CSlowStoreByte:
		if size == 0 || words > 8 {
			return false
		}
	case CSlowConvUntyped:
	case CSlowStorePtr:
		if size != 8 {
			return false
		}
	default:
		return false
	}
	if dry {
		return true
	}
	q := b.NewValue2(pos, OpConvert, uintptrT, v.Args[0], mem)
	// differ is not 0 if the words' bytes, under m, are not want.
	differ := func(x *Value, m, want int64) *Value {
		return b.NewValue2(pos, OpXor64, u64T, b.NewValue2(pos, OpAnd64, u64T, x, f.ConstInt64(u64T, m)), f.ConstInt64(u64T, want))
	}
	// finish makes v what d says, for a pointer q that has none of the
	// bits that no address has, is aligned as mask says, and has what
	// is converted or stored in the words that were looked at.
	finish := func(d *Value) {
		d = b.NewValue2(pos, OpOr64, u64T, d, b.NewValue2(pos, OpRsh64Ux64, u64T, q, c(47)))
		if mask != 0 {
			d = b.NewValue2(pos, OpOr64, u64T, d, b.NewValue2(pos, OpAnd64, u64T, q, c(mask)))
		}
		if size > 1 && mask&7 != 7 {
			// What begins in the word so far that it ends
			// in one more than were looked at.
			last := b.NewValue2(pos, OpRsh64Ux64, u64T, b.NewValue2(pos, OpAdd64, u64T, b.NewValue2(pos, OpAnd64, u64T, q, c(7)), c(size-1)), c(3))
			last.AuxInt = 1
			d = b.NewValue2(pos, OpOr64, u64T, d, b.NewValue2(pos, OpXor64, u64T, last, c(words-1)))
		}
		slow := b.NewValue2(pos, OpNeq64, boolT, d, f.ConstInt64(u64T, 0))
		v.copyOf(slow)
	}
	switch check {
	case CSlowArith:
		k, ok := cConstOff(v.Args[0], v.Args[1])
		if !ok || k == 0 || k%8 != 0 || k < -56 || k > 56 {
			return false
		}
		// q is in the object that the pointer was in if no word
		// from the one after the lower of the two to the upper is
		// the first of an object, and q's is in one.
		var m, want int64
		var x *Value
		if k < 0 {
			n := -k / 8
			m = CShadowIn | each(CShadowStart, n)<<8
			want = CShadowIn
			x = window(q, 0, n+1)
		} else {
			n := k / 8
			m = each(CShadowStart, n)<<8 | CShadowIn<<(8*n)
			want = CShadowIn << (8 * n)
			x = window(q, n, n+1)
		}
		// A pointer with the bit of an object's end, or any of
		// the others, is the runtime's: what it is the end of is
		// not what is at its address.
		r := b.NewValue2(pos, OpRsh64Ux64, u64T, b.NewValue2(pos, OpConvert, uintptrT, v.Args[1], mem), c(54))
		r.AuxInt = 1
		d := b.NewValue2(pos, OpXor64, u64T, b.NewValue2(pos, OpAnd64, u64T, x, f.ConstInt64(u64T, m)), f.ConstInt64(u64T, want))
		v.reset(OpNeq64)
		v.AddArg2(b.NewValue2(pos, OpOr64, u64T, d, r), f.ConstInt64(u64T, 0))
		return true
	case CSlowConvScalar:
		if size == 0 || words > 8 {
			return false
		}
		finish(differ(window(q, 0, words), each(CShadowIn|3, words), each(CShadowIn|CClassScalar, words)))
		return true
	case CSlowConvUntyped:
		finish(differ(window(q, 0, 1), CShadowIn|3, CShadowIn|2))
		return true
	case CSlowConv:
		if size == 0 || words > 8 {
			return false
		}
		finish(differ(window(q, 0, words), each(CShadowIn, words), each(CShadowIn, words)))
		return true
	case CSlowStorePtr:
		if size != 8 {
			return false
		}
		mask = 7
		finish(differ(window(q, 0, 1), CShadowIn|CShadowRO|3, CShadowIn|2))
		return true
	case CSlowStoreByte:
		if size == 0 || words > 8 {
			return false
		}
		finish(differ(window(q, 0, words), each(CShadowIn|CShadowRO|2, words), each(CShadowIn, words)))
		return true
	}
	return false
}
