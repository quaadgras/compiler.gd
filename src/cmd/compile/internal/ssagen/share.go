// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssagen

import (
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
// stack checks the sequence lock of its page before and after, inline; a
// store there calls the runtime, which takes the locks if the memory is
// shared.

const (
	sharePageShift = 12
	shareStripes   = 1 << 10 // must match runtime.shareStripes
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
	return fn.Pragma&(ir.Norace|ir.Nosplit) == 0
}

// algFunc reports whether fn is an equality or hash function the compiler
// wrote. Every package that needs one compiles it, the runtime's among
// them, and all must be the same: they load whole in the runtime too.
func algFunc(fn *ir.Func) bool {
	name := fn.Sym().Name
	return strings.HasPrefix(name, ".eq.") || strings.HasPrefix(name, ".hash.")
}

// tearGuard reports whether a value of type t at addr is loaded and
// stored under the sequence locks.
func (s *state) tearGuard(t *types.Type, addr *ssa.Value) bool {
	return s.tear && !s.dstLocal && !ssa.IsStackAddr(addr) && types.HasTearable(t) && !s.unpublished(addr)
}

// localExpr reports whether n designates memory that escape analysis
// found no other goroutine can reach while the function runs: a variable
// whose memory is not shared, or what a variable that only points to such
// memory points to.
func (s *state) localExpr(n ir.Node) bool {
	if s.gd.Debug.NoShare != 0 || s.gd.Debug.NoLocal != 0 {
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
	base := addr
	for base.Op == ssa.OpOffPtr || base.Op == ssa.OpAddPtr || base.Op == ssa.OpPtrIndex || base.Op == ssa.OpCopy || base.Op == ssa.OpNilCheck {
		base = base.Args[0]
	}
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
				for p.Op == ssa.OpOffPtr || p.Op == ssa.OpAddPtr || p.Op == ssa.OpPtrIndex || p.Op == ssa.OpCopy || p.Op == ssa.OpNilCheck {
					p = p.Args[0]
				}
				if p != base {
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

// shareSeqAddr returns the address of the sequence lock of addr's page.
func (s *state) shareSeqAddr(addr *ssa.Value) *ssa.Value {
	uptr := types.Types[types.TUINTPTR]
	u32p := types.NewPtr(types.Types[types.TUINT32])
	p := s.newValue2(ssa.OpConvert, uptr, addr, s.mem())
	// The offset of the lock: its index times 4.
	var off *ssa.Value
	if s.config.PtrSize == 8 {
		off = s.newValue2(ssa.OpRsh64Ux64, uptr, p, s.constInt64(types.Types[types.TUINT64], sharePageShift-2))
		off = s.newValue2(ssa.OpAnd64, uptr, off, s.constInt64(uptr, (shareStripes-1)<<2))
	} else {
		off = s.newValue2(ssa.OpRsh32Ux64, uptr, p, s.constInt64(types.Types[types.TUINT64], sharePageShift-2))
		off = s.newValue2(ssa.OpAnd32, uptr, off, s.constInt32(uptr, (shareStripes-1)<<2))
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
	if t.Size() > 1 {
		// One lock was checked: the value must be in one page.
		p := s.newValue2(ssa.OpConvert, uptr, src, s.mem())
		var onePage *ssa.Value
		if s.config.PtrSize == 8 {
			last := s.newValue2(ssa.OpAdd64, uptr, p, s.constInt64(uptr, t.Size()-1))
			x := s.newValue2(ssa.OpRsh64Ux64, uptr, s.newValue2(ssa.OpXor64, uptr, p, last), s.constInt64(types.Types[types.TUINT64], sharePageShift))
			onePage = s.newValue2(ssa.OpEq64, types.Types[types.TBOOL], x, s.constInt64(uptr, 0))
		} else {
			last := s.newValue2(ssa.OpAdd32, uptr, p, s.constInt32(uptr, int32(t.Size()-1)))
			x := s.newValue2(ssa.OpRsh32Ux64, uptr, s.newValue2(ssa.OpXor32, uptr, p, last), s.constInt64(types.Types[types.TUINT64], sharePageShift))
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
func (s *state) shareLock(dst *ssa.Value, size int64) *ssa.Value {
	return s.rtcall(ir.Syms(s.gd).ShareStore, true, []*types.Type{types.Types[types.TBOOL]}, dst, s.constInt(types.Types[types.TUINTPTR], size))[0]
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
// store of only some of its words (s = s[:n] stores a length) is a store
// of all of them in shared memory, where another goroutine may have
// stored the others since they were loaded.
func (s *state) shareStoreType(t *types.Type, left, right *ssa.Value, skip skipMask, leftIsStmt bool) {
	locked := s.shareLock(left, t.Size())
	if skip == 0 {
		s.rawStoreType(t, left, right, 0, leftIsStmt)
		s.shareUnlock(locked, left, t.Size())
		return
	}
	bAll := s.f.NewBlock(ssa.BlockPlain)
	bSome := s.f.NewBlock(ssa.BlockPlain)
	bDone := s.f.NewBlock(ssa.BlockPlain)
	b := s.endBlock()
	b.Kind = ssa.BlockIf
	b.SetControl(locked)
	b.Likely = ssa.BranchUnlikely
	b.AddEdgeTo(bAll)
	b.AddEdgeTo(bSome)

	s.startBlock(bAll)
	s.rawStoreType(t, left, right, 0, leftIsStmt)
	s.rtcall(ir.Syms(s.gd).ShareUnlock, true, nil, left, s.constInt(types.Types[types.TUINTPTR], t.Size()))
	s.endBlock().AddEdgeTo(bDone)

	s.startBlock(bSome)
	s.rawStoreType(t, left, right, skip, leftIsStmt)
	s.endBlock().AddEdgeTo(bDone)

	s.startBlock(bDone)
}

// shareZero is zero for a value tearGuard says to guard.
func (s *state) shareZero(t *types.Type, dst *ssa.Value) {
	locked := s.shareLock(dst, t.Size())
	s.rawZero(t, dst)
	s.shareUnlock(locked, dst, t.Size())
}

// shareMove is the move of a value of type t, which holds values of more
// than one word, when dst or src is not in the stack.
func (s *state) shareMove(t *types.Type, dst, src *ssa.Value) {
	// (Memory no other goroutine can reach is as good as the stack.)
	srcStack, dstStack := ssa.IsStackAddr(src), ssa.IsStackAddr(dst) || s.dstLocal
	switch {
	case dstStack:
		s.shareLoadMem(t, dst, src)
	case srcStack:
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
		// The runtime reads the one whole and writes the other whole.
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
