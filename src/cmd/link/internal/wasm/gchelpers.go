// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wasm

import (
	"bytes"
	"cmd/internal/obj/wasm"
	"cmd/link/internal/loader"
	"strings"
)

// The functions that resolve a pointer at run time (GOWASM=gc).
//
// Compiled code reads and writes through a pointer on the assumption
// that it is what it most likely is: for a *T, an object of T; for the
// elements of a slice with no pointers, linear memory. When the
// pointer turns out to be something else (see the kinds of pointer in
// cmd/internal/obj/wasm/gc.go), it calls one of the functions here,
// with the pointer and the byte offset of the scalar in the Go value
// the pointer points to. They know every layout in the program: an
// object's header says which is its own.

const gcKinds = "bhwlfdr"

// gcSize is the function that returns the size of the Go value of an
// object: of a struct by its layout, of an array by its length and
// elements. Pointer arithmetic into an array of objects needs it.
const gcSize = "wasm.size"

// gcStoreBits is the function that stores the n low bytes of an i64 at
// a byte offset from any pointer, into whatever scalars are there.
const gcStoreBits = "wasm.storebits"

// helperIndex returns the index, among the functions the linker
// generates, of the one named.
func (l *gcLinker) helperIndex(name string) (int, bool) {
	switch {
	case strings.HasPrefix(name, wasm.GCLoadPrefix):
		return strings.IndexByte(gcKinds, name[len(wasm.GCLoadPrefix)]), true
	case strings.HasPrefix(name, wasm.GCStorePrefix):
		return len(gcKinds) + strings.IndexByte(gcKinds, name[len(wasm.GCStorePrefix)]), true
	case name == wasm.GCAddPtr:
		return 2 * len(gcKinds), true
	case name == wasm.GCClone:
		return 2*len(gcKinds) + 1, true
	case name == wasm.GCBits:
		return 2*len(gcKinds) + 2, true
	case name == wasm.GCPtrID:
		return 2*len(gcKinds) + 3, true
	case name == wasm.GCCopy:
		return 2*len(gcKinds) + 4, true
	case name == wasm.GCNew:
		return 2*len(gcKinds) + 5, true
	case name == gcSize:
		return 2*len(gcKinds) + 6, true
	case name == gcStoreBits:
		return 2*len(gcKinds) + 7, true
	case name == wasm.GCPtrEq:
		return 2*len(gcKinds) + 8, true
	case name == wasm.GCClear:
		return 2*len(gcKinds) + 9, true
	case name == wasm.GCReflectCall:
		return 2*len(gcKinds) + 10, true
	case name == wasm.GCMakeFunc:
		return 2*len(gcKinds) + 11, true
	}
	return 0, false
}

// gcValue returns the letter of the WebAssembly type that holds a
// scalar of kind k.
func gcValue(k byte) byte {
	switch k {
	case 'b', 'h', 'w':
		return 'l'
	}
	return k
}

func gcLog2(k byte) int32 {
	switch gcKindSize(k) {
	case 1:
		return 0
	case 2:
		return 1
	case 4:
		return 2
	}
	return 3
}

// A gcAsm writes the body of a function.
type gcAsm struct {
	bytes.Buffer
	l *gcLinker
}

func (a *gcAsm) op(b ...byte)              { a.Write(b) }
func (a *gcAsm) get(i uint32)              { a.WriteByte(0x20); writeUleb128(a, uint64(i)) }
func (a *gcAsm) set(i uint32)              { a.WriteByte(0x21); writeUleb128(a, uint64(i)) }
func (a *gcAsm) i32(v int64)               { writeI32Const(a, int32(v)) }
func (a *gcAsm) br(depth int)              { a.WriteByte(0x0C); writeUleb128(a, uint64(depth)) }
func (a *gcAsm) gc(op byte, idx ...uint32) { gcOp(&a.Buffer, op, idx...) }
func (a *gcAsm) typ(name string) uint32    { return a.l.typeIndex(name) }

// cast casts the reference pushed to a non-null reference to the type.
func (a *gcAsm) cast(t uint32) {
	a.op(0xFB, 0x16)
	writeSleb128(a, int64(t))
}

// isA opens a block that computes, with the instructions do writes,
// what follows if local 0 is of the type t (or, if t < 0, an i31), and
// is skipped otherwise. do starts with the reference cast pushed, and
// must not fall through.
func (a *gcAsm) isA(t int64, do func()) {
	a.op(0x02, wasm.GCTypeEqRef) // block (result eqref)
	a.get(0)
	a.op(0xFB, 0x19, 0x01, 0x00, wasm.GCHeapEq) // br_on_cast_fail 0 eq
	if t < 0 {
		a.op(0x6C)
	} else {
		writeSleb128(a, t)
	}
	do()
	a.op(0x0B, 0x1A) // end, drop
}

// memory writes a load (store if store) of a scalar of kind k at the
// address pushed.
// addrToRef converts the address pushed, an i64, to a pointer: 0 is nil.
// tmp is an i32 local it may use.
func (a *gcAsm) addrToRef(tmp uint32) {
	a.op(0xA7) // i32.wrap_i64
	a.op(0x22) // local.tee
	writeUleb128(a, uint64(tmp))
	a.op(0xFB, 0x1C)                   // ref.i31
	a.op(0xD0, wasm.GCHeapEq)          // ref.null eq
	a.get(tmp)                         //
	a.op(0x1C, 0x01, wasm.GCTypeEqRef) // select eqref
}

// memory writes a load (store if store) of a scalar of kind k at the
// address pushed. tmp is an i32 local it may use.
func (a *gcAsm) memory(k byte, store bool, tmp uint32) {
	load := map[byte]byte{'b': 0x31, 'h': 0x33, 'w': 0x35, 'l': 0x29, 'f': 0x2A, 'd': 0x2B, 'r': 0x29}
	stor := map[byte]byte{'b': 0x3C, 'h': 0x3D, 'w': 0x3E, 'l': 0x37, 'f': 0x38, 'd': 0x39, 'r': 0x37}
	if store {
		a.get(2)
		if k == 'r' {
			// Only an address in linear memory can be stored
			// there, or nil.
			a.op(0x10) // call
			writeUleb128(a, uint64(a.l.ptridFunc))
		}
		a.op(stor[k], 0x00, 0x00)
		return
	}
	a.op(load[k], 0x00, 0x00)
	if k == 'r' {
		a.addrToRef(tmp)
	}
}

// field writes a load (store if store) of field f of the struct t
// pushed, a scalar of kind k.
func (a *gcAsm) field(t, f uint32, k byte, store bool) {
	narrow := k == 'b' || k == 'h' || k == 'w'
	if store {
		a.get(2)
		if narrow {
			a.op(0xA7) // i32.wrap_i64
		}
		a.gc(0x05, t, f)
		return
	}
	if k == 'b' || k == 'h' {
		a.gc(0x04, t, f) // struct.get_u
	} else {
		a.gc(0x02, t, f)
	}
	if narrow {
		a.op(0xAD) // i64.extend_i32_u
	}
}

// element is field for the element of the array, of the type of
// arrays of kind k, and the index pushed.
func (a *gcAsm) element(k byte, store bool) {
	t := a.typ(wasm.GCArrayPrefix + string(k))
	narrow := k == 'b' || k == 'h' || k == 'w'
	if store {
		a.get(2)
		if narrow {
			a.op(0xA7)
		}
		a.gc(0x0E, t)
		return
	}
	if k == 'b' || k == 'h' {
		a.gc(0x0D, t) // array.get_u
	} else {
		a.gc(0x0B, t)
	}
	if narrow {
		a.op(0xAD)
	}
}

