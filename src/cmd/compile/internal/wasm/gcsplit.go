// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wasm

import (
	"cmd/compile/internal/ir"
	"cmd/compile/internal/reflectdata"
	"cmd/compile/internal/ssa"
	"cmd/compile/internal/ssagen"
	"cmd/compile/internal/types"
	"cmd/internal/obj"
	"cmd/internal/obj/wasm"
	"cmd/internal/objabi"
	"cmp"
	"fmt"
	"internal/buildcfg"
	"math"
	"os"
	"slices"
	"strings"
)

// Code generation for GOWASM=gc,split (stage 15 of doc/gd/wasm-gc.md).
//
// Every Go value is in linear memory, laid out as Go lays it out, the
// words of pointers included: they hold the address pointed to. Each
// allocation that has pointers also has an array of references
// (wasm.GCSlots), one element per pointer word (types.PtrSize: 4
// bytes with wasm32, see spSlots), in which the pointers
// are, so that the engine's collector sees them. A pointer is a
// wasm.GCPtr: the address, the array of what it points into, and the
// offset of the address from where element 0 of the array is.
//
// An address that a load or a store uses is resolved, as in the model
// of objects, from its SSA: a base (a pointer, a variable in the frame
// in linear memory, a variable in a local that holds a pointer to it,
// or static data), a constant offset in bytes and terms computed at run
// time (indexes, scaled). A scalar is then loaded or stored at the
// address; a pointer is read from or written to the array, and its
// address to memory, by the functions the linker generates
// (wasm.GCSplitLoadPtr and the others).

func gcSplit() bool { return buildcfg.GOWASM.Split }

// Bounds.
//
// A pointer has the size of what it points into (wasm.GCPtrLim), and
// how far its address is from where that begins (wasm.GCPtrOff): the
// block of an allocation, a variable of a frame, the data. Code that is
// not the runtime's loads and stores through a pointer only what is
// inside (wasm.GCSplitCheck), whatever made the pointer: the program
// may have added anything to it, and converted it to a pointer to
// anything (package unsafe, C that cmd/cc compiled). A pointer made of
// an integer has no size, and nothing is loaded through it. So a
// program reaches the memory that it was given pointers to and no
// other, whatever it does: doc/gd/cc.md, "On wasm".
//
// What is not checked: an address that the compiler made itself, of a
// variable of the frame or of the data, at a constant offset or at an
// index that Go checked; and the runtime, which makes pointers of
// addresses.

// splitChecked reports whether the code of f is checked.
func splitChecked(s *ssagen.State, f *ssa.Func) bool {
	if !gcSplit() || os.Getenv("GOWASMGC_NOBOUNDS") != "" {
		return false
	}
	if s.GD().Flag.CompilingRuntime {
		return false
	}
	if fn := f.Frontend().Func(); fn != nil && fn.Pragma&ir.NoCheckPtr != 0 {
		return false
	}
	return true
}

// splitHelper returns the function name of the linker's, or the form
// of it that checked code calls.
func (g *gcGen) splitHelper(name string) string {
	if g.checked {
		return wasm.GCSplitChecked(name)
	}
	return name
}

// integer reports whether a is an address that the compiler has as an
// integer: the word of an interface that holds its type or its table,
// which is the address of a descriptor in the data, and what is read
// from there. (An integer that the program converts to a pointer is a
// pointer, of no size, before anything is loaded through it.)
func (g *gcGen) integer(a spAddr) bool {
	return a.kind == spBase && g.wtype(a.base.Type) == 'l'
}

// splitHelperAt is splitHelper for a load or a store at a.
func (g *gcGen) splitHelperAt(name string, as ...spAddr) string {
	for _, a := range as {
		if g.integer(a) {
			return name
		}
	}
	return g.splitHelper(name)
}

func (g *gcGen) ptrSym() *obj.LSym { return wasm.GCTypeSym(g.ctxt, wasm.GCPtr) }

// The kinds of base of an address.
const (
	spBase   = iota // an SSA value: a pointer, or an integer that is an address
	spLocal         // a local that holds a pointer to the variable
	spFrame         // a variable in the frame in linear memory
	spStatic        // static data: the symbol
)

type spTerm struct {
	v     *ssa.Value // an integer
	scale int64
}

type spAddr struct {
	kind  int
	added bool       // an offset was added that Go did not check (unsafe.Add, pointer arithmetic)
	base  *ssa.Value // spBase
	local int32      // spLocal
	sym   *obj.LSym  // spStatic
	name  *ir.Name   // spFrame: the variable
	off   int64      // in bytes, from the base (spFrame: from fp)
	dyn   []spTerm
}

