// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wasm

import (
	"fmt"

	"cmd/internal/obj/wasm"
	"cmd/link/internal/ld"
	"cmd/link/internal/loader"
	"strings"
)

// Calls of functions whose type is known at run time, for package
// reflect (GOWASM=gc): Value.Call and MakeFunc.
//
// A call goes through wasm.reflectcall, which finds the adapter of the
// func type by the address of its descriptor: the adapter loads each
// register of the arguments from the values that an array of pointers
// points to, calls the function through the table, and stores each
// register of the results through another array of pointers. A
// function that MakeFunc makes is a stub of its type, which makes an
// object of each argument, and calls reflect.makeFuncCallGC with them
// and the closure it was called through; wasm.makefunc finds the stub.
//
// Only the func types the program has descriptors of are covered, and
// only those whose arguments and results are all in registers.

// The registers of the register ABI (see cmd/compile/internal/ssa).
const gcIntRegs, gcFloatRegs = 120, 120

// gcReflectFunc is a func type that reflect can call or make.
type gcReflectFunc struct {
	typ     loader.Sym
	in, out [][]ld.WasmGCReg
}

func gcRegLetters(ps [][]ld.WasmGCReg) string {
	var b strings.Builder
	for _, p := range ps {
		if gcOnStack(p) {
			continue
		}
		for _, r := range p {
			b.WriteByte(r.Reg)
		}
	}
	return b.String()
}

// gcOnStack reports whether an argument or a result is not in
// registers: the caller passes an object for it, after the arguments
// in registers (those of the arguments, then those of the results).
func gcOnStack(p []ld.WasmGCReg) bool {
	return len(p) == 1 && p[0].Reg == ld.WasmGCOnStack
}

// gcStackRefs returns one 'r' for each argument and result of f that
// is not in registers.
func gcStackRefs(f gcReflectFunc) string {
	n := 0
	for _, ps := range [][][]ld.WasmGCReg{f.in, f.out} {
		for _, p := range ps {
			if gcOnStack(p) {
				n++
			}
		}
	}
	return strings.Repeat("r", n)
}

// reflectTypes returns the func types of the program that reflect can
// call or make.
func (l *gcLinker) reflectTypes() []gcReflectFunc {
	ldr := l.ldr
	var fs []gcReflectFunc
	for s := loader.Sym(1); s < loader.Sym(ldr.NSym()); s++ {
		name := ldr.SymName(s)
		if !strings.HasPrefix(name, "type:") || strings.HasPrefix(name, "type:.") || !ldr.AttrReachable(s) || ldr.SymType(s).IsText() || ldr.SymValue(s) == 0 || len(ldr.Data(s)) < 48 {
			continue
		}
		if !ld.WasmGCIsFunc(l.ctxt, s) {
			continue
		}
		in, out, ok := ld.WasmGCFunc(l.ctxt, s, gcIntRegs, gcFloatRegs)
		if !ok {
			continue
		}
		fs = append(fs, gcReflectFunc{s, in, out})
	}
	return fs
}

// load pushes register r of the value that the pointer pushed by ptr
// points to.
func (a *gcAsm) loadReg(r ld.WasmGCReg, ptr func()) {
	if splitMode() {
		a.splitLoadReg(r, ptr)
		return
	}
	ptr()
	a.i32(r.Off)
	k := r.Mem
	if r.Reg == 'r' {
		k = 'r'
	}
	idx, _ := a.l.helperIndex(wasm.GCLoadPrefix + string(k))
	a.op(0x10)
	writeUleb128(a, uint64(a.l.helperFirst+idx))
	if r.Signed {
		switch r.Mem {
		case 'b':
			a.op(0xC2) // i64.extend8_s
		case 'h':
			a.op(0xC3)
		case 'w':
			a.op(0xC4)
		}
	}
}

// storeReg stores register r, which val pushes, in the value that the
// pointer pushed by ptr points to.
func (a *gcAsm) storeReg(r ld.WasmGCReg, ptr, val func()) {
	if splitMode() {
		a.splitStoreReg(r, ptr, val)
		return
	}
	ptr()
	a.i32(r.Off)
	val()
	k := r.Mem
	if r.Reg == 'r' {
		k = 'r'
	}
	idx, _ := a.l.helperIndex(wasm.GCStorePrefix + string(k))
	a.op(0x10)
	writeUleb128(a, uint64(a.l.helperFirst+idx))
}

func gcRegType(k byte) byte {
	switch k {
	case 'l':
		return wasm.GCTypeI64
	case 'f':
		return wasm.GCTypeF32
	case 'd':
		return wasm.GCTypeF64
	}
	return wasm.GCTypeEqRef
}

