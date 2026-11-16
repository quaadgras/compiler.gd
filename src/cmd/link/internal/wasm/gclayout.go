// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wasm

import (
	"cmd/internal/obj/wasm"
	"encoding/binary"
	"strings"
)

// The layouts of the structs of Go values (GOWASM=gc), as data.
//
// A pointer that is not what the compiled code expects is resolved at
// run time by the functions in gchelpers.go, which must then find the
// scalar at a byte offset in an object of any struct of the program.
// They do not hold the layouts as code. The linker describes each
// layout in linear memory, after the program's data, and the header of
// an object is the address of the description of its layout:
//
//	+0   the size of the Go value
//	+4   the number of fields
//	+8   the number of the layout: its index among the program's
//	+12  where the index of the fields is, from the start
//	+16  the number of nested arrays
//	+24  the fields in the order of their offsets, 8 bytes each: the
//	     offset, and a word of the kind (in its low 4 bits: the index of
//	     the letter in gcKinds, or gcKindArray for a nested array) and,
//	     for a scalar, its size in bytes (bits 8 to 15); for an array,
//	     the kind of its elements (bits 4 to 7, gcKindArray for
//	     objects) and the index of its entry in what follows (from bit
//	     16)
//	then 8 bytes that end the fields: an offset no field has, and a
//	     kind that is none
//	then for each nested array, 8 bytes: the size of an element and
//	     the number of elements
//	then the index of the fields: for each 8 bytes of the Go value, 2
//	     bytes, the index of the first field that ends after they
//	     start, so that the field at an offset is found without a
//	     search
//
// One loop over a description thus serves every layout, where the
// functions used to carry the code of each: they were a fifth of the
// code of a small program. What cannot be a loop is the access to a
// field itself, as a field of a WebAssembly struct is named by the
// instruction: wasm.fget, wasm.fgetr and wasm.fset read and write a
// field given the number of the layout and the index of the field, and
// those hold a few bytes for each field of the program. So do
// wasm.clone, which makes an object, and wasm.copy for the layouts of
// few fields: they are what copies the elements of a slice or a map.

const (
	gcKindArray = 7 // after the indexes of gcKinds

	gcLayoutSize   = 0
	gcLayoutFields = 4
	gcLayoutNum    = 8
	gcLayoutIndex  = 12
	gcLayoutArrays = 16
	gcLayoutFirst  = 24 // the first field
	gcFieldOff     = 0
	gcFieldInfo    = 4
	gcFieldBytes   = 8 // the size of the description of a field

	// What ends the fields of a layout.
	gcNoFieldOff  = 0xFFFFFFFF
	gcNoFieldInfo = 15
)

// gcSmallLayout is the most fields of a layout that wasm.copy has code
// of its own for: strings, slices, interfaces and most structs are
// copied without a call for each field.
const gcSmallLayout = 12

// layout returns the address of the description of the layout that
// name spells out: what the header of an object of it holds.
func (l *gcLinker) layout(name string) int64 {
	if a, ok := l.layoutAddr[name]; ok {
		return a
	}
	if l.layoutsDone {
		panic("GOWASM=gc: layout " + name + " after the functions that know the layouts")
	}
	l.typeIndex(name)
	a := l.layoutNext
	l.layoutAddr[name] = a
	l.layouts = append(l.layouts, name)
	l.layoutNext += gcLayoutFirst + gcFieldBytes
	for _, f := range gcParseStruct(name) {
		l.layoutNext += gcFieldBytes
		if f.letter == 'A' {
			l.layoutNext += 8
		}
	}
	l.layoutNext += (gcStructSize(name) + 7) / 8 * 2
	l.layoutNext = (l.layoutNext + 7) &^ 7
	return a
}