// spResolve resolves the address v.
func (g *gcGen) spResolve(v *ssa.Value) spAddr {
	var a spAddr
	for {
		switch v.Op {
		case ssa.OpOffPtr:
			a.off += v.AuxInt
			v = v.Args[0]
			continue
		case ssa.OpPtrIndex:
			if e := v.Type.Elem(); e.Size() != 0 {
				a.addTerm(v.Args[1], e.Size())
			}
			v = v.Args[0]
			continue
		case ssa.OpAddPtr:
			a.addTerm(v.Args[1], 1)
			a.added = true
			v = v.Args[0]
			continue
		case ssa.OpLocalAddr:
			n := v.Aux.(*ir.Name)
			if off, ok := g.frame[n]; ok {
				a.kind = spFrame
				a.name = n
				a.off += off
				return a
			}
			if l, ok := g.objs[n]; ok {
				a.kind = spLocal
				a.local = l
				return a
			}
			g.unsupported(v, "address of a variable that has no place")
		case ssa.OpAddr:
			sym := v.Aux.(*obj.LSym)
			if sym.Gotype != nil && sym.Gotype.TypeInfo() != nil && sym.Gotype.TypeInfo().Type.(*types.Type).Size() == 0 {
				// Every variable of size zero is at the same address.
				sym = g.ctxt.Lookup("runtime.zerobase")
			}
			a.kind = spStatic
			a.sym = sym
			return a
		}
		a.kind = spBase
		a.base = v
		return a
	}
}

// addTerm adds x*scale to the address, x an integer: to the constant
// if x is one (computed where it is used, so nothing is left to do).
func (a *spAddr) addTerm(x *ssa.Value, scale int64) {
	if x.Op == ssa.OpWasmI64Const {
		a.off += x.AuxInt * scale
		return
	}
	a.dyn = append(a.dyn, spTerm{x, scale})
}

// spDyn pushes the sum of the terms computed at run time, and reports
// whether there were any.
func (g *gcGen) spDyn(a spAddr) bool {
	for i, t := range a.dyn {
		g.get(t.v, true)
		if t.scale != 1 {
			i32Const(g.s, int32(t.scale))
			g.prog(wasm.AI32Mul)
		}
		if i > 0 {
			g.prog(wasm.AI32Add)
		}
	}
	return len(a.dyn) > 0
}

// spAddr pushes, as an i32, the address a is, but for an offset that it
// returns, which the instruction that uses the address adds (an offset
// of a load or a store).
func (g *gcGen) spPushAddr(a spAddr) int64 {
	off := a.off
	switch a.kind {
	case spBase:
		g.get(a.base, false)
		if g.wtype(a.base.Type) == 'l' {
			g.prog(wasm.AI32WrapI64)
		} else {
			g.typed(wasm.AStructGet, g.ptrSym(), wasm.GCPtrAddr)
		}
	case spLocal:
		g.local(wasm.ALocalGet, a.local)
		g.typed(wasm.AStructGet, g.ptrSym(), wasm.GCPtrAddr)
	case spFrame:
		g.local(wasm.ALocalGet, g.fp)
	case spStatic:
		p := g.prog(wasm.AI32Const)
		p.From = obj.Addr{Type: obj.TYPE_ADDR, Name: obj.NAME_EXTERN, Sym: a.sym}
	}
	if g.spDyn(a) {
		g.prog(wasm.AI32Add)
	}
	if off < 0 || off > math.MaxInt32 {
		i32Const(g.s, int32(off))
		g.prog(wasm.AI32Add)
		off = 0
	}
	return off
}

// direct reports whether a, the address of n bytes, is one that the
// compiler made itself and needs no check: of a variable of the frame
// or of the data, with nothing added but what Go checked. It returns
// -1 if a load or a store there is to be checked, and 1 if not.
func (a spAddr) direct(g *gcGen, n int64) int {
	switch a.kind {
	case spFrame:
		if a.added {
			return -1
		}
		// (A constant offset may be what was added, folded.)
		if in := a.off - g.frame[a.name]; len(a.dyn) == 0 && (in < 0 || in+n > a.name.Type().Size()) {
			return -1
		}
		return 1
	case spStatic:
		if a.added {
			return -1
		}
		return 1
	}
	return -1
}

// One check for the loads and stores of a block that are through the
// same pointer.
//
// The code of a block runs from its start to its end, so what is
// loaded and stored at constant offsets from one pointer (and the same
// indexes) is asked about once, for all the bytes from the first to the
// last: p.x, p.y and p.z are one check, of the three. splitRanges finds
// those bytes for each such address of the block (chkRange); the first
// load or store that is generated asks for them all, and the others
// are then of an address that is known to be inside (chkDone, which is
// forgotten wherever the code that is generated branches: splitFlow).
//
// (So a program that loads the first field of a struct through a
// pointer to less memory than the struct, and never reaches the load of
// a later field that is in the same block, stops at the first.)

