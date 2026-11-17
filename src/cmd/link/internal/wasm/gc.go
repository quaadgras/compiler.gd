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
	"cmd/link/internal/sym"
	"encoding/binary"
	"fmt"
	"internal/buildcfg"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Linking for GOWASM=gc: Go objects in the WebAssembly
// garbage-collected heap. See doc/gd/wasm-gc.md, and
// cmd/internal/obj/wasm/gc.go for how WebAssembly types are named and
// cmd/compile/internal/wasm/gc.go for how Go values are laid out.
//
// The linker defines every WebAssembly type that the code names, and
// turns every data symbol that the code refers to as an object (a
// global variable, a string constant, a composite literal) into a
// WebAssembly global that holds the object. The start function of the
// module creates the objects from the data of the symbols: first all
// of them, then the pointers between them.
//
// Data that is not an object stays in linear memory, at the address the
// linker gave it: type descriptors, itabs.

func gcMode() bool { return buildcfg.GOWASM.GC }

// gcExports are the functions of the runtime that the host calls, by
// the name they are exported under.
var gcExports = map[string]string{
	"free":      "runtime.linfree",
	"sched":     "runtime.sched",
	"gorun":     "runtime.gorun",
	"unstart":   "runtime.unstart",
	"deadlock":  "runtime.deadlock",
	"timerwait": "runtime.timerwait",
	"cleanup":   "runtime.runcleanup",

	// GOOS=js: a call from JavaScript (syscall/js).
	"callback":     "runtime.jscallback",
	"callbackdone": "runtime.jscallbackdone",
}

const sectionDataCount = 12

type gcLinker struct {
	ctorOf      map[loader.Sym]int     // the number of the constructor of each type
	objidFunc   int                    // the index of the import that gives an array an identity
	ptridFunc   int                    // the index of wasm.ptrid
	helperFirst int                    // the index of the first function the linker generates
	funcIndex   func(loader.Sym) int64 // the index of a Go function

	ctxt *ld.Link
	ldr  *loader.Loader

	types   []string
	typeIdx map[string]uint32

	globals    []loader.Sym
	globalIdx  map[loader.Sym]uint32
	globalType map[uint32]uint32 // the type of the object of each global of a data symbol

	passive bytes.Buffer // the data of the arrays created by array.new_data
	// initLocal is set while the function that makes the objects of
	// the data symbols is written: it has a local (0, an eqref) that
	// newObject may use.
	initLocal bool

	// The layouts of the structs of which there are objects (see
	// gclayout.go).
	layouts     []string         // by number
	layoutAddr  map[string]int64 // the address of the description of each
	layoutBase  int64            // the address of the first
	layoutNext  int64            // the address of the next
	layoutsDone bool             // the functions that go by layout are written

	// split: the index of reflect.makeFuncArgsGC.
	makeFuncArgs uint32

	// The indexes of the functions that read a layout and its fields.
	fnFind, fnFlen, fnFget, fnFgetr, fnFset int
}

func (l *gcLinker) typeIndex(name string) uint32 {
	if i, ok := l.typeIdx[name]; ok {
		return i
	}
	i := uint32(len(l.types))
	l.types = append(l.types, name)
	l.typeIdx[name] = i
	return i
}

// globalIndex returns the index of the global that holds the object of
// the data symbol s.
func (l *gcLinker) globalIndex(s loader.Sym) uint32 {
	if i, ok := l.globalIdx[s]; ok {
		return i
	}
	if typ := l.ldr.SymGoType(s); typ != 0 && l.ldr.SymSize(s) > 0 && ld.WasmGCPtrdata(l.ctxt, typ) == 0 && !splitMode() {
		l.ctxt.Errorf(s, "GOWASM=gc: a variable without pointers is referred to as an object")
	}
	i := uint32(wasm.GCGlobals + len(l.globals))
	if splitMode() {
		i = uint32(wasm.GCSplitGlobals + len(l.globals))
	}
	l.globals = append(l.globals, s)
	l.globalIdx[s] = i
	return i
}

// linear reports whether s is addressed in linear memory (or is a
// function) rather than being an object. The compiler decides for the
// symbols that code refers to (see gcLinear there); of the others,
// which only data refers to, the objects are those that have a Go
// type and the function values.
func (l *gcLinker) linear(s loader.Sym) bool {
	if l.ldr.SymType(s).IsText() {
		return true
	}
	name := l.ldr.SymName(s)
	if strings.HasPrefix(name, "type:") || strings.HasPrefix(name, "go:itab.") || strings.HasPrefix(name, "go:string.") || name == "runtime.zeroVal" || strings.Contains(name, "..typeAssert.") || strings.Contains(name, "..interfaceSwitch.") || strings.Contains(name, "..dict.") {
		return true
	}
	if typ := l.ldr.SymGoType(s); typ != 0 && l.ldr.SymSize(s) > 0 && ld.WasmGCPtrdata(l.ctxt, typ) == 0 {
		// A variable without pointers (as the compiler decides too).
		return true
	}
	if _, ok := l.globalIdx[s]; ok {
		return false
	}
	return l.ldr.SymGoType(s) == 0 && !strings.HasSuffix(name, "·f")
}

func gcStorage(k byte) byte {
	switch k {
	case 'b':
		return 0x78
	case 'h':
		return 0x77
	case 'w', 'i':
		return wasm.GCTypeI32
	case 'l':
		return wasm.GCTypeI64
	case 'f':
		return wasm.GCTypeF32
	case 'd':
		return wasm.GCTypeF64
	case 'r':
		return wasm.GCTypeEqRef
	case 'o':
		return 0x6E // anyref
	case 'x':
		return ExternRef
	}
	panic("bad wasm type letter " + string(k))
}

// A gcField is a field of the struct of a Go value, as its name
// spells it (see cmd/internal/obj/wasm/gc.go).
type gcField struct {
	letter byte  // of the scalar, or A for a nested array
	off    int64 // in the Go value
	// Of a nested array:
	esize int64 // the size of an element
	n     int64 // the number of elements
	elem  byte  // the letter of the array
}

// gcStructSize returns the size of the Go value of the struct name
// spells.
func gcStructSize(name string) int64 {
	_, size, _ := strings.Cut(name[len(wasm.GCStructPrefix):], "/")
	n, _ := strconv.ParseInt(size, 10, 64)
	return n
}

func gcParseStruct(name string) []gcField {
	var fs []gcField
	fields, _, _ := strings.Cut(name[len(wasm.GCStructPrefix):], "/")
	for _, tok := range strings.Split(fields, ",") {
		if tok == "" {
			continue
		}
		f := gcField{letter: tok[0]}
		if f.letter == 'A' {
			parts := strings.Split(tok[1:], ":")
			f.off, _ = strconv.ParseInt(parts[0], 10, 64)
			f.esize, _ = strconv.ParseInt(parts[1], 10, 64)
			f.n, _ = strconv.ParseInt(parts[2][:len(parts[2])-1], 10, 64)
			f.elem = parts[2][len(parts[2])-1]
		} else {
			f.off, _ = strconv.ParseInt(tok[1:], 10, 64)
		}
		fs = append(fs, f)
	}
	return fs
}

