// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wasm

import (
	"cmd/compile/internal/ir"
	"cmd/compile/internal/reflectdata"
	"cmd/compile/internal/ssa"
	"cmd/compile/internal/types"
	"cmd/internal/obj"
	"cmd/internal/obj/wasm"
	"internal/buildcfg"
	"math"
)

// Code generation for GOWASM=gc,split (stage 15 of doc/gd/wasm-gc.md).
//
// Every Go value is in linear memory, laid out as Go lays it out, the
// words of pointers included: they hold the address pointed to. Each
// allocation that has pointers also has an array of references
// (wasm.GCSlots), one element per 8-byte word, in which the pointers
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
	base  *ssa.Value // spBase
	local int32      // spLocal
	sym   *obj.LSym  // spStatic
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
				a.dyn = append(a.dyn, spTerm{v.Args[1], e.Size()})
			}
			v = v.Args[0]
			continue
		case ssa.OpAddPtr:
			a.dyn = append(a.dyn, spTerm{v.Args[1], 1})
			v = v.Args[0]
			continue
		case ssa.OpLocalAddr:
			n := v.Aux.(*ir.Name)
			if off, ok := g.frame[n]; ok {
				a.kind = spFrame
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
		g.local(wasm.ALocalGet, g.fptr)
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
			for _, f := range t.Fields() {
				if f.Type.Size() == 0 {
					continue
				}
				only = f.Type
			}
			if only == nil {
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
		g.helper(wasm.GCSplitLoadPtr)
		return
	}
	off := g.spPushAddr(a)
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
		g.helper(wasm.GCSplitStorePtr)
		return
	}
	st := spScalar(t)
	off := g.spPushAddr(a)
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
			g.helper(wasm.GCSplitIntToPtr)
		}
		return
	}
	g.spPushPtrRel(a)
	g.helper(wasm.GCSplitAddPtr)
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
		g.helper(wasm.GCSplitMove)
		return
	}
	d, s := g.scratch('i'), g.scratch2()
	g.spAddrLocal(dst, d)
	g.spAddrLocal(src, s)
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
		g.helper(wasm.GCSplitZero)
		return
	}
	d := g.scratch('i')
	g.spAddrLocal(a, d)
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

// spAddrLocal sets the i32 local l to the address a.
func (g *gcGen) spAddrLocal(a spAddr, l int32) {
	if off := g.spPushAddr(a); off != 0 {
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
// with an array for its pointers if any variable in it has some.
func (g *gcGen) splitFrame() {
	if len(g.frame) == 0 {
		return
	}
	end := int64(0)
	ptrs := false
	for n, off := range g.frame {
		end = max(end, off+n.Type().Size())
		ptrs = ptrs || n.Type().HasPointers()
	}
	g.fptr = g.newLocal('r')
	g.local(wasm.ALocalGet, g.fp)
	if ptrs {
		i32Const(g.s, int32((end+7)/8))
		g.typed(wasm.AArrayNewDefault, wasm.GCTypeSym(g.ctxt, wasm.GCSlots), 0)
	} else {
		g.prog(wasm.ARefNull)
	}
	i32Const(g.s, 0)
	g.typed(wasm.AStructNew, g.ptrSym(), 0)
	g.local(wasm.ALocalSet, g.fptr)
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
		g.helper(wasm.GCSplitNew)
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