// spKey returns what names the pointer and the indexes of a, for
// chkRange, or "" if a is not checked.
func (g *gcGen) spKey(a spAddr, n int64) string {
	if !g.checked || a.direct(g, n) > 0 || g.integer(a) {
		return ""
	}
	var b strings.Builder
	switch a.kind {
	case spBase:
		fmt.Fprintf(&b, "v%d", a.base.ID)
	case spLocal:
		fmt.Fprintf(&b, "l%d", a.local)
	default:
		return ""
	}
	for _, t := range a.dyn {
		fmt.Fprintf(&b, "+v%d*%d", t.v.ID, t.scale)
	}
	return b.String()
}

// splitRanges finds the bytes that the loads and stores of the block b
// are of, by pointer.
func (g *gcGen) splitRanges(b *ssa.Block) {
	if !g.checked {
		return
	}
	g.chkRange = make(map[string][2]int64)
	g.chkDone = make(map[string][2]int64)
	if g.inLoop == nil {
		g.inLoop = splitLoops(g.f)
	}
	g.hot = g.inLoop[b.ID]
	add := func(addr *ssa.Value, n int64) {
		a := g.spResolve(addr)
		k := g.spKey(a, n)
		if k == "" {
			return
		}
		r, ok := g.chkRange[k]
		if !ok {
			r = [2]int64{a.off, a.off + n}
		}
		g.chkRange[k] = [2]int64{min(r[0], a.off), max(r[1], a.off+n)}
	}
	for _, v := range b.Values {
		switch v.Op {
		case ssa.OpLoad:
			if g.wtype(v.Type) != 'r' {
				add(v.Args[0], spScalar(v.Type).Size())
			}
		case ssa.OpStore:
			if t := v.Aux.(*types.Type); g.wtype(t) != 'r' {
				add(v.Args[0], spScalar(t).Size())
			}
		case ssa.OpMove:
			if t := v.Aux.(*types.Type); !t.HasPointers() && t.Size() != 0 {
				add(v.Args[0], t.Size())
				add(v.Args[1], t.Size())
			}
		case ssa.OpZero:
			if t := v.Aux.(*types.Type); !t.HasPointers() && t.Size() != 0 {
				add(v.Args[0], t.Size())
			}
		}
	}
}

// splitFlow is called before the instruction as is generated: after one
// that branches, or that a branch may reach, what was asked before is
// not known to have been.
func (g *gcGen) splitFlow(as obj.As) {
	if len(g.chkDone) == 0 {
		return
	}
	switch as {
	case wasm.ABlock, wasm.ALoop, wasm.AIf, wasm.AElse, wasm.AEnd, wasm.ABr, wasm.ABrIf, wasm.ABrTable,
		wasm.ATRYCATCH, wasm.ABrOnCast, wasm.ABrOnCastFail, wasm.ARefBlock, wasm.ABrOnNonNull:
		clear(g.chkDone)
	}
}

// spCheck is wasm.GCSplitCheck for n bytes, of the pointer and the
// offset that are pushed, as instructions: in a loop, where a call that
// the engine does not expand costs several times the load that it is
// for.
func (g *gcGen) spCheck(n int64) {
	if g.chkR < 0 {
		g.chkR, g.chkI = g.newLocal('r'), g.newLocal('i')
	}
	ptr := g.ptrSym()
	g.local(wasm.ALocalSet, g.chkI)
	g.local(wasm.ALocalTee, g.chkR)
	g.typed(wasm.AStructGet, ptr, wasm.GCPtrOff)
	g.local(wasm.ALocalGet, g.chkI)
	g.prog(wasm.AI32Add)
	g.prog(wasm.AI64ExtendI32U)
	i64Const(g.s, n)
	g.prog(wasm.AI64Add)
	g.local(wasm.ALocalGet, g.chkR)
	g.typed(wasm.AStructGet, ptr, wasm.GCPtrLim)
	g.prog(wasm.AI64ExtendI32U)
	g.prog(wasm.AI64GtU)
	g.prog(wasm.AIf)
	g.prog(obj.AUNDEF)
	g.prog(wasm.AEnd)
	g.local(wasm.ALocalGet, g.chkR)
	g.typed(wasm.AStructGet, ptr, wasm.GCPtrAddr)
	g.local(wasm.ALocalGet, g.chkI)
	g.prog(wasm.AI32Add)
}

