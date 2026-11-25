// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"internal/goarch"
	"unsafe"
)

// Compatible types (doc/gd/unsafe.md).
//
// Code that uses unsafe may take the bytes of memory for whatever it
// likes, but not its pointers: (*T)(p) is refused if a store through the
// result, or a load, could make a pointer of what is not one, or a
// pointer to a T of what points to something else. A conversion cannot
// tell a load from a store, so the type converted to and the type of the
// memory must agree, word by word, on what a word is:
//
//   - where one has no pointer the other has none: an integer stored over
//     a pointer is a pointer that the program made up, when it is loaded
//     again, and an integer loaded as a pointer is one already. Bytes
//     are integers in this. (Where the compiler knows that the result is
//     only loaded through, integers may be over pointers. And in a
//     package that is C translated by cmd/cc bytes may be over anything:
//     C looks at everything as bytes, and its stores of them are to be
//     checked by themselves.)
//   - a *B is over a *A only if A and B are the same size and agree in
//     the same way, both ways, since a *B that is stored there is loaded
//     as a *A by the code that the memory is A's to;
//   - an unsafe.Pointer, or a pointer to what has no size, which is what
//     C's void * is, may be over any pointer: what is loaded is converted
//     again before it is used, and checked then. The reverse is refused:
//     a *B over an unsafe.Pointer would be a conversion with no check;
//   - a func, a map or a chan is over one of the same type only, a string
//     over a string, a slice over a slice whose elements are of the same
//     size and agree, an interface over one of the same type.

// What a word of a value is, for compatLeaf.
const (
	compatScalar = iota // no pointer that the collector looks at
	compatPtr           // a pointer to a type that has a size
	compatUnsafe        // an unsafe.Pointer, or a pointer to what has no size
	compatFunc
	compatMap
	compatChan
	compatString
	compatSlice
	compatIface
	compatStruct // a struct that goes whole (isWholeStruct)
)

// compatLeaf returns what is at offset off of a value of type t: its
// kind, the type of the pointer or of the string, slice, interface or
// whole struct that the offset is in, and the offset in that.
func compatLeaf(t *_type, off uintptr) (kind uint8, leaf *_type, in uintptr) {
	for {
		if !t.Pointers() {
			return compatScalar, nil, 0
		}
		switch t.Kind() {
		case abi.Pointer:
			if (*ptrtype)(unsafe.Pointer(t)).Elem.Size_ == 0 {
				return compatUnsafe, t, off
			}
			return compatPtr, t, off
		case abi.UnsafePointer:
			return compatUnsafe, t, off
		case abi.Func:
			return compatFunc, t, off
		case abi.Map:
			return compatMap, t, off
		case abi.Chan:
			return compatChan, t, off
		case abi.String:
			return compatString, t, off
		case abi.Slice:
			return compatSlice, t, off
		case abi.Interface:
			return compatIface, t, off
		case abi.Array:
			a := (*arraytype)(unsafe.Pointer(t))
			if a.Len == 0 || a.Elem.Size_ == 0 {
				return compatScalar, nil, 0
			}
			off %= a.Elem.Size_
			t = a.Elem
		case abi.Struct:
			if isWholeStruct(t) {
				return compatStruct, t, off
			}
			if isABIInterface(t) {
				return compatIface, t, off
			}
			st := (*structtype)(unsafe.Pointer(t))
			var f *abi.StructField
			for i := range st.Fields {
				if g := &st.Fields[i]; g.Typ.Size_ != 0 && g.Offset <= off && off < g.Offset+g.Typ.Size_ {
					f = g
					break
				}
			}
			if f == nil {
				return compatScalar, nil, 0
			}
			off -= f.Offset
			t = f.Typ
		default:
			return compatScalar, nil, 0
		}
	}
}

// compatMaskBit reports whether the word at offset off of a value of type
// t, whose mask is mask, is a pointer that the collector looks at.
func compatMaskBit(t *_type, mask *byte, off uintptr) bool {
	w := off / goarch.PtrSize
	return off < t.PtrBytes && *addb(mask, w/8)>>(w%8)&1 != 0
}

