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
// stack checks the sequence lock of its 64-byte block before and after, inline; a
// store there calls the runtime, which takes the locks if the memory is
// shared.

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
		// One lock was checked: the value must be in one block.
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