// splitLoops returns the blocks of f that are in a loop: those that can
// be reached from themselves.
func splitLoops(f *ssa.Func) map[ssa.ID]bool {
	// (Tarjan's strongly connected components, without recursion.)
	in := make(map[ssa.ID]bool)
	index := make(map[ssa.ID]int)
	low := make(map[ssa.ID]int)
	on := make(map[ssa.ID]bool)
	var stack []*ssa.Block
	type frame struct {
		b *ssa.Block
		i int
	}
	next := 1
	for _, root := range f.Blocks {
		if index[root.ID] != 0 {
			continue
		}
		work := []frame{{root, 0}}
		index[root.ID], low[root.ID] = next, next
		next++
		stack = append(stack, root)
		on[root.ID] = true
		for len(work) > 0 {
			fr := &work[len(work)-1]
			b := fr.b
			if fr.i < len(b.Succs) {
				c := b.Succs[fr.i].Block()
				fr.i++
				if c == b {
					in[b.ID] = true
				}
				if index[c.ID] == 0 {
					index[c.ID], low[c.ID] = next, next
					next++
					stack = append(stack, c)
					on[c.ID] = true
					work = append(work, frame{c, 0})
				} else if on[c.ID] {
					low[b.ID] = min(low[b.ID], index[c.ID])
				}
				continue
			}
			work = work[:len(work)-1]
			if len(work) > 0 {
				p := work[len(work)-1].b
				low[p.ID] = min(low[p.ID], low[b.ID])
			}
			if low[b.ID] == index[b.ID] {
				n := 0
				for i := len(stack) - 1; ; i-- {
					n++
					if stack[i] == b {
						break
					}
				}
				for i := 0; i < n; i++ {
					c := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					on[c.ID] = false
					if n > 1 {
						in[c.ID] = true
					}
				}
			}
		}
	}
	return in
}

// spPushAddrN is spPushAddr for a load or a store of n bytes at a,
// which in checked code are in what the pointer points into.
func (g *gcGen) spPushAddrN(a spAddr, n int64) int64 {
	if !g.checked || a.direct(g, n) > 0 || g.integer(a) {
		return g.spPushAddr(a)
	}
	if k := g.spKey(a, n); k != "" {
		if r, ok := g.chkDone[k]; ok && r[0] <= a.off && a.off+n <= r[1] {
			return g.spPushAddr(a)
		}
		if r, ok := g.chkRange[k]; ok && r[0] <= a.off && a.off+n <= r[1] && (r[0] != a.off || r[1] != a.off+n) && r[1]-r[0] <= math.MaxInt32 {
			// The first of several: all of them.
			off := a.off - r[0]
			a.off = r[0]
			g.spPushPtrRel(a)
			if g.hot {
				g.spCheck(r[1] - r[0])
			} else {
				i32Const(g.s, int32(r[1]-r[0]))
				g.helper(wasm.GCSplitCheck)
			}
			g.chkDone[k] = r
			return off
		}
	}
	g.spPushPtrRel(a)
	if g.hot {
		g.spCheck(n)
		return 0
	}
	switch n {
	case 1:
		g.helper(wasm.GCSplitCheck1)
	case 2:
		g.helper(wasm.GCSplitCheck2)
	case 4:
		g.helper(wasm.GCSplitCheck4)
	case 8:
		g.helper(wasm.GCSplitCheck8)
	default:
		i32Const(g.s, int32(n))
		g.helper(wasm.GCSplitCheck)
	}
	return 0
}

// spPushPtrRel pushes a pointer and an offset in bytes from it (an
// i32) that a is.
func (g *gcGen) spPushPtrRel(a spAddr) {
	switch a.kind {
	case spBase:
		g.get(a.base, false)
		if g.wtype(a.base.Type) == 'l' {
			g.helper(wasm.GCSplitIntToPtr)
		}
	case spLocal:
		g.local(wasm.ALocalGet, a.local)
	case spFrame:
		if l, ok := g.framePtrs[a.name]; ok {
			// A variable with pointers: its own pointer, and array.
			g.local(wasm.ALocalGet, l)
			a.off -= g.frame[a.name]
		} else if g.checked {
			// A pointer to the variable alone: what is added to
			// it stays in it.
			g.splitFrameVar(a.name)
			a.off -= g.frame[a.name]
		} else {
			g.local(wasm.ALocalGet, g.fptr)
		}
	case spStatic:
		g.global(wasm.AGlobalGet, wasm.GCGlobalStatic)
		p := g.prog(wasm.AI32Const)
		p.From = obj.Addr{Type: obj.TYPE_ADDR, Name: obj.NAME_EXTERN, Sym: a.sym, Offset: a.off}
		if g.spDyn(a) {
			g.prog(wasm.AI32Add)
		}
		return
	}
	dyn := g.spDyn(a)
	if a.off != 0 || !dyn {
		i32Const(g.s, int32(a.off))
		if dyn {
			g.prog(wasm.AI32Add)
		}
	}
}