// access returns the body of the function that loads (stores, if
// store) a scalar of kind k at a byte offset from a pointer.
//
//	local 0: the pointer; local 1: the offset;
//	local 2: the value to store; the last local: scratch.
func (l *gcLinker) access(k byte, store bool, layouts []string, bits, size, storeBits, ptrid int) []byte {
	a := &gcAsm{l: l}
	// An integer can be loaded from where there is another scalar:
	// part of a wider one, the bits of a float, the identity of the
	// object a pointer points to.
	pun := k != 'r'
	// punned loads or stores the bytes there whatever they are: those of
	// a wider scalar, or of several (two bools compared as a uint16).
	punned := func() {
		a.get(0)
		a.get(1)
		if store {
			// The bits of the value, into whatever is there.
			a.get(2)
			switch k {
			case 'f':
				a.op(0xBC, 0xAD) // i32.reinterpret_f32, i64.extend_i32_u
			case 'd':
				a.op(0xBD) // i64.reinterpret_f64
			}
			a.i32(gcKindSize(k))
			a.op(0x10) // call
			writeUleb128(a, uint64(storeBits))
			a.op(0x0F)
			return
		}
		a.i32(gcKindSize(k))
		a.op(0x10) // call
		writeUleb128(a, uint64(bits))
		if k != 'l' && k != 'd' {
			writeI64Const(a, int64(1)<<(8*uint(gcKindSize(k)))-1)
			a.op(0x83) // i64.and
		}
		switch k {
		case 'f':
			a.op(0xA7, 0xBE) // i32.wrap_i64, f32.reinterpret_i32
		case 'd':
			a.op(0xBF) // f64.reinterpret_i64
		}
		a.op(0x0F)
	}
	tmp := uint32(2)
	if store {
		tmp = 3
	}
	sz := tmp + 1
	shift, ref := sz+1, sz+2
	done := func() { a.op(0x0F) } // return
	writeUleb128(a, 4)
	writeUleb128(a, 1)
	a.op(wasm.GCTypeEqRef)
	writeUleb128(a, 1)
	a.op(wasm.GCTypeI32)
	writeUleb128(a, 1)
	a.op(wasm.GCTypeI64)
	writeUleb128(a, 1)
	a.op(wasm.GCTypeEqRef)

	linear, box := a.typ(wasm.GCLinear), a.typ(wasm.GCBox)
	a.op(0x03, 0x40) // loop: again, with the object a box or an element is in
	// Linear memory.
	a.isA(int64(linear), func() {
		a.gc(0x02, linear, wasm.GCLinearAddr)
		a.get(1)
		a.op(0x6A) // i32.add
		a.memory(k, store, sz)
		done()
	})
	a.isA(-1, func() {
		a.op(0xFB, 0x1E) // i31.get_u
		a.get(1)
		a.op(0x6A)
		a.memory(k, store, sz)
		done()
	})
	// A pointer into an object.
	a.isA(int64(box), func() {
		a.set(tmp)
		a.get(tmp)
		a.cast(box)
		a.gc(0x02, box, wasm.GCBoxOffset)
		a.get(1)
		a.op(0x6A)
		a.set(1)
		a.get(tmp)
		a.cast(box)
		a.gc(0x02, box, wasm.GCBoxObject)
		a.set(0)
		a.br(1)
	})
	// A struct of a type made at run time.
	if l.generic() {
		a.isA(int64(a.typ(wasm.GCGeneric)), func() {
			a.set(tmp)
			a.genericAccess(k, store, tmp, sz, shift, ref)
		})
	}
	// An array of objects: go on in the element, whose size that of
	// the first says.
	objs := a.typ(wasm.GCArrayPrefix + "o")
	a.isA(int64(objs), func() {
		a.set(tmp)
		a.get(tmp)
		a.cast(objs)
		a.i32(0)
		a.gc(0x0B, objs)
		a.op(0xFB, 0x16, wasm.GCHeapEq) // ref.cast eq
		a.op(0x10)                      // call
		writeUleb128(a, uint64(size))
		a.set(sz)
		a.get(tmp)
		a.cast(objs)
		a.get(1)
		a.get(sz)
		a.op(0x6E) // i32.div_u
		a.gc(0x0B, objs)
		a.op(0xFB, 0x16, wasm.GCHeapEq)
		a.set(0)
		a.get(1)
		a.get(sz)
		a.op(0x70) // i32.rem_u
		a.set(1)
		a.br(1)
	})
	// An array of such scalars.
	a.isA(int64(a.typ(wasm.GCArrayPrefix+string(k))), func() {
		a.get(1)
		a.i32(int64(gcLog2(k)))
		a.op(0x76) // i32.shr_u
		a.element(k, store)
		done()
	})
	if pun {
		// An array of other scalars.
		a.get(0)
		a.op(0xFB, 0x14)
		writeSleb128(a, int64(a.typ(wasm.GCHeader))) // ref.test
		a.op(0x45, 0x04, 0x40)                       // i32.eqz, if
		punned()
		a.op(0x0B)
	}
	// A struct: go by its layout.
	n := len(layouts)
	a.op(0x02, 0x40) // block: no such layout
	for range layouts {
		a.op(0x02, 0x40)
	}
	a.get(0)
	a.cast(a.typ(wasm.GCHeader))
	a.gc(0x02, a.typ(wasm.GCHeader), 0)
	index := make(map[string]int)
	for j, name := range layouts {
		index[name] = j
	}
	a.op(0x0E) // br_table
	writeUleb128(a, uint64(len(l.types)))
	for _, name := range l.types {
		if j, ok := index[name]; ok {
			writeUleb128(a, uint64(j))
		} else {
			writeUleb128(a, uint64(n))
		}
	}
	writeUleb128(a, uint64(n))
	for j, name := range layouts {
		a.op(0x0B) // end: the layout
		t := a.typ(name)
		for i, f := range gcParseStruct(name) {
			field := uint32(wasm.GCHeaderFields + i)
			self := func() {
				a.get(0)
				a.cast(t)
			}
			rel := func() { // the offset within the field
				a.get(1)
				a.i32(f.off)
				a.op(0x6B) // i32.sub
			}
			switch {
			case f.letter == k:
				a.get(1)
				a.i32(f.off)
				a.op(0x46, 0x04, 0x40) // i32.eq, if
				self()
				a.field(t, field, k, store)
				done()
				a.op(0x0B)
			case f.letter == 'l' && k == 'r':
				// A pointer kept as an integer, as the type
				// word of an interface is: the address of a
				// type descriptor or an itab.
				a.get(1)
				a.i32(f.off)
				a.op(0x46, 0x04, 0x40) // i32.eq, if
				self()
				if store {
					a.get(2)
					a.op(0x10) // call
					writeUleb128(a, uint64(ptrid))
					a.gc(0x05, t, field)
				} else {
					a.gc(0x02, t, field)
					a.addrToRef(sz)
				}
				done()
				a.op(0x0B)
			case f.letter == 'A' && (f.elem == k || f.elem == 'o'):
				arr := a.typ(wasm.GCArrayPrefix + string(f.elem))
				rel()
				a.i32(f.esize * f.n)
				a.op(0x49, 0x04, 0x40) // i32.lt_u, if
				self()
				a.gc(0x02, t, field)
				a.cast(arr)
				rel()
				a.i32(f.esize)
				a.op(0x6E) // i32.div_u
				if f.elem == k {
					a.element(k, store)
					done()
				} else {
					// An element that is an object: go on in it.
					a.gc(0x0B, arr)
					a.op(0xFB, 0x16, wasm.GCHeapEq) // ref.cast eq
					a.set(tmp)
					rel()
					a.i32(f.esize)
					a.op(0x70) // i32.rem_u
					a.set(1)
					a.get(tmp)
					a.set(0)
					a.br(n - j + 1)
				}
				a.op(0x0B)
			}
		}
		if pun {
			punned()
		} else {
			a.op(0x00) // unreachable: no such scalar there
		}
	}
	a.op(0x0B, 0x00) // end; unreachable: not a struct of the program
	a.op(0x0B, 0x00) // end of the loop
	a.op(0x0B)
	return a.Bytes()
}

