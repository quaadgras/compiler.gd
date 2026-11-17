// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wasm

import (
	"bytes"
	"cmd/internal/obj/wasm"
)

// The functions that hold JavaScript values for syscall/js with
// GOWASM=gc (see wasm.GCJSBox): the wrappers of the imports of module
// gojs call them. They are made only for a program that has such
// imports.

// jsHelper returns the number of the function named among those
// jsHelpers makes, or -1.
func jsHelper(name string) int {
	switch name {
	case wasm.GCJSBox:
		return 0
	case wasm.GCJSUnbox:
		return 1
	}
	return -1
}

// A gcJSCall is a call of one of the functions of jsHelpers, whose index
// is written once it is known: in the code of function fn, at off.
type gcJSCall struct {
	fn, off, helper int
}

// jsHelpers returns wasm.jsbox and wasm.jsunbox.
func (l *gcLinker) jsHelpers() []*wasmFunc {
	ext := l.typeIndex(wasm.GCExtern)
	var ptr []byte // the value type of a pointer
	nullPtr := []byte{0xD0, wasm.GCHeapEq}
	field := uint64(0)
	if splitMode() {
		var b bytes.Buffer
		b.WriteByte(wasm.GCRefNull)
		writeSleb128(&b, int64(l.typeIndex(wasm.GCPtr)))
		ptr = b.Bytes()
		nullPtr = append([]byte{0xD0}, ptr[1:]...)
		field = 3
	} else {
		ptr = []byte{wasm.GCTypeEqRef}
	}

	// jsbox (x externref) r: nil for null, else a new GCExtern of x.
	var box bytes.Buffer
	box.WriteByte(0x00)                 // no locals
	box.Write([]byte{0x20, 0x00, 0xD1}) // local.get 0; ref.is_null
	box.WriteByte(0x04)                 // if
	box.Write(ptr)                      // (result r)
	box.Write(nullPtr)                  // ref.null
	box.WriteByte(0x05)                 // else
	if splitMode() {
		writeI32Const(&box, wasm.GCExternAddr) // addr
		box.WriteByte(0xD0)                    // ref.null slots
		writeSleb128(&box, int64(l.typeIndex(wasm.GCSlots)))
		writeI32Const(&box, 0) // off
	}
	box.Write([]byte{0x20, 0x00, 0xFB, 0x00}) // local.get 0; struct.new
	writeUleb128(&box, uint64(ext))
	box.Write([]byte{0x0B, 0x0B}) // end; end

	// jsunbox (p r) externref: null for nil, else the value p holds.
	var unbox bytes.Buffer
	unbox.WriteByte(0x00)
	unbox.Write([]byte{0x20, 0x00, 0xD1})
	unbox.Write([]byte{0x04, ExternRef}) // if (result externref)
	unbox.Write([]byte{0xD0, ExternRef}) // ref.null extern
	unbox.WriteByte(0x05)
	unbox.Write([]byte{0x20, 0x00, 0xFB, 0x16}) // local.get 0; ref.cast
	writeSleb128(&unbox, int64(ext))
	unbox.Write([]byte{0xFB, 0x02}) // struct.get
	writeUleb128(&unbox, uint64(ext))
	writeUleb128(&unbox, field)
	unbox.Write([]byte{0x0B, 0x0B})

	return []*wasmFunc{
		{Name: wasm.GCJSBox, Type: l.typeIndex(wasm.GCFuncPrefix + "x.r"), Code: box.Bytes()},
		{Name: wasm.GCJSUnbox, Type: l.typeIndex(wasm.GCFuncPrefix + "r.x"), Code: unbox.Bytes()},
	}
}
