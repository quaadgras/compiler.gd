// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wasm

import (
	"bytes"
	"cmd/internal/obj"
	"cmd/internal/objabi"
	"cmd/internal/src"
	"slices"
)

// With GOWASM=gc, Go objects are WebAssembly structs and arrays in the
// engine's garbage-collected heap (see doc/gd/wasm-gc.md), and a Go
// function is a WebAssembly function of its own type.
//
// A WebAssembly type is referred to by a symbol whose name spells the
// type out, so that the compiler needs no table of types and the linker
// can define every type it sees a reference to:
//
//	wasm.struct.<fields>	the struct of a Go value: a header and the fields
//	wasm.array.<elem>	an array of the element the letter stands for
//	wasm.func.<params>.<results>
//
// The letters are b, h, w and l for integers of 8, 16, 32 and 64 bits,
// f and d for floats of 32 and 64 bits, and r for a reference (eqref).
// An array can also be of o: references to the objects that are its
// elements, when a Go element is more than one scalar. Every field and
// element is mutable.
//
// The fields of a struct are separated by commas. Each is a letter and
// the offset of the scalar in the Go value, as in "r0,l8,l16" for a
// slice; or, for a nested array, which is an object of its own,
// A<offset>:<size of an element>:<number of elements><letter>. The
// size of the Go value follows a slash, as in "r0,l8,l16/24". Go's
// layout is thus part of the name: it is what a pointer into the
// object is resolved with (see the linker). Two names that differ only
// in the offsets are the same WebAssembly type.
//
// The header of a struct is two 32-bit fields: the layout, which is the
// index of the type the object was created as, and the identity of the
// object, assigned when it is first converted to an integer.
//
// A pointer is an eqref. It is one of:
//   - an object: a pointer to the whole of it, cast to the type the code
//     expects where it is used;
//   - a GCBox: a pointer into an object, at a byte offset within its Go
//     value;
//   - a GCLinear: an address in linear memory, where slices whose
//     elements have no pointers live, and the object that owns the
//     memory, which is freed when the owner is collected;
//   - an i31: the address of static data in linear memory;
//   - null.
const (
	GCStructPrefix = "wasm.struct."
	GCArrayPrefix  = "wasm.array."
	GCFuncPrefix   = "wasm.func."

	// GCHeader is the supertype of the structs of Go values: the
	// header alone.
	GCHeader = "wasm.header"

	// GCHeaderFields is the number of fields before the first Go
	// field of a struct.
	GCHeaderFields = 2

	// GCBox is the struct of a pointer into an object: the object
	// and the offset in bytes. It has no header.
	GCBox       = "wasm.box"
	GCBoxObject = 0
	GCBoxOffset = 1

	// GCGeneric is the struct of a value of a struct type made at run
	// time (reflect.StructOf), whose layout no type of the module has:
	// the header, the size of the Go value, and, by 8-byte word, the
	// pointer in it (if it holds one) and its bits (for a pointer, its
	// identity, as wasm.ptrid gives it). Only the accessors reach it.
	GCGeneric      = "wasm.generic"
	GCGenericSize  = 2
	GCGenericRefs  = 3
	GCGenericWords = 4

	// GCLinear is the struct of a pointer to linear memory: the
	// address and the owner (null if this is the owner). It has no
	// header.
	GCLinear      = "wasm.linear"
	GCLinearAddr  = 0
	GCLinearOwner = 1

	// The functions the linker generates: to load and store a scalar
	// of the kind the letter stands for at a byte offset from any
	// pointer, and to add a byte offset to any pointer.
	GCLoadPrefix  = "wasm.load."
	GCStorePrefix = "wasm.store."
	GCAddPtr      = "wasm.addptr"

	// GCAddPtrStruct, in the offset passed to GCAddPtr, says that the
	// pointer made is to a struct, not into an array nested at the same
	// address.
	GCAddPtrStruct = 1 << 30

	// GCClone copies an object that holds a Go value: its fields
	// and the arrays nested in it.
	GCClone = "wasm.clone"
	// GCBits returns the bytes at a byte offset from any pointer, as
	// an integer: whatever the scalar there is, to the end of it.
	GCBits = "wasm.bits"
	// GCNew returns a new zeroed object of the Go type the linker
	// numbered as its argument: the number is in the GCData word of
	// the type's descriptor.
	GCNew = "wasm.new"
	// GCPtrEq reports whether two pointers are equal: point to the
	// same place, whatever kinds of pointer they are.
	GCPtrEq = "wasm.ptreq"
	// GCClear zeroes the Go value in an object.
	GCClear = "wasm.clear"
	// GCReflectCall calls a function of the func type whose descriptor
	// is at the address given; GCMakeFunc returns the code of the
	// function of that type that reflect.MakeFunc makes. See the
	// linker.
	GCReflectCall = "wasm.reflectcall"
	GCMakeFunc    = "wasm.makefunc"
	// GCPtrID converts any pointer to an integer: the address of one
	// to linear memory, the identity of an object.
	GCPtrID = "wasm.ptrid"
	// GCCopy copies the Go value in one object to another of the
	// same type: its arguments are the source and the destination.
	GCCopy = "wasm.copy"

	// GCGlobalCtxt is the global (an eqref) in which a call of a
	// closure passes the closure to the function.
	GCGlobalCtxt = 0
	// GCGlobalSP is the global (an i32) that is the top of the stack
	// in linear memory, which grows down. The variables whose address
	// is taken and that have no pointers live there; the compiler
	// only leaves a variable on the stack if no pointer to it
	// outlives the function.
	GCGlobalSP = 1
	// GCGlobalID is the global (an i32) that counts the identities
	// given to objects.
	GCGlobalID = 2
	// GCGlobalStackLo is the global (an i32) below which a frame on
	// the stack in linear memory must not go: the bottom of the
	// segment of the stack in use, plus GCStackRedZone. A function
	// whose frame would asks the runtime for another segment (see
	// runtime/wasmgc/stack.go).
	GCGlobalStackLo = 3
	// GCGlobalKeep is the global (an eqref) that runtime.KeepAlive
	// stores its argument in: a use that the engine cannot drop, so
	// the object is not collected before.
	GCGlobalKeep = 4
	// GCGlobals is the number of globals before those of the data
	// symbols.
	GCGlobals = 5

	// GCStackRedZone is the room below GCGlobalStackLo that the
	// runtime switches segments in.
	GCStackRedZone = 4 << 10

	// GCTagRecover is the tag of the exception thrown to unwind to
	// the function whose deferred call recovered from a panic.
	GCTagRecover = 0

	// GCStackSize is the size of the first segment of the stack in
	// linear memory of the goroutine that runs main.
	GCStackSize = 1 << 20
)