// layoutTable returns the descriptions of the layouts, which go at
// l.layoutBase.
func (l *gcLinker) layoutTable() []byte {
	var b []byte
	u32 := func(v int64) { b = binary.LittleEndian.AppendUint32(b, uint32(v)) }
	for num, name := range l.layouts {
		if int64(len(b)) != l.layoutAddr[name]-l.layoutBase {
			panic("GOWASM=gc: layout out of place")
		}
		fields := gcParseStruct(name)
		size := gcStructSize(name)
		narrays := 0
		for _, f := range fields {
			if f.letter == 'A' {
				narrays++
			}
		}
		if len(fields) >= 1<<16 {
			l.ctxt.Errorf(0, "GOWASM=gc: unsupported: a struct of %d scalars", len(fields))
		}
		u32(size)
		u32(int64(len(fields)))
		u32(int64(num))
		u32(int64(gcLayoutFirst + gcFieldBytes*(len(fields)+1) + 8*narrays))
		u32(int64(narrays))
		u32(0)
		var arrays []gcField
		for _, f := range fields {
			u32(f.off)
			if f.letter != 'A' {
				u32(int64(strings.IndexByte(gcKinds, f.letter)) | gcKindSize(f.letter)<<8)
				continue
			}
			elem := int64(gcKindArray)
			if f.elem != 'o' {
				elem = int64(strings.IndexByte(gcKinds, f.elem))
			}
			if len(arrays) >= 1<<16 {
				l.ctxt.Errorf(0, "GOWASM=gc: unsupported: a struct of %d arrays", len(arrays))
			}
			u32(gcKindArray | elem<<4 | int64(len(arrays))<<16)
			arrays = append(arrays, f)
		}
		u32(gcNoFieldOff)
		u32(gcNoFieldInfo)
		for _, f := range arrays {
			u32(f.esize)
			u32(f.n)
		}
		i := 0
		for w := int64(0); w < size; w += 8 {
			for i < len(fields) && fields[i].off+gcFieldSize(fields[i]) <= w {
				i++
			}
			b = binary.LittleEndian.AppendUint16(b, uint16(i))
		}
		for len(b)%8 != 0 {
			b = append(b, 0)
		}
	}
	return b
}

// gcFieldSize returns the size in bytes of what f is a field of, in
// the Go value.
func gcFieldSize(f gcField) int64 {
	if f.letter == 'A' {
		return f.esize * f.n
	}
	return gcKindSize(f.letter)
}

// More of the instructions the functions are written with.

func (a *gcAsm) tee(i uint32)   { a.WriteByte(0x22); writeUleb128(a, uint64(i)) }
func (a *gcAsm) call(f int)     { a.WriteByte(0x10); writeUleb128(a, uint64(f)) }
func (a *gcAsm) i64(v int64)    { writeI64Const(a, v) }
func (a *gcAsm) load(off int)   { a.op(0x28, 0x02); writeUleb128(a, uint64(off)) } // i32.load
func (a *gcAsm) if_()           { a.op(0x04, 0x40) }
func (a *gcAsm) else_()         { a.op(0x05) }
func (a *gcAsm) end()           { a.op(0x0B) }
func (a *gcAsm) ret()           { a.op(0x0F) }
func (a *gcAsm) unreachable()   { a.op(0x00) }
func (a *gcAsm) block()         { a.op(0x02, 0x40) }
func (a *gcAsm) loop()          { a.op(0x03, 0x40) }
func (a *gcAsm) brIf(depth int) { a.WriteByte(0x0D); writeUleb128(a, uint64(depth)) }

// locals declares the locals of a function, after its parameters: one
// of each of the value types given.
func (a *gcAsm) locals(types ...byte) {
	var groups [][2]int
	for _, t := range types {
		if n := len(groups); n > 0 && groups[n-1][1] == int(t) {
			groups[n-1][0]++
			continue
		}
		groups = append(groups, [2]int{1, int(t)})
	}
	writeUleb128(a, uint64(len(groups)))
	for _, g := range groups {
		writeUleb128(a, uint64(g[0]))
		a.WriteByte(byte(g[1]))
	}
}

// A gcLoc names the locals in which a function keeps the place of a
// byte offset in a struct: the description of the layout of the object,
// that of the field the offset is in, the word of its kind, and the
// offset within the field.
type gcLoc struct {
	obj, off        uint32 // the object and the byte offset: given
	d, e, info, rel uint32 // i32 locals: found
}

