// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

// IsWholeStruct reports whether t is a struct whose words mean nothing
// apart, and which is loaded and stored whole as an interface is:
// reflect.Value, whose type, pointer, inline payload and flags must be
// those of one value (doc/gd/publication-point.md). As runtime's
// isWholeStruct.
func IsWholeStruct(t *Type) bool {
	// By its fields, not its name: generic code has its shape, and a type
	// defined from it, as in type V reflect.Value, has another name.
	if !t.IsStruct() || t.NumFields() != 4 {
		return false
	}
	f := t.Fields()
	return f[0].Sym != nil && f[1].Sym != nil && f[0].Sym.Name == "typ_" && f[1].Sym.Name == "ptr" && f[0].Sym.Pkg != nil && f[0].Sym.Pkg.Path == "reflect"
}

// HasTearable reports whether a value of type t holds a slice, a string
// or an interface: values of more than one word, which are loaded and
// stored whole where goroutines may race on them (runtime/shared.go).
func HasTearable(t *Type) bool {
	switch {
	case t.IsSlice(), t.IsString(), t.IsInterface(), IsWholeStruct(t):
		return true
	case t.IsArray():
		return t.NumElem() > 0 && HasTearable(t.Elem())
	case t.IsStruct():
		for _, f := range t.Fields() {
			if HasTearable(f.Type) {
				return true
			}
		}
	}
	return false
}

// HasIface reports whether a value of type t holds an interface, or a
// struct that goes whole as one does (IsWholeStruct).
func HasIface(t *Type) bool {
	switch {
	case t.IsInterface(), IsWholeStruct(t):
		return true
	case t.IsArray():
		return t.NumElem() > 0 && HasIface(t.Elem())
	case t.IsStruct():
		for _, f := range t.Fields() {
			if HasIface(f.Type) {
				return true
			}
		}
	}
	return false
}

// HasString reports whether a value of type t holds a string.
func HasString(t *Type) bool {
	switch {
	case t.IsString():
		return true
	case t.IsArray():
		return t.NumElem() > 0 && HasString(t.Elem())
	case t.IsStruct():
		for _, f := range t.Fields() {
			if HasString(f.Type) {
				return true
			}
		}
	}
	return false
}

// HasSlice reports whether a value of type t holds a slice.
func HasSlice(t *Type) bool {
	switch {
	case t.IsSlice():
		return true
	case t.IsArray():
		return t.NumElem() > 0 && HasSlice(t.Elem())
	case t.IsStruct():
		for _, f := range t.Fields() {
			if HasSlice(f.Type) {
				return true
			}
		}
	}
	return false
}

// Has16 reports whether a value of type t holds values of 16 bytes,
// aligned to 16, that are loaded and stored with one 16-byte access where
// it is atomic: the strings (StringAligned16) and the slices
// (SliceAligned16; runtime/shared_string.go, doc/gd/slice16.md).
func Has16(t *Type) bool {
	return StringAligned16() && HasString(t) || SliceAligned16() && HasSlice(t)
}

// HasWhole reports whether a value of type t holds values that are
// loaded and stored whole by default, without the sharing analysis: the
// interfaces, and the strings and slices where they are aligned to 16
// (Has16).
func HasWhole(t *Type) bool {
	return HasIface(t) || Has16(t)
}
