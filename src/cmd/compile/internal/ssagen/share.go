// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssagen

import (
	"internal/buildcfg"

	"cmd/compile/internal/ir"
	"cmd/compile/internal/ssa"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
	"cmd/internal/sys"
	"strings"
)

// Values of more than one word (a slice, a string, an interface) are
// loaded and stored whole where another goroutine may be storing or
// loading them: see runtime/shared.go. A load from memory that is not the
// stack checks the sequence lock of its 64-byte block before and after, inline; a
// store there calls the runtime, which takes the locks if the memory is
// shared.
//
// Without the sharing experiment (GOEXPERIMENT=sharing) the values are
// the interfaces alone (s.ifaceOnly), and a store of one takes the lock
// of its block wherever it is but in the stack, inline (shareStoreIface).
// A value that holds interfaces is stored field by field, each interface
// whole, as it is loaded (shareLoad); one too large for that is copied or
// cleared by the runtime, which locks by type.

const (
	shareBlockShift = 6       // a lock for every 64 bytes, 64 bytes apart
	shareStripes    = 1 << 14 // both must match runtime's
	sharePageShift  = 13      // as runtime.sharePageShift
	sharePagesLen   = 1 << 20 // len(runtime.sharePages)
)

// tearArch reports whether the target has the inline atomic load the
// sequence locks are read with.
func tearArch(f sys.ArchFamily) bool {
	switch f {
	case sys.AMD64, sys.ARM64, sys.Loong64, sys.MIPS, sys.MIPS64, sys.PPC64, sys.RISCV64, sys.S390X:
		return true
	}
	return false
}

// tearFunc reports whether fn may call the runtime for the memory it
// shares. Not the functions that run where the runtime cannot: in the
// child of a fork (syscall marks them norace), or without a stack check.
func tearFunc(fn *ir.Func) bool {
	return fn.Pragma&(ir.Norace|ir.Nosplit|ir.NoTear) == 0
}

// algFunc reports whether fn is an equality or hash function the compiler
// wrote. Every package that needs one compiles it, the runtime's among
// them, and all must be the same: they load whole in the runtime too.
func algFunc(fn *ir.Func) bool {
	name := fn.Sym().Name
	return strings.HasPrefix(name, ".eq.") || strings.HasPrefix(name, ".hash.")
}

// runtimeTearFunc reports whether fn, in the runtime's package pkg, loads
// and stores whole as compiled code does: the map's functions, and the
// equality functions it calls on the keys in its slots, read memory that
// another goroutine may be writing (doc/gd/maps.md).
func runtimeTearFunc(pkg string, fn *ir.Func) bool {
	switch pkg {
	case "internal/runtime/maps":
		return true
	case "runtime":
		switch fn.Sym().Name {
		case "strequal", "interequal", "nilinterequal":
			return true
		}
	}
	return false
}

// tearGuard reports whether a value of type t at addr is loaded and
// stored under the sequence locks.
func (s *state) tearGuard(t *types.Type, addr *ssa.Value) bool {
	return s.tear && !s.dstLocal && !isStackAddr(addr) && s.tearable(t) && !s.unpublished(addr)
}

// tearGuardStore is tearGuard for a store. If it is not guarded because
// the object is new, its plain stores must be seen before the pointer to
// it is: see sealFills.
func (s *state) tearGuardStore(t *types.Type, addr *ssa.Value) bool {
	if !s.tear || s.dstLocal || isStackAddr(addr) || !s.tearable(t) {
		return false
	}
	if !s.unpublished(addr) {
		return true
	}
	if sealArch(s.gd.Ctxt.Arch.Family) {
		if s.fills == nil {
			s.fills = make(map[*ssa.Value]bool)
		}
		s.fills[addrBase(addr)] = true
	}
	return false
}

// sealArch reports whether stores may be seen out of order on the target
// (a weakly ordered one with 8-byte words), so that a new object filled
// with plain stores needs a barrier before anything can publish it.
func sealArch(f sys.ArchFamily) bool {
	switch f {
	case sys.ARM64, sys.Loong64, sys.MIPS64, sys.PPC64, sys.RISCV64:
		return true
	}
	return false
}

// addrBase returns the pointer that addr is an offset from.
func addrBase(addr *ssa.Value) *ssa.Value {
	for addr.Op == ssa.OpOffPtr || addr.Op == ssa.OpAddPtr || addr.Op == ssa.OpPtrIndex || addr.Op == ssa.OpCopy || addr.Op == ssa.OpNilCheck {
		addr = addr.Args[0]
	}
	return addr
}

// sealFills adds one store barrier after the last plain store of each
// straight-line fill of a new object (doc/gd/publication-point.md, §5).
//
// tearGuardStore let those stores be plain because nothing but stores in
// the object and the stack happened since it was allocated. So every point
// that could publish the object comes after the run of such stores ends,
// and a barrier there orders all of them before it, wherever it is: a
// store, a call, a return. A run is in one block, since unpublished stops
// at the start of its block. It goes on across any store in the same
// object, guarded or not, and across stores to the stack.
//
// It runs before the optimization passes, which may merge or remove the
// stores: from here on the barrier is a memory operation that none of them
// moves a store across.
func (s *state) sealFills() {
	if len(s.fills) == 0 {
		return
	}
	seals := make(map[*ssa.Value]*ssa.Value) // last store of a run -> its barrier
	seal := func(last *ssa.Value) {
		seals[last] = last.Block.NewValue1(last.Pos, ssa.OpPubBarrier, types.TypeMem, last)
	}
	for _, b := range s.f.Blocks {
		var open, last *ssa.Value // the object of the run, and its last store
		for _, m := range s.blockMemChain(b) {
			switch m.Op {
			case ssa.OpStore, ssa.OpZero, ssa.OpMove:
				dst := m.Args[0]
				if isStackAddr(dst) {
					continue
				}
				base := addrBase(dst)
				if base == open {
					last = m
					continue
				}
				if open != nil {
					seal(last)
				}
				open, last = nil, nil
				if s.fills[base] {
					open, last = base, m
				}
			case ssa.OpVarDef, ssa.OpVarLive:
			default:
				if open != nil {
					seal(last)
				}
				open, last = nil, nil
			}
		}
		if open != nil {
			seal(last)
		}
	}
	// What goes on from a sealed store goes on from its barrier. (A load
	// may stay before the barrier: it sees the goroutine's own stores
	// either way, and is forwarded from them.)
	for _, b := range s.f.Blocks {
		for _, v := range b.Values {
			if v.Op == ssa.OpPubBarrier && seals[v.Args[0]] == v {
				continue
			}
			if !v.Type.IsMemory() && !v.Type.IsTuple() && !v.Type.IsResults() {
				continue
			}
			for i, a := range v.Args {
				if r := seals[a]; r != nil {
					v.SetArg(i, r)
				}
			}
		}
		for i, c := range b.ControlValues() {
			if r := seals[c]; r != nil {
				b.ReplaceControl(i, r)
			}
		}
	}
}

// blockMemChain returns the memory values of b in the order they happen.
func (s *state) blockMemChain(b *ssa.Block) []*ssa.Value {
	prev := func(m *ssa.Value) *ssa.Value {
		switch m.Op {
		case ssa.OpPhi:
			return nil
		case ssa.OpSelectN, ssa.OpSelect0, ssa.OpSelect1:
			// The memory of a call, which is one of its results, or
			// of an atomic operation, which is half of a tuple.
			m = m.Args[0]
			if m.Type.IsMemory() {
				return m
			}
		}
		p := m.MemoryArg()
		for p != nil && p.Op == ssa.OpCopy {
			p = p.Args[0] // (What insertPhis made of a forward reference.)
		}
		return p
	}
	var first *ssa.Value
	n := 0
	next := make(map[*ssa.Value]*ssa.Value)
	for _, v := range b.Values {
		if !v.Type.IsMemory() || v.Op == ssa.OpCopy {
			continue
		}
		n++
		if p := prev(v); p != nil && p.Block == b {
			if next[p] != nil {
				s.Fatalf("sealFills: two memory states follow %v in %v: %v and %v", p.LongString(), b, next[p].LongString(), v.LongString())
			}
			next[p] = v
		} else if first != nil {
			s.Fatalf("sealFills: memory of %v does not start at one value: %v, %v", b, first, v)
		} else {
			first = v
		}
	}
	chain := make([]*ssa.Value, 0, n)
	for m := first; m != nil; m = next[m] {
		chain = append(chain, m)
	}
	if len(chain) != n {
		s.Fatalf("sealFills: %d of the %d memory values of %v are in its chain", len(chain), n, b)
	}
	return chain
}

