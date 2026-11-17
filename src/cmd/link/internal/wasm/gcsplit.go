// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wasm

import (
	"bytes"
	"cmd/internal/obj/wasm"
	"cmd/internal/objabi"
	"cmd/link/internal/ld"
	"cmd/link/internal/loader"
	"internal/buildcfg"
	"os"
	"sort"
)

// Linking for GOWASM=gc,split (stage 15 of doc/gd/wasm-gc.md; see
// cmd/compile/internal/wasm/gcsplit.go).
//
// All data is in linear memory, where the linker lays it out. The
// pointers in it are also in one array of references (global
// wasm.GCGlobalSlots), which covers linear memory from address 0 to
// the end of the data, a word per element: the start function sets the
// element of each word that a relocation makes a pointer to data. A
// pointer to the start of a data symbol that the code uses is an
// immutable global of its own (R_WASMGLOBAL), made where it is
// declared.
//
// The functions the code calls to read and write pointers are
// generated here (splitHelpers).

func splitMode() bool { return buildcfg.GOWASM.Split }

// The size of a pointer word (4 bytes with wasm32, 8 with
// GOWASM=gc,ptr64) is l.ptrSize(); everything below that depends on it
// goes through these. (Addresses are i32, wasm.GCTypeAddr, whatever the
// word: linear memory is of 32 bits.)

func (l *gcLinker) ptrSize() int64 { return int64(l.ctxt.Arch.PtrSize) }

// toSlot divides the i32 on the stack, a byte offset in a block, by
// the size of a word: the index of the element of the block's array
// for the word there.
func (a *gcAsm) toSlot() {
	a.i32(wasm.GCWordShift(int(a.l.ptrSize())))
	a.op(0x76) // i32.shr_u
}

// toSlotUp is toSlot of the i32 on the stack rounded up to a word.
func (a *gcAsm) toSlotUp() {
	a.i32(a.l.ptrSize() - 1)
	a.op(0x6A) // i32.add
	a.toSlot()
}

// wordMisaligned pushes the i32 on the stack and'ed with the size of a
// word less 1: not zero if it is not a multiple of a word.
func (a *gcAsm) wordMisaligned() {
	a.i32(a.l.ptrSize() - 1)
	a.op(0x71) // i32.and
}

// loadWord loads the pointer word at the address on the stack (plus
// off) as an i64: i64.load, or i64.load32_u for a word of 4 bytes.
func (a *gcAsm) loadWord(off uint64) {
	if a.l.ptrSize() == 4 {
		a.op(0x35, 0x02) // i64.load32_u
	} else {
		a.op(0x29, 0x03) // i64.load
	}
	writeUleb128(a, off)
}

// storeWord stores the i64 on the stack as the pointer word at the
// address under it (plus off).
func (a *gcAsm) storeWord(off uint64) {
	if a.l.ptrSize() == 4 {
		a.op(0x3E, 0x02) // i64.store32
	} else {
		a.op(0x37, 0x03) // i64.store
	}
	writeUleb128(a, off)
}

// The functions splitHelpers generates, in the order of their indexes.
var gcSplitHelpers = []string{
	wasm.GCSplitLoadPtr,
	wasm.GCSplitStorePtr,
	wasm.GCSplitAddPtr,
	wasm.GCSplitPtrAddr,
	wasm.GCSplitIntToPtr,
	wasm.GCSplitMove,
	wasm.GCSplitZero,
	wasm.GCSplitNew,
	gcSplitSlotsOf,
	gcSplitMkPtr,
	gcSplitSetPtr,
}

// gcSplitSlotsOf is the function, exported to the host as "slotsof",
// that returns the array of a pointer: what the host keeps a block of
// linear memory by (gd.register, weak pointers, cleanups).
const gcSplitSlotsOf = "wasm.slotsof"

// gcSplitMkPtr is the function, exported to the host as "mkptr", that
// makes a pointer of an address, an array and an offset: a weak
// pointer made strong (gd.weakgetat).
const gcSplitMkPtr = "wasm.mkptr"

