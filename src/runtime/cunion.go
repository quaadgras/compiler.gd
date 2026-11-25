// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"internal/goarch"
	"internal/runtime/atomic"
	"unsafe"
)

// The values of C's unions (doc/gd/cc.md, "Unions").
//
// A union of C's that has a pointer in a member is, where it is, its
// bytes, in which its members without pointers are, and one pointer.
// Whatever of it has a pointer is in an object of the heap of its own,
// a union value, which that pointer points to: so no word is an integer
// to one member and a pointer to another, and a pointer that was taken
// to a member is to an object that stays what it was when the union is
// used for something else and gets another value.
//
// A union value is an object of C's spans (cspan.go). It begins with a
// header of two words, what it is the value of:
//
//	typ  *_type          // the type of the value that follows
//	back unsafe.Pointer  // the pointer of the union that it is the value of
//
// and the value follows, its words claimed for what the type has there
// (cclaim.go), as malloc that is told the type claims them. The first
// word's claim says that the object is a union value (cUnionValueID).
// The back reference is how an access through a union knows that the
// value is its own, and not one that came in a copy of the union's
// bytes; and how a pointer to the value finds its union again.

// cUnionHeader is the size of the header of a union value.
const cUnionHeader = 2 * goarch.PtrSize

// cUnionValueID is the id of the claim of the first word of a union
// value, which holds its type: no type has it.
const cUnionValueID = ^uint32(0)

// cUnionHdr is the header of a union value.
type cUnionHdr struct {
	typ  *_type
	back unsafe.Pointer
}

// cUnionNew allocates a union value of typ, zero, for the union whose
// pointer is at back.
func cUnionNew(typ *_type, back unsafe.Pointer) unsafe.Pointer {
	const word = goarch.PtrSize
	x := mallocC(cUnionHeader + typ.Size_)
	if x == nil {
		throw("out of memory")
	}
	s := spanOfUnchecked(uintptr(x))
	b := s.cBitsOf()
	w := (uintptr(x) - s.base()) / word
	ids := s.cIDsOf()
	if ids == nil {
		ids = s.newCIDs()
	}
	atomic.Store(ids.word(w), cUnionValueID)
	cClassSet(b, w, 1, cTyped)
	cClassSet(b, w+1, 1, cUntyped)
	cClaimValues(s, b, w+cUnionHeader/word, typ, typ.Size_)
	h := (*cUnionHdr)(x)
	h.typ, h.back = typ, back
	publicationBarrier()
	return x
}

// cUnionValue returns the header of the union value that p points to
// the beginning of, or nil if it points to none.
func cUnionValue(p unsafe.Pointer) *cUnionHdr {
	return cUnionValueAt(uintptr(p))
}

// cUnionValueAt is cUnionValue for an address that may be of nothing,
// as the one a header's size before a pointer is: it is an integer
// until it is known to be a union value's. As a pointer it would be
// one that the collector marks an object for, when it looks at the
// frame, that is not there: in what is after the last object of the
// span before, a mark that no object has.
func cUnionValueAt(a uintptr) *cUnionHdr {
	s := cSpanOf(a)
	if s == nil {
		return nil
	}
	b, ids := s.cBitsOf(), s.cIDsOf()
	if b == nil || ids == nil {
		return nil
	}
	i := s.objIndex(a)
	if a != s.base()+i*s.elemsize || s.isFree(i) {
		return nil
	}
	w := (a - s.base()) / goarch.PtrSize
	if cClass(b, w) != cTyped || atomic.Load(ids.word(w)) != cUnionValueID {
		return nil
	}
	return (*cUnionHdr)(unsafe.Pointer(a))
}