// The WebAssembly value types of locals and signatures.
const (
	GCTypeI32    = 0x7F
	GCTypeI64    = 0x7E
	GCTypeF32    = 0x7D
	GCTypeF64    = 0x7C
	GCTypeEqRef  = 0x6D // (ref null eq)
	GCHeapEq     = 0x6D
	GCRefNonNull = 0x64 // (ref ht)
	GCRefNull    = 0x63 // (ref null ht)
)

// GCValueType returns the value type that the letter k of a signature
// stands for.
func GCValueType(k byte) byte {
	switch k {
	case 'i':
		return GCTypeI32
	case 'l':
		return GCTypeI64
	case 'f':
		return GCTypeF32
	case 'd':
		return GCTypeF64
	case 'r':
		return GCTypeEqRef
	}
	panic("bad wasm value type " + string(k))
}

// GCImportRefs reports whether the functions imported from module take
// pointers as references, rather than as addresses in linear memory.
func GCImportRefs(module string) bool { return module == "gd" }

// GCTypeSym returns the symbol that names a WebAssembly type.
func GCTypeSym(ctxt *obj.Link, name string) *obj.LSym {
	return ctxt.Lookup(name)
}

func gcTypeAddr(p *obj.Prog) *obj.LSym {
	if p.To.Sym == nil {
		panic("wasm: " + p.As.String() + " without a type")
	}
	return p.To.Sym
}

