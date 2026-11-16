// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wasm

import (
	"cmd/compile/internal/abi"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/reflectdata"
	"cmd/compile/internal/ssa"
	"cmd/compile/internal/ssagen"
	"cmd/compile/internal/types"
	"cmd/internal/obj"
	"cmd/internal/obj/wasm"
	"cmd/internal/objabi"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
)

// Code generation for GOWASM=gc: Go objects in the WebAssembly
// garbage-collected heap. See doc/gd/wasm-gc.md for the design and
// cmd/internal/obj/wasm/gc.go for how WebAssembly types are named.
//
// Objects. A Go value that has an address is a WebAssembly object. A
// value of an array type of two or more elements is a WebAssembly
// array; any other is a struct with one field per scalar in the Go
// value, in the order of their offsets: nested structs are flattened,
// and strings, slices and interfaces are their words. An array nested
// in a struct is a field that refers to an array of its own, allocated
// with the struct and never replaced. The elements of an array are
// scalars if the Go element is one scalar, and otherwise references to
// one object per element.
//
// Pointers. A pointer is an eqref. A pointer to a whole object is the
// object, cast to the type the code expects where it is used. A
// pointer into an object is a box (wasm.GCBox) of the object and the
// byte offset within its Go value. So Go's layout survives as the
// name of a place: the generic SSA still computes addresses with
// OffPtr and PtrIndex, and place turns an address used by a load or a
// store into the object and the field or element it stands for.
//
// Values. There is no stack in linear memory. Every SSA value is a
// WebAssembly local of its own, a constant recomputed where it is
// used, or an operand left on the WebAssembly stack for its only use.
// Arguments and results are WebAssembly parameters and results, one
// per register of the register ABI. A variable whose address is taken
// is an object allocated on entry.

type gcGen struct {
	s    *ssagen.State
	f    *ssa.Func
	ctxt *obj.Link

	loc    []int32 // the local of each value, or -1
	remat  []bool  // the value is recomputed at each use
	locals []byte  // the type of each local after the parameters
	nparam int32

	intArg, floatArg []int32 // parameter of each ABI register
	paramKind        []byte  // the WebAssembly type of each parameter
	resultKind       []byte  // and of each result

	objs map[*ir.Name]int32 // the local that holds an addressed variable

	// Arguments and results that the ABI does not put in registers
	// (arrays too large) are objects passed by reference: those of
	// the function are parameters (paramObjs), and those of its calls
	// are variables of the function (forceObj), one per slot.
	paramObjs map[*ir.Name]bool
	forceObj  map[*ir.Name]bool
	callSlots map[ssa.ID][]*ir.Name

	// The addressed variables that have no pointers are in the frame
	// of the function on the stack in linear memory, at these offsets
	// from the local fp.
	frame     map[*ir.Name]int64
	frameSize int64
	fp        int32
	csp       int32 // the top of the stack on entry: where the caller's frame ends
	newSeg    int32 // set if the frame is in a segment of the stack of its own

	// hasDefer is set if the function defers calls. Its frame is
	// then what the runtime knows its deferred calls by, and its
	// body is in a try block that catches the exception that
	// recovering from a panic throws (see runtime/wasmgc/panic.go).
	hasDefer bool

	scratchI32, scratchRef, scratchI64 int32 // scratch locals, or -1
	scratchNil                         int32 // the scratch local of intToPtr, or -1

	// typedLoc is, for a pointer that is the base of more than one
	// access, a local that holds it as a reference to the type of
	// object it most likely points to (typedSym), or null if it does
	// not point to one: an access then checks for null rather than
	// casting (see fillTyped and place).
	typedLoc map[ssa.ID]int32
	typedSym map[ssa.ID]*obj.LSym
	// linLoc is, for a pointer that is the base of accesses to linear
	// memory, a local that holds the address it points to, or 0 if
	// it is not a pointer to linear memory.
	linLoc    map[ssa.ID]int32
	localSyms map[int32]*obj.LSym // the type of each local of a typed reference

	// fused are the stores that set the fields of an object as it is
	// made (see fuseStores).
	fused   map[ssa.ID]bool
	fusedAt map[ssa.ID]map[int64]*ssa.Value // by allocation

	// slow, if not nil, is the block that place branches to, with
	// the pointer, if the pointer is not the object expected.
	slow *obj.Prog
}

func gcOf(s *ssagen.State) *gcGen {
	g, _ := s.WasmGC.(*gcGen)
	return g
}

// gcUnsupported is what unsupported panics with.
type gcUnsupported string

// unsupported gives up on the function: it uses something that is not
// implemented yet. The function is compiled to a trap (see
// genWasmGC), so that the rest of its package can be used.
func (g *gcGen) unsupported(v *ssa.Value, what string) {
	if v != nil {
		what += " (" + v.LongString() + ")"
	}
	if os.Getenv("GOWASMGC_STACK") != "" {
		what += "\n" + string(debug.Stack())
	}
	panic(gcUnsupported(what))
}

// Layout.

// A gcLeaf is one field of the WebAssembly struct of a Go type.
type gcLeaf struct {
	off  int64
	kind byte        // b h w l f d r, or a for an array of its own
	t    *types.Type // of kind a: the array type
}

func gcLeafSize(l gcLeaf) int64 {
	switch l.kind {
	case 'b':
		return 1
	case 'h':
		return 2
	case 'w', 'f':
		return 4
	case 'a':
		return l.t.Size()
	}
	return 8
}

func gcLeaves(t *types.Type) []gcLeaf {
	return gcAppendLeaves(nil, t, 0)
}

func gcAppendLeaves(ls []gcLeaf, t *types.Type, off int64) []gcLeaf {
	word := func(kinds string) []gcLeaf {
		for i := range kinds {
			ls = append(ls, gcLeaf{off: off + int64(i)*8, kind: kinds[i]})
		}
		return ls
	}
	switch {
	case t.IsStruct():
		for _, f := range t.Fields() {
			ls = gcAppendLeaves(ls, f.Type, off+f.Offset)
		}
		return ls
	case t.IsArray():
		switch n := t.NumElem(); {
		case n == 0:
			return ls
		case n == 1:
			return gcAppendLeaves(ls, t.Elem(), off)
		}
		return append(ls, gcLeaf{off: off, kind: 'a', t: t})
	case t.IsString(), t.IsSlice():
		return word("rll")
	case t.IsInterface():
		return word("lrdd")
	case t.IsComplex():
		if t.Size() == 8 {
			return append(ls, gcLeaf{off: off, kind: 'f'}, gcLeaf{off: off + 4, kind: 'f'})
		}
		return word("dd")
	case t.IsPtrShaped():
		return word("r")
	case t.IsFloat():
		if t.Size() == 4 {
			return append(ls, gcLeaf{off: off, kind: 'f'})
		}
		return word("d")
	case t.IsInteger(), t.IsBoolean():
		return append(ls, gcLeaf{off: off, kind: "bh?w???l"[t.Size()-1]})
	}
	panic("GOWASM=gc: no layout for type " + t.String())
}

// gcElem reports how the elements of an array of e are stored: as
// scalars of the kind returned, or (obj) as references to one object
// per element.
func gcElem(e *types.Type) (kind byte, obj bool) {
	if ls := gcLeaves(e); len(ls) == 1 && ls[0].kind != 'a' && ls[0].off == 0 && gcLeafSize(ls[0]) == e.Size() {
		return ls[0].kind, false
	}
	return 'o', true
}

// gcArrayOf returns the array type that p points to, or nil if p is not
// a pointer to an array.
func gcArrayOf(p *ssa.Value) *types.Type {
	if p.Type.IsPtr() && p.Type.Elem().IsArray() {
		return p.Type.Elem()
	}
	return nil
}

// gcIndexedArray returns the array type that the PtrIndex v indexes,
// or nil if its base is not a pointer to an array of v's element type:
// a pointer converted through unsafe.Pointer, which is then accessed
// as memory.
func gcIndexedArray(v *ssa.Value) *types.Type {
	at := gcArrayOf(v.Args[0])
	if at == nil {
		return nil
	}
	e := v.Type.Elem()
	if !types.Identical(at.Elem(), e) {
		ka, oa := gcElem(at.Elem())
		ke, oe := gcElem(e)
		if oa || oe || ka != ke || at.Elem().Size() != e.Size() {
			return nil
		}
	}
	return at
}

func gcIsArray(t *types.Type) bool {
	return t.IsArray() && t.NumElem() >= 2
}

// structSym returns the WebAssembly struct type of objects of type t,
// whose name spells the layout of t (see cmd/internal/obj/wasm/gc.go).
func (g *gcGen) structSym(t *types.Type) *obj.LSym {
	var b strings.Builder
	b.WriteString(wasm.GCStructPrefix)
	for i, l := range gcLeaves(t) {
		if i > 0 {
			b.WriteByte(',')
		}
		if l.kind == 'a' {
			k, _ := gcElem(l.t.Elem())
			fmt.Fprintf(&b, "A%d:%d:%d%c", l.off, l.t.Elem().Size(), l.t.NumElem(), k)
		} else {
			fmt.Fprintf(&b, "%c%d", l.kind, l.off)
		}
	}
	fmt.Fprintf(&b, "/%d", t.Size())
	return wasm.GCTypeSym(g.ctxt, b.String())
}

func (g *gcGen) arraySym(kind byte) *obj.LSym {
	return wasm.GCTypeSym(g.ctxt, wasm.GCArrayPrefix+string(kind))
}

// objSym returns the WebAssembly type of objects of type t.
func (g *gcGen) objSym(t *types.Type) *obj.LSym {
	if gcIsArray(t) {
		k, _ := gcElem(t.Elem())
		return g.arraySym(k)
	}
	return g.structSym(t)
}

func (g *gcGen) boxSym() *obj.LSym    { return wasm.GCTypeSym(g.ctxt, wasm.GCBox) }
func (g *gcGen) linearSym() *obj.LSym { return wasm.GCTypeSym(g.ctxt, wasm.GCLinear) }

// helper calls one of the functions the linker generates.
func (g *gcGen) helper(name string) {
	p := g.prog(obj.ACALL)
	p.To = obj.Addr{Type: obj.TYPE_MEM, Name: obj.NAME_EXTERN, Sym: g.ctxt.Lookup(name)}
}

// gcScalar returns the letter of the scalar that a value of type t is.
func (g *gcGen) scalar(t *types.Type, v *ssa.Value) byte {
	ls := gcLeaves(t)
	if len(ls) != 1 || ls[0].kind == 'a' {
		g.unsupported(v, "load or store of a value of type "+t.String())
	}
	return ls[0].kind
}

// gcKindType returns a Go type that is a scalar of the letter k.
func gcKindType(k byte) *types.Type {
	switch k {
	case 'b':
		return types.Types[types.TUINT8]
	case 'h':
		return types.Types[types.TUINT16]
	case 'w':
		return types.Types[types.TUINT32]
	case 'l':
		return types.Types[types.TUINT64]
	case 'f':
		return types.Types[types.TFLOAT32]
	case 'd':
		return types.Types[types.TFLOAT64]
	}
	return types.Types[types.TUNSAFEPTR]
}

// wtype returns the WebAssembly type of values of type t: one of
// r, l, f and d.
func (g *gcGen) wtype(t *types.Type) byte {
	switch {
	case t.IsPtrShaped():
		return 'r'
	case t.IsFloat():
		if t.Size() == 4 {
			return 'f'
		}
		return 'd'
	case t.IsInteger(), t.IsBoolean():
		return 'l'
	case t.IsStruct():
		// A struct of one field (of size) is that field.
		var only *types.Type
		for _, f := range t.Fields() {
			if f.Type.Size() == 0 {
				continue
			}
			if only != nil {
				only = nil
				break
			}
			only = f.Type
		}
		if only != nil {
			return g.wtype(only)
		}
	case t.IsArray() && t.NumElem() == 1:
		return g.wtype(t.Elem())
	}
	g.unsupported(nil, "value of type "+t.String())
	return 0
}

// Instructions.

func (g *gcGen) prog(as obj.As) *obj.Prog { return g.s.Prog(as) }

func (g *gcGen) typed(as obj.As, sym *obj.LSym, n int64) *obj.Prog {
	p := g.s.Prog(as)
	p.To = obj.Addr{Type: obj.TYPE_MEM, Name: obj.NAME_EXTERN, Sym: sym, Offset: n}
	return p
}

func (g *gcGen) local(as obj.As, n int32) {
	p := g.s.Prog(as)
	p.From = obj.Addr{Type: obj.TYPE_CONST, Offset: int64(n)}
}

// newTypedLocal returns a local of a nullable reference to the type
// sym.
func (g *gcGen) newTypedLocal(sym *obj.LSym) int32 {
	g.locals = append(g.locals, 0)
	n := g.nparam + int32(len(g.locals)) - 1
	if g.localSyms == nil {
		g.localSyms = make(map[int32]*obj.LSym)
	}
	g.localSyms[n] = sym
	return n
}

// fillTyped sets the typed local of v, if it has one, after v is set.
func (g *gcGen) fillTyped(v *ssa.Value) {
	if la, ok := g.linLoc[v.ID]; ok {
		done := g.block('i')
		slow := g.block('r')
		g.linearAddr(v, slow)
		g.br(wasm.ABr, done, nil)
		g.prog(wasm.AEnd)
		g.prog(wasm.ADrop)
		i32Const(g.s, 0)
		g.prog(wasm.AEnd)
		g.local(wasm.ALocalSet, la)
	}
	tl, ok := g.typedLoc[v.ID]
	if !ok {
		return
	}
	g.prog(wasm.ARefNullNone)
	g.local(wasm.ALocalSet, tl)
	outer := g.block(0)
	notT := g.block('r')
	g.local(wasm.ALocalGet, g.loc[v.ID])
	g.br(wasm.ABrOnCastFail, notT, g.typedSym[v.ID])
	g.local(wasm.ALocalSet, tl)
	g.br(wasm.ABr, outer, nil)
	g.prog(wasm.AEnd)
	g.prog(wasm.ADrop)
	g.prog(wasm.AEnd)
}

// chooseTyped decides which pointers get a typed local.
func (g *gcGen) chooseTyped() {
	f := g.f
	// The stores that allocations do first.
	g.fusedAt = make(map[ssa.ID]map[int64]*ssa.Value)
	for _, b := range f.Blocks {
		for _, v := range b.Values {
			if aux, ok := v.Aux.(*ssa.AuxCall); ok && v.Op == ssa.OpWasmLoweredStaticCall && aux.Fn != nil && aux.Fn.Name == "runtime.newobject" {
				if t := g.constType(v.Args[0]); t != nil {
					g.fusedAt[v.ID] = g.fuseStores(v, t)
				}
			}
		}
	}
	uses := make(map[*ssa.Value]int)
	g.linLoc = make(map[ssa.ID]int32)
	count := func(addr *ssa.Value) {
		root, style := g.root(addr)
		if style == gcMemory && root.Type.IsPtr() && g.loc[root.ID] >= 0 && !root.OnWasmStack && !g.remat[root.ID] {
			if _, ok := g.linLoc[root.ID]; !ok {
				g.linLoc[root.ID] = g.newLocal('i')
			}
		}
		// (A new object is of its type for sure: the cast only says
		// so to the engine.)
		if (style == gcObject || style == gcStatic && gcIsNew(root)) && root.Type.IsPtr() && g.loc[root.ID] >= 0 && !root.OnWasmStack && !g.remat[root.ID] {
			uses[root]++
		}
	}
	for _, b := range f.Blocks {
		for _, v := range b.Values {
			switch v.Op {
			case ssa.OpStore:
				if !g.fused[v.ID] {
					count(v.Args[0])
				}
			case ssa.OpLoad, ssa.OpZero:
				count(v.Args[0])
			case ssa.OpMove:
				count(v.Args[0])
				count(v.Args[1])
			}
		}
	}
	g.typedLoc = make(map[ssa.ID]int32)
	g.typedSym = make(map[ssa.ID]*obj.LSym)
	for v, n := range uses {
		if n < 2 || v.Type.Elem().Size() == 0 {
			continue
		}
		sym := g.objSym(v.Type.Elem())
		g.typedLoc[v.ID] = g.newTypedLocal(sym)
		g.typedSym[v.ID] = sym
	}
}