// writeType writes the definition of the type that name spells out.
func (l *gcLinker) writeType(w *bytes.Buffer, name string) {
	switch {
	case name == wasm.GCHeader:
		// Open to subtypes: the structs of Go values.
		w.Write([]byte{0x50, 0x00, 0x5F, 0x02, wasm.GCTypeI32, 0x00, wasm.GCTypeI32, 0x01})
	case name == wasm.GCBox:
		w.Write([]byte{0x5F, 0x02, wasm.GCTypeEqRef, 0x00, wasm.GCTypeI32, 0x00})
	case name == wasm.GCGeneric:
		w.Write([]byte{0x4F, 0x01}) // final, with one supertype
		writeUleb128(w, uint64(l.typeIndex(wasm.GCHeader)))
		w.Write([]byte{0x5F, 0x05, wasm.GCTypeI32, 0x00, wasm.GCTypeI32, 0x01, wasm.GCTypeI32, 0x00, wasm.GCTypeEqRef, 0x00, wasm.GCTypeEqRef, 0x00})
	case name == wasm.GCExtern:
		// A JavaScript value, for syscall/js (see wasm.GCJSBox).
		if splitMode() {
			// A subtype of GCPtr (which is then open to it: see
			// writeSplitTypes), with the value after its fields.
			w.Write([]byte{0x4F, 0x01}) // final, with one supertype
			writeUleb128(w, uint64(l.typeIndex(wasm.GCPtr)))
			w.Write([]byte{0x5F, 0x04, wasm.GCTypeI32, 0x01, wasm.GCRefNull})
			writeSleb128(w, int64(l.typeIndex(wasm.GCSlots)))
			w.Write([]byte{0x00, wasm.GCTypeI32, 0x01, ExternRef, 0x00})
			break
		}
		w.Write([]byte{0x5F, 0x01, ExternRef, 0x00})
	case name == wasm.GCLinear:
		w.Write([]byte{0x5F, 0x02, wasm.GCTypeI32, 0x00, wasm.GCTypeEqRef, 0x01})
	case strings.HasPrefix(name, wasm.GCStructPrefix):
		fields := gcParseStruct(name)
		w.Write([]byte{0x4F, 0x01}) // final, with one supertype
		writeUleb128(w, uint64(l.typeIndex(wasm.GCHeader)))
		w.WriteByte(0x5F)
		writeUleb128(w, uint64(wasm.GCHeaderFields+len(fields)))
		w.Write([]byte{wasm.GCTypeI32, 0x00, wasm.GCTypeI32, 0x01})
		for _, f := range fields {
			if f.letter == 'A' {
				w.WriteByte(wasm.GCTypeEqRef)
			} else {
				w.WriteByte(gcStorage(f.letter))
			}
			w.WriteByte(0x01) // mutable
		}
	case strings.HasPrefix(name, wasm.GCArrayPrefix):
		w.WriteByte(0x5E)
		w.WriteByte(gcStorage(name[len(wasm.GCArrayPrefix)]))
		w.WriteByte(0x01) // mutable
	case strings.HasPrefix(name, wasm.GCFuncPrefix):
		params, results, _ := strings.Cut(name[len(wasm.GCFuncPrefix):], ".")
		w.WriteByte(0x60)
		writeUleb128(w, uint64(len(params)))
		for i := range params {
			if splitMode() {
				l.splitValType(w, params[i])
				continue
			}
			w.WriteByte(gcStorage(params[i]))
		}
		writeUleb128(w, uint64(len(results)))
		for i := range results {
			if splitMode() {
				l.splitValType(w, results[i])
				continue
			}
			w.WriteByte(gcStorage(results[i]))
		}
	default:
		panic("bad wasm type name " + name)
	}
}

func gcSigName(params, results []byte) string {
	letter := func(t byte) byte {
		switch t {
		case I32:
			return 'i'
		case I64:
			return 'l'
		case F32:
			return 'f'
		case F64:
			return 'd'
		case ExternRef:
			return 'x'
		}
		panic("bad wasm value type")
	}
	var b strings.Builder
	b.WriteString(wasm.GCFuncPrefix)
	for _, t := range params {
		b.WriteByte(letter(t))
	}
	b.WriteByte('.')
	for _, t := range results {
		b.WriteByte(letter(t))
	}
	return b.String()
}

// Object types.

func (l *gcLinker) structName(typ loader.Sym) string {
	var b strings.Builder
	b.WriteString(wasm.GCStructPrefix)
	for i, lf := range ld.WasmGCLeaves(l.ctxt, typ) {
		if i > 0 {
			b.WriteByte(',')
		}
		if lf.Kind == 'a' {
			n, e, _ := ld.WasmGCArray(l.ctxt, lf.Type)
			k, _ := l.elem(e)
			fmt.Fprintf(&b, "A%d:%d:%d%c", lf.Off, ld.WasmGCSize(l.ctxt, e), n, k)
		} else {
			fmt.Fprintf(&b, "%c%d", lf.Kind, lf.Off)
		}
	}
	fmt.Fprintf(&b, "/%d", ld.WasmGCSize(l.ctxt, typ))
	return b.String()
}

// elem reports how the elements of an array of the type elem are
// stored (see gcElem in the compiler).
func (l *gcLinker) elem(elem loader.Sym) (kind byte, obj bool) {
	ls := ld.WasmGCLeaves(l.ctxt, elem)
	if len(ls) == 1 && ls[0].Kind != 'a' && ls[0].Off == 0 && gcKindSize(ls[0].Kind) == ld.WasmGCSize(l.ctxt, elem) {
		return ls[0].Kind, false
	}
	return 'o', true
}

func gcKindSize(k byte) int64 {
	switch k {
	case 'b':
		return 1
	case 'h':
		return 2
	case 'w', 'f':
		return 4
	}
	return 8
}

// objType returns the index of the WebAssembly type of objects of the
// Go type typ.
func (l *gcLinker) objType(typ loader.Sym) uint32 {
	if _, e, ok := ld.WasmGCArray(l.ctxt, typ); ok {
		k, _ := l.elem(e)
		return l.typeIndex(wasm.GCArrayPrefix + string(k))
	}
	return l.typeIndex(l.structName(typ))
}

// Instructions.

func gcOp(w *bytes.Buffer, op byte, idx ...uint32) {
	w.WriteByte(0xFB)
	w.WriteByte(op)
	for _, i := range idx {
		writeUleb128(w, uint64(i))
	}
}

// gcCast writes a cast to a non-null reference to the type, whose index
// is written as a heap type: signed.
func gcCast(w *bytes.Buffer, typ uint32) {
	w.Write([]byte{0xFB, 0x16})
	writeSleb128(w, int64(typ))
}
func gcGlobalGet(w *bytes.Buffer, i uint32)       { w.WriteByte(0x23); writeUleb128(w, uint64(i)) }
func gcGlobalSet(w *bytes.Buffer, i uint32)       { w.WriteByte(0x24); writeUleb128(w, uint64(i)) }
func gcRefNull(w *bytes.Buffer)                   { w.Write([]byte{0xD0, wasm.GCHeapEq}) }
func gcStructSet(w *bytes.Buffer, t, f uint32)    { gcOp(w, 0x05, t, f) }
func gcStructGet(w *bytes.Buffer, t, f uint32)    { gcOp(w, 0x02, t, f) }
func gcArraySet(w *bytes.Buffer, t uint32)        { gcOp(w, 0x0E, t) }
func gcArrayGet(w *bytes.Buffer, t uint32)        { gcOp(w, 0x0B, t) }
func gcArrayNewDefault(w *bytes.Buffer, t uint32) { gcOp(w, 0x07, t) }