// gcSplitSetPtr is the function, exported to the host as "setptr",
// that sets the address and the offset of a pointer. Nothing calls it.
// It is there so that the two fields are written somewhere that an
// engine cannot see the callers of: an address is then only known by
// reading the pointer, again after every call, and a pointer that is
// read is one that the engine keeps, with its array, which is what
// keeps the block from being freed (see writeSplitTypes).
const gcSplitSetPtr = "wasm.setptr"

func (l *gcLinker) splitHelperIndex(name string) (int, bool) {
	for i, h := range gcSplitHelpers {
		if h == name {
			return i, true
		}
	}
	switch name {
	case wasm.GCReflectCall:
		return len(gcSplitHelpers), true
	case wasm.GCMakeFunc:
		return len(gcSplitHelpers) + 1, true
	}
	return 0, false
}

// splitValType writes the value type that the letter k of a signature
// stands for, with split: 'r' is a nullable GCPtr, 'a' a nullable
// GCSlots.
func (l *gcLinker) splitValType(w *bytes.Buffer, k byte) {
	switch k {
	case 'r':
		w.WriteByte(wasm.GCRefNull)
		writeSleb128(w, int64(l.typeIndex(wasm.GCPtr)))
	case 'a':
		w.WriteByte(wasm.GCRefNull)
		writeSleb128(w, int64(l.typeIndex(wasm.GCSlots)))
	default:
		w.WriteByte(gcStorage(k))
	}
}

// writeSplitTypes writes the recursion group of GCPtr and GCSlots,
// types 0 and 1.
func (l *gcLinker) writeSplitTypes(w *bytes.Buffer) {
	ptr, slots := l.typeIndex(wasm.GCPtr), l.typeIndex(wasm.GCSlots)
	if ptr != 0 || slots != 1 {
		panic("wasm: split types not first")
	}
	w.Write([]byte{0x4E, 0x02}) // rec, two types
	if _, ok := l.typeIdx[wasm.GCExtern]; ok {
		// Open to GCExtern, the holder of a JavaScript value.
		w.Write([]byte{0x50, 0x00})
	}
	// GCPtr: the address and the offset are mutable, the array not.
	// (Nothing changes them. But an engine may keep the value of an
	// immutable field it has read, or that it knows from where the
	// object was made, and let the object go: the code would then use
	// the address while nothing keeps the array alive, and the block
	// would be freed in use. A mutable field of an object others may
	// have is read again after a call, and the host frees a block
	// only from a call: the pointer, and its array, stay live as long
	// as the address is used.)
	// (addr and off are addresses: wasm.GCTypeAddr.)
	w.Write([]byte{0x5F, 0x03, wasm.GCTypeAddr, 0x01})
	w.WriteByte(wasm.GCRefNull)
	writeSleb128(w, int64(slots))
	w.Write([]byte{0x00, wasm.GCTypeAddr, 0x01})
	// GCSlots: an array of mutable nullable GCPtrs.
	w.WriteByte(0x5E)
	w.WriteByte(wasm.GCRefNull)
	writeSleb128(w, int64(ptr))
	w.WriteByte(0x01)
}

// splitDataSlots returns the words of the data that relocations make
// pointers to data: the elements of the array of data that the start
// function sets.
func (l *gcLinker) splitDataSlots(sects []wasmDataSect, end uint64) []uint32 {
	ldr := l.ldr
	var words []uint32
	for s := loader.Sym(1); s < loader.Sym(ldr.NSym()); s++ {
		if !ldr.AttrReachable(s) || ldr.SymType(s).IsText() {
			continue
		}
		sect := ldr.SymSect(s)
		if sect == nil {
			continue
		}
		in := false
		for _, ds := range sects {
			in = in || ds.sect == sect
		}
		if !in {
			continue
		}
		addr := ldr.SymValue(s)
		relocs := ldr.Relocs(s)
		for i := 0; i < relocs.Count(); i++ {
			r := relocs.At(i)
			if int64(r.Siz()) != l.ptrSize() || r.Sym() == 0 || (r.Type() != objabi.R_ADDR) {
				continue
			}
			if ldr.SymType(r.Sym()).IsText() {
				continue // a function's index, not a pointer
			}
			at := addr + int64(r.Off())
			if at%l.ptrSize() != 0 || uint64(at) >= end {
				continue
			}
			words = append(words, uint32(at/l.ptrSize()))
		}
	}
	sort.Slice(words, func(i, j int) bool { return words[i] < words[j] })
	return words
}