// preprocessGC prepares a function compiled with GOWASM=gc. Such a
// function has no frame in linear memory and is never re-entered, so
// all there is to do is to resolve its branches.
func preprocessGC(ctxt *obj.Link, s *obj.LSym, newprog obj.ProgAlloc) {
	if wi := s.Func().WasmImport; wi != nil {
		// The wrapper of an imported function: convert the
		// arguments, call, convert the result.
		wi.CreateAuxSym()
		p := s.Func().Text
		add := func(as obj.As) *obj.Prog {
			p = obj.Appendp(p, newprog)
			p.As = as
			return p
		}
		// A pointer is passed to the runtime's own imports (module
		// gd) as the reference it is, and to any other as the
		// address in linear memory it points to; a uintptr, as an
		// address.
		sig := s.Func().WasmGC.Sig.Name[len(GCFuncPrefix):]
		for i, f := range wi.Params {
			add(ALocalGet).From = constAddr(int64(i))
			switch f.Type {
			case obj.WasmI32, obj.WasmBool:
				add(AI32WrapI64)
			case obj.WasmPtr:
				if sig[i] != 'r' {
					add(AI32WrapI64)
				} else if !GCImportRefs(wi.Module) {
					p := add(ACall)
					p.To = obj.Addr{Type: obj.TYPE_MEM, Name: obj.NAME_EXTERN, Sym: ctxt.Lookup(GCPtrID)}
					add(AI32WrapI64)
				}
			}
		}
		call := add(ACall)
		call.To = obj.Addr{Type: obj.TYPE_MEM, Name: obj.NAME_EXTERN, Sym: s}
		call.Mark = WasmImport
		for _, f := range wi.Results {
			switch f.Type {
			case obj.WasmI32, obj.WasmBool:
				add(AI64ExtendI32U) // TODO: sign
			case obj.WasmPtr:
				if !GCImportRefs(wi.Module) {
					ctxt.Diag("%v: GOWASM=gc: an imported function cannot return a pointer", s)
				}
			}
		}
		return
	}
	if s.Func().WasmGC.Trap {
		text := s.Func().Text
		text.Link = nil
		p := obj.Appendp(text, newprog)
		p.As = obj.AUNDEF
		return
	}
	depth := 0
	depths := make(map[*obj.Prog]int)
	for p := s.Func().Text; p != nil; p = p.Link {
		p.Pc = 0 // there are no program counters
		switch p.As {
		case obj.ARET:
			p.As = AReturn
			p.To = obj.Addr{}
		case obj.ACALL, ACALLNORESUME:
			p.As = ACall
		case ABlock, ALoop, AIf, ATRYCATCH, ARefBlock:
			depth++
			depths[p] = depth
		case AEnd:
			depth--
		case ABr, ABrIf, ABrOnCast, ABrOnCastFail, ABrOnNonNull:
			if p.To.Type == obj.TYPE_BRANCH {
				d, ok := depths[p.To.Val.(*obj.Prog)]
				if !ok {
					panic("wasm: label not at block")
				}
				p.To = constAddr(int64(depth - d))
			}
		}
	}
}

// gcTypeRef writes a reference to the type sym names, as an index that
// the linker fills in.
func gcTypeRef(ctxt *obj.Link, s *obj.LSym, w *bytes.Buffer, sym *obj.LSym) {
	s.AddRel(ctxt, obj.Reloc{
		Type: objabi.R_WASMTYPE,
		Off:  int32(w.Len()),
		Siz:  1, // actually variable sized
		Sym:  sym,
	})
}