// isStackAddr is ssa.IsStackAddr, which also sees through the nil checks
// that are still there when the stores are made (p := &T{...} in the
// stack; p.f = x).
func isStackAddr(v *ssa.Value) bool {
	for v.Op == ssa.OpOffPtr || v.Op == ssa.OpAddPtr || v.Op == ssa.OpPtrIndex || v.Op == ssa.OpCopy || v.Op == ssa.OpNilCheck {
		v = v.Args[0]
	}
	return ssa.IsStackAddr(v)
}

// tearable reports whether a value of type t holds values that the
// function loads and stores whole: slices, strings and interfaces, or
// the interfaces alone.
func (s *state) tearable(t *types.Type) bool {
	if s.ifaceOnly {
		return types.HasWhole(t)
	}
	return types.HasTearable(t)
}

// localExpr reports whether n designates memory that escape analysis
// found no other goroutine can reach while the function runs: a variable
// whose memory is not shared, or what a variable that only points to such
// memory points to.
func (s *state) localExpr(n ir.Node) bool {
	if !s.gd.Sharing || s.gd.Debug.NoLocal != 0 {
		return false
	}
	for {
		switch n.Op() {
		case ir.ODOT:
			n = n.(*ir.SelectorExpr).X
		case ir.OCONVNOP:
			n = n.(*ir.ConvExpr).X
		case ir.OPAREN:
			n = n.(*ir.ParenExpr).X
		case ir.OINDEX:
			n := n.(*ir.IndexExpr)
			if n.X.Type().IsSlice() {
				return localPointer(n.X)
			}
			if !n.X.Type().IsArray() {
				return false
			}
			return s.localExpr(n.X)
		case ir.ODOTPTR:
			return localPointer(n.(*ir.SelectorExpr).X)
		case ir.ODEREF:
			return localPointer(n.(*ir.StarExpr).X)
		case ir.ONAME:
			n := n.(*ir.Name)
			return n.Class == ir.PAUTO && n.Unshared&ir.UnsharedStorage != 0
		default:
			return false
		}
	}
}

// localPointer reports whether the pointer or slice n is a variable that
// only points to memory no other goroutine can reach.
func localPointer(n ir.Node) bool {
	for n.Op() == ir.OCONVNOP {
		x := n.(*ir.ConvExpr).X
		if !x.Type().HasPointers() {
			return false
		}
		n = x
	}
	name, ok := n.(*ir.Name)
	return ok && name.Op() == ir.ONAME && name.Class == ir.PAUTO && name.Unshared&ir.UnsharedPointee != 0
}

// loadFrom is load for the value of the expression n, at src.
func (s *state) loadFrom(n ir.Node, t *types.Type, src *ssa.Value) *ssa.Value {
	if s.tear && s.localExpr(n) {
		s.shareCheck(src)
		return s.loadWhole(t, src)
	}
	if name, ok := n.(*ir.Name); ok && name.Op() == ir.ONAME && name.Class == ir.PEXTERN && name.Readonly() {
		// Nothing stores in a read-only variable: it loads whole.
		return s.loadWhole(t, src)
	}
	return s.load(t, src)
}

// shareCheck has the runtime check, with -d=sharecheck, that the memory at
// addr, which escape analysis found no other goroutine can reach, has not
// been published.
func (s *state) shareCheck(addr *ssa.Value) {
	if s.gd.Debug.ShareCheck != 0 && !s.gd.Flag.CompilingRuntime && tearFunc(s.curfn) && !ssa.IsStackAddr(addr) {
		s.rtcall(ir.Syms(s.gd).ShareCheck, true, nil, addr)
	}
}

// unpublished reports whether addr is in an object that the function
// allocated and has only stored in since, in the object itself and in the
// stack: no other goroutine can reach it yet.
func (s *state) unpublished(addr *ssa.Value) bool {
	if s.hasdefer && sealArch(s.gd.Ctxt.Arch.Family) {
		// A fault in the middle of a fill (p.t = q.v) may run a deferred
		// function that publishes the object, with no barrier after
		// the stores made so far. Stores are in order on the targets
		// that are not weakly ordered, and a fault does not land
		// between the words of one value.
		return false
	}
	base := addrBase(addr)
	if base.Op != ssa.OpSelectN || base.AuxInt != 0 {
		return false
	}
	call := base.Args[0]
	if call.Op != ssa.OpStaticLECall {
		return false
	}
	switch fn := call.Aux.(*ssa.AuxCall).Fn; {
	case fn == ir.Syms(s.gd).Newobject, fn == ir.Syms(s.gd).MallocGC:
	case fn != nil && strings.HasPrefix(fn.Name, "runtime.mallocgc"):
		// A malloc for one size.
	default:
		return false
	}
	mem := s.mem()
	for n := 0; n < 100; n++ {
		switch mem.Op {
		case ssa.OpSelectN:
			return mem.Args[0] == call
		case ssa.OpStore, ssa.OpZero, ssa.OpMove:
			p := mem.Args[0]
			if !ssa.IsStackAddr(p) {
				if addrBase(p) != base {
					return false
				}
			}
		case ssa.OpVarDef, ssa.OpVarLive:
		default:
			return false
		}
		mem = mem.MemoryArg()
	}
	return false
}

// shareSeqAddr returns the address of the sequence lock of addr's block.
func (s *state) shareSeqAddr(addr *ssa.Value) *ssa.Value {
	uptr := types.Types[types.TUINTPTR]
	u32p := types.NewPtr(types.Types[types.TUINT32])
	p := s.newValue2(ssa.OpConvert, uptr, addr, s.mem())
	// The offset of the lock in the table is the address modulo the
	// table's size, at the start of its block.
	var off *ssa.Value
	if s.config.PtrSize == 8 {
		off = s.newValue2(ssa.OpAnd64, uptr, p, s.constInt64(uptr, (shareStripes-1)<<shareBlockShift))
	} else {
		off = s.newValue2(ssa.OpAnd32, uptr, p, s.constInt32(uptr, (shareStripes-1)<<shareBlockShift))
	}
	base := s.entryNewValue1A(ssa.OpAddr, u32p, ir.Syms(s.gd).ShareSeq, s.sb)
	return s.newValue2(ssa.OpAddPtr, u32p, base, off)
}

// shareSeq loads a sequence lock. The load is a memory operation, so
// that what is loaded between two of them stays between them.
func (s *state) shareSeq(seq *ssa.Value) *ssa.Value {
	u32 := types.Types[types.TUINT32]
	if !tearArch(s.gd.Ctxt.Arch.Family) {
		// No inline atomic load: a call, which is one too.
		return s.rtcall(ir.Syms(s.gd).ShareSeqLoad, true, []*types.Type{u32}, seq)[0]
	}
	v := s.newValue2(ssa.OpAtomicLoad32, types.NewTuple(u32, types.TypeMem), seq, s.mem())
	s.vars[memVar] = s.newValue1(ssa.OpSelect1, types.TypeMem, v)
	return s.newValue1(ssa.OpSelect0, u32, v)
}

// shareLoaded is called between the load of a value and the second load
// of its sequence lock. Where loads may be reordered (arm64), the value's
// must be done before the lock is read again.
func (s *state) shareLoaded() {
	if s.gd.Ctxt.Arch.Family == sys.ARM64 {
		// DMB ISHLD
		s.vars[memVar] = s.newValue1I(ssa.OpARM64DMB, types.TypeMem, 0x9, s.mem())
	}
}