func (g *gcGen) newLocal(kind byte) int32 {
	g.locals = append(g.locals, wasm.GCValueType(kind))
	return g.nparam + int32(len(g.locals)) - 1
}

func (g *gcGen) scratch(kind byte) int32 {
	p := &g.scratchI32
	switch kind {
	case 'r':
		p = &g.scratchRef
	case 'L':
		p, kind = &g.scratchI64, 'l'
	}
	if *p < 0 {
		*p = g.newLocal(kind)
	}
	return *p
}

func (g *gcGen) cast(sym *obj.LSym) { g.typed(wasm.ARefCast, sym, 0) }

// Values.

// through reports whether the address v is to be computed where it is
// used, rather than read from a local.
func (g *gcGen) through(v *ssa.Value) bool {
	if v.OnWasmStack {
		g.s.OnWasmStackSkipped--
		return true
	}
	return g.remat[v.ID]
}

// get pushes v. If want32, v is an integer and is pushed as an i32.
func (g *gcGen) get(v *ssa.Value, want32 bool) {
	switch {
	case v.OnWasmStack:
		g.s.OnWasmStackSkipped--
		g.onStack(v, !want32)
		if !want32 || gcIs32(v) {
			return
		}
	case g.remat[v.ID]:
		g.onStack(v, !want32)
		if !want32 || gcIs32(v) {
			return
		}
	default:
		if g.loc[v.ID] < 0 {
			g.unsupported(v, "value without a local")
		}
		g.local(wasm.ALocalGet, g.loc[v.ID])
		if !want32 {
			return
		}
	}
	if g.wtype(v.Type) != 'l' {
		g.unsupported(v, "32-bit use of a value that is not an integer")
	}
	g.prog(wasm.AI32WrapI64)
}

// gcIs32 reports whether onStack(v, false) leaves an i32.
func gcIs32(v *ssa.Value) bool {
	switch v.Op {
	case ssa.OpIsNonNil, ssa.OpEqPtr, ssa.OpNeqPtr:
		return true
	}
	return isAlready32(v)
}

// discard accounts for an operand that turned out not to be needed.
func (g *gcGen) discard(v *ssa.Value) {
	if v.OnWasmStack {
		g.s.OnWasmStackSkipped--
	}
}

// onStack computes v, leaving it on the stack. A comparison is left as
// an i32 unless extend is set.
func (g *gcGen) onStack(v *ssa.Value, extend bool) {
	s := g.s
	switch v.Op {
	case ssa.OpStructMake, ssa.OpArrayMake1:
		// Of a single field: the field.
		if len(v.Args) != 1 {
			g.unsupported(v, "struct value")
		}
		g.get(v.Args[0], false)
	case ssa.OpStructSelect, ssa.OpArraySelect:
		g.get(v.Args[0], false)
	case ssa.OpConstNil:
		if g.wtype(v.Type) == 'l' {
			i64Const(s, 0)
		} else {
			g.prog(wasm.ARefNull)
		}

	case ssa.OpIsNonNil:
		// (The operand is an integer if it is the type word of an
		// interface.)
		g.get(v.Args[0], false)
		if g.wtype(v.Args[0].Type) == 'l' {
			g.prog(wasm.AI64Eqz)
		} else {
			g.prog(wasm.ARefIsNull)
		}
		g.prog(wasm.AI32Eqz)
		if extend {
			g.prog(wasm.AI64ExtendI32U)
		}

	case ssa.OpEqPtr, ssa.OpNeqPtr:
		ints := g.wtype(v.Args[0].Type) == 'l' || g.wtype(v.Args[1].Type) == 'l'
		for _, a := range v.Args {
			g.get(a, false)
			if ints && g.wtype(a.Type) == 'r' {
				g.ptrToInt()
			}
		}
		switch {
		case ints:
			g.prog(wasm.AI64Eq)
		case v.Args[0].Op == ssa.OpConstNil || v.Args[1].Op == ssa.OpConstNil:
			g.prog(wasm.ARefEq)
		default:
			// The same place may be pointed to by different
			// pointers: an i31 and a GCLinear, two boxes.
			g.helper(wasm.GCPtrEq)
		}
		if v.Op == ssa.OpNeqPtr {
			g.prog(wasm.AI32Eqz)
		}
		if extend {
			g.prog(wasm.AI64ExtendI32U)
		}

	case ssa.OpLoad:
		if base, off := gcLinearAddr(v.Args[0]); base != nil {
			// A load from linear memory: of a type descriptor or
			// an itab, whose address is an integer.
			if g.wtype(base.Type) != 'l' {
				g.unsupported(v, "load from linear memory")
			}
			g.get(base, true)
			p := g.prog(loadOp(v.Type))
			p.From = obj.Addr{Type: obj.TYPE_CONST, Offset: off}
			if g.wtype(v.Type) == 'r' {
				g.intToPtr()
			}
			return
		}
		g.accessLoad(v.Args[0], 0, v.Type, v)

	case ssa.OpWasmLoweredGetClosurePtr:
		g.global(wasm.AGlobalGet, wasm.GCGlobalCtxt)

	case ssa.OpWasmLoweredGetCallerPC:
		// There are no program counters.
		p := g.prog(wasm.AI64Const)
		p.From = obj.Addr{Type: obj.TYPE_CONST, Offset: 0}

	case ssa.OpWasmLoweredGetCallerSP:
		// Where the caller's frame in linear memory ends.
		g.callerSP()

	case ssa.OpSP:
		// The bottom of the frame in linear memory.
		g.global(wasm.AGlobalGet, wasm.GCGlobalSP)
		g.prog(wasm.AI64ExtendI32U)

	case ssa.OpLocalAddr, ssa.OpAddr, ssa.OpOffPtr, ssa.OpPtrIndex, ssa.OpAddPtr:
		g.pointer(v)

	case ssa.OpCopy, ssa.OpWasmLoweredConvert, ssa.OpConvert:
		g.get(v.Args[0], false)
		switch from, to := g.wtype(v.Args[0].Type), g.wtype(v.Type); {
		case from == to:
		case from == 'r' && to == 'l':
			g.ptrToInt()
		case from == 'l' && to == 'r':
			g.intToPtr()
		default:
			g.unsupported(v, "conversion")
		}

	case ssa.OpWasmSelect:
		want := g.wtype(v.Type)
		if want != 'r' && want != 'l' {
			ssaGenValueOnStack(s, v, extend)
			return
		}
		for _, a := range v.Args[:2] {
			// An operand may be a pointer where the result is an
			// integer, or the other way round.
			g.get(a, false)
			switch from := g.wtype(a.Type); {
			case from == 'r' && want == 'l':
				g.ptrToInt()
			case from == 'l' && want == 'r':
				g.intToPtr()
			}
		}
		g.get(v.Args[2], true)
		if want == 'r' {
			g.prog(wasm.ARefSelect)
		} else {
			g.prog(wasm.ASelect)
		}

	case ssa.OpWasmI64DivS:
		// The most negative integer divided by -1 is itself, but
		// the instruction traps: x / -1 is 0 - x.
		g.get(v.Args[1], false)
		i64Const(s, -1)
		g.prog(wasm.AI64Eq)
		p := g.prog(wasm.AIf)
		p.From.Offset = 0x80 - wasm.GCTypeI64
		i64Const(s, 0)
		g.getAgain(v.Args[0], false)
		g.prog(wasm.AI64Sub)
		g.prog(wasm.AElse)
		g.getAgain(v.Args[0], false)
		g.getAgain(v.Args[1], false)
		g.prog(wasm.AI64DivS)
		g.prog(wasm.AEnd)

	default:
		if v.Op.IsGeneric() {
			g.unsupported(v, "op")
		}
		ssaGenValueOnStack(s, v, extend)
	}
}

// gcLinearAddr reports whether the address v is an offset from an
// integer, and so an address in linear memory.
func gcLinearAddr(v *ssa.Value) (base *ssa.Value, off int64) {
	for v.Op == ssa.OpOffPtr {
		off += v.AuxInt
		v = v.Args[0]
	}
	if v.Type.IsInteger() {
		return v, off
	}
	return nil, 0
}

// ptrToInt converts the pointer pushed to an integer: the address of a
// pointer to linear memory, the identity of an object (see wasm.GCPtrID).
func (g *gcGen) ptrToInt() {
	tmp := g.scratch('r')
	g.local(wasm.ALocalSet, tmp)
	done := g.block('l')
	i31 := g.typed(wasm.ARefBlock, nil, 0)
	g.local(wasm.ALocalGet, tmp)
	g.br(wasm.ABrOnCast, i31, nil)
	g.helper(wasm.GCPtrID)
	g.br(wasm.ABr, done, nil)
	g.prog(wasm.AEnd)
	g.prog(wasm.AI31GetU)
	g.prog(wasm.AI64ExtendI32U)
	g.prog(wasm.AEnd)
}

// intToPtr converts the integer pushed, an address in linear memory,
// to a pointer.
func (g *gcGen) intToPtr() {
	// 0 is nil.
	g.prog(wasm.AI32WrapI64)
	if g.scratchNil < 0 {
		g.scratchNil = g.newLocal('i')
	}
	g.local(wasm.ALocalTee, g.scratchNil)
	g.prog(wasm.ARefI31)
	g.prog(wasm.ARefNull)
	g.local(wasm.ALocalGet, g.scratchNil)
	g.prog(wasm.ARefSelect)
}

func (g *gcGen) global(as obj.As, n int64) {
	p := g.s.Prog(as)
	p.From = obj.Addr{Type: obj.TYPE_CONST, Offset: n}
}

// getAgain pushes v a second time: v must be in a local or
// recomputable.
func (g *gcGen) getAgain(v *ssa.Value, want32 bool) {
	if v.OnWasmStack {
		g.unsupported(v, "operand used twice")
	}
	g.get(v, want32)
}

// pointer pushes the pointer that the address v stands for.
func (g *gcGen) pointer(v *ssa.Value) {
	if root, style := g.root(v); style == gcFrame {
		// On the stack: the address.
		g.frameAddr(v, root, 0)
		g.prog(wasm.ARefI31)
		return
	}
	switch v.Op {
	case ssa.OpLocalAddr:
		g.local(wasm.ALocalGet, g.objs[v.Aux.(*ir.Name)])
		return
	case ssa.OpAddr:
		sym := v.Aux.(*obj.LSym)
		if g.wtype(v.Type) == 'l' {
			// The address of data in linear memory, or a function.
			p := g.prog(wasm.AI64Const)
			p.From = obj.Addr{Type: obj.TYPE_ADDR, Name: obj.NAME_EXTERN, Sym: sym}
			return
		}
		if sym.Gotype != nil && sym.Gotype.TypeInfo() != nil && sym.Gotype.TypeInfo().Type.(*types.Type).Size() == 0 && !gcLinear(sym) {
			// Every variable of size zero is at the same address.
			g.zerobase()
			return
		}
		if gcLinear(sym) {
			// Data in linear memory, or a function: the address, as
			// an i31.
			p := g.prog(wasm.AI32Const)
			p.From = obj.Addr{Type: obj.TYPE_ADDR, Name: obj.NAME_EXTERN, Sym: sym}
			g.prog(wasm.ARefI31)
			return
		}
		g.typed(wasm.ARefGlobalGet, sym, 0)
		return
	case ssa.OpOffPtr:
		g.get(v.Args[0], false)
		if v.AuxInt != 0 {
			off := int32(v.AuxInt)
			if v.Type.IsPtr() && v.Type.Elem().IsStruct() {
				off |= wasm.GCAddPtrStruct
			}
			i32Const(g.s, off)
			g.helper(wasm.GCAddPtr)
		}
		return
	case ssa.OpAddPtr:
		g.get(v.Args[0], false)
		g.get(v.Args[1], true)
		g.helper(wasm.GCAddPtr)
		return
	case ssa.OpPtrIndex:
		p, i, e := v.Args[0], v.Args[1], v.Type.Elem()
		at := gcIndexedArray(v)
		if at != nil && at.NumElem() < 2 {
			g.get(p, false)
			return
		}
		offset := func() {
			g.get(i, true)
			i32Const(g.s, int32(e.Size()))
			g.prog(wasm.AI32Mul)
			g.helper(wasm.GCAddPtr)
		}
		k, isObj := gcElem(e)
		switch {
		case !isObj, at == nil && !e.HasPointers():
			// A pointer into the array, or into linear memory.
			g.get(p, false)
			offset()
		case at == nil:
			// An element of a slice, which is an object, unless the
			// slice is static data in linear memory.
			idx := []*ssa.Value{i}
			for p.Op == ssa.OpPtrIndex && types.Identical(p.Type, v.Type) {
				g.through(p)
				idx = append(idx, p.Args[1])
				p = p.Args[0]
			}
			done := g.block('r')
			outerSlow1 := g.slow // (restored: the access may be within another)
			g.slow = g.block('r')
			g.sliceBase(p, e, k)
			g.slow = outerSlow1
			g.local(wasm.ALocalGet, g.scratch('i'))
			for _, x := range idx {
				g.get(x, true)
				g.prog(wasm.AI32Add)
			}
			g.typed(wasm.AArrayGet, g.arraySym(k), 0)
			g.castEq()
			g.br(wasm.ABr, done, nil)
			g.prog(wasm.AEnd)
			for j, x := range idx {
				g.get(x, true)
				if j > 0 {
					g.prog(wasm.AI32Add)
				}
			}
			i32Const(g.s, int32(e.Size()))
			g.prog(wasm.AI32Mul)
			g.helper(wasm.GCAddPtr)
			g.prog(wasm.AEnd)
		default:
			// An element of an array, which is an object, unless the
			// array is not one: it may be in linear memory.
			done := g.block('r')
			outerSlow2 := g.slow // (restored: the access may be within another)
			g.slow = g.block('r')
			g.loadRef(g.place(p, 0, at))
			g.slow = outerSlow2
			g.get(i, true)
			g.typed(wasm.AArrayGet, g.arraySym(k), 0)
			g.castEq()
			g.br(wasm.ABr, done, nil)
			g.prog(wasm.AEnd)
			offset()
			g.prog(wasm.AEnd)
		}
		return
	}
	g.unsupported(v, "address")
}

// block opens a block whose result is of the WebAssembly type the
// letter stands for (0 for none).
func (g *gcGen) block(result byte) *obj.Prog {
	p := g.prog(wasm.ABlock)
	if result != 0 {
		p.From.Offset = 0x80 - int64(wasm.GCValueType(result))
	}
	return p
}

func (g *gcGen) br(as obj.As, to *obj.Prog, sym *obj.LSym) {
	p := g.prog(as)
	p.To = obj.Addr{Type: obj.TYPE_BRANCH, Val: to}
	if sym != nil {
		p.From = obj.Addr{Type: obj.TYPE_MEM, Name: obj.NAME_EXTERN, Sym: sym}
	}
}

// castEq casts the reference pushed, an element of an array of
// objects, to an eqref.
func (g *gcGen) castEq() {
	p := g.prog(wasm.ARefCastNull)
	p.From.Offset = 2
}