// A gcData is the data of a symbol and its relocations.
type gcData struct {
	p      []byte
	relocs map[int64]loader.Reloc
	offs   []int64 // the offsets of the relocations, sorted
}

func (l *gcLinker) data(s loader.Sym) *gcData {
	d := &gcData{p: l.ldr.Data(s), relocs: make(map[int64]loader.Reloc)}
	if size := l.ldr.SymSize(s); int64(len(d.p)) < size {
		// Data the linker generated, such as the module data, which
		// it has written out to linear memory (see asmbGC) since.
		v, _ := dataSects.Load(l.ctxt)
		sects, _ := v.([]wasmDataSect)
		addr := uint64(l.ldr.SymValue(s))
		for _, ds := range sects {
			if ds.sect.Vaddr <= addr && addr+uint64(size) <= ds.sect.Vaddr+uint64(len(ds.data)) {
				d.p = ds.data[addr-ds.sect.Vaddr:][:size]
			}
		}
	}
	relocs := l.ldr.Relocs(s)
	for i := 0; i < relocs.Count(); i++ {
		r := relocs.At(i)
		if r.Siz() == 0 || r.Sym() == 0 {
			continue
		}
		d.relocs[int64(r.Off())] = r
		d.offs = append(d.offs, int64(r.Off()))
	}
	sort.Slice(d.offs, func(i, j int) bool { return d.offs[i] < d.offs[j] })
	return d
}

// hasReloc reports whether d has a relocation in [off, off+size).
func (d *gcData) hasReloc(off, size int64) bool {
	i := sort.Search(len(d.offs), func(i int) bool { return d.offs[i] >= off })
	return i < len(d.offs) && d.offs[i] < off+size
}

// bytes returns the size bytes of d at off: zero past the end.
func (d *gcData) bytes(off, size int64) []byte {
	b := make([]byte, size)
	if off < int64(len(d.p)) {
		copy(b, d.p[off:])
	}
	return b
}

func (d *gcData) uint(off, size int64) uint64 {
	var b [8]byte
	copy(b[:], d.bytes(off, size))
	return binary.LittleEndian.Uint64(b[:])
}

// newObject writes the instructions that push a new object of the Go
// type typ with the data of d at off. References to objects are left
// null: see fill.
func (l *gcLinker) newObject(w *bytes.Buffer, typ loader.Sym, d *gcData, off int64) {
	if n, e, ok := ld.WasmGCArray(l.ctxt, typ); ok {
		k, isObj := l.elem(e)
		t := l.typeIndex(wasm.GCArrayPrefix + string(k))
		size := ld.WasmGCSize(l.ctxt, e)
		switch {
		case isObj:
			if n > 32 {
				// Many to make one by one here (the code of each,
				// in a function the engine is slow to compile): the
				// runtime makes them, if they are all zero.
				newArr := l.ldr.Lookup("runtime.newobjarrayT", 0)
				zero := !d.hasReloc(off, n*size)
				for _, b := range d.bytes(off, n*size) {
					zero = zero && b == 0
				}
				// (How many are not zero.)
				var set int64
				for i := int64(0); i < n && !zero; i++ {
					o := off + i*size
					isZero := !d.hasReloc(o, size)
					for _, b := range d.bytes(o, size) {
						isZero = isZero && b == 0
					}
					if !isZero {
						set++
					}
				}
				if canCall := newArr != 0 && l.ldr.AttrReachable(newArr) && l.funcIndex != nil && l.numbered(e); !zero && 2*set <= n && canCall && l.initLocal && l.flat(e) {
					// Mostly zero (a table by number, of which few
					// are used): the runtime makes them all, and
					// those that are not zero are made here and put
					// in their places.
					writeI32Const(w, int32(l.ldr.SymValue(e)))
					gcOp(w, 0x1C) // ref.i31
					writeI64Const(w, n)
					w.WriteByte(0x10) // call
					writeUleb128(w, uint64(l.funcIndex(newArr)))
					w.WriteByte(0x21) // local.set
					writeUleb128(w, 0)
					for i := int64(0); i < n; i++ {
						o := off + i*size
						isZero := !d.hasReloc(o, size)
						for _, b := range d.bytes(o, size) {
							isZero = isZero && b == 0
						}
						if isZero {
							continue
						}
						w.WriteByte(0x20) // local.get
						writeUleb128(w, 0)
						gcCast(w, t)
						writeI32Const(w, int32(i))
						l.newObject(w, e, d, o)
						gcArraySet(w, t)
					}
					w.WriteByte(0x20) // local.get
					writeUleb128(w, 0)
					gcCast(w, t)
					return
				} else if !zero || !canCall && n <= 10000 || newArr == 0 || !l.ldr.AttrReachable(newArr) || l.funcIndex == nil {
					if n > 10000 {
						l.ctxt.Errorf(0, "GOWASM=gc: unsupported: a static array of %d structs", n)
						n = 0
					}
				} else {
					writeI32Const(w, int32(l.ldr.SymValue(e)))
					gcOp(w, 0x1C) // ref.i31: the address of the element type
					writeI64Const(w, n)
					w.WriteByte(0x10) // call
					writeUleb128(w, uint64(l.funcIndex(newArr)))
					gcCast(w, t)
					return
				}
			}
			for i := int64(0); i < n; i++ {
				l.newObject(w, e, d, off+i*size)
			}
			gcOp(w, 0x08, t, uint32(n)) // array.new_fixed
		case k == 'r' || off >= int64(len(d.p)):
			writeI32Const(w, int32(n))
			gcArrayNewDefault(w, t)
		default:
			// The data of the array is its elements.
			writeI32Const(w, int32(l.passive.Len()))
			writeI32Const(w, int32(n))
			gcOp(w, 0x09, t, 0) // array.new_data, of data segment 0
			l.passive.Write(d.bytes(off, n*size))
		}
		return
	}
	name := l.structName(typ)
	t := l.typeIndex(name)
	writeI32Const(w, int32(l.layout(name)))
	writeI32Const(w, 0)
	for _, lf := range ld.WasmGCLeaves(l.ctxt, typ) {
		o := off + lf.Off
		switch lf.Kind {
		case 'a':
			l.newObject(w, lf.Type, d, o)
		case 'r':
			// (A pointer to linear memory is a constant: it is in
			// the object as it is made.)
			if r, ok := d.relocs[o]; ok && l.linear(r.Sym()) {
				l.ref(w, r)
			} else {
				gcRefNull(w)
			}
		case 'f':
			w.WriteByte(0x43)
			w.Write(d.bytes(o, 4))
		case 'd':
			w.WriteByte(0x44)
			w.Write(d.bytes(o, 8))
		case 'l':
			v := int64(d.uint(o, 8))
			if r, ok := d.relocs[o]; ok {
				// An address in linear memory, or a function.
				v = l.ldr.SymValue(r.Sym()) + r.Add()
			}
			writeI64Const(w, v)
		default:
			writeI32Const(w, int32(d.uint(o, gcKindSize(lf.Kind))))
		}
	}
	gcOp(w, 0x00, t) // struct.new
}

// flat reports whether an object of the Go type typ has no objects of
// its own (so that making one does not use the local of initGlobals).
func (l *gcLinker) flat(typ loader.Sym) bool {
	if _, _, ok := ld.WasmGCArray(l.ctxt, typ); ok {
		return false
	}
	for _, lf := range ld.WasmGCLeaves(l.ctxt, typ) {
		if lf.Kind == 'a' {
			return false
		}
	}
	return true
}