// addPtr returns the body of the function that adds a byte offset to a
// pointer.
func (l *gcLinker) addPtr(layouts []string) []byte {
	a := &gcAsm{l: l}
	writeUleb128(a, 2)
	writeUleb128(a, 1)
	a.op(wasm.GCTypeEqRef)
	writeUleb128(a, 1)
	a.op(wasm.GCTypeI32)
	linear, box := a.typ(wasm.GCLinear), a.typ(wasm.GCBox)
	// Bit 30 of the offset says that the pointer is to a struct, not
	// into an array nested where it starts (wasm.GCAddPtrStruct).
	a.get(1)
	a.i32(wasm.GCAddPtrStruct)
	a.op(0x71) // i32.and
	a.set(3)
	a.get(1)
	a.i32(^wasm.GCAddPtrStruct)
	a.op(0x71)
	a.set(1)
	// Nothing to add: the pointer, which may be nil.
	a.get(1)
	a.op(0x45, 0x04, 0x40) // i32.eqz, if
	a.get(0)
	a.op(0x0F, 0x0B)
	a.isA(int64(linear), func() {
		a.set(2)
		a.get(2)
		a.cast(linear)
		a.gc(0x02, linear, wasm.GCLinearAddr)
		a.get(1)
		a.op(0x6A)
		// The owner: that of the pointer, or the pointer itself.
		a.get(2)
		a.cast(linear)
		a.gc(0x02, linear, wasm.GCLinearOwner)
		a.op(0xD1, 0x04, wasm.GCTypeEqRef) // ref.is_null, if (result eqref)
		a.get(2)
		a.op(0x05) // else
		a.get(2)
		a.cast(linear)
		a.gc(0x02, linear, wasm.GCLinearOwner)
		a.op(0x0B)
		a.gc(0x00, linear)
		a.op(0x0F)
	})
	a.isA(-1, func() {
		a.op(0xFB, 0x1E)
		a.get(1)
		a.op(0x6A, 0xFB, 0x1C, 0x0F) // i32.add, ref.i31, return
	})
	a.isA(int64(box), func() {
		a.set(2)
		a.get(2)
		a.cast(box)
		a.gc(0x02, box, wasm.GCBoxOffset)
		a.get(1)
		a.op(0x6A)
		a.set(1)
		a.get(2)
		a.cast(box)
		a.gc(0x02, box, wasm.GCBoxObject)
		a.set(0)
		a.get(0)
	})
	// A pointer into an array nested in a struct is one into the
	// array, which is an object of its own: so slices of it are.
	hdr := a.typ(wasm.GCHeader)
	var nested []string
	for _, name := range layouts {
		for _, f := range gcParseStruct(name) {
			if f.letter == 'A' {
				nested = append(nested, name)
				break
			}
		}
	}
	a.get(0)
	a.op(0xFB, 0x14)
	writeSleb128(a, int64(hdr)) // ref.test
	a.get(3)
	a.op(0x45, 0x71) // i32.eqz, i32.and
	a.op(0x04, 0x40) // if
	n := len(nested)
	a.op(0x02, 0x40)
	for range nested {
		a.op(0x02, 0x40)
	}
	a.get(0)
	a.cast(hdr)
	a.gc(0x02, hdr, 0)
	index := make(map[string]int)
	for j, name := range nested {
		index[name] = j
	}
	a.op(0x0E)
	writeUleb128(a, uint64(len(l.types)))
	for _, name := range l.types {
		if j, ok := index[name]; ok {
			writeUleb128(a, uint64(j))
		} else {
			writeUleb128(a, uint64(n))
		}
	}
	writeUleb128(a, uint64(n))
	for j, name := range nested {
		a.op(0x0B)
		t := a.typ(name)
		for i, f := range gcParseStruct(name) {
			if f.letter != 'A' {
				continue
			}
			field := uint32(wasm.GCHeaderFields + i)
			a.get(1)
			a.i32(f.off)
			a.op(0x6B) // i32.sub
			a.i32(f.esize * f.n)
			a.op(0x49, 0x04, 0x40) // i32.lt_u, if
			a.get(0)
			a.cast(t)
			a.gc(0x02, t, field)
			a.get(1)
			a.i32(f.off)
			a.op(0x6B)
			a.gc(0x00, box)
			a.op(0x0F, 0x0B)
		}
		a.br(n - j - 1)
	}
	a.op(0x0B) // no nested array there
	a.op(0x0B) // end if
	a.get(0)
	a.get(1)
	a.gc(0x00, box)
	a.op(0x0B)
	return a.Bytes()
}

// clone returns the body of the function that copies an object that
// holds a Go value. self is the index of the function.
//
//	local 0: the object; 1: the same; 2: the copy; 3: a length; 4: an index
func (l *gcLinker) clone(self int, layouts []string) []byte {
	a := &gcAsm{l: l}
	writeUleb128(a, 2)
	writeUleb128(a, 2)
	a.op(wasm.GCTypeEqRef)
	writeUleb128(a, 2)
	a.op(wasm.GCTypeI32)

	// newArray starts the copy of the array of type t pushed.
	newArray := func(t uint32) {
		a.set(1)
		a.get(1)
		a.cast(t)
		a.op(0xFB, 0x0F) // array.len
		a.set(3)
		a.get(3)
		a.gc(0x07, t) // array.new_default
		a.set(2)
	}
	gen := a.typ(wasm.GCGeneric)
	a.isA(int64(gen), func() {
		if !l.generic() {
			a.op(0x00) // unreachable
			return
		}
		a.set(1)
		a.i32(int64(gen))
		a.i32(0)
		a.get(1)
		a.cast(gen)
		a.gc(0x02, gen, wasm.GCGenericSize)
		for _, f := range []uint32{wasm.GCGenericRefs, wasm.GCGenericWords} {
			t := a.typ(wasm.GCArrayPrefix + map[uint32]string{wasm.GCGenericRefs: "r", wasm.GCGenericWords: "l"}[f])
			a.get(1)
			a.cast(gen)
			a.gc(0x02, gen, f)
			a.cast(t)
			a.op(0xFB, 0x0F) // array.len
			a.set(3)
			a.get(3)
			a.gc(0x07, t) // array.new_default
			a.set(2)
			a.get(2)
			a.cast(t)
			a.i32(0)
			a.get(1)
			a.cast(gen)
			a.gc(0x02, gen, f)
			a.cast(t)
			a.i32(0)
			a.get(3)
			a.gc(0x11, t, t) // array.copy
			a.get(2)
		}
		a.gc(0x00, gen) // struct.new
		a.op(0x0F)
	})
	for i := range gcKinds {
		t := a.typ(wasm.GCArrayPrefix + gcKinds[i:i+1])
		a.isA(int64(t), func() {
			newArray(t)
			a.get(2)
			a.cast(t)
			a.i32(0)
			a.get(1)
			a.cast(t)
			a.i32(0)
			a.get(3)
			a.gc(0x11, t, t) // array.copy
			a.get(2)
			a.op(0x0F)
		})
	}
	// An array of objects: copy each.
	objs := a.typ(wasm.GCArrayPrefix + "o")
	a.isA(int64(objs), func() {
		newArray(objs)
		a.op(0x02, 0x40, 0x03, 0x40) // block, loop
		a.get(4)
		a.get(3)
		a.op(0x4F, 0x0D, 0x01) // i32.ge_u, br_if 1
		a.get(2)
		a.cast(objs)
		a.get(4)
		a.get(1)
		a.cast(objs)
		a.get(4)
		a.gc(0x0B, objs)
		a.op(0xFB, 0x17, wasm.GCHeapEq) // ref.cast null eq
		a.op(0x10)                      // call
		writeUleb128(a, uint64(self))
		a.gc(0x0E, objs)
		a.get(4)
		a.i32(1)
		a.op(0x6A)
		a.set(4)
		a.br(0)
		a.op(0x0B, 0x0B)
		a.get(2)
		a.op(0x0F)
	})
	// A struct: by its layout. Anything else is not a Go value that
	// is copied: a pointer is.
	hdr := a.typ(wasm.GCHeader)
	n := len(layouts)
	a.isA(int64(hdr), func() {
		a.op(0x1A)       // drop
		a.op(0x02, 0x40) // block: no such layout
		for range layouts {
			a.op(0x02, 0x40)
		}
		a.get(0)
		a.cast(hdr)
		a.gc(0x02, hdr, 0)
		index := make(map[string]int)
		for j, name := range layouts {
			index[name] = j
		}
		a.op(0x0E) // br_table
		writeUleb128(a, uint64(len(l.types)))
		for _, name := range l.types {
			if j, ok := index[name]; ok {
				writeUleb128(a, uint64(j))
			} else {
				writeUleb128(a, uint64(n))
			}
		}
		writeUleb128(a, uint64(n))
		for _, name := range layouts {
			a.op(0x0B) // end: the layout
			t := a.typ(name)
			a.i32(int64(t))
			a.i32(0)
			for i, f := range gcParseStruct(name) {
				field := uint32(wasm.GCHeaderFields + i)
				a.get(0)
				a.cast(t)
				switch f.letter {
				case 'b', 'h':
					a.gc(0x04, t, field) // struct.get_u
				case 'A':
					a.gc(0x02, t, field)
					a.op(0x10)
					writeUleb128(a, uint64(self))
				default:
					a.gc(0x02, t, field)
				}
			}
			a.gc(0x00, t)
			a.op(0x0F)
		}
		a.op(0x0B, 0x00) // end; unreachable
	})
	a.get(0)
	a.op(0x0B)
	return a.Bytes()
}