// gcLinear reports whether the data of sym stays in linear memory
// rather than being an object: type descriptors and the like.
// gcLinearVar reports whether the global variable that v, an Addr, is
// the address of stays in linear memory: one without pointers. (The
// linker decides the same by the type of the variable.)
func gcLinearVar(v *ssa.Value) bool {
	sym := v.Aux.(*obj.LSym)
	if sym.Name == "runtime.zerobase" {
		return true // whatever the type it is the address of
	}
	if gcLinear(sym) || strings.HasSuffix(sym.Name, "·f") || !v.Type.IsPtr() {
		return false
	}
	// The type of the variable, which the type of the address may not
	// be (static initialization writes a field at an offset from the
	// symbol); or, for a variable of another package, that of the
	// address, which is the variable's.
	t := v.Type.Elem()
	if sym.Gotype != nil && sym.Gotype.TypeInfo() != nil {
		t = sym.Gotype.TypeInfo().Type.(*types.Type)
	}
	return t.Size() > 0 && !t.HasPointers()
}

func gcLinear(sym *obj.LSym) bool {
	if sym.Type == objabi.STEXT || sym.Func() != nil {
		return true
	}
	return sym.TypeInfo() != nil || strings.HasPrefix(sym.Name, "type:") || strings.HasPrefix(sym.Name, "go:itab.") || strings.HasPrefix(sym.Name, "go:string.") || sym.Name == "runtime.zeroVal" || strings.Contains(sym.Name, "..typeAssert.") || strings.Contains(sym.Name, "..interfaceSwitch.") || strings.Contains(sym.Name, "..dict.")
}

// Places.

// A gcPlace is where a load or a store goes, once place has pushed the
// object, and the index if it is an array.
type gcPlace struct {
	sym   *obj.LSym // the type of the object
	field int64     // the field of a struct, or one of the following
	kind  byte
	t     *types.Type // of kind a: the array type
}

const (
	gcElemPlace = -1 // an element: the array and the index were pushed
	gcSelfPlace = -2 // the object pushed is itself the array wanted
)

// place pushes the object that holds the scalar (or, if want is an
// array type, the array) at byte off of what the address v points to.
func (g *gcGen) place(v *ssa.Value, off int64, want *types.Type) gcPlace {
	switch v.Op {
	case ssa.OpOffPtr:
		if g.through(v) {
			return g.place(v.Args[0], off+v.AuxInt, want)
		}
	case ssa.OpPtrIndex:
		if !g.through(v) {
			break
		}
		p, e := v.Args[0], v.Type.Elem()
		if at := gcIndexedArray(v); at != nil && at.NumElem() < 2 {
			g.discard(v.Args[1])
			return g.place(p, off, want)
		}
		g.arrayAt2(v)
		return g.elem(e, off, want)
	case ssa.OpLocalAddr:
		n := v.Aux.(*ir.Name)
		g.local(wasm.ALocalGet, g.objs[n])
		g.cast(g.objSym(n.Type()))
		return g.descend(n.Type(), off, want, v)
	case ssa.OpAddr:
		sym := v.Aux.(*obj.LSym)
		if gcLinear(sym) {
			g.unsupported(v, "load from linear memory")
		}
		t := g.staticType(v)
		g.typed(wasm.ARefGlobalGet, sym, 0)
		g.cast(g.objSym(t))
		return g.descend(t, off, want, v)
	}
	var t *types.Type
	switch {
	case v.Type.IsPtr():
		t = v.Type.Elem()
	case want != nil && off == 0:
		// An unsafe.Pointer (a phi of them): to what is wanted
		// there, most likely.
		t = want
	default:
		g.unsupported(v, "address of an array through an unsafe pointer")
	}
	if tl, ok := g.typedLoc[v.ID]; ok && g.typedSym[v.ID] == g.objSym(t) && !v.OnWasmStack && !g.remat[v.ID] {
		// Cast once, where v is set.
		if g.slow != nil {
			ok := g.typed(wasm.ARefBlock, g.objSym(t), 0)
			g.local(wasm.ALocalGet, tl)
			g.br(wasm.ABrOnNonNull, ok, nil)
			g.get(v, false)
			g.br(wasm.ABr, g.slow, nil)
			g.prog(wasm.AEnd)
		} else {
			g.local(wasm.ALocalGet, tl)
			g.prog(wasm.ARefAsNonNull)
		}
		return g.descend(t, off, want, v)
	}
	g.get(v, false)
	if g.slow != nil {
		// Not the object expected: leave it to the caller.
		g.br(wasm.ABrOnCastFail, g.slow, g.objSym(t))
	} else {
		g.cast(g.objSym(t))
	}
	return g.descend(t, off, want, v)
}

// How an address is resolved (see root).
const (
	gcStatic   = iota // a variable: the object is known
	gcObject          // a pointer to an object, most likely
	gcMemory          // a pointer to linear memory, most likely
	gcElements        // a pointer to an element of an array of references
	gcUnknown         // an unsafe pointer
	gcFrame           // a variable on the stack in linear memory
)

// frameAddr pushes the address in linear memory of byte off of what
// the address addr, of something in a variable on the stack, points to.
func (g *gcGen) frameAddr(addr, root *ssa.Value, off int64) {
	if root.Op == ssa.OpAddr {
		// A variable in linear memory.
		p := g.prog(wasm.AI32Const)
		p.From = obj.Addr{Type: obj.TYPE_ADDR, Name: obj.NAME_EXTERN, Sym: root.Aux.(*obj.LSym)}
		g.byteOff(addr, root, off)
		g.prog(wasm.AI32Add)
		return
	}
	g.local(wasm.ALocalGet, g.fp)
	g.byteOff(addr, root, off+g.frame[root.Aux.(*ir.Name)])
	g.prog(wasm.AI32Add)
}

// root returns the pointer that the address v is an offset from, and
// how to resolve it.
func (g *gcGen) root(v *ssa.Value) (*ssa.Value, int) {
	style := gcObject
	for {
		switch v.Op {
		case ssa.OpOffPtr:
			v = v.Args[0]
			continue
		case ssa.OpPtrIndex:
			p := v.Args[0]
			if gcIndexedArray(v) == nil && style == gcObject {
				// An element of a slice.
				if v.Type.Elem().HasPointers() {
					// As arrayAt folds the indexes of p[i][j].
					for p.Op == ssa.OpPtrIndex && types.Identical(p.Type, v.Type) {
						p = p.Args[0]
					}
					return p, gcElements
				}
				style = gcMemory
			}
			v = p
			continue
		case ssa.OpAddPtr:
			if style == gcObject {
				style = gcUnknown
				if v.Type.IsPtr() && !v.Type.Elem().HasPointers() {
					style = gcMemory
				}
			}
			v = v.Args[0]
			continue
		case ssa.OpLocalAddr:
			if _, ok := g.frame[v.Aux.(*ir.Name)]; ok {
				return v, gcFrame
			}
			if style != gcObject {
				// Bytes of a variable that is an object, addressed
				// as memory.
				return v, gcUnknown
			}
			return v, gcStatic
		case ssa.OpAddr:
			if gcLinearVar(v) || gcLinear(v.Aux.(*obj.LSym)) && g.wtype(v.Type) != 'l' {
				// In linear memory, at a constant address.
				return v, gcFrame
			}
			if !gcLinear(v.Aux.(*obj.LSym)) {
				if style != gcObject {
					return v, gcUnknown
				}
				return v, gcStatic
			}
			if style == gcObject {
				style = gcUnknown
			}
		}
		break
	}
	if style == gcObject && !v.Type.IsPtr() {
		style = gcUnknown
	}
	if style == gcObject && gcIsNew(v) {
		// A new object is what its type says.
		style = gcStatic
	}
	return v, style
}

// gcIsNew reports whether v is the result of an allocation.
func gcIsNew(v *ssa.Value) bool {
	if v.Op != ssa.OpSelectN || v.AuxInt != 0 {
		return false
	}
	call := v.Args[0]
	if call.Op != ssa.OpWasmLoweredStaticCall {
		return false
	}
	aux, ok := call.Aux.(*ssa.AuxCall)
	return ok && aux.Fn != nil && aux.Fn.Name == "runtime.newobject" && call.Args[0].Op == ssa.OpAddr
}

// rootAt is root for the address of byte off of what v points to. If
// that is past the end of what the root points to, according to its
// type, the type is not to be believed: a closure is addressed as a
// *uint8, for one.
func (g *gcGen) rootAt(v *ssa.Value, off int64, k byte) (*ssa.Value, int) {
	root, style := g.root(v)
	if style != gcObject && style != gcStatic {
		return root, style
	}
	t := g.staticType(root)
	if style == gcObject {
		t = root.Type.Elem()
	}
	for a := v; a != root; a = a.Args[0] {
		switch a.Op {
		case ssa.OpOffPtr:
			off += a.AuxInt
		default:
			return root, style
		}
	}
	// The scalar there must be of the kind wanted (or an integer,
	// for a pointer to linear memory).
	have := gcKindAt(t, off)
	ok := have == k || have == 'l' && (k == 'r' || k == 'd') || have == 'd' && k == 'l' || have == 'w' && k == 'f' || have == 'f' && k == 'w'
	if !ok {
		style = gcUnknown
	}
	return root, style
}

// staticType returns the type of the variable whose address v is.
func (g *gcGen) staticType(v *ssa.Value) *types.Type {
	switch v.Op {
	case ssa.OpLocalAddr:
		return v.Aux.(*ir.Name).Type()
	case ssa.OpAddr:
		// The type of the variable, which the type of the address
		// may not be: the address of an array is also that of its
		// first element.
		if sym := v.Aux.(*obj.LSym); sym.Gotype != nil && sym.Gotype.TypeInfo() != nil {
			return sym.Gotype.TypeInfo().Type.(*types.Type)
		}
	}
	return v.Type.Elem()
}

// gcKindAt returns the kind of the scalar at byte off of a value of
// type t, or 0 if no scalar starts there.
func gcKindAt(t *types.Type, off int64) byte {
	for {
		if off < 0 || off >= t.Size() {
			return 0
		}
		if gcIsArray(t) {
			t, off = t.Elem(), off%t.Elem().Size()
			continue
		}
		var in *gcLeaf
		for _, l := range gcLeaves(t) {
			if l.off <= off && off < l.off+gcLeafSize(l) {
				in = &l
				break
			}
		}
		switch {
		case in == nil:
			return 0
		case in.kind == 'a':
			t, off = in.t, off-in.off
		case in.off == off:
			return in.kind
		default:
			return 0
		}
	}
}

// byteOff pushes, as an i32, the offset in bytes of the address v, plus
// off, from root.
func (g *gcGen) byteOff(v, root *ssa.Value, off int64) {
	var dyn []func()
loop:
	for v != root {
		switch v.Op {
		case ssa.OpOffPtr:
			off += v.AuxInt
		case ssa.OpPtrIndex:
			i, size := v.Args[1], v.Type.Elem().Size()
			if at := gcArrayOf(v.Args[0]); at == nil || at.NumElem() >= 2 {
				dyn = append(dyn, func() {
					g.get(i, true)
					i32Const(g.s, int32(size))
					g.prog(wasm.AI32Mul)
				})
			}
		case ssa.OpAddPtr:
			x := v.Args[1]
			dyn = append(dyn, func() { g.get(x, true) })
		default:
			break loop
		}
		v = v.Args[0]
	}
	i32Const(g.s, int32(off))
	for _, d := range dyn {
		d()
		g.prog(wasm.AI32Add)
	}
}

// linearAddr pushes the address in linear memory that the pointer p
// is, as an i32. If p is not a pointer to linear memory, it branches
// to slow with p.
func (g *gcGen) linearAddr(p *ssa.Value, slow *obj.Prog) {
	addr := g.block('i')
	i31 := g.typed(wasm.ARefBlock, nil, 0)
	lin := g.typed(wasm.ARefBlock, g.linearSym(), 0)
	g.get(p, false)
	g.br(wasm.ABrOnCast, lin, g.linearSym())
	g.br(wasm.ABrOnCast, i31, nil)
	g.br(wasm.ABr, slow, nil)
	g.prog(wasm.AEnd)
	g.typed(wasm.AStructGet, g.linearSym(), wasm.GCLinearAddr)
	g.br(wasm.ABr, addr, nil)
	g.prog(wasm.AEnd)
	g.prog(wasm.AI31GetU)
	g.prog(wasm.AEnd)
}

// linearBase is linearAddr, from the local that holds the address if p
// has one.
func (g *gcGen) linearBase(p *ssa.Value, slow *obj.Prog) {
	la, ok := g.linLoc[p.ID]
	if !ok || p.OnWasmStack || g.remat[p.ID] {
		g.linearAddr(p, slow)
		return
	}
	g.local(wasm.ALocalGet, la)
	g.prog(wasm.AI32Eqz)
	g.prog(wasm.AIf)
	g.get(p, false)
	g.br(wasm.ABr, slow, nil)
	g.prog(wasm.AEnd)
	g.local(wasm.ALocalGet, la)
}

// accessLoad pushes the scalar of type t at byte off of what the
// address addr points to.
func (g *gcGen) accessLoad(addr *ssa.Value, off int64, t *types.Type, v *ssa.Value) {
	k := g.scalar(t, v)
	root, style := g.rootAt(addr, off, k)
	// slowLoad loads through the pointer pushed, whatever it is.
	slowLoad := func() {
		g.byteOff(addr, root, off)
		g.helper(wasm.GCLoadPrefix + string(k))
		switch {
		case k == 'l' && g.wtype(t) == 'r':
			g.intToPtr()
		case !t.IsSigned():
		case k == 'b':
			g.prog(wasm.AI64Extend8S)
		case k == 'h':
			g.prog(wasm.AI64Extend16S)
		case k == 'w':
			g.prog(wasm.AI64Extend32S)
		}
	}
	switch style {
	case gcStatic:
		g.load(g.place(addr, off, t), t, v)
	case gcFrame:
		g.frameAddr(addr, root, off)
		p := g.prog(loadOp(t))
		p.From = obj.Addr{Type: obj.TYPE_CONST}
		if g.wtype(t) == 'r' {
			g.intToPtr()
		}
	case gcUnknown:
		g.get(root, false)
		slowLoad()
	case gcObject, gcElements:
		done := g.block(g.wtype(t))
		outerSlow3 := g.slow // (restored: the access may be within another)
		g.slow = g.block('r')
		pl := g.place(addr, off, t)
		g.slow = outerSlow3
		g.load(pl, t, v)
		g.br(wasm.ABr, done, nil)
		g.prog(wasm.AEnd)
		slowLoad()
		g.prog(wasm.AEnd)
	case gcMemory:
		done := g.block(g.wtype(t))
		slow := g.block('r')
		g.linearBase(root, slow)
		g.byteOff(addr, root, off)
		g.prog(wasm.AI32Add)
		p := g.prog(loadOp(t))
		p.From = obj.Addr{Type: obj.TYPE_CONST}
		if g.wtype(t) == 'r' {
			g.intToPtr()
		}
		g.br(wasm.ABr, done, nil)
		g.prog(wasm.AEnd)
		slowLoad()
		g.prog(wasm.AEnd)
	}
}

