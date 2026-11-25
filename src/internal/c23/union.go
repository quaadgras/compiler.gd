// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"internal/abi"
	"unsafe"
)

// A union of C's that has a pointer in a member is to Go its bytes and
// one pointer, a UnionRef (doc/gd/cc.md, "Unions"). The members that
// have no pointer are in the bytes. Whatever of it has a pointer, a
// member that is one or that has one inside it, or a value that C makes
// in its bytes through a cast, is in an object of the heap of its own, a
// union value (runtime/cunion.go), which the UnionRef points to. So a
// word of the union is never an integer to one member and a pointer to
// another; and a pointer that was taken to a member points to the
// object that the member was in then, which stays what it was when the
// union is used for something else and gets another.

// A UnionRef is the pointer of a union to its value: nil, or the
// address of a union value, which begins with a unionHeader.
type UnionRef unsafe.Pointer

// A unionHeader is what a union value begins with: runtime's cUnionHdr.
type unionHeader struct {
	typ  *abi.Type      // the type of the value that follows
	back unsafe.Pointer // the UnionRef of the union that this is the value of
}

// UnionBox returns the value of type M of the union whose UnionRef is
// at slot: what it points to, if that is the union's own value of an M,
// and otherwise a new one that the union gets (runtime.c23UnionBox: a
// copy, if the value came in a copy of the union's bytes; the same
// pointer converted, if M and what the union held are pointers; zero
// otherwise).
//
//go:nocheckptr
func UnionBox[M any](slot *UnionRef) *M {
	t := abi.TypeFor[M]()
	if h := (*unionHeader)(*slot); h != nil && h.typ == t && h.back == unsafe.Pointer(slot) {
		return (*M)(unsafe.Add(unsafe.Pointer(h), unsafe.Sizeof(unionHeader{})))
	}
	return (*M)(unionBox(slot, t))
}

//go:linkname unionBox runtime.c23UnionBox
func unionBox(slot *UnionRef, t *abi.Type) unsafe.Pointer

// UnionClone is for a union that was copied whole, as part of a struct
// that was assigned: the copy, whose UnionRef is at slot, gets a value
// of its own now, as it would when it is next used.
//
//go:nocheckptr
func UnionClone(slot *UnionRef) {
	if h := (*unionHeader)(*slot); h != nil && h.back != unsafe.Pointer(slot) {
		unionClone(slot)
	}
}

//go:linkname unionClone runtime.c23UnionClone
func unionClone(slot *UnionRef)

//go:linkname unionPointer runtime.c23UnionPointer
func unionPointer(x unsafe.Pointer) (unsafe.Pointer, bool)

// UnionValuePtr returns the pointer that a union holds, of which p is
// the copy that was passed by value in variable arguments, if the
// union is one that is laid out as bytes and a UnionRef: the library's
// functions that take such a union (semctl's union semun, which the
// program declares) look there for its pointer members.
//
//go:nocheckptr
func UnionValuePtr(p *Void) (*Void, bool) {
	const word = unsafe.Sizeof(uintptr(0))
	n, ok := usable(unsafe.Pointer(p))
	if !ok || word != 8 || n < 2*word || n%word != 0 {
		return nil, false
	}
	ref := *(*unsafe.Pointer)(unsafe.Add(unsafe.Pointer(p), n-word))
	if ref == nil {
		return nil, true
	}
	q, ok := unionPointer(ref)
	return (*Void)(q), ok
}

// The unions of C's own headers that have a pointer in a member (union
// sigval, epoll_data_t, the handlers of a struct sigaction) are laid
// out as any other union is: the bytes of the members without a
// pointer, and a UnionRef after them. The library is given the address
// of one, and reads and writes it with these, as the code that cmd/cc
// generates does for the members: the integers in the bytes, with
// Load and Store, and the pointer in the union's value.

// UnionRefs says whether a union that has a pointer in a member is its
// bytes and a UnionRef: with 8-byte words (cmd/cc's unionPtr). With 4
// it is its words, and its pointer is where its integers are.
const UnionRefs = WordSize == 8