// layoutOf sets c.d to the layout of the object in c.obj, which is a
// struct of a Go value.
func (a *gcAsm) layoutOf(c gcLoc) {
	hdr := a.typ(wasm.GCHeader)
	a.get(c.obj)
	a.cast(hdr)
	a.gc(0x02, hdr, 0)
	a.set(c.d)
}

// locate finds the field that the offset c.off is in, in the object
// c.obj: it sets c.d and c.e, which is 0 if the offset is past the end
// of the struct, and in padding the field before it, or what ends the
// fields. It traps if the object is not a struct of the program.
func (a *gcAsm) locate(c gcLoc) {
	a.layoutOf(c)
	a.get(c.d)
	a.op(0x45) // i32.eqz
	a.if_()
	a.unreachable()
	a.end()
	a.i32(0)
	a.set(c.e)
	a.get(c.off)
	a.get(c.d)
	a.load(gcLayoutSize)
	a.op(0x49) // i32.lt_u
	a.if_()
	// Most often a field starts at the offset, and it is the first
	// that the index gives for its 8 bytes. Otherwise, wasm.find.
	a.indexed(c.d, c.off)
	a.tee(c.e)
	a.load(gcFieldOff)
	a.get(c.off)
	a.op(0x47) // i32.ne
	a.if_()
	a.get(c.d)
	a.get(c.off)
	a.call(a.l.fnFind)
	a.set(c.e)
	a.end()
	a.end()
}

// indexed pushes the first field of the layout d (a local) that the
// index gives for the 8 bytes that the offset off (a local) is in:
// the first that ends after they start, or what ends the fields.
func (a *gcAsm) indexed(d, off uint32) {
	a.get(d)
	a.get(d)
	a.get(d)
	a.load(gcLayoutIndex)
	a.op(0x6A) // i32.add
	a.get(off)
	a.i32(3)
	a.op(0x76) // i32.shr_u
	a.i32(1)
	a.op(0x74, 0x6A)       // i32.shl, i32.add
	a.op(0x2F, 0x01, 0x00) // i32.load16_u
	a.i32(3)
	a.op(0x74, 0x6A) // i32.shl, i32.add
	a.i32(gcLayoutFirst)
	a.op(0x6A)
}

// within sets c.info and c.rel for the field c.e.
func (a *gcAsm) within(c gcLoc) {
	a.get(c.e)
	a.load(gcFieldInfo)
	a.set(c.info)
	a.get(c.off)
	a.get(c.e)
	a.load(gcFieldOff)
	a.op(0x6B) // i32.sub
	a.set(c.rel)
}

// kind pushes the kind of the field c.e.
func (a *gcAsm) kind(c gcLoc) {
	a.get(c.info)
	a.i32(15)
	a.op(0x71) // i32.and
}

// kindIs pushes whether the field c.e is of kind k, a letter of gcKinds
// or 'A'.
func (a *gcAsm) kindIs(c gcLoc, k byte) {
	a.kind(c)
	if k == 'A' {
		a.i32(gcKindArray)
	} else {
		a.i32(int64(strings.IndexByte(gcKinds, k)))
	}
	a.op(0x46) // i32.eq
}

// scalarSize pushes the size in bytes of the field c.e, a scalar.
func (a *gcAsm) scalarSize(c gcLoc) {
	a.get(c.info)
	a.i32(8)
	a.op(0x76) // i32.shr_u
	a.i32(255)
	a.op(0x71) // i32.and
}

// arraySize pushes the size in bytes of the field c.e, a nested array.
func (a *gcAsm) arraySize(c gcLoc) {
	a.get(c.d)
	a.get(c.e)
	a.call(a.l.fnFlen)
}