// shareQuiet reports whether no store happened between the two loads s1
// and s2 of a sequence lock: it did not move and was not held.
func (s *state) shareQuiet(s1, s2 *ssa.Value) *ssa.Value {
	u32 := types.Types[types.TUINT32]
	x := s.newValue2(ssa.OpXor32, u32, s1, s2)
	x = s.newValue2(ssa.OpOr32, u32, x, s.newValue2(ssa.OpAnd32, u32, s1, s.constInt32(u32, 1)))
	return s.newValue2(ssa.OpEq32, types.Types[types.TBOOL], x, s.constInt32(u32, 0))
}

// shareLoad loads a value of type t, which tearGuard says to guard, from
// src.
func (s *state) shareLoad(t *types.Type, src *ssa.Value) *ssa.Value {
	switch {
	case types.IsWholeStruct(t):
		return s.shareLoadWhole(t, src)
	case t.IsStruct():
		// Each field is whole; the struct need not be.
		// (The loads end blocks: the struct is made after them.)
		var args []*ssa.Value
		for _, f := range t.Fields() {
			if f.Type.Size() == 0 {
				args = append(args, s.zeroVal(f.Type))
				continue
			}
			args = append(args, s.load(f.Type, s.newValue1I(ssa.OpOffPtr, types.NewPtr(f.Type), f.Offset, src)))
		}
		v := s.newValue0(ssa.OpStructMake, t)
		v.AddArgs(args...)
		return v
	case t.IsArray():
		return s.newValue1(ssa.OpArrayMake1, t, s.load(t.Elem(), s.newValue1I(ssa.OpOffPtr, types.NewPtr(t.Elem()), 0, src)))
	case t.IsString() && s.ifaceOnly && stringAtomicArch(s.gd.Ctxt.Arch.Family):
		return s.shareLoadString(t, src)
	case t.IsSlice() && s.ifaceOnly && stringAtomicArch(s.gd.Ctxt.Arch.Family):
		return s.shareLoadSlice(t, src)
	}

	if t.IsSlice() && types.SlicePacked() {
		return s.shareLoadSliceLocked(t, src)
	}

	// A variable of its own: the values of two loads have two types.
	shareVar := ssaMarker("share")
	seq := s.shareSeqAddr(src)
	s1 := s.shareSeq(seq)
	v := s.rawLoad(t, src)
	s.shareLoaded()
	s2 := s.shareSeq(seq)
	quiet := s.shareQuiet(s1, s2)

	bSlow := s.f.NewBlock(ssa.BlockPlain)
	bDone := s.f.NewBlock(ssa.BlockPlain)
	s.vars[shareVar] = v
	b := s.endBlock()
	b.Kind = ssa.BlockIf
	b.SetControl(quiet)
	b.Likely = ssa.BranchLikely
	b.AddEdgeTo(bDone)
	b.AddEdgeTo(bSlow)

	// A store was in progress: the runtime waits for it.
	s.startBlock(bSlow)
	fn := ir.Syms(s.gd).ShareLoadSlice
	switch {
	case t.IsString():
		fn = ir.Syms(s.gd).ShareLoadString
	case t.IsInterface():
		fn = ir.Syms(s.gd).ShareLoadIface
	}
	s.vars[shareVar] = s.rtcall(fn, true, []*types.Type{t}, src)[0]
	s.endBlock().AddEdgeTo(bDone)

	s.startBlock(bDone)
	v = s.variable(shareVar, t)
	delete(s.vars, shareVar)
	return v
}

// shareLoadWhole is shareLoad for a struct that goes whole
// (types.IsWholeStruct), under the sequence lock of the block where it
// starts, which every store of it holds, as for an interface.
func (s *state) shareLoadWhole(t *types.Type, src *ssa.Value) *ssa.Value {
	shareVar := ssaMarker("share")
	seq := s.shareSeqAddr(src)
	s1 := s.shareSeq(seq)
	v := s.rawLoad(t, src)
	s.shareLoaded()
	s2 := s.shareSeq(seq)
	quiet := s.shareQuiet(s1, s2)

	bSlow := s.f.NewBlock(ssa.BlockPlain)
	bDone := s.f.NewBlock(ssa.BlockPlain)
	s.vars[shareVar] = v
	b := s.endBlock()
	b.Kind = ssa.BlockIf
	b.SetControl(quiet)
	b.Likely = ssa.BranchLikely
	b.AddEdgeTo(bDone)
	b.AddEdgeTo(bSlow)

	// A store was in progress: the runtime waits for it. (The temporary
	// is live across its call, so it holds a value before.)
	s.startBlock(bSlow)
	tmp := typecheck.TempAt(s.gd, s.peekPos(), s.curfn, t)
	tmp.SetAddrtaken(true)
	s.vars[memVar] = s.newValue1A(ssa.OpVarDef, types.TypeMem, tmp, s.mem())
	tmpAddr := s.addr(tmp)
	s.rawZero(t, tmpAddr)
	s.rtcall(ir.Syms(s.gd).ShareLoadMem, true, nil, tmpAddr, s.nilCheck(src), s.constInt(types.Types[types.TUINTPTR], t.Size()))
	s.vars[shareVar] = s.rawLoad(t, tmpAddr)
	s.endBlock().AddEdgeTo(bDone)

	s.startBlock(bDone)
	v = s.variable(shareVar, t)
	delete(s.vars, shareVar)
	return v
}

// shareLoadSliceLocked is shareLoad for a packed slice (types.SlicePacked)
// under the sequence lock: its words are loaded between the checks of the
// lock, and unpacked only once the lock says they go together (a torn
// huge slice's w0 is no header).
func (s *state) shareLoadSliceLocked(t *types.Type, src *ssa.Value) *ssa.Value {
	ptrT := t.Elem().PtrTo()
	u64 := types.Types[types.TUINT64]
	w0Var, w1Var := ssaMarker("slicew0"), ssaMarker("slicew1")
	seq := s.shareSeqAddr(src)
	s1 := s.shareSeq(seq)
	w0 := s.rawLoad(ptrT, src)
	w1 := s.rawLoad(u64, s.newValue1I(ssa.OpOffPtr, types.NewPtr(u64), 8, src))
	s.shareLoaded()
	s2 := s.shareSeq(seq)
	quiet := s.shareQuiet(s1, s2)

	bQuiet := s.f.NewBlock(ssa.BlockPlain)
	bSlow := s.f.NewBlock(ssa.BlockPlain)
	bDone := s.f.NewBlock(ssa.BlockPlain)
	b := s.endBlock()
	b.Kind = ssa.BlockIf
	b.SetControl(quiet)
	b.Likely = ssa.BranchLikely
	b.AddEdgeTo(bQuiet)
	b.AddEdgeTo(bSlow)

	s.startBlock(bQuiet)
	s.vars[w0Var], s.vars[w1Var] = w0, w1
	s.endBlock().AddEdgeTo(bDone)

	// A store was in progress: the runtime waits for it.
	s.startBlock(bSlow)
	s.vars[w0Var], s.vars[w1Var] = s.shareLoadSliceWords(t, src)
	s.endBlock().AddEdgeTo(bDone)

	// (One unpack of the words of both.)
	s.startBlock(bDone)
	v := s.sliceUnpack(t, s.variable(w0Var, ptrT), s.variable(w1Var, u64))
	delete(s.vars, w0Var)
	delete(s.vars, w1Var)
	return v
}

// shareLoadSliceWords calls runtime.shareloadslice for the slice of type
// t at src, and returns its words, packed (doc/gd/slice16.md): its
// result is a slice, two words in two registers, or two in the frame.
func (s *state) shareLoadSliceWords(t *types.Type, src *ssa.Value) (w0, w1 *ssa.Value) {
	r := s.rtcall(ir.Syms(s.gd).ShareLoadSlice, true, []*types.Type{t.Elem().PtrTo(), types.Types[types.TUINT64]}, src)
	return r[0], r[1]
}