// spScalar returns the scalar type that a value of type t is: t, or
// the one field of a struct or element of an array.
func spScalar(t *types.Type) *types.Type {
	for {
		switch {
		case t.IsStruct():
			var only *types.Type
			n := 0
			for _, f := range t.Fields() {
				if f.Type.Size() == 0 {
					continue
				}
				only = f.Type
				n++
			}
			if n != 1 {
				return t
			}
			t = only
		case t.IsArray() && t.NumElem() == 1:
			t = t.Elem()
		default:
			return t
		}
	}
}

// spConvert converts the value pushed from the WebAssembly type have
// to want.
func (g *gcGen) spConvert(have, want byte, v *ssa.Value) {
	switch {
	case have == want:
	case have == 'r' && want == 'l':
		g.ptrToInt()
	case have == 'l' && want == 'r':
		g.intToPtr()
	case have == 'l' && want == 'd':
		g.prog(wasm.AF64ReinterpretI64)
	case have == 'd' && want == 'l':
		g.prog(wasm.AI64ReinterpretF64)
	case have == 'l' && want == 'f':
		g.prog(wasm.AI32WrapI64)
		g.prog(wasm.AF32ReinterpretI32)
	case have == 'f' && want == 'l':
		g.prog(wasm.AI32ReinterpretF32)
		g.prog(wasm.AI64ExtendI32U)
	default:
		g.unsupported(v, "conversion of "+string(have)+" to "+string(want))
	}
}

// splitLoad pushes the value that the load v loads.
func (g *gcGen) splitLoad(v *ssa.Value) {
	a := g.spResolve(v.Args[0])
	t := spScalar(v.Type)
	if g.wtype(v.Type) == 'r' {
		g.spPushPtrRel(a)
		g.helper(g.splitHelperAt(wasm.GCSplitLoadPtr, a))
		return
	}
	off := g.spPushAddrN(a, t.Size())
	p := g.prog(loadOp(t))
	p.From = obj.Addr{Type: obj.TYPE_CONST, Offset: off}
	if w := g.wtype(t); w != g.wtype(v.Type) {
		g.spConvert(w, g.wtype(v.Type), v)
	}
}

// splitStore stores the value of type t that val pushes (of the
// WebAssembly type have) at the address addr.
func (g *gcGen) splitStore(addr *ssa.Value, t *types.Type, have byte, val func(), v *ssa.Value) {
	a := g.spResolve(addr)
	if g.wtype(t) == 'r' {
		g.spPushPtrRel(a)
		val()
		g.spConvert(have, 'r', v)
		g.helper(g.splitHelperAt(wasm.GCSplitStorePtr, a))
		return
	}
	st := spScalar(t)
	off := g.spPushAddrN(a, st.Size())
	val()
	g.spConvert(have, g.wtype(st), v)
	p := g.prog(storeOp(st))
	p.To = obj.Addr{Type: obj.TYPE_CONST, Offset: off}
}

// splitPointer pushes the pointer (or, if v is an integer, the
// address) that the address v is.
func (g *gcGen) splitPointer(v *ssa.Value) {
	a := g.spResolve(v)
	if g.wtype(v.Type) == 'l' {
		if a.kind == spStatic && len(a.dyn) == 0 && (a.sym.Type == objabi.STEXT || a.sym.Func() != nil) {
			// A function's address (its index << 16) is past
			// what an i32 holds in a program of more than 28672
			// functions.
			p := g.prog(wasm.AI64Const)
			p.From = obj.Addr{Type: obj.TYPE_ADDR, Name: obj.NAME_EXTERN, Sym: a.sym, Offset: a.off}
			return
		}
		off := g.spPushAddr(a)
		if off != 0 {
			i32Const(g.s, int32(off))
			g.prog(wasm.AI32Add)
		}
		g.prog(wasm.AI64ExtendI32U)
		return
	}
	switch {
	case a.kind == spStatic && a.off == 0 && len(a.dyn) == 0:
		g.typed(wasm.ARefGlobalGet, a.sym, 0)
		return
	case a.kind == spLocal && a.off == 0 && len(a.dyn) == 0:
		g.local(wasm.ALocalGet, a.local)
		return
	case a.kind == spBase && a.off == 0 && len(a.dyn) == 0:
		g.get(a.base, false)
		if g.wtype(a.base.Type) == 'l' {
			g.helper(g.splitHelper(wasm.GCSplitIntToPtr))
		}
		return
	}
	g.spPushPtrRel(a)
	g.helper(wasm.GCSplitAddPtr)
}

