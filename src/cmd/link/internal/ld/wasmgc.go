// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"cmd/internal/sys"
	"cmd/link/internal/loader"
	"internal/abi"
)

// A WasmGCLeaf is one field of the WebAssembly struct of a Go type
// with GOWASM=gc: the linker's copy of the layout that
// cmd/compile/internal/wasm/gc.go describes and computes.
type WasmGCLeaf struct {
	Off  int64
	Kind byte       // b h w l f d r, or a for an array of its own
	Type loader.Sym // of kind a: the array type
}

// WasmGCLeaves returns the fields of the WebAssembly struct of the Go
// type whose descriptor is typ.
func WasmGCLeaves(ctxt *Link, typ loader.Sym) []WasmGCLeaf {
	return wasmGCAppendLeaves(ctxt, nil, typ, 0)
}

func wasmGCAppendLeaves(ctxt *Link, ls []WasmGCLeaf, typ loader.Sym, off int64) []WasmGCLeaf {
	ldr, arch := ctxt.loader, ctxt.Arch
	// word appends leaves of a word each (arch.PtrSize bytes), of the
	// kinds given ('i' an integer word), then those of 8 bytes each in
	// d8, at an offset aligned to 8 (as cmd/compile/internal/wasm's
	// gcAppendLeaves).
	word := func(kinds, d8 string) []WasmGCLeaf {
		o := off
		for i := range kinds {
			k := kinds[i]
			if k == 'i' {
				k = WasmGCWordKind(arch)
			}
			ls = append(ls, WasmGCLeaf{Off: o, Kind: k})
			o += int64(arch.PtrSize)
		}
		o = Rnd(o, 8)
		for i := range d8 {
			ls = append(ls, WasmGCLeaf{Off: o, Kind: d8[i]})
			o += 8
		}
		return ls
	}
	data := ldr.Data(typ)
	switch decodetypeKind(arch, data) {
	case abi.Struct:
		n := decodetypeStructFieldCount(ldr, arch, typ)
		for i := 0; i < n; i++ {
			ls = wasmGCAppendLeaves(ctxt, ls, decodetypeStructFieldType(ctxt, arch, typ, i), off+decodetypeStructFieldOffset(ldr, arch, typ, i))
		}
		return ls
	case abi.Array:
		switch n := decodetypeArrayLen(ldr, arch, typ); {
		case n == 0:
			return ls
		case n == 1:
			return wasmGCAppendLeaves(ctxt, ls, decodetypeArrayElem(ctxt, arch, typ), off)
		}
		return append(ls, WasmGCLeaf{Off: off, Kind: 'a', Type: typ})
	case abi.String, abi.Slice:
		return word("rii", "")
	case abi.Interface:
		return word("ir", "dd")
	case abi.Complex64:
		return append(ls, WasmGCLeaf{Off: off, Kind: 'f'}, WasmGCLeaf{Off: off + 4, Kind: 'f'})
	case abi.Complex128:
		return word("", "dd")
	case abi.Pointer, abi.Map, abi.Chan, abi.Func, abi.UnsafePointer:
		return word("r", "")
	case abi.Float32:
		return append(ls, WasmGCLeaf{Off: off, Kind: 'f'})
	case abi.Float64:
		return word("", "d")
	}
	return append(ls, WasmGCLeaf{Off: off, Kind: "bh?w???l"[decodetypeSize(arch, data)-1]})
}

// WasmGCWordKind is the kind of a word that is an integer (an int, a
// uintptr, the length of a string): 'w' or 'l' as a pointer is 4 or 8
// bytes.
func WasmGCWordKind(arch *sys.Arch) byte {
	if arch.PtrSize == 4 {
		return 'w'
	}
	return 'l'
}