// shareLoadMem copies a value of type t from src, which is not in the
// stack, to dst, which is.
func (s *state) shareLoadMem(t *types.Type, dst, src *ssa.Value) {
	uptr := types.Types[types.TUINTPTR]
	seq := s.shareSeqAddr(src)
	s1 := s.shareSeq(seq)
	s.rawMove(t, dst, src)
	s.shareLoaded()
	s2 := s.shareSeq(seq)
	quiet := s.shareQuiet(s1, s2)
	if t.Size() > 1 && !types.IsWholeStruct(t) {
		// One lock was checked: the value must be in one block. (Not
		// for one that goes whole: every store holds the lock of the
		// block where it starts.)
		p := s.newValue2(ssa.OpConvert, uptr, src, s.mem())
		var onePage *ssa.Value
		if s.config.PtrSize == 8 {
			last := s.newValue2(ssa.OpAdd64, uptr, p, s.constInt64(uptr, t.Size()-1))
			x := s.newValue2(ssa.OpRsh64Ux64, uptr, s.newValue2(ssa.OpXor64, uptr, p, last), s.constInt64(types.Types[types.TUINT64], shareBlockShift))
			onePage = s.newValue2(ssa.OpEq64, types.Types[types.TBOOL], x, s.constInt64(uptr, 0))
		} else {
			last := s.newValue2(ssa.OpAdd32, uptr, p, s.constInt32(uptr, int32(t.Size()-1)))
			x := s.newValue2(ssa.OpRsh32Ux64, uptr, s.newValue2(ssa.OpXor32, uptr, p, last), s.constInt64(types.Types[types.TUINT64], shareBlockShift))
			onePage = s.newValue2(ssa.OpEq32, types.Types[types.TBOOL], x, s.constInt32(uptr, 0))
		}
		quiet = s.newValue2(ssa.OpAndB, types.Types[types.TBOOL], quiet, onePage)
	}

	bSlow := s.f.NewBlock(ssa.BlockPlain)
	bDone := s.f.NewBlock(ssa.BlockPlain)
	b := s.endBlock()
	b.Kind = ssa.BlockIf
	b.SetControl(quiet)
	b.Likely = ssa.BranchLikely
	b.AddEdgeTo(bDone)
	b.AddEdgeTo(bSlow)

	s.startBlock(bSlow)
	s.rtcall(ir.Syms(s.gd).ShareLoadMem, true, nil, dst, src, s.constInt(uptr, t.Size()))
	s.endBlock().AddEdgeTo(bDone)

	s.startBlock(bDone)
}

// shareLock is called before the store of size bytes at dst, which is not
// in the stack. It returns whether the runtime took the locks, which
// shareUnlock gives back.
// It first loads the byte that says whether anything in a page like dst's
// was ever shared (runtime.sharePages), and does not call the runtime if
// nothing was.
func (s *state) shareLock(dst *ssa.Value, size int64) *ssa.Value {
	uptr := types.Types[types.TUINTPTR]
	u8 := types.Types[types.TUINT8]
	p := s.newValue2(ssa.OpConvert, uptr, dst, s.mem())
	var idx *ssa.Value
	if s.config.PtrSize == 8 {
		idx = s.newValue2(ssa.OpRsh64Ux64, uptr, p, s.constInt64(types.Types[types.TUINT64], sharePageShift))
		idx = s.newValue2(ssa.OpAnd64, uptr, idx, s.constInt64(uptr, sharePagesLen-1))
	} else {
		idx = s.newValue2(ssa.OpRsh32Ux64, uptr, p, s.constInt64(types.Types[types.TUINT64], sharePageShift))
		idx = s.newValue2(ssa.OpAnd32, uptr, idx, s.constInt32(uptr, sharePagesLen-1))
	}
	base := s.entryNewValue1A(ssa.OpAddr, types.NewPtr(u8), ir.Syms(s.gd).SharePages, s.sb)
	any := s.rawLoad(u8, s.newValue2(ssa.OpAddPtr, types.NewPtr(u8), base, idx))
	cond := s.newValue2(ssa.OpNeq8, types.Types[types.TBOOL], any, s.constInt8(u8, 0))

	lockedVar := ssaMarker("locked")
	bCall := s.f.NewBlock(ssa.BlockPlain)
	bDone := s.f.NewBlock(ssa.BlockPlain)
	s.vars[lockedVar] = s.constBool(false)
	b := s.endBlock()
	b.Kind = ssa.BlockIf
	b.SetControl(cond)
	b.Likely = ssa.BranchUnlikely
	b.AddEdgeTo(bCall)
	b.AddEdgeTo(bDone)

	s.startBlock(bCall)
	s.vars[lockedVar] = s.rtcall(ir.Syms(s.gd).ShareStore, true, []*types.Type{types.Types[types.TBOOL]}, dst, s.constInt(uptr, size))[0]
	s.endBlock().AddEdgeTo(bDone)

	s.startBlock(bDone)
	locked := s.variable(lockedVar, types.Types[types.TBOOL])
	delete(s.vars, lockedVar)
	return locked
}

// sharePage loads the byte that says whether anything in a page like
// dst's was ever shared.
func (s *state) sharePage(dst *ssa.Value) *ssa.Value {
	uptr := types.Types[types.TUINTPTR]
	u8 := types.Types[types.TUINT8]
	p := s.newValue2(ssa.OpConvert, uptr, dst, s.mem())
	var idx *ssa.Value
	if s.config.PtrSize == 8 {
		idx = s.newValue2(ssa.OpRsh64Ux64, uptr, p, s.constInt64(types.Types[types.TUINT64], sharePageShift))
		idx = s.newValue2(ssa.OpAnd64, uptr, idx, s.constInt64(uptr, sharePagesLen-1))
	} else {
		idx = s.newValue2(ssa.OpRsh32Ux64, uptr, p, s.constInt64(types.Types[types.TUINT64], sharePageShift))
		idx = s.newValue2(ssa.OpAnd32, uptr, idx, s.constInt32(uptr, sharePagesLen-1))
	}
	base := s.entryNewValue1A(ssa.OpAddr, types.NewPtr(u8), ir.Syms(s.gd).SharePages, s.sb)
	return s.rawLoad(u8, s.newValue2(ssa.OpAddPtr, types.NewPtr(u8), base, idx))
}