// spSlots returns the length of the array of a block of size bytes
// that has pointers: an element for each pointer word (types.PtrSize
// bytes: 4 with wasm32), to the end.
func spSlots(size int64) int64 {
	ps := int64(types.PtrSize)
	return (size + ps - 1) / ps
}

// spSmall is the largest value without pointers that is copied or
// zeroed by loads and stores rather than by memory.copy and
// memory.fill.
const spSmall = 64

// splitMove copies the value of type t from the address v.Args[1] to
// v.Args[0].
func (g *gcGen) splitMove(v *ssa.Value, t *types.Type) {
	size := t.Size()
	dst, src := g.spResolve(v.Args[0]), g.spResolve(v.Args[1])
	if size == 0 {
		g.discard(v.Args[0])
		g.discard(v.Args[1])
		return
	}
	if t.HasPointers() {
		g.spPushPtrRel(dst)
		g.spPushPtrRel(src)
		i32Const(g.s, int32(size))
		g.helper(g.splitHelperAt(wasm.GCSplitMove, dst, src))
		return
	}
	d, s := g.scratch('i'), g.scratch2()
	g.spAddrLocal(dst, d, size)
	g.spAddrLocal(src, s, size)
	if size > spSmall {
		g.local(wasm.ALocalGet, d)
		g.local(wasm.ALocalGet, s)
		i32Const(g.s, int32(size))
		g.prog(wasm.AMemoryCopy)
		return
	}
	for off := int64(0); off < size; {
		n := spChunk(size - off)
		g.local(wasm.ALocalGet, d)
		g.local(wasm.ALocalGet, s)
		g.prog(spLoad(n)).From = obj.Addr{Type: obj.TYPE_CONST, Offset: off}
		g.prog(spStore(n)).To = obj.Addr{Type: obj.TYPE_CONST, Offset: off}
		off += n
	}
}

// splitZero zeroes the value of type t at the address v.Args[0].
func (g *gcGen) splitZero(v *ssa.Value, t *types.Type) {
	size := t.Size()
	a := g.spResolve(v.Args[0])
	if size == 0 {
		g.discard(v.Args[0])
		return
	}
	if t.HasPointers() {
		g.spPushPtrRel(a)
		i32Const(g.s, int32(size))
		g.helper(g.splitHelperAt(wasm.GCSplitZero, a))
		return
	}
	d := g.scratch('i')
	g.spAddrLocal(a, d, size)
	if size > spSmall {
		g.local(wasm.ALocalGet, d)
		i32Const(g.s, 0)
		i32Const(g.s, int32(size))
		g.prog(wasm.AMemoryFill)
		return
	}
	for off := int64(0); off < size; {
		n := spChunk(size - off)
		g.local(wasm.ALocalGet, d)
		i64Const(g.s, 0)
		g.prog(spStore(n)).To = obj.Addr{Type: obj.TYPE_CONST, Offset: off}
		off += n
	}
}

// spAddrLocal sets the i32 local l to the address a, of n bytes.
func (g *gcGen) spAddrLocal(a spAddr, l int32, n int64) {
	if off := g.spPushAddrN(a, n); off != 0 {
		i32Const(g.s, int32(off))
		g.prog(wasm.AI32Add)
	}
	g.local(wasm.ALocalSet, l)
}

// scratch2 returns a second scratch i32 local.
func (g *gcGen) scratch2() int32 {
	if g.scratchI32b < 0 {
		g.scratchI32b = g.newLocal('i')
	}
	return g.scratchI32b
}

func spChunk(n int64) int64 {
	switch {
	case n >= 8:
		return 8
	case n >= 4:
		return 4
	case n >= 2:
		return 2
	}
	return 1
}

func spLoad(n int64) obj.As {
	return map[int64]obj.As{8: wasm.AI64Load, 4: wasm.AI64Load32U, 2: wasm.AI64Load16U, 1: wasm.AI64Load8U}[n]
}

func spStore(n int64) obj.As {
	return map[int64]obj.As{8: wasm.AI64Store, 4: wasm.AI64Store32, 2: wasm.AI64Store16, 1: wasm.AI64Store8}[n]
}