// bits returns the body of the function that returns the n bytes at a
// byte offset from a pointer as an integer (n at most 8): those of the
// scalar there, from the offset to its end, and as many of the next as
// are wanted (a wide load where there are narrower scalars: two bools
// compared as a uint16). Padding reads as zeros.
//
//	local 0: the pointer; local 1: the offset; local 2: n;
//	local 3: scratch; locals 4 and 5: the pointer and the offset given;
//	local 6: the bytes found; local 7: their value.
func (l *gcLinker) bits(layouts []string, self int) []byte {
	ptrid := self + 1
	a := &gcAsm{l: l}
	writeUleb128(a, 3)
	writeUleb128(a, 2)
	a.op(wasm.GCTypeEqRef)
	writeUleb128(a, 2)
	a.op(wasm.GCTypeI32)
	writeUleb128(a, 1)
	a.op(wasm.GCTypeI64)
	linear, box := a.typ(wasm.GCLinear), a.typ(wasm.GCBox)
	a.get(0)
	a.set(4)
	a.get(1)
	a.set(5)
	// more returns the value found (local 7, local 6 bytes) and, if
	// more bytes are wanted, those after it.
	more := func() {
		a.get(2)
		a.get(6)
		a.op(0x4A, 0x04, 0x40) // i32.gt_s, if
		a.get(7)
		a.get(4)
		a.get(5)
		a.get(6)
		a.op(0x6A) // i32.add
		a.get(2)
		a.get(6)
		a.op(0x6B) // i32.sub
		a.op(0x10) // call
		writeUleb128(a, uint64(self))
		a.get(6)
		a.i32(3)
		a.op(0x74, 0xAD, 0x86, 0x84, 0x0F) // i32.shl, i64.extend_i32_u, i64.shl, i64.or, return
		a.op(0x0B)
		a.get(7)
		a.op(0x0F)
	}
	// toBits converts the scalar of kind k pushed to an i64, shifts it
	// right by the bytes rel pushes, and returns it with what follows.
	toBits := func(k byte, rel func()) {
		switch k {
		case 'b', 'h', 'w':
			a.op(0xAD) // i64.extend_i32_u
		case 'f':
			a.op(0xBC, 0xAD) // i32.reinterpret_f32
		case 'd':
			a.op(0xBD) // i64.reinterpret_f64
		case 'r':
			a.op(0x10)
			writeUleb128(a, uint64(ptrid))
		}
		rel()
		a.i32(3)
		a.op(0x74, 0xAD, 0x88) // i32.shl, i64.extend_i32_u, i64.shr_u
		a.set(7)
		a.i32(gcKindSize(k))
		rel()
		a.op(0x6B) // i32.sub
		a.set(6)
		more()
	}
	a.op(0x03, 0x40) // loop
	a.isA(int64(linear), func() {
		a.gc(0x02, linear, wasm.GCLinearAddr)
		a.get(1)
		a.op(0x6A, 0x29, 0x00, 0x00, 0x0F) // i32.add, i64.load, return
	})
	a.isA(-1, func() {
		a.op(0xFB, 0x1E)
		a.get(1)
		a.op(0x6A, 0x29, 0x00, 0x00, 0x0F)
	})
	a.isA(int64(box), func() {
		a.set(3)
		a.get(3)
		a.cast(box)
		a.gc(0x02, box, wasm.GCBoxOffset)
		a.get(1)
		a.op(0x6A)
		a.set(1)
		a.get(3)
		a.cast(box)
		a.gc(0x02, box, wasm.GCBoxObject)
		a.set(0)
		a.br(1)
	})
	for i := range gcKinds {
		k := gcKinds[i]
		t := a.typ(wasm.GCArrayPrefix + string(k))
		a.isA(int64(t), func() {
			a.get(1)
			a.i32(int64(gcLog2(k)))
			a.op(0x76) // i32.shr_u
			if k == 'b' || k == 'h' {
				a.gc(0x0D, t)
			} else {
				a.gc(0x0B, t)
			}
			toBits(k, func() {
				a.get(1)
				a.i32(gcKindSize(k) - 1)
				a.op(0x71) // i32.and
			})
		})
	}
	n := len(layouts)
	a.op(0x02, 0x40)
	for range layouts {
		a.op(0x02, 0x40)
	}
	a.get(0)
	a.cast(a.typ(wasm.GCHeader))
	a.gc(0x02, a.typ(wasm.GCHeader), 0)
	index := make(map[string]int)
	for j, name := range layouts {
		index[name] = j
	}
	a.op(0x0E)
	writeUleb128(a, uint64(len(l.types)))
	for _, name := range l.types {
		if j, ok := index[name]; ok {
			writeUleb128(a, uint64(j))
		} else {
			writeUleb128(a, uint64(n))
		}
	}
	writeUleb128(a, uint64(n))
	for j, name := range layouts {
		a.op(0x0B)
		t := a.typ(name)
		for i, f := range gcParseStruct(name) {
			field := uint32(wasm.GCHeaderFields + i)
			rel := func() {
				a.get(1)
				a.i32(f.off)
				a.op(0x6B)
			}
			if f.letter == 'A' {
				// Go on in the array, or in the element that is
				// an object.
				arr := a.typ(wasm.GCArrayPrefix + string(f.elem))
				rel()
				a.i32(f.esize * f.n)
				a.op(0x49, 0x04, 0x40) // i32.lt_u, if
				a.get(0)
				a.cast(t)
				a.gc(0x02, t, field)
				if f.elem == 'o' {
					a.cast(arr)
					rel()
					a.i32(f.esize)
					a.op(0x6E)
					a.gc(0x0B, arr)
					a.op(0xFB, 0x16, wasm.GCHeapEq)
					a.set(3)
					rel()
					a.i32(f.esize)
					a.op(0x70)
					a.set(1)
					a.get(3)
					a.set(0)
				} else {
					a.set(3)
					rel()
					a.set(1)
					a.get(3)
					a.set(0)
				}
				a.br(n - j + 1)
				a.op(0x0B)
				continue
			}
			rel()
			a.i32(gcKindSize(f.letter))
			a.op(0x49, 0x04, 0x40)
			a.get(0)
			a.cast(t)
			if f.letter == 'b' || f.letter == 'h' {
				a.gc(0x04, t, field)
			} else {
				a.gc(0x02, t, field)
			}
			toBits(f.letter, rel)
			a.op(0x0B)
		}
		// Padding: a zero byte.
		writeI64Const(a, 0)
		a.set(7)
		a.i32(1)
		a.set(6)
		more()
	}
	a.op(0x0B, 0x00)
	a.op(0x0B, 0x00)
	a.op(0x0B)
	return a.Bytes()
}

// ptrID returns the body of the function that converts a pointer to an
// integer. self is its index.
func (l *gcLinker) ptrID(self int) []byte {
	a := &gcAsm{l: l}
	writeUleb128(a, 1)
	writeUleb128(a, 1)
	a.op(wasm.GCTypeEqRef)
	linear, box, hdr := a.typ(wasm.GCLinear), a.typ(wasm.GCBox), a.typ(wasm.GCHeader)
	a.get(0)
	a.op(0xD1, 0x04, 0x40) // ref.is_null, if
	writeI64Const(a, 0)
	a.op(0x0F, 0x0B)
	a.isA(-1, func() {
		a.op(0xFB, 0x1E, 0xAD, 0x0F) // i31.get_u, i64.extend_i32_u, return
	})
	a.isA(int64(linear), func() {
		a.gc(0x02, linear, wasm.GCLinearAddr)
		a.op(0xAD, 0x0F)
	})
	a.isA(int64(box), func() {
		// The identity of the object, plus the offset: the identity
		// of an object leaves room for its bytes (see below).
		a.set(1)
		a.get(1)
		a.cast(box)
		a.gc(0x02, box, wasm.GCBoxObject)
		a.op(0x10)
		writeUleb128(a, uint64(self))
		a.get(1)
		a.cast(box)
		a.gc(0x02, box, wasm.GCBoxOffset)
		a.op(0xAD, 0x7C, 0x0F) // i64.extend_i32_u, i64.add, return
	})
	a.isA(int64(hdr), func() {
		// The identity in the header, assigned now if there is none.
		// It is set apart from the addresses of linear memory.
		a.set(1)
		a.get(1)
		a.cast(hdr)
		a.gc(0x02, hdr, 1)
		a.op(0x45, 0x04, 0x40) // i32.eqz, if
		a.op(0x23, wasm.GCGlobalID)
		a.i32(1)
		a.op(0x6A, 0x24, wasm.GCGlobalID) // i32.add, global.set
		a.get(1)
		a.cast(hdr)
		a.op(0x23, wasm.GCGlobalID)
		a.gc(0x05, hdr, 1)
		a.op(0x0B)
		a.get(1)
		a.cast(hdr)
		a.gc(0x02, hdr, 1)
		a.op(0xAD)
		writeI64Const(a, 1<<40)
		a.op(0x84) // i64.or
		writeI64Const(a, 20)
		a.op(0x86, 0x0F) // i64.shl: room for the bytes of the object; return
	})
	// An array: the host gives it an identity, set apart from those
	// above.
	a.get(0)
	a.op(0x10) // call
	writeUleb128(a, uint64(l.objidFunc))
	a.op(0xAD) // i64.extend_i32_u
	writeI64Const(a, 1<<41)
	a.op(0x84) // i64.or
	writeI64Const(a, 20)
	a.op(0x86) // i64.shl
	a.op(0x0B)
	return a.Bytes()
}