// c23UnionBox returns the value of type typ of the union whose pointer
// is at slot: what the pointer points to, if that is the union's own
// value of that type, and otherwise a new value that the union gets:
//
//   - a copy of the one it has, if that is of typ and another union's:
//     it came in a copy of the union's bytes (memcpy, realloc);
//   - the pointer that it has, converted as a pointer of C's is, if it
//     has a pointer and typ is one;
//   - and otherwise zero.
//
// internal/c23's UnionBox makes the first comparison itself.
//
//go:linkname c23UnionBox
func c23UnionBox(slot *unsafe.Pointer, typ *_type) unsafe.Pointer {
	old := cUnionValue(*slot)
	if old != nil && old.typ == typ && old.back == unsafe.Pointer(slot) {
		return add(unsafe.Pointer(old), cUnionHeader)
	}
	x := cUnionNew(typ, unsafe.Pointer(slot))
	v := add(x, cUnionHeader)
	if old != nil {
		ov := add(unsafe.Pointer(old), cUnionHeader)
		switch {
		case old.typ == typ:
			c23Memmove(v, ov, typ.Size_)
		case cIsPointer(old.typ) && cIsPointer(typ):
			if p := *(*unsafe.Pointer)(ov); p != nil {
				if typ.Kind() == abi.Pointer {
					if elem := (*ptrtype)(unsafe.Pointer(typ)).Elem; elem.Size_ != 0 {
						p = checkptrConvC1(p, elem, false)
					}
				}
				*(*unsafe.Pointer)(v) = p
			}
		}
	}
	// The word that points to the value is one that the collector
	// looks at from here on, if nothing said so yet: the library
	// gives a union of malloc's memory its value before C converts a
	// pointer to it.
	if s := cSpanOf(uintptr(unsafe.Pointer(slot))); s != nil {
		if b := s.cBitsOf(); b != nil {
			if w := (uintptr(unsafe.Pointer(slot)) - s.base()) / goarch.PtrSize; cClass(b, w) == cUnclaimed {
				cClassSet(b, w, 1, cUntyped)
			}
		}
	}
	*slot = x
	return v
}

// cIsPointer reports whether t is a pointer type.
func cIsPointer(t *_type) bool {
	return t.Kind() == abi.Pointer || t.Kind() == abi.UnsafePointer
}

// c23UnionClone is for a union that was copied whole, as part of a
// struct that was assigned: the copy, whose pointer is at slot, gets a
// value of its own now, as it would when it is next used.
//
//go:linkname c23UnionClone
func c23UnionClone(slot *unsafe.Pointer) {
	if old := cUnionValue(*slot); old != nil && old.back != unsafe.Pointer(slot) {
		c23UnionBox(slot, old.typ)
	}
}

// c23UnionPointer returns the pointer that the union value at x holds,
// if it is the value of a pointer: for internal/c23's functions that are
// given a union by value.
//
//go:linkname c23UnionPointer
func c23UnionPointer(x unsafe.Pointer) (unsafe.Pointer, bool) {
	h := cUnionValue(x)
	if h == nil || !cIsPointer(h.typ) {
		return nil, false
	}
	return *(*unsafe.Pointer)(add(x, cUnionHeader)), true
}

// Where the unions are.
//
// A pointer with no type that C converts to a pointer to a type with
// pointers may be the address of a union, whose member of that type is
// not at that address but in the union's value. So the runtime knows
// where the unions are in what it has a type or claims for: in an
// object of Go's, from the object's type, in which a union is a struct
// whose last field is internal/c23's UnionRef; and in an object of C's
// from the claims of the union's first word and of its UnionRef, which
// have ids that no type has (cUnionStartID, cUnionRefID) and the number
// of words of the union's bytes. A union in a variable of the stack or
// in the program's data is not known here.

// cUnionRefType is internal/c23's UnionRef, the type of the pointer of
// a union to its value: nil in a program that has no C.
var cUnionRefType *_type

// c23SetUnionRef tells the runtime the type of a union's pointer.
//
//go:linkname c23SetUnionRef
func c23SetUnionRef(t *_type) { cUnionRefType = t }

// cIntcapType is internal/c23's Intcap, an intptr_t or a uintptr_t of
// C's in memory: nil in a program that has no C. cCapID is the id of a
// word that is claimed for one.
var (
	cIntcapType *_type
	cCapID      uint32
)

// c23SetIntcap tells the runtime the type of an intptr_t in memory.
//
//go:linkname c23SetIntcap
func c23SetIntcap(t *_type) {
	cIntcapType = t
	_, cCapID = cWant(compatCap, t, 0, true)
}

// The ids of the claims of a union's first word, which is claimed for
// no pointer, and of its UnionRef, which is claimed for a pointer of a
// type: with the number of words of the union's bytes in the low bits.
const (
	cUnionStartID = 0x80000000
	cUnionRefID   = 0xC0000000
	cUnionWords   = 0x3FFFFFFF
)