// accessStore stores what val pushes, a scalar of type t, at byte off
// of what the address addr points to.
func (g *gcGen) accessStore(addr *ssa.Value, off int64, t *types.Type, val func(want32 bool), v *ssa.Value) {
	k := g.scalar(t, v)
	root, style := g.rootAt(addr, off, k)
	slowStore := func() {
		g.byteOff(addr, root, off)
		val(false)
		if k == 'l' && g.wtype(t) == 'r' {
			g.ptrToInt()
		}
		g.helper(wasm.GCStorePrefix + string(k))
	}
	switch style {
	case gcStatic:
		g.store(g.place(addr, off, t), t, val, v)
	case gcFrame:
		g.frameAddr(addr, root, off)
		val(false)
		if g.wtype(t) == 'r' {
			g.ptrToInt()
		}
		p := g.prog(storeOp(t))
		p.To = obj.Addr{Type: obj.TYPE_CONST}
	case gcUnknown:
		g.get(root, false)
		slowStore()
	case gcObject, gcElements:
		done := g.block(0)
		outerSlow4 := g.slow // (restored: the access may be within another)
		g.slow = g.block('r')
		pl := g.place(addr, off, t)
		g.slow = outerSlow4
		g.again(func() { g.store(pl, t, val, v) })
		g.br(wasm.ABr, done, nil)
		g.prog(wasm.AEnd)
		slowStore()
		g.prog(wasm.AEnd)
	case gcMemory:
		done := g.block(0)
		slow := g.block('r')
		g.linearBase(root, slow)
		g.byteOff(addr, root, off)
		g.prog(wasm.AI32Add)
		g.again(func() { val(false) })
		if g.wtype(t) == 'r' {
			g.ptrToInt()
		}
		p := g.prog(storeOp(t))
		p.To = obj.Addr{Type: obj.TYPE_CONST}
		g.br(wasm.ABr, done, nil)
		g.prog(wasm.AEnd)
		slowStore()
		g.prog(wasm.AEnd)
	}
}

// rawLoad loads the field or element at pl, sign-extending an 8-bit or
// 16-bit one if signed. An integer narrower than 64 bits is left as an
// i32.
func (g *gcGen) rawLoad(pl gcPlace, signed bool) {
	as, asElem := wasm.AStructGet, wasm.AArrayGet
	if pl.kind == 'b' || pl.kind == 'h' {
		as, asElem = wasm.AStructGetU, wasm.AArrayGetU
		if signed {
			as, asElem = wasm.AStructGetS, wasm.AArrayGetS
		}
	}
	if pl.field == gcElemPlace {
		g.typed(asElem, pl.sym, 0)
	} else {
		g.typed(as, pl.sym, pl.field)
	}
}

// rawStore stores what val pushes in the field or element at pl.
func (g *gcGen) rawStore(pl gcPlace, val func()) {
	val()
	if pl.field == gcElemPlace {
		g.typed(wasm.AArraySet, pl.sym, 0)
	} else {
		g.typed(wasm.AStructSet, pl.sym, pl.field)
	}
}

// arrayAt pushes the array and the index (an i32) of the element that
// the PtrIndex v addresses. The PtrIndex is one that through said to
// compute here, or one whose operands are in locals.
func (g *gcGen) arrayAt(v *ssa.Value) { g.arrayAt2(v) }

func (g *gcGen) arrayAt2(v *ssa.Value) {
	p, i, e := v.Args[0], v.Args[1], v.Type.Elem()
	k, _ := gcElem(e)
	if at := gcIndexedArray(v); at != nil {
		// p points to an array.
		pl := g.place(p, 0, at)
		g.loadRef(pl)
		g.get(i, true)
		return
	}
	// p points to an element: of a slice, usually. Fold the indexes
	// of p[i][j].
	idx := []*ssa.Value{i}
	for p.Op == ssa.OpPtrIndex && types.Identical(p.Type, v.Type) {
		// (Folded even if p is in a local: as an element that is an
		// object, p would not know the array it is in.)
		g.through(p)
		idx = append(idx, p.Args[1])
		p = p.Args[0]
	}
	g.sliceBase(p, e, k)
	g.local(wasm.ALocalGet, g.scratch('i'))
	for j := len(idx) - 1; j >= 0; j-- {
		g.get(idx[j], true)
		g.prog(wasm.AI32Add)
	}
}

// sliceBase pushes the array that the element pointer p points into,
// and sets the i32 scratch local to the index of the element.
func (g *gcGen) sliceBase(p *ssa.Value, e *types.Type, k byte) {
	arr := g.arraySym(k)
	idx := g.scratch('i')
	blk := g.typed(wasm.ARefBlock, arr, 0)
	i32Const(g.s, 0)
	g.local(wasm.ALocalSet, idx)
	g.get(p, false)
	br := g.prog(wasm.ABrOnCast)
	br.From = obj.Addr{Type: obj.TYPE_MEM, Name: obj.NAME_EXTERN, Sym: arr}
	br.To = obj.Addr{Type: obj.TYPE_BRANCH, Val: blk}
	// Not an array: a box of an array and a byte offset.
	box := g.boxSym()
	ref := g.scratch('r')
	if g.slow != nil {
		// Nor a box: leave it to the caller. It may be static data
		// in linear memory.
		g.br(wasm.ABrOnCastFail, g.slow, box)
	} else {
		g.cast(box)
	}
	g.local(wasm.ALocalTee, ref)
	g.cast(box)
	g.typed(wasm.AStructGet, box, wasm.GCBoxOffset)
	i32Const(g.s, int32(e.Size()))
	g.prog(wasm.AI32DivU)
	g.local(wasm.ALocalSet, idx)
	g.local(wasm.ALocalGet, ref)
	g.cast(box)
	g.typed(wasm.AStructGet, box, wasm.GCBoxObject)
	if g.slow != nil {
		// A box of something else, such as a struct whose field is
		// an array of one element: leave it to the caller.
		g.br(wasm.ABrOnCast, blk, arr)
		g.prog(wasm.ADrop)
		g.local(wasm.ALocalGet, ref)
		g.br(wasm.ABr, g.slow, nil)
	} else {
		g.cast(arr)
	}
	g.prog(wasm.AEnd)
}

// elem continues place from the array and index pushed, into the
// element of type e.
func (g *gcGen) elem(e *types.Type, off int64, want *types.Type) gcPlace {
	k, isObj := gcElem(e)
	if !isObj {
		if off != 0 {
			g.unsupported(nil, "access to part of an array element")
		}
		return gcPlace{sym: g.arraySym(k), field: -1, kind: k}
	}
	g.typed(wasm.AArrayGet, g.arraySym(k), 0)
	g.cast(g.objSym(e))
	return g.descend(e, off, want, nil)
}

// descend continues place from the object of type t pushed.
func (g *gcGen) descend(t *types.Type, off int64, want *types.Type, v *ssa.Value) gcPlace {
	wantArray := want != nil && gcIsArray(want)
	for {
		if gcIsArray(t) {
			if wantArray && off == 0 && t.Size() == want.Size() {
				return gcPlace{sym: g.objSym(t), field: -2, kind: 'a', t: t}
			}
			e := t.Elem()
			i32Const(g.s, int32(off/e.Size()))
			return g.elem(e, off%e.Size(), want)
		}
		ls := gcLeaves(t)
		i := 0
		for i < len(ls) && !(ls[i].off <= off && off < ls[i].off+gcLeafSize(ls[i])) {
			i++
		}
		if i == len(ls) {
			g.unsupported(v, "access to padding")
		}
		l := ls[i]
		sym := g.structSym(t)
		field := int64(i) + wasm.GCHeaderFields
		if l.kind == 'a' {
			if wantArray && off == l.off && l.t.Size() == want.Size() {
				return gcPlace{sym: sym, field: field, kind: 'a', t: l.t}
			}
			g.typed(wasm.AStructGet, sym, field)
			g.cast(g.objSym(l.t))
			t, off = l.t, off-l.off
			continue
		}
		if off != l.off {
			g.unsupported(v, "access to part of a field")
		}
		return gcPlace{sym: sym, field: field, kind: l.kind}
	}
}

// loadRef loads the array at pl, typed.
func (g *gcGen) loadRef(pl gcPlace) {
	if pl.t == nil {
		// Part of an array, as an array (a slice of it converted to
		// an array pointer): leave it to the caller, if it can.
		if g.slow == nil {
			g.unsupported(nil, "an array where there is a scalar")
			return
		}
		if pl.field == gcElemPlace {
			g.prog(wasm.ADrop) // the index
		}
		g.br(wasm.ABr, g.slow, nil)
		return
	}
	switch pl.field {
	case gcSelfPlace:
		return
	case gcElemPlace:
		g.typed(wasm.AArrayGet, pl.sym, 0)
	default:
		g.typed(wasm.AStructGet, pl.sym, pl.field)
	}
	g.cast(g.objSym(pl.t))
}

// load loads the value of type t at pl.
func (g *gcGen) load(pl gcPlace, t *types.Type, v *ssa.Value) {
	if pl.kind == 'a' {
		g.unsupported(v, "load of an array")
	}
	conv := g.checkKind(pl, t, v)
	g.rawLoad(pl, t.IsSigned())
	switch pl.kind {
	case 'b', 'h', 'w':
		if conv == wasm.AF32ReinterpretI32 {
			break
		}
		if t.IsSigned() {
			g.prog(wasm.AI64ExtendI32S)
		} else {
			g.prog(wasm.AI64ExtendI32U)
		}
	}
	switch conv {
	case 0:
	case gcAsInt:
		g.ptrToInt()
	case wasm.ARefI31:
		g.intToPtr()
	case wasm.AI32ReinterpretF32:
		g.prog(conv)
		g.prog(wasm.AI64ExtendI32U)
	default:
		g.prog(conv)
	}
}

// gcAsInt is what checkKind returns for a pointer accessed as an integer.
const gcAsInt obj.As = -2

// checkKind checks that a value of type t can be loaded from or stored
// to pl, and returns the instruction that converts what is loaded, if
// the field is not of the kind of t:
//   - ARefI31 if t is a pointer and the field an integer: the pointer is
//     one to linear memory, kept as its address;
//   - a reinterpretation if one is a float and the other an integer of
//     the same size: the payload of an interface is kept as floats.
func (g *gcGen) checkKind(pl gcPlace, t *types.Type, v *ssa.Value) obj.As {
	w := g.wtype(t)
	switch pl.kind {
	case 'l':
		switch {
		case t.Size() != 8:
		case w == 'l':
			return 0
		case w == 'r':
			return wasm.ARefI31
		case w == 'd':
			return wasm.AF64ReinterpretI64
		}
	case 'd':
		switch {
		case w == 'd':
			return 0
		case w == 'l' && t.Size() == 8:
			return wasm.AI64ReinterpretF64
		}
	case 'f':
		switch {
		case w == 'f':
			return 0
		case w == 'l' && t.Size() == 4:
			return wasm.AI32ReinterpretF32
		}
	case 'w':
		switch {
		case w == 'l' && t.Size() == 4:
			return 0
		case w == 'f':
			return wasm.AF32ReinterpretI32
		}
	case 'b', 'h':
		if w == 'l' && t.Size() == gcLeafSize(gcLeaf{kind: pl.kind}) {
			return 0
		}
	case 'r':
		switch w {
		case 'r':
			return 0
		case 'l':
			// A pointer as a uintptr.
			return gcAsInt
		}
	default:
		if w == pl.kind {
			return 0
		}
	}
	g.unsupported(v, "access to a field of kind "+string(pl.kind)+" as "+t.String())
	return 0
}

// store stores val, of type t, at pl.
func (g *gcGen) store(pl gcPlace, t *types.Type, val func(want32 bool), v *ssa.Value) {
	conv := g.checkKind(pl, t, v)
	g.rawStore(pl, func() {
		switch conv {
		case 0:
			val(pl.kind == 'b' || pl.kind == 'h' || pl.kind == 'w')
		case wasm.ARefI31:
			val(false)
			g.ptrToInt()
		case gcAsInt:
			val(false)
			g.intToPtr()
		case wasm.AF64ReinterpretI64: // a float in an integer field
			val(false)
			g.prog(wasm.AI64ReinterpretF64)
		case wasm.AI64ReinterpretF64: // an integer in a float field
			val(false)
			g.prog(wasm.AF64ReinterpretI64)
		case wasm.AF32ReinterpretI32:
			val(false)
			g.prog(wasm.AI32ReinterpretF32)
		case wasm.AI32ReinterpretF32:
			val(true)
			g.prog(wasm.AF32ReinterpretI32)
		}
	})
}

// zero pushes the zero value of a field of the kind given.
func (g *gcGen) zero(kind byte) {
	switch kind {
	case 'b', 'h', 'w':
		i32Const(g.s, 0)
	case 'l':
		i64Const(g.s, 0)
	case 'f':
		f32Const(g.s, 0)
	case 'd':
		f64Const(g.s, 0)
	default:
		g.prog(wasm.ARefNull)
	}
}

// fuseStores returns the stores that set fields of the object that the
// call v allocates, of type t, before anything else can see it, by the
// offset of the field: newObjectInit does them as it makes the object
// (struct.new), which saves a cast and a write per field.
func (g *gcGen) fuseStores(v *ssa.Value, t *types.Type) map[int64]*ssa.Value {
	if t.Size() == 0 || gcIsArray(t) {
		return nil
	}
	b := v.Block
	pos := make(map[*ssa.Value]int, len(b.Values))
	for i, u := range b.Values {
		pos[u] = i
	}
	var ptr *ssa.Value
	for _, u := range b.Values {
		if u.Op == ssa.OpSelectN && u.Args[0] == v && u.AuxInt == 0 {
			ptr = u
		}
	}
	if ptr == nil {
		return nil
	}
	leaves := make(map[int64]gcLeaf)
	for _, l := range gcLeaves(t) {
		leaves[l.off] = l
	}
	// The pointer, and what checks it is not nil (which it is not).
	alias := map[*ssa.Value]bool{ptr: true}
	// uses reports whether u uses ptr other than as the address of a
	// store.
	uses := func(u *ssa.Value) bool {
		for _, a := range u.Args {
			if alias[a] {
				return true
			}
			if a.Op == ssa.OpOffPtr && alias[a.Args[0]] && !(u.Op == ssa.OpStore && a == u.Args[0]) {
				return true
			}
		}
		return false
	}
	// self reports whether w is computed from ptr, which does not
	// exist before the allocation: &l.root, stored in l.root.next.
	var self func(w *ssa.Value) bool
	self = func(w *ssa.Value) bool {
		if alias[w] {
			return true
		}
		if !g.remat[w.ID] {
			return false
		}
		for _, a := range w.Args {
			if self(a) {
				return true
			}
		}
		return false
	}
	var fused map[int64]*ssa.Value
	for _, u := range b.Values[pos[v]+1:] {
		if u == ptr || u.Op == ssa.OpSelectN && u.Args[0] == v {
			continue
		}
		if (u.Op == ssa.OpNilCheck || u.Op == ssa.OpWasmLoweredNilCheck) && alias[u.Args[0]] {
			alias[u] = true
			continue
		}
		if u.Op == ssa.OpOffPtr && alias[u.Args[0]] && g.remat[u.ID] {
			continue // an address, computed where it is used
		}
		if u.Op == ssa.OpStore {
			addr, off := u.Args[0], int64(0)
			if addr.Op == ssa.OpOffPtr && alias[addr.Args[0]] {
				off, addr = addr.AuxInt, ptr
			} else if alias[addr] {
				addr = ptr
			}
			w := u.Args[1]
			l, ok := leaves[off]
			st := u.Aux.(*types.Type)
			if addr == ptr && ok && l.kind != 'a' && fused[off] == nil && st.Size() == gcLeafSize(l) && !w.OnWasmStack && !self(w) &&
				(w.Block != b || pos[w] < pos[v] || g.remat[w.ID]) && gcFusible(l.kind, g.wtype(st), g.wtype(w.Type)) {
				if fused == nil {
					fused = make(map[int64]*ssa.Value)
				}
				fused[off] = u
				continue
			}
		}
		if uses(u) {
			break
		}
	}
	for _, u := range fused {
		g.fused[u.ID] = true
	}
	return fused
}

// gcFusible reports whether a store of a value of WebAssembly type w as
// a value of type st can go into a field of kind k as it is made.
func gcFusible(k, st, w byte) bool {
	switch k {
	case 'r':
		return st == 'r' && w == 'r'
	case 'l', 'b', 'h', 'w':
		return st == 'l' && w == 'l'
	case 'f', 'd':
		return st == k && w == k
	}
	return false
}

