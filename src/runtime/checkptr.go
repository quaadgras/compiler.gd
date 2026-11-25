// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"internal/goarch"
	"internal/runtime/atomic"
	"internal/runtime/gc"
	"unsafe"
)

func checkptrAlignment(p unsafe.Pointer, elem *_type, n uintptr) {
	checkptrAlign(p, elem, n, false, false)
}

// checkptrAlignmentLoad is checkptrAlignment for a conversion whose
// result the compiler knows to be loaded through and nothing else, as
// in *(*uintptr)(p): what is not stored through may have integers where
// the memory has pointers (checkptr_compat.go).
func checkptrAlignmentLoad(p unsafe.Pointer, elem *_type, n uintptr) {
	checkptrAlign(p, elem, n, true, false)
}

// checkptrAlign is checkptrAlignment: elem is compared with what the
// memory is (checkptr_compat.go), and if p is to an object of C's
// (cspan.go), its words are claimed for what elem has there (cclaim.go).
// If c is set the conversion is one of a package of C's.
func checkptrAlign(p unsafe.Pointer, elem *_type, n uintptr, load, c bool) {
	// nil pointer is always suitably aligned (#47430).
	if p == nil {
		return
	}

	// Check that (*[n]elem)(p) is appropriately aligned.
	// Note that we allow unaligned pointers if the types they point to contain
	// no pointers themselves. See issue 37298.
	// TODO(mdempsky): What about fieldAlign?
	// (gd: pointers need no more than their own alignment; a string,
	// aligned to 16 with 8-byte words for a 16-byte access, is only
	// aligned to 8 in a stack frame: doc/gd/sso-string.md.)
	if elem.Pointers() && uintptr(p)&(uintptr(min(elem.Align_, goarch.PtrSize))-1) != 0 {
		if checkptrStop("checkptr: misaligned pointer conversion") {
			return
		}
	}

	// gd: these checks are on in every build (doc/gd/unsafe.md), so the
	// two usual cases are found without looking for the allocation of
	// each end: the goroutine's stack, and one object of the heap.
	size := n * elem.Size_
	if gp := getg(); gp.stack.lo <= uintptr(p) && uintptr(p)+size <= gp.stack.hi && uintptr(p)+size >= uintptr(p) {
		return
	}
	if s := spanOfHeap(uintptr(p)); s != nil && !s.isUserArenaChunk {
		base := s.base() + s.objIndex(uintptr(p))*s.elemsize
		if end := uintptr(p) + size; end > base+s.elemsize || end < uintptr(p) {
			if checkptrStop("checkptr: converted pointer straddles multiple allocations") {
				return
			}
		}
		typ, tbase := checkptrObjectType(s, base)
		if typ == elem && size != 0 && uintptr(p) >= tbase && (uintptr(p)-tbase)%elem.Size_ == 0 {
			// A pointer to what the object was allocated as.
			return
		}
		if s.spanclass.c() {
			// An object of C's has no type: its words are what
			// they were claimed for (cclaim.go).
			cClaim(s, s.objIndex(uintptr(p)), uintptr(p), elem, n, load, c)
			return
		}
		if typ != nil {
			checkptrWhole(typ, tbase, base+s.elemsize, uintptr(p), elem, size)
			checkptrCompat(typ, tbase, base+s.elemsize, uintptr(p), elem, size, load, c)
			return
		}
		if wholeBitsEnabled {
			checkptrWholeSmall(s, base, uintptr(p), elem, size)
		}
		if s.spanclass.noscan() || heapBitsInSpan(s.elemsize) {
			checkptrPointers(s, uintptr(p), elem, n, load)
		}
		return
	}

	// Not in the heap, and neither is its end: a global, or memory that
	// is not Go's, which have no bounds here.
	if size > 0 && uintptr(p)+size-1 >= uintptr(p) && spanOfHeap(uintptr(p)+size-1) == nil {
		return
	}

	// Check that (*[n]elem)(p) doesn't straddle multiple heap objects.
	// TODO(mdempsky): Fix #46938 so we don't need to worry about overflow here.
	if checkptrStraddles(p, size) {
		throw("checkptr: converted pointer straddles multiple allocations")
	}
}

// checkptrStop stops the program for a conversion that msg says what is
// wrong with, unless the conversion is only asked about (cConvProbe):
// then it reports true, and the conversion is to do nothing more.
func checkptrStop(msg string) bool {
	if mp := getg().m; mp.cprobe {
		mp.cprobeFail = true
		return true
	}
	throw(msg)
	return true
}