// cUnionMirror reports whether t is what Go has for a union with
// pointers, and returns the number of bytes before its UnionRef.
func cUnionMirror(t *_type) (uintptr, bool) {
	if cUnionRefType == nil || t.Kind() != abi.Struct {
		return 0, false
	}
	st := (*structtype)(unsafe.Pointer(t))
	if n := len(st.Fields); n > 0 && st.Fields[n-1].Typ == cUnionRefType {
		return st.Fields[n-1].Offset, true
	}
	return 0, false
}

// What cUnionIn finds at an offset.
const (
	cUnionNone  = iota
	cUnionStart // the first of the bytes of a union that has bytes
	cUnionRef   // the UnionRef of a union
)

// cUnionIn reports whether a union begins, or has its UnionRef, at
// offset off of a value of type t, and returns the number of bytes
// before the UnionRef.
func cUnionIn(t *_type, off uintptr) (int, uintptr) {
	for {
		switch t.Kind() {
		case abi.Array:
			a := (*arraytype)(unsafe.Pointer(t))
			if a.Len == 0 || a.Elem.Size_ == 0 {
				return cUnionNone, 0
			}
			off %= a.Elem.Size_
			t = a.Elem
		case abi.Struct:
			if n, ok := cUnionMirror(t); ok {
				switch off {
				case n:
					return cUnionRef, n
				case 0:
					return cUnionStart, n
				}
				return cUnionNone, 0
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
				return cUnionNone, 0
			}
			off -= f.Offset
			t = f.Typ
		default:
			return cUnionNone, 0
		}
	}
}

// cUnionID returns the id of the claim of the word at offset off of a
// value of type t, if a union begins or has its UnionRef there, and
// the class that goes with it; 0 otherwise.
func cUnionID(t *_type, off uintptr) (class, id uint32) {
	if cUnionRefType == nil {
		return 0, 0
	}
	switch k, n := cUnionIn(t, off); k {
	case cUnionStart:
		return cScalar, cUnionStartID | uint32(n/goarch.PtrSize)
	case cUnionRef:
		return cTyped, cUnionRefID | uint32(n/goarch.PtrSize)
	}
	return 0, 0
}

// cUnionAt returns the address of the UnionRef of the union that begins
// at a, or nil if no union is known to begin there.
func cUnionAt(a uintptr) *unsafe.Pointer {
	const word = goarch.PtrSize
	s := spanOfHeap(a)
	if s == nil {
		// A variable of the program's data that the runtime was
		// told of?
		if g, ok := cUnionGlobalAt(a); ok {
			switch k, n := cUnionIn(g.typ, a-g.base); {
			case k == cUnionStart:
				return (*unsafe.Pointer)(unsafe.Pointer(a + n))
			case k == cUnionRef && n == 0:
				return (*unsafe.Pointer)(unsafe.Pointer(a))
			}
		}
		return nil
	}
	if a%word != 0 || s.isUserArenaChunk {
		return nil
	}
	i := s.objIndex(a)
	if s.isFree(i) {
		return nil
	}
	if s.spanclass.c() {
		b, ids := s.cBitsOf(), s.cIDsOf()
		if b == nil || ids == nil {
			return nil
		}
		w := (a - s.base()) / word
		id := atomic.Load(ids.word(w))
		if id == cUnionValueID || id < cUnionStartID {
			return nil
		}
		n := uintptr(id & cUnionWords)
		if id >= cUnionRefID {
			if n != 0 || cClass(b, w) != cTyped {
				return nil
			}
			return (*unsafe.Pointer)(unsafe.Pointer(a))
		}
		// The first of its bytes: its UnionRef says the same.
		if cClass(b, w+n) != cTyped || atomic.Load(ids.word(w+n)) != cUnionRefID|uint32(n) {
			return nil
		}
		return (*unsafe.Pointer)(unsafe.Pointer(a + n*word))
	}
	base := s.base() + i*s.elemsize
	typ, tbase := checkptrObjectType(s, base)
	if typ == nil || a < tbase || typ.Size_ == 0 {
		return nil
	}
	switch k, n := cUnionIn(typ, (a-tbase)%typ.Size_); {
	case k == cUnionStart:
		return (*unsafe.Pointer)(unsafe.Pointer(a + n))
	case k == cUnionRef && n == 0:
		return (*unsafe.Pointer)(unsafe.Pointer(a))
	}
	return nil
}