// newObjectInit is newObject with the fields that the stores in fused
// set, by offset.
func (g *gcGen) newObjectInit(t *types.Type, fused map[int64]*ssa.Value) {
	if len(fused) == 0 {
		g.newObject(t)
		return
	}
	sym := g.structSym(t)
	g.typed(wasm.ATypeConst, sym, 0) // the layout
	i32Const(g.s, 0)
	for _, l := range gcLeaves(t) {
		switch u := fused[l.off]; {
		case l.kind == 'a':
			g.newObject(l.t)
		case u != nil:
			g.get(u.Args[1], l.kind == 'b' || l.kind == 'h' || l.kind == 'w')
		default:
			g.zero(l.kind)
		}
	}
	g.typed(wasm.AStructNew, sym, 0)
}

// typeDesc pushes a pointer to the type descriptor of t, which is in
// linear memory.
func (g *gcGen) typeDesc(t *types.Type) {
	p := g.prog(wasm.AI32Const)
	p.From = obj.Addr{Type: obj.TYPE_ADDR, Name: obj.NAME_EXTERN, Sym: reflectdata.TypeLinksym(g.s.GD(), t)}
	g.prog(wasm.ARefI31)
}

// zerobase pushes the address of runtime.zerobase, which is in linear
// memory.
func (g *gcGen) zerobase() {
	p := g.prog(wasm.AI32Const)
	p.From = obj.Addr{Type: obj.TYPE_ADDR, Name: obj.NAME_EXTERN, Sym: g.ctxt.Lookup("runtime.zerobase")}
	g.prog(wasm.ARefI31)
}

// newObject pushes a new, zeroed object of type t.
func (g *gcGen) newObject(t *types.Type) {
	if t.Size() == 0 {
		// Every variable of size zero is at the same address.
		g.zerobase()
		return
	}
	if gcIsArray(t) {
		k, isObj := gcElem(t.Elem())
		n := t.NumElem()
		if !isObj {
			i32Const(g.s, int32(n))
			g.typed(wasm.AArrayNewDefault, g.arraySym(k), 0)
			return
		}
		if n > 1000 {
			// Made by the runtime, element by element.
			g.typeDesc(t.Elem())
			i64Const(g.s, n)
			g.runtime("runtime.newobjarrayT")
			g.cast(g.objSym(t))
			return
		}
		for i := int64(0); i < n; i++ {
			g.newObject(t.Elem())
		}
		g.typed(wasm.AArrayNewFixed, g.arraySym(k), n)
		return
	}
	sym := g.structSym(t)
	g.typed(wasm.ATypeConst, sym, 0) // the layout
	i32Const(g.s, 0)
	for _, l := range gcLeaves(t) {
		if l.kind == 'a' {
			g.newObject(l.t)
		} else {
			g.zero(l.kind)
		}
	}
	g.typed(wasm.AStructNew, sym, 0)
}

// gcEach calls do for every scalar of a value of type t, with its
// offset, and for every nested array too large to go through element
// by element.
func gcEach(t *types.Type, do func(l gcLeaf)) {
	gcEachAt(t, 0, do)
}

func gcEachAt(t *types.Type, off int64, do func(l gcLeaf)) {
	if gcIsArray(t) {
		e := t.Elem()
		n := 0
		gcEachAt(e, 0, func(gcLeaf) { n++ })
		if int64(n)*t.NumElem() > 64 {
			do(gcLeaf{off: off, kind: 'a', t: t})
			return
		}
		for i := int64(0); i < t.NumElem(); i++ {
			gcEachAt(e, off+i*e.Size(), do)
		}
		return
	}
	for _, l := range gcLeaves(t) {
		if l.kind == 'a' {
			gcEachAt(l.t, off+l.off, do)
			continue
		}
		l.off += off
		do(l)
	}
}

// again runs gen, which computes operands left on the stack for it,
// without counting them as consumed: they are computed once more.
func (g *gcGen) again(gen func()) {
	saved := g.s.OnWasmStackSkipped
	gen()
	g.s.OnWasmStackSkipped = saved
}

// zeroValue zeroes the value of type t at the address v.
func (g *gcGen) zeroValue(v *ssa.Value, t *types.Type) {
	var parts []gcLeaf
	gcEach(t, func(l gcLeaf) { parts = append(parts, l) })
	for i, l := range parts {
		gen := func() {
			if root, style := g.root(v.Args[0]); l.kind == 'a' && style == gcFrame {
				g.frameAddr(v.Args[0], root, l.off)
				i32Const(g.s, 0)
				i32Const(g.s, int32(l.t.Size()))
				g.prog(wasm.AMemoryFill)
				return
			}
			if l.kind == 'a' {
				// The array, most likely; or else (through a box of
				// what it is in) by its type, in the runtime.
				k, isObj := gcElem(l.t.Elem())
				done := g.block(0)
				outerSlow5 := g.slow // (restored: the access may be within another)
				g.slow = g.block('r')
				g.loadRef(g.place(v.Args[0], l.off, l.t))
				g.slow = outerSlow5
				if isObj {
					g.helper(wasm.GCClear)
				} else {
					i32Const(g.s, 0)
					g.zero(k)
					i32Const(g.s, int32(l.t.NumElem()))
					g.typed(wasm.AArrayFill, g.arraySym(k), 0)
				}
				g.br(wasm.ABr, done, nil)
				g.prog(wasm.AEnd)
				g.prog(wasm.ADrop)
				g.typeDesc(l.t)
				g.pointerAt(v.Args[0], l.off)
				g.runtime("runtime.typedmemclr")
				g.prog(wasm.AEnd)
				return
			}
			t := gcKindType(l.kind)
			g.accessStore(v.Args[0], l.off, t, func(want32 bool) {
				if want32 {
					i32Const(g.s, 0)
				} else {
					g.zero(g.wtype(t))
				}
			}, v)
		}
		if i < len(parts)-1 {
			g.again(gen)
		} else {
			gen()
		}
	}
	if len(parts) == 0 {
		g.discard(v.Args[0])
	}
}

// pointerAt pushes a pointer to byte off of what the address v points
// to.
func (g *gcGen) pointerAt(v *ssa.Value, off int64) {
	g.get(v, false)
	if off != 0 {
		i32Const(g.s, int32(off))
		g.helper(wasm.GCAddPtr)
	}
}

// moveValue copies the value of type t from the address v.Args[1] to
// the address v.Args[0].
func (g *gcGen) moveValue(v *ssa.Value, t *types.Type) {
	var parts []gcLeaf
	gcEach(t, func(l gcLeaf) { parts = append(parts, l) })
	for i, l := range parts {
		gen := func() {
			if l.kind == 'a' {
				droot, dstyle := g.root(v.Args[0])
				sroot, sstyle := g.root(v.Args[1])
				if dstyle == gcFrame && sstyle == gcFrame {
					g.frameAddr(v.Args[0], droot, l.off)
					g.frameAddr(v.Args[1], sroot, l.off)
					i32Const(g.s, int32(l.t.Size()))
					g.prog(wasm.AMemoryCopy)
					return
				}
				if dstyle == gcFrame || sstyle == gcFrame {
					// Between the stack and an object: a byte
					// at a time.
					g.pointerAt(v.Args[0], l.off)
					g.pointerAt(v.Args[1], l.off)
					i64Const(g.s, l.t.Size())
					g.runtime("runtime.memmove")
					return
				}
				k, isObj := gcElem(l.t.Elem())
				if isObj {
					g.loadRef(g.place(v.Args[1], l.off, l.t))
					g.loadRef(g.place(v.Args[0], l.off, l.t))
					g.helper(wasm.GCCopy)
					return
				}
				// Arrays, most likely; or else anything a pointer
				// is, as in linear memory: a byte at a time.
				done := g.block(0)
				outerSlow6 := g.slow // (restored: the access may be within another)
				g.slow = g.block('r')
				g.loadRef(g.place(v.Args[0], l.off, l.t))
				i32Const(g.s, 0)
				g.loadRef(g.place(v.Args[1], l.off, l.t))
				g.slow = outerSlow6
				i32Const(g.s, 0)
				i32Const(g.s, int32(l.t.NumElem()))
				g.typed(wasm.AArrayCopy, g.arraySym(k), 0)
				g.br(wasm.ABr, done, nil)
				g.prog(wasm.AEnd)
				g.prog(wasm.ADrop)
				g.pointerAt(v.Args[0], l.off)
				g.pointerAt(v.Args[1], l.off)
				i64Const(g.s, l.t.Size())
				g.runtime("runtime.memmove")
				g.prog(wasm.AEnd)
				return
			}
			t := gcKindType(l.kind)
			g.accessStore(v.Args[0], l.off, t, func(want32 bool) {
				g.accessLoad(v.Args[1], l.off, t, v)
				if want32 {
					g.prog(wasm.AI32WrapI64)
				}
			}, v)
		}
		if i < len(parts)-1 {
			g.again(gen)
		} else {
			gen()
		}
	}
	if len(parts) == 0 {
		g.discard(v.Args[0])
		g.discard(v.Args[1])
	}
}

// Calls.

// zeroObject returns the symbol of a variable of type t that is never
// written: the zero value, as an object.
func (g *gcGen) zeroObject(t *types.Type) *obj.LSym {
	gd := g.s.GD()
	tsym := reflectdata.TypeLinksym(gd, t)
	return g.ctxt.LookupInit("wasm.zero."+tsym.Name, func(s *obj.LSym) {
		s.Gotype = tsym
		g.ctxt.Globl(s, t.Size(), obj.DUPOK)
	})
}

// runtime calls the function of the runtime named.
func (g *gcGen) runtime(name string) {
	p := g.prog(obj.ACALL)
	p.To = obj.Addr{Type: obj.TYPE_MEM, Name: obj.NAME_EXTERN, Sym: g.ctxt.LookupABI(name, obj.ABIInternal)}
}

// args pushes the arguments of a call, converting between pointer and
// integer where the callee declares the other: the compiler has the
// word of an interface that holds its type as an integer, and the
// runtime as a pointer.
func (g *gcGen) args(aux *ssa.AuxCall, args []*ssa.Value) {
	g.argsFrom(aux, args, 0)
}

// argsFrom is args for the arguments from the one numbered first.
func (g *gcGen) argsFrom(aux *ssa.AuxCall, args []*ssa.Value, first int) {
	want := abi.RegisterTypes(aux.ABIInfo().InParams())
	want = want[min(first, len(want)):]
	for i, a := range args {
		g.get(a, false)
		if i >= len(want) {
			continue
		}
		switch have, w := g.wtype(a.Type), g.wtype(want[i]); {
		case have == 'l' && w == 'r':
			g.intToPtr()
		case have == 'r' && w == 'l':
			g.ptrToInt()
		}
	}
}

// constType returns the type whose descriptor the call argument v is
// the address of, or nil if it is not a constant.
func (g *gcGen) constType(v *ssa.Value) *types.Type {
	if v.Op == ssa.OpAddr {
		if ti := v.Aux.(*obj.LSym).TypeInfo(); ti != nil {
			return ti.Type.(*types.Type)
		}
	}
	return nil
}

// typeArg returns the type whose descriptor the call argument v is the
// address of.
func (g *gcGen) typeArg(v *ssa.Value) *types.Type {
	if v.Op == ssa.OpAddr {
		if ti := v.Aux.(*obj.LSym).TypeInfo(); ti != nil {
			return ti.Type.(*types.Type)
		}
	}
	g.unsupported(v, "type that is not a constant")
	return nil
}

// gcUsesTypeTwice reports whether the call v may push its first
// argument, a type, twice (see zeroElemArg).
func gcUsesTypeTwice(v *ssa.Value) bool {
	if v.Op != ssa.OpWasmLoweredStaticCall {
		return false
	}
	aux, ok := v.Aux.(*ssa.AuxCall)
	if !ok || aux.Fn == nil {
		return false
	}
	switch aux.Fn.Name {
	case "runtime.makechan", "runtime.makechan64", "runtime.mapaccess1", "runtime.mapaccess2", "runtime.mapassign":
		return true
	}
	return false
}

// zeroElemArg pushes the zero value of the elements of the map or
// channel type whose descriptor t is the address of, if they are
// objects, or null: the object is a variable never written if the
// type is known, or else one the runtime makes.
func (g *gcGen) zeroElemArg(t *ssa.Value) {
	if t.Op == ssa.OpAddr {
		if ti := t.Aux.(*obj.LSym).TypeInfo(); ti != nil {
			if e := ti.Type.(*types.Type).Elem(); e.HasPointers() {
				g.typed(wasm.ARefGlobalGet, g.zeroObject(e), 0)
			} else {
				g.prog(wasm.ARefNull)
			}
			return
		}
	}
	g.getAgain(t, false)
	g.runtime("runtime.zeroelem")
}