// ref writes the instructions that push the pointer that the
// relocation r stands for.
func (l *gcLinker) ref(w *bytes.Buffer, r loader.Reloc) {
	rs := r.Sym()
	if l.linear(rs) {
		writeI32Const(w, int32(l.ldr.SymValue(rs)+r.Add()))
		gcOp(w, 0x1C) // ref.i31
		return
	}
	if r.Add() == 0 {
		gcGlobalGet(w, l.globalIndex(rs))
		return
	}
	// A pointer into the object.
	gcGlobalGet(w, l.globalIndex(rs))
	writeI32Const(w, int32(r.Add()))
	gcOp(w, 0x00, l.typeIndex(wasm.GCBox))
}

// fill writes the instructions that set the references of the object
// of the Go type typ that the instructions path push: those for which d
// has a relocation, at off.
func (l *gcLinker) fill(w *bytes.Buffer, path []byte, typ loader.Sym, d *gcData, off int64) {
	if !d.hasReloc(off, ld.WasmGCSize(l.ctxt, typ)) {
		return
	}
	if n, e, ok := ld.WasmGCArray(l.ctxt, typ); ok {
		k, isObj := l.elem(e)
		t := l.typeIndex(wasm.GCArrayPrefix + string(k))
		size := ld.WasmGCSize(l.ctxt, e)
		for i := int64(0); i < n; i++ {
			o := off + i*size
			if !d.hasReloc(o, size) {
				continue
			}
			var p bytes.Buffer
			p.Write(path)
			writeI32Const(&p, int32(i))
			if isObj {
				gcArrayGet(&p, t)
				gcCast(&p, l.objType(e))
				l.fill(w, p.Bytes(), e, d, o)
				continue
			}
			if r, ok := d.relocs[o]; ok && k == 'r' {
				w.Write(p.Bytes())
				l.ref(w, r)
				gcArraySet(w, t)
			}
		}
		return
	}
	t := l.typeIndex(l.structName(typ))
	for i, lf := range ld.WasmGCLeaves(l.ctxt, typ) {
		o := off + lf.Off
		field := uint32(wasm.GCHeaderFields + i)
		switch lf.Kind {
		case 'a':
			var p bytes.Buffer
			p.Write(path)
			gcStructGet(&p, t, field)
			gcCast(&p, l.objType(lf.Type))
			l.fill(w, p.Bytes(), lf.Type, d, o)
		case 'r':
			if r, ok := d.relocs[o]; ok && !l.linear(r.Sym()) {
				w.Write(path)
				l.ref(w, r)
				gcStructSet(w, t, field)
			}
		}
	}
}

// A gcDataChunk is data that a segment puts at addr in linear memory.
type gcDataChunk struct {
	addr int64
	data []byte
}

// gcDataChunks adds to chunks the parts of data, which is at addr, that
// are not zero: a run of 16 zero bytes or more is left out (a segment
// costs some 8 bytes).
func gcDataChunks(chunks []gcDataChunk, addr int64, data []byte) []gcDataChunk {
	const gap = 16
	for i := 0; i < len(data); {
		for i < len(data) && data[i] == 0 {
			i++
		}
		if i == len(data) {
			break
		}
		start, end := i, i
		for zeros := 0; i < len(data) && zeros < gap; i++ {
			if data[i] == 0 {
				zeros++
			} else {
				zeros, end = 0, i+1
			}
		}
		chunks = append(chunks, gcDataChunk{addr + int64(start), data[start:end]})
	}
	return chunks
}

// initGlobals returns the body of the function that creates the
// objects of the data symbols.
func (l *gcLinker) initGlobals() []byte {
	var create, fill bytes.Buffer
	l.initLocal = true
	defer func() { l.initLocal = false }()
	l.globalType = make(map[uint32]uint32)
	// (For finding what makes the function large: GOWASMGC_GLOBALS=1
	// prints the bytes of it that each data symbol takes.)
	note := func(string) {}
	if os.Getenv("GOWASMGC_GLOBALS") != "" {
		last, at := "", 0
		note = func(next string) {
			if n := create.Len() + fill.Len(); last != "" {
				fmt.Fprintf(os.Stderr, "initglobals %6d %s\n", n-at, last)
			}
			last, at = next, create.Len()+fill.Len()
		}
		defer note("")
	}
	// The list grows as the pointers of one object lead to another.
	for i := 0; i < len(l.globals); i++ {
		s := l.globals[i]
		note(l.ldr.SymName(s))
		gi := l.globalIdx[s]
		d := l.data(s)
		typ := l.ldr.SymGoType(s)
		if typ == 0 {
			// Words: a function value, which is the address of the
			// function and what it closes over.
			n := (l.ldr.SymSize(s) + 7) / 8
			name := wasm.GCStructPrefix
			for j := int64(0); j < n; j++ {
				if j > 0 {
					name += ","
				}
				name += fmt.Sprintf("l%d", j*8)
			}
			name += fmt.Sprintf("/%d", n*8)
			t := l.typeIndex(name)
			writeI32Const(&create, int32(l.layout(name)))
			writeI32Const(&create, 0)
			for j := int64(0); j < n; j++ {
				v := int64(d.uint(j*8, 8))
				if r, ok := d.relocs[j*8]; ok {
					if !l.linear(r.Sym()) {
						l.ctxt.Errorf(s, "GOWASM=gc: data without a type points to an object")
					}
					v = l.ldr.SymValue(r.Sym()) + r.Add()
				}
				writeI64Const(&create, v)
			}
			gcOp(&create, 0x00, t)
			gcGlobalSet(&create, gi)
			l.globalType[gi] = t
			continue
		}
		l.newObject(&create, typ, d, 0)
		gcGlobalSet(&create, gi)
		l.globalType[gi] = l.objType(typ)

		var path bytes.Buffer
		gcGlobalGet(&path, gi)
		gcCast(&path, l.objType(typ))
		l.fill(&fill, path.Bytes(), typ, d, 0)
	}
	var body bytes.Buffer
	// One local, for an array while its elements are set (newObject).
	writeUleb128(&body, 1)
	writeUleb128(&body, 1)
	body.WriteByte(0x6D) // eqref
	body.Write(create.Bytes())
	body.Write(fill.Bytes())
	body.WriteByte(0x0b) // end
	return body.Bytes()
}

// gcTypeGCDataOff is the offset of the GCData word in an abi.Type,
// where the number of the type's constructor goes.
const gcTypeGCDataOff = 32

// constructors returns the code that pushes a new object of each type
// whose descriptor is in the program, if the program makes objects of
// types it knows by their descriptors (with wasm.new): the number of a
// type's code, from 1, is in l.ctorOf.
func (l *gcLinker) constructors() [][]byte {
	ldr := l.ldr
	used := false
	for v := 0; v < 2; v++ {
		if s := ldr.Lookup(wasm.GCNew, v); s != 0 {
			used = true
		}
	}
	l.ctorOf = make(map[loader.Sym]int)
	if !used {
		return nil
	}
	var ctors [][]byte
	index := make(map[string]int)
	empty := &gcData{relocs: make(map[int64]loader.Reloc)}
	for s := loader.Sym(1); s < loader.Sym(ldr.NSym()); s++ {
		if !l.numbered(s) {
			continue
		}
		var w bytes.Buffer
		l.newObject(&w, s, empty, 0)
		key := w.String()
		n, ok := index[key]
		if !ok {
			ctors = append(ctors, w.Bytes())
			n = len(ctors)
			index[key] = n
		}
		l.ctorOf[s] = n
	}
	return ctors
}