// WasmGCArray returns the length and the element type of the array
// type whose descriptor is typ, and reports whether it is an array of
// two or more elements: one that is a WebAssembly array.
func WasmGCArray(ctxt *Link, typ loader.Sym) (n int64, elem loader.Sym, ok bool) {
	ldr, arch := ctxt.loader, ctxt.Arch
	if decodetypeKind(arch, ldr.Data(typ)) != abi.Array {
		return 0, 0, false
	}
	n = decodetypeArrayLen(ldr, arch, typ)
	return n, decodetypeArrayElem(ctxt, arch, typ), n >= 2
}

// WasmGCSize returns the size of the Go type whose descriptor is typ.
func WasmGCSize(ctxt *Link, typ loader.Sym) int64 {
	return decodetypeSize(ctxt.Arch, ctxt.loader.Data(typ))
}

// WasmGCInitFuncs returns the functions that initialize the packages of
// the program, in the order they are to be called.
func WasmGCInitFuncs(ctxt *Link) []loader.Sym {
	ldr := ctxt.loader
	if ctxt.mainInittasks == 0 {
		return nil
	}
	var fns []loader.Sym
	tasks := ldr.Relocs(ctxt.mainInittasks)
	for i := 0; i < tasks.Count(); i++ {
		task := tasks.At(i).Sym()
		if task == 0 {
			continue
		}
		relocs := ldr.Relocs(task)
		for j := 0; j < relocs.Count(); j++ {
			if fn := relocs.At(j).Sym(); fn != 0 && ldr.SymType(fn).IsText() {
				fns = append(fns, fn)
			}
		}
	}
	return fns
}

// A WasmGCReg is a scalar of a Go value as the register ABI passes it:
// in a register of WebAssembly type Reg, from the field of kind Mem at
// Off in the Go value (see WasmGCLeaf).
type WasmGCReg struct {
	Off    int64
	Mem    byte // b h w l f d r
	Reg    byte // l f d r
	Signed bool // an integer narrower than a register, sign-extended
}

// WasmGCRegs returns the scalars of a value of the Go type typ in the
// order the register ABI passes them.
func WasmGCRegs(ctxt *Link, typ loader.Sym) []WasmGCReg {
	return wasmGCAppendRegs(ctxt, nil, typ, 0)
}

func wasmGCAppendRegs(ctxt *Link, rs []WasmGCReg, typ loader.Sym, off int64) []WasmGCReg {
	ldr, arch := ctxt.loader, ctxt.Arch
	data := ldr.Data(typ)
	add := func(o int64, mem, reg byte, signed bool) {
		rs = append(rs, WasmGCReg{Off: off + o, Mem: mem, Reg: reg, Signed: signed})
	}
	switch k := decodetypeKind(arch, data); k {
	case abi.Struct:
		n := decodetypeStructFieldCount(ldr, arch, typ)
		for i := 0; i < n; i++ {
			ft := decodetypeStructFieldType(ctxt, arch, typ, i)
			if decodetypeSize(arch, ldr.Data(ft)) == 0 {
				continue
			}
			rs = wasmGCAppendRegs(ctxt, rs, ft, off+decodetypeStructFieldOffset(ldr, arch, typ, i))
		}
	case abi.Array:
		n := decodetypeArrayLen(ldr, arch, typ)
		e := decodetypeArrayElem(ctxt, arch, typ)
		size := decodetypeSize(arch, ldr.Data(e))
		for i := int64(0); i < n; i++ {
			rs = wasmGCAppendRegs(ctxt, rs, e, off+i*size)
		}
	case abi.String, abi.Slice:
		ps, w := int64(arch.PtrSize), WasmGCWordKind(arch)
		add(0, 'r', 'r', false)
		add(ps, w, 'l', false)
		add(2*ps, w, 'l', false)
	case abi.Interface:
		// The word that holds the type is an integer in memory and
		// a pointer in a register. (The two floats of its inline
		// payload are after two words, at 8 or 16.)
		ps := int64(arch.PtrSize)
		add(0, WasmGCWordKind(arch), 'r', false)
		add(ps, 'r', 'r', false)
		add(2*ps, 'd', 'd', false)
		add(2*ps+8, 'd', 'd', false)
	case abi.Complex64:
		add(0, 'f', 'f', false)
		add(4, 'f', 'f', false)
	case abi.Complex128:
		add(0, 'd', 'd', false)
		add(8, 'd', 'd', false)
	case abi.Pointer, abi.Map, abi.Chan, abi.Func, abi.UnsafePointer:
		add(0, 'r', 'r', false)
	case abi.Float32:
		add(0, 'f', 'f', false)
	case abi.Float64:
		add(0, 'd', 'd', false)
	default:
		size := decodetypeSize(arch, data)
		signed := k == abi.Int8 || k == abi.Int16 || k == abi.Int32 || k == abi.Int && size < 8
		add(0, "bh?w???l"[size-1], 'l', signed)
	}
	return rs
}