// appendGrouped ends the append n to the slice at addr, which is not in
// the stack and may be shared, as may its array: it stores the length l
// (the pointer p and the capacity c too if the slice grew, or if another
// goroutine stored another slice there) and the nargs elements.
//
// Each of these stores would take the locks of what it stores in, and
// give them back. They take them once for all: no synchronisation
// separates the stores, so no goroutine can tell, and a program in which
// one could has no say in how they interleave. Nothing between the locks
// can fault, call, or wait for another lock: the operands are evaluated
// before, and the stores are in memory the append has loaded from or
// that growslice returned.
func (s *state) appendGrouped(n *ir.CallExpr, addr, p, l, c, nargs *ssa.Value) {
	tInt := types.Types[types.TINT]
	uptr := types.Types[types.TUINTPTR]
	et := n.Type().Elem()
	pt := types.NewPtr(et)

	var vals, addrs []*ssa.Value
	for _, a := range n.Args[1:] {
		vals = append(vals, s.expr(a))
	}
	oldLen := s.newValue2(s.ssaOp(ir.OSUB, tInt), tInt, l, nargs)
	p2 := s.newValue2(ssa.OpPtrIndex, pt, p, oldLen)
	for i := range vals {
		ea := s.newValue2(ssa.OpPtrIndex, pt, p2, s.constInt(tInt, int64(i)))
		s.instrument(et, ea, instrumentWrite)
		addrs = append(addrs, ea)
	}
	lenaddr := s.newValue1I(ssa.OpOffPtr, s.f.Config.Types.IntPtr, types.SliceLenOffset, addr)
	capaddr := s.newValue1I(ssa.OpOffPtr, s.f.Config.Types.IntPtr, types.SliceCapOffset, addr)
	hdrSize := s.constInt(uptr, n.Type().Size())
	elemSize := s.constInt(uptr, int64(len(vals))*et.Size())

	u8 := types.Types[types.TUINT8]
	any := s.newValue2(ssa.OpOr8, u8, s.sharePage(addr), s.sharePage(p2))
	cond := s.newValue2(ssa.OpNeq8, types.Types[types.TBOOL], any, s.constInt8(u8, 0))

	bCall := s.f.NewBlock(ssa.BlockPlain)
	bLocked := s.f.NewBlock(ssa.BlockPlain)
	bAll := s.f.NewBlock(ssa.BlockPlain)
	bLen := s.f.NewBlock(ssa.BlockPlain)
	bElems := s.f.NewBlock(ssa.BlockPlain)
	bLocal := s.f.NewBlock(ssa.BlockPlain)
	bLocalAll := s.f.NewBlock(ssa.BlockPlain)
	bLocalElems := s.f.NewBlock(ssa.BlockPlain)
	bDone := s.f.NewBlock(ssa.BlockPlain)

	b := s.endBlock()
	b.Kind = ssa.BlockIf
	b.SetControl(cond)
	b.Likely = ssa.BranchUnlikely
	b.AddEdgeTo(bCall)
	b.AddEdgeTo(bLocal)

	s.startBlock(bCall)
	locked := s.rtcall(ir.Syms(s.gd).ShareStore2, true, []*types.Type{types.Types[types.TBOOL]}, addr, hdrSize, p2, elemSize)[0]
	b = s.endBlock()
	b.Kind = ssa.BlockIf
	b.SetControl(locked)
	b.AddEdgeTo(bLocked)
	b.AddEdgeTo(bLocal)

	// With the locks: the length alone if the array is the one that
	// is there, with at least that capacity.
	s.startBlock(bLocked)
	capNow := s.rawLoad(tInt, capaddr)
	fits := s.newValue2(s.ssaOp(ir.OLE, types.Types[types.TUINT]), types.Types[types.TBOOL], l, capNow)
	fits = s.newValue2(ssa.OpAndB, types.Types[types.TBOOL], fits, s.newValue2(ssa.OpEqPtr, types.Types[types.TBOOL], s.rawLoad(pt, addr), p))
	b = s.endBlock()
	b.Kind = ssa.BlockIf
	b.SetControl(fits)
	b.Likely = ssa.BranchLikely
	b.AddEdgeTo(bLen)
	b.AddEdgeTo(bAll)

	s.startBlock(bAll)
	s.store(tInt, lenaddr, l)
	s.store(tInt, capaddr, c)
	s.store(pt, addr, p)
	s.endBlock().AddEdgeTo(bElems)

	s.startBlock(bLen)
	s.store(tInt, lenaddr, l)
	s.endBlock().AddEdgeTo(bElems)

	s.startBlock(bElems)
	for i, v := range vals {
		s.rawStoreType(et, addrs[i], v, 0, true)
	}
	s.rtcall(ir.Syms(s.gd).ShareUnlock2, true, nil, addr, hdrSize, p2, elemSize)
	s.endBlock().AddEdgeTo(bDone)

	// Neither is shared, which the runtime may have just found: what
	// is stored there needs no publishing either.
	s.startBlock(bLocal)
	s.store(tInt, lenaddr, l)
	same := s.newValue2(ssa.OpEqPtr, types.Types[types.TBOOL], s.rawLoad(pt, addr), p)
	b = s.endBlock()
	b.Kind = ssa.BlockIf
	b.SetControl(same)
	b.Likely = ssa.BranchLikely
	b.AddEdgeTo(bLocalElems)
	b.AddEdgeTo(bLocalAll)

	// The slice grew.
	s.startBlock(bLocalAll)
	s.dstLocal = true
	s.store(tInt, capaddr, c)
	s.store(pt, addr, p)
	s.dstLocal = false
	s.endBlock().AddEdgeTo(bLocalElems)

	s.startBlock(bLocalElems)
	s.dstLocal = true
	for i, v := range vals {
		s.rawStoreType(et, addrs[i], v, 0, true)
	}
	s.dstLocal = false
	s.endBlock().AddEdgeTo(bDone)

	s.startBlock(bDone)
}

func (s *state) shareUnlock(locked, dst *ssa.Value, size int64) {
	bUnlock := s.f.NewBlock(ssa.BlockPlain)
	bDone := s.f.NewBlock(ssa.BlockPlain)
	b := s.endBlock()
	b.Kind = ssa.BlockIf
	b.SetControl(locked)
	b.Likely = ssa.BranchUnlikely
	b.AddEdgeTo(bUnlock)
	b.AddEdgeTo(bDone)

	s.startBlock(bUnlock)
	s.rtcall(ir.Syms(s.gd).ShareUnlock, true, nil, dst, s.constInt(types.Types[types.TUINTPTR], size))
	s.endBlock().AddEdgeTo(bDone)

	s.startBlock(bDone)
}

// shareStoreType is storeType for a value tearGuard says to guard. A
// store of only some of the words of a slice (s = s[:n] stores a length)
// is, in shared memory, still that if what it stores fits the slice that
// is there now, and a store of the whole slice otherwise: another
// goroutine may have stored a shorter one since this one was loaded.
func (s *state) shareStoreType(t *types.Type, left, right *ssa.Value, skip skipMask, leftIsStmt bool) {
	if s.ifaceOnly {
		s.shareStoreParts(t, left, right, leftIsStmt)
		return
	}
	if skip == 0 && s.gd.Debug.NoGroup == 0 && (t.IsString() || t.IsSlice() || t.IsInterface()) {
		// The whole of a string, a slice or an interface: the
		// runtime stores it if the memory is shared, and this
		// function has only the store for memory that is not.
		fn := ir.Syms(s.gd).ShareStoreSlice
		switch {
		case t.IsString():
			fn = ir.Syms(s.gd).ShareStoreString
		case t.IsInterface():
			fn = ir.Syms(s.gd).ShareStoreIface
		}
		cond := s.newValue2(ssa.OpNeq8, types.Types[types.TBOOL], s.sharePage(left), s.constInt8(types.Types[types.TUINT8], 0))
		bCall := s.f.NewBlock(ssa.BlockPlain)
		bLocal := s.f.NewBlock(ssa.BlockPlain)
		bDone := s.f.NewBlock(ssa.BlockPlain)
		b := s.endBlock()
		b.Kind = ssa.BlockIf
		b.SetControl(cond)
		b.Likely = ssa.BranchUnlikely
		b.AddEdgeTo(bCall)
		b.AddEdgeTo(bLocal)

		s.startBlock(bCall)
		stored := s.rtcall(fn, true, []*types.Type{types.Types[types.TBOOL]}, left, right)[0]
		b = s.endBlock()
		b.Kind = ssa.BlockIf
		b.SetControl(stored)
		b.Likely = ssa.BranchLikely
		b.AddEdgeTo(bDone)
		b.AddEdgeTo(bLocal)

		s.startBlock(bLocal)
		s.dstLocal = true
		s.rawStoreType(t, left, right, 0, leftIsStmt)
		s.dstLocal = false
		s.endBlock().AddEdgeTo(bDone)

		s.startBlock(bDone)
		return
	}
	locked := s.shareLock(left, t.Size())
	if skip == 0 {
		// Two copies of the store: if the memory is not shared,
		// which the runtime has just found, what is stored there
		// needs no publishing either, and the pointer stores of
		// that copy say so to the write barrier pass.
		bShared := s.f.NewBlock(ssa.BlockPlain)
		bLocal := s.f.NewBlock(ssa.BlockPlain)
		bDone := s.f.NewBlock(ssa.BlockPlain)
		b := s.endBlock()
		b.Kind = ssa.BlockIf
		b.SetControl(locked)
		b.Likely = ssa.BranchUnlikely
		b.AddEdgeTo(bShared)
		b.AddEdgeTo(bLocal)

		s.startBlock(bShared)
		s.rawStoreType(t, left, right, 0, leftIsStmt)
		s.rtcall(ir.Syms(s.gd).ShareUnlock, true, nil, left, s.constInt(types.Types[types.TUINTPTR], t.Size()))
		s.endBlock().AddEdgeTo(bDone)

		s.startBlock(bLocal)
		s.dstLocal = true
		s.rawStoreType(t, left, right, 0, leftIsStmt)
		s.dstLocal = false
		s.endBlock().AddEdgeTo(bDone)

		s.startBlock(bDone)
		return
	}
	bLocked := s.f.NewBlock(ssa.BlockPlain)
	bAll := s.f.NewBlock(ssa.BlockPlain)
	bSome := s.f.NewBlock(ssa.BlockPlain)
	bUnlock := s.f.NewBlock(ssa.BlockPlain)
	bDone := s.f.NewBlock(ssa.BlockPlain)
	b := s.endBlock()
	b.Kind = ssa.BlockIf
	b.SetControl(locked)
	b.Likely = ssa.BranchUnlikely
	b.AddEdgeTo(bLocked)
	b.AddEdgeTo(bSome)

	// With the locks: does it fit?
	s.startBlock(bLocked)
	fits := s.constBool(false)
	if t.IsSlice() {
		fits = s.shareFits(left, right, skip)
	}
	b = s.endBlock()
	b.Kind = ssa.BlockIf
	b.SetControl(fits)
	b.Likely = ssa.BranchLikely
	b.AddEdgeTo(bSome)
	b.AddEdgeTo(bAll)

	s.startBlock(bAll)
	s.rawStoreType(t, left, right, 0, leftIsStmt)
	s.endBlock().AddEdgeTo(bUnlock)

	s.startBlock(bSome)
	s.rawStoreType(t, left, right, skip, leftIsStmt)
	s.endBlock().AddEdgeTo(bUnlock)

	s.startBlock(bUnlock)
	s.shareUnlock(locked, left, t.Size())
	s.endBlock().AddEdgeTo(bDone)

	s.startBlock(bDone)
}