// numbered reports whether the type descriptor s gets the number of a
// function that makes an object of the type (see constructors), with
// which the runtime makes one.
func (l *gcLinker) numbered(s loader.Sym) bool {
	ldr := l.ldr
	name := ldr.SymName(s)
	if !strings.HasPrefix(name, "type:") || strings.HasPrefix(name, "type:.") || !ldr.AttrReachable(s) || ldr.SymType(s).IsText() || ldr.SymValue(s) == 0 || len(ldr.Data(s)) < 48 {
		return false
	}
	return l.constructible(s)
}

// constructible reports whether newObject can make an object of typ.
func (l *gcLinker) constructible(typ loader.Sym) bool {
	if n, e, ok := ld.WasmGCArray(l.ctxt, typ); ok {
		if _, isObj := l.elem(e); isObj {
			return n <= 10000 && l.constructible(e)
		}
		return true
	}
	for _, lf := range ld.WasmGCLeaves(l.ctxt, typ) {
		if lf.Kind == 'a' && !l.constructible(lf.Type) {
			return false
		}
	}
	return true
}

// asmbGC collects the data that stays in linear memory.
func asmbGC(ctxt *ld.Link, ldr *loader.Loader) {
	var sects []wasmDataSect
	for _, name := range []string{"runtime.rodata", "runtime.types", "go:funcdesc", "runtime.firstmoduledata", "runtime.pclntab", "runtime.noptrdata", "runtime.data"} {
		s := ldr.Lookup(name, 0)
		if s == 0 {
			continue
		}
		sect := ldr.SymSect(s)
		if sect == nil || sect.Length == 0 {
			continue
		}
		dup := false
		for _, o := range sects {
			dup = dup || o.sect == sect
		}
		if dup {
			continue
		}
		sects = append(sects, wasmDataSect{sect, ld.DatblkBytes(ctxt, int64(sect.Vaddr), int64(sect.Length))})
	}
	dataSects.Store(ctxt, sects)
}