// field pushes what wasm.fget and the like take to name the field c.e
// of an object of the layout c.d, in local obj: the object, the number
// of the layout and the index of the field.
func (a *gcAsm) field(c gcLoc, obj uint32) {
	a.get(obj)
	a.get(c.d)
	a.load(gcLayoutNum)
	a.get(c.e)
	a.get(c.d)
	a.op(0x6B) // i32.sub
	a.i32(gcLayoutFirst)
	a.op(0x6B)
	a.i32(3)
	a.op(0x76) // i32.shr_u
}

// fieldAt is field for the field of index i (a local) of the layout d
// (a local).
func (a *gcAsm) fieldAt(obj, d, i uint32) {
	a.get(obj)
	a.get(d)
	a.load(gcLayoutNum)
	a.get(i)
}

// infoAt pushes the word of the kind of the field of index i (a local)
// of the layout d (a local).
func (a *gcAsm) infoAt(d, i uint32) {
	a.get(d)
	a.get(i)
	a.i32(3)
	a.op(0x74, 0x6A) // i32.shl, i32.add
	a.load(gcLayoutFirst + gcFieldInfo)
}

// find returns the body of the function that returns the description
// of the field of a layout that a byte offset is in; or, if it is in
// none (in padding), that of the last field before it, or what ends
// the fields; or 0 if the offset is past the end of the struct.
//
//	local 0: the layout; 1: the offset; 2: the field
func (l *gcLinker) find() []byte {
	a := &gcAsm{l: l}
	a.locals(wasm.GCTypeI32)
	a.get(1)
	a.get(0)
	a.load(gcLayoutSize)
	a.op(0x4F) // i32.ge_u
	a.if_()
	a.i32(0)
	a.ret()
	a.end()
	// The first field that ends after the 8 bytes the offset is in
	// start, and then the last of those after it that start at or
	// before the offset (what ends the fields does not).
	a.indexed(0, 1)
	a.set(2)
	a.block()
	a.loop()
	a.get(2)
	a.load(gcFieldBytes + gcFieldOff)
	a.get(1)
	a.op(0x4B) // i32.gt_u
	a.brIf(1)
	a.get(2)
	a.i32(gcFieldBytes)
	a.op(0x6A)
	a.set(2)
	a.br(0)
	a.end()
	a.end()
	a.get(2)
	a.end()
	return a.Bytes()
}

// flen returns the body of the function that returns the size in bytes
// of a field of a layout that is a nested array.
//
//	local 0: the layout; 1: the field; 2: the entry of the array
func (l *gcLinker) flen() []byte {
	a := &gcAsm{l: l}
	a.locals(wasm.GCTypeI32)
	// After the fields and what ends them, by the index in the word
	// of the kind.
	a.get(0)
	a.get(0)
	a.load(gcLayoutFields)
	a.i32(3)
	a.op(0x74, 0x6A) // i32.shl, i32.add
	a.i32(gcFieldBytes)
	a.op(0x6A)
	a.get(1)
	a.load(gcFieldInfo)
	a.i32(16)
	a.op(0x76) // i32.shr_u
	a.i32(3)
	a.op(0x74, 0x6A)
	a.tee(2)
	a.load(gcLayoutFirst)
	a.get(2)
	a.load(gcLayoutFirst + 4)
	a.op(0x6C) // i32.mul
	a.end()
	return a.Bytes()
}

// byLayout writes the dispatch on the number of a layout that get
// pushes: arm writes the code of each layout for which it is wanted,
// which must not fall through; the others, and a number that is not a
// layout's, go on after.
func (a *gcAsm) byLayout(get func(), want func(name string) bool, arm func(name string)) {
	layouts := a.l.layouts
	var wanted []string
	index := make([]int, len(layouts))
	for j, name := range layouts {
		index[j] = -1
		if want(name) {
			index[j] = len(wanted)
			wanted = append(wanted, name)
		}
	}
	if len(wanted) == 0 {
		return
	}
	n := len(wanted)
	a.block()
	for range wanted {
		a.block()
	}
	get()
	a.op(0x0E) // br_table
	writeUleb128(a, uint64(len(layouts)))
	for _, i := range index {
		if i < 0 {
			i = n
		}
		writeUleb128(a, uint64(i))
	}
	writeUleb128(a, uint64(n))
	for _, name := range wanted {
		a.end()
		arm(name)
	}
	a.end()
}