// copyObj returns the body of the function that copies the Go value in
// one object to another of the same type. self is its index.
//
//	local 0: the source; 1: the destination; 2: a length; 3: an index
func (l *gcLinker) copyObj(self int, layouts []string) []byte {
	a := &gcAsm{l: l}
	writeUleb128(a, 1)
	writeUleb128(a, 2)
	a.op(wasm.GCTypeI32)
	gen := a.typ(wasm.GCGeneric)
	a.isA(int64(gen), func() {
		if !l.generic() {
			a.op(0x00) // unreachable
			return
		}
		a.op(0x1A) // drop
		for _, f := range []uint32{wasm.GCGenericRefs, wasm.GCGenericWords} {
			t := a.typ(wasm.GCArrayPrefix + map[uint32]string{wasm.GCGenericRefs: "r", wasm.GCGenericWords: "l"}[f])
			a.get(1)
			a.cast(gen)
			a.gc(0x02, gen, f)
			a.cast(t)
			a.i32(0)
			a.get(0)
			a.cast(gen)
			a.gc(0x02, gen, f)
			a.cast(t)
			a.i32(0)
			a.get(0)
			a.cast(gen)
			a.gc(0x02, gen, f)
			a.cast(t)
			a.op(0xFB, 0x0F) // array.len
			a.gc(0x11, t, t) // array.copy
		}
		a.op(0x0F)
	})
	for i := range gcKinds {
		t := a.typ(wasm.GCArrayPrefix + gcKinds[i:i+1])
		a.isA(int64(t), func() {
			a.op(0xFB, 0x0F) // array.len
			a.set(2)
			a.get(1)
			a.cast(t)
			a.i32(0)
			a.get(0)
			a.cast(t)
			a.i32(0)
			a.get(2)
			a.gc(0x11, t, t) // array.copy
			a.op(0x0F)
		})
	}
	objs := a.typ(wasm.GCArrayPrefix + "o")
	a.isA(int64(objs), func() {
		a.op(0xFB, 0x0F)
		a.set(2)
		a.op(0x02, 0x40, 0x03, 0x40) // block, loop
		a.get(3)
		a.get(2)
		a.op(0x4F, 0x0D, 0x01) // i32.ge_u, br_if 1
		for _, arr := range []uint32{0, 1} {
			a.get(arr)
			a.cast(objs)
			a.get(3)
			a.gc(0x0B, objs)
			a.op(0xFB, 0x17, wasm.GCHeapEq)
		}
		a.op(0x10)
		writeUleb128(a, uint64(self))
		a.get(3)
		a.i32(1)
		a.op(0x6A)
		a.set(3)
		a.br(0)
		a.op(0x0B, 0x0B, 0x0F)
	})
	// A struct: by its layout.
	hdr := a.typ(wasm.GCHeader)
	n := len(layouts)
	a.op(0x02, 0x40)
	for range layouts {
		a.op(0x02, 0x40)
	}
	a.get(0)
	a.cast(hdr)
	a.gc(0x02, hdr, 0)
	index := make(map[string]int)
	for j, name := range layouts {
		index[name] = j
	}
	a.op(0x0E)
	writeUleb128(a, uint64(len(l.types)))
	for _, name := range l.types {
		if j, ok := index[name]; ok {
			writeUleb128(a, uint64(j))
		} else {
			writeUleb128(a, uint64(n))
		}
	}
	writeUleb128(a, uint64(n))
	for _, name := range layouts {
		a.op(0x0B)
		t := a.typ(name)
		for i, f := range gcParseStruct(name) {
			field := uint32(wasm.GCHeaderFields + i)
			if f.letter == 'A' {
				for _, obj := range []uint32{0, 1} {
					a.get(obj)
					a.cast(t)
					a.gc(0x02, t, field)
				}
				a.op(0x10)
				writeUleb128(a, uint64(self))
				continue
			}
			a.get(1)
			a.cast(t)
			a.get(0)
			a.cast(t)
			if f.letter == 'b' || f.letter == 'h' {
				a.gc(0x04, t, field)
			} else {
				a.gc(0x02, t, field)
			}
			a.gc(0x05, t, field)
		}
		a.op(0x0F)
	}
	a.op(0x0B, 0x00)
	a.op(0x0B)
	return a.Bytes()
}

// helpers returns the functions the linker generates, in the order of
// helperIndex. first is the index of the first.
func (l *gcLinker) helpers(first int, ctors [][]byte, nimport int, funcIndex func(loader.Sym) int64) []*wasmFunc {
	l.helperFirst = first
	// Every type they use, before the layouts are listed.
	for i := range gcKinds {
		l.typeIndex(wasm.GCArrayPrefix + gcKinds[i:i+1])
	}
	l.typeIndex(wasm.GCArrayPrefix + "o")
	l.typeIndex(wasm.GCBox)
	l.typeIndex(wasm.GCLinear)
	l.typeIndex(wasm.GCGeneric)
	var sigs []string
	for _, store := range []bool{false, true} {
		for i := range gcKinds {
			sig := wasm.GCFuncPrefix + "ri." + string(gcValue(gcKinds[i]))
			if store {
				sig = wasm.GCFuncPrefix + "ri" + string(gcValue(gcKinds[i])) + "."
			}
			l.typeIndex(sig)
			sigs = append(sigs, sig)
		}
	}
	addptr := wasm.GCFuncPrefix + "ri.r"
	l.typeIndex(addptr)
	clone := wasm.GCFuncPrefix + "r.r"
	l.typeIndex(clone)
	bits := wasm.GCFuncPrefix + "rii.l"
	l.typeIndex(bits)
	ptrid := wasm.GCFuncPrefix + "r.l"
	l.typeIndex(ptrid)
	copyObj := wasm.GCFuncPrefix + "rr."
	l.typeIndex(copyObj)
	newSig := wasm.GCFuncPrefix + "i.r"
	l.typeIndex(newSig)
	sizeSig := wasm.GCFuncPrefix + "r.i"
	l.typeIndex(sizeSig)
	storeBitsSig := wasm.GCFuncPrefix + "rili."
	l.typeIndex(storeBitsSig)
	ptrEqSig := wasm.GCFuncPrefix + "rr.i"
	l.typeIndex(ptrEqSig)
	clearSig := wasm.GCFuncPrefix + "r."
	l.typeIndex(clearSig)
	l.typeIndex(wasm.GCHeader)

	var layouts []string
	for _, name := range l.types {
		if strings.HasPrefix(name, wasm.GCStructPrefix) {
			layouts = append(layouts, name)
		}
	}
	l.ptridFunc = first + 2*len(gcKinds) + 3
	var fns []*wasmFunc
	for i, sig := range sigs {
		k, store := gcKinds[i%len(gcKinds)], i >= len(gcKinds)
		name := wasm.GCLoadPrefix
		if store {
			name = wasm.GCStorePrefix
		}
		fns = append(fns, &wasmFunc{Name: name + string(k), Type: l.typeIndex(sig), Code: l.access(k, store, layouts, first+2*len(gcKinds)+2, first+2*len(gcKinds)+6, first+2*len(gcKinds)+7, first+2*len(gcKinds)+3)})
	}
	fns = append(fns, &wasmFunc{Name: wasm.GCAddPtr, Type: l.typeIndex(addptr), Code: l.addPtr(layouts)})
	fns = append(fns, &wasmFunc{Name: wasm.GCClone, Type: l.typeIndex(clone), Code: l.clone(first+len(fns), layouts)})
	fns = append(fns, &wasmFunc{Name: wasm.GCBits, Type: l.typeIndex(bits), Code: l.bits(layouts, first+len(fns))})
	fns = append(fns, &wasmFunc{Name: wasm.GCPtrID, Type: l.typeIndex(ptrid), Code: l.ptrID(first + len(fns))})
	fns = append(fns, &wasmFunc{Name: wasm.GCCopy, Type: l.typeIndex(copyObj), Code: l.copyObj(first+len(fns), layouts)})
	fns = append(fns, &wasmFunc{Name: wasm.GCNew, Type: l.typeIndex(newSig), Code: l.newByNumber(ctors)})
	fns = append(fns, &wasmFunc{Name: gcSize, Type: l.typeIndex(sizeSig), Code: l.sizeOf(first+len(fns), layouts)})
	fns = append(fns, &wasmFunc{Name: gcStoreBits, Type: l.typeIndex(storeBitsSig), Code: l.storeBits(layouts, first+2*len(gcKinds)+6, first+len(fns))})
	fns = append(fns, &wasmFunc{Name: wasm.GCPtrEq, Type: l.typeIndex(ptrEqSig), Code: l.ptrEq()})
	fns = append(fns, &wasmFunc{Name: wasm.GCClear, Type: l.typeIndex(clearSig), Code: l.clearObj(first+len(fns), layouts)})
	return append(fns, l.reflectFuncs(first+len(fns), nimport, funcIndex)...)
}