// shareFits reports whether the words of the slice right that a store
// with skip stores at left make a valid slice with the words that are
// there: the largest of the length and capacity stored is at most the
// capacity that stays, and the pointer stays.
func (s *state) shareFits(left, right *ssa.Value, skip skipMask) *ssa.Value {
	if skip&skipPtr == 0 || skip&skipCap == 0 {
		// A new pointer goes with its own length and capacity; a
		// new capacity is one of the array that was loaded.
		return s.constBool(false)
	}
	intT := types.Types[types.TINT]
	capNow := s.rawLoad(intT, s.newValue1I(ssa.OpOffPtr, s.f.Config.Types.IntPtr, types.SliceCapOffset, left))
	n := s.newValue1(ssa.OpSliceLen, intT, right)
	return s.newValue2(s.ssaOp(ir.OLE, types.Types[types.TUINT]), types.Types[types.TBOOL], n, capNow)
}

// shareStoreParts is storeType for a value that holds interfaces, by
// itself an SSA value, without the sharing analysis: each interface is
// stored whole, and the rest as it is (the value is loaded the same way:
// shareLoad).
func (s *state) shareStoreParts(t *types.Type, left, right *ssa.Value, leftIsStmt bool) {
	switch {
	case t.IsInterface():
		s.shareStoreIface(t, left, right, leftIsStmt)
	case types.IsWholeStruct(t):
		s.shareStoreWhole(t, left, right, leftIsStmt)
	case t.IsString():
		s.shareStoreString(left, right)
	case t.IsSlice():
		s.shareStoreSlice(t, left, right)
	case !types.HasWhole(t):
		s.rawStoreType(t, left, right, 0, leftIsStmt)
	case t.IsStruct():
		for i, f := range t.Fields() {
			if f.Type.Size() == 0 {
				continue
			}
			addr := s.newValue1I(ssa.OpOffPtr, f.Type.PtrTo(), f.Offset, left)
			s.shareStoreParts(f.Type, addr, s.newValue1I(ssa.OpStructSelect, f.Type, int64(i), right), leftIsStmt)
		}
	case t.IsArray() && t.NumElem() == 1:
		s.shareStoreParts(t.Elem(), left, s.newValue1I(ssa.OpArraySelect, t.Elem(), 0, right), leftIsStmt)
	default:
		s.Fatalf("shareStoreParts of %v", t)
	}
}

// ifaceLockInline reports whether the target takes the lock of an
// interface's store inline (shareStoreIface); the others call the runtime.
func ifaceLockInline(f sys.ArchFamily) bool {
	return f == sys.AMD64 || f == sys.ARM64
}

// shareStoreIface stores the interface right at left, which is not in the
// stack, under the sequence lock of left's block: the one that a load of
// it checks (shareLoad), and that the runtime takes for a copy of more.
//
//	seq := &runtime.shareSeq[left's block]
//	v := *seq &^ 1
//	if CAS(seq, v, v+1) {    // likely: no store in that block now
//		*left = right
//		*seq = v + 2      // a release store
//	} else {
//		runtime.ifacestore(left, right)
//	}
//
// The goroutine must not stop while it holds the lock: one that waits for
// it may be unable to let it run again (one P), so the code between the
// CAS and the release, write barrier included, is not a place where it
// may be preempted (ShareLocked and ShareUnlocked mark it for liveness),
// and nothing there can fault or call but the write barrier: the pointer
// is checked for nil first, and the value is computed before.
//
// The lock is found from the address, and taken and given back by the
// same code with nothing between that can move the stack: an address in
// the stack that is not known to be one is locked and unlocked alike.
func (s *state) shareStoreIface(t *types.Type, left, right *ssa.Value, leftIsStmt bool) {
	slow := func(left *ssa.Value) {
		s.rtcall(ir.Syms(s.gd).IfaceStore, true, nil, left, right)
	}
	if !ifaceLockInline(s.gd.Ctxt.Arch.Family) {
		slow(left)
		return
	}
	s.shareStoreLocked(t, left, right, leftIsStmt, slow)
}

// shareStoreWhole stores a struct that goes whole (types.IsWholeStruct)
// as shareStoreIface stores an interface. If another store holds the lock,
// or the target takes none inline, the runtime copies it from the stack.
func (s *state) shareStoreWhole(t *types.Type, left, right *ssa.Value, leftIsStmt bool) {
	slow := func(left *ssa.Value) {
		tmp := typecheck.TempAt(s.gd, s.peekPos(), s.curfn, t)
		tmp.SetAddrtaken(true)
		s.vars[memVar] = s.newValue1A(ssa.OpVarDef, types.TypeMem, tmp, s.mem())
		tmpAddr := s.addr(tmp)
		s.rawStoreType(t, tmpAddr, right, 0, false)
		s.rtcall(ir.Syms(s.gd).ShareTypedmemmove, true, nil, s.reflectType(t), left, tmpAddr)
		s.curfn.SetWBPos(s.gd, s.peekPos())
	}
	if !ifaceLockInline(s.gd.Ctxt.Arch.Family) {
		slow(s.nilCheck(left))
		return
	}
	s.shareStoreLocked(t, left, right, leftIsStmt, slow)
}

// shareStoreLocked is the store of shareStoreIface, with slow for when
// another store holds the lock.
func (s *state) shareStoreLocked(t *types.Type, left, right *ssa.Value, leftIsStmt bool, slow func(left *ssa.Value)) {
	left = s.nilCheck(left)
	u32 := types.Types[types.TUINT32]
	seq := s.shareSeqAddr(left)
	v := s.newValue2(ssa.OpAnd32, u32, s.rawLoad(u32, seq), s.constInt32(u32, ^1))
	cas := s.newValue4(ssa.OpAtomicCompareAndSwap32, types.NewTuple(types.Types[types.TBOOL], types.TypeMem), seq, v, s.newValue2(ssa.OpAdd32, u32, v, s.constInt32(u32, 1)), s.mem())
	s.vars[memVar] = s.newValue1(ssa.OpSelect1, types.TypeMem, cas)
	locked := s.newValue1(ssa.OpSelect0, types.Types[types.TBOOL], cas)

	bLocked := s.f.NewBlock(ssa.BlockPlain)
	bSlow := s.f.NewBlock(ssa.BlockPlain)
	bDone := s.f.NewBlock(ssa.BlockPlain)
	b := s.endBlock()
	b.Kind = ssa.BlockIf
	b.SetControl(locked)
	b.Likely = ssa.BranchLikely
	b.AddEdgeTo(bLocked)
	b.AddEdgeTo(bSlow)

	s.startBlock(bLocked)
	s.vars[memVar] = s.newValue1(ssa.OpShareLocked, types.TypeMem, s.mem())
	if s.gd.Ctxt.Arch.Family == sys.ARM64 {
		// DMB ISHST: the lock is seen held before the stores are seen.
		s.vars[memVar] = s.newValue1I(ssa.OpARM64DMB, types.TypeMem, 0xa, s.mem())
	}
	s.rawStoreType(t, left, right, 0, leftIsStmt)
	unlocked := s.newValue2(ssa.OpAdd32, u32, v, s.constInt32(u32, 2))
	if s.gd.Ctxt.Arch.Family == sys.AMD64 {
		// Stores are seen in their order: a plain store releases.
		s.store(u32, seq, unlocked)
	} else {
		s.vars[memVar] = s.newValue3(ssa.OpAtomicStore32, types.TypeMem, seq, unlocked, s.mem())
	}
	s.vars[memVar] = s.newValue1(ssa.OpShareUnlocked, types.TypeMem, s.mem())
	s.endBlock().AddEdgeTo(bDone)

	// Another store holds the lock: the runtime waits for it.
	s.startBlock(bSlow)
	slow(left)
	s.endBlock().AddEdgeTo(bDone)

	s.startBlock(bDone)
}