func asmb2GC(ctxt *ld.Link, ldr *loader.Loader) {
	l := &gcLinker{ctxt: ctxt, ldr: ldr, typeIdx: make(map[string]uint32), globalIdx: make(map[loader.Sym]uint32), layoutAddr: make(map[string]int64)}
	if splitMode() {
		l.typeIndex(wasm.GCPtr) // types 0 and 1
		l.typeIndex(wasm.GCSlots)
	} else {
		l.typeIndex(wasm.GCHeader) // before its subtypes
	}

	// The descriptions of the layouts follow the data in linear memory.
	// (Address 0 is no layout.)
	{
		v, _ := dataSects.Load(ctxt)
		sects, _ := v.([]wasmDataSect)
		l.layoutBase = int64(max(gcDataEnd(ldr, sects), 8)+7) &^ 7
		l.layoutNext = l.layoutBase
	}

	// The functions imported from the host.
	var hostImports []*wasmFunc
	hostImportMap := make(map[loader.Sym]int64)
	// The imports that call a function again on a new stack of the
	// engine's (wasm.GCHop): one for each type of function that does,
	// by the name of the type. It has the function's parameters, then
	// the function, and its results. (The types are numbered later:
	// those that the code refers to most come first.)
	hops := make(map[string]*wasmFunc)
	hopIndex := make(map[string]int64)
	sigOf := func(fn loader.Sym) string {
		relocs := ldr.Relocs(fn)
		for ri := 0; ri < relocs.Count(); ri++ {
			if r := relocs.At(ri); r.Type() == objabi.R_WASMTYPE && r.Siz() == 0 && ldr.SymName(r.Sym()) != wasm.GCLines {
				return ldr.SymName(r.Sym())
			}
		}
		return ""
	}
	for _, fn := range ctxt.Textp {
		relocs := ldr.Relocs(fn)
		for ri := 0; ri < relocs.Count(); ri++ {
			if r := relocs.At(ri); r.Type() == objabi.R_CALL && ldr.SymName(r.Sym()) == wasm.GCHop {
				sig := sigOf(fn)
				if _, ok := hops[sig]; !ok {
					hops[sig] = &wasmFunc{Module: "gd", Name: "hop"}
					hopIndex[sig] = int64(len(hostImports))
					hostImports = append(hostImports, hops[sig])
				}
				continue
			}
			if relocs.At(ri).Type() != objabi.R_WASMIMPORT {
				continue
			}
			wsym := ldr.WasmImportSym(fn)
			if wsym == 0 {
				panic(fmt.Sprintf("missing wasm symbol for %s", ldr.SymName(fn)))
			}
			wi := readWasmImport(ldr, wsym)
			// The type of the import is that of its wrapper, with
			// the integers as narrow as the import wants them.
			sig := []byte(gcSigName(fieldsToTypes(wi.Params), fieldsToTypes(wi.Results)))
			for rj := 0; rj < relocs.Count(); rj++ {
				if r := relocs.At(rj); r.Type() == objabi.R_WASMTYPE && r.Siz() == 0 {
					wrapper := ldr.SymName(r.Sym())
					for i := len(wasm.GCFuncPrefix); i < len(sig) && i < len(wrapper); i++ {
						if wrapper[i] == 'r' && sig[i] != 'x' && (wasm.GCImportRefs(wi.Module) || i >= len(wasm.GCFuncPrefix)+len(wi.Params)) {
							sig[i] = 'r'
						}
					}
					break
				}
			}
			hostImportMap[fn] = int64(len(hostImports))
			hostImports = append(hostImports, &wasmFunc{
				Module: wi.Module,
				Name:   wi.Name,
				Type:   l.typeIndex(string(sig)),
			})
		}
	}
	// The identity of an object that has no header to keep it in:
	// an array.
	if !splitMode() {
		objid := len(hostImports)
		hostImports = append(hostImports, &wasmFunc{Module: "gd", Name: "objid", Type: l.typeIndex(wasm.GCFuncPrefix + "r.i")})
		l.objidFunc = objid
	}
	funcIndex := func(s loader.Sym) int64 {
		return int64(len(hostImports)) + ldr.SymValue(s)>>16 - funcValueOffset
	}
	l.funcIndex = funcIndex

	// The functions.
	var buildid []byte
	var jsCalls []gcJSCall
	fns := make([]*wasmFunc, len(ctxt.Textp), len(ctxt.Textp)+16)
	for i, fn := range ctxt.Textp {
		// The name is Go's, which runtime.FuncForPC and
		// CallersFrames report (from the name section): only
		// spaces, which the names of some generated functions have,
		// are replaced.
		name := gcSpaceRegexp.ReplaceAllString(ldr.SymName(fn), "_")
		if ldr.SymName(fn) == "go:buildid" {
			buildid = ldr.Data(fn)
			fns[i] = &wasmFunc{Name: name, Type: l.typeIndex(wasm.GCFuncPrefix + "."), Code: []byte{0x00, 0x0b}}
			continue
		}
		wfn := new(bytes.Buffer)
		typ := int64(-1)
		relocs := ldr.Relocs(fn)
		P := ldr.Data(fn)
		off := int32(0)
		// After the code may be the table of its lines (see
		// wasm.GCLines), whose offsets are in the code as it is here:
		// grown records where the linker's indexes went.
		var lines []byte
		var grown []gcGrowth
		for ri := 0; ri < relocs.Count(); ri++ {
			r := relocs.At(ri)
			rs := r.Sym()
			if r.Siz() == 0 {
				if r.Type() == objabi.R_WASMTYPE && ldr.SymName(rs) == wasm.GCLines {
					lines = P[r.Off():]
					P = P[:r.Off()]
					continue
				}
				if r.Type() == objabi.R_WASMTYPE && typ < 0 {
					typ = int64(l.typeIndex(ldr.SymName(rs)))
				}
				continue
			}
			wfn.Write(P[off:r.Off()])
			off = r.Off()
			switch r.Type() {
			case objabi.R_ADDR:
				v := ldr.SymValue(rs) + r.Add()
				if r.Off() > 0 && P[r.Off()-1] == 0x41 && v != int64(int32(v)) {
					// An i32.const: a function's address (its
					// index << 16) does not fit when there are
					// more than 28672 functions.
					ldr.Errorf(fn, "address of %s (%#x) does not fit in an i32.const", ldr.SymName(rs), v)
				}
				writeSleb128(wfn, v)
			case objabi.R_CALL:
				if k := jsHelper(ldr.SymName(rs)); k >= 0 {
					// (Made only for a program that calls them,
					// after the others: the index is written once
					// it is known.)
					jsCalls = append(jsCalls, gcJSCall{i, wfn.Len(), k})
					writeUleb128FixedLength(wfn, 0, 5)
					break
				}
				if ldr.SymName(rs) == wasm.GCHop {
					writeSleb128(wfn, hopIndex[sigOf(fn)])
					break
				}
				if h, ok := l.splitHelperIndex(ldr.SymName(rs)); ok && splitMode() {
					writeSleb128(wfn, int64(len(hostImports)+len(ctxt.Textp)+h))
					break
				}
				if h, ok := l.helperIndex(ldr.SymName(rs)); ok && !splitMode() {
					writeSleb128(wfn, int64(len(hostImports)+len(ctxt.Textp)+h))
					break
				}
				if !ldr.SymType(rs).IsText() {
					ldr.Errorf(fn, "call of %s, which is not defined", ldr.SymName(rs))
				}
				writeSleb128(wfn, funcIndex(rs))
			case objabi.R_WASMIMPORT:
				writeSleb128(wfn, hostImportMap[rs])
			case objabi.R_WASMTYPE:
				if r.Siz() == 4 { // where the instruction is in the function (wasm.GCPlace)
					writeSleb128(wfn, int64(wfn.Len()))
					break
				}
				if r.Siz() == 3 { // the layout of a struct, for the header of an object
					writeSleb128(wfn, l.layout(ldr.SymName(rs)))
					break
				}
				if r.Siz() == 2 { // a heap type
					writeSleb128(wfn, int64(l.typeIndex(ldr.SymName(rs))))
					break
				}
				writeUleb128(wfn, uint64(l.typeIndex(ldr.SymName(rs))))
			case objabi.R_WASMGLOBAL:
				writeUleb128(wfn, uint64(l.globalIndex(rs)))
			default:
				ldr.Errorf(fn, "bad reloc type %d (%s)", r.Type(), sym.RelocName(ctxt.Arch, r.Type()))
			}
			grown = append(grown, gcGrowth{off, int32(wfn.Len()) - off})
		}
		wfn.Write(P[off:])
		if typ < 0 {
			ldr.Errorf(fn, "GOWASM=gc: function without a WebAssembly type")
			typ = 0
		}
		fns[i] = &wasmFunc{Name: name, Type: uint32(typ), Code: wfn.Bytes()}
		if !ctxt.FlagS {
			fns[i].Lines = gcLines(lines, grown)
		}
	}

	// The start function, which creates the objects, and the
	// functions that resolve pointers. The start function comes
	// first: it may add layouts.
	var init *wasmFunc
	var ctors [][]byte
	var splitWords []uint32
	var splitEnd, splitWordsAddr int64
	if splitMode() {
		v, _ := dataSects.Load(ctxt)
		sects, _ := v.([]wasmDataSect)
		splitEnd = int64(gcDataEnd(ldr, sects))
		splitWords = l.splitDataSlots(sects, uint64(splitEnd))
		splitWordsAddr = (splitEnd + 7) &^ 7
		init = &wasmFunc{Name: "go.initdata", Type: l.typeIndex(wasm.GCFuncPrefix + "."), Code: l.splitInit(splitWordsAddr, int64(len(splitWords)))}
	} else {
		init = &wasmFunc{Name: "go.initglobals", Type: l.typeIndex(wasm.GCFuncPrefix + "."), Code: l.initGlobals()}
		ctors = l.constructors()
	}
	for _, fn := range ctxt.Textp { // (in the order of the functions, which a build keeps)
		sig := sigOf(fn)
		if imp := hops[sig]; imp != nil {
			dot := strings.LastIndexByte(sig, '.')
			imp.Type = l.typeIndex(sig[:dot] + "l" + sig[dot:])
			delete(hops, sig)
		}
	}
	if splitMode() {
		first := len(hostImports) + len(fns)
		l.helperFirst = first
		helpers := l.splitHelpers(first, splitEnd)
		fns = append(fns, helpers...)
		// (wasm.reflectcall and wasm.makefunc follow: see
		// splitHelperIndex.)
		fns = append(fns, l.reflectFuncs(first+len(helpers), len(hostImports), funcIndex)...)
	} else {
		fns = append(fns, l.helpers(len(hostImports)+len(fns), ctors, len(hostImports), funcIndex)...)
	}
	if jsCalls != nil {
		first := len(hostImports) + len(fns)
		fns = append(fns, l.jsHelpers()...)
		for _, c := range jsCalls {
			var b bytes.Buffer
			writeUleb128FixedLength(&b, uint64(first+c.helper), 5)
			copy(fns[c.fn].Code[c.off:], b.Bytes())
		}
	}
	start := len(hostImports) + len(fns)
	fns = append(fns, init)

	entry := loader.Sym(0)
	for v := 0; v < 2 && entry == 0; v++ {
		entry = ldr.Lookup("runtime.rt0gc", v)
	}
	if entry == 0 {
		ctxt.Errorf(0, "GOWASM=gc: runtime.rt0gc not defined")
	}
	// The function the host calls: initialize the packages, then run
	// the program.
	// TODO: on a goroutine, by the runtime.
	var body bytes.Buffer
	writeUleb128(&body, 0) // no locals
	for _, fn := range append(ld.WasmGCInitFuncs(ctxt), entry) {
		body.WriteByte(0x10) // call
		writeUleb128(&body, uint64(funcIndex(fn)))
	}
	body.WriteByte(0x0b)
	run := len(hostImports) + len(fns)
	fns = append(fns, &wasmFunc{Name: "go.run", Type: l.typeIndex(wasm.GCFuncPrefix + "."), Code: body.Bytes()})

	out := ctxt.Out
	out.Write([]byte{0x00, 0x61, 0x73, 0x6d}) // magic
	out.Write([]byte{0x01, 0x00, 0x00, 0x00}) // version
	if len(buildid) != 0 {
		writeBuildID(ctxt, buildid)
	}
	section := func(id uint8, body func(w *bytes.Buffer)) {
		var b bytes.Buffer
		body(&b)
		sizeOffset := writeSecHeader(ctxt, id)
		out.Write(b.Bytes())
		writeSecSize(ctxt, sizeOffset)
	}

	section(sectionType, func(w *bytes.Buffer) {
		if splitMode() {
			// (The first two are one recursion group.)
			writeUleb128(w, uint64(len(l.types)-1))
			l.writeSplitTypes(w)
			for _, name := range l.types[2:] {
				l.writeType(w, name)
			}
			return
		}
		writeUleb128(w, uint64(len(l.types)))
		for _, name := range l.types {
			l.writeType(w, name)
		}
	})
	section(sectionImport, func(w *bytes.Buffer) {
		writeUleb128(w, uint64(len(hostImports)))
		for _, fn := range hostImports {
			writeName(w, fn.Module)
			writeName(w, fn.Name)
			w.WriteByte(0x00) // func import
			writeUleb128(w, uint64(fn.Type))
		}
	})
	writeFunctionSec(ctxt, fns)
	writeTableSec(ctxt, fns)

	// Linear memory, and its data.
	v, _ := dataSects.LoadAndDelete(ctxt)
	sects, _ := v.([]wasmDataSect)
	// The number of each type's constructor, in its descriptor.
	for typ, n := range l.ctorOf {
		addr := uint64(ldr.SymValue(typ)) + gcTypeGCDataOff
		for _, ds := range sects {
			if ds.sect.Vaddr <= addr && addr+8 <= ds.sect.Vaddr+uint64(len(ds.data)) {
				binary.LittleEndian.PutUint64(ds.data[addr-ds.sect.Vaddr:], uint64(n))
			}
		}
	}
	end := gcDataEnd(ldr, sects)
	var layouts []byte
	if splitMode() {
		// The list of the words of data that are pointers, which the
		// start function reads, follows the data.
		end = uint64(splitWordsAddr) + 4*uint64(len(splitWords))
	} else {
		layouts = l.layoutTable()
	}
	if len(layouts) > 0 {
		end = uint64(l.layoutBase) + uint64(len(layouts))
	}
	const wasmPageSize = 64 << 10
	// The stack follows the data.
	stackTop := (end+wasmPageSize-1)&^(wasmPageSize-1) + wasm.GCStackSize
	section(sectionMemory, func(w *bytes.Buffer) {
		writeUleb128(w, 1) // number of memories
		w.WriteByte(0x00)  // no maximum memory size
		writeUleb128(w, stackTop/wasmPageSize)
	})
	section(sectionTag, func(w *bytes.Buffer) {
		writeUleb128(w, 1) // number of tags: wasm.GCTagRecover
		w.WriteByte(0x00)  // an exception
		writeUleb128(w, uint64(l.typeIndex(wasm.GCFuncPrefix+".")))
	})
	section(sectionGlobal, func(w *bytes.Buffer) {
		if splitMode() {
			l.splitGlobals(w, stackTop, uint64(splitEnd))
			return
		}
		writeUleb128(w, uint64(wasm.GCGlobals+len(l.globals)))
		for i := 0; i < wasm.GCGlobals+len(l.globals); i++ {
			if i == wasm.GCGlobalSP || i == wasm.GCGlobalID || i == wasm.GCGlobalStackLo || i == wasm.GCGlobalPanicLine || i == wasm.GCGlobalDefer {
				w.Write([]byte{wasm.GCTypeI32, 0x01})
				switch i {
				case wasm.GCGlobalSP:
					writeI32Const(w, int32(stackTop))
				case wasm.GCGlobalStackLo:
					writeI32Const(w, int32(stackTop-wasm.GCStackSize+wasm.GCStackRedZone))
				default:
					writeI32Const(w, 0)
				}
				w.WriteByte(0x0b)
				continue
			}
			if t, ok := l.globalType[uint32(i)]; ok {
				// The object's own type: no cast where it is used.
				w.WriteByte(0x63) // ref null
				writeSleb128(w, int64(t))
				w.WriteByte(0x01)           // mutable
				w.Write([]byte{0xD0, 0x71}) // ref.null none
				w.WriteByte(0x0b)
				continue
			}
			w.WriteByte(wasm.GCTypeEqRef)
			w.WriteByte(0x01) // mutable
			gcRefNull(w)
			w.WriteByte(0x0b) // end
		}
	})
	// What the host calls: to free a block of linear memory whose
	// owner was collected, and to run goroutines (see
	// runtime/wasmgc/proc.go).
	type export struct {
		name string
		fn   loader.Sym
	}
	var exports []export
	for _, e := range []export{{name: "free"}, {name: "sched"}, {name: "gorun"}, {name: "unstart"}, {name: "deadlock"}, {name: "timerwait"}, {name: "cleanup"}, {name: "callback"}, {name: "callbackdone"}} {
		for v := 0; v < 2 && e.fn == 0; v++ {
			e.fn = ldr.Lookup(gcExports[e.name], v)
		}
		if e.fn != 0 && ldr.AttrReachable(e.fn) {
			exports = append(exports, e)
		}
	}
	// The functions of //go:wasmexport, by the names they give.
	for _, fn := range ldr.WasmExports {
		exports = append(exports, export{name: ldr.SymName(fn), fn: fn})
	}
	section(sectionExport, func(w *bytes.Buffer) {
		n := 5 + len(exports)
		if splitMode() {
			n += 3
		}
		writeUleb128(w, uint64(n))
		if splitMode() {
			// For the host: the array of a pointer (see
			// gcSplitSlotsOf), and a pointer of its parts.
			for _, e := range [][2]string{{"slotsof", gcSplitSlotsOf}, {"mkptr", gcSplitMkPtr}, {"setptr", gcSplitSetPtr}} {
				writeName(w, e[0])
				w.WriteByte(0x00)
				i, _ := l.splitHelperIndex(e[1])
				writeUleb128(w, uint64(len(hostImports)+len(ctxt.Textp)+i))
			}
		}
		for _, e := range exports {
			writeName(w, e.name)
			w.WriteByte(0x00) // func export
			writeUleb128(w, uint64(funcIndex(e.fn)))
		}
		writeName(w, "run")
		w.WriteByte(0x00) // func export
		writeUleb128(w, uint64(run))
		writeName(w, "mem")
		w.WriteByte(0x02) // mem export
		writeUleb128(w, 0)
		// (The functions, where a func value says: for wasm.GCHop.)
		writeName(w, "table")
		w.WriteByte(0x01) // table export
		writeUleb128(w, 0)
		writeName(w, "panicline")
		w.WriteByte(0x03) // global export
		writeUleb128(w, wasm.GCGlobalPanicLine)
		writeName(w, "deferframe")
		w.WriteByte(0x03) // global export
		writeUleb128(w, wasm.GCGlobalDefer)
	})
	section(sectionStart, func(w *bytes.Buffer) {
		writeUleb128(w, uint64(start))
	})
	// (All functions: MakeFunc calls the stubs the linker generates.)
	writeElementSec(ctxt, uint64(len(hostImports)), uint64(len(fns)))
	// (Memory is zero to begin with: what is zero in the data is not
	// written, where it is long enough to be worth a segment.)
	var chunks []gcDataChunk
	for _, ds := range sects {
		chunks = gcDataChunks(chunks, int64(ds.sect.Vaddr), ds.data)
	}
	nseg := 1 + len(chunks)
	if len(layouts) > 0 || len(splitWords) > 0 {
		nseg++
	}
	section(sectionDataCount, func(w *bytes.Buffer) {
		writeUleb128(w, uint64(nseg))
	})
	// (As writeCodeSec, noting where each function is.)
	starts := make([]int64, len(fns))
	{
		sizeOffset := writeSecHeader(ctxt, sectionCode)
		writeUleb128(ctxt.Out, uint64(len(fns)))
		for i, fn := range fns {
			writeUleb128(ctxt.Out, uint64(len(fn.Code)))
			starts[i] = ctxt.Out.Offset()
			ctxt.Out.Write(fn.Code)
		}
		writeSecSize(ctxt, sizeOffset)
	}
	section(sectionData, func(w *bytes.Buffer) {
		writeUleb128(w, uint64(nseg))
		// Segment 0 is passive: the data of the arrays.
		w.WriteByte(0x01)
		writeUleb128(w, uint64(l.passive.Len()))
		w.Write(l.passive.Bytes())
		for _, c := range chunks {
			writeUleb128(w, 0) // active, memory 0
			writeI32Const(w, int32(c.addr))
			w.WriteByte(0x0b) // end
			writeUleb128(w, uint64(len(c.data)))
			w.Write(c.data)
		}
		if len(splitWords) > 0 {
			writeUleb128(w, 0) // active, memory 0
			writeI32Const(w, int32(splitWordsAddr))
			w.WriteByte(0x0b) // end
			writeUleb128(w, uint64(4*len(splitWords)))
			for _, word := range splitWords {
				w.Write(binary.LittleEndian.AppendUint32(nil, word))
			}
		}
		if len(layouts) > 0 {
			writeUleb128(w, 0) // active, memory 0
			writeI32Const(w, int32(l.layoutBase))
			w.WriteByte(0x0b) // end
			writeUleb128(w, uint64(len(layouts)))
			w.Write(layouts)
		}
	})
	writeProducerSec(ctxt)
	// The names of the functions are kept with -s: they are what
	// runtime.FuncForPC and runtime.CallersFrames report (as the pc-value
	// table is kept on other ports), and package testing finds its
	// frames by name.
	writeNameSec(ctxt, len(hostImports), fns)
	if !ctxt.FlagS {
		writeLinesSec(ctxt, len(hostImports), fns, starts)
	}
}