// splitInit returns the body of the start function: it sets the
// elements of the array of data for the words listed at addr in linear
// memory (n of them).
func (l *gcLinker) splitInit(addr, n int64) []byte {
	ptr, slots := l.typeIndex(wasm.GCPtr), l.typeIndex(wasm.GCSlots)
	a := &gcAsm{l: l}
	// local 0: where in the list; 1: the word; 2: its address
	a.op(0x01, 0x03, wasm.GCTypeI32)
	a.i32(addr)
	a.set(0)
	a.block()
	a.loop()
	a.get(0)
	a.i32(addr + 4*n)
	a.op(0x4F) // i32.ge_u
	a.brIf(1)
	a.get(0)
	a.load(0)
	a.tee(1)
	a.i32(wasm.GCWordShift(int(l.ptrSize())))
	a.op(0x74) // i32.shl
	a.load(0)
	a.tee(2)
	a.if_()
	gcGlobalGet(&a.Buffer, wasm.GCGlobalSlots)
	a.get(1)
	a.get(2)
	gcGlobalGet(&a.Buffer, wasm.GCGlobalSlots)
	a.get(2)
	a.gc(0x00, ptr) // struct.new
	a.gc(0x0E, slots)
	a.end()
	a.get(0)
	a.i32(4)
	a.op(0x6A) // i32.add
	a.set(0)
	a.br(0)
	a.end()
	a.end()
	a.end()
	return a.Bytes()
}

// splitLocals writes the declaration of the locals of a function:
// letters as in a signature.
func (a *gcAsm) splitLocals(kinds string) {
	writeUleb128(a, uint64(len(kinds)))
	for i := range kinds {
		writeUleb128(a, 1)
		a.l.splitValType(&a.Buffer, kinds[i])
	}
}