// reflectCall returns the body of the adapter that calls a function of
// type f.
//
//	local 0: the code; 1: the closure; 2: the arguments; 3: the
//	results; 4: the receiver; 5: flags (1: there is a receiver); then
//	one per register of the results
func (l *gcLinker) reflectCall(f gcReflectFunc) []byte {
	a := &gcAsm{l: l}
	outs := gcRegLetters(f.out)
	if splitMode() {
		a.splitLocals(outs)
	} else {
		writeUleb128(a, uint64(len(outs)))
		for i := range outs {
			writeUleb128(a, 1)
			a.op(gcRegType(outs[i]))
		}
	}
	// The arrays of pointers may be anything a pointer is: an array,
	// a variable on the stack...
	elem := func(arr uint32, i int) func() {
		return func() { a.loadPtrAt(arr, int64(i)*a.l.ptrSize()) }
	}
	ins := gcRegLetters(f.in)
	call := func(rcvr bool) {
		if rcvr {
			a.get(4)
		}
		for i, p := range f.in {
			if gcOnStack(p) {
				continue
			}
			for _, r := range p {
				a.loadReg(r, elem(2, i))
			}
		}
		// Those not in registers: the object of the argument (a
		// copy that reflect made), and the object for the result,
		// which the function fills.
		for i, p := range f.in {
			if gcOnStack(p) {
				elem(2, i)()
			}
		}
		for j, p := range f.out {
			if gcOnStack(p) {
				elem(3, j)()
			}
		}
		a.get(0)
		writeI64Const(a, 16)
		a.op(0x88, 0xA7) // i64.shr_u, i32.wrap_i64
		sig := wasm.GCFuncPrefix + ins + gcStackRefs(f) + "." + outs
		if rcvr {
			sig = wasm.GCFuncPrefix + "r" + ins + gcStackRefs(f) + "." + outs
		}
		a.op(0x11) // call_indirect
		writeUleb128(a, uint64(a.typ(sig)))
		writeUleb128(a, 0)
		for i := len(outs) - 1; i >= 0; i-- {
			a.set(uint32(6 + i))
		}
	}
	a.get(1)
	gcGlobalSet(&a.Buffer, wasm.GCGlobalCtxt)
	a.get(5)
	a.i32(1)
	a.op(0x71, 0x04, 0x40) // i32.and, if
	call(true)
	a.op(0x05) // else
	call(false)
	a.op(0x0B)
	n := 0
	for j, p := range f.out {
		if gcOnStack(p) {
			continue
		}
		for _, r := range p {
			v := uint32(6 + n)
			a.storeReg(r, elem(3, j), func() { a.get(v) })
			n++
		}
	}
	a.op(0x0B)
	return a.Bytes()
}

// makeFuncStub returns the body of the function of type f that a
// function that MakeFunc makes runs.
func (l *gcLinker) makeFuncStub(f gcReflectFunc, call uint32) []byte {
	if splitMode() {
		return l.splitMakeFuncStub(f, call)
	}
	a := &gcAsm{l: l}
	ins := gcRegLetters(f.in)
	// (After the arguments in registers, the objects of those that
	// are not, and of such results.)
	stack := uint32(len(ins))
	nparam := stack + uint32(len(gcStackRefs(f)))
	args, obj, res := nparam, nparam+1, nparam+2
	writeUleb128(a, 1)
	writeUleb128(a, 3)
	a.op(wasm.GCTypeEqRef)
	refs := a.typ(wasm.GCArrayPrefix + "r")
	loadr, _ := l.helperIndex(wasm.GCLoadPrefix + "r")
	a.i32(int64(len(f.in)))
	a.gc(0x07, refs) // array.new_default
	a.set(args)
	relocs := l.ldr.Relocs(f.typ)
	empty := &gcData{relocs: make(map[int64]loader.Reloc)}
	p := uint32(0)
	for i, regs := range f.in {
		if gcOnStack(regs) {
			// The caller's object, which is the function's to keep.
			a.get(args)
			a.cast(refs)
			a.i32(int64(i))
			a.get(stack)
			a.gc(0x0E, refs) // array.set
			stack++
			continue
		}
		t := ld.WasmGCFuncIn(l.ctxt, f.typ, &relocs, i)
		l.newObject(&a.Buffer, t, empty, 0)
		a.set(obj)
		for _, r := range regs {
			v := p
			a.storeReg(r, func() { a.get(obj) }, func() { a.get(v) })
			p++
		}
		a.get(args)
		a.cast(refs)
		a.i32(int64(i))
		a.get(obj)
		a.gc(0x0E, refs) // array.set
	}
	gcGlobalGet(&a.Buffer, wasm.GCGlobalCtxt)
	a.get(args)
	a.op(0x10)
	writeUleb128(a, uint64(call))
	a.set(res)
	copyObj, _ := l.helperIndex(wasm.GCCopy)
	for j, regs := range f.out {
		result := func() {
			a.get(res)
			a.i32(int64(j) * l.ptrSize())
			a.op(0x10)
			writeUleb128(a, uint64(l.helperFirst+loadr))
		}
		if gcOnStack(regs) {
			// Into the caller's object for it.
			result()
			a.get(stack)
			a.op(0x10)
			writeUleb128(a, uint64(l.helperFirst+copyObj))
			stack++
			continue
		}
		for _, r := range regs {
			a.loadReg(r, result)
		}
	}
	a.op(0x0B)
	return a.Bytes()
}

