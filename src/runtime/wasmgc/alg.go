// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

import (
	"internal/abi"
	"unsafe"
)

// Equality of values through pointers, for the type descriptors and the
// functions the compiler generates.

func f32equal(p, q unsafe.Pointer) bool { return *(*float32)(p) == *(*float32)(q) }
func f64equal(p, q unsafe.Pointer) bool { return *(*float64)(p) == *(*float64)(q) }
func c64equal(p, q unsafe.Pointer) bool { return *(*complex64)(p) == *(*complex64)(q) }
func c128equal(p, q unsafe.Pointer) bool {
	return *(*complex128)(p) == *(*complex128)(q)
}
func strequal(p, q unsafe.Pointer) bool { return *(*string)(p) == *(*string)(q) }

func interequal(p, q unsafe.Pointer) bool {
	x := (*iface)(p)
	y := (*iface)(q)
	if x.tab != y.tab {
		return false
	}
	if x.tab == 0 {
		return true
	}
	return ifaceEqual((*itab)(unsafe.Pointer(x.tab)).Type, (*eface)(p), (*eface)(q))
}

func nilinterequal(p, q unsafe.Pointer) bool {
	x := (*eface)(p)
	y := (*eface)(q)
	if x._type != y._type {
		return false
	}
	if x._type == 0 {
		return true
	}
	return ifaceEqual((*_type)(unsafe.Pointer(x._type)), x, y)
}

// efaceeq and ifaceeq compare the data of two interface values of the
// same type (the same itab).
func efaceeq(t *_type, x, y unsafe.Pointer) bool {
	if t == nil {
		return true
	}
	return ifaceEqual(t, (*eface)(x), (*eface)(y))
}

func ifaceeq(tab *itab, x, y unsafe.Pointer) bool {
	if tab == nil {
		return true
	}
	return ifaceEqual(tab.Type, (*eface)(x), (*eface)(y))
}

// spread is the value of a type kept in an interface as its pointer
// (the first word) and two words of payload.
type spread struct {
	p      unsafe.Pointer
	w0, w1 uint64
}

func float64bits(f float64) uint64 { return *(*uint64)(unsafe.Pointer(&f)) }

// ifaceEqual reports whether two interface values of dynamic type t are
// equal.
func ifaceEqual(t *_type, x, y *eface) bool {
	if t.Equal == nil && !structComparable(t) {
		panic(errorString("comparing uncomparable type " + toRType(t).string()))
	}
	switch {
	case t.IsDirectIface():
		return x.data == y.data
	case t.IsInlineIface():
		// The payload, as memory.
		xb := [2]uint64{float64bits(x.w0), float64bits(x.w1)}
		yb := [2]uint64{float64bits(y.w0), float64bits(y.w1)}
		return typeEqual(t, noescape(unsafe.Pointer(&xb)), noescape(unsafe.Pointer(&yb)))
	case t.IsSpreadIface():
		xs := &spread{x.data, float64bits(x.w0), float64bits(x.w1)}
		ys := &spread{y.data, float64bits(y.w0), float64bits(y.w1)}
		return typeEqual(t, unsafe.Pointer(xs), unsafe.Pointer(ys))
	}
	return typeEqual(t, x.data, y.data)
}

// typeEqual reports whether the values of type t at p and q are equal:
// by t.Equal, or field by field for a struct type made by
// reflect.StructOf, which has no Equal with GOWASM=gc (its descriptor,
// in linear memory, cannot hold a closure).
//
//go:linkname typeEqual
func typeEqual(t *_type, p, q unsafe.Pointer) bool {
	if t.Equal != nil {
		return t.Equal(p, q)
	}
	if t.Kind() == abi.Array {
		at := (*abi.ArrayType)(unsafe.Pointer(t))
		for i := uintptr(0); i < at.Len; i++ {
			o := i * at.Elem.Size_
			if !typeEqual(at.Elem, add(p, o), add(q, o)) {
				return false
			}
		}
		return true
	}
	st := (*abi.StructType)(unsafe.Pointer(t))
	for i := range st.Fields {
		f := &st.Fields[i]
		if !typeEqual(f.Typ, add(p, f.Offset), add(q, f.Offset)) {
			return false
		}
	}
	return true
}

// structComparable reports whether t, which has no Equal, is a struct
// type made by reflect.StructOf whose values can be compared (field by
// field): reflect marks it with a flag that is otherwise unused.
func structComparable(t *_type) bool {
	return (t.Kind() == abi.Struct || t.Kind() == abi.Array) && t.TFlag&abi.TFlagGCMaskOnDemand != 0
}