// splitHelpers returns the functions that split code calls (see
// gcSplitHelpers). staticEnd is the end of the data, which the array
// of data covers.
func (l *gcLinker) splitHelpers(first int, staticEnd int64) []*wasmFunc {
	ptr, slots := l.typeIndex(wasm.GCPtr), l.typeIndex(wasm.GCSlots)
	sigs := map[string]string{
		wasm.GCSplitLoadPtr:  "ri.r",
		wasm.GCSplitStorePtr: "rir.",
		wasm.GCSplitAddPtr:   "ri.r",
		wasm.GCSplitPtrAddr:  "r.i",
		wasm.GCSplitIntToPtr: "l.r",
		wasm.GCSplitMove:     "ririi.",
		wasm.GCSplitZero:     "rii.",
		wasm.GCSplitNew:      "ii.r",
		gcSplitSlotsOf:       "r.a",
		gcSplitMkPtr:         "iai.r",
		gcSplitSetPtr:        "rii.",
	}
	index := func(name string) int {
		i, _ := l.splitHelperIndex(name)
		return first + i
	}
	get := func(a *gcAsm, f uint32) { a.gc(0x02, ptr, f) } // struct.get
	refNull := func(a *gcAsm) { a.op(0xD0, 0x71) }
	isNull := func(a *gcAsm) { a.op(0xD1) }
	ret := func(a *gcAsm) { a.op(0x0F) }
	// min pushes the smaller of local x and the length of the array in
	// local arr.
	min := func(a *gcAsm, x, arr uint32) {
		a.get(x)
		a.get(arr)
		a.gc(0x0F) // array.len
		a.get(x)
		a.get(arr)
		a.gc(0x0F)
		a.op(0x49) // i32.lt_u
		a.op(0x1B) // select
	}
	var fns []*wasmFunc
	for _, name := range gcSplitHelpers {
		a := &gcAsm{l: l}
		switch name {
		case wasm.GCSplitLoadPtr:
			// The element of the array, if there is one and its
			// address is what memory holds; or else the address in
			// memory, made a pointer (it was written as an integer).
			// p 0, rel 1; arr 2, i 3, r 4, addr 5
			a.splitLocals("airi")
			a.get(0)
			get(a, wasm.GCPtrAddr)
			a.get(1)
			a.op(0x6A)
			a.set(5)
			a.block()
			a.get(0)
			get(a, wasm.GCPtrSlots)
			a.tee(2)
			isNull(a)
			a.brIf(0)
			a.get(0)
			get(a, wasm.GCPtrOff)
			a.get(1)
			a.op(0x6A)
			a.toSlot()
			a.tee(3)
			a.get(2)
			a.gc(0x0F) // array.len
			a.op(0x4F) // i32.ge_u
			a.brIf(0)
			a.get(2)
			a.get(3)
			a.gc(0x0B, slots) // array.get
			a.tee(4)
			isNull(a)
			a.brIf(0)
			a.get(4)
			get(a, wasm.GCPtrAddr)
			a.get(5)
			a.load(0)
			a.op(0x47) // i32.ne
			a.brIf(0)
			a.get(4)
			ret(a)
			a.end()
			a.get(5)
			a.loadWord(0)
			a.call(index(wasm.GCSplitIntToPtr))
			a.end()
		case wasm.GCSplitStorePtr:
			// p 0, rel 1, v 2; arr 3, i 4
			a.splitLocals("ai")
			a.get(0)
			get(a, wasm.GCPtrAddr)
			a.get(1)
			a.op(0x6A)
			a.get(2)
			isNull(a)
			a.op(0x04, wasm.GCTypeI64) // if (result i64)
			a.op(0x42, 0x00)           // i64.const 0
			a.else_()
			a.get(2)
			get(a, wasm.GCPtrAddr)
			a.op(0xAD) // i64.extend_i32_u
			a.end()
			a.storeWord(0)
			// (For debugging: a pointer to a block stored where
			// there is no array keeps nothing alive.)
			check := func() {
				if os.Getenv("GOWASMGC_SPLITCHECK") == "" {
					return
				}
				a.get(2)
				isNull(a)
				a.op(0x45) // i32.eqz
				a.get(2)
				get(a, wasm.GCPtrSlots)
				isNull(a)
				a.op(0x45)
				a.op(0x71) // i32.and
				a.if_()
				a.unreachable()
				a.end()
			}
			a.get(0)
			get(a, wasm.GCPtrSlots)
			a.tee(3)
			isNull(a)
			a.if_()
			check()
			ret(a)
			a.end()
			a.get(0)
			get(a, wasm.GCPtrOff)
			a.get(1)
			a.op(0x6A)
			a.toSlot()
			a.tee(4)
			a.get(3)
			a.gc(0x0F)
			a.op(0x4F)
			a.if_()
			check()
			ret(a)
			a.end()
			a.get(3)
			a.get(4)
			a.get(2)
			a.gc(0x0E, slots) // array.set
			a.end()
		case wasm.GCSplitAddPtr:
			// p 0, rel 1; q 2
			a.splitLocals("r")
			a.get(0)
			isNull(a)
			a.if_()
			a.get(1)
			a.op(0xAD)
			a.call(index(wasm.GCSplitIntToPtr))
			ret(a)
			a.end()
			a.get(0)
			get(a, wasm.GCPtrAddr)
			a.get(1)
			a.op(0x6A)
			a.get(0)
			get(a, wasm.GCPtrSlots)
			a.get(0)
			get(a, wasm.GCPtrOff)
			a.get(1)
			a.op(0x6A)
			a.gc(0x00, ptr)
			// (Kept where others could see it, so that an engine that
			// inlines this does not take the new pointer apart: see
			// writeSplitTypes.)
			a.op(0x22) // local.tee
			writeUleb128(a, 2)
			gcGlobalSet(&a.Buffer, wasm.GCGlobalKeep)
			a.get(2)
			a.end()
		case wasm.GCSplitPtrAddr:
			a.splitLocals("")
			a.get(0)
			isNull(a)
			a.op(0x04, wasm.GCTypeI32) // if (result i32)
			a.i32(0)
			a.else_()
			a.get(0)
			get(a, wasm.GCPtrAddr)
			a.end()
			a.end()
		case wasm.GCSplitIntToPtr:
			// x 0; addr 1
			a.splitLocals("i")
			a.get(0)
			a.op(0x50) // i64.eqz
			a.if_()
			refNull(a)
			ret(a)
			a.end()
			a.get(0)
			a.op(0xA7) // i32.wrap_i64
			a.tee(1)
			a.get(1)
			a.i32(staticEnd)
			a.op(0x49) // i32.lt_u
			a.op(0x04) // if (result (ref null slots))
			l.splitValType(&a.Buffer, 'a')
			gcGlobalGet(&a.Buffer, wasm.GCGlobalSlots)
			a.else_()
			if os.Getenv("GOWASMGC_SPLITCHECK") != "" {
				// (For debugging: a pointer made of the address of
				// a block keeps nothing alive. Not one beyond memory:
				// a function's.)
				a.get(1)
				a.op(0x3F, 0x00) // memory.size
				a.i32(16)
				a.op(0x74) // i32.shl
				a.op(0x49) // i32.lt_u
				a.if_()
				a.unreachable()
				a.end()
			}
			refNull(a)
			a.end()
			a.get(1)
			a.gc(0x00, ptr)
			a.end()
		case wasm.GCSplitMove:
			// d 0, drel 1, s 2, srel 3, n 4; da 5, sa 6, di 7, si 8, cnt 9, first 10, k 11
			a.splitLocals("aaiiiii")
			a.get(0)
			get(a, wasm.GCPtrAddr)
			a.get(1)
			a.op(0x6A)
			a.get(2)
			get(a, wasm.GCPtrAddr)
			a.get(3)
			a.op(0x6A)
			a.get(4)
			a.op(0xFC, 0x0A, 0x00, 0x00) // memory.copy
			a.get(0)
			get(a, wasm.GCPtrSlots)
			a.tee(5)
			isNull(a)
			a.if_()
			ret(a)
			a.end()
			a.get(0)
			get(a, wasm.GCPtrOff)
			a.get(1)
			a.op(0x6A)
			a.tee(7)
			a.toSlotUp()
			a.set(10)
			a.get(7)
			a.get(4)
			a.op(0x6A)
			a.toSlot()
			a.set(9)
			min(a, 9, 5)
			a.get(10)
			a.op(0x6B) // i32.sub
			a.tee(9)
			a.i32(0)
			a.op(0x4C) // i32.le_s
			a.if_()
			ret(a)
			a.end()
			a.block()
			a.get(2)
			get(a, wasm.GCPtrSlots)
			a.tee(6)
			isNull(a)
			a.brIf(0)
			a.get(2)
			get(a, wasm.GCPtrOff)
			a.get(3)
			a.op(0x6A)
			a.tee(8)
			a.get(7)
			a.op(0x73) // i32.xor
			a.wordMisaligned()
			a.brIf(0)
			// The source's elements from its first word: as many as
			// there are (k), and nil for the rest.
			a.get(6)
			a.gc(0x0F)
			a.get(8)
			a.toSlotUp()
			a.tee(8)
			a.op(0x6B) // i32.sub
			a.tee(11)
			a.i32(0)
			a.op(0x4C) // i32.le_s
			a.brIf(0)
			a.get(9)
			a.get(11)
			a.get(9)
			a.get(11)
			a.op(0x49) // i32.lt_u
			a.op(0x1B) // select
			a.set(11)
			a.get(5)
			a.get(10)
			a.get(6)
			a.get(8)
			a.get(11)
			a.gc(0x11, slots, slots) // array.copy
			a.get(5)
			a.get(10)
			a.get(11)
			a.op(0x6A)
			refNull(a)
			a.get(9)
			a.get(11)
			a.op(0x6B)
			a.gc(0x10, slots) // array.fill
			ret(a)
			a.end()
			// No pointers to copy: the destination has none there.
			a.get(5)
			a.get(10)
			refNull(a)
			a.get(9)
			a.gc(0x10, slots) // array.fill
			a.end()
		case wasm.GCSplitZero:
			// p 0, rel 1, n 2; arr 3, first 4, cnt 5
			a.splitLocals("aii")
			a.get(0)
			get(a, wasm.GCPtrAddr)
			a.get(1)
			a.op(0x6A)
			a.i32(0)
			a.get(2)
			a.op(0xFC, 0x0B, 0x00) // memory.fill
			a.get(0)
			get(a, wasm.GCPtrSlots)
			a.tee(3)
			isNull(a)
			a.if_()
			ret(a)
			a.end()
			a.get(0)
			get(a, wasm.GCPtrOff)
			a.get(1)
			a.op(0x6A)
			a.tee(4)
			a.get(2)
			a.op(0x6A)
			a.toSlot()
			a.set(5)
			a.get(4)
			a.toSlotUp()
			a.set(4)
			min(a, 5, 3)
			a.get(4)
			a.op(0x6B)
			a.tee(5)
			a.i32(0)
			a.op(0x4C)
			a.if_()
			ret(a)
			a.end()
			a.get(3)
			a.get(4)
			refNull(a)
			a.get(5)
			a.gc(0x10, slots)
			a.end()
		case wasm.GCSplitNew:
			a.splitLocals("")
			a.get(0)
			a.get(1)
			a.gc(0x07, slots) // array.new_default
			a.i32(0)
			a.gc(0x00, ptr)
			a.end()
		case gcSplitSlotsOf:
			a.splitLocals("")
			a.get(0)
			get(a, wasm.GCPtrSlots)
			a.end()
		case gcSplitMkPtr:
			a.splitLocals("")
			a.get(0)
			a.get(1)
			a.get(2)
			a.gc(0x00, ptr)
			a.end()
		case gcSplitSetPtr:
			a.splitLocals("")
			a.get(0)
			a.get(1)
			a.gc(0x05, ptr, wasm.GCPtrAddr) // struct.set
			a.get(0)
			a.get(2)
			a.gc(0x05, ptr, wasm.GCPtrOff)
			a.end()
		}
		fns = append(fns, &wasmFunc{Name: name, Type: l.typeIndex(wasm.GCFuncPrefix + sigs[name]), Code: a.Bytes()})
	}
	return fns
}