// fieldFunc returns the body of a function that reads or writes a
// field of an object, given the number of its layout and the index of
// the field: arm writes what it does to a field for which it is wanted,
// with the object pushed, cast to its type t. A field for which it is
// not is one the function is not called for.
//
//	local 0: the object; 1: the number of the layout; 2: the index of
//	the field; 3: the value to store, if any
func (l *gcLinker) fieldFunc(want func(f gcField) bool, arm func(a *gcAsm, t, field uint32, f gcField)) []byte {
	a := &gcAsm{l: l}
	a.locals()
	a.byLayout(func() { a.get(1) }, func(string) bool { return true }, func(name string) {
		t := a.typ(name)
		fields := gcParseStruct(name)
		var wanted []int
		for i, f := range fields {
			if want(f) {
				wanted = append(wanted, i)
			}
		}
		if len(wanted) == 0 {
			a.unreachable()
			return
		}
		// A block for each field wanted, and one around them for the
		// others: the object, cast once, is their result.
		for range len(wanted) + 1 {
			a.op(0x02, wasm.GCRefNonNull)
			writeSleb128(a, int64(t))
		}
		a.get(0)
		a.cast(t)
		a.get(2)
		a.op(0x0E) // br_table
		writeUleb128(a, uint64(len(fields)))
		w := 0
		for i := range fields {
			if w < len(wanted) && wanted[w] == i {
				writeUleb128(a, uint64(w))
				w++
			} else {
				writeUleb128(a, uint64(len(wanted)))
			}
		}
		writeUleb128(a, uint64(len(wanted)))
		for _, i := range wanted {
			a.end()
			arm(a, t, uint32(wasm.GCHeaderFields+i), fields[i])
			a.ret()
		}
		a.end()
		a.unreachable()
	})
	a.unreachable()
	a.end()
	return a.Bytes()
}

// fget returns the body of wasm.fget: the bits of a scalar field, as an
// i64; of a pointer, its identity.
func (l *gcLinker) fget() []byte {
	return l.fieldFunc(func(f gcField) bool { return f.letter != 'A' }, func(a *gcAsm, t, field uint32, f gcField) {
		switch f.letter {
		case 'b', 'h':
			a.gc(0x04, t, field) // struct.get_u
		default:
			a.gc(0x02, t, field)
		}
		switch f.letter {
		case 'b', 'h', 'w':
			a.op(0xAD) // i64.extend_i32_u
		case 'f':
			a.op(0xBC, 0xAD) // i32.reinterpret_f32
		case 'd':
			a.op(0xBD) // i64.reinterpret_f64
		case 'r':
			a.call(l.ptridFunc)
		}
	})
}

// fset returns the body of wasm.fset: it sets a scalar field from its
// bits, an i64, or one that is a pointer or the object of a nested
// array from a reference.
//
//	local 3: the bits; 4: the reference
func (l *gcLinker) fset() []byte {
	return l.fieldFunc(func(f gcField) bool { return true }, func(a *gcAsm, t, field uint32, f gcField) {
		switch f.letter {
		case 'A', 'r':
			a.get(4)
		case 'b', 'h', 'w':
			a.get(3)
			a.op(0xA7) // i32.wrap_i64
		case 'f':
			a.get(3)
			a.op(0xA7, 0xBE) // f32.reinterpret_i32
		case 'd':
			a.get(3)
			a.op(0xBF) // f64.reinterpret_i64
		default:
			a.get(3)
		}
		a.gc(0x05, t, field)
	})
}

// fgetr returns the body of wasm.fgetr: a field that is a pointer, or
// the object of a nested array.
func (l *gcLinker) fgetr() []byte {
	return l.fieldFunc(func(f gcField) bool { return f.letter == 'A' || f.letter == 'r' }, func(a *gcAsm, t, field uint32, f gcField) {
		a.gc(0x02, t, field)
	})
}