// Why two words are not compatible.
const (
	compatOK      = iota
	compatPtrInt  // a pointer over an integer
	compatIntPtr  // an integer over a pointer
	compatPointee // pointers to types that are not compatible, or other kinds
)

// compatSeen is the pairs of types that are being compared: a type that
// points to itself is compatible with another if the rest of them is.
type compatSeen struct {
	n     int
	pairs [32][2]*_type
	c     bool // the conversion is C's: see compatLeaves
	// direct: the type converted to is itself a pointer to anything
	// (*unsafe.Pointer, C's void ** and char **), or an array of
	// them; or the conversion is one that is only loaded through.
	direct bool
	// cmem: the memory is an object that C allocated.
	cmem bool
}

// cDirect reports whether t is a pointer to anything, or an array of
// them: what a pointer to it is stored through is looked at where it is
// stored (cclaim.go's c23PtrStore, checkptrStore), so such a pointer may
// be over a pointer of a type. To C, if c is set, a pointer to a byte
// is a pointer to anything too.
func cDirect(t *_type, c bool) bool {
	for t.Kind() == abi.Array {
		t = (*arraytype)(unsafe.Pointer(t)).Elem
	}
	switch t.Kind() {
	case abi.UnsafePointer:
		return true
	case abi.Pointer:
		e := (*ptrtype)(unsafe.Pointer(t)).Elem
		return e.Size_ == 0 || c && e.Size_ == 1 && !e.Pointers()
	}
	return false
}

// cObjectIsC reports whether typ is what a package of C's allocates an
// object of the heap as: struct { v T; _ byte } (cmd/compile's
// ir.CObjType, internal/c23's padded). In such an object a pointer to a
// byte is C's, a pointer to anything.
func cObjectIsC(typ *_type) bool {
	if typ == nil || typ.Kind() != abi.Struct {
		return false
	}
	st := (*structtype)(unsafe.Pointer(typ))
	return len(st.Fields) == 2 && st.Fields[1].Typ.Kind() == abi.Uint8 && st.Fields[0].Name.Name() == "v" && st.Fields[1].Name.Name() == "_"
}

// compatBytePtr reports whether t, a pointer type, is a pointer to a
// byte: C's char *, which C keeps the address of anything in, as it
// does in a void *.
func compatBytePtr(t *_type) bool {
	elem := (*ptrtype)(unsafe.Pointer(t)).Elem
	return elem.Size_ == 1 && !elem.Pointers()
}

