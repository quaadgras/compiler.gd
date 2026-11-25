// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wasm

import (
	"bytes"
	"cmd/internal/obj"
	"cmd/internal/objabi"
	"cmd/internal/src"
	"internal/buildcfg"
	"slices"
	"strings"
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
// address in linear memory of the description of the Go layout the
// object was created with (the linker writes one for each: the fields
// by offset), and the identity of the object, assigned when it is first
// converted to an integer.
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
	// GCMove copies the bytes of a Go value from one place to another:
	// its arguments are the destination, the source, both any pointers,
	// and the size. It reports whether it did: it does if both are
	// within structs. GCZero zeroes the bytes of one likewise.
	GCMove = "wasm.move"
	GCZero = "wasm.zero"

	// GCHop is what a function calls, with its own arguments and then
	// itself (as a func value is: an i64), when its frame does not fit
	// in what is left of the segment of the stack in linear memory: an
	// import from the host, of the function's type (the linker makes
	// one for each type that needs it), which calls the function on a
	// new stack of the engine's and returns its results. The function
	// has made a new segment of the stack first (runtime.morestackgc),
	// so the call finds room; it frees it afterwards. The engine's
	// stacks (the host makes one for each goroutine: JSPI) do not grow,
	// and a browser's are small.
	GCHop = "wasm.hop"

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
	// GCGlobalPanicLine is the global (an i32) that holds the line of
	// the last panic the compiler inserted (a nil dereference, an
	// index out of range): set before the call of the runtime's
	// function, it is exact where the engine's position for the frame
	// may not be (V8's, when it has inlined that function). The host
	// reads it: it is exported as "panicline".
	GCGlobalPanicLine = 5
	// GCGlobalDefer is the global (an i32) that holds the address of
	// the record of the innermost running function that defers calls,
	// in its frame in linear memory (0 if there is none): where the
	// record of the function that defers around it is (GCDeferPrev),
	// where in the function the call it is making is (GCDeferSite, as
	// ALinePlace gives it), and the function, as a func value has it
	// (GCDeferFunc). Such a function's code is in a try block, and V8's
	// optimizing compiler gives, for a frame that is at a call inside
	// one, the position of the last call before the block: the host
	// takes the place from the record instead. It is exported as
	// "deferframe"; the runtime keeps it by goroutine.
	GCGlobalDefer = 6
	// GCGlobals is the number of globals before those of the data
	// symbols.
	GCGlobals = 7

	GCDeferPrev = 0
	GCDeferSite = 4
	GCDeferFunc = 8
	GCDeferSize = 16

	// With GOWASM=gc,split (stage 15 of doc/gd/wasm-gc.md) the bytes
	// of every Go value are in linear memory, pointer words included
	// (the address, as an integer), and the pointers themselves are in
	// one array of references per allocation (GCSlots), one element
	// per pointer word of the allocation (4 bytes with wasm32, 8 with
	// GOWASM=gc,ptr64: see GCWordShift): the pointer at byte b of it is
	// element b/PtrSize. A pointer is a GCPtr: the address it points to, the
	// array of the allocation it points into (null if there is none)
	// and the offset in bytes of the address from where element 0 of
	// the array is. These two are the only types of Go values: the
	// linker makes them types 0 and 1, one recursion group, and 'r' in
	// a signature or a local is (ref null GCPtr).
	// GCJSBox and GCJSUnbox are the functions the linker generates
	// for the imports of module gojs (syscall/js) with GOWASM=gc: a
	// JavaScript value (an externref) is held in an object of type
	// GCExtern, which Go has as a *syscall/js.externref. GCJSBox (x
	// externref) r makes one (nil for null), GCJSUnbox (p) externref
	// gives the value back (null for nil). With split, GCExtern is a
	// subtype of GCPtr with the externref after its fields, and with
	// an address of GCExternAddr: not 0, which would be nil, and not
	// one that anything is at.
	GCJSBox      = "wasm.jsbox"
	GCJSUnbox    = "wasm.jsunbox"
	GCExtern     = "wasm.extern"
	GCExternAddr = 1

	// (The address of a GCPtr, its offset, and every address the
	// code computes are i32, GCTypeAddr: linear memory is of 32 bits
	// whatever the size of a pointer word. A memory64 build would
	// change GCTypeAddr and what is written with it.)
	GCPtr      = "wasm.ptr"
	GCPtrAddr  = 0
	GCPtrSlots = 1
	GCPtrOff   = 2
	// GCPtrLim is the size in bytes of what the pointer points into,
	// from where element 0 of the array is: the block, the variable of
	// a frame, the data. A load or a store through the pointer is of
	// bytes from GCPtrOff on that end at GCPtrLim or before it (see
	// GCSplitCheck). It is 0 for a pointer that was made of an integer:
	// nothing is loaded or stored through that one.
	GCPtrLim = 3
	GCSlots  = "wasm.slots"

	// The functions the linker generates for split. See
	// cmd/link/internal/wasm/gcsplit.go.
	GCSplitLoadPtr  = "wasm.loadptr"  // (p, rel i32) ptr: the pointer at byte rel from p
	GCSplitStorePtr = "wasm.storeptr" // (p, rel i32, v ptr): store v there
	GCSplitAddPtr   = "wasm.addptr"   // (p, rel i32) ptr: p + rel
	GCSplitPtrAddr  = "wasm.ptraddr"  // (p) i32: the address, 0 for nil
	GCSplitIntToPtr = "wasm.inttoptr" // (i64) ptr: the pointer of an address
	GCSplitMove     = "wasm.smove"    // (dst, drel i32, src, srel i32, n i32): copy n bytes and their pointers
	GCSplitZero     = "wasm.szero"    // (p, rel i32, n i32): zero n bytes and their pointers
	GCSplitNew      = "wasm.snew"     // (addr i32, nslots i32, size i32) ptr: a pointer to a new block of size bytes
	GCSplitStrLen   = "wasm.strlen"   // (a, b i64) i64: the length of the string of wasm32 whose A and B these are (with 4-byte words only)

	// What code that is not the runtime's loads and stores through a
	// pointer is in what the pointer points into: GCSplitCheck (p, rel
	// i32, n i32) i32 returns the address of the n bytes at rel from
	// p, and traps if they are not all between GCPtrOff - GCPtrOff
	// and GCPtrLim. GCSplitCheck1 and the others are that for 1, 2, 4
	// and 8 bytes. The functions above have a form that asks the same
	// (GCSplitChecked), and whose pointer made of an integer is one
	// with no size, which nothing is loaded through.
	GCSplitCheck  = "wasm.chk"
	GCSplitCheck1 = "wasm.chk1"
	GCSplitCheck2 = "wasm.chk2"
	GCSplitCheck4 = "wasm.chk4"
	GCSplitCheck8 = "wasm.chk8"

	// GCGlobalSlots is the global (with split) that holds the array of
	// the program's data, which covers all of it from address 0, and
	// GCGlobalStatic the one that holds a GCPtr to address 0 with that
	// array: a pointer to data is that plus the address.
	GCGlobalSlots  = 7
	GCGlobalStatic = 8
	// GCSplitGlobals is GCGlobals with split.
	GCSplitGlobals = 9

	// GCStackRatio is how much of the engine's stack a function may
	// take for each byte of its frame in linear memory, and
	// GCEngineFrame what a frame of the engine's is taken to be beyond
	// a slot for each parameter and variable (see padFrame in the
	// compiler). The segments of the stack in linear memory are 64 KB:
	// the frames of one take no more than 512 KB of the engine's stack.
	GCStackRatio  = 8
	GCEngineFrame = 128

	// GCStackRedZone is the room below GCGlobalStackLo that the
	// runtime switches segments in.
	GCStackRedZone = 4 << 10

	// GCTagRecover is the tag of the exception thrown to unwind to
	// the function whose deferred call recovered from a panic.
	GCTagRecover = 0

	// GCStackSize is the size of the first segment of the stack in
	// linear memory of the goroutine that runs main: as the segments
	// of the others (see GCStackRatio).
	GCStackSize = 64 << 10
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

// The comparisons of integers, each with the one that is true when it
// is not.
var gcNot = map[obj.As]obj.As{
	AI32Eq: AI32Ne, AI32Ne: AI32Eq, AI32LtS: AI32GeS, AI32GeS: AI32LtS, AI32LtU: AI32GeU, AI32GeU: AI32LtU,
	AI32GtS: AI32LeS, AI32LeS: AI32GtS, AI32GtU: AI32LeU, AI32LeU: AI32GtU,
	AI64Eq: AI64Ne, AI64Ne: AI64Eq, AI64LtS: AI64GeS, AI64GeS: AI64LtS, AI64LtU: AI64GeU, AI64GeU: AI64LtU,
	AI64GtS: AI64LeS, AI64LeS: AI64GtS, AI64GtU: AI64LeU, AI64LeU: AI64GtU,
}

// gcPeephole shortens the code of s where the compiler set a variable
// and read it at once: the value is left on the stack if that is the
// only place the variable is read (and it is a number: a reference
// read from a variable has the variable's type, which what follows may
// count on), and the variable is set as the value passes otherwise
// (local.tee). And where it negated a comparison of integers, which is
// another comparison, or narrowed a constant.
func gcPeephole(s *obj.LSym) {
	for p := s.Func().Text; p != nil; p = p.Link {
		q := p.Link
		if q == nil {
			break
		}
		switch {
		case q.As == AI32Eqz && gcNot[p.As] != 0:
			p.As = gcNot[p.As]
			p.Link = q.Link
		case p.As == AI64Const && q.As == AI32WrapI64 && p.From.Type == obj.TYPE_CONST && p.From.Sym == nil:
			p.As = AI32Const
			p.From.Offset = int64(int32(p.From.Offset))
			p.Link = q.Link
		}
	}
	fn := s.Func()
	sig := fn.WasmGC.Sig.Name[len(GCFuncPrefix):]
	nparam := int64(strings.IndexByte(sig, '.'))
	number := func(n int64) bool {
		if n < nparam {
			return sig[n] != 'r'
		}
		switch fn.WasmGC.Locals[n-nparam] {
		case GCTypeI32, GCTypeI64, GCTypeF32, GCTypeF64:
			return true
		}
		return false
	}
	gets := make(map[int64]int)
	for p := fn.Text; p != nil; p = p.Link {
		if p.As == ALocalGet {
			gets[p.From.Offset]++
		}
	}
	var prev *obj.Prog
	for p := fn.Text; p != nil; prev, p = p, p.Link {
		q := p.Link
		if p.As != ALocalSet || q == nil || q.As != ALocalGet || p.From.Offset != q.From.Offset {
			continue
		}
		if n := p.From.Offset; gets[n] == 1 && number(n) && prev != nil {
			prev.Link = q.Link
			p = prev
		} else {
			p.As = ALocalTee
			p.Link = q.Link
		}
	}
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
			case obj.WasmExtern:
				p := add(ACall)
				p.To = obj.Addr{Type: obj.TYPE_MEM, Name: obj.NAME_EXTERN, Sym: ctxt.Lookup(GCJSUnbox)}
			case obj.WasmPtr:
				if sig[i] != 'r' {
					add(AI32WrapI64)
				} else if !GCImportRefs(wi.Module) && buildcfg.GOWASM.Split {
					p := add(ACall)
					p.To = obj.Addr{Type: obj.TYPE_MEM, Name: obj.NAME_EXTERN, Sym: ctxt.Lookup(GCSplitPtrAddr)}
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
			case obj.WasmI32:
				if wi.Module == GojsModule {
					add(AI64ExtendI32S) // (gojs: int and int32)
				} else {
					add(AI64ExtendI32U) // TODO: sign
				}
			case obj.WasmBool:
				add(AI64ExtendI32U)
			case obj.WasmExtern:
				p := add(ACall)
				p.To = obj.Addr{Type: obj.TYPE_MEM, Name: obj.NAME_EXTERN, Sym: ctxt.Lookup(GCJSBox)}
			case obj.WasmPtr:
				if !GCImportRefs(wi.Module) {
					ctxt.Diag("%v: GOWASM=gc: an imported function cannot return a pointer", s)
				}
			}
		}
		return
	}
	if we := s.Func().WasmExport; we != nil {
		// The wrapper of an exported function (see setupWasmExportGC
		// in the compiler): convert the arguments to registers, call,
		// convert the results.
		we.CreateAuxSym()
		p := s.Func().Text
		p.Link = nil
		add := func(as obj.As) *obj.Prog {
			p = obj.Appendp(p, newprog)
			p.As = as
			return p
		}
		for i, f := range we.Params {
			add(ALocalGet).From = constAddr(int64(i))
			switch f.Type {
			case obj.WasmI32, obj.WasmBool:
				if f.Offset == 1 {
					add(AI64ExtendI32S)
				} else {
					add(AI64ExtendI32U)
				}
			}
		}
		call := add(ACall)
		call.To = obj.Addr{Type: obj.TYPE_MEM, Name: obj.NAME_EXTERN, Sym: we.WrappedSym}
		for _, f := range we.Results {
			switch f.Type {
			case obj.WasmI32, obj.WasmBool:
				add(AI32WrapI64)
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
	gcPeephole(s)
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

// gcLayoutRef writes a reference to the layout of the struct sym names:
// what the header of an object of it holds, a constant that the linker
// fills in (the address of the layout's description; see the linker).
func gcLayoutRef(ctxt *obj.Link, s *obj.LSym, w *bytes.Buffer, sym *obj.LSym) {
	s.AddRel(ctxt, obj.Reloc{
		Type: objabi.R_WASMTYPE,
		Off:  int32(w.Len()),
		Siz:  3, // actually variable sized
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
		gcLayoutRef(ctxt, s, w, gcTypeAddr(p))
	case ALinePlace:
		w.WriteByte(0x41) // i32.const
		s.AddRel(ctxt, obj.Reloc{
			Type: objabi.R_WASMTYPE,
			Off:  int32(w.Len()),
			Siz:  4, // actually variable sized
			Sym:  ctxt.Lookup(GCPlace),
		})
	case ARefNull:
		w.WriteByte(0xD0)
		if buildcfg.GOWASM.Split {
			w.WriteByte(0x71) // none: a null of any reference type
			break
		}
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
		if p.From.Offset == 1 {
			// (ref null To.Sym)
			w.WriteByte(GCRefNull)
			gcHeapRef(ctxt, s, w, gcTypeAddr(p))
			break
		}
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
		if buildcfg.GOWASM.Split {
			w.WriteByte(GCRefNull)
			gcHeapRef(ctxt, s, w, ctxt.Lookup(GCPtr))
			break
		}
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

// GCPlace names the relocation of ALinePlace: the linker, which knows
// where the indexes it writes make the code longer, writes the offset
// of the instruction in the function.
const GCPlace = "wasm.place"

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

// GCSplitChecked returns the name of the form of the function name
// (GCSplitLoadPtr, GCSplitStorePtr, GCSplitIntToPtr, GCSplitMove or
// GCSplitZero) that code which is not the runtime's calls.
func GCSplitChecked(name string) string { return name + "c" }

// GCTypeAddr is the WebAssembly type of an address in linear memory,
// of the fields GCPtrAddr and GCPtrOff of a GCPtr, and of an address
// that the code computes.
const GCTypeAddr = GCTypeI32

// GCWordShift returns log2 of ptrSize, the size in bytes of a pointer
// word (sys.Arch.PtrSize: 4 for wasm32, 8 with GOWASM=gc,ptr64). With
// split, the pointer at byte b of a block is element b>>GCWordShift of
// the block's array (GCSlots).
func GCWordShift(ptrSize int) int64 {
	switch ptrSize {
	case 4:
		return 2
	case 8:
		return 3
	}
	panic("wasm: no pointer word of this size")
}