// gcSmall reports whether wasm.copy has code for the layout that name
// spells (see gcSmallLayout).
func gcSmall(name string) bool {
	return len(gcParseStruct(name)) <= gcSmallLayout
}

// resolve resolves the pointer in local p, with the byte offset in
// local off added, to the struct it points into: p is then an object
// that is no pointer into another, and off the offset in it. A pointer
// into an array of objects is one into the element. The function
// returns 0 if the pointer is past the end of such an array. tmp is an
// eqref local and esz an i32 one.
func (a *gcAsm) resolve(p, off, tmp, esz uint32, size int) {
	box, objs := a.typ(wasm.GCBox), a.typ(wasm.GCArrayPrefix+"o")
	is := func(t uint32, do func()) {
		a.op(0x02, wasm.GCTypeEqRef) // block (result eqref)
		a.get(p)
		a.op(0xFB, 0x19, 0x01, 0x00, wasm.GCHeapEq) // br_on_cast_fail 0 eq
		writeSleb128(a, int64(t))
		a.set(tmp)
		do()
		a.br(1)
		a.op(0x0B, 0x1A) // end, drop
	}
	none := func() {
		a.if_()
		a.i32(0)
		a.ret()
		a.end()
	}
	a.loop()
	is(box, func() {
		a.get(tmp)
		a.cast(box)
		a.gc(0x02, box, wasm.GCBoxOffset)
		a.get(off)
		a.op(0x6A) // i32.add
		a.set(off)
		a.get(tmp)
		a.cast(box)
		a.gc(0x02, box, wasm.GCBoxObject)
		a.set(p)
	})
	is(objs, func() {
		a.get(tmp)
		a.cast(objs)
		a.op(0xFB, 0x0F) // array.len
		a.op(0x45)       // i32.eqz
		none()
		a.get(tmp)
		a.cast(objs)
		a.i32(0)
		a.gc(0x0B, objs)
		a.op(0xFB, 0x16, wasm.GCHeapEq) // ref.cast eq
		a.call(size)
		a.set(esz)
		a.get(off)
		a.get(esz)
		a.op(0x6E) // i32.div_u
		a.get(tmp)
		a.cast(objs)
		a.op(0xFB, 0x0F)
		a.op(0x4F) // i32.ge_u
		none()
		a.get(tmp)
		a.cast(objs)
		a.get(off)
		a.get(esz)
		a.op(0x6E)
		a.gc(0x0B, objs)
		a.op(0xFB, 0x16, wasm.GCHeapEq)
		a.set(p)
		a.get(off)
		a.get(esz)
		a.op(0x70) // i32.rem_u
		a.set(off)
	})
	a.end()
}

// fieldOf is field for the field e of the layout d (locals).
func (a *gcAsm) fieldOf(obj, d, e uint32) {
	a.field(gcLoc{d: d, e: e}, obj)
}