// What an address is in, for the checks of a package of C's.
const (
	cObjNone  = iota // nothing that the runtime knows the bounds of
	cObjC            // an object of C's: what malloc gave (cspan.go)
	cObjGo           // an object of Go's heap
	cObjStack        // the goroutine's stack
	cObjData         // the program's data
)

// cObjectOf returns what the address a is in, for the checks of a
// package of C's: where it begins and its size, which for an object of
// C's is what was asked for.
func cObjectOf(a uintptr) (base, size uintptr, kind int) {
	if gp := getg(); gp.stack.lo <= a && a < gp.stack.hi {
		return gp.stack.lo, gp.stack.hi - gp.stack.lo, cObjStack
	}
	if s := spanOfHeap(a); s != nil && !s.isUserArenaChunk {
		i := s.objIndex(a)
		base = s.base() + i*s.elemsize
		if s.spanclass.c() {
			return base, s.cSizeOf(i), cObjC
		}
		return base, s.elemsize, cObjGo
	}
	for _, datap := range activeModules() {
		for _, r := range [...][2]uintptr{
			{datap.data, datap.edata}, {datap.bss, datap.ebss},
			{datap.noptrdata, datap.enoptrdata}, {datap.noptrbss, datap.enoptrbss},
			{datap.rodata, datap.etypes},
		} {
			if r[0] <= a && a < r[1] {
				return r[0], r[1] - r[0], cObjData
			}
		}
	}
	return 0, 0, cObjNone
}

// checkptrArithmeticC is checkptrArithmetic for a package that is C
// translated by cmd/cc, and returns the pointer that the arithmetic
// gives: p, which the compiled code made of the originals with cEnd
// taken off, or p with cEnd if it is to the end of the object.
//
// C's rule is the one that is checked: the result is in the object that
// an original is in, or one past its last byte. A pointer to the end is
// one that C makes all the time, the end of an array that a loop runs
// to; it has cEnd, so that a load or a store through it faults, and so
// has every pointer to that address, which is what lets one be compared
// with another. An object of C's on the heap has a byte after it
// (cPad), so that pointer is into the object's own memory to the
// collector; the end of anything else is where the next thing begins
// (cEndAddr).
//
// What is in nothing that the runtime knows the bounds of, memory that
// the program mapped, is let be for now.
func checkptrArithmeticC(p unsafe.Pointer, originals []unsafe.Pointer) unsafe.Pointer {
	r := uintptr(p)
	if r&cPoison != 0 {
		// Made of what was no pointer: it stays that.
		return p
	}
	a := r &^ cEnd
	if a < minLegalPointer || a >= ^uintptr(0)-minLegalPointer {
		// An integer that C keeps in a pointer, or one added to
		// a null pointer: the address of nothing.
		return unsafe.Pointer(a)
	}
	known := false
	for _, original := range originals {
		o := uintptr(original)
		if o&cPoison != 0 {
			return unsafe.Pointer(a | cPoison)
		}
		base, size, kind := cObjectOf(cEndAddr(o))
		if kind == cObjNone {
			continue
		}
		known = true
		switch {
		case a-base < size:
			return unsafe.Pointer(a)
		case a-base == size:
			return unsafe.Pointer(a | cEnd)
		}
	}
	if !known {
		if _, _, kind := cObjectOf(a); kind == cObjNone {
			for _, original := range originals {
				if uintptr(original) >= minLegalPointer {
					// Memory that is not Go's.
					return unsafe.Pointer(a)
				}
			}
		}
		// What was added to an integer, and is an address now.
		cIntPtrs.Add(1)
		return unsafe.Pointer(a | cPoison)
	}
	throw("checkptr: pointer arithmetic result is out of its object")
	return nil
}