// reflectFuncs returns wasm.reflectcall, wasm.makefunc and the
// functions they dispatch to, the first of which is to be the function
// numbered first; nimport is the number of functions imported.
func (l *gcLinker) reflectFuncs(first, nimport int, funcIndex func(loader.Sym) int64) []*wasmFunc {
	// (A function that nothing reachable calls is not needed, nor is
	// what it would dispatch to: the adapters are a function for each
	// func type of the program.)
	used := func(name string) bool {
		for v := 0; v < 2; v++ {
			if s := l.ldr.Lookup(name, v); s != 0 && l.ldr.AttrReachable(s) {
				return true
			}
		}
		return false
	}
	var types []gcReflectFunc
	callUsed, makeUsed := used(wasm.GCReflectCall), used(wasm.GCMakeFunc)
	if callUsed || makeUsed {
		types = l.reflectTypes()
	}
	callSig := l.typeIndex(wasm.GCFuncPrefix + "ilrrrri.i")
	makeSig := l.typeIndex(wasm.GCFuncPrefix + "i.l")
	adapterSig := l.typeIndex(wasm.GCFuncPrefix + "lrrrri.")

	var extra []*wasmFunc
	next := first + 2
	// The adapters, one per register layout.
	adapters := make(map[loader.Sym]int)
	if callUsed {
		byLayout := make(map[string]int)
		for _, f := range types {
			var key strings.Builder
			for _, ps := range [][][]ld.WasmGCReg{f.in, f.out} {
				for _, p := range ps {
					for _, r := range p {
						key.WriteString(string([]byte{r.Mem, r.Reg}))
						if r.Signed {
							key.WriteByte('s')
						}
						fmt.Fprintf(&key, "%d", r.Off)
					}
					key.WriteByte(',')
				}
				key.WriteByte(';')
			}
			idx, ok := byLayout[key.String()]
			if !ok {
				idx = next
				next++
				byLayout[key.String()] = idx
				extra = append(extra, &wasmFunc{Name: "wasm.reflectcall." + l.ldr.SymName(f.typ), Type: adapterSig, Code: l.reflectCall(f)})
			}
			adapters[f.typ] = idx
		}
	}
	// The stubs of MakeFunc.
	stubs := make(map[loader.Sym]int)
	if makeUsed {
		var call loader.Sym
		for v := 0; v < 2 && call == 0; v++ {
			call = l.ldr.Lookup("reflect.makeFuncCallGC", v)
		}
		if splitMode() {
			// (The stubs call it for the pointers to their arguments.)
			var args loader.Sym
			for v := 0; v < 2 && args == 0; v++ {
				args = l.ldr.Lookup("reflect.makeFuncArgsGC", v)
			}
			if args == 0 || !l.ldr.AttrReachable(args) {
				call = 0
			} else {
				l.makeFuncArgs = uint32(funcIndex(args))
			}
		}
		if call != 0 && l.ldr.AttrReachable(call) {
			for _, f := range types {
				sig := l.typeIndex(wasm.GCFuncPrefix + gcRegLetters(f.in) + gcStackRefs(f) + "." + gcRegLetters(f.out))
				stubs[f.typ] = next
				next++
				extra = append(extra, &wasmFunc{Name: "wasm.makefunc." + l.ldr.SymName(f.typ), Type: sig, Code: l.makeFuncStub(f, uint32(funcIndex(call)))})
			}
		}
	}

	// wasm.reflectcall: by the address of the type.
	a := &gcAsm{l: l}
	writeUleb128(a, 0)
	for _, f := range types {
		idx, ok := adapters[f.typ]
		if !ok {
			continue
		}
		a.get(0)
		a.i32(l.ldr.SymValue(f.typ))
		a.op(0x46, 0x04, 0x40) // i32.eq, if
		for i := uint32(1); i <= 6; i++ {
			a.get(i)
		}
		a.op(0x10)
		writeUleb128(a, uint64(idx))
		a.i32(1)
		a.op(0x0F, 0x0B)
	}
	a.i32(0)
	a.op(0x0B)
	reflectCall := &wasmFunc{Name: wasm.GCReflectCall, Type: callSig, Code: a.Bytes()}

	// wasm.makefunc: the code of the stub, as a function value has it.
	a = &gcAsm{l: l}
	writeUleb128(a, 0)
	for _, f := range types {
		idx, ok := stubs[f.typ]
		if !ok {
			continue
		}
		a.get(0)
		a.i32(l.ldr.SymValue(f.typ))
		a.op(0x46, 0x04, 0x40)
		writeI64Const(a, int64(funcValueOffset+idx-nimport)<<16)
		a.op(0x0F, 0x0B)
	}
	writeI64Const(a, 0)
	a.op(0x0B)
	makeFunc := &wasmFunc{Name: wasm.GCMakeFunc, Type: makeSig, Code: a.Bytes()}

	return append([]*wasmFunc{reflectCall, makeFunc}, extra...)
}