// compatLeaves reports whether a word that is (vk, vt, vin) to the
// converted type may be over one that is (mk, mt, min) to the memory. If
// view is not set the two are the types of what two pointers point to,
// and they must agree both ways.
//
// In a conversion of C's (seen.c) a pointer to a byte is a pointer to
// anything, as unsafe.Pointer is: what C loads from a void * as a char *
// is a pointer that it may look at the bytes of, and what it stores
// through either is checked where it stores (doc/gd/cc.md). The memory's
// own word is what its type says, unless C allocated the object
// (seen.cmem): a *byte of Go's is a pointer to a byte that is no part of
// a pointer.
//
// Only a pointer to a pointer to anything (seen.direct) is over a
// pointer of a type, in a conversion of Go's as in one of C's: a struct
// that has an unsafe.Pointer where the memory has a typed pointer would
// store any pointer there, with nothing looking.
func compatLeaves(mk uint8, mt *_type, min uintptr, vk uint8, vt *_type, vin uintptr, view bool, seen *compatSeen) int {
	if seen.c {
		if vk == compatPtr && compatBytePtr(vt) {
			vk = compatUnsafe
		}
		if (!view || seen.cmem) && mk == compatPtr && compatBytePtr(mt) {
			mk = compatUnsafe
		}
	}
	if mk == compatScalar || vk == compatScalar {
		switch {
		case mk == vk:
			return compatOK
		case mk == compatScalar:
			return compatPtrInt
		}
		return compatIntPtr
	}
	if view && vk == compatUnsafe {
		switch mk {
		case compatUnsafe:
			return compatOK
		case compatPtr, compatFunc, compatMap, compatChan:
			if seen.direct {
				return compatOK
			}
		}
		return compatPointee
	}
	if mk == compatMap && vk == compatPtr && compatIsMap((*ptrtype)(unsafe.Pointer(vt)).Elem) ||
		vk == compatMap && mk == compatPtr && compatIsMap((*ptrtype)(unsafe.Pointer(mt)).Elem) {
		// A map is a pointer to the runtime's map.
		return compatOK
	}
	if mk != vk || min != vin {
		return compatPointee
	}
	if mt == vt {
		return compatOK
	}
	ok := false
	switch mk {
	case compatUnsafe, compatString:
		ok = true
	case compatPtr:
		// (The shape of a pointer type says nothing of what it
		// points to: see compatShape.)
		ok = compatShape(mt) || compatShape(vt) ||
			compatWords((*ptrtype)(unsafe.Pointer(mt)).Elem, (*ptrtype)(unsafe.Pointer(vt)).Elem, false, seen)
	case compatFunc:
		ok = compatFuncs((*functype)(unsafe.Pointer(mt)), (*functype)(unsafe.Pointer(vt)))
	case compatMap:
		m, v := (*abi.MapType)(unsafe.Pointer(mt)), (*abi.MapType)(unsafe.Pointer(vt))
		ok = compatSameType(m.Key, v.Key) && compatSameType(m.Elem, v.Elem)
	case compatChan:
		ok = compatSameType((*chantype)(unsafe.Pointer(mt)).Elem, (*chantype)(unsafe.Pointer(vt)).Elem)
	case compatSlice:
		// The elements are reached through the slice, as the words
		// of this view are through its pointer.
		ok = compatWords((*slicetype)(unsafe.Pointer(mt)).Elem, (*slicetype)(unsafe.Pointer(vt)).Elem, view, seen)
	case compatIface:
		// internal/abi's structs are how the library looks at an
		// interface of any type.
		ok = mt.Kind() == abi.Struct || vt.Kind() == abi.Struct ||
			len((*interfacetype)(unsafe.Pointer(mt)).Methods) == len((*interfacetype)(unsafe.Pointer(vt)).Methods) &&
				(len((*interfacetype)(unsafe.Pointer(mt)).Methods) == 0 || compatShape(mt) || compatShape(vt))
	case compatStruct:
		// reflect.Value, a type defined from it, or its shape.
		ok = true
	}
	if ok {
		return compatOK
	}
	return compatPointee
}

// compatIsMap reports whether t is internal/runtime/maps.Map, which a
// map value points to.
func compatIsMap(t *_type) bool {
	return t.Kind() == abi.Struct && t.TFlag&abi.TFlagNamed != 0 && toRType(t).string() == "maps.Map" && toRType(t).pkgpath() == "internal/runtime/maps"
}

// compatFuncs reports whether two func types have the same parameters
// and results.
func compatFuncs(a, b *functype) bool {
	if a.InCount != b.InCount || a.OutCount != b.OutCount {
		return false
	}
	ai, bi := a.InSlice(), b.InSlice()
	for i := range ai {
		if !compatSameType(ai[i], bi[i]) {
			return false
		}
	}
	ao, bo := a.OutSlice(), b.OutSlice()
	for i := range ao {
		if !compatSameType(ao[i], bo[i]) {
			return false
		}
	}
	return true
}

// compatShape reports whether t is the shape of a type: what generic
// code has for a type that it is given. A variable of such code that is
// moved to the heap is allocated as its shape, in which an int32 is
// whatever has an int32 under it, and every pointer is a *uint8. So a
// shape is compared for what it does say, its layout and its kinds, and
// what a pointer of a shape points to is not known.
func compatShape(t *_type) bool {
	const prefix = "go.shape."
	s := toRType(t).string()
	return len(s) > len(prefix) && s[:len(prefix)] == prefix
}