// cViewOK reports whether a value of type v may be looked at where a
// value of type m is: checkptr_compat.go's rule, for every word of v.
func cViewOK(m, v *_type) bool {
	if m == v {
		return true
	}
	if v.Size_ > m.Size_ {
		return false
	}
	const word = goarch.PtrSize
	mmask, vmask := getGCMask(m), getGCMask(v)
	var seen compatSeen
	seen.c = true
	for w := uintptr(0); w < v.Size_; w += word {
		if !compatMaskBit(m, mmask, w) && !compatMaskBit(v, vmask, w) {
			continue
		}
		mk, mt, min := compatLeaf(m, w)
		vk, vt, vin := compatLeaf(v, w)
		if compatLeaves(mk, mt, min, vk, vt, vin, true, &seen) != compatOK {
			return false
		}
	}
	return true
}

// cUnionConv returns what the conversion of a to a pointer to elem
// gives, if that is not a itself.
func cUnionConv(a uintptr, elem *_type) unsafe.Pointer {
	if k, _ := cUnionIn(elem, 0); k != cUnionNone {
		// To a type that begins with a union: a's own, if a is one.
		// But a pointer to a union's value goes back to the union.
		// (If the value is not itself something that begins with a
		// union: then a is the address of that.)
		h := cUnionValueOf(a)
		if h != nil {
			if k, _ := cUnionIn(h.typ, 0); k != cUnionNone {
				h = nil
			}
		}
		if h != nil {
			if n, ok := cUnionMirror(elem); ok && *(*unsafe.Pointer)(h.back) == unsafe.Pointer(h) {
				if start := uintptr(h.back) - n; cUnionAt(start) == (*unsafe.Pointer)(h.back) {
					return unsafe.Pointer(start)
				}
			}
		}
		return nil
	}
	if ref := cUnionAt(a); ref != nil {
		return c23UnionBox(ref, elem)
	}
	if h := cUnionValueOf(a); h != nil && !cViewOK(h.typ, elem) && *(*unsafe.Pointer)(h.back) == unsafe.Pointer(h) {
		return c23UnionBox((*unsafe.Pointer)(h.back), elem)
	}
	return nil
}

// cUnionValueOf returns the header of the union value whose value
// begins at a, or nil.
func cUnionValueOf(a uintptr) *cUnionHdr {
	if a < cUnionHeader {
		return nil
	}
	return cUnionValueAt(a - cUnionHeader)
}

// A cUnionGlobal is a variable of the program's data that has a union
// with pointers in it.
type cUnionGlobal struct {
	base uintptr
	typ  *_type
}

// cUnionGlobals is the list of them, in blocks outside the heap: one is
// written and then counted, so a reader needs no lock.
var cUnionGlobals struct {
	lock   mutex
	n      atomic.Uintptr
	blocks [64]*[cUnionGlobalBlock]cUnionGlobal
}

const cUnionGlobalBlock = 256

// c23UnionGlobal tells the runtime of a variable at p, of type typ,
// that has a union with pointers in it: cmd/cc's code calls it for each
// as the program starts.
//
//go:linkname c23UnionGlobal
func c23UnionGlobal(p unsafe.Pointer, typ *_type) {
	g := &cUnionGlobals
	lock(&g.lock)
	n := g.n.Load()
	if n/cUnionGlobalBlock < uintptr(len(g.blocks)) {
		b := g.blocks[n/cUnionGlobalBlock]
		if b == nil {
			b = (*[cUnionGlobalBlock]cUnionGlobal)(persistentalloc(unsafe.Sizeof(*b), 0, &memstats.other_sys))
			g.blocks[n/cUnionGlobalBlock] = b
		}
		b[n%cUnionGlobalBlock] = cUnionGlobal{uintptr(p), typ}
		g.n.Store(n + 1)
	}
	unlock(&g.lock)
}

// cUnionGlobalAt returns the variable that a is in, if the runtime was
// told of it.
func cUnionGlobalAt(a uintptr) (cUnionGlobal, bool) {
	g := &cUnionGlobals
	for i, n := uintptr(0), g.n.Load(); i < n; i++ {
		if v := g.blocks[i/cUnionGlobalBlock][i%cUnionGlobalBlock]; a-v.base < v.typ.Size_ {
			return v, true
		}
	}
	return cUnionGlobal{}, false
}