// move returns the body of wasm.move, which copies the bytes of a Go
// value from one place to another, where both are within structs, and
// reports whether they are. The runtime copies with it a value of a
// type it knows only by its descriptor, instead of scalar by scalar.
// The two places hold a value of one type, so the fields of the one
// are those of the other, though the structs they are in may differ.
//
//	local 0: the destination; 1: the source; 2: the size;
//	3, 4: the offsets in them; 5: scratch; 6, 7: their layouts;
//	8, 9: the field of each; 10, 11: the end of the fields of each;
//	12: the kind of the field; 13: scratch
func (l *gcLinker) move(copyObj, size int) []byte {
	a := &gcAsm{l: l}
	a.locals(wasm.GCTypeI32, wasm.GCTypeI32, wasm.GCTypeEqRef,
		wasm.GCTypeI32, wasm.GCTypeI32, wasm.GCTypeI32, wasm.GCTypeI32, wasm.GCTypeI32, wasm.GCTypeI32, wasm.GCTypeI32, wasm.GCTypeI32)
	hdr := a.typ(wasm.GCHeader)
	none := func() {
		a.if_()
		a.i32(0)
		a.ret()
		a.end()
	}
	a.resolve(0, 3, 5, 13, size)
	a.resolve(1, 4, 5, 13, size)
	for p := uint32(0); p < 2; p++ {
		a.get(p)
		a.op(0xFB, 0x14)
		writeSleb128(a, int64(hdr)) // ref.test
		a.op(0x45)                  // i32.eqz
		none()
		a.layoutOf(gcLoc{obj: p, d: 6 + p})
		a.get(6 + p)
		a.op(0x45)
		none() // not a struct of the program
	}
	// The whole of two objects of one layout.
	a.get(3)
	a.get(4)
	a.op(0x72, 0x45) // i32.or, i32.eqz
	a.get(6)
	a.get(7)
	a.op(0x46, 0x71) // i32.eq, i32.and
	a.get(2)
	a.get(7)
	a.load(gcLayoutSize)
	a.op(0x46, 0x71)
	a.if_()
	a.get(1)
	a.get(0)
	a.call(copyObj)
	a.i32(1)
	a.ret()
	a.end()
	// Part of them: the fields from the one offset, to those from the
	// other.
	for p := uint32(0); p < 2; p++ {
		a.get(3 + p)
		a.get(2)
		a.op(0x6A) // i32.add
		a.get(6 + p)
		a.load(gcLayoutSize)
		a.op(0x4B) // i32.gt_u
		none()
		a.get(6 + p)
		a.get(3 + p)
		a.call(l.fnFind)
		a.tee(8 + p)
		a.op(0x45)
		none()
		a.get(8 + p)
		a.load(gcFieldOff)
		a.get(3 + p)
		a.op(0x47) // i32.ne
		none()
		a.get(6 + p)
		a.get(6 + p)
		a.load(gcLayoutFields)
		a.i32(3)
		a.op(0x74, 0x6A) // i32.shl, i32.add
		a.i32(gcLayoutFirst)
		a.op(0x6A)
		a.set(10 + p)
	}
	a.block()
	a.loop()
	a.get(9)
	a.get(11)
	a.op(0x4F) // i32.ge_u
	a.brIf(1)
	a.get(9)
	a.load(gcFieldOff)
	a.get(4)
	a.op(0x6B) // i32.sub
	a.get(2)
	a.op(0x4F) // i32.ge_u
	a.brIf(1)
	// The field of the destination is the same, at the same place.
	a.get(8)
	a.get(10)
	a.op(0x4F) // i32.ge_u
	none()
	a.get(9)
	a.load(gcFieldInfo)
	a.i32(0xFFFF)
	a.op(0x71) // i32.and
	a.tee(12)
	a.get(8)
	a.load(gcFieldInfo)
	a.i32(0xFFFF)
	a.op(0x71)
	a.op(0x47) // i32.ne
	a.get(9)
	a.load(gcFieldOff)
	a.get(4)
	a.op(0x6B)
	a.get(8)
	a.load(gcFieldOff)
	a.get(3)
	a.op(0x6B)
	a.op(0x47, 0x72) // i32.ne, i32.or
	none()
	a.get(12)
	a.i32(15)
	a.op(0x71)
	a.set(12)
	a.get(12)
	a.i32(gcKindArray)
	a.op(0x46) // i32.eq
	a.if_()
	a.fieldOf(1, 7, 9)
	a.call(l.fnFgetr)
	a.fieldOf(0, 6, 8)
	a.call(l.fnFgetr)
	a.call(copyObj)
	a.else_()
	a.get(12)
	a.i32(int64(strings.IndexByte(gcKinds, 'r')))
	a.op(0x46)
	a.if_()
	a.fieldOf(0, 6, 8)
	a.i64(0)
	a.fieldOf(1, 7, 9)
	a.call(l.fnFgetr)
	a.call(l.fnFset)
	a.else_()
	a.fieldOf(0, 6, 8)
	a.fieldOf(1, 7, 9)
	a.call(l.fnFget)
	gcRefNull(&a.Buffer)
	a.call(l.fnFset)
	a.end()
	a.end()
	for p := uint32(8); p < 10; p++ {
		a.get(p)
		a.i32(gcFieldBytes)
		a.op(0x6A)
		a.set(p)
	}
	a.br(0)
	a.end()
	a.end()
	a.i32(1)
	a.end()
	return a.Bytes()
}