// gcDataEnd returns the end of the program's data in linear memory:
// that of sects and of the variables without pointers that start as
// zeros, which are there too, with nothing to initialize.
func gcDataEnd(ldr *loader.Loader, sects []wasmDataSect) uint64 {
	end := uint64(0)
	for _, ds := range sects {
		end = max(end, ds.sect.Vaddr+ds.sect.Length)
	}
	for _, name := range []string{"runtime.noptrbss", "runtime.bss"} {
		if s := ldr.Lookup(name, 0); s != 0 {
			if sect := ldr.SymSect(s); sect != nil {
				end = max(end, sect.Vaddr+sect.Length)
			}
		}
	}
	return end
}

// A gcGrowth is how much longer the code of a function is, from an
// offset in the code the compiler wrote, for the indexes the linker
// wrote there.
type gcGrowth struct {
	off, by int32
}

// A gcLine is a frame of where, in the code of a function, the code of
// a line starts: the frames of one place follow each other, the
// innermost first, when calls were inlined there.
type gcLine struct {
	off  int32
	file string
	line int32
	name string // the function inlined, or "" for the function itself
}

// gcLines reads the table of lines the compiler left after the code of
// a function (see wasm.GCLines in cmd/internal/obj/wasm).
func gcLines(tab []byte, grown []gcGrowth) []gcLine {
	var lines []gcLine
	var off int32
	var file string
	g := 0
	uleb := func() uint64 {
		v, n := binary.Uvarint(tab)
		tab = tab[n:]
		return v
	}
	str := func(n uint64) string {
		s := string(tab[:n-1])
		tab = tab[n-1:]
		return s
	}
	for len(tab) > 0 {
		off += int32(uleb())
		for g < len(grown) && grown[g].off <= off {
			g++
		}
		at := off
		if g > 0 {
			at += grown[g-1].by
		}
		for k := len(lines); k > 0 && lines[k-1].off == at; k-- {
			lines = lines[:k-1] // (no code for it)
		}
		for n := uleb(); n > 0; n-- {
			l := gcLine{off: at, line: int32(uleb())}
			if f := uleb(); f > 0 {
				file = str(f)
			}
			l.file = file
			if f := uleb(); f > 0 {
				l.name = str(f)
			}
			lines = append(lines, l)
		}
	}
	return lines
}

