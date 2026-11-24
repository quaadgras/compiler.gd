// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

// A WholeKind is a kind of value that is only read or written whole,
// because it is loaded and stored as a unit where goroutines may race on
// it (doc/gd/unsafe.md). A pointer conversion must not make a view of
// part of one.
type WholeKind uint8

const (
	NotWhole    WholeKind = iota
	WholeString           // a string
	WholeSlice            // a slice, of any element type
	WholeIface            // an interface, or internal/abi's view of one
	WholeStruct           // a struct that goes whole: IsWholeStruct
)

func (k WholeKind) String() string {
	switch k {
	case WholeString:
		return "string"
	case WholeSlice:
		return "slice"
	case WholeIface:
		return "interface"
	case WholeStruct:
		return "whole struct"
	}
	return "none"
}

// WholeKindOf returns the kind of whole value that t itself is.
func WholeKindOf(t *Type) WholeKind {
	switch {
	case t.IsString():
		return WholeString
	case t.IsSlice():
		return WholeSlice
	case t.IsInterface(), isABIInterface(t):
		return WholeIface
	case IsWholeStruct(t), isWholeShape(t):
		return WholeStruct
	}
	return NotWhole
}

// isABIInterface reports whether t is internal/abi's description of an
// interface, which is the same words, or the shape of one, which is what
// generic code has for it.
func isABIInterface(t *Type) bool {
	if !t.IsStruct() {
		return false
	}
	if s := t.Sym(); s != nil && s.Pkg != nil && s.Pkg.Path == "internal/abi" {
		switch s.Name {
		case "EmptyInterface", "NonEmptyInterface", "CommonInterface":
			return true
		}
	}
	if !t.IsShape() || t.NumFields() != 3 {
		return false
	}
	f := t.Fields()
	if !f[1].Type.IsUnsafePtr() || f[2].Type.Kind() != TCOMPLEX128 || f[1].Sym.Name != "Data" || f[2].Sym.Name != "Inline" {
		return false
	}
	if f[0].Sym.Name == "_" {
		return f[0].Sym.Pkg != nil && f[0].Sym.Pkg.Path == "internal/abi" && f[0].Type.IsUnsafePtr()
	}
	if !f[0].Type.IsPtr() {
		return false
	}
	e := f[0].Type.Elem().Sym()
	return e != nil && e.Pkg != nil && e.Pkg.Path == "internal/abi" && (e.Name == "Type" || e.Name == "ITab")
}

// isWholeShape reports whether t is the shape of reflect.Value.
func isWholeShape(t *Type) bool {
	if !t.IsShape() || !t.IsStruct() || t.NumFields() != 4 {
		return false
	}
	f := t.Fields()
	return f[0].Sym.Name == "typ_" && f[1].Sym.Name == "ptr" && f[0].Sym.Pkg != nil && f[0].Sym.Pkg.Path == "reflect"
}

// holdsWhole reports whether a value of type t holds a whole value.
func holdsWhole(t *Type) bool {
	if WholeKindOf(t) != NotWhole {
		return true
	}
	switch {
	case t.IsArray():
		return t.NumElem() > 0 && holdsWhole(t.Elem())
	case t.IsStruct():
		for _, f := range t.Fields() {
			if holdsWhole(f.Type) {
				return true
			}
		}
	}
	return false
}

// wholeAt returns the kind and the size of the whole value that begins at
// offset off of a value of type t, if one does.
func wholeAt(t *Type, off int64) (WholeKind, int64) {
	for {
		if off < 0 || off >= t.Size() {
			return NotWhole, 0
		}
		if k := WholeKindOf(t); k != NotWhole {
			if off != 0 {
				return NotWhole, 0
			}
			return k, t.Size()
		}
		switch {
		case t.IsArray():
			es := t.Elem().Size()
			if es == 0 {
				return NotWhole, 0
			}
			off %= es
			t = t.Elem()
		case t.IsStruct():
			var in *Field
			for _, f := range t.Fields() {
				if f.Type.Size() > 0 && f.Offset <= off && off < f.Offset+f.Type.Size() {
					in = f
					break
				}
			}
			if in == nil {
				return NotWhole, 0
			}
			off -= in.Offset
			t = in.Type
		default:
			return NotWhole, 0
		}
	}
}

// PartialView reports whether a value of type view, of which size bytes
// are used, at offset off of a value of type src, is a partial view of a
// whole value of src: one it overlaps that it does not cover with a value
// of the same kind at the same offset. It returns the kind of that value,
// or NotWhole. Where a slice is stock Go's three words, a string is its
// first two, and the two may view each other (!SlicePacked). Only the
// bytes of src are looked at: what is beyond them is not known.
func PartialView(src *Type, off int64, view *Type, size int64) WholeKind {
	if size <= 0 {
		return NotWhole
	}
	return partialIn(src, 0, off, off+size, view)
}

func partialIn(t *Type, base, lo, hi int64, view *Type) WholeKind {
	size := t.Size()
	if size == 0 || base >= hi || base+size <= lo || !holdsWhole(t) {
		return NotWhole
	}
	if k := WholeKindOf(t); k != NotWhole {
		if base < lo {
			return k
		}
		vk, vsize := wholeAt(view, base-lo)
		if !SlicePacked() && (k == WholeSlice && vk == WholeString || k == WholeString && vk == WholeSlice) {
			return NotWhole
		}
		if vk != k || vsize != size || base+size > hi {
			return k
		}
		return NotWhole
	}
	switch {
	case t.IsArray():
		es := t.Elem().Size()
		if es == 0 {
			return NotWhole
		}
		first := max(lo-base, 0) / es
		for i := first; i < t.NumElem() && base+i*es < hi; i++ {
			if k := partialIn(t.Elem(), base+i*es, lo, hi, view); k != NotWhole {
				return k
			}
		}
	case t.IsStruct():
		for _, f := range t.Fields() {
			if k := partialIn(f.Type, base+f.Offset, lo, hi, view); k != NotWhole {
				return k
			}
		}
	}
	return NotWhole
}