// WasmGCOnStack is the Reg of the one WasmGCReg that WasmGCFunc gives
// for an argument or a result that is not in registers.
const WasmGCOnStack = 'S'

// WasmGCFunc returns the scalars of the arguments and of the results
// of the func type typ, by argument and result, as the register ABI
// passes them (there are intRegs and floatRegs registers). One that
// does not fit in the registers left is not in registers at all (and
// leaves them to those after it): it is a single WasmGCOnStack.
func WasmGCFunc(ctxt *Link, typ loader.Sym, intRegs, floatRegs int) (in, out [][]WasmGCReg, ok bool) {
	ldr, arch := ctxt.loader, ctxt.Arch
	data := ldr.Data(typ)
	relocs := ldr.Relocs(typ)
	assign := func(n int, at func(i int) loader.Sym) ([][]WasmGCReg, bool) {
		var ps [][]WasmGCReg
		ni, nf := 0, 0
		for i := 0; i < n; i++ {
			t := at(i)
			rs := WasmGCRegs(ctxt, t)
			i0, f0 := ni, nf
			for _, r := range rs {
				if r.Reg == 'f' || r.Reg == 'd' {
					nf++
				} else {
					ni++
				}
			}
			if ni > intRegs || nf > floatRegs {
				ni, nf = i0, f0
				rs = []WasmGCReg{{Reg: WasmGCOnStack}}
			}
			ps = append(ps, rs)
		}
		return ps, true
	}
	in, ok1 := assign(decodetypeFuncInCount(arch, data), func(i int) loader.Sym {
		return decodetypeFuncInType(ldr, arch, typ, &relocs, i)
	})
	out, ok2 := assign(decodetypeFuncOutCount(arch, data), func(i int) loader.Sym {
		return decodetypeFuncOutType(ldr, arch, typ, &relocs, i)
	})
	return in, out, ok1 && ok2
}

// WasmGCIsFunc reports whether typ is the descriptor of a func type.
func WasmGCIsFunc(ctxt *Link, typ loader.Sym) bool {
	return decodetypeKind(ctxt.Arch, ctxt.loader.Data(typ)) == abi.Func
}

// WasmGCFuncIn returns the type of argument i of the func type typ.
func WasmGCFuncIn(ctxt *Link, typ loader.Sym, relocs *loader.Relocs, i int) loader.Sym {
	return decodetypeFuncInType(ctxt.loader, ctxt.Arch, typ, relocs, i)
}

// WasmGCFuncOut returns the type of result i of the func type typ.
func WasmGCFuncOut(ctxt *Link, typ loader.Sym, relocs *loader.Relocs, i int) loader.Sym {
	return decodetypeFuncOutType(ctxt.loader, ctxt.Arch, typ, relocs, i)
}

// WasmGCPtrdata returns the number of bytes of a value of the Go type
// typ that can hold pointers.
func WasmGCPtrdata(ctxt *Link, typ loader.Sym) int64 {
	return decodetypePtrdata(ctxt.Arch, ctxt.loader.Data(typ))
}
