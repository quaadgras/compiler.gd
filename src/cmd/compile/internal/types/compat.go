// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import "cmd/compile/internal/base"

// Compatible types (doc/gd/unsafe.md, runtime/checkptr_compat.go).
//
// (*T)(p), p an unsafe.Pointer, is refused if the memory at p has a
// pointer where T has none or the reverse, or a pointer to something
// that T's pointer there does not point to. The runtime compares T with
// the type that an object of the heap records. Where the compiler knows
// what p was made of, a pointer to S, it compares T with S: S is itself
// a type that the memory was found compatible with, so what S and T
// disagree on, the memory and T do too.

// A ViewCompat is why a view is not compatible with what it is of.
type ViewCompat uint8

const (
	ViewOK      ViewCompat = iota
	ViewPtrInt             // a pointer over an integer
	ViewIntPtr             // an integer over a pointer
	ViewPointee            // pointers to types that are not compatible, or other kinds
)

// What a word of a value is, for compatLeaf: as the runtime's.
const (
	compatScalar = iota
	compatPtr
	compatUnsafe
	compatFunc
	compatMap
	compatChan
	compatString
	compatSlice
	compatIface
	compatStruct
)

// compatLeaf returns what is at offset off of a value of type t: its
// kind, the type of the pointer or of the string, slice, interface or
// whole struct that the offset is in, the offset in that, and for what
// has no pointer, the number of bytes from off that have none.
func compatLeaf(t *Type, off int64) (kind int, leaf *Type, in, rest int64) {
	for {
		if !t.HasPointers() {
			return compatScalar, nil, 0, t.Size() - off
		}
		switch {
		case t.IsPtr():
			if t.Elem().Size() == 0 {
				return compatUnsafe, t, off, 0
			}
			return compatPtr, t, off, 0
		case t.IsUnsafePtr():
			return compatUnsafe, t, off, 0
		case t.Kind() == TFUNC:
			return compatFunc, t, off, 0
		case t.IsMap():
			return compatMap, t, off, 0
		case t.IsChan():
			return compatChan, t, off, 0
		case t.IsString():
			return compatString, t, off, 0
		case t.IsSlice():
			return compatSlice, t, off, 0
		case t.IsInterface():
			return compatIface, t, off, 0
		case t.IsArray():
			es := t.Elem().Size()
			if es == 0 {
				return compatScalar, nil, 0, t.Size() - off
			}
			off %= es
			t = t.Elem()
		case t.IsStruct():
			if IsWholeStruct(t) {
				return compatStruct, t, off, 0
			}
			if isABIInterface(t) {
				return compatIface, t, off, 0
			}
			var in *Field
			next := t.Size() // where the field after off begins
			for _, f := range t.Fields() {
				if f.Type.Size() > 0 && f.Offset <= off && off < f.Offset+f.Type.Size() {
					in = f
					break
				}
				if f.Offset > off {
					next = min(next, f.Offset)
				}
			}
			if in == nil {
				return compatScalar, nil, 0, next - off
			}
			off -= in.Offset
			t = in.Type
		default:
			return compatScalar, nil, 0, t.Size() - off
		}
	}
}

// compatSeen is the pairs of types that are being compared.
type compatSeen [][2]*Type

// compatBytes reports whether t is bytes: a pointer to them says nothing
// of what the memory is, since bytes may be over anything where they
// are only loaded, and in C.
func compatBytes(t *Type) bool {
	return !t.HasPointers() && t.Alignment() == 1
}

// compatLeaves is the runtime's: whether a word that is (vk, vt, vin) to
// the type converted to may be over one that is (mk, mt, min) to the
// memory, or, if view is not set, whether the two agree both ways.
func compatLeaves(gd *base.Invocation, mk int, mt *Type, min int64, vk int, vt *Type, vin int64, view bool, seen *compatSeen) ViewCompat {
	if mk == compatScalar || vk == compatScalar {
		switch {
		case mk == vk:
			return ViewOK
		case mk == compatScalar:
			return ViewPtrInt
		}
		return ViewIntPtr
	}
	if view && vk == compatUnsafe {
		switch mk {
		case compatPtr, compatUnsafe, compatFunc, compatMap, compatChan:
			return ViewOK
		}
		return ViewPointee
	}
	if mk == compatMap && vk == compatPtr && compatIsMap(vt.Elem()) || vk == compatMap && mk == compatPtr && compatIsMap(mt.Elem()) {
		// A map is a pointer to the runtime's map.
		return ViewOK
	}
	if mk != vk || min != vin {
		return ViewPointee
	}
	if mt == vt || Identical(mt, vt) {
		return ViewOK
	}
	ok := false
	switch mk {
	case compatUnsafe, compatString, compatStruct:
		ok = true
	case compatPtr:
		ok = compatWords(gd, mt.Elem(), vt.Elem(), false, seen)
	case compatFunc:
		ok = compatFuncs(mt, vt)
	case compatMap:
		ok = Identical(mt.Key(), vt.Key()) && Identical(mt.Elem(), vt.Elem())
	case compatChan:
		ok = Identical(mt.Elem(), vt.Elem())
	case compatSlice:
		ok = compatWords(gd, mt.Elem(), vt.Elem(), view, seen)
	case compatIface:
		ok = mt.IsStruct() || vt.IsStruct() || mt.IsEmptyInterface() && vt.IsEmptyInterface()
	}
	if ok {
		return ViewOK
	}
	return ViewPointee
}