// loadRef and storeRef load and store the UnionRef at slot, which the
// caller checked to be in an object: the word is the union's, and is
// claimed for nothing else by these.
//
//go:nocheckptr
func loadRef(slot unsafe.Pointer) unsafe.Pointer { return *(*unsafe.Pointer)(slot) }

//go:nocheckptr
func storeRef(slot unsafe.Pointer, v unsafe.Pointer) { *(*unsafe.Pointer)(slot) = v }

// refAt is the UnionRef at slot. (A conversion that is checked would
// be one of C's to a type with a pointer, at the address of a union:
// the union's value, as that type.)
//
//go:nocheckptr
func refAt(slot unsafe.Pointer) *UnionRef { return (*UnionRef)(slot) }

// UnionPtr returns the pointer that the union at u holds, whose bytes
// are the first n of it: what C reads as a member of it that is a
// pointer. It is nil if the union holds no pointer: an integer that
// was stored in the union's bytes is not one.
func UnionPtr(u *Void, n int) *Void {
	if !UnionRefs {
		return LoadPtr(u)
	}
	ref := loadRef(in(Add(u, n), int(WordSize)))
	if ref == nil {
		return nil
	}
	q, _ := unionPointer(ref)
	return (*Void)(q)
}

// SetUnionPtr makes the union at u, whose bytes are the first n of it,
// hold the pointer v, as C does when it stores a member that is a
// pointer; if v is nil it holds none. The union's value points back to
// the union, so what u points into is an object of the heap.
func SetUnionPtr(u *Void, n int, v *Void) {
	if !UnionRefs {
		StorePtr(u, v)
		return
	}
	slot := in(Add(u, n), int(WordSize))
	if v == nil {
		storeRef(slot, nil)
		return
	}
	*UnionBox[*Void](refAt(slot)) = v
}

// A SigVal is a union sigval {int sival_int; void *sival_ptr;}, and an
// epoll_data_t: the integer that is in its bytes, and the pointer that
// it holds. C that stores one member and reads the other reads zero,
// here as in the union.
type SigVal struct {
	Int uint64
	Ptr *Void
}

// SigValSize is sizeof(union sigval).
const SigValSize = (1 + UnionRefs2) * WordSize

// UnionRefs2 is 1 if UnionRefs and 0 otherwise.
const UnionRefs2 = WordSize / 8

// LoadSigVal reads the union at p, whose bytes are a word.
func LoadSigVal(p *Void) SigVal {
	if !UnionRefs {
		return SigVal{Int: uint64(Load32(p)), Ptr: LoadPtr(p)}
	}
	return SigVal{Int: Load64(p), Ptr: UnionPtr(p, int(WordSize))}
}

// StoreSigVal stores v in the union at p, whose bytes are a word.
func StoreSigVal(p *Void, v SigVal) {
	if !UnionRefs {
		if v.Ptr != nil {
			StorePtr(p, v.Ptr)
		} else {
			Store32(p, uint32(v.Int))
		}
		return
	}
	Store64(p, v.Int)
	SetUnionPtr(p, int(WordSize), v.Ptr)
}

// NewSigVal allocates a union sigval that is v: what a function that
// takes one by value is given the pointer to.
func NewSigVal(v SigVal) *Void {
	p := Calloc(1, uint64(SigValSize))
	StoreSigVal(p, v)
	return p
}

//go:linkname setUnionRef runtime.c23SetUnionRef
func setUnionRef(t *abi.Type)

func init() { setUnionRef(abi.TypeFor[UnionRef]()) }

// UnionGlobal tells the runtime of a variable of the program's data, of
// type T, that has a union with pointers in it: a pointer with no type
// that is the address of that union is then known for what it is when C
// converts it to a pointer to a member (runtime.checkptrConvC), as one
// into the heap is from the heap's types. It returns true.
func UnionGlobal[T any](p *T) bool {
	unionGlobal(unsafe.Pointer(p), abi.TypeFor[T]())
	return true
}

//go:linkname unionGlobal runtime.c23UnionGlobal
func unionGlobal(p unsafe.Pointer, t *abi.Type)