// checkptrAlignmentC is checkptrAlignment for unsafe.Slice and the like
// in a package that is C translated by cmd/cc: the n values of elem at p
// are in one object, each of them whole, and an object of C's is claimed
// for them (cclaim.go).
func checkptrAlignmentC(p unsafe.Pointer, elem *_type, n uintptr) {
	if p == nil || uintptr(p) < minLegalPointer {
		return
	}
	a := uintptr(p)
	if a&(cPoison|cEnd) != 0 {
		if n == 0 || elem.Size_ == 0 {
			return
		}
		throw("checkptr: converted pointer is to the end of its object")
	}
	if s := spanOfHeap(a); s != nil && !s.isUserArenaChunk && s.spanclass.c() {
		i := s.objIndex(a)
		limit := s.base() + i*s.elemsize + s.cSizeOf(i)
		if size := n * elem.Size_; a > limit || size > limit-a || elem.Size_ != 0 && size/elem.Size_ != n {
			throw("checkptr: converted pointer type does not fit in its object")
		}
		if elem.Pointers() && a&(uintptr(min(elem.Align_, goarch.PtrSize))-1) != 0 {
			throw("checkptr: misaligned pointer conversion")
		}
		cClaim(s, i, a, elem, n, false, true)
		return
	}
	checkptrAlign(p, elem, n, false, true)
}

// checkptrConvC is the check of the conversion (*elem)(p) in a package
// that is C translated by cmd/cc, and returns the pointer that the
// conversion gives. That is p, but:
//
//   - a pointer that a value of elem does not fit at, in what it points
//     into, has cEnd: the pointer to the end, as arithmetic gives it,
//     and one to a type that is larger than what is left of the object.
//     C makes both and keeps them; a load or a store through one
//     faults;
//   - a pointer to a union with pointers, or to the value of one, is
//     converted to where what is asked for is (cUnionConv).
//
// Anywhere else a value of elem is whole in the object that p is in: the
// code that uses the pointer is checked no further. In an object of C's the words that the value is over are
// claimed for what elem has there, or were claimed for what is
// compatible with it (cclaim.go); in an object of Go's the object's type
// is (checkptr_compat.go).
func checkptrConvC(p unsafe.Pointer, elem *_type, n uintptr) unsafe.Pointer {
	return checkptrConvC1(p, elem, false)
}

// checkptrConvCLoad is checkptrConvC for a conversion whose result the
// compiler knows to be loaded through and nothing else: what is not
// stored through may have integers where the memory has pointers.
func checkptrConvCLoad(p unsafe.Pointer, elem *_type, n uintptr) unsafe.Pointer {
	return checkptrConvC1(p, elem, true)
}

func checkptrConvC1(p unsafe.Pointer, elem *_type, load bool) unsafe.Pointer {
	a := uintptr(p)
	if a < minLegalPointer || a&(cPoison|cEnd) != 0 {
		// Null, an integer that C keeps in a pointer, or a pointer
		// that nothing is loaded through.
		return p
	}
	a &^= cPtrBits
	p = unsafe.Pointer(a)
	if cUnionRefType != nil && elem.Pointers() && !getg().m.cprobe {
		if q := cUnionConv(a, elem); q != nil {
			return q
		}
	}
	if s := spanOfHeap(a); s != nil && !s.isUserArenaChunk && s.spanclass.c() {
		i := s.objIndex(a)
		limit := s.base() + i*s.elemsize + s.cSizeOf(i)
		if a > limit {
			checkptrStop("checkptr: converted pointer is past the end of its object")
			return p
		}
		if elem.Size_ > limit-a {
			return unsafe.Pointer(a | cEnd)
		}
		if elem.Pointers() && a&(uintptr(min(elem.Align_, goarch.PtrSize))-1) != 0 {
			checkptrStop("checkptr: misaligned pointer conversion")
			return p
		}
		cClaim(s, i, a, elem, 1, load, true)
		return p
	}
	if base, size, kind := cObjectOf(a); kind != cObjNone && elem.Size_ > size-(a-base) {
		return unsafe.Pointer(a | cEnd)
	}
	if !elem.Pointers() && elem.Align_ == 1 {
		// Bytes, which C looks at everything as.
		checkptrView(p, elem, 1)
		return p
	}
	checkptrAlign(p, elem, 1, load, true)
	return p
}

// checkptrObjectType returns the type that the object at base of the span
// s was allocated for, and where the values of that type begin, or nil if
// it has only a bitmap (a small object of a type that the program made as
// it ran), or no pointers.
func checkptrObjectType(s *mspan, base uintptr) (*_type, uintptr) {
	if s.spanclass.noscan() {
		return nil, 0
	}
	i := s.objIndex(base)
	if s.isFree(i) {
		// A pointer kept from before: its first word is not a type.
		return nil, 0
	}
	if heapBitsInSpan(s.elemsize) {
		// No header: the span has what it was allocated for
		// (mobjtypes.go), unless that is a type without a number.
		return s.objType(i), base
	}
	if s.spanclass.sizeclass() != 0 {
		// The object begins with its type.
		return *(**_type)(unsafe.Pointer(base)), base + gc.MallocHeaderSize
	}
	return (*_type)(atomic.Loadp(unsafe.Pointer(&s.largeType))), base
}

