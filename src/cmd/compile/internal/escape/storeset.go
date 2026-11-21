// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package escape

import (
	"fmt"
	"slices"

	"cmd/compile/internal/ir"
	"cmd/compile/internal/types"
)

// What a bit says (doc/gd/sharing.md, "What a bit says: the objects
// that are stored in").
//
// A parameter's bit says that every object reached from the argument,
// of one of the types of memory that the function may store a pointer
// in through it, is local: the parameter's store types. A caller's
// group for the argument checks only the members that may be, or lead
// to, memory of those types (relevant).

// maxStoreTypes is the most types that a store set names before it is
// any type.
const maxStoreTypes = 8

// A storeSet is a set of types of memory; with all, any type.
type storeSet struct {
	all bool
	ts  []*types.Type
}

func (s *storeSet) isAll() bool { return s == nil || s.all }

// add adds the type t, or any type if t is nil, and reports whether s
// changed.
func (s *storeSet) add(t *types.Type) bool {
	if s.all {
		return false
	}
	if t == nil || t.HasShape() {
		s.all, s.ts = true, nil
		return true
	}
	for _, x := range s.ts {
		if types.Identical(x, t) {
			return false
		}
	}
	if len(s.ts) == maxStoreTypes {
		s.all, s.ts = true, nil
		return true
	}
	s.ts = append(s.ts, t)
	return true
}

// union adds o to s, and reports whether s changed.
func (s *storeSet) union(o *storeSet) bool {
	if o.isAll() {
		if s.all {
			return false
		}
		s.all, s.ts = true, nil
		return true
	}
	changed := false
	for _, t := range o.ts {
		if s.add(t) {
			changed = true
		}
	}
	return changed
}

func (s *storeSet) String() string {
	if s.isAll() {
		return "any"
	}
	return fmt.Sprint(s.ts)
}

// storeTarget returns the type of the memory that a store through the
// pointer, slice or map p writes, or nil if it is not known.
func storeTarget(p *types.Type) *types.Type {
	switch {
	case p == nil:
		return nil
	case p.IsPtr(), p.IsSlice():
		return p.Elem()
	case p.IsMap():
		return p
	}
	return nil
}

// A reliance is that the function fn relies on the bits of its
// parameters of mask for what the function callee stores in what its
// parameter arg reaches.
type reliance struct {
	fn     *ir.Func
	mask   uint16
	callee *ir.Name
	arg    int
}

// noteStoreType records that the current function stores in memory of
// the type t through what its parameters of mask reach.
func (e *escape) noteStoreType(mask uint16, t *types.Type) {
	if mask == 0 || !e.deepOn() || e.skipStoreTypes {
		return
	}
	if e.gd.Flag.LowerM >= 4 {
		fmt.Printf("%s: %v stores in %v through what its parameters %b reach\n", e.gd.FmtPos(e.gd.Pos), e.curfn, t, mask)
	}
	if e.storeTypes == nil {
		e.storeTypes = make(map[*ir.Func]*[16]storeSet)
	}
	st := e.storeTypes[e.curfn]
	if st == nil {
		st = new([16]storeSet)
		e.storeTypes[e.curfn] = st
	}
	for k := 0; k < 16; k++ {
		if mask&(1<<k) != 0 {
			st[k].add(t)
		}
	}
}

// noteReliance records that the current function relies on the bits of
// its parameters of mask for what callee stores in what its parameter
// arg reaches.
func (e *escape) noteReliance(mask uint16, callee *ir.Name, arg int) {
	if mask == 0 || !e.deepOn() {
		return
	}
	if e.gd.Flag.LowerM >= 4 {
		fmt.Printf("%s: %v relies on the bits %b for what %v stores in what its parameter %d reaches\n", e.gd.FmtPos(e.gd.Pos), e.curfn, mask, callee, arg)
	}
	e.relies = append(e.relies, reliance{e.curfn, mask, callee, arg})
}

// storeSetOf returns the store types of the parameter arg of fn (the
// receiver first): any type if nobody knows them.
func (b *batch) storeSetOf(fn *ir.Name, arg int) *storeSet {
	if fn == nil || fn.Func == nil || arg < 0 || arg >= 16 {
		return nil
	}
	if b.inMutualBatch(fn) {
		if st := b.storeSets[fn.Func]; st != nil {
			return &st[arg]
		}
		return &storeSet{}
	}
	if arg >= len(fn.Func.ParamShares) {
		return nil
	}
	ps := &fn.Func.ParamShares[arg]
	if !ps.StoreKnown {
		return nil
	}
	return &storeSet{ts: ps.StoreTypes}
}

