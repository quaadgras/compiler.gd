// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa

import (
	"cmd/compile/internal/types"
)

// cscalars is for a package of C's (base.Invocation.CStores), whose
// stores may be in the memory that C allocates (runtime/cspan.go): there
// the runtime knows of each word whether it may hold a pointer, from
// what is stored in it. A store of a pointer says so wherever it is
// (ssagen: runtime.c23StorePtr). A store of anything else says nothing,
// except where this pass knows that it is through a pointer into what
// malloc gave with no type: the memory that nothing has said anything of
// yet, which the store is the first to make something of. Such a store
// has a call to runtime.c23Scalars before it, and the words it writes
// in hold no pointer from then on.
//
// A pointer is known to be into such memory if it is the result of
// malloc with no type (c23.AllocRaw, c23.ReallocRaw) in this function,
// after inlining, or is made of one that is: by an offset or an index,
// or by a phi of nothing else but nil. Whether anything was stored there
// since is not followed: the call finds the bit set then, and returns.
//
// What is stored is of a type that has no pointer, and that is not one
// of bytes alone: C copies anything a byte at a time, a pointer too.
func cscalars(f *Func) {
	if !f.Config.gd.CStores {
		return
	}
	// The pointers that are made of other pointers, and of which.
	derived := func(v *Value) bool {
		switch v.Op {
		case OpOffPtr, OpAddPtr, OpPtrIndex, OpCopy, OpNilCheck:
			return true
		}
		return false
	}
	// raw is what may be a pointer into malloc's memory: everything
	// that could be made of one at first, less what is made of
	// anything else, until nothing changes. Then, of those, the ones
	// that a result of malloc reaches (grounded): a phi of itself and
	// nil is not one.
	raw := f.newSparseSet(f.NumValues())
	defer f.retSparseSet(raw)
	grounded := f.newSparseSet(f.NumValues())
	defer f.retSparseSet(grounded)
	var work []*Value
	for _, b := range f.Blocks {
		for _, v := range b.Values {
			switch {
			case cRawResult(v):
				raw.add(v.ID)
				grounded.add(v.ID)
			case derived(v), v.Op == OpPhi && v.Type.IsPtrShaped():
				raw.add(v.ID)
				work = append(work, v)
			}
		}
	}
	if grounded.size() == 0 {
		return
	}
	for changed := true; changed; {
		changed = false
		for _, v := range work {
			if !raw.contains(v.ID) {
				continue
			}
			ok := true
			if v.Op == OpPhi {
				for _, a := range v.Args {
					if a.Op != OpConstNil && !raw.contains(a.ID) {
						ok = false
					}
				}
			} else {
				ok = raw.contains(v.Args[0].ID)
			}
			if !ok {
				raw.remove(v.ID)
				changed = true
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, v := range work {
			if !raw.contains(v.ID) || grounded.contains(v.ID) {
				continue
			}
			ok := false
			if v.Op == OpPhi {
				for _, a := range v.Args {
					ok = ok || grounded.contains(a.ID)
				}
			} else {
				ok = grounded.contains(v.Args[0].ID)
			}
			if ok {
				grounded.add(v.ID)
				changed = true
			}
		}
	}

	// The stores, each with a call before it. A store that follows
	// another with nothing between them, at an offset from the same
	// pointer that leaves no word between the two, is of the same
	// call, which is then for the words of both: the fields of a
	// struct that C fills in one after the other are one call.
	type barrier struct {
		call   *Value
		base   *Value // the pointer that the stores are at offsets from
		lo, hi int64  // the bytes that the call is for
	}
	var aux *AuxCall
	uintptrT := f.Config.Types.Uintptr
	size := func(n int64) *Value {
		if f.Config.PtrSize == 4 {
			return f.ConstInt32(uintptrT, int32(n))
		}
		return f.ConstInt64(uintptrT, n)
	}
	barriers := map[*Value]*barrier{} // by store
	// before returns the store before v that the call of v may be one
	// with: the memory that v is made in, or, if that is of stores of
	// bytes and other things that hold no pointer where nothing is
	// known, which say nothing of any word, the one before those. It
	// returns nil if anything but the next store uses the memory of
	// one of them: a load between a call and its store could be of
	// the pointer that the store writes over, which the collector
	// would not be shown if it began to mark after the call.
	before := func(v *Value) *Value {
		m := v.MemoryArg()
		for m.Block == v.Block && m.Uses == 1 && m.Op == OpStore && !m.Aux.(*types.Type).HasPointers() {
			if _, ok := barriers[m]; ok {
				return m
			}
			m = m.MemoryArg()
		}
		if _, ok := barriers[m]; ok && m.Block == v.Block && m.Uses == 1 {
			return m
		}
		return nil
	}
	for _, b := range f.Blocks {
		// The stores of b in the order of its memory: last is the
		// one that nothing of b follows.
		var stores []*Value
		for _, v := range b.Values {
			if cScalarStore(v) > 0 && raw.contains(v.Args[0].ID) && grounded.contains(v.Args[0].ID) {
				stores = append(stores, v)
				barriers[v] = nil
			}
		}
		for _, v := range stores {
			if barriers[v] != nil {
				continue
			}
			// The stores before v that have no call yet, first.
			chain := []*Value{v}
			for m := before(v); m != nil && barriers[m] == nil; m = before(m) {
				chain = append(chain, m)
			}
			for i := len(chain) - 1; i >= 0; i-- {
				v := chain[i]
				base, lo := v.Args[0], int64(0)
				for base.Op == OpOffPtr {
					lo += base.AuxInt
					base = base.Args[0]
				}
				hi := lo + cScalarStore(v)
				mem := v.MemoryArg()
				if m := before(v); m != nil {
					if w := barriers[m]; w != nil && w.base == base && lo-w.hi < int64(f.Config.PtrSize) && w.lo-hi < int64(f.Config.PtrSize) {
						if lo < w.lo || hi > w.hi {
							w.lo, w.hi = min(lo, w.lo), max(hi, w.hi)
							p := base
							if w.lo != 0 {
								p = b.NewValue1I(v.Pos, OpOffPtr, types.Types[types.TUNSAFEPTR], w.lo, base)
							}
							w.call.SetArg(0, p)
							w.call.SetArg(1, size(w.hi-w.lo))
						}
						barriers[v] = w
						continue
					}
				}
				if aux == nil {
					argTypes := []*types.Type{types.Types[types.TUNSAFEPTR], uintptrT}
					aux = StaticAuxCall(f.fe.Syslook("c23Scalars"), f.ABIDefault.ABIAnalyzeTypes(argTypes, nil))
				}
				call := b.NewValue0A(v.Pos, OpStaticLECall, aux.LateExpansionResultType(), aux)
				call.AddArgs(v.Args[0], size(hi-lo), mem)
				v.SetArg(len(v.Args)-1, b.NewValue1I(v.Pos, OpSelectN, types.TypeMem, 0, call))
				barriers[v] = &barrier{call, base, lo, hi}
			}
		}
	}
}

// cScalarStore returns the number of bytes that v stores, if it is a
// store of what has no pointer and is not of bytes alone, and 0
// otherwise.
func cScalarStore(v *Value) int64 {
	var size int64
	switch v.Op {
	case OpStore:
		size = v.Aux.(*types.Type).Size()
	case OpMove, OpZero:
		size = v.AuxInt
	default:
		return 0
	}
	if t := v.Aux.(*types.Type); t.HasPointers() || t.Alignment() < 2 {
		return 0
	}
	return size
}

// cRawResult reports whether v is the result of malloc with no type:
// of c23.AllocRaw or c23.ReallocRaw, or of what AllocRaw is when it is
// inlined, the runtime's malloc for C with no type and not for scalars.
func cRawResult(v *Value) bool {
	if v.Op != OpSelectN || v.AuxInt != 0 || !v.Type.IsPtrShaped() {
		return false
	}
	call := v.Args[0]
	if call.Op != OpStaticLECall {
		return false
	}
	switch {
	case isSameCall(call.Aux, "internal/c23.AllocRaw"), isSameCall(call.Aux, "internal/c23.ReallocRaw"):
		return true
	case isSameCall(call.Aux, "runtime.c23Malloc"):
		// (size, typ, scalar, mem)
		return len(call.Args) == 4 && call.Args[1].Op == OpConstNil && call.Args[2].Op == OpConstBool && call.Args[2].AuxInt == 0
	}
	return false
}