// checkptrView is the check of (*[n]elem)(p) for a type that needs no
// other: its alignment is 1, and p is not known to the compiler to point
// to anything in particular.
func checkptrView(p unsafe.Pointer, elem *_type, n uintptr) {
	if p == nil {
		return
	}
	s := spanOfHeap(uintptr(p))
	if s == nil || s.isUserArenaChunk {
		return
	}
	base := s.base() + s.objIndex(uintptr(p))*s.elemsize
	if typ, tbase := checkptrObjectType(s, base); typ != nil {
		checkptrWhole(typ, tbase, base+s.elemsize, uintptr(p), elem, n*elem.Size_)
	} else if wholeBitsEnabled {
		checkptrWholeSmall(s, base, uintptr(p), elem, n*elem.Size_)
	}
}

// checkptrViewKept is checkptrView for a conversion of a package of Go's
// whose result is not only loaded through, and for the elements of
// unsafe.Slice(p, n): bytes that are kept may be stored through, so
// they are over no pointer, as other integers are (checkptr_compat.go).
// The view is compared as far as the object goes: (*[1<<30]byte)(p) is
// a type that is longer than what it is over.
func checkptrViewKept(p unsafe.Pointer, elem *_type, n uintptr) {
	if p == nil {
		return
	}
	s := spanOfHeap(uintptr(p))
	if s == nil || s.isUserArenaChunk || s.spanclass.c() {
		// (The words of an object of C's are what they are
		// claimed for, and bytes claim nothing: cclaim.go.)
		return
	}
	if s.spanclass.noscan() && !elem.Pointers() {
		return
	}
	base := s.base() + s.objIndex(uintptr(p))*s.elemsize
	size := n * elem.Size_
	typ, tbase := checkptrObjectType(s, base)
	if typ != nil {
		if typ == elem && uintptr(p) >= tbase && (uintptr(p)-tbase)%elem.Size_ == 0 {
			// What the object was allocated as.
			return
		}
		checkptrWhole(typ, tbase, base+s.elemsize, uintptr(p), elem, size)
		checkptrCompat(typ, tbase, base+s.elemsize, uintptr(p), elem, size, false, false)
		return
	}
	if wholeBitsEnabled {
		checkptrWholeSmall(s, base, uintptr(p), elem, size)
	}
	if s.spanclass.noscan() || heapBitsInSpan(s.elemsize) {
		checkptrPointers(s, uintptr(p), elem, n, false)
	}
}

// checkptrPartial is called where the compiler knows that a conversion
// makes a view of part of a string, a slice or an interface, if p is not
// nil: a nil pointer views nothing. It cannot move the stack, as exposeptr
// cannot, since it runs between the conversions of a call's arguments.
//
//go:nosplit
func checkptrPartial(p unsafe.Pointer) {
	if p != nil {
		throw(checkptrPartialMsg)
	}
}

const checkptrPartialMsg = "checkptr: converted pointer is a partial view of a string, a slice or an interface"

// The state of a word of memory, for the check that a view of it does not
// take part of a string, a slice or an interface. A view is whole if each
// of its words has the state the memory has where the memory holds one.
const (
	wordPlain      = 0b00 // not part of a whole value
	wordSliceFirst = 0b01 // the first word of a slice
	wordCont       = 0b10 // a later word of a whole value
	wordFirst      = 0b11 // the first word of a string, an interface or a whole struct
)

// wordState returns the state of the word at offset off of a value of
// type t.
func wordState(t *_type, off uintptr) uint8 {
	for {
		if !t.Pointers() {
			return wordPlain
		}
		switch t.Kind() {
		case abi.String, abi.Interface:
			if off == 0 {
				return wordFirst
			}
			return wordCont
		case abi.Slice:
			if off == 0 {
				return wordSliceFirst
			}
			return wordCont
		case abi.Array:
			a := (*arraytype)(unsafe.Pointer(t))
			if a.Len == 0 || a.Elem.Size_ == 0 {
				return wordPlain
			}
			off %= a.Elem.Size_
			t = a.Elem
		case abi.Struct:
			if isWholeStruct(t) || isABIInterface(t) {
				if off == 0 {
					return wordFirst
				}
				return wordCont
			}
			st := (*structtype)(unsafe.Pointer(t))
			var in *abi.StructField
			for i := range st.Fields {
				f := &st.Fields[i]
				if f.Typ.Size_ != 0 && f.Offset <= off && off < f.Offset+f.Typ.Size_ {
					in = f
					break
				}
			}
			if in == nil {
				return wordPlain
			}
			off -= in.Offset
			t = in.Typ
		default:
			return wordPlain
		}
	}
}