// zero returns the body of wasm.zero, which zeroes the bytes of a Go
// value within a struct, and reports whether it is within one.
//
//	local 0: the pointer; 1: the size; 2: the offset; 3: scratch;
//	4: the layout; 5: the field; 6: the end of the fields; 7: the kind
//	of the field; 8: scratch
func (l *gcLinker) zero(clearObj, size int) []byte {
	a := &gcAsm{l: l}
	a.locals(wasm.GCTypeI32, wasm.GCTypeEqRef, wasm.GCTypeI32, wasm.GCTypeI32, wasm.GCTypeI32, wasm.GCTypeI32, wasm.GCTypeI32)
	hdr := a.typ(wasm.GCHeader)
	none := func() {
		a.if_()
		a.i32(0)
		a.ret()
		a.end()
	}
	a.resolve(0, 2, 3, 8, size)
	a.get(0)
	a.op(0xFB, 0x14)
	writeSleb128(a, int64(hdr)) // ref.test
	a.op(0x45)                  // i32.eqz
	none()
	a.layoutOf(gcLoc{obj: 0, d: 4})
	a.get(4)
	a.op(0x45)
	none()
	// The whole object.
	a.get(2)
	a.op(0x45)
	a.get(1)
	a.get(4)
	a.load(gcLayoutSize)
	a.op(0x46, 0x71) // i32.eq, i32.and
	a.if_()
	a.get(0)
	a.call(clearObj)
	a.i32(1)
	a.ret()
	a.end()
	a.get(2)
	a.get(1)
	a.op(0x6A) // i32.add
	a.get(4)
	a.load(gcLayoutSize)
	a.op(0x4B) // i32.gt_u
	none()
	a.get(4)
	a.get(2)
	a.call(l.fnFind)
	a.tee(5)
	a.op(0x45)
	none()
	a.get(5)
	a.load(gcFieldOff)
	a.get(2)
	a.op(0x47) // i32.ne
	none()
	a.get(4)
	a.get(4)
	a.load(gcLayoutFields)
	a.i32(3)
	a.op(0x74, 0x6A) // i32.shl, i32.add
	a.i32(gcLayoutFirst)
	a.op(0x6A)
	a.set(6)
	a.block()
	a.loop()
	a.get(5)
	a.get(6)
	a.op(0x4F) // i32.ge_u
	a.brIf(1)
	a.get(5)
	a.load(gcFieldOff)
	a.get(2)
	a.op(0x6B) // i32.sub
	a.get(1)
	a.op(0x4F) // i32.ge_u
	a.brIf(1)
	a.get(5)
	a.load(gcFieldInfo)
	a.i32(15)
	a.op(0x71) // i32.and
	a.set(7)
	a.get(7)
	a.i32(gcKindArray)
	a.op(0x46) // i32.eq
	a.if_()
	a.fieldOf(0, 4, 5)
	a.call(l.fnFgetr)
	a.call(clearObj)
	a.else_()
	a.get(7)
	a.i32(int64(strings.IndexByte(gcKinds, 'r')))
	a.op(0x46)
	a.if_()
	a.fieldOf(0, 4, 5)
	a.i64(0)
	gcRefNull(&a.Buffer)
	a.call(l.fnFset)
	a.else_()
	a.fieldOf(0, 4, 5)
	a.i64(0)
	gcRefNull(&a.Buffer)
	a.call(l.fnFset)
	a.end()
	a.end()
	a.get(5)
	a.i32(gcFieldBytes)
	a.op(0x6A)
	a.set(5)
	a.br(0)
	a.end()
	a.end()
	a.i32(1)
	a.end()
	return a.Bytes()
}