// solveStoreSets finds the store types of the parameters of the
// functions fns, when the groups' conditions are known.
func (b *batch) solveStoreSets(fns []*ir.Func) {
	if !b.deepOn() {
		return
	}
	b.storeSets = make(map[*ir.Func]*[16]storeSet)
	for _, fn := range fns {
		st := new([16]storeSet)
		if own := b.storeTypes[fn]; own != nil {
			*st = *own
			for k := range st {
				st[k].ts = slices.Clone(st[k].ts)
			}
		}
		b.storeSets[fn] = st
	}
	// The groups whose conditions need the functions' own bits.
	for _, g := range b.needGroups {
		st := b.storeSets[g.curfn]
		if st == nil {
			continue
		}
		c := b.cond(g)
		if c.bad {
			continue
		}
		own := c.own >> 1
		for k := 0; k < 16; k++ {
			if own&(1<<k) != 0 && st[k].union(g.dynRoot.need) && b.gd.Flag.LowerM >= 4 {
				fmt.Printf("%s: %v relies on its parameter %d for a group that needs %v\n", b.gd.FmtPos(g.curfn.Pos()), g.curfn, k, g.dynRoot.need)
			}
		}
	}
	// What the functions called store, until nothing changes.
	for changed := true; changed; {
		changed = false
		for _, r := range b.relies {
			st := b.storeSets[r.fn]
			if st == nil {
				continue
			}
			src := b.storeSetOf(r.callee, r.arg)
			for k := 0; k < 16; k++ {
				if r.mask&(1<<k) != 0 && st[k].union(src) {
					changed = true
				}
			}
		}
	}
	if b.gd.Flag.LowerM >= 4 {
		for _, fn := range fns {
			st := b.storeSets[fn]
			for i, f := range fn.Type().RecvParams() {
				if i < 16 && f.Type.HasPointers() {
					fmt.Printf("%s: %v stores through its parameter %d in %v\n", b.gd.FmtPos(fn.Pos()), fn, i, &st[i])
				}
			}
		}
	}
}

// noteStoreSet records in ps the store types of the parameter i of fn.
func (b *batch) noteStoreSet(fn *ir.Func, i int, ps *ir.ParamShare) {
	ps.StoreKnown, ps.StoreTypes = false, nil
	st := b.storeSets[fn]
	if st == nil || i >= 16 || st[i].all {
		return
	}
	ps.StoreKnown, ps.StoreTypes = true, slices.Clone(st[i].ts)
}

// relevant reports whether the member m of a group whose user stores in
// memory of the types need may be such memory, or lead to it.
func (b *batch) relevant(m *location, need *storeSet) bool {
	if need.isAll() {
		return true
	}
	if len(need.ts) == 0 {
		return false
	}
	if m.n == nil || m.curfn == nil || b.unsafeFns[m.curfn] {
		return true
	}
	t := m.n.Type()
	if t == nil {
		return true
	}
	if isAlloc(m) {
		obj := b.valueType(m, false)
		if obj == nil {
			return true
		}
		for _, u := range need.ts {
			if mayHoldMem(obj, u) {
				return true
			}
		}
		return false
	}
	for _, u := range need.ts {
		if b.mayReachMem(t, u) {
			return true
		}
	}
	return false
}

// mayHoldMem reports whether an object of type x may have memory of
// type u in it, or be it.
func mayHoldMem(x, u *types.Type) bool {
	if x.HasShape() || u.HasShape() {
		return true
	}
	return sameUnder(x, u) || holds(x, u)
}

// mayReachMem reports whether a value of type v may lead, through
// pointers, to memory of type u.
func (b *batch) mayReachMem(v, u *types.Type) bool {
	type key struct{ v, u *types.Type }
	if r, ok := b.reachMem[key{v, u}]; ok {
		return r
	}
	seen := make(map[*types.Type]bool)
	var walk func(x *types.Type) bool
	walk = func(x *types.Type) bool {
		if x == nil || x.HasShape() {
			return true
		}
		if seen[x] {
			return false
		}
		seen[x] = true
		switch x.Kind() {
		case types.TPTR, types.TSLICE, types.TCHAN:
			e := x.Elem()
			return mayHoldMem(e, u) || walk(e)
		case types.TARRAY:
			return walk(x.Elem())
		case types.TMAP:
			return mayHoldMem(x, u) || walk(x.Key()) || walk(x.Elem())
		case types.TSTRUCT:
			for _, f := range x.Fields() {
				if walk(f.Type) {
					return true
				}
			}
			return false
		case types.TSTRING:
			return false
		case types.TINTER, types.TUNSAFEPTR, types.TFUNC:
			// What it holds or has captured, nothing says.
			return true
		}
		return false
	}
	r := walk(v)
	if b.reachMem == nil {
		b.reachMem = make(map[any]bool)
	}
	b.reachMem[key{v, u}] = r
	return r
}