// clearObj returns the body of the function that zeroes the Go value in
// an object. self is its index.
//
//	local 0: the object; 1: a length; 2: an index
func (l *gcLinker) clearObj(self int, layouts []string) []byte {
	a := &gcAsm{l: l}
	writeUleb128(a, 1)
	writeUleb128(a, 2)
	a.op(wasm.GCTypeI32)
	zero := func(k byte) {
		switch gcStorage(k) {
		case wasm.GCTypeI64:
			writeI64Const(a, 0)
		case wasm.GCTypeF32:
			a.op(0x43, 0, 0, 0, 0)
		case wasm.GCTypeF64:
			a.op(0x44, 0, 0, 0, 0, 0, 0, 0, 0)
		case wasm.GCTypeEqRef:
			a.op(0xD0, wasm.GCHeapEq)
		default:
			a.i32(0)
		}
	}
	for i := range gcKinds {
		k := gcKinds[i]
		t := a.typ(wasm.GCArrayPrefix + string(k))
		a.isA(int64(t), func() {
			a.op(0xFB, 0x0F) // array.len
			a.set(1)
			a.get(0)
			a.cast(t)
			a.i32(0)
			zero(k)
			a.get(1)
			a.gc(0x10, t) // array.fill
			a.op(0x0F)
		})
	}
	objs := a.typ(wasm.GCArrayPrefix + "o")
	a.isA(int64(objs), func() {
		a.op(0xFB, 0x0F)
		a.set(1)
		a.op(0x02, 0x40, 0x03, 0x40) // block, loop
		a.get(2)
		a.get(1)
		a.op(0x4F, 0x0D, 0x01) // i32.ge_u, br_if 1
		a.get(0)
		a.cast(objs)
		a.get(2)
		a.gc(0x0B, objs)
		a.op(0xFB, 0x17, wasm.GCHeapEq)
		a.op(0x10)
		writeUleb128(a, uint64(self))
		a.get(2)
		a.i32(1)
		a.op(0x6A)
		a.set(2)
		a.br(0)
		a.op(0x0B, 0x0B, 0x0F)
	})
	hdr := a.typ(wasm.GCHeader)
	n := len(layouts)
	a.op(0x02, 0x40)
	for range layouts {
		a.op(0x02, 0x40)
	}
	a.get(0)
	a.cast(hdr)
	a.gc(0x02, hdr, 0)
	index := make(map[string]int)
	for j, name := range layouts {
		index[name] = j
	}
	a.op(0x0E)
	writeUleb128(a, uint64(len(l.types)))
	for _, name := range l.types {
		if j, ok := index[name]; ok {
			writeUleb128(a, uint64(j))
		} else {
			writeUleb128(a, uint64(n))
		}
	}
	writeUleb128(a, uint64(n))
	for _, name := range layouts {
		a.op(0x0B)
		t := a.typ(name)
		for i, f := range gcParseStruct(name) {
			field := uint32(wasm.GCHeaderFields + i)
			a.get(0)
			a.cast(t)
			if f.letter == 'A' {
				a.gc(0x02, t, field)
				a.op(0x10)
				writeUleb128(a, uint64(self))
				continue
			}
			zero(f.letter)
			a.gc(0x05, t, field)
		}
		a.op(0x0F)
	}
	a.op(0x0B) // not an object: nothing to clear
	a.op(0x0B)
	return a.Bytes()
}

// ptrEq returns the body of the function that reports whether two
// pointers are equal: they point to the same place, whatever they are.
//
//	local 0, 1: the pointers; 2, 3: what they are pointers into, or
//	null for linear memory; 4, 5: the byte offsets in it
func (l *gcLinker) ptrEq() []byte {
	a := &gcAsm{l: l}
	writeUleb128(a, 2)
	writeUleb128(a, 2)
	a.op(wasm.GCTypeEqRef)
	writeUleb128(a, 2)
	a.op(wasm.GCTypeI32)
	linear, box := a.typ(wasm.GCLinear), a.typ(wasm.GCBox)
	a.get(0)
	a.get(1)
	a.op(0xD3, 0x04, 0x40) // ref.eq, if
	a.i32(1)
	a.op(0x0F, 0x0B)
	for i := uint32(0); i < 2; i++ {
		p, base, off := i, 2+i, 4+i
		a.get(p)
		a.op(0xD1, 0x04, 0x40) // ref.is_null, if: nil is equal to nothing else
		a.i32(0)
		a.op(0x0F, 0x0B)
		a.op(0x02, 0x40, 0x03, 0x40) // block, loop
		// A box: what it is into, at its offset.
		a.get(p)
		a.op(0xFB, 0x14)
		writeSleb128(a, int64(box)) // ref.test
		a.op(0x04, 0x40)            // if
		a.get(p)
		a.cast(box)
		a.gc(0x02, box, wasm.GCBoxOffset)
		a.get(off)
		a.op(0x6A)
		a.set(off)
		a.get(p)
		a.cast(box)
		a.gc(0x02, box, wasm.GCBoxObject)
		a.set(p)
		a.br(1)
		a.op(0x0B)
		// Linear memory: the address.
		a.get(p)
		a.op(0xFB, 0x14)
		writeSleb128(a, int64(linear))
		a.op(0x04, 0x40)
		a.get(p)
		a.cast(linear)
		a.gc(0x02, linear, wasm.GCLinearAddr)
		a.get(off)
		a.op(0x6A)
		a.set(off)
		a.br(2)
		a.op(0x0B)
		a.get(p)
		a.op(0xFB, 0x14, 0x6C) // ref.test i31
		a.op(0x04, 0x40)
		a.get(p)
		a.op(0xFB, 0x16, 0x6C, 0xFB, 0x1E) // ref.cast i31, i31.get_u
		a.get(off)
		a.op(0x6A)
		a.set(off)
		a.br(2)
		a.op(0x0B)
		// An object.
		a.get(p)
		a.set(base)
		a.op(0x0B, 0x0B) // end loop, end block
	}
	a.get(2)
	a.get(3)
	a.op(0xD3) // ref.eq
	a.get(4)
	a.get(5)
	a.op(0x46, 0x71) // i32.eq, i32.and
	a.op(0x0B)
	return a.Bytes()
}