// isABIInterface reports whether the struct type t is internal/abi's
// description of an interface, which is the same words, or the shape of
// one, which is what generic code has for it: it is known by its fields.
func isABIInterface(t *_type) bool {
	st := (*structtype)(unsafe.Pointer(t))
	if len(st.Fields) != 3 {
		return false
	}
	f := st.Fields
	if f[1].Typ.Kind() != abi.UnsafePointer || f[2].Typ.Kind() != abi.Complex128 ||
		f[1].Name.Name() != "Data" || f[2].Name.Name() != "Inline" {
		return false
	}
	switch f[0].Typ {
	case abi.TypeOf((*abi.Type)(nil)), abi.TypeOf((*abi.ITab)(nil)):
		return true
	}
	return f[0].Name.Name() == "_" && f[0].Typ.Kind() == abi.UnsafePointer && st.PkgPath.Name() == "internal/abi"
}

// wholeStringAt reports whether a string begins at offset off of a value
// of type t.
func wholeStringAt(t *_type, off uintptr) bool {
	for {
		switch t.Kind() {
		case abi.String:
			return off == 0
		case abi.Array:
			a := (*arraytype)(unsafe.Pointer(t))
			if a.Len == 0 || a.Elem.Size_ == 0 {
				return false
			}
			off %= a.Elem.Size_
			t = a.Elem
		case abi.Struct:
			st := (*structtype)(unsafe.Pointer(t))
			var in *abi.StructField
			for i := range st.Fields {
				f := &st.Fields[i]
				if f.Typ.Size_ != 0 && f.Offset <= off && off < f.Offset+f.Typ.Size_ {
					in = f
					break
				}
			}
			if in == nil {
				return false
			}
			off -= in.Offset
			t = in.Typ
		default:
			return false
		}
	}
}

// checkptrWhole checks that the view of size bytes at p, of values of type
// elem, is not of part of a string, a slice or an interface of the object
// that ends at limit, which holds values of type typ from tbase on.
func checkptrWhole(typ *_type, tbase, limit, p uintptr, elem *_type, size uintptr) {
	if !typ.HoldsWhole() {
		return
	}
	obj := wholeObject{typ: typ, tbase: tbase}
	checkptrWholeIn(&obj, limit, p, elem, size)
}

// A wholeObject gives the states of the words of an object: from the type
// it records, or from its span's bitmap (mwholebits.go).
type wholeObject struct {
	typ   *_type
	bits  *wholeBits
	tbase uintptr // where the object's values begin
	sbase uintptr // the base of its span, with bits
}

// state returns the state of the word at offset off from tbase.
func (o *wholeObject) state(off uintptr) uint8 {
	if o.typ != nil {
		return wordState(o.typ, off%o.typ.Size_)
	}
	return o.bits.state((o.tbase + off - o.sbase) / goarch.PtrSize)
}