func (g *gcGen) call(v *ssa.Value) {
	aux := v.Aux.(*ssa.AuxCall)
	args := v.Args[:len(v.Args)-1]
	switch v.Op {
	case ssa.OpWasmLoweredStaticCall:
		if aux.Fn == nil {
			g.unsupported(v, "call")
		}
	case ssa.OpWasmLoweredTailCall:
		// The results of the call are those of the function.
		if len(gcStackParams(aux.ABIInfo())) > 0 {
			g.unsupported(v, "tail call with arguments not in registers")
		}
		g.args(aux, args)
		p := g.prog(obj.ACALL)
		p.To = obj.Addr{Type: obj.TYPE_MEM, Name: obj.NAME_EXTERN, Sym: aux.Fn}
		p.Pos = v.Pos
		g.popFrame()
		g.prog(obj.ARET)
		return
	case ssa.OpWasmLoweredClosureCall, ssa.OpWasmLoweredInterCall:
		// args[0] is the function: its index in the table, shifted
		// as in a program counter (see the linker).
		code := args[0]
		args = args[1:]
		if v.Op == ssa.OpWasmLoweredClosureCall {
			g.get(args[0], false)
			g.global(wasm.AGlobalSet, wasm.GCGlobalCtxt)
			args = args[1:]
		}
		g.args(aux, args)
		g.slotArgs(v)
		g.get(code, false)
		i64Const(g.s, 16)
		g.prog(wasm.AI64ShrU)
		g.prog(wasm.AI32WrapI64)
		p := g.typed(wasm.ACallIndirect, g.sigOf(aux.ABIInfo()), 0)
		p.Pos = v.Pos
		g.results(v)
		return
	default:
		g.unsupported(v, "tail call")
	}
	fn := aux.Fn
	name := fn.Name
	if name == "runtime.memclrNoHeapPointers" {
		// Objects (structs, arrays of structs) are cleared by type.
		p := args[0]
		for (p.Op == ssa.OpCopy || p.Op == ssa.OpConvert) && p.Type.IsUnsafePtr() {
			p = p.Args[0]
		}
		if p.Type.IsPtr() && p.Type.Elem().Size() > 0 {
			e := p.Type.Elem()
			for e.IsArray() {
				e = e.Elem()
			}
			if _, isObj := gcElem(e); isObj {
				name = "runtime.memclrHasPointers"
			}
		}
	}
	switch name {
	case "runtime.abort":
		g.prog(obj.AUNDEF)
	case "runtime.linsp":
		g.global(wasm.AGlobalGet, wasm.GCGlobalSP)
		g.prog(wasm.AI64ExtendI32U)
	case "runtime.gcthrow":
		g.prog(wasm.ATHROW).From = obj.Addr{Type: obj.TYPE_CONST, Offset: wasm.GCTagRecover}
	case "runtime.selectgo":
		// The cases are passed as a pointer to the first, which is
		// an object: pass the array, which a slice can be made of.
		if c := args[0]; c.Op == ssa.OpPtrIndex && gcArrayOf(c.Args[0]) != nil && gcArrayOf(c.Args[0]).NumElem() >= 2 {
			g.get(c.Args[0], false)
			g.argsFrom(aux, args[1:], 1)
		} else {
			g.args(aux, args)
		}
		p := g.prog(obj.ACALL)
		p.To = obj.Addr{Type: obj.TYPE_MEM, Name: obj.NAME_EXTERN, Sym: fn}
		p.Pos = v.Pos
	case "runtime.newproc":
		// (The function is declared an integer to the compiler: it
		// is passed as the pointer it is.)
		g.get(args[0], false)
		g.runtime("runtime.newproc")
	case "runtime.makechan", "runtime.makechan64":
		// The runtime is given the zero value of an element that
		// is an object (unless it is the runtime that calls).
		g.args(aux, args)
		if len(args) == 2 {
			g.zeroElemArg(args[0])
		}
		p := g.prog(obj.ACALL)
		p.To = obj.Addr{Type: obj.TYPE_MEM, Name: obj.NAME_EXTERN, Sym: fn}
		p.Pos = v.Pos
	case "runtime.gccopy":
		g.get(args[1], false)
		g.get(args[0], false)
		g.helper(wasm.GCCopy)
	case "runtime.linsetsplim":
		g.get(args[0], true)
		g.global(wasm.AGlobalSet, wasm.GCGlobalStackLo)
	case "runtime.linsplim":
		g.global(wasm.AGlobalGet, wasm.GCGlobalStackLo)
		g.prog(wasm.AI64ExtendI32U)
	case "runtime.linsetsp":
		g.get(args[0], true)
		g.global(wasm.AGlobalSet, wasm.GCGlobalSP)
	case "runtime.deferproc", "runtime.deferreturn":
		// The runtime is told whose deferred calls they are, and
		// (to defer a call) where the caller's frame ends. (The
		// function deferred is declared an integer to the compiler:
		// it is passed as the pointer it is.)
		for _, a := range args {
			g.get(a, false)
		}
		g.frameID()
		if fn.Name == "runtime.deferproc" {
			g.callerSP()
		}
		p := g.prog(obj.ACALL)
		p.To = obj.Addr{Type: obj.TYPE_MEM, Name: obj.NAME_EXTERN, Sym: fn}
		p.Pos = v.Pos
	case "runtime.deferprocat":
		// (As for deferproc, the function is passed as the pointer
		// it is.)
		g.get(args[0], false)
		g.argsFrom(aux, args[1:], 1)
		p := g.prog(obj.ACALL)
		p.To = obj.Addr{Type: obj.TYPE_MEM, Name: obj.NAME_EXTERN, Sym: fn}
		p.Pos = v.Pos
	case "runtime.memclrHasPointers":
		// Cleared by the type of the elements, which the address
		// still has before its conversion to unsafe.Pointer.
		p := args[0]
		for (p.Op == ssa.OpCopy || p.Op == ssa.OpConvert) && p.Type.IsUnsafePtr() {
			p = p.Args[0]
		}
		if p.Type.IsPtr() && p.Type.Elem().Size() > 0 {
			g.typeDesc(p.Type.Elem())
		} else {
			g.prog(wasm.ARefNullNone)
		}
		// (Only if p is computed here, where its arguments are.)
		here := p.OnWasmStack || g.remat[p.ID]
		for x := args[0]; x != p; x = x.Args[0] {
			here = here && (x.OnWasmStack || g.remat[x.ID])
		}
		if _, isObj := gcElem(p.Type.Elem()); here && p.Op == ssa.OpPtrIndex && isObj {
			// &s[i] is the element, which does not know the array
			// it is in: pass the array, and the offset of the element.
			for x := args[0]; x != p; x = x.Args[0] {
				g.discard(x)
			}
			g.through(p)
			g.get(p.Args[0], false)
			g.get(p.Args[1], true)
			i32Const(g.s, int32(p.Type.Elem().Size()))
			g.prog(wasm.AI32Mul)
			g.helper(wasm.GCAddPtr)
		} else {
			g.get(args[0], false)
		}
		g.get(args[1], false)
		g.runtime("runtime.memclrHasPointersT")
	case "runtime.deferrangefunc":
		// The frame of the function whose deferred calls the
		// body of a range over a function defers.
		g.frameID()
		g.runtime("runtime.deferrangefuncgc")
	case "runtime.newobject":
		// (Counted, for runtime.ReadMemStats: an allocation of Go's
		// heap.)
		// (Not the runtime's own: its temporaries, which Go would
		// not allocate; it counts what it makes for programs.)
		if !g.s.GD().Flag.CompilingRuntime {
			g.runtime("runtime.countalloc")
		}
		if g.constType(args[0]) == nil {
			// A type known at run time, from a dictionary.
			g.get(args[0], false)
			g.runtime("runtime.newobjectT")
			break
		}
		g.newObjectInit(g.typeArg(args[0]), g.fusedAt[v.ID])
	case "runtime.maybeInPlace":
		// The result of a function, for which the caller may have
		// offered room on its stack (doc/gd/escape-bits.md). There
		// is no such room: allocate.
		g.discard(args[0])
		if g.constType(args[1]) == nil {
			g.get(args[1], false)
			g.runtime("runtime.newobjectT")
			break
		}
		g.newObject(g.typeArg(args[1]))
	case "runtime.getclosureptr":
		g.global(wasm.AGlobalGet, wasm.GCGlobalCtxt)
		g.ptrToInt()
	case "runtime.makeslice":
		if t := g.constType(args[0]); t != nil && t.Size() == 0 {
			// Elements of size zero: no array.
			g.get(args[1], false)
			g.get(args[2], false)
			g.runtime("runtime.makeslicezero")
			break
		}
		if g.constType(args[0]) == nil {
			for _, a := range args {
				g.get(a, false)
			}
			g.runtime("runtime.makesliceT")
			break
		}
		e := g.typeArg(args[0])
		if !e.HasPointers() {
			// No pointers: in linear memory.
			i64Const(g.s, e.Size())
			g.get(args[1], false)
			g.get(args[2], false)
			g.runtime("runtime.makeslicelin")
			break
		}
		k, isObj := gcElem(e)
		if isObj {
			// An array of objects: the runtime copies the zero
			// value for each.
			g.newObject(e)
			g.get(args[1], false)
			g.get(args[2], false)
			g.runtime("runtime.makesliceobj")
			break
		}
		if !g.s.GD().Flag.CompilingRuntime {
			// Checked by the runtime.
			g.get(args[1], false)
			g.get(args[2], false)
			g.runtime("runtime.makeslicer")
			break
		}
		g.discard(args[1])
		g.get(args[2], true)
		g.typed(wasm.AArrayNewDefault, g.arraySym(k), 0)
	case "runtime.growslice":
		if t := g.constType(args[4]); t != nil && t.Size() == 0 {
			for _, a := range args[:4] {
				g.get(a, false)
			}
			g.discard(args[4])
			g.runtime("runtime.growslicezero")
			break
		}
		if g.constType(args[4]) == nil {
			for _, a := range args {
				g.get(a, false)
			}
			g.runtime("runtime.growsliceT")
			break
		}
		e := g.typeArg(args[4])
		for _, a := range args[:4] {
			g.get(a, false)
		}
		if !e.HasPointers() {
			i64Const(g.s, e.Size())
			g.runtime("runtime.growslicelin")
			break
		}
		if _, isObj := gcElem(e); isObj {
			i64Const(g.s, e.Size())
			g.newObject(e)
			g.runtime("runtime.growsliceobj")
			break
		}
		g.runtime("runtime.growslicer")

	// Arrays of objects, for the runtime.
	case "runtime.isobjarray":
		g.get(args[0], false)
		g.typed(wasm.ARefTest, g.arraySym('o'), 0)
		g.prog(wasm.AI64ExtendI32U)
	case "runtime.newobjarray":
		g.get(args[0], true)
		g.typed(wasm.AArrayNewDefault, g.arraySym('o'), 0)
	case "runtime.objarrayget":
		g.get(args[0], false)
		g.cast(g.arraySym('o'))
		g.get(args[1], true)
		g.typed(wasm.AArrayGet, g.arraySym('o'), 0)
		g.castEq()
	case "runtime.objarrayset":
		g.get(args[0], false)
		g.cast(g.arraySym('o'))
		g.get(args[1], true)
		g.get(args[2], false)
		g.typed(wasm.AArraySet, g.arraySym('o'), 0)
	case "runtime.gorecover":
		// With where the frame of the function that calls recover
		// ends: where its caller's begins. A wrapper, as for defer
		// recover(), is not a function that recovers.
		if g.f.Frontend().Func().Wrapper() && g.ctxt.PosTable.Pos(v.Pos).Base().InliningIndex() < 0 {
			g.runtime("runtime.gorecoverwrap")
			break
		}
		g.callerSP()
		g.runtime("runtime.gorecoverfp")
	case "runtime.gcclone":
		g.get(args[0], false)
		g.helper(wasm.GCClone)
	case "runtime.gcreflectcall":
		// (The type as the address of its descriptor.)
		g.get(args[0], false)
		g.ptrToInt()
		g.prog(wasm.AI32WrapI64)
		g.get(args[1], false)
		for _, a := range args[2:6] {
			g.get(a, false)
		}
		g.get(args[6], true)
		g.helper(wasm.GCReflectCall)
		g.prog(wasm.AI64ExtendI32U)
	case "runtime.gcmakefunc":
		g.get(args[0], false)
		g.ptrToInt()
		g.prog(wasm.AI32WrapI64)
		g.helper(wasm.GCMakeFunc)
	case "runtime.gcnew":
		g.get(args[0], true)
		g.helper(wasm.GCNew)
	case "runtime.boxobj", "runtime.boxoff":
		// The object and the offset of a pointer into an object.
		tmp := g.scratch('r')
		g.get(args[0], false)
		g.local(wasm.ALocalSet, tmp)
		g.local(wasm.ALocalGet, tmp)
		g.typed(wasm.ARefTest, g.boxSym(), 0)
		p := g.prog(wasm.AIf)
		if fn.Name == "runtime.boxobj" {
			p.From.Offset = 0x80 - wasm.GCTypeEqRef
			g.local(wasm.ALocalGet, tmp)
			g.cast(g.boxSym())
			g.typed(wasm.AStructGet, g.boxSym(), wasm.GCBoxObject)
			g.prog(wasm.AElse)
			g.local(wasm.ALocalGet, tmp)
		} else {
			p.From.Offset = 0x80 - wasm.GCTypeI64
			g.local(wasm.ALocalGet, tmp)
			g.cast(g.boxSym())
			g.typed(wasm.AStructGet, g.boxSym(), wasm.GCBoxOffset)
			g.prog(wasm.AI64ExtendI32U)
			g.prog(wasm.AElse)
			i64Const(g.s, 0)
		}
		g.prog(wasm.AEnd)

	case "runtime.mapaccess1", "runtime.mapaccess2", "runtime.mapassign":
		// The runtime is given the zero value of an element that
		// is an object, to copy or to return for a key that is not
		// there.
		g.args(aux, args)
		if len(args) == 3 {
			g.zeroElemArg(args[0])
		}
		p := g.prog(obj.ACALL)
		p.To = obj.Addr{Type: obj.TYPE_MEM, Name: obj.NAME_EXTERN, Sym: fn}
		p.Pos = v.Pos

	// Linear memory, for the runtime.
	case "runtime.linload8", "runtime.linload16", "runtime.linload32", "runtime.linload64":
		g.get(args[0], true)
		as := map[string]obj.As{"8": wasm.AI64Load8U, "16": wasm.AI64Load16U, "32": wasm.AI64Load32U, "64": wasm.AI64Load}[fn.Name[len("runtime.linload"):]]
		g.prog(as).From = obj.Addr{Type: obj.TYPE_CONST}
	case "runtime.linstore8", "runtime.linstore16", "runtime.linstore32", "runtime.linstore64":
		g.get(args[0], true)
		g.get(args[1], false)
		as := map[string]obj.As{"8": wasm.AI64Store8, "16": wasm.AI64Store16, "32": wasm.AI64Store32, "64": wasm.AI64Store}[fn.Name[len("runtime.linstore"):]]
		g.prog(as).To = obj.Addr{Type: obj.TYPE_CONST}
	case "runtime.linsize":
		g.prog(wasm.ACurrentMemory)
		g.prog(wasm.AI64ExtendI32U)
		i64Const(g.s, 16)
		g.prog(wasm.AI64Shl)
	case "runtime.lingrow":
		g.get(args[0], true)
		g.prog(wasm.AGrowMemory)
		g.prog(wasm.ADrop)
	case "runtime.linzero":
		g.get(args[0], true)
		i32Const(g.s, 0)
		g.get(args[1], true)
		g.prog(wasm.AMemoryFill)
	case "runtime.lincopy":
		g.get(args[0], true)
		g.get(args[1], true)
		g.get(args[2], true)
		g.prog(wasm.AMemoryCopy)
	case "runtime.newlinear":
		g.get(args[0], true)
		g.prog(wasm.ARefNull)
		g.typed(wasm.AStructNew, g.linearSym(), 0)
	case "runtime.trylinaddr":
		// The address, or 0 if the pointer is not to linear memory.
		done := g.block('l')
		slow := g.block('r')
		g.linearAddr(args[0], slow)
		g.prog(wasm.AI64ExtendI32U)
		g.br(wasm.ABr, done, nil)
		g.prog(wasm.AEnd)
		g.prog(wasm.ADrop)
		i64Const(g.s, 0)
		g.prog(wasm.AEnd)
	case "runtime.linbase":
		// The address of the block of linear memory a pointer is
		// into (its owner's), or 0 if it is not into one.
		done := g.block('l')
		slow := g.block('r')
		lin := g.linearSym()
		g.get(args[0], false)
		g.br(wasm.ABrOnCastFail, slow, lin)
		ref, addr := g.scratch('r'), g.scratch('i')
		g.local(wasm.ALocalTee, ref)
		g.cast(lin)
		g.typed(wasm.AStructGet, lin, wasm.GCLinearAddr)
		g.local(wasm.ALocalSet, addr)
		g.local(wasm.ALocalGet, ref)
		g.cast(lin)
		g.typed(wasm.AStructGet, lin, wasm.GCLinearOwner)
		g.prog(wasm.ARefIsNull)
		g.prog(wasm.AI32Eqz)
		g.prog(wasm.AIf)
		g.local(wasm.ALocalGet, ref)
		g.cast(lin)
		g.typed(wasm.AStructGet, lin, wasm.GCLinearOwner)
		g.cast(lin)
		g.typed(wasm.AStructGet, lin, wasm.GCLinearAddr)
		g.local(wasm.ALocalSet, addr)
		g.prog(wasm.AEnd)
		g.local(wasm.ALocalGet, addr)
		g.prog(wasm.AI64ExtendI32U)
		g.br(wasm.ABr, done, nil)
		g.prog(wasm.AEnd)
		g.prog(wasm.ADrop)
		i64Const(g.s, 0)
		g.prog(wasm.AEnd)
	case "runtime.linaddr":
		// TODO: a pointer into an array object; it traps.
		done := g.block('l')
		slow := g.block('r')
		g.linearAddr(args[0], slow)
		g.prog(wasm.AI64ExtendI32U)
		g.br(wasm.ABr, done, nil)
		g.prog(wasm.AEnd)
		g.prog(obj.AUNDEF)
		g.prog(wasm.AEnd)

	default:
		g.args(aux, args)
		g.slotArgs(v)
		p := g.prog(obj.ACALL)
		p.To = obj.Addr{Type: obj.TYPE_MEM, Name: obj.NAME_EXTERN, Sym: fn}
		p.Pos = v.Pos
	}

	g.results(v)
}

// slotArgs pushes the objects of the arguments and results of the call
// v that are not in registers (see stackSlots).
func (g *gcGen) slotArgs(v *ssa.Value) {
	for _, n := range g.callSlots[v.ID] {
		g.local(wasm.ALocalGet, g.objs[n])
	}
}