// compatSameType reports whether a and b are the same type, as the key
// of a map, the element of a chan or a parameter of a func is to be, or
// one is a shape that the other may have.
func compatSameType(a, b *_type) bool {
	return a == b || (compatShape(a) || compatShape(b)) && a.Kind() == b.Kind() && a.Size_ == b.Size_ && a.PtrBytes == b.PtrBytes
}

// compatWords reports whether a value of type v may be over a value of
// type m, which is the same size: they agree on every word (compatLeaves).
func compatWords(m, v *_type, view bool, seen *compatSeen) bool {
	if m == v {
		return true
	}
	if m.Size_ != v.Size_ {
		return false
	}
	if !m.Pointers() && !v.Pointers() {
		return true
	}
	for i := 0; i < seen.n; i++ {
		if p := &seen.pairs[i]; p[0] == m && p[1] == v || !view && p[0] == v && p[1] == m {
			return true
		}
	}
	if seen.n == len(seen.pairs) {
		return false
	}
	if m.Kind() == abi.Array && v.Kind() == abi.Array {
		if me, ve := (*arraytype)(unsafe.Pointer(m)).Elem, (*arraytype)(unsafe.Pointer(v)).Elem; me.Size_ == ve.Size_ {
			return compatWords(me, ve, view, seen)
		}
	}
	seen.pairs[seen.n] = [2]*_type{m, v}
	seen.n++
	mmask, vmask := getGCMask(m), getGCMask(v)
	ok := true
	for w := uintptr(0); w < max(m.PtrBytes, v.PtrBytes); w += goarch.PtrSize {
		if !compatMaskBit(m, mmask, w) && !compatMaskBit(v, vmask, w) {
			continue
		}
		mk, mt, min := compatLeaf(m, w)
		vk, vt, vin := compatLeaf(v, w)
		if compatLeaves(mk, mt, min, vk, vt, vin, view, seen) != compatOK {
			ok = false
			break
		}
	}
	seen.n--
	return ok
}

// checkptrCompat checks that the view of size bytes at p, of values of
// type elem, is compatible with the object that ends at limit, which
// holds values of type typ from tbase on.
//
// If c is set the conversion is one of a package of C's (compatLeaves).
func checkptrCompat(typ *_type, tbase, limit, p uintptr, elem *_type, size uintptr, load, c bool) {
	checkptrCompatMem(typ, tbase, limit, p, elem, size, load, c, c && cObjectIsC(typ))
}

// checkptrCompatMem is checkptrCompat, with cmem set if the memory is
// C's own: an object that C allocated, or a variable, which a
// conversion of C's is taken to be in a variable of C's.
func checkptrCompatMem(typ *_type, tbase, limit, p uintptr, elem *_type, size uintptr, load, c, cmem bool) {
	if size == 0 || p < tbase || p >= limit {
		return
	}
	if !elem.Pointers() && (load || !typ.Pointers()) {
		return
	}
	const word = goarch.PtrSize
	lo := p - tbase
	if size > limit-p {
		// A type longer than the object: as checkptrWholeIn.
		limit = min(limit, tbase+(lo/typ.Size_+1)*typ.Size_)
		size = limit - p
	}
	mmask, vmask := getGCMask(typ), getGCMask(elem)
	aligned := lo&(word-1) == 0
	var seen compatSeen
	seen.c = c
	seen.direct = load || cDirect(elem, c)
	seen.cmem = cmem
	for w := lo &^ (word - 1); w < lo+size; w += word {
		mo := w % typ.Size_
		mp := compatMaskBit(typ, mmask, mo)
		var vo uintptr
		vp := false
		if aligned {
			vo = (w - lo) % elem.Size_
			vp = compatMaskBit(elem, vmask, vo)
		}
		if !mp && !vp {
			continue
		}
		mk, mt, min := compatLeaf(typ, mo)
		vk, vt, vin := uint8(compatScalar), (*_type)(nil), uintptr(0)
		if aligned {
			vk, vt, vin = compatLeaf(elem, vo)
		}
		if load && vk == compatScalar {
			// A pointer that is loaded as an integer.
			continue
		}
		if why := compatLeaves(mk, mt, min, vk, vt, vin, true, &seen); why != compatOK {
			checkptrIncompatible(why, elem, typ, mo)
		}
	}
}