// gcHeapRef is gcTypeRef where the index is written as a heap type or
// a constant: signed.
func gcHeapRef(ctxt *obj.Link, s *obj.LSym, w *bytes.Buffer, sym *obj.LSym) {
	s.AddRel(ctxt, obj.Reloc{
		Type: objabi.R_WASMTYPE,
		Off:  int32(w.Len()),
		Siz:  2, // actually variable sized
		Sym:  sym,
	})
}

// assembleGC assembles p if it is one of the instructions of GOWASM=gc,
// and reports whether it was.
func assembleGC(ctxt *obj.Link, s *obj.LSym, w *bytes.Buffer, p *obj.Prog) bool {
	fb := func(op byte) {
		w.WriteByte(0xFB)
		w.WriteByte(op)
	}
	typed := func(op byte) {
		fb(op)
		gcTypeRef(ctxt, s, w, gcTypeAddr(p))
	}
	heap := func(op byte) {
		fb(op)
		gcHeapRef(ctxt, s, w, gcTypeAddr(p))
	}
	switch p.As {
	case AStructNew:
		typed(0x00)
	case AStructNewDefault:
		typed(0x01)
	case AStructGet, AStructGetS, AStructGetU, AStructSet:
		typed(map[obj.As]byte{AStructGet: 0x02, AStructGetS: 0x03, AStructGetU: 0x04, AStructSet: 0x05}[p.As])
		writeUleb128(w, uint64(p.To.Offset))
	case AArrayNew:
		typed(0x06)
	case AArrayNewDefault:
		typed(0x07)
	case AArrayNewFixed:
		typed(0x08)
		writeUleb128(w, uint64(p.To.Offset))
	case AArrayGet:
		typed(0x0B)
	case AArrayGetS:
		typed(0x0C)
	case AArrayGetU:
		typed(0x0D)
	case AArraySet:
		typed(0x0E)
	case AArrayLen:
		fb(0x0F)
	case AArrayFill:
		typed(0x10)
	case AArrayCopy:
		typed(0x11)
		gcTypeRef(ctxt, s, w, gcTypeAddr(p))
	case ARefTest:
		heap(0x14)
	case ARefCast:
		heap(0x16)
	case ARefCastNull:
		if p.From.Offset != 0 {
			fb(0x17)
			w.WriteByte(map[int64]byte{1: 0x6C, 2: GCHeapEq}[p.From.Offset]) // i31 or eq
			break
		}
		heap(0x17)
	case ATypeConst:
		w.WriteByte(0x41) // i32.const
		gcHeapRef(ctxt, s, w, gcTypeAddr(p))
	case ARefNull:
		w.WriteByte(0xD0)
		w.WriteByte(GCHeapEq)
	case ARefIsNull:
		w.WriteByte(0xD1)
	case ARefEq:
		w.WriteByte(0xD3)
	case ARefAsNonNull:
		w.WriteByte(0xD4)
	case ARefI31:
		fb(0x1C)
	case AI31GetS:
		fb(0x1D)
	case AI31GetU:
		fb(0x1E)
	case ABrOnCast, ABrOnCastFail:
		if p.As == ABrOnCast {
			fb(0x18)
		} else {
			fb(0x19)
		}
		w.WriteByte(0x01) // from a nullable reference to a non-null one
		writeUleb128(w, uint64(p.To.Offset))
		w.WriteByte(GCHeapEq)
		if p.From.Sym == nil {
			w.WriteByte(0x6C) // i31
			break
		}
		gcHeapRef(ctxt, s, w, p.From.Sym)
	case ARefBlock:
		w.WriteByte(0x02) // block
		w.WriteByte(GCRefNonNull)
		if p.To.Sym == nil {
			w.WriteByte(0x6C) // i31
			break
		}
		gcHeapRef(ctxt, s, w, gcTypeAddr(p))
	case ABrOnNonNull:
		w.WriteByte(0xD6)
		writeUleb128(w, uint64(p.To.Offset))
	case ARefNullNone:
		w.WriteByte(0xD0)
		w.WriteByte(0x71) // none
	case ARefSelect:
		w.WriteByte(0x1C) // select t*
		writeUleb128(w, 1)
		w.WriteByte(GCTypeEqRef)
	case ARefGlobalGet, ARefGlobalSet:
		if p.As == ARefGlobalGet {
			w.WriteByte(0x23)
		} else {
			w.WriteByte(0x24)
		}
		s.AddRel(ctxt, obj.Reloc{
			Type: objabi.R_WASMGLOBAL,
			Off:  int32(w.Len()),
			Siz:  1, // actually variable sized
			Sym:  gcTypeAddr(p),
		})
	case ALocalGet, ALocalSet, ALocalTee, AGlobalGet, AGlobalSet:
		writeOpcode(w, p.As)
		writeUleb128(w, uint64(p.From.Offset))
	case ACallIndirect:
		writeOpcode(w, p.As)
		gcTypeRef(ctxt, s, w, gcTypeAddr(p))
		w.WriteByte(0x00) // table 0
	case AReturn:
		writeOpcode(w, p.As)
	default:
		return false
	}
	return true
}

