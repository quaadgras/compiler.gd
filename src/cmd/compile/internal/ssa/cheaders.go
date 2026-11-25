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
	CSlowArith  = iota // arg0 is the result of arithmetic on arg1: runtime.checkptrArithmetic
	CSlowConv          // arg0 is converted to a pointer to a type: runtime.checkptrAlignmentC
	CSlowStore         // a pointer is stored at arg0: runtime.c23StorePtr
	CSlowStored        // a pointer was stored at arg0: runtime.c23PointerSlot
	CSlowScalar        // what holds no pointer is stored at arg0, in malloc's memory with no type: runtime.c23Scalars
)

// CHeaders reports whether the checks of C's pointers, and the stores of
// C's that the runtime is told of, are made in the compiled code where
// it knows what the pointers point into (cheaders), with a call only for
// what it does not let through.
func (c *Config) CHeaders() bool {
	return c.PtrSize == 8 && c.gd.Flag.N == 0 && c.gd.Debug.CHeaders == 0
}

// CConvMax is the size of the largest type that a C23Slow of a
// conversion is for: runtime's cConvMax.
const CConvMax = 4096

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
			// A store of integers that is in more than one word is
			// the runtime's: one bit is looked at here.
			if size, mask := v.AuxInt>>16, v.AuxInt>>8&0xff; v.AuxInt&0xff == CSlowScalar && (size > 8 || size > mask+1) {
				cSlowAlways(v)
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
	u32T := f.Config.Types.UInt32

	// The calls of runtime.c23Header, by root.
	type header struct {
		call *Value
		sel  [6]*Value
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
		// off is where q is in what r points into.
		off := b.NewValue2(pos, OpAdd64, uintptrT, b.NewValue2(pos, OpSub64, uintptrT, q, b.NewValue2(pos, OpConvert, uintptrT, r, mem)), result(h, 0))
		switch check {
		case CSlowArith:
			v.reset(OpLess64U)
			v.AddArg2(result(h, 1), off)
		case CSlowConv:
			out := b.NewValue2(pos, OpLeq64U, boolT, result(h, 2), off)
			if mask == 0 {
				v.copyOf(out)
				break
			}
			odd := b.NewValue2(pos, OpNeq64, boolT, b.NewValue2(pos, OpAnd64, uintptrT, q, f.ConstInt64(uintptrT, mask)), f.ConstInt64(uintptrT, 0))
			v.reset(OpOrB)
			v.AddArg2(out, odd)
		case CSlowStore, CSlowStored, CSlowScalar:
			out := b.NewValue2(pos, OpLess64U, boolT, result(h, 1), off)
			// The word, or the first one if q is not in the
			// object: its bit is then not looked at, but it is
			// loaded.
			w := b.NewValue2(pos, OpRsh64Ux64, uintptrT, b.NewValue2(pos, OpAdd64, uintptrT, off, result(h, 3)), result(h, 4))
			w.AuxInt = 1 // the shift is less than 64
			in := b.NewValue2(pos, OpSub64, uintptrT, b.NewValue1(pos, OpZeroExt8to64, uintptrT, b.NewValue1(pos, OpCvtBoolToUint8, f.Config.Types.UInt8, out)), f.ConstInt64(uintptrT, 1))
			w = b.NewValue2(pos, OpAnd64, uintptrT, w, in)
			at := b.NewValue2(pos, OpAdd64, uintptrT, result(h, 5), b.NewValue2(pos, OpLsh64x64, uintptrT, b.NewValue2(pos, OpRsh64Ux64, uintptrT, w, f.ConstInt64(uintptrT, 5)), f.ConstInt64(uintptrT, 2)))
			word := b.NewValue2(pos, OpLoad, u32T, b.NewValue2(pos, OpConvert, u32T.PtrTo(), at, mem), mem)
			shift := b.NewValue2(pos, OpRsh32Ux64, u32T, word, b.NewValue2(pos, OpAnd64, uintptrT, w, f.ConstInt64(uintptrT, 31)))
			shift.AuxInt = 1
			set := b.NewValue2(pos, OpNeq32, boolT, b.NewValue2(pos, OpAnd32, u32T, shift, f.ConstInt32(u32T, 1)), f.ConstInt32(u32T, 0))
			if check == CSlowStored {
				v.copyOf(set)
				break
			}
			if check == CSlowScalar {
				// The call unless the word is known to hold no
				// pointer already.
				v.reset(OpOrB)
				v.AddArg2(out, b.NewValue1(pos, OpNot, boolT, set))
				break
			}
			v.reset(OpOrB)
			v.AddArg2(out, set)
		default:
			f.Fatalf("bad C23Slow %s", v.LongString())
		}
	}
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
		v.reset(OpLess64U)
		v.AddArg2(f.ConstInt64(uintptrT, varSize), off)
	case CSlowConv:
		// (Not past the end of the variable, where the runtime
		// asks only that it is in the stack.)
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
	default:
		// A store in a variable is not one that the runtime is
		// told of, and ssagen asks for none.
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