// shareZero is zero for a value tearGuard says to guard.
func (s *state) shareZero(t *types.Type, dst *ssa.Value) {
	if s.ifaceOnly {
		if ssa.CanSSA(t) {
			s.shareStoreParts(t, dst, s.zeroVal(t), false)
			return
		}
		// The runtime clears each interface whole.
		s.rtcall(ir.Syms(s.gd).TypedArrayClear, true, nil, s.reflectType(t), dst, s.constInt(types.Types[types.TUINTPTR], 1))
		return
	}
	locked := s.shareLock(dst, t.Size())
	s.rawZero(t, dst)
	s.shareUnlock(locked, dst, t.Size())
}

// shareMove is the move of a value of type t, which holds values of more
// than one word, when dst or src is not in the stack.
func (s *state) shareMove(t *types.Type, dst, src *ssa.Value) {
	// (Memory no other goroutine can reach is as good as the stack.)
	srcStack, dstStack := isStackAddr(src), isStackAddr(dst) || s.dstLocal
	if s.ifaceOnly && dstStack && types.Has16(t) {
		// shareLoadMem's copy does not copy a string whole: load it
		// whole (shareLoad), or have the runtime copy it.
		if ssa.CanSSA(t) {
			s.rawStoreType(t, dst, s.shareLoad(t, src), 0, false)
			return
		}
		// dst is live across the runtime's call: what it holds before
		// is no value, which the collector would scan.
		s.rawZero(t, dst)
		dstStack = false
	}
	switch {
	case dstStack:
		s.shareLoadMem(t, dst, src)
	case srcStack && s.ifaceOnly && ssa.CanSSA(t):
		s.shareStoreParts(t, dst, s.rawLoad(t, src), false)
	case srcStack && !s.ifaceOnly:
		// The calls must not write over a call's results, which
		// are where the next call's arguments go.
		if isCallResultAddr(src) {
			tmp := typecheck.TempAt(s.gd, s.peekPos(), s.curfn, t)
			tmp.SetAddrtaken(true)
			s.vars[memVar] = s.newValue1A(ssa.OpVarDef, types.TypeMem, tmp, s.mem())
			tmpAddr := s.addr(tmp)
			s.rawMove(t, tmpAddr, src)
			src = tmpAddr
		}
		locked := s.shareLock(dst, t.Size())
		s.rawMove(t, dst, src)
		s.shareUnlock(locked, dst, t.Size())
	default:
		// The runtime reads the one whole and writes the other whole
		// (without the analysis, also if it is a stack's).
		if srcStack && isCallResultAddr(src) {
			// (As above.)
			tmp := typecheck.TempAt(s.gd, s.peekPos(), s.curfn, t)
			tmp.SetAddrtaken(true)
			s.vars[memVar] = s.newValue1A(ssa.OpVarDef, types.TypeMem, tmp, s.mem())
			tmpAddr := s.addr(tmp)
			s.rawMove(t, tmpAddr, src)
			src = tmpAddr
		}
		s.rtcall(ir.Syms(s.gd).ShareTypedmemmove, true, nil, s.reflectType(t), dst, src)
		s.curfn.SetWBPos(s.gd, s.peekPos())
	}
}

// isCallResultAddr reports whether addr is in the results of a call.
func isCallResultAddr(addr *ssa.Value) bool {
	for addr.Op == ssa.OpOffPtr || addr.Op == ssa.OpAddPtr || addr.Op == ssa.OpPtrIndex || addr.Op == ssa.OpCopy {
		addr = addr.Args[0]
	}
	return addr.Op == ssa.OpSP || addr.Op == ssa.OpSelectNAddr
}

// Strings, without the sharing analysis (runtime/shared_string.go): with
// 8-byte words a string is aligned to 16 (types.StringAligned16), and is
// loaded and stored wherever it is but in the stack with one 16-byte
// access where the processor makes that single-copy atomic
// (runtime.stringAtomic): on amd64 and arm64. Where it does not, the
// runtime loads and stores it under the sequence lock of its block, as
// on the other targets, which check the lock inline when they load one
// (shareLoad) and call the runtime to store one.

// stringAtomicArch reports whether the target may load and store a
// string with one 16-byte access.
func stringAtomicArch(f sys.ArchFamily) bool {
	return f == sys.AMD64 || f == sys.ARM64
}

// stringAtomicKnown reports whether the target always has the 16-byte
// accesses atomic: amd64 v3 and later have AVX.
func (s *state) stringAtomicKnown() bool {
	return s.gd.Ctxt.Arch.Family == sys.AMD64 && buildcfg.GOAMD64 >= 3
}

// stringAtomicBranch ends the current block on runtime.stringAtomic and
// returns its (true, false) successors, true likely.
func (s *state) stringAtomicBranch() (atomic, locked *ssa.Block) {
	boolT := types.Types[types.TBOOL]
	addr := s.entryNewValue1A(ssa.OpAddr, types.NewPtr(boolT), ir.Syms(s.gd).StringAtomic, s.sb)
	flag := s.rawLoad(boolT, addr)
	atomic = s.f.NewBlock(ssa.BlockPlain)
	locked = s.f.NewBlock(ssa.BlockPlain)
	b := s.endBlock()
	b.Kind = ssa.BlockIf
	b.SetControl(flag)
	b.Likely = ssa.BranchLikely
	b.AddEdgeTo(atomic)
	b.AddEdgeTo(locked)
	return
}

// stringLoad16 loads the string at src with one 16-byte access.
func (s *state) stringLoad16(t *types.Type, src *ssa.Value) *ssa.Value {
	ptrT := s.f.Config.Types.BytePtr
	intT := types.Types[types.TINT]
	tup := s.newValue2(ssa.OpStringLoad16, types.NewTuple(ptrT, intT), src, s.mem())
	w0 := s.newValue1(ssa.OpSelect0, ptrT, tup)
	w1 := s.newValue1(ssa.OpSelect1, intT, tup)
	return s.newValue2(ssa.OpStringMake, t, w0, w1)
}

// shareLoadString is shareLoad for a string on amd64 and arm64.
func (s *state) shareLoadString(t *types.Type, src *ssa.Value) *ssa.Value {
	if s.stringAtomicKnown() {
		return s.stringLoad16(t, src)
	}
	strVar := ssaMarker("string")
	atomic, locked := s.stringAtomicBranch()
	bDone := s.f.NewBlock(ssa.BlockPlain)

	s.startBlock(atomic)
	s.vars[strVar] = s.stringLoad16(t, src)
	s.endBlock().AddEdgeTo(bDone)

	s.startBlock(locked)
	s.vars[strVar] = s.rtcall(ir.Syms(s.gd).StringLoad, true, []*types.Type{t}, s.nilCheck(src))[0]
	s.endBlock().AddEdgeTo(bDone)

	s.startBlock(bDone)
	v := s.variable(strVar, t)
	delete(s.vars, strVar)
	return v
}