// results pops the results of the call v into the locals of their
// selectors.
func (g *gcGen) results(v *ssa.Value) {
	if !v.Type.IsResults() && !v.Type.IsTuple() {
		return
	}
	n := v.Type.NumFields() - 1
	sel := make([]*ssa.Value, n)
	for _, u := range v.Block.Values {
		if u.Op == ssa.OpSelectN && u.Args[0] == v && int(u.AuxInt) < n {
			sel[u.AuxInt] = u
		}
	}
	var have []*types.Type
	if aux, ok := v.Aux.(*ssa.AuxCall); ok {
		have = abi.RegisterTypes(aux.ABIInfo().OutParams())
	}
	for i := n - 1; i >= 0; i-- {
		if sel[i] == nil || g.loc[sel[i].ID] < 0 {
			g.prog(wasm.ADrop)
			continue
		}
		if i < len(have) {
			switch h, w := g.wtype(have[i]), g.wtype(sel[i].Type); {
			case h == 'l' && w == 'r':
				g.intToPtr()
			case h == 'r' && w == 'l':
				g.ptrToInt()
			}
		}
		g.local(wasm.ALocalSet, g.loc[sel[i].ID])
		g.fillTyped(sel[i])
	}
}

// Functions.

// gcStackable reports whether v can be computed where it is used.
func gcStackable(v *ssa.Value) bool {
	switch v.Op {
	case ssa.OpLoad, ssa.OpIsNonNil, ssa.OpEqPtr, ssa.OpNeqPtr, ssa.OpCopy:
		return true
	case ssa.OpWasmLoweredGetClosurePtr, ssa.OpWasmLoweredGetCallerPC, ssa.OpWasmLoweredGetCallerSP:
		return false
	}
	return !v.Op.IsGeneric()
}

// gcRemat reports whether v is recomputed at each use.
func gcRemat(v *ssa.Value) bool {
	switch v.Op {
	case ssa.OpWasmI64Const, ssa.OpWasmF32Const, ssa.OpWasmF64Const, ssa.OpConstNil, ssa.OpLocalAddr, ssa.OpAddr, ssa.OpSP:
		return true
	case ssa.OpOffPtr, ssa.OpPtrIndex, ssa.OpAddPtr:
		// An address is a pure function of its operands, which
		// stackify leaves in locals.
		return true
	}
	return false
}

func genWasmGC(s *ssagen.State, f *ssa.Func) {
	g := &gcGen{s: s, f: f, ctxt: s.GD().Ctxt, scratchI32: -1, scratchRef: -1, scratchI64: -1, scratchNil: -1, fused: make(map[ssa.ID]bool)}
	s.WasmGC = g
	defer func() {
		// TODO: nothing should be unsupported.
		switch e := recover().(type) {
		case nil:
		case gcUnsupported:
			if os.Getenv("GOWASMGC_NOTES") != "" {
				fmt.Fprintf(os.Stderr, "GOWASM=gc: %s traps: not supported yet: %s\n", f.Name, string(e))
			}
			s.OnWasmStackSkipped = 0
			f.Frontend().Func().LSym.Func().WasmGC = &obj.WasmGCFunc{Sig: g.sig(), Trap: true}
		default:
			panic(e)
		}
	}()
	if f.WasmCF == nil {
		g.unsupported(nil, "irreducible control flow")
	}
	g.objs = make(map[*ir.Name]int32)
	g.paramObjs = make(map[*ir.Name]bool)
	g.stackSlots()
	g.params()

	// A wrapper has no frame on the stack in linear memory, so that
	// the function it calls sees its caller's (see recover below):
	// its variables are objects. Unless a function was inlined into
	// it, which is then the function.
	frameless := false
	if f.Frontend().Func().Wrapper() {
		frameless = true
		for _, b := range f.Blocks {
			for _, v := range b.Values {
				if v.Op == ssa.OpInlMark {
					frameless = false
				}
			}
		}
	}

	// Decide where each value lives.
	g.loc = make([]int32, f.NumValues())
	g.remat = make([]bool, f.NumValues())
	g.frame = make(map[*ir.Name]int64)
	for _, b := range f.Blocks {
		g.stackify(b)
	}
	for _, b := range f.Blocks {
		for _, v := range b.Values {
			g.loc[v.ID] = -1
			switch {
			case v.Op == ssa.OpArgIntReg:
				g.loc[v.ID] = g.intArg[v.AuxInt]
				if p := g.loc[v.ID]; g.wtype(v.Type) != g.paramKind[p] {
					// The word of an interface that holds its type:
					// a pointer as a parameter, an integer here.
					g.loc[v.ID] = g.newLocal(g.wtype(v.Type))
					g.local(wasm.ALocalGet, p)
					if g.paramKind[p] == 'r' {
						g.ptrToInt()
					} else {
						g.intToPtr()
					}
					g.local(wasm.ALocalSet, g.loc[v.ID])
				}
			case v.Op == ssa.OpArgFloatReg:
				g.loc[v.ID] = g.floatArg[v.AuxInt]
			case v.Op == ssa.OpLocalAddr:
				n := v.Aux.(*ir.Name)
				_, isObj := g.objs[n]
				_, inFrame := g.frame[n]
				switch {
				case isObj, inFrame:
				case !n.Type().HasPointers() && !g.forceObj[n] && !frameless:
					g.frame[n] = g.frameSize
					g.frameSize += (n.Type().Size() + 7) &^ 7
				default:
					g.objs[n] = g.newLocal('r')
				}
				g.remat[v.ID] = true
			case gcRemat(v):
				g.remat[v.ID] = true
			case v.OnWasmStack, v.Uses == 0:
			case v.Type.IsMemory(), v.Type.IsVoid(), v.Type.IsTuple(), v.Type.IsResults(), v.Type.IsFlags():
			case v.Op == ssa.OpSB, v.Op == ssa.OpSP:
			default:
				g.loc[v.ID] = gcLocalPending
			}
		}
	}
	g.colorLocals()

	// Allocate the variables whose address is taken.
	// TODO: only once the variable is declared, and not at all if
	// the pointers to it do not outlive the function.
	for _, b := range f.Blocks {
		g.hasDefer = g.hasDefer || b.Kind == ssa.BlockDefer
		for _, v := range b.Values {
			if aux, ok := v.Aux.(*ssa.AuxCall); ok && aux.Fn != nil && aux.Fn.Name == "runtime.deferrangefunc" {
				g.hasDefer = true
			}
		}
	}
	// A function that calls others has a frame of its own, unless it is
	// a wrapper: recover recovers only in the function that a panic
	// calls (directly, or through a wrapper), which it knows by where
	// its caller's frame ends (see gorecoverfp in the runtime).
	// (A wrapper into which the function it wraps was inlined is
	// that function.)
	calls, inlined := false, false
	for _, b := range f.Blocks {
		for _, v := range b.Values {
			switch v.Op {
			case ssa.OpWasmLoweredStaticCall, ssa.OpWasmLoweredClosureCall, ssa.OpWasmLoweredInterCall:
				calls = true
			case ssa.OpInlMark:
				inlined = true
			}
		}
	}
	if (g.hasDefer || len(g.frame) > 0 || calls && (!f.Frontend().Func().Wrapper() || inlined)) && g.frameSize == 0 {
		// (A variable of size zero still has an address.)
		g.frameSize = 8 // so that no other frame is at the same address
	}
	if g.frameSize > 0 {
		// The frame, below the caller's; in a segment of the stack
		// of its own if there is no room left in this one.
		g.fp = g.newLocal('i')
		g.csp = g.newLocal('i')
		g.newSeg = g.newLocal('i')
		g.global(wasm.AGlobalGet, wasm.GCGlobalSP)
		g.local(wasm.ALocalTee, g.csp)
		i32Const(s, int32(g.frameSize))
		g.prog(wasm.AI32Sub)
		g.local(wasm.ALocalTee, g.fp)
		if f.Frontend().Func().Pragma&ir.Nosplit == 0 {
			g.global(wasm.AGlobalGet, wasm.GCGlobalStackLo)
			g.prog(wasm.AI32LtU)
			g.prog(wasm.AIf)
			i64Const(s, g.frameSize)
			g.runtime("runtime.morestackgc")
			g.prog(wasm.AI32WrapI64)
			i32Const(s, int32(g.frameSize))
			g.prog(wasm.AI32Sub)
			g.local(wasm.ALocalSet, g.fp)
			i32Const(s, 1)
			g.local(wasm.ALocalSet, g.newSeg)
			g.prog(wasm.AEnd)
		} else {
			g.prog(wasm.ADrop)
		}
		g.local(wasm.ALocalGet, g.fp)
		g.global(wasm.AGlobalSet, wasm.GCGlobalSP)
	}
	names := make([]*ir.Name, 0, len(g.objs))
	for _, b := range f.Blocks {
		for _, v := range b.Values {
			if v.Op == ssa.OpLocalAddr {
				if _, ok := g.objs[v.Aux.(*ir.Name)]; !ok {
					continue
				}
				if n := v.Aux.(*ir.Name); g.objs[n] >= 0 && !g.paramObjs[n] {
					names = append(names, n)
					g.newObject(n.Type())
					g.local(wasm.ALocalSet, g.objs[n])
					g.objs[n] = -g.objs[n] - 1
				}
			}
		}
	}
	for _, n := range names {
		g.objs[n] = -g.objs[n] - 1
	}

	g.chooseTyped()
	if g.hasDefer {
		g.block(0)
		g.prog(wasm.ATRYCATCH).From = obj.Addr{Type: obj.TYPE_CONST, Offset: wasm.GCTagRecover}
	}
	if f.WasmCF.Table != nil {
		f.WasmCF.State = g.newLocal('i')
	}
	for _, b := range f.Blocks {
		s.SetPos(b.Pos)
		if b == f.Blocks[0] {
			s.WasmCFProgs = make(map[int32]*obj.Prog)
			genCF(s, b, f.WasmCF.Pre)
		}
		for _, v := range b.Values {
			if v.Op == ssa.OpPhi || v.Op == ssa.OpArgIntReg {
				g.fillTyped(v)
			}
		}
		for _, v := range b.Values {
			s.SetPos(v.Pos)
			g.value(v)
		}
		g.blockEnd(b)
		genCF(s, b, f.WasmCF.Post[b.ID])
		if s.OnWasmStackSkipped != 0 {
			g.unsupported(nil, "operands left on the stack in "+b.String())
		}
	}
	if g.hasDefer {
		g.prog(wasm.AEnd)
		g.prog(obj.AUNDEF)
		g.prog(wasm.AEnd)
		g.recovery()
	}
	// The function's type is checked at its end.
	s.Prog(obj.AUNDEF)

	var localTypes []*obj.LSym
	if len(g.localSyms) > 0 {
		localTypes = make([]*obj.LSym, len(g.locals))
		for n, sym := range g.localSyms {
			localTypes[n-g.nparam] = sym
		}
	}
	f.Frontend().Func().LSym.Func().WasmGC = &obj.WasmGCFunc{
		Sig:        g.sig(),
		Locals:     g.locals,
		LocalTypes: localTypes,
	}
}

// sig returns the WebAssembly type of the function: a parameter or
// result per ABI register. A parameter or result that is not in
// registers (an array) is not supported: it is left out.
func (g *gcGen) sig() *obj.LSym {
	return g.sigOf(g.f.OwnAux.ABIInfo())
}

// sigOf returns the WebAssembly type of functions of the ABI info: a
// parameter or result per register, and a parameter (a reference to an
// object) per argument or result that is not in registers.
func (g *gcGen) sigOf(info *abi.ABIParamResultInfo) *obj.LSym {
	var sig strings.Builder
	sig.WriteString(wasm.GCFuncPrefix)
	for _, t := range abi.RegisterTypes(info.InParams()) {
		sig.WriteByte(g.wtype(t))
	}
	for range gcStackParams(info) {
		sig.WriteByte('r')
	}
	sig.WriteByte('.')
	for _, t := range abi.RegisterTypes(info.OutParams()) {
		sig.WriteByte(g.wtype(t))
	}
	return wasm.GCTypeSym(g.ctxt, sig.String())
}

// gcStackParams returns the arguments, then the results, that are not
// in registers.
func gcStackParams(info *abi.ABIParamResultInfo) []abi.ABIParamAssignment {
	var ps []abi.ABIParamAssignment
	for _, pa := range info.InParams() {
		if len(pa.Registers) == 0 && pa.Type.Size() > 0 {
			ps = append(ps, pa)
		}
	}
	for _, pa := range info.OutParams() {
		if len(pa.Registers) == 0 && pa.Type.Size() > 0 {
			ps = append(ps, pa)
		}
	}
	return ps
}

// stackSlots gives each argument and result of a call that is not in
// registers a variable, and makes the addresses of its slot on the
// stack, which the SSA computes from SP, addresses in the variable.
func (g *gcGen) stackSlots() {
	f := g.f
	type slot struct {
		off  int64
		t    *types.Type
		name *ir.Name
	}
	var slots []slot
	g.forceObj = make(map[*ir.Name]bool)
	g.callSlots = make(map[ssa.ID][]*ir.Name)
	for _, b := range f.Blocks {
		for _, v := range b.Values {
			switch v.Op {
			case ssa.OpWasmLoweredStaticCall, ssa.OpWasmLoweredClosureCall, ssa.OpWasmLoweredInterCall:
			default:
				continue
			}
			aux, ok := v.Aux.(*ssa.AuxCall)
			if !ok {
				continue
			}
			for _, pa := range gcStackParams(aux.ABIInfo()) {
				off := int64(pa.Offset())
				var name *ir.Name
				for _, sl := range slots {
					if sl.off == off && types.Identical(sl.t, pa.Type) {
						name = sl.name
					}
				}
				if name == nil {
					name = f.NewLocal(v.Pos, pa.Type)
					slots = append(slots, slot{off, pa.Type, name})
					g.forceObj[name] = true
				}
				g.callSlots[v.ID] = append(g.callSlots[v.ID], name)
			}
		}
	}
	if len(slots) == 0 {
		return
	}
	var mem *ssa.Value
	for _, v := range f.Entry.Values {
		if v.Op == ssa.OpInitMem {
			mem = v
		}
	}
	addrs := make(map[*ir.Name]*ssa.Value)
	for _, b := range f.Blocks {
		for _, v := range b.Values {
			if v.Op != ssa.OpOffPtr || v.Args[0].Op != ssa.OpSP {
				continue
			}
			var found *slot
			for i := range slots {
				sl := &slots[i]
				if sl.off <= v.AuxInt && v.AuxInt < sl.off+sl.t.Size() {
					if found != nil && found.name != sl.name {
						g.unsupported(v, "stack slots of two types at once")
					}
					found = sl
				}
			}
			if found == nil {
				g.unsupported(v, "an address on the stack")
			}
			a := addrs[found.name]
			if a == nil {
				a = f.Entry.NewValue2A(v.Pos, ssa.OpLocalAddr, types.NewPtr(found.t), found.name, v.Args[0], mem)
				addrs[found.name] = a
			}
			v.AuxInt -= found.off
			v.SetArg(0, a)
		}
	}
}

// params finds the WebAssembly parameter of each ABI register.
func (g *gcGen) params() {
	info := g.f.OwnAux.ABIInfo()
	cfg := g.f.Config
	nint := cfg.ABI1.NumIntRegs()
	g.intArg = make([]int32, nint)
	g.floatArg = make([]int32, cfg.ABI1.NumFloatRegs())
	for _, pa := range info.InParams() {
		if len(pa.Registers) == 0 && pa.Type.Size() > 0 {
			continue // see below
		}
		for i, t := range abi.RegisterTypes([]abi.ABIParamAssignment{pa}) {
			if r := int(pa.Registers[i]); r < nint {
				g.intArg[r] = g.nparam
			} else {
				g.floatArg[r-nint] = g.nparam
			}
			g.paramKind = append(g.paramKind, g.wtype(t))
			g.nparam++
		}
	}
	for _, t := range abi.RegisterTypes(info.OutParams()) {
		g.resultKind = append(g.resultKind, g.wtype(t))
	}
	// The arguments and results not in registers: objects the caller
	// passes.
	for _, pa := range gcStackParams(info) {
		if n := pa.Name; n != nil {
			g.objs[n] = g.nparam
			g.paramObjs[n] = true
		}
		g.paramKind = append(g.paramKind, 'r')
		g.nparam++
	}
}