// splitGlobals writes the globals of split: those of wasm.GCGlobals,
// the array of data and the pointer to it, and a pointer to each data
// symbol that the code uses as one.
func (l *gcLinker) splitGlobals(w *bytes.Buffer, stackTop, dataEnd uint64) {
	ptr, slots := l.typeIndex(wasm.GCPtr), l.typeIndex(wasm.GCSlots)
	writeUleb128(w, uint64(wasm.GCSplitGlobals+len(l.globals)))
	for i := 0; i < wasm.GCSplitGlobals; i++ {
		switch i {
		case wasm.GCGlobalCtxt, wasm.GCGlobalKeep:
			l.splitValType(w, 'r')
			w.WriteByte(0x01)
			w.Write([]byte{0xD0, 0x71})
		case wasm.GCGlobalSlots:
			w.WriteByte(wasm.GCRefNonNull)
			writeSleb128(w, int64(slots))
			w.WriteByte(0x00) // immutable
			writeI32Const(w, int32((int64(dataEnd)+l.ptrSize()-1)/l.ptrSize()))
			gcOp(w, 0x07, slots) // array.new_default
		case wasm.GCGlobalStatic:
			w.WriteByte(wasm.GCRefNonNull)
			writeSleb128(w, int64(ptr))
			w.WriteByte(0x00)
			writeI32Const(w, 0)
			gcGlobalGet(w, wasm.GCGlobalSlots)
			writeI32Const(w, 0)
			gcOp(w, 0x00, ptr) // struct.new
		default:
			w.Write([]byte{wasm.GCTypeI32, 0x01})
			switch i {
			case wasm.GCGlobalSP:
				writeI32Const(w, int32(stackTop))
			case wasm.GCGlobalStackLo:
				writeI32Const(w, int32(stackTop-wasm.GCStackSize+wasm.GCStackRedZone))
			default:
				writeI32Const(w, 0)
			}
		}
		w.WriteByte(0x0b)
	}
	for _, s := range l.globals {
		addr := l.ldr.SymValue(s)
		w.WriteByte(wasm.GCRefNonNull)
		writeSleb128(w, int64(ptr))
		w.WriteByte(0x00)
		writeI32Const(w, int32(addr))
		if !l.ldr.SymType(s).IsText() && addr >= 0 && uint64(addr) < dataEnd {
			gcGlobalGet(w, wasm.GCGlobalSlots)
		} else {
			w.Write([]byte{0xD0, 0x71})
		}
		writeI32Const(w, int32(addr))
		gcOp(w, 0x00, ptr)
		w.WriteByte(0x0b)
	}
}