// shareStoreString stores the string right at left, which is not in the
// stack, whole: with one 16-byte access (and the write barrier of its
// first word, which the write barrier pass adds), or by the runtime.
func (s *state) shareStoreString(left, right *ssa.Value) {
	if !stringAtomicArch(s.gd.Ctxt.Arch.Family) {
		s.rtcall(ir.Syms(s.gd).StringStore, true, nil, s.nilCheck(left), right)
		return
	}
	store := func() {
		w0 := s.newValue1(ssa.OpStringPtr, s.f.Config.Types.BytePtr, right)
		w1 := s.newValue1(ssa.OpStringWord1, types.Types[types.TINT], right)
		v := s.newValue4(ssa.OpStringStore16, types.TypeMem, left, w0, w1, s.mem())
		v.Aux = types.Types[types.TSTRING]
		s.vars[memVar] = v
	}
	if s.stringAtomicKnown() {
		store()
		return
	}
	atomic, locked := s.stringAtomicBranch()
	bDone := s.f.NewBlock(ssa.BlockPlain)

	s.startBlock(atomic)
	store()
	s.endBlock().AddEdgeTo(bDone)

	s.startBlock(locked)
	s.rtcall(ir.Syms(s.gd).StringStore, true, nil, s.nilCheck(left), right)
	s.endBlock().AddEdgeTo(bDone)

	s.startBlock(bDone)
}

// slicePack returns the words w0 and w1 of the slice v, packed
// (types.SlicePacked, ssa/slicecodec.go).
func (s *state) slicePack(v *ssa.Value) (w0, w1 *ssa.Value) {
	pt := v.Type.Elem().PtrTo()
	p := s.newValue1(ssa.OpSlicePtrUnchecked, pt, v)
	l := s.newValue1(ssa.OpSliceLen, types.Types[types.TINT], v)
	c := s.newValue1(ssa.OpSliceCap, types.Types[types.TINT], v)
	pk := s.newValue4(ssa.OpSlicePack, types.NewTuple(pt, types.TypeMem), p, l, c, s.mem())
	s.vars[memVar] = s.newValue1(ssa.OpSelect1, types.TypeMem, pk)
	w0 = s.newValue1(ssa.OpSelect0, pt, pk)
	w1 = s.newValue3(ssa.OpSlicePackW1, types.Types[types.TUINT64], l, c, w0)
	return
}

// sliceUnpack returns the slice of type t whose words are w0 and w1.
func (s *state) sliceUnpack(t *types.Type, w0, w1 *ssa.Value) *ssa.Value {
	mem := s.mem()
	p := s.newValue3(ssa.OpSliceUnpackPtr, t.Elem().PtrTo(), w0, w1, mem)
	l := s.newValue3(ssa.OpSliceUnpackLen, types.Types[types.TINT], w0, w1, mem)
	c := s.newValue3(ssa.OpSliceUnpackCap, types.Types[types.TINT], w0, w1, mem)
	return s.newValue3(ssa.OpSliceMake, t, p, l, c)
}

// Slices, without the sharing analysis (doc/gd/slice16.md): with 8-byte
// words a slice is two words aligned to 16 (types.SliceAligned16), loaded
// and stored as a string is, with one 16-byte access where it is atomic,
// and under the sequence lock of its block elsewhere. It is packed before
// it is stored: the header of a huge one is made before any lock is taken.

// sliceLoad16 loads the words of the slice of type t at src with one
// 16-byte access.
func (s *state) sliceLoad16(t *types.Type, src *ssa.Value) (w0, w1 *ssa.Value) {
	ptrT := t.Elem().PtrTo()
	u64 := types.Types[types.TUINT64]
	tup := s.newValue2(ssa.OpStringLoad16, types.NewTuple(ptrT, u64), src, s.mem())
	return s.newValue1(ssa.OpSelect0, ptrT, tup), s.newValue1(ssa.OpSelect1, u64, tup)
}

// shareLoadSlice is shareLoad for a slice on amd64 and arm64.
func (s *state) shareLoadSlice(t *types.Type, src *ssa.Value) *ssa.Value {
	if s.stringAtomicKnown() {
		w0, w1 := s.sliceLoad16(t, src)
		return s.sliceUnpack(t, w0, w1)
	}
	ptrT := t.Elem().PtrTo()
	u64 := types.Types[types.TUINT64]
	w0Var, w1Var := ssaMarker("slicew0"), ssaMarker("slicew1")
	atomic, locked := s.stringAtomicBranch()
	bDone := s.f.NewBlock(ssa.BlockPlain)

	s.startBlock(atomic)
	s.vars[w0Var], s.vars[w1Var] = s.sliceLoad16(t, src)
	s.endBlock().AddEdgeTo(bDone)

	s.startBlock(locked)
	s.vars[w0Var], s.vars[w1Var] = s.shareLoadSliceWords(t, s.nilCheck(src))
	s.endBlock().AddEdgeTo(bDone)

	// (One unpack of the words of both.)
	s.startBlock(bDone)
	v := s.sliceUnpack(t, s.variable(w0Var, ptrT), s.variable(w1Var, u64))
	delete(s.vars, w0Var)
	delete(s.vars, w1Var)
	return v
}

// shareStoreSlice stores the slice right of type t at left, which is not
// in the stack, whole: packed, then with one 16-byte access (and the
// write barrier of its first word, which the write barrier pass adds), or
// by the runtime.
func (s *state) shareStoreSlice(t *types.Type, left, right *ssa.Value) {
	w0, w1 := s.slicePack(right)
	if !stringAtomicArch(s.gd.Ctxt.Arch.Family) {
		s.rtcall(ir.Syms(s.gd).SliceStore, true, nil, s.nilCheck(left), w0, w1)
		return
	}
	store := func() {
		v := s.newValue4(ssa.OpStringStore16, types.TypeMem, left, w0, w1, s.mem())
		v.Aux = t
		s.vars[memVar] = v
	}
	if s.stringAtomicKnown() {
		store()
		return
	}
	atomic, locked := s.stringAtomicBranch()
	bDone := s.f.NewBlock(ssa.BlockPlain)

	s.startBlock(atomic)
	store()
	s.endBlock().AddEdgeTo(bDone)

	s.startBlock(locked)
	s.rtcall(ir.Syms(s.gd).SliceStore, true, nil, s.nilCheck(left), w0, w1)
	s.endBlock().AddEdgeTo(bDone)

	s.startBlock(bDone)
}

// sliceLenCapMem returns the length, or the capacity with isCap, of the
// packed slice x in memory (types.SlicePacked): its second word alone,
// one 8-byte load, whose fields go together, unless it is the marker of a
// huge slice; then the slice is loaded whole.
func (s *state) sliceLenCapMem(isCap bool, x ir.Node) *ssa.Value {
	t := x.Type()
	addr := s.addr(x)
	s.instrumentFields(t, addr, instrumentRead)
	intT := types.Types[types.TINT]
	u64 := types.Types[types.TUINT64]
	w1 := s.rawLoad(u64, s.newValue1I(ssa.OpOffPtr, types.NewPtr(u64), 8, addr))
	lo := s.newValue1(ssa.OpZeroExt32to64, intT, s.newValue1(ssa.OpTrunc64to32, types.Types[types.TUINT32], w1))
	hi := s.newValue2(ssa.OpRsh64Ux64, intT, w1, s.constInt64(u64, 32))
	lenF, capF := lo, hi
	if s.f.Config.BigEndian {
		lenF, capF = hi, lo
	}
	huge := s.newValue2(ssa.OpEq32, types.Types[types.TBOOL], s.newValue1(ssa.OpTrunc64to32, types.Types[types.TUINT32], capF), s.constInt32(types.Types[types.TUINT32], -1))
	resVar := ssaMarker("lencap")
	if isCap {
		s.vars[resVar] = capF
	} else {
		s.vars[resVar] = lenF
	}
	bHuge := s.f.NewBlock(ssa.BlockPlain)
	bDone := s.f.NewBlock(ssa.BlockPlain)
	b := s.endBlock()
	b.Kind = ssa.BlockIf
	b.SetControl(huge)
	b.Likely = ssa.BranchUnlikely
	b.AddEdgeTo(bHuge)
	b.AddEdgeTo(bDone)

	s.startBlock(bHuge)
	v := s.load(t, addr)
	if isCap {
		s.vars[resVar] = s.newValue1(ssa.OpSliceCap, intT, v)
	} else {
		s.vars[resVar] = s.newValue1(ssa.OpSliceLen, intT, v)
	}
	s.endBlock().AddEdgeTo(bDone)

	s.startBlock(bDone)
	r := s.variable(resVar, intT)
	delete(s.vars, resVar)
	return r
}