// storeBits returns the body of gcStoreBits: the bytes go into the
// scalar at the offset and, as many as are left, into the next (a wide
// store where there are narrower scalars). Padding is skipped. self is
// the index of the function.
//
//	local 0: the pointer; 1: the offset; 2: the bits; 3: how many bytes;
//	4: scratch; 5: a size; 6: the mask of the bytes; 7: the shift;
//	8 and 9: the pointer and the offset given
func (l *gcLinker) storeBits(layouts []string, size, self int) []byte {
	a := &gcAsm{l: l}
	writeUleb128(a, 5)
	writeUleb128(a, 1)
	a.op(wasm.GCTypeEqRef)
	writeUleb128(a, 1)
	a.op(wasm.GCTypeI32)
	writeUleb128(a, 2)
	a.op(wasm.GCTypeI64)
	writeUleb128(a, 1)
	a.op(wasm.GCTypeEqRef)
	writeUleb128(a, 1)
	a.op(wasm.GCTypeI32)
	linear, box := a.typ(wasm.GCLinear), a.typ(wasm.GCBox)
	a.get(0)
	a.set(8)
	a.get(1)
	a.set(9)
	// rest stores what is left after the c bytes that c pushes.
	rest := func(c func()) {
		a.get(3)
		c()
		a.op(0x4A, 0x04, 0x40) // i32.gt_s, if
		a.get(8)
		a.get(9)
		c()
		a.op(0x6A) // i32.add
		a.get(2)
		c()
		a.i32(3)
		a.op(0x74, 0xAD, 0x88) // i32.shl, i64.extend_i32_u, i64.shr_u
		a.get(3)
		c()
		a.op(0x6B) // i32.sub
		a.op(0x10) // call
		writeUleb128(a, uint64(self))
		a.op(0x0B)
	}

	// The mask: n bytes.
	writeI64Const(a, -1)
	writeI64Const(a, 1)
	a.get(3)
	a.i32(3)
	a.op(0x74, 0xAD, 0x86) // i32.shl, i64.extend_i32_u, i64.shl
	writeI64Const(a, 1)
	a.op(0x7D) // i64.sub
	a.get(3)
	a.i32(8)
	a.op(0x46, 0x1B) // i32.eq, select
	a.set(6)

	// memory stores the bits at the address pushed.
	memory := func() {
		a.set(5)
		for _, c := range []struct {
			n  int64
			op byte
		}{{1, 0x3C}, {2, 0x3D}, {4, 0x3E}, {8, 0x37}} {
			a.get(3)
			a.i32(c.n)
			a.op(0x46, 0x04, 0x40) // i32.eq, if
			a.get(5)
			a.get(2)
			a.op(c.op, 0x00, 0x00, 0x0F) // store, return
			a.op(0x0B)
		}
		a.op(0x00)
	}
	// rmw replaces the bytes in the scalar of kind fk that get pushes
	// and set stores (with what to store it in pushed first by obj):
	// rel pushes the byte offset in it.
	rmw := func(fk byte, rel func(), obj func(), get func(), set func()) {
		rel()
		a.i32(3)
		a.op(0x74, 0xAD) // i32.shl, i64.extend_i32_u
		a.set(7)
		obj()
		get()
		switch fk {
		case 'b', 'h', 'w':
			a.op(0xAD)
		case 'f':
			a.op(0xBC, 0xAD)
		case 'd':
			a.op(0xBD)
		}
		a.get(6)
		a.get(7)
		a.op(0x86) // i64.shl
		writeI64Const(a, -1)
		a.op(0x85, 0x83) // i64.xor, i64.and
		a.get(2)
		a.get(7)
		a.op(0x86)
		a.get(6)
		a.get(7)
		a.op(0x86, 0x83, 0x84) // i64.shl, i64.and, i64.or
		switch fk {
		case 'b', 'h', 'w':
			a.op(0xA7)
		case 'f':
			a.op(0xA7, 0xBE)
		case 'd':
			a.op(0xBF)
		}
		set()
		rest(func() {
			a.i32(gcKindSize(fk))
			rel()
			a.op(0x6B) // i32.sub
		})
		a.op(0x0F)
	}

	a.op(0x03, 0x40) // loop
	a.isA(int64(linear), func() {
		a.gc(0x02, linear, wasm.GCLinearAddr)
		a.get(1)
		a.op(0x6A)
		memory()
	})
	a.isA(-1, func() {
		a.op(0xFB, 0x1E)
		a.get(1)
		a.op(0x6A)
		memory()
	})
	a.isA(int64(box), func() {
		a.set(4)
		a.get(4)
		a.cast(box)
		a.gc(0x02, box, wasm.GCBoxOffset)
		a.get(1)
		a.op(0x6A)
		a.set(1)
		a.get(4)
		a.cast(box)
		a.gc(0x02, box, wasm.GCBoxObject)
		a.set(0)
		a.br(1)
	})
	objs := a.typ(wasm.GCArrayPrefix + "o")
	a.isA(int64(objs), func() {
		a.set(4)
		a.get(4)
		a.cast(objs)
		a.i32(0)
		a.gc(0x0B, objs)
		a.op(0xFB, 0x16, wasm.GCHeapEq)
		a.op(0x10)
		writeUleb128(a, uint64(size))
		a.set(5)
		a.get(4)
		a.cast(objs)
		a.get(1)
		a.get(5)
		a.op(0x6E)
		a.gc(0x0B, objs)
		a.op(0xFB, 0x16, wasm.GCHeapEq)
		a.set(0)
		a.get(1)
		a.get(5)
		a.op(0x70)
		a.set(1)
		a.br(1)
	})
	refs := a.typ(wasm.GCArrayPrefix + "r")
	a.isA(int64(refs), func() {
		// Zeros stored over a pointer clear it.
		a.op(0x1A) // drop
		a.get(2)
		a.op(0x50, 0x04, 0x40) // i64.eqz, if
		a.get(0)
		a.cast(refs)
		a.get(1)
		a.i32(3)
		a.op(0x76) // i32.shr_u
		gcRefNull(&a.Buffer)
		a.gc(0x0E, refs)
		a.op(0x0F, 0x0B)
		a.op(0x00)
	})
	for i := range gcKinds {
		k := gcKinds[i]
		if k == 'r' {
			continue
		}
		t := a.typ(wasm.GCArrayPrefix + string(k))
		a.isA(int64(t), func() {
			a.op(0x1A) // drop
			idx := func() {
				a.get(1)
				a.i32(int64(gcLog2(k)))
				a.op(0x76)
			}
			rmw(k, func() {
				a.get(1)
				a.i32(gcKindSize(k) - 1)
				a.op(0x71)
			}, func() {
				a.get(0)
				a.cast(t)
				idx()
			}, func() {
				a.get(0)
				a.cast(t)
				idx()
				if k == 'b' || k == 'h' {
					a.gc(0x0D, t)
				} else {
					a.gc(0x0B, t)
				}
			}, func() {
				a.gc(0x0E, t)
			})
		})
	}
	n := len(layouts)
	a.op(0x02, 0x40)
	for range layouts {
		a.op(0x02, 0x40)
	}
	a.get(0)
	a.cast(a.typ(wasm.GCHeader))
	a.gc(0x02, a.typ(wasm.GCHeader), 0)
	index := make(map[string]int)
	for j, name := range layouts {
		index[name] = j
	}
	a.op(0x0E)
	writeUleb128(a, uint64(len(l.types)))
	for _, name := range l.types {
		if j, ok := index[name]; ok {
			writeUleb128(a, uint64(j))
		} else {
			writeUleb128(a, uint64(n))
		}
	}
	writeUleb128(a, uint64(n))
	for j, name := range layouts {
		a.op(0x0B)
		t := a.typ(name)
		for i, f := range gcParseStruct(name) {
			field := uint32(wasm.GCHeaderFields + i)
			rel := func() {
				a.get(1)
				a.i32(f.off)
				a.op(0x6B)
			}
			if f.letter == 'A' {
				arr := a.typ(wasm.GCArrayPrefix + string(f.elem))
				rel()
				a.i32(f.esize * f.n)
				a.op(0x49, 0x04, 0x40)
				a.get(0)
				a.cast(t)
				a.gc(0x02, t, field)
				if f.elem == 'o' {
					a.cast(arr)
					rel()
					a.i32(f.esize)
					a.op(0x6E)
					a.gc(0x0B, arr)
					a.op(0xFB, 0x16, wasm.GCHeapEq)
					a.set(4)
					rel()
					a.i32(f.esize)
					a.op(0x70)
					a.set(1)
					a.get(4)
					a.set(0)
				} else {
					a.set(4)
					rel()
					a.set(1)
					a.get(4)
					a.set(0)
				}
				a.br(n - j + 1)
				a.op(0x0B)
				continue
			}
			if f.letter == 'r' {
				// Zeros stored over a pointer clear it.
				rel()
				a.i32(8)
				a.op(0x49, 0x04, 0x40) // i32.lt_u, if
				a.get(2)
				a.op(0x50, 0x04, 0x40) // i64.eqz, if
				a.get(0)
				a.cast(t)
				gcRefNull(&a.Buffer)
				a.gc(0x05, t, field)
				a.op(0x0F, 0x0B)
				a.op(0x00, 0x0B) // unreachable: not zeros
				continue
			}
			rel()
			a.i32(gcKindSize(f.letter))
			a.op(0x49, 0x04, 0x40) // i32.lt_u, if
			rmw(f.letter, rel, func() {
				a.get(0)
				a.cast(t)
			}, func() {
				a.get(0)
				a.cast(t)
				if f.letter == 'b' || f.letter == 'h' {
					a.gc(0x04, t, field)
				} else {
					a.gc(0x02, t, field)
				}
			}, func() {
				a.gc(0x05, t, field)
			})
			a.op(0x0B)
		}
		// Padding: a byte skipped.
		rest(func() { a.i32(1) })
		a.op(0x0F)
	}
	a.op(0x0B, 0x00)
	a.op(0x0B, 0x00)
	a.op(0x0B)
	return a.Bytes()
}

