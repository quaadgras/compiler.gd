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
		throw("checkptr: misaligned pointer conversion")
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
			throw("checkptr: converted pointer straddles multiple allocations")
		}
		typ, tbase := checkptrObjectType(s, base)
		if typ == elem && size != 0 && uintptr(p) >= tbase && (uintptr(p)-tbase)%elem.Size_ == 0 {
			// A pointer to what the object was allocated as.
			return
		}
		if elem.Pointers() {
			checkptrPointers(s, uintptr(p), elem, n)
		}
		if typ != nil {
			checkptrWhole(typ, tbase, base+s.elemsize, uintptr(p), elem, size)
		} else if wholeBitsEnabled {
			checkptrWholeSmall(s, base, uintptr(p), elem, size)
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

// checkptrObjectType returns the type that the object at base of the span
// s records, and where the values of that type begin, or nil if it has only
// a bitmap, or no pointers.
func checkptrObjectType(s *mspan, base uintptr) (*_type, uintptr) {
	if s.spanclass.noscan() || heapBitsInSpan(s.elemsize) {
		return nil, 0
	}
	if s.isFree(s.objIndex(base)) {
		// A pointer kept from before: its first word is not a type.
		return nil, 0
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

// checkptrSameObject reports whether a, in an object of the span s, and v
// are addresses in the same object.
func checkptrSameObject(s *mspan, a, v uintptr) bool {
	return s.base() <= v && v < s.limit && s.objIndex(a) == s.objIndex(v)
}

// checkptrPointers checks that (*[n]elem)(p), which is in one object of
// the span s, has pointers only where the object has, or where the object
// has a word that does not hold the address of anything in the heap now:
// a pointer read from a word that holds an integer is whatever the
// integer is.
//
// What it cannot see: a pointer stored later through the converted
// pointer in a word the collector does not look at, and an integer stored
// where the object has a pointer. A conversion cannot tell what will be
// stored.
func checkptrPointers(s *mspan, p uintptr, elem *_type, n uintptr) {
	limit := p + n*elem.Size_
	var tp typePointers
	var slot uintptr // the next word of the object that holds a pointer
	if !s.spanclass.noscan() {
		tp = s.typePointersOf(p, limit-p)
		tp, slot = tp.next(limit)
	}
	mask := getGCMask(elem)
	words := elem.PtrBytes / goarch.PtrSize
	for i := uintptr(0); i < n; i++ {
		for w := uintptr(0); w < words; w++ {
			if *addb(mask, w/8)>>(w%8)&1 == 0 {
				continue
			}
			a := p + i*elem.Size_ + w*goarch.PtrSize
			for slot != 0 && slot < a {
				tp, slot = tp.next(limit)
			}
			if slot != a {
				// The first word of an interface is one that the
				// collector does not look at, and is a pointer all
				// the same, to a type or a method table, which are
				// not in the heap: package reflect and others read
				// it as one. So is a pointer to a type that is
				// never in the heap. So a word that holds no
				// pointer may be seen as a pointer while what it
				// holds is not one into the heap.
				//
				// Nor is it a pointer that the collector has to see
				// if it points into the object that it is in, which
				// lives while the word is read: a buffer of bytes
				// that the system fills with a list whose links are
				// addresses in the buffer (windows).
				if v := *(*uintptr)(unsafe.Pointer(a)); v != 0 && spanOfHeap(v) != nil && !checkptrSameObject(s, a, v) {
					throw("checkptr: converted pointer type has a pointer where the memory has an integer")
				}
			}
		}
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
	if 0 < uintptr(p) && uintptr(p) < minLegalPointer {
		throw("checkptr: pointer arithmetic computed bad pointer value")
	}

	// Check that if the computed pointer p points into a heap
	// object, then one of the original pointers must have pointed
	// into the same object.
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