// checkptrWholeIn is the comparison of the view with the object that ends
// at limit. It goes by words: where the object has a whole value, the view
// must have the same state, and the word after the view must not be the
// rest of a value that the view began. A view of plain memory may have any
// type.
func checkptrWholeIn(o *wholeObject, limit, p uintptr, elem *_type, size uintptr) {
	if size == 0 || p < o.tbase || p >= limit {
		return
	}
	const word = goarch.PtrSize
	lo := p - o.tbase
	if size > limit-p {
		// A type longer than the object, as (*[1<<30]byte)(p) has.
		// The object may have room past its last value, which
		// holds nothing: go no further than the value p is in.
		if o.typ != nil {
			limit = min(limit, o.tbase+(lo/o.typ.Size_+1)*o.typ.Size_)
		}
		size = limit - p
	}
	aligned := lo&(word-1) == 0
	end := alignUp(lo+size, word)
	excused := ^uintptr(0) // the third word of a slice viewed as a string
	beyond := ^uintptr(0)  // the third word of a string viewed as a slice
	wasPlain := false
	for w := lo &^ (word - 1); w < end; w += word {
		have := o.state(w)
		var want uint8
		if aligned {
			want = wordState(elem, (w-lo)%elem.Size_)
		}
		// A value of the view is over one whole value or over plain
		// memory, not partly each, as an interface over a string and
		// the two words after it would be.
		if want == wordCont && (have == wordPlain) != wasPlain && w != beyond {
			throw(checkptrPartialMsg)
		}
		wasPlain = have == wordPlain
		if have == want || have == wordPlain || w == excused && want == wordPlain {
			continue
		}
		// Where a slice is three words, a string is its first two,
		// and the one may be viewed as the other.
		if !abi.SlicePacked && aligned {
			if have == wordSliceFirst && want == wordFirst && wholeStringAt(elem, (w-lo)%elem.Size_) {
				excused = w + 2*word
				continue
			}
			// (A string is two words: what is first and has no
			// third word is one.)
			if have == wordFirst && want == wordSliceFirst && (o.tbase+w+2*word >= limit || o.state(w+2*word) != wordCont) {
				beyond = w + 2*word
				continue
			}
		}
		throw(checkptrPartialMsg)
	}
	if end == excused {
		end += word
	}
	if o.tbase+end < limit && o.state(end) == wordCont {
		throw(checkptrPartialMsg)
	}
}

// checkptrStraddles reports whether the first size-bytes of memory
// addressed by ptr is known to straddle more than one Go allocation.
func checkptrStraddles(ptr unsafe.Pointer, size uintptr) bool {
	if size <= 1 {
		return false
	}

	// Check that add(ptr, size-1) won't overflow. This avoids the risk
	// of producing an illegal pointer value (assuming ptr is legal).
	if uintptr(ptr) >= -(size - 1) {
		return true
	}
	end := add(ptr, size-1)

	// TODO(mdempsky): Detect when [ptr, end] contains Go allocations,
	// but neither ptr nor end point into one themselves.

	return checkptrBase(ptr) != checkptrBase(end)
}

func checkptrArithmetic(p unsafe.Pointer, originals []unsafe.Pointer) {
	// (A program with C in it keeps small integers in pointers, and
	// adds to a null pointer: c23AllowIntegers.)
	if 0 < uintptr(p) && uintptr(p) < minLegalPointer && debug.invalidptr != 0 {
		throw("checkptr: pointer arithmetic computed bad pointer value")
	}

	// Check that if the computed pointer p points into a heap
	// object, then one of the original pointers must have pointed
	// into the same object.
	//
	// gd: the usual case first, with the span looked for once: p is
	// in an object of the heap, and so is one of the originals.
	if gp := getg(); uintptr(p) < gp.stack.lo || uintptr(p) >= gp.stack.hi {
		if s := spanOfHeap(uintptr(p)); s != nil {
			i := s.objIndex(uintptr(p))
			base := s.base() + i*s.elemsize
			// What malloc gave C (cspan.go) ends where C asked
			// it to, and a pointer of C's is to its object or
			// to the end of it.
			if s.spanclass.c() && uintptr(p)-base > s.cSizeOf(i) {
				throw("checkptr: pointer arithmetic result is past the end of its object")
			}
			for _, original := range originals {
				if uintptr(original)-base < s.elemsize {
					return
				}
			}
		}
	}
	base := checkptrBase(p)
	if base == 0 {
		return
	}

	for _, original := range originals {
		if base == checkptrBase(original) {
			return
		}
	}

	// gd: an integer that was kept is a pointer again if it is in an
	// object of the heap whose address the program took as an integer:
	// see exposeptr.
	if checkptrExposed(uintptr(p)) {
		return
	}

	throw("checkptr: pointer arithmetic result points to invalid allocation")
}

// Exposed objects.
//
// Go's rules for unsafe.Pointer let an integer become a pointer only in
// the expression that made it of one. Code written as C is keeps
// addresses in integers: a handle in a uintptr field, an argument of a
// callback. So a pointer may also be made of an integer that is the
// address of something in an object of the heap that is exposed: one whose
// address the program made an integer of, and kept. Compiled code calls
// exposeptr where it converts a pointer to a uintptr that outlives the
// expression. The object is no more alive for it: when it is freed the
// integer is the address of nothing, and of nothing exposed if the memory
// is used again.
//
// This is what C calls an exposed address (PNVI-ae): an integer gives no
// access to an object that never gave its address away.