const (
	checkptrPtrIntMsg  = "checkptr: converted pointer type has a pointer where the memory has an integer"
	checkptrIntPtrMsg  = "checkptr: converted pointer type has an integer where the memory has a pointer"
	checkptrPointeeMsg = "checkptr: converted pointer type has a pointer that is not compatible with the memory's"
)

// checkptrIncompatible stops the program for a conversion to a pointer
// to elem of a pointer into memory that is typ's, or nil's if it has no
// type, at offset off.
func checkptrIncompatible(why int, elem, typ *_type, off uintptr) {
	if mp := getg().m; mp.cprobe {
		mp.cprobeFail = true
		return
	}
	var buf [20]byte
	msg := checkptrWhy(why) + ": *" + toRType(elem).string() + ", at offset " + string(itoa(buf[:], uint64(off)))
	if typ != nil {
		msg += " of the memory's " + toRType(typ).string()
	}
	throw(msg)
}

func checkptrWhy(why int) string {
	switch why {
	case compatPtrInt:
		return checkptrPtrIntMsg
	case compatIntPtr:
		return checkptrIntPtrMsg
	}
	return checkptrPointeeMsg
}

// checkptrIncompat is called where the compiler knows that a conversion
// is to a type that is not compatible with what p points into
// (cmd/compile/internal/types.CompatView), if p is not nil. As
// checkptrPartial, it cannot move the stack.
//
//go:nosplit
func checkptrIncompat(p unsafe.Pointer, why int) {
	if p != nil {
		throw(checkptrWhy(why))
	}
}

// checkptrPointers checks that (*[n]elem)(p), which is in one object of
// the span s that has no type to compare with (one without pointers, or
// a small object of a type that has no number: mobjtypes.go), has
// pointers where the object has them and nowhere else: all that the
// object's bitmap says.
func checkptrPointers(s *mspan, p uintptr, elem *_type, n uintptr, load bool) {
	if s.spanclass.c() || !elem.Pointers() && load {
		return
	}
	const word = goarch.PtrSize
	// (No further than the object, for a type that is longer.)
	size := min(n*elem.Size_, s.base()+(s.objIndex(p)+1)*s.elemsize-p)
	if size == 0 || s.isFree(s.objIndex(p)) {
		// (A pointer kept from before: the bitmap is not its object's.)
		return
	}
	if s.spanclass.noscan() {
		if elem.Pointers() {
			checkptrIncompatible(compatPtrInt, elem, nil, 0)
		}
		return
	}
	lo, hi := alignDown(p, word), alignUp(p+size, word)
	tp := s.typePointersOf(lo, hi-lo)
	tp, slot := tp.next(hi)
	aligned := p&(word-1) == 0
	mask := getGCMask(elem)
	for a := lo; a < hi; a += word {
		for slot != 0 && slot < a {
			tp, slot = tp.next(hi)
		}
		vp := aligned && compatMaskBit(elem, mask, (a-p)%elem.Size_)
		if vp == (slot == a) {
			continue
		}
		// The first word of an interface is one that the collector
		// does not look at, and internal/abi's structs have a
		// pointer for it: one may be over the other.
		if aligned {
			if k, _, in := compatLeaf(elem, (a-p)%elem.Size_); k == compatIface && in == 0 {
				if bits := s.wholeBitsOf(); bits != nil && bits.state((a-s.base())/word) == wordFirst {
					continue
				}
			}
		}
		if !vp {
			if load {
				continue
			}
			checkptrIncompatible(compatIntPtr, elem, nil, a-p)
		}
		checkptrIncompatible(compatPtrInt, elem, nil, a-p)
	}
}