// stackify marks the values of b that are computed where they are used
// (see the same in ssa/regalloc.go).
func (g *gcGen) stackify(b *ssa.Block) {
	can := make(map[ssa.ID]bool)
	for _, c := range b.ControlValues() {
		if c.Uses == 1 && c.Block == b && gcStackable(c) && !gcRemat(c) {
			can[c.ID] = true
		}
	}
	for i := len(b.Values) - 1; i >= 0; i-- {
		v := b.Values[i]
		switch {
		case can[v.ID]:
			v.OnWasmStack = true
		case v.Op == ssa.OpMakeResult && i == len(b.Values)-1:
			// Computed at the end of the block.
		case gcRemat(v), v.Op == ssa.OpSelectN, v.Type.IsMemory() && v.Op == ssa.OpPhi:
			continue
		default:
			clear(can)
		}
		// The addresses of a move are computed once per field (see
		// again): the stores in between must not change them.
		if v.Op == ssa.OpMove || v.Op == ssa.OpZero || v.Op == ssa.OpWasmI64DivS || v.Op == ssa.OpPtrIndex || gcUsesTypeTwice(v) {
			continue
		}
		for _, a := range v.Args {
			if a.Uses == 1 && a.Block == b && !a.Type.IsMemory() && gcStackable(a) && !gcRemat(a) {
				can[a.ID] = true
			}
		}
	}
}

func (g *gcGen) value(v *ssa.Value) {
	s := g.s
	switch v.Op {
	case ssa.OpPhi, ssa.OpSB, ssa.OpSP, ssa.OpInitMem, ssa.OpArgIntReg, ssa.OpArgFloatReg,
		ssa.OpVarDef, ssa.OpVarLive, ssa.OpSelectN, ssa.OpMakeResult,
		ssa.OpClobber, ssa.OpClobberReg, ssa.OpInlMark:
		return

	case ssa.OpKeepAlive:
		// A use that the engine keeps: the object stays alive until
		// here (weak pointers, cleanups).
		if g.wtype(v.Args[0].Type) == 'r' {
			g.get(v.Args[0], false)
			g.global(wasm.AGlobalSet, wasm.GCGlobalKeep)
		} else {
			g.discard(v.Args[0])
		}

	case ssa.OpStore:
		if g.fused[v.ID] {
			return
		}
		t := v.Aux.(*types.Type)
		g.accessStore(v.Args[0], 0, t, func(want32 bool) {
			// The value may be a pointer stored as an integer, or
			// the reverse: the type word of an interface.
			switch have, want := g.wtype(v.Args[1].Type), g.wtype(t); {
			case have == 'r' && want == 'l':
				g.get(v.Args[1], false)
				g.ptrToInt()
				if want32 {
					g.prog(wasm.AI32WrapI64)
				}
			case have == 'l' && want == 'r':
				g.get(v.Args[1], false)
				g.intToPtr()
			default:
				g.get(v.Args[1], want32)
			}
		}, v)

	case ssa.OpZero:
		g.zeroValue(v, v.Aux.(*types.Type))

	case ssa.OpMove:
		g.moveValue(v, v.Aux.(*types.Type))

	case ssa.OpWasmLoweredStaticCall, ssa.OpWasmLoweredClosureCall, ssa.OpWasmLoweredInterCall,
		ssa.OpWasmLoweredTailCall, ssa.OpWasmLoweredTailCallInter:
		g.call(v)

	case ssa.OpWasmLoweredNilCheck:
		// (The operand is an integer if it is the itab of an
		// interface.)
		kind := g.wtype(v.Args[0].Type)
		tmp := g.loc[v.ID]
		if tmp < 0 {
			tmp = g.scratch(map[byte]byte{'r': 'r', 'l': 'L'}[kind])
		}
		g.get(v.Args[0], false)
		g.local(wasm.ALocalTee, tmp)
		if kind == 'l' {
			g.prog(wasm.AI64Eqz)
		} else {
			g.prog(wasm.ARefIsNull)
		}
		g.prog(wasm.AIf)
		g.runtime("runtime.panicnil")
		g.prog(obj.AUNDEF)
		g.prog(wasm.AEnd)

	default:
		if v.Type.IsMemory() || g.remat[v.ID] {
			return
		}
		if v.OnWasmStack {
			s.OnWasmStackSkipped++
			return
		}
		if v.Uses == 0 && g.loc[v.ID] < 0 {
			return
		}
		g.onStack(v, true)
		g.local(wasm.ALocalSet, g.loc[v.ID])
		g.fillTyped(v)
	}
}

// recovery emits what runs when the function catches the exception of
// a recovered panic: if the deferred call that recovered was one of
// this function's, the function returns to its caller, as the code
// that follows a deferproc that "returns 1" does; otherwise the
// exception goes on up.
func (g *gcGen) recovery() {
	g.runtime("runtime.recoveringFrame")
	g.frameID()
	g.prog(wasm.AI64Ne)
	g.prog(wasm.AIf)
	g.prog(wasm.ATHROW).From = obj.Addr{Type: obj.TYPE_CONST, Offset: wasm.GCTagRecover}
	g.prog(wasm.AEnd)
	// The frames below are gone, and the segments of the stack they
	// were in.
	g.local(wasm.ALocalGet, g.fp)
	g.prog(wasm.AI64ExtendI32U)
	g.runtime("runtime.stackunwindgc")
	g.local(wasm.ALocalGet, g.fp)
	g.global(wasm.AGlobalSet, wasm.GCGlobalSP)
	// Run the remaining deferred calls and return the results as
	// they are: the code of the block the compiler made for that.
	for b := g.f.DeferReturn; ; b = b.Succs[0].Block() {
		for _, v := range b.Values {
			g.value(v)
		}
		g.blockEnd(b)
		if b.Kind == ssa.BlockRet {
			g.prog(obj.ARET)
			return
		}
		if b.Kind != ssa.BlockPlain {
			g.unsupported(nil, "return after a recovered panic")
		}
	}
}

// frameID pushes what identifies the frame of the function to the
// runtime: its address on the stack in linear memory.
func (g *gcGen) frameID() {
	g.local(wasm.ALocalGet, g.fp)
	g.prog(wasm.AI64ExtendI32U)
}

// popFrame frees the frame of the function on the stack in linear
// memory.
// callerSP pushes, as an i64, where the frame of the caller in linear
// memory ends.
func (g *gcGen) callerSP() {
	if g.frameSize > 0 {
		g.local(wasm.ALocalGet, g.csp)
	} else {
		g.global(wasm.AGlobalGet, wasm.GCGlobalSP)
	}
	g.prog(wasm.AI64ExtendI32U)
}

func (g *gcGen) popFrame() {
	if g.frameSize > 0 {
		g.local(wasm.ALocalGet, g.newSeg)
		g.prog(wasm.AIf)
		g.runtime("runtime.lessstackgc")
		g.prog(wasm.AEnd)
		g.local(wasm.ALocalGet, g.csp)
		g.global(wasm.AGlobalSet, wasm.GCGlobalSP)
	}
}

// blockEnd emits what follows the values of b: the copies into the
// phis of its successor, or the results of the function.
func (g *gcGen) blockEnd(b *ssa.Block) {
	if b.Kind == ssa.BlockRet {
		r := b.Controls[0]
		if r.Op != ssa.OpMakeResult {
			g.unsupported(r, "return")
		}
		for i, a := range r.Args[:len(r.Args)-1] {
			g.get(a, false)
			switch have, want := g.wtype(a.Type), g.resultKind[i]; {
			case have == 'l' && want == 'r':
				g.intToPtr()
			case have == 'r' && want == 'l':
				g.ptrToInt()
			}
		}
		g.popFrame()
		return
	}
	if len(b.Succs) != 1 {
		// The critical edges are split: only a block with one
		// successor leads to phis.
		return
	}
	e := b.Succs[0]
	var phis []*ssa.Value
	for _, v := range e.Block().Values {
		if v.Op == ssa.OpPhi && !v.Type.IsMemory() && g.loc[v.ID] >= 0 {
			phis = append(phis, v)
		}
	}
	// The copies are parallel: push every source, then pop.
	for _, v := range phis {
		a := v.Args[e.Index()]
		g.get(a, false)
		switch have, want := g.wtype(a.Type), g.wtype(v.Type); {
		case have == 'l' && want == 'r':
			g.intToPtr()
		case have == 'r' && want == 'l':
			g.ptrToInt()
		}
	}
	for i := len(phis) - 1; i >= 0; i-- {
		g.local(wasm.ALocalSet, g.loc[phis[i].ID])
	}
}

// gcLocalPending marks a value that needs a local, which colorLocals
// gives it.
const gcLocalPending = -2

// colorLocals gives the values that need a local one each, shared by
// values whose lifetimes do not overlap: a function has many fewer
// locals than values, and an engine's baseline compiler gives every
// local a slot in the frame (with one local per value, recursion
// through large functions ran out of stack).
func (g *gcGen) colorLocals() {
	f := g.f
	isVar := func(v *ssa.Value) bool { return g.loc[v.ID] == gcLocalPending }
	// reads calls do for each value whose local the code for v's
	// argument a reads: a itself, or (if a is computed where it is
	// used) the values it is computed from.
	var reads func(a *ssa.Value, do func(*ssa.Value))
	reads = func(a *ssa.Value, do func(*ssa.Value)) {
		if isVar(a) {
			do(a)
			return
		}
		if g.loc[a.ID] >= 0 {
			return
		}
		if a.OnWasmStack || g.remat[a.ID] {
			for _, x := range a.Args {
				reads(x, do)
			}
		}
	}
	// The values a block reads before it sets them, and those it
	// reads at its end for the phis of its successor.
	type set = map[ssa.ID]bool
	liveIn := make([]set, f.NumBlocks())
	liveOut := make([]set, f.NumBlocks())
	for _, b := range f.Blocks {
		liveIn[b.ID] = set{}
		liveOut[b.ID] = set{}
	}
	// endReads are what the end of b reads: its controls and the
	// arguments of the phis of its only successor.
	endReads := func(b *ssa.Block, do func(*ssa.Value)) {
		for _, c := range b.ControlValues() {
			reads(c, do)
		}
		if len(b.Succs) == 1 {
			e := b.Succs[0]
			for _, v := range e.Block().Values {
				if v.Op == ssa.OpPhi && g.loc[v.ID] != -1 {
					reads(v.Args[e.Index()], do)
				}
			}
		}
	}
	// defs[u] are the values that the code for u sets: u, or the
	// results of a call.
	sel := make(map[ssa.ID][]*ssa.Value)
	for _, b := range f.Blocks {
		for _, v := range b.Values {
			if v.Op == ssa.OpSelectN && isVar(v) {
				sel[v.Args[0].ID] = append(sel[v.Args[0].ID], v)
			}
		}
	}
	defs := func(u *ssa.Value, do func(*ssa.Value)) {
		if u.Op == ssa.OpSelectN {
			return // set by the call
		}
		if isVar(u) {
			do(u)
		}
		for _, s := range sel[u.ID] {
			do(s)
		}
	}
	// Backward dataflow to a fixed point.
	po := f.Postorder()
	for changed := true; changed; {
		changed = false
		for _, b := range po {
			out := liveOut[b.ID]
			for _, e := range b.Succs {
				for id := range liveIn[e.Block().ID] {
					if !out[id] {
						out[id] = true
						changed = true
					}
				}
			}
			live := make(set, len(out))
			for id := range out {
				live[id] = true
			}
			endReads(b, func(a *ssa.Value) { live[a.ID] = true })
			for i := len(b.Values) - 1; i >= 0; i-- {
				u := b.Values[i]
				defs(u, func(d *ssa.Value) { delete(live, d.ID) })
				if u.Op == ssa.OpPhi {
					continue
				}
				for _, a := range u.Args {
					reads(a, func(x *ssa.Value) { live[x.ID] = true })
				}
			}
			for id := range live {
				if !liveIn[b.ID][id] {
					liveIn[b.ID][id] = true
					changed = true
				}
			}
		}
	}
	// Interference: a value set while another is live (after it).
	conflict := make(map[ssa.ID]set)
	add := func(x, y ssa.ID) {
		if x == y {
			return
		}
		if conflict[x] == nil {
			conflict[x] = set{}
		}
		if conflict[y] == nil {
			conflict[y] = set{}
		}
		conflict[x][y] = true
		conflict[y][x] = true
	}
	var order []*ssa.Value
	for _, b := range f.Blocks {
		live := make(set, len(liveOut[b.ID]))
		for id := range liveOut[b.ID] {
			live[id] = true
		}
		// A phi's local is set at the end of each predecessor,
		// while what is live out of it is.
		if len(b.Succs) == 1 {
			for _, v := range b.Succs[0].Block().Values {
				if v.Op == ssa.OpPhi && isVar(v) {
					for id := range live {
						add(v.ID, id)
					}
					endReads(b, func(a *ssa.Value) { add(v.ID, a.ID) })
				}
			}
		}
		endReads(b, func(a *ssa.Value) { live[a.ID] = true })
		var phis []*ssa.Value
		for i := len(b.Values) - 1; i >= 0; i-- {
			u := b.Values[i]
			defs(u, func(d *ssa.Value) {
				for id := range live {
					add(d.ID, id)
				}
				// (The results of a call are set together.)
				for _, s := range sel[u.ID] {
					add(d.ID, s.ID)
				}
				delete(live, d.ID)
				order = append(order, d)
				if u.Op == ssa.OpPhi {
					phis = append(phis, d)
				}
			})
			if u.Op == ssa.OpPhi {
				continue
			}
			for _, a := range u.Args {
				reads(a, func(x *ssa.Value) { live[x.ID] = true })
			}
		}
		// The phis of a block are all set before it starts.
		for _, p := range phis {
			for _, q := range phis {
				add(p.ID, q.ID)
			}
			for id := range live {
				add(p.ID, id)
			}
		}
	}
	// What the code that runs after a recovered panic reads may be
	// read after anything (the panic can come from any call after the
	// defer): those values keep a local of their own.
	pinned := set{}
	for _, b := range f.Blocks {
		if b.Kind == ssa.BlockDefer {
			for id := range liveIn[b.Succs[1].Block().ID] {
				pinned[id] = true
			}
		}
	}
	// Greedy coloring, by kind of local.
	slots := make(map[byte][]int32)
	for i := len(order) - 1; i >= 0; i-- {
		v := order[i]
		if g.loc[v.ID] != gcLocalPending {
			continue
		}
		if pinned[v.ID] {
			g.loc[v.ID] = g.newLocal(g.wtype(v.Type))
			continue
		}
		k := g.wtype(v.Type)
		used := make(map[int32]bool)
		for id := range conflict[v.ID] {
			if l := g.loc[id]; l >= 0 {
				used[l] = true
			}
		}
		l := int32(-1)
		for _, s := range slots[k] {
			if !used[s] {
				l = s
				break
			}
		}
		if l < 0 {
			l = g.newLocal(k)
			slots[k] = append(slots[k], l)
		}
		g.loc[v.ID] = l
	}
	// Values set by no code (never reached): a local of their own.
	for _, b := range f.Blocks {
		for _, v := range b.Values {
			if g.loc[v.ID] == gcLocalPending {
				g.loc[v.ID] = g.newLocal(g.wtype(v.Type))
			}
		}
	}
}