// splitEqPtr compares two pointers, or a pointer and an integer, by
// their addresses.
func (g *gcGen) splitEqPtr(v *ssa.Value, extend bool) {
	x, y := v.Args[0], v.Args[1]
	switch {
	case g.wtype(x.Type) == 'l' || g.wtype(y.Type) == 'l':
		for _, a := range []*ssa.Value{x, y} {
			g.get(a, false)
			if g.wtype(a.Type) == 'r' {
				g.ptrToInt()
			}
		}
		g.prog(wasm.AI64Eq)
	case y.Op == ssa.OpConstNil:
		g.get(x, false)
		g.discard(y)
		g.prog(wasm.ARefIsNull)
	case x.Op == ssa.OpConstNil:
		g.discard(x)
		g.get(y, false)
		g.prog(wasm.ARefIsNull)
	default:
		g.get(x, false)
		g.helper(wasm.GCSplitPtrAddr)
		g.get(y, false)
		g.helper(wasm.GCSplitPtrAddr)
		g.prog(wasm.AI32Eq)
	}
	if v.Op == ssa.OpNeqPtr {
		g.prog(wasm.AI32Eqz)
	}
	if extend {
		g.prog(wasm.AI64ExtendI32U)
	}
}

// splitStatic pushes a pointer to the start of the data symbol sym.
func (g *gcGen) splitStatic(sym *obj.LSym) {
	g.typed(wasm.ARefGlobalGet, sym, 0)
}

// splitNewObject pushes a pointer to a new zeroed variable of type t.
func (g *gcGen) splitNewObject(t *types.Type) {
	if t.Size() == 0 {
		g.zerobase()
		return
	}
	g.typeDesc(t)
	g.runtime("runtime.newobject")
}

// splitFrame makes the pointer to the frame in linear memory (g.fptr),
// which has no array, and to each variable in it that has pointers
// (g.framePtrs), which has an array of its own: what a variable points
// to is kept as long as the variable is used (or a pointer to it), as
// Go keeps what a live variable on the stack points to, and not as
// long as the frame.
func (g *gcGen) splitFrame() {
	if len(g.frame) == 0 {
		return
	}
	g.fptr = g.newLocal('r')
	g.local(wasm.ALocalGet, g.fp)
	g.prog(wasm.ARefNull)
	i32Const(g.s, 0)
	i32Const(g.s, int32(g.frameSize))
	g.typed(wasm.AStructNew, g.ptrSym(), 0)
	g.local(wasm.ALocalSet, g.fptr)
	names := make([]*ir.Name, 0, len(g.frame))
	for n := range g.frame {
		if n.Type().HasPointers() {
			names = append(names, n)
		}
	}
	slices.SortFunc(names, func(a, b *ir.Name) int { return cmp.Compare(g.frame[a], g.frame[b]) })
	g.framePtrs = make(map[*ir.Name]int32)
	for _, n := range names {
		l := g.newLocal('r')
		g.framePtrs[n] = l
		g.local(wasm.ALocalGet, g.fp)
		if off := g.frame[n]; off != 0 {
			i32Const(g.s, int32(off))
			g.prog(wasm.AI32Add)
		}
		i32Const(g.s, int32(spSlots(n.Type().Size())))
		g.typed(wasm.AArrayNewDefault, wasm.GCTypeSym(g.ctxt, wasm.GCSlots), 0)
		i32Const(g.s, 0)
		i32Const(g.s, int32(n.Type().Size()))
		g.typed(wasm.AStructNew, g.ptrSym(), 0)
		g.local(wasm.ALocalSet, l)
	}
}

// splitFrameVar pushes a pointer to the variable n in the frame.
func (g *gcGen) splitFrameVar(n *ir.Name) {
	if l, ok := g.framePtrs[n]; ok {
		g.local(wasm.ALocalGet, l)
		return
	}
	// A variable without pointers: a pointer to it alone, with no
	// array.
	g.local(wasm.ALocalGet, g.fp)
	if off := g.frame[n]; off != 0 {
		i32Const(g.s, int32(off))
		g.prog(wasm.AI32Add)
	}
	g.prog(wasm.ARefNull)
	i32Const(g.s, 0)
	i32Const(g.s, int32(n.Type().Size()))
	g.typed(wasm.AStructNew, g.ptrSym(), 0)
}