// writeLinesSec writes the custom section "go.lines", from which the
// host tells runtime.Caller and the like the file and line of a place
// in the code (see lib/wasm/wasm_gc.js). It is omitted with -s. In it
// are the names of the files, the names of the functions inlined, then
// each function that has lines: its index, where its code is in the
// module, and its frames (see gcLine): the offset since the last, the
// file, the line since the last, and 0 or 1 + the function inlined.
func writeLinesSec(ctxt *ld.Link, nimport int, fns []*wasmFunc, starts []int64) {
	sizeOffset := writeSecHeader(ctxt, sectionCustom)
	writeName(ctxt.Out, "go.lines")
	files, funcs := make(map[string]int), make(map[string]int)
	var fileNames, funcNames []string
	nfn := 0
	for _, fn := range fns {
		if len(fn.Lines) > 0 {
			nfn++
		}
		for _, l := range fn.Lines {
			if _, ok := files[l.file]; !ok {
				files[l.file] = len(fileNames)
				fileNames = append(fileNames, l.file)
			}
			if _, ok := funcs[l.name]; !ok && l.name != "" {
				funcs[l.name] = len(funcNames)
				funcNames = append(funcNames, l.name)
			}
		}
	}
	writeUleb128(ctxt.Out, uint64(len(fileNames)))
	for _, name := range fileNames {
		if root := buildcfg.GOROOT; root != "" && strings.HasPrefix(name, "$GOROOT/") {
			name = root + name[len("$GOROOT"):]
		}
		writeName(ctxt.Out, name)
	}
	writeUleb128(ctxt.Out, uint64(len(funcNames)))
	for _, name := range funcNames {
		writeName(ctxt.Out, name)
	}
	writeUleb128(ctxt.Out, uint64(nfn))
	for i, fn := range fns {
		if len(fn.Lines) == 0 {
			continue
		}
		writeUleb128(ctxt.Out, uint64(nimport+i))
		writeUleb128(ctxt.Out, uint64(starts[i]))
		writeUleb128(ctxt.Out, uint64(len(fn.Lines)))
		var off, line int32
		for _, l := range fn.Lines {
			writeUleb128(ctxt.Out, uint64(l.off-off))
			writeUleb128(ctxt.Out, uint64(files[l.file]))
			writeSleb128(ctxt.Out, int64(l.line-line))
			if l.name == "" {
				writeUleb128(ctxt.Out, 0)
			} else {
				writeUleb128(ctxt.Out, uint64(funcs[l.name]+1))
			}
			off, line = l.off, l.line
		}
	}
	writeSecSize(ctxt, sizeOffset)
}

var gcSpaceRegexp = regexp.MustCompile(`\s`)