// GCLines names the relocation that marks, in a function of GOWASM=gc,
// the end of its code: after it is the table of the lines of the code,
// from which the linker makes the module's (see gcLineTable).
const GCLines = "wasm.lines"

// A gcLineTable is the table of the files and lines of a function's
// code: an entry where they change, as the offset in the code since
// the last entry (unsigned LEB128) and the frames there, the innermost
// first: more than one where calls were inlined. A frame is its line,
// its file (0 for the same as the last written, or else 1 + the length
// of its name and the name), and the function inlined (0 for none: the
// function itself, the outermost frame; or else its name likewise).
type gcLineTable struct {
	buf    bytes.Buffer
	files  []string
	off    int
	last   src.XPos
	frames []gcLineFrame // the last written
	file   string
}

type gcLineFrame struct {
	file, name string
	line       int32
}

func (t *gcLineTable) add(ctxt *obj.Link, off int, p *obj.Prog) {
	if !p.Pos.IsKnown() || p.Pos == t.last {
		return
	}
	if t.files == nil {
		t.files = ctxt.PosTable.FileTable()
	}
	var frames []gcLineFrame
	at := func(xpos src.XPos, name string) {
		pos := ctxt.InnermostPos(xpos)
		f := gcLineFrame{name: name, line: int32(pos.RelLine())}
		if i := pos.FileIndex(); i >= 0 && i < len(t.files) {
			f.file = t.files[i]
		}
		frames = append(frames, f)
	}
	xpos := p.Pos
	for ix := ctxt.InnermostPos(xpos).Base().InliningIndex(); ix >= 0; ix = ctxt.InlTree.Parent(ix) {
		at(xpos, ctxt.InlTree.InlinedFunction(ix).Name)
		xpos = ctxt.InlTree.CallPos(ix)
	}
	at(xpos, "")
	t.last = p.Pos
	if slices.Equal(frames, t.frames) {
		return // the same lines
	}
	t.frames = frames
	writeUleb128(&t.buf, uint64(off-t.off))
	t.off = off
	writeUleb128(&t.buf, uint64(len(frames)))
	str := func(s string) {
		writeUleb128(&t.buf, uint64(len(s)+1))
		t.buf.WriteString(s)
	}
	for _, f := range frames {
		writeUleb128(&t.buf, uint64(f.line))
		if f.file == t.file {
			writeUleb128(&t.buf, 0)
		} else {
			str(f.file)
			t.file = f.file
		}
		if f.name == "" {
			writeUleb128(&t.buf, 0)
		} else {
			str(f.name)
		}
	}
}

func (t *gcLineTable) write(ctxt *obj.Link, s *obj.LSym, w *bytes.Buffer) {
	s.AddRel(ctxt, obj.Reloc{
		Type: objabi.R_WASMTYPE,
		Off:  int32(w.Len()),
		Siz:  0,
		Sym:  ctxt.Lookup(GCLines),
	})
	w.Write(t.buf.Bytes())
}