// splitCall emits the call v, if it is one that split compiles in its
// own way, and reports whether it did.
func (g *gcGen) splitCall(v *ssa.Value, name string, args []*ssa.Value) bool {
	switch name {
	case "runtime.newobject":
		if !g.s.GD().Flag.CompilingRuntime {
			g.runtime("runtime.countalloc")
		}
		g.get(args[0], false)
		g.runtime("runtime.newobject")
	case "runtime.maybeInPlace":
		g.discard(args[0])
		g.get(args[1], false)
		g.runtime("runtime.newobject")
	case "runtime.linaddr", "runtime.trylinaddr":
		g.get(args[0], false)
		g.helper(wasm.GCSplitPtrAddr)
		g.prog(wasm.AI64ExtendI32U)
	case "runtime.newblockptr":
		g.get(args[0], true)
		g.get(args[1], true)
		g.get(args[2], true)
		g.helper(wasm.GCSplitNew)
	case "runtime.ptrlim":
		g.get(args[0], false)
		g.typed(wasm.AStructGet, g.ptrSym(), wasm.GCPtrLim)
		g.prog(wasm.AI64ExtendI32U)
	case "runtime.ptroff":
		g.get(args[0], false)
		g.typed(wasm.AStructGet, g.ptrSym(), wasm.GCPtrOff)
		g.prog(wasm.AI64ExtendI32U)
	case "runtime.ptrnslots":
		// The length of the array of a pointer, 0 if it has none.
		tmp := g.scratch('r')
		g.get(args[0], false)
		g.local(wasm.ALocalTee, tmp)
		g.typed(wasm.AStructGet, g.ptrSym(), wasm.GCPtrSlots)
		g.prog(wasm.ARefIsNull)
		p := g.prog(wasm.AIf)
		p.From.Offset = 0x80 - wasm.GCTypeI64
		i64Const(g.s, 0)
		g.prog(wasm.AElse)
		g.local(wasm.ALocalGet, tmp)
		g.typed(wasm.AStructGet, g.ptrSym(), wasm.GCPtrSlots)
		g.prog(wasm.AArrayLen)
		g.prog(wasm.AI64ExtendI32U)
		g.prog(wasm.AEnd)
	case "runtime.splitmove":
		g.get(args[0], false)
		i32Const(g.s, 0)
		g.get(args[1], false)
		i32Const(g.s, 0)
		g.get(args[2], true)
		g.helper(wasm.GCSplitMove)
	case "runtime.splitmovec":
		// The runtime's copy for the program: of what the two
		// pointers point into.
		g.get(args[0], false)
		i32Const(g.s, 0)
		g.get(args[1], false)
		i32Const(g.s, 0)
		g.get(args[2], true)
		g.helper(wasm.GCSplitChecked(wasm.GCSplitMove))
	case "runtime.splitintptr":
		g.get(args[0], false)
		g.helper(wasm.GCSplitChecked(wasm.GCSplitIntToPtr))
	case "runtime.splitzeroc":
		g.get(args[0], false)
		i32Const(g.s, 0)
		g.get(args[1], true)
		g.helper(wasm.GCSplitChecked(wasm.GCSplitZero))
	case "runtime.splitzero":
		g.get(args[0], false)
		i32Const(g.s, 0)
		g.get(args[1], true)
		g.helper(wasm.GCSplitZero)
	case "runtime.makechan", "runtime.makechan64", "runtime.mapaccess1", "runtime.mapaccess2", "runtime.mapassign":
		// (The runtime declares one more parameter, the zero value of
		// an element that is an object in the other model: nil.)
		aux := v.Aux.(*ssa.AuxCall)
		g.args(aux, args)
		if name == "runtime.makechan" || name == "runtime.makechan64" {
			if len(args) == 2 {
				g.prog(wasm.ARefNull)
			}
		} else if len(args) == 3 {
			g.prog(wasm.ARefNull)
		}
		p := g.prog(obj.ACALL)
		p.To = obj.Addr{Type: obj.TYPE_MEM, Name: obj.NAME_EXTERN, Sym: aux.Fn}
		p.Pos = v.Pos
	case "runtime.selectgo", "runtime.memclrNoHeapPointers", "runtime.memclrHasPointers", "runtime.makeslice", "runtime.growslice":
		g.args(v.Aux.(*ssa.AuxCall), args)
		p := g.prog(obj.ACALL)
		p.To = obj.Addr{Type: obj.TYPE_MEM, Name: obj.NAME_EXTERN, Sym: v.Aux.(*ssa.AuxCall).Fn}
		p.Pos = v.Pos
	case "runtime.gccopy", "runtime.gcmove", "runtime.gczero", "runtime.gcclone", "runtime.gcnew", "runtime.boxobj", "runtime.boxoff",
		"runtime.isobjarray", "runtime.newobjarray", "runtime.objarrayget", "runtime.objarrayset", "runtime.newlinear",
		"runtime.gclinbase":
		// The other model's.
		g.unsupported(v, "with split, "+name)
	default:
		return false
	}
	return true
}

// splitTypeDesc pushes a pointer to the type descriptor of t.
func (g *gcGen) splitTypeDesc(t *types.Type) {
	g.splitStatic(reflectdata.TypeLinksym(g.s.GD(), t))
}
