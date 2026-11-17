// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wasm

import (
	"bytes"
	"cmd/internal/obj/wasm"
	"cmd/internal/objabi"
	"cmd/link/internal/loader"
	"internal/buildcfg"
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
}

// gcSplitSlotsOf is the function, exported to the host as "slotsof",
// that returns the array of a pointer: what the host keeps a block of
// linear memory by (gd.register, weak pointers, cleanups).
const gcSplitSlotsOf = "wasm.slotsof"

func (l *gcLinker) splitHelperIndex(name string) (int, bool) {
	for i, h := range gcSplitHelpers {
		if h == name {
			return i, true
		}
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
	// GCPtr: final, immutable fields.
	w.Write([]byte{0x5F, 0x03, wasm.GCTypeI32, 0x00})
	w.WriteByte(wasm.GCRefNull)
	writeSleb128(w, int64(slots))
	w.Write([]byte{0x00, wasm.GCTypeI32, 0x00})
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
			if r.Siz() != 8 || r.Sym() == 0 || (r.Type() != objabi.R_ADDR) {
				continue
			}
			if ldr.SymType(r.Sym()).IsText() {
				continue // a function's index, not a pointer
			}
			at := addr + int64(r.Off())
			if at%8 != 0 || uint64(at) >= end {
				continue
			}
			words = append(words, uint32(at/8))
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
	a.i32(3)
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
			a.i32(3)
			a.op(0x76) // i32.shr_u
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
			a.op(0x29, 0x03, 0x00) // i64.load
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
			a.op(0x37, 0x03, 0x00) // i64.store
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
			a.i32(3)
			a.op(0x76)
			a.tee(4)
			a.get(3)
			a.gc(0x0F)
			a.op(0x4F)
			a.if_()
			ret(a)
			a.end()
			a.get(3)
			a.get(4)
			a.get(2)
			a.gc(0x0E, slots) // array.set
			a.end()
		case wasm.GCSplitAddPtr:
			// p 0, rel 1
			a.splitLocals("")
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
			refNull(a)
			a.end()
			a.get(1)
			a.gc(0x00, ptr)
			a.end()
		case wasm.GCSplitMove:
			// d 0, drel 1, s 2, srel 3, n 4; da 5, sa 6, di 7, si 8, cnt 9, first 10
			a.splitLocals("aaiiii")
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
			a.i32(7)
			a.op(0x6A)
			a.i32(3)
			a.op(0x76)
			a.set(10)
			a.get(7)
			a.get(4)
			a.op(0x6A)
			a.i32(3)
			a.op(0x76)
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
			a.i32(7)
			a.op(0x71) // i32.and
			a.brIf(0)
			a.get(8)
			a.i32(7)
			a.op(0x6A)
			a.i32(3)
			a.op(0x76)
			a.tee(8)
			a.get(9)
			a.op(0x6A)
			a.get(6)
			a.gc(0x0F)
			a.op(0x4B) // i32.gt_u
			a.brIf(0)
			a.get(5)
			a.get(10)
			a.get(6)
			a.get(8)
			a.get(9)
			a.gc(0x11, slots, slots) // array.copy
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
			a.i32(3)
			a.op(0x76)
			a.set(5)
			a.get(4)
			a.i32(7)
			a.op(0x6A)
			a.i32(3)
			a.op(0x76)
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
			writeI32Const(w, int32((dataEnd+7)/8))
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