// loadPtrAt pushes the pointer at byte off of what the pointer in local
// p points to.
func (a *gcAsm) loadPtrAt(p uint32, off int64) {
	a.get(p)
	a.i32(off)
	if splitMode() {
		i, _ := a.l.splitHelperIndex(wasm.GCSplitLoadPtr)
		a.call(a.l.helperFirst + i)
		return
	}
	loadr, _ := a.l.helperIndex(wasm.GCLoadPrefix + "r")
	a.call(a.l.helperFirst + loadr)
}

// splitLoadReg is loadReg with split: register r of the value that the
// pointer pushed by ptr points to.
func (a *gcAsm) splitLoadReg(r ld.WasmGCReg, ptr func()) {
	l := a.l
	helper := func(name string) {
		i, _ := l.splitHelperIndex(name)
		a.call(l.helperFirst + i)
	}
	if r.Mem == 's' {
		// Word 0 of a string of wasm32 (ld.WasmGCRegs): a pointer if
		// the tag, the top 4 bits of its byte 15, is 0, else nil
		// without reading the word as one.
		a.op(0x02) // block (result (ref null ptr))
		l.splitValType(&a.Buffer, 'r')
		a.op(0xD0, 0x71) // ref.null none
		ptr()
		a.gc(0x02, l.typeIndex(wasm.GCPtr), wasm.GCPtrAddr)
		a.op(0x2D, 0) // i32.load8_u
		writeUleb128(a, uint64(r.Off+15))
		a.i32(4)
		a.op(0x76)    // i32.shr_u
		a.op(0x0D, 0) // br_if 0
		a.op(0x1A)    // drop
		ptr()
		a.i32(r.Off)
		helper(wasm.GCSplitLoadPtr)
		a.end()
		return
	}
	ptr()
	switch {
	case r.Reg == 'r' && r.Mem == 'r':
		a.i32(r.Off)
		helper(wasm.GCSplitLoadPtr)
		return
	case r.Reg == 'r':
		// (The word of an interface that holds its type: an integer
		// in memory.)
		a.gc(0x02, l.typeIndex(wasm.GCPtr), wasm.GCPtrAddr)
		a.loadWord(uint64(r.Off))
		helper(wasm.GCSplitIntToPtr)
		return
	}
	a.gc(0x02, l.typeIndex(wasm.GCPtr), wasm.GCPtrAddr)
	type op struct{ u, s, align byte }
	ops := map[byte]op{'b': {0x31, 0x30, 0}, 'h': {0x33, 0x32, 1}, 'w': {0x35, 0x34, 2}, 'l': {0x29, 0x29, 3}, 'f': {0x2A, 0x2A, 2}, 'd': {0x2B, 0x2B, 3}}
	o := ops[r.Mem]
	if r.Signed {
		a.op(o.s, o.align)
	} else {
		a.op(o.u, o.align)
	}
	writeUleb128(a, uint64(r.Off))
}