// compatIsMap reports whether t is internal/runtime/maps.Map.
func compatIsMap(t *Type) bool {
	s := t.Sym()
	return s != nil && s.Name == "Map" && s.Pkg != nil && s.Pkg.Path == "internal/runtime/maps"
}

func compatFuncs(a, b *Type) bool {
	if a.NumParams() != b.NumParams() || a.NumResults() != b.NumResults() || a.NumRecvs() != b.NumRecvs() {
		return false
	}
	for i, f := range a.RecvParamsResults() {
		if !Identical(f.Type, b.RecvParamsResults()[i].Type) {
			return false
		}
	}
	return true
}

// compatWords reports whether a value of type v may be over a value of
// type m, which is the same size.
func compatWords(gd *base.Invocation, m, v *Type, view bool, seen *compatSeen) bool {
	if m == v || Identical(m, v) {
		return true
	}
	if m.HasShape() || v.HasShape() {
		// A shape stands for the types that have it: not known.
		return true
	}
	CalcSize(gd, m)
	CalcSize(gd, v)
	if m.Size() != v.Size() {
		return false
	}
	if !m.HasPointers() && !v.HasPointers() {
		return true
	}
	for _, p := range *seen {
		if p[0] == m && p[1] == v || !view && p[0] == v && p[1] == m {
			return true
		}
	}
	if len(*seen) >= 32 {
		return false
	}
	if m.IsArray() && v.IsArray() && m.Elem().Size() == v.Elem().Size() {
		return compatWords(gd, m.Elem(), v.Elem(), view, seen)
	}
	*seen = append(*seen, [2]*Type{m, v})
	ok := true
	ptr := int64(PtrSize)
	for w := int64(0); w < m.Size(); {
		mk, mt, min, mrest := compatLeaf(m, w)
		vk, vt, vin, vrest := compatLeaf(v, w)
		if compatLeaves(gd, mk, mt, min, vk, vt, vin, view, seen) != ViewOK {
			ok = false
			break
		}
		next := w + ptr
		if mk == compatScalar && vk == compatScalar {
			next = max(next, (w+min64(mrest, vrest))&^(ptr-1))
		}
		w = next
	}
	*seen = (*seen)[:len(*seen)-1]
	return ok
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// CompatView reports whether a value of type view, of which size bytes
// are used, at offset off of a value of type src, is known not to be
// compatible with the memory, given that src is: it returns why, or
// ViewOK if nothing says so. Only the bytes of src are looked at. A src
// that is bytes says nothing of the memory, and neither does a word
// that it has an unsafe.Pointer in, but that it holds a pointer.
//
// If load is set the view is only loaded through, and may have integers
// where the memory has pointers.
func CompatView(gd *base.Invocation, src *Type, off int64, view *Type, size int64, load bool) ViewCompat {
	if size <= 0 || src.HasShape() || view.HasShape() || compatBytes(src) {
		return ViewOK
	}
	if !view.HasPointers() && (load || !src.HasPointers()) {
		return ViewOK
	}
	ptr := int64(PtrSize)
	lo, hi := max(off, 0), min64(src.Size(), off+size)
	aligned := off&(ptr-1) == 0
	var seen compatSeen
	for w := lo &^ (ptr - 1); w < hi; {
		mk, mt, min, mrest := compatLeaf(src, w)
		vk, vt, vin, vrest := compatScalar, (*Type)(nil), int64(0), ptr
		if aligned && w >= off {
			vk, vt, vin, vrest = compatLeaf(view, (w-off)%view.Size())
		}
		next := w + ptr
		switch {
		case mk == compatScalar && vk == compatScalar:
			next = max(next, (w+min64(mrest, vrest))&^(ptr-1))
		case mk == compatUnsafe && (vk == compatPtr || vk == compatFunc || vk == compatMap || vk == compatChan):
			// What the memory has there is a pointer, of a type
			// that src does not say.
		case mk >= compatString && mk != vk:
			// A view of part of a string, a slice or an interface:
			// PartialView's to say.
		case load && vk == compatScalar:
			// A pointer that is loaded as an integer.
		default:
			if why := compatLeaves(gd, mk, mt, min, vk, vt, vin, true, &seen); why != ViewOK {
				return why
			}
		}
		w = next
	}
	return ViewOK
}