// exposeptr marks the object of the heap that p points into as exposed.
//
// It cannot move the stack: the compiler calls it between the conversions
// of the arguments of a call, and an address of the stack that one of them
// made an integer of, for a system call to write to, must stay the
// address of the variable until the call (syscall.SyscallN and the like
// are nosplit for the same reason). So it is nosplit, and marks on the
// system stack.
//
//go:nosplit
func exposeptr(p unsafe.Pointer) {
	if p == nil {
		return
	}
	s := spanOfHeap(uintptr(p))
	if s == nil || s.isUserArenaChunk {
		return
	}
	i := s.objIndex(uintptr(p))
	if bits := (*gcBits)(atomic.Loadp(unsafe.Pointer(&s.exposedBits))); bits != nil && atomic.Load8(bits.bytep(i/8))&(uint8(1)<<(i%8)) != 0 {
		return
	}
	systemstack(func() {
		exposeobj(s, i)
	})
	KeepAlive(p)
}

// exposeobj marks object i of s as exposed.
//
//go:systemstack
func exposeobj(s *mspan, i uintptr) {
	mask := uint8(1) << (i % 8)
	// The sweeper replaces the bitmap, so the span must be swept and
	// stay so: as for the bits of shared.go.
	mp := acquirem()
	s.ensureSwept()
	lock(&s.speciallock)
	bits := (*gcBits)(atomic.Loadp(unsafe.Pointer(&s.exposedBits)))
	if bits == nil {
		bits = newMarkBits(uintptr(s.nelems))
		atomic.StorepNoWB(unsafe.Pointer(&s.exposedBits), unsafe.Pointer(bits))
	}
	if !s.isFree(i) {
		atomic.Or8(bits.bytep(i/8), mask)
	}
	unlock(&s.speciallock)
	releasem(mp)
}

// checkptrExposed reports whether p is in an exposed object of the heap.
func checkptrExposed(p uintptr) bool {
	s := spanOfHeap(p)
	if s == nil || s.isUserArenaChunk {
		return false
	}
	bits := (*gcBits)(atomic.Loadp(unsafe.Pointer(&s.exposedBits)))
	if bits == nil {
		return false
	}
	i := s.objIndex(p)
	return !s.isFree(i) && atomic.Load8(bits.bytep(i/8))&(1<<(i%8)) != 0
}

// checkptrBase returns the base address for the allocation containing
// the address p.
//
// Importantly, if p1 and p2 point into the same variable, then
// checkptrBase(p1) == checkptrBase(p2). However, the converse/inverse
// is not necessarily true as allocations can have trailing padding,
// and multiple variables may be packed into a single allocation.
//
// checkptrBase should be an internal detail,
// but widely used packages access it using linkname.
// Notable members of the hall of shame include:
//   - github.com/bytedance/sonic
//
// Do not remove or change the type signature.
// See go.dev/issue/67401.
//
//go:linkname checkptrBase
func checkptrBase(p unsafe.Pointer) uintptr {
	// stack
	if gp := getg(); gp.stack.lo <= uintptr(p) && uintptr(p) < gp.stack.hi {
		// TODO(mdempsky): Walk the stack to identify the
		// specific stack frame or even stack object that p
		// points into.
		//
		// In the mean time, use "1" as a pseudo-address to
		// represent the stack. This is an invalid address on
		// all platforms, so it's guaranteed to be distinct
		// from any of the addresses we might return below.
		return 1
	}

	// heap (must check after stack because of #35068)
	// (Not findObject: it throws for a pointer into a span that was
	// freed, which an integer kept from before may well be; that is
	// for this check to say, or not, as for any other address.)
	if s := spanOfHeap(uintptr(p)); s != nil {
		return s.base() + s.objIndex(uintptr(p))*s.elemsize
	}

	// data or bss
	for _, datap := range activeModules() {
		if datap.data <= uintptr(p) && uintptr(p) < datap.edata {
			return datap.data
		}
		if datap.bss <= uintptr(p) && uintptr(p) < datap.ebss {
			return datap.bss
		}
	}

	return 0
}

// reflect_checkptr is the check of the conversion (*t)(p) for
// reflect.NewAt.
//
//go:linkname reflect_checkptr reflect.checkptr
func reflect_checkptr(p unsafe.Pointer, t *_type) {
	if GOARCH != "wasm" { // see cmd/compile/internal/base/flag.go
		checkptrAlignment(p, t, 1)
	}
}