// newByNumber returns the body of the function that returns a new
// object of the type numbered as its argument: ctors[n-1] is the code
// that pushes it.
func (l *gcLinker) newByNumber(ctors [][]byte) []byte {
	a := &gcAsm{l: l}
	writeUleb128(a, 0)
	// A negative number: a wasm.generic, for a Go value of the size in
	// its low 31 bits.
	gen := a.typ(wasm.GCGeneric)
	a.get(0)
	a.i32(0)
	a.op(0x48, 0x04, 0x40) // i32.lt_s, if
	a.i32(int64(gen))
	a.i32(0)
	size := func() {
		a.get(0)
		a.i32(0x7FFFFFFF)
		a.op(0x71) // i32.and
	}
	size()
	for _, k := range []string{"r", "l"} {
		size()
		a.i32(7)
		a.op(0x6A) // i32.add
		a.i32(3)
		a.op(0x76)                              // i32.shr_u
		a.gc(0x07, a.typ(wasm.GCArrayPrefix+k)) // array.new_default
	}
	a.gc(0x00, gen) // struct.new
	a.op(0x0F, 0x0B)
	n := len(ctors)
	a.op(0x02, 0x40) // block: no such number
	for range ctors {
		a.op(0x02, 0x40)
	}
	a.get(0)
	a.op(0x0E) // br_table
	writeUleb128(a, uint64(n+1))
	writeUleb128(a, uint64(n)) // 0: none
	for j := range ctors {
		writeUleb128(a, uint64(j))
	}
	writeUleb128(a, uint64(n))
	for _, c := range ctors {
		a.op(0x0B)
		a.Write(c)
		a.op(0x0F)
	}
	a.op(0x0B, 0x00) // end; unreachable
	a.op(0x0B)
	return a.Bytes()
}

// sizeOf returns the body of gcSize. self is its index.
func (l *gcLinker) sizeOf(self int, layouts []string) []byte {
	a := &gcAsm{l: l}
	writeUleb128(a, 0)
	for i := range gcKinds {
		t := a.typ(wasm.GCArrayPrefix + gcKinds[i:i+1])
		a.isA(int64(t), func() {
			a.op(0xFB, 0x0F) // array.len
			a.i32(gcKindSize(gcKinds[i]))
			a.op(0x6C, 0x0F) // i32.mul, return
		})
	}
	objs := a.typ(wasm.GCArrayPrefix + "o")
	a.isA(int64(objs), func() {
		a.op(0xFB, 0x0F)       // array.len
		a.op(0x45, 0x04, 0x40) // i32.eqz, if
		a.i32(0)
		a.op(0x0F, 0x0B)
		a.get(0)
		a.cast(objs)
		a.op(0xFB, 0x0F)
		a.get(0)
		a.cast(objs)
		a.i32(0)
		a.gc(0x0B, objs)
		a.op(0xFB, 0x16, wasm.GCHeapEq)
		a.op(0x10)
		writeUleb128(a, uint64(self))
		a.op(0x6C, 0x0F)
	})
	if l.generic() {
		gen := a.typ(wasm.GCGeneric)
		a.isA(int64(gen), func() {
			a.gc(0x02, gen, wasm.GCGenericSize)
			a.op(0x0F)
		})
	}
	hdr := a.typ(wasm.GCHeader)
	n := len(layouts)
	a.isA(int64(hdr), func() {
		a.op(0x1A)       // drop
		a.op(0x02, 0x40) // block: no such layout
		for range layouts {
			a.op(0x02, 0x40)
		}
		a.get(0)
		a.cast(hdr)
		a.gc(0x02, hdr, 0)
		index := make(map[string]int)
		for j, name := range layouts {
			index[name] = j
		}
		a.op(0x0E) // br_table
		writeUleb128(a, uint64(len(l.types)))
		for _, name := range l.types {
			if j, ok := index[name]; ok {
				writeUleb128(a, uint64(j))
			} else {
				writeUleb128(a, uint64(n))
			}
		}
		writeUleb128(a, uint64(n))
		for _, name := range layouts {
			a.op(0x0B)
			a.i32(gcStructSize(name))
			a.op(0x0F)
		}
		a.op(0x0B, 0x00)
	})
	a.op(0x00) // unreachable
	a.op(0x0B)
	return a.Bytes()
}

// genericAccess writes the rest of access for a wasm.generic, in local
// tmp: the word the offset is in, of the pointers or of the bits. sz is
// an i32 local, shift an i64 one and ref an eqref one.
func (a *gcAsm) genericAccess(k byte, store bool, tmp, sz, shift, ref uint32) {
	gen := a.typ(wasm.GCGeneric)
	refs, words := a.typ(wasm.GCArrayPrefix+"r"), a.typ(wasm.GCArrayPrefix+"l")
	arr := func(f, t uint32) {
		a.get(tmp)
		a.cast(gen)
		a.gc(0x02, gen, f)
		a.cast(t)
	}
	index := func() {
		a.get(1)
		a.i32(3)
		a.op(0x76) // i32.shr_u
	}
	if k == 'r' {
		if store {
			arr(wasm.GCGenericRefs, refs)
			index()
			a.get(2)
			a.gc(0x0E, refs) // array.set
			arr(wasm.GCGenericWords, words)
			index()
			a.get(2)
			a.op(0x10) // call
			writeUleb128(a, uint64(a.l.ptridFunc))
			a.gc(0x0E, words)
			a.op(0x0F)
			return
		}
		arr(wasm.GCGenericRefs, refs)
		index()
		a.gc(0x0B, refs) // array.get
		a.set(ref)
		a.get(ref)
		a.op(0xD1, 0x04, 0x40) // ref.is_null, if
		// Not stored as a pointer: the address in the bits, as
		// the type word of an interface is.
		arr(wasm.GCGenericWords, words)
		index()
		a.gc(0x0B, words)
		a.addrToRef(sz)
		a.op(0x0F, 0x0B)
		a.get(ref)
		a.op(0x0F)
		return
	}
	n := gcKindSize(k)
	mask := int64(-1)
	if n < 8 {
		mask = int64(1)<<(8*uint(n)) - 1
	}
	a.get(1)
	a.i32(7)
	a.op(0x71) // i32.and
	a.i32(3)
	a.op(0x74, 0xAD) // i32.shl, i64.extend_i32_u
	a.set(shift)
	if !store {
		arr(wasm.GCGenericWords, words)
		index()
		a.gc(0x0B, words)
		a.get(shift)
		a.op(0x88) // i64.shr_u
		if n < 8 {
			writeI64Const(a, mask)
			a.op(0x83) // i64.and
		}
		switch k {
		case 'f':
			a.op(0xA7, 0xBE) // i32.wrap_i64, f32.reinterpret_i32
		case 'd':
			a.op(0xBF) // f64.reinterpret_i64
		}
		a.op(0x0F)
		return
	}
	// The word now holds no pointer.
	arr(wasm.GCGenericRefs, refs)
	index()
	a.op(0xD0, wasm.GCHeapEq) // ref.null eq
	a.gc(0x0E, refs)
	arr(wasm.GCGenericWords, words)
	index()
	arr(wasm.GCGenericWords, words)
	index()
	a.gc(0x0B, words)
	writeI64Const(a, mask)
	a.get(shift)
	a.op(0x86) // i64.shl
	writeI64Const(a, -1)
	a.op(0x85, 0x83) // i64.xor, i64.and
	a.get(2)
	switch k {
	case 'f':
		a.op(0xBC, 0xAD) // i32.reinterpret_f32, i64.extend_i32_u
	case 'd':
		a.op(0xBD) // i64.reinterpret_f64
	}
	if n < 8 {
		writeI64Const(a, mask)
		a.op(0x83)
	}
	a.get(shift)
	a.op(0x86, 0x84) // i64.shl, i64.or
	a.gc(0x0E, words)
	a.op(0x0F)
}

// generic reports whether the program can make a wasm.generic: if it
// has reflect.StructOf.
func (l *gcLinker) generic() bool {
	for v := 0; v < 2; v++ {
		if s := l.ldr.Lookup("reflect.StructOf", v); s != 0 && l.ldr.AttrReachable(s) {
			return true
		}
	}
	return false
}