// splitStoreReg is storeReg with split.
func (a *gcAsm) splitStoreReg(r ld.WasmGCReg, ptr, val func()) {
	l := a.l
	helper := func(name string) {
		i, _ := l.splitHelperIndex(name)
		a.call(l.helperFirst + i)
	}
	ptr()
	if r.Reg == 'r' && (r.Mem == 'r' || r.Mem == 's') {
		// (Word 0 of a string, 's', is stored as a pointer before A,
		// the next register, writes the word again.)
		a.i32(r.Off)
		val()
		helper(wasm.GCSplitStorePtr)
		return
	}
	a.gc(0x02, l.typeIndex(wasm.GCPtr), wasm.GCPtrAddr)
	val()
	if r.Reg == 'r' {
		helper(wasm.GCSplitPtrAddr)
		a.op(0xAD) // i64.extend_i32_u
	}
	type op struct{ op, align byte }
	ops := map[byte]op{'b': {0x3C, 0}, 'h': {0x3D, 1}, 'w': {0x3E, 2}, 'l': {0x37, 3}, 'f': {0x38, 2}, 'd': {0x39, 3}}
	o := ops[r.Mem]
	a.op(o.op, o.align)
	writeUleb128(a, uint64(r.Off))
}

// splitMakeFuncStub is makeFuncStub with split: the stub asks
// reflect.makeFuncArgsGC for a pointer to the pointers to new values
// of its arguments, stores its arguments there, calls call
// (reflect.makeFuncCallGC) with them and returns the results that the
// pointers it returns point to.
func (l *gcLinker) splitMakeFuncStub(f gcReflectFunc, call uint32) []byte {
	a := &gcAsm{l: l}
	ins := gcRegLetters(f.in)
	stack := uint32(len(ins))
	nparam := stack + uint32(len(gcStackRefs(f)))
	args, res := nparam, nparam+1
	a.splitLocals("rr")
	helper := func(name string) {
		i, _ := l.splitHelperIndex(name)
		a.call(l.helperFirst + i)
	}
	gcGlobalGet(&a.Buffer, wasm.GCGlobalCtxt)
	a.call(int(l.makeFuncArgs))
	a.set(args)
	p := uint32(0)
	for i, regs := range f.in {
		if gcOnStack(regs) {
			// The caller's value, which the function is given.
			a.get(args)
			a.i32(int64(i) * l.ptrSize())
			a.get(stack)
			helper(wasm.GCSplitStorePtr)
			stack++
			continue
		}
		for _, r := range regs {
			v := p
			a.storeReg(r, func() { a.loadPtrAt(args, int64(i)*l.ptrSize()) }, func() { a.get(v) })
			p++
		}
	}
	gcGlobalGet(&a.Buffer, wasm.GCGlobalCtxt)
	a.get(args)
	a.call(int(call))
	a.set(res)
	relocs := l.ldr.Relocs(f.typ)
	for j, regs := range f.out {
		if gcOnStack(regs) {
			// Into the caller's value for it.
			t := ld.WasmGCFuncOut(l.ctxt, f.typ, &relocs, j)
			a.get(stack)
			a.i32(0)
			a.loadPtrAt(res, int64(j)*l.ptrSize())
			a.i32(0)
			a.i32(ld.WasmGCSize(l.ctxt, t))
			helper(wasm.GCSplitMove)
			stack++
			continue
		}
		for _, r := range regs {
			a.loadReg(r, func() { a.loadPtrAt(res, int64(j)*l.ptrSize()) })
		}
	}
	a.end()
	return a.Bytes()
}
