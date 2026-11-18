// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package c23 is the run-time support of C compiled by cmd/cc: pointers
// with typed capabilities (doc/gd/cc.md).
//
// In a register (a Go variable) a C pointer is a Ptr: a capability (the
// base, size and type of the memory it may reach) and an offset, which may
// be anything. In memory a C pointer is one word, where the memory's type
// has a pointer:
//
//   - if the address holds a whole value of the type the word points to,
//     the word is the address: a Go pointer, which Go code may use as it is;
//   - otherwise the word is the address with bit 55 set (tagged): not
//     canonical, so it faults where Go dereferences it, and the collector
//     does not take it for a pointer. In C's objects its capability is
//     kept in the object's side table, so C can bring it back into bounds.
//
// Capabilities come from types, never from the heap's guesses:
//
//   - C's allocations (New, NewArray) have a header with their type and
//     exact size; a word that points into one gets that capability back;
//   - a Go pointer gets the capability of its static type: the value it
//     points to (FromGo), or the array of a slice (FromSlice), as cgo's
//     rules say what C may reach;
//   - a word loaded from memory that is not in C's allocation gets the
//     capability of the type the memory says it points to.
//
// Every access through a Ptr is checked against its capability and its
// type: a scalar store may not write a pointer word or a word of a Go value
// with invariants, a pointer store may only write a pointer word. Checks
// read the capability's type, not the heap, so C's objects and Go's may be
// on the stack: escape analysis keeps there what C does not store.
package c23

import (
	"fmt"
	"internal/abi"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"
)

// object returns the base of the heap object that p points into, as a
// pointer derived from p (with GOWASM=gc,split, that keeps p's array of
// pointers), and its size; nil, 0 if p does not point into the heap. p
// does not escape: the base is only ever that of a heap object.
//
//go:noescape
//go:linkname object runtime.c23Object
func object(p unsafe.Pointer) (base unsafe.Pointer, size uintptr)

// Ptr is a C pointer in a register.
type Ptr struct {
	obj  unsafe.Pointer // base of the capability; nil: none
	size uintptr        // bytes the capability covers
	off  int            // address - base; with no capability, the address
	ti   uintptr        // the memory is an array of this *tinfo's type; bit 0: C's object; bit 1: scalars only
}

// A Ptr holds its *tinfo as an index into tinfoTab, with bits saying
// what checks read without the table (refC, refStoresFree, refSplit):
// escape analysis, which sees a Ptr as one value,
// would otherwise take every use of the type as a use of the memory and
// move what the memory points to off the stack; and an index, unlike a
// pointer made of an integer, is the same on every target.
func tref(ti *tinfo) uintptr { return ti.ref }

// The bits of a Ptr's ti (and a header's ref) below the tinfo's index.
const (
	refC          = 1 << 0 // the memory is C's object, with a header
	refStoresFree = 1 << 1 // no word a scalar store may not write
	refSplit      = 1 << 2 // every word is split (malloc's memory; unions): a pointer's capability is its slot's
	refShift      = 3
)

func (p Ptr) info() *tinfo {
	if i := p.ti >> refShift; i < uintptr(len(tinfoFast)) {
		return tinfoFast[i].Load()
	}
	return (*tinfoTab.Load())[p.ti>>refShift]
}

// cref is tref for a capability of C's object, which has a header.
func cref(ti *tinfo) uintptr { return tref(ti) | refC }

func (p Ptr) header() *header {
	if p.ti&refC == 0 {
		return nil
	}
	return (*header)(abi.NoEscape(unsafe.Add(p.obj, -int(hdrSize))))
}

// A Fault is what a C access that its capability does not allow panics with.
type Fault struct {
	Msg  string
	Addr uintptr
}

func (f *Fault) Error() string { return fmt.Sprintf("c23: %s (address %#x)", f.Msg, f.Addr) }

// A boundsFault is an access check's Fault, its message made when it is
// read, so that check stays small enough to inline.
//
// It holds no pointer, so that the Ptr it describes does not escape.
type boundsFault struct {
	obj, size, ti, n uintptr
	off              int
}

func (b boundsFault) Error() string { return b.fault().Error() }

func (b boundsFault) fault() *Fault {
	addr := b.obj + uintptr(b.off)
	if b.obj == 0 {
		return &Fault{"access through a pointer with no capability", addr}
	}
	ti := (*tinfoTab.Load())[b.ti>>refShift]
	return &Fault{fmt.Sprintf("access of %d bytes out of bounds of %s", b.n, capString(true, ti, b.size, b.off)), addr}
}

func fault(msg string, addr uintptr) { panic(&Fault{msg, addr}) }

// Addr returns the address p holds.
func Addr(p Ptr) uintptr { return uintptr(p.obj) + uintptr(p.off) }

// FromInt returns a pointer made of an integer: it has no capability.
func FromInt(u uintptr) Ptr { return Ptr{off: int(u)} }

// Add returns p moved by n bytes. Arithmetic is never checked.
func Add(p Ptr, n int) Ptr { p.off += n; return p }

// Sub returns the difference of the addresses of p and q.
func Sub(p, q Ptr) int { return int(Addr(p) - Addr(q)) }

// Cap describes p's capability, for tests and messages.
func Cap(p Ptr) string { return capString(p.obj != nil, p.info(), p.size, p.off) }

// capString takes the parts of a capability that do not point into its
// memory, so that formatting a fault does not make what the memory points
// to escape.
//
//go:noinline
func capString(ok bool, ti *tinfo, size uintptr, off int) string {
	if !ok {
		return fmt.Sprintf("{addr %#x, no capability}", off)
	}
	return fmt.Sprintf("{%s, size %d, off %d}", typeName(ti), size, off)
}

// The header of C's allocations. Its first word is its Ptrs' ti, odd
// (refC), where a capRec's is even: a slot, which points to either, says
// which, and gives the capability's type, with one load. cobject trusts
// a header in memory only if ti is a registered *tinfo, which safe Go code
// cannot make, and ref is its.
type header struct {
	ref  uintptr // ti.ref | refC
	ti   *tinfo
	size uintptr
	aux  unsafe.Pointer // the slots of its pointer words: [size/wordSize]unsafe.Pointer, made when first needed
}

const hdrSize = unsafe.Sizeof(header{})

// Obj is a C object: a header, then the value. C's locals are Go
// variables of this type, which escape analysis keeps on the stack unless
// C stores their address.
type Obj[T any] struct {
	h header
	V T // the value: what Go that cmd/cc generates reaches directly, when it knows the object
}

// Local returns the C pointer for a local C object.
func Local[T any](o *Obj[T]) Ptr { return LocalT(o, tinfoOf[T]()) }

// A Type describes a type for C's checks: cmd/cc makes one for each type
// once (TypeOf), in a package variable, and passes it to the allocations
// of its functions (LocalT, NewT, NewArrayT, NewAlignedT), which so do
// not look the type up.
type Type = tinfo

// TypeOf returns T's Type.
func TypeOf[T any]() *Type { return tinfoOf[T]() }

// LocalT is Local, of a T whose Type is t.
func LocalT[T any](o *Obj[T], t *Type) Ptr {
	o.h = header{ref: t.ref | refC, ti: t, size: unsafe.Sizeof(o.V)}
	return Ptr{obj: unsafe.Pointer(&o.V), size: unsafe.Sizeof(o.V), ti: t.ref | refC}
}

// New allocates a T for C in the heap.
func New[T any]() Ptr { return Local(new(Obj[T])) }

// NewT is New, of a T whose Type is t.
func NewT[T any](t *Type) Ptr { return LocalT(new(Obj[T]), t) }

// NewArray allocates n bytes for C as an array of T, in the heap.
func NewArray[T any](n uintptr) Ptr { return newOf(tinfoOf[T](), n) }

// NewArrayT allocates n bytes for C as an array of t's type, in the heap.
func NewArrayT(t *Type, n uintptr) Ptr { return newOf(t, n) }

var rawInfo = tinfoOf[Raw]()

// Malloc is C's malloc: n bytes of untyped memory, in which C may keep
// pointers.
func Malloc(n uint64) Ptr { return newOf(rawInfo, uintptr(n)) }

// Free is C's free, which does nothing: the collector frees what is not
// reachable.
func Free(p Ptr) {}

// cobject returns the capability of C's allocation that contains w, if it
// is one.
func cobject(w unsafe.Pointer) (Ptr, bool) {
	base, size := object(w)
	if base == nil || size < hdrSize {
		return Ptr{}, false
	}
	if *(*uintptr)(base) == alignMagic {
		// An over-aligned object (NewAligned): its header is further in.
		off := *(*uintptr)(unsafe.Add(base, wordSize))
		if off < hdrSize || off > size {
			return Ptr{}, false
		}
		base = unsafe.Add(base, off-hdrSize)
	}
	h := (*header)(base)
	if h.ref&refC == 0 || h.ti == nil {
		return Ptr{}, false
	}
	if _, ok := registered.Load(h.ti); !ok || h.ref != h.ti.ref|refC {
		return Ptr{}, false
	}
	data := unsafe.Add(base, hdrSize)
	off := int(uintptr(w) - uintptr(data))
	if off < 0 || uintptr(off) > h.size {
		return Ptr{}, false
	}
	return Ptr{obj: data, size: h.size, off: off, ti: h.ref}, true
}

// derive returns the capability of the untagged word w, which points to a
// want (nil: unsafe.Pointer, any type).
func derive(w unsafe.Pointer, want *tinfo) Ptr {
	if w == nil {
		return Ptr{}
	}
	if p, ok := cobject(w); ok {
		return p
	}
	if want == nil {
		return FromInt(uintptr(w))
	}
	return Ptr{obj: w, size: want.size, ti: tref(want)}
}

// FromGo returns the C pointer for a Go pointer: a capability for the value
// it points to (or the whole of C's allocation, if it points into one).
func FromGo[T any](p *T) Ptr {
	if p == nil {
		return Ptr{}
	}
	if uintptr(unsafe.Pointer(p))&Tag != 0 {
		return FromInt(uintptr(unsafe.Pointer(p)) &^ Tag)
	}
	if c, ok := cobject(unsafe.Pointer(p)); ok {
		return c
	}
	return Ptr{obj: unsafe.Pointer(p), size: unsafe.Sizeof(*p), ti: tref(tinfoOf[T]())}
}

// FromSlice returns the C pointer for s's first element, with a capability
// for its whole array, as cgo lets C reach.
func FromSlice[T any](s []T) Ptr {
	var z T
	return Ptr{obj: unsafe.Pointer(unsafe.SliceData(s)), size: uintptr(cap(s)) * unsafe.Sizeof(z), ti: tref(tinfoOf[T]())}
}

// FromGoUnsafe returns the C pointer for an unsafe.Pointer: only C's own
// allocations have a capability; anything else Go must pass typed.
func FromGoUnsafe(p unsafe.Pointer) Ptr { return derive(p, nil) }

// valid reports whether v may be stored as a plain word where Go reads it
// as a pointer to want.
func valid(v Ptr, want *tinfo) bool {
	if v.obj == nil || v.off < 0 {
		return false
	}
	if want == nil {
		return uintptr(v.off) < v.size
	}
	return uintptr(v.off)+want.size <= v.size && holds(v.info(), uintptr(v.off), want)
}

// encode returns the word for v where Go reads a pointer to want.
func encode(v Ptr, want *tinfo) (w unsafe.Pointer, tagged bool) {
	if valid(v, want) {
		return unsafe.Add(v.obj, v.off), false
	}
	if v.obj == nil && v.off == 0 {
		return nil, false
	}
	return unsafe.Pointer(Addr(v) | Tag), true
}

// ToGo returns p as a Go *T: tagged, so that it faults when Go uses it, if
// it is not a pointer to a whole T.
func ToGo[T any](p Ptr) *T {
	w, _ := encode(p, tinfoOf[T]())
	return (*T)(w)
}

// ToGoUnsafe returns p as an unsafe.Pointer.
func ToGoUnsafe(p Ptr) unsafe.Pointer {
	w, _ := encode(p, nil)
	return w
}

// The capabilities of pointer words in C's objects are kept as Fil-C keeps
// them: beside the object, a slot for each word, which every pointer store
// sets to the capability of what it stores (nil for none). A slot points
// to a header: the header of the C object the capability is of, or a
// capRec for other memory. A load takes the slot's capability for the word
// it reads, if the word is still in it (Go may have written the word
// since). The slots are made when the object first has a pointer stored.
//
// Elsewhere (side.go) a table keeps only the capabilities that words do not
// give back by themselves: tagged words, and capabilities wider than the
// word's type (a slice's array stored in an int*). An entry is used only
// while its word still holds what was stored with it; a stale one can only
// give back a capability C once had for that exact word. Where the table
// is:
//
//   - in a goroutine's stack: in the goroutine, by address, re-keyed by
//     the runtime when the stack moves;
//   - in Go's heap: by the object, removed when the object dies;
//   - in Go's globals: by address.
type auxTable struct {
	mu sync.Mutex
	m  map[uintptr]auxEntry
}

type auxEntry struct {
	word uintptr
	p    Ptr
	ok   bool
}

func (t *auxTable) get(key uintptr) (auxEntry, bool) {
	t.mu.Lock()
	e := t.m[key]
	t.mu.Unlock()
	return e, e.ok
}

// A capRec is a capability of memory that is not C's object, as a slot
// holds it: its first word, the capability's Ptr.ti, is even (no refC),
// where a header's is odd.
type capRec struct {
	ti   uintptr
	obj  unsafe.Pointer
	size uintptr
}

// capOf returns what a slot holds for v's capability.
func capOf(v Ptr) unsafe.Pointer {
	if v.obj == nil {
		return nil
	}
	if v.ti&refC != 0 {
		return unsafe.Add(v.obj, -int(hdrSize))
	}
	return unsafe.Pointer(&capRec{v.ti, v.obj, v.size})
}

// fromCap returns the capability a slot holds, at offset 0.
func fromCap(c unsafe.Pointer) Ptr {
	if h := (*header)(c); h.ref&refC != 0 {
		return Ptr{obj: unsafe.Add(c, hdrSize), size: h.size, ti: h.ref}
	}
	r := (*capRec)(c)
	return Ptr{obj: r.obj, size: r.size, ti: r.ti}
}

// slot returns the slot of the word at off in h's object; nil if it has
// no slots yet, unless create.
func (h *header) slot(off uintptr, create bool) *unsafe.Pointer {
	t := atomic.LoadPointer(&h.aux)
	if t == nil {
		if !create {
			return nil
		}
		t = h.makeSlots()
	}
	return (*unsafe.Pointer)(unsafe.Add(t, off/wordSize*wordSize))
}

func (h *header) makeSlots() unsafe.Pointer {
	t := unsafe.Pointer(unsafe.SliceData(make([]unsafe.Pointer, max((h.size+wordSize-1)/wordSize, 1))))
	if atomic.CompareAndSwapPointer(&h.aux, nil, t) {
		return t
	}
	return atomic.LoadPointer(&h.aux)
}

// check returns the address of n bytes at p, which its capability must
// cover. cmd/compile makes it an intrinsic (ssagen's c23Check): Go's own
// tests of a slice's bounds, which its prove pass reasons about, so that
// a check another dominates goes, and a call of boundsPanic, in a block
// that does not return. This body is the same check, for where the
// intrinsic is not used.
func check(p Ptr, n uintptr) unsafe.Pointer {
	// A pointer with no capability has size 0; a negative offset is
	// past any size.
	if off := uintptr(p.off); off > p.size || n > p.size-off {
		// A panic, not a call: Go knows it does not return, so values
		// need not be saved around it.
		panic(boundsFault{uintptr(p.obj), p.size, p.ti, n, p.off})
	}
	return unsafe.Add(p.obj, p.off)
}

// boundsPanic is check's fault, as the intrinsic calls it.
//
//go:noinline
func boundsPanic(obj unsafe.Pointer, size, ti, n uintptr, off int) {
	panic(boundsFault{uintptr(obj), size, ti, n, off})
}

// checkFail faults for an access check refuses: apart, so that check
// is small enough to inline.
//
//go:noinline
func checkFail(p Ptr, n uintptr) {
	if p.obj == nil {
		fault("access through a pointer with no capability", Addr(p))
	}
	ti, size, off := p.info(), p.size, p.off
	fault(fmt.Sprintf("access of %d bytes out of bounds of %s", n, capString(true, ti, size, off)), Addr(p))
}

func checkPtrWord(p Ptr) unsafe.Pointer {
	a := check(p, wordSize)
	// Aligned, and the object's types are aligned too: one word.
	if uintptr(a)%wordSize != 0 || uintptr(p.off)%wordSize != 0 {
		fault("misaligned pointer access", uintptr(a))
	}
	return a
}

// checkScalar checks a store of n bytes at p.
func checkScalar(p Ptr, n uintptr) unsafe.Pointer {
	a := check(p, n)
	if p.ti&2 != 0 { // no word a scalar store may not write
		return a
	}
	return checkScalarWords(p, n, a)
}

func checkScalarWords(p Ptr, n uintptr, a unsafe.Pointer) unsafe.Pointer {
	ti := p.info()
	if ti.fewWords && n > 0 {
		// The words the store covers, in one value of the type, against
		// the mask of those it may not write.
		if o := ti.mod(uintptr(p.off)); o+n <= ti.size {
			w0, w1 := o/wordSize, (o+n-1)/wordSize
			if ti.noStore>>w0&(2<<(w1-w0)-1) == 0 {
				return a
			}
		}
	}
	end := uintptr(p.off) + n
	for b := uintptr(p.off); b < end; b = (b + wordSize) &^ (wordSize - 1) {
		switch ti.kindAt(b) {
		case wPtr:
			fault("scalar store over a pointer word", uintptr(a))
		case wRO:
			fault(fmt.Sprintf("store into a word of a Go value with invariants (%s)", typeName(p.info())), uintptr(a))
		}
	}
	return a
}

// splitLoad loads a pointer from a split word outside C's objects: its
// capability is the one kept for the word, whatever the bytes hold now, as
// in Fil-C; every access is checked against it.
func splitLoad(p Ptr, a unsafe.Pointer) Ptr {
	w := *(*uintptr)(a)
	if c, ok := auxLoad(p, a, w, false); ok {
		return Ptr{obj: c.obj, size: c.size, off: int(w - uintptr(c.obj)), ti: c.ti}
	}
	return FromInt(w)
}

// LoadPtr loads the pointer at p. Its fast path, which cmd/compile
// inlines, is a C object's slot holding the header of the C object the
// word is still in.
func LoadPtr(p Ptr) Ptr {
	// C's object (whose base is aligned), an aligned word in bounds.
	if off := uintptr(p.off); p.ti&refC != 0 && off%wordSize == 0 && off < p.size && p.size-off >= wordSize {
		// A slot is only set by a pointer store, which checked the word
		// may hold one.
		w := *(*uintptr)(unsafe.Add(p.obj, off))
		var c *header
		if t := atomic.LoadPointer(&(*header)(abi.NoEscape(unsafe.Add(p.obj, -int(hdrSize)))).aux); t != nil {
			c = (*header)(atomic.LoadPointer((*unsafe.Pointer)(unsafe.Add(t, off))))
		}
		if c == nil {
			if w == 0 {
				// NULL, no capability kept (a NULL stored clears the
				// slot): as loadPtr finds it, for any kind of word.
				return Ptr{}
			}
		} else if c.ref&refC != 0 {
			// The slot's C object's header (a capRec's first word is
			// even): its capability, and its type, at once.
			obj := unsafe.Add(unsafe.Pointer(c), hdrSize)
			// A split word's capability is its slot's, whatever the
			// bytes hold (Fil-C's); another word's, only while it is in
			// it (Go may have written the word since).
			if o := w - uintptr(obj); p.ti&refSplit != 0 || o <= c.size {
				return Ptr{obj: obj, size: c.size, off: int(o), ti: c.ref}
			}
		}
	}
	return loadPtrSlow(p)
}

//go:noinline
func loadPtrSlow(p Ptr) Ptr { return loadPtr(p, checkPtrWord(p)) }

func loadPtr(p Ptr, a unsafe.Pointer) Ptr {
	k := p.info().kindAt(uintptr(p.off))
	if k != wPtr && k != wSplit {
		// An integer, or a word of a Go value, read as a pointer: no
		// capability.
		return FromInt(*(*uintptr)(a))
	}
	if h := p.header(); h != nil {
		w := *(*uintptr)(a)
		if s := h.slot(uintptr(p.off), false); s != nil {
			if c := atomic.LoadPointer(s); c != nil {
				q := fromCap(c)
				if k == wSplit {
					// Whatever the bytes hold now.
					q.off = int(w - uintptr(q.obj))
					return q
				}
				// A tagged word is one C stored (out of the bounds or
				// type of its capability); a plain one may be Go's since.
				if q.off = int(w&^Tag - uintptr(q.obj)); uintptr(q.off) <= q.size || w&Tag != 0 {
					return q
				}
			}
		}
		if k == wSplit {
			return FromInt(w)
		}
	} else if k == wSplit {
		return splitLoad(p, a)
	} else if c, ok := auxLoad(p, a, *(*uintptr)(a), true); ok {
		return c
	}
	w := *(*unsafe.Pointer)(a)
	if uintptr(w)&Tag != 0 {
		// Copied by Go, or a stale word: the capability is lost.
		return FromInt(uintptr(w) &^ Tag)
	}
	return derive(w, p.info().elemAt(uintptr(p.off)))
}

// StorePtr stores the pointer v at p. Its fast path, which cmd/compile
// inlines, is C's pointer into C's object stored in a split word of C's
// object (a union's, say).
func StorePtr(p, v Ptr) {
	if off := uintptr(p.off); p.ti&v.ti&refC != 0 && off%wordSize == 0 && off < p.size && p.size-off >= wordSize {
		h := (*header)(abi.NoEscape(unsafe.Add(p.obj, -int(hdrSize))))
		// A split word: of memory that is all split (malloc's, refSplit),
		// or found by a mask: C's arrays' elements are mostly of a size
		// that is a power of two.
		if t := atomic.LoadPointer(&h.aux); t != nil && (p.ti&refSplit != 0 || h.ti.mask != 0 && h.ti.kinds[(off&h.ti.mask)/wordSize] == wSplit) {
			// The capability first, so that what it points to stays
			// alive; a plain store, with the write barrier: a load
			// racing with it sees the old capability or the new one, as
			// Go's memory model has a word-sized read see one write.
			*(*unsafe.Pointer)(unsafe.Add(t, off)) = unsafe.Add(v.obj, -int(hdrSize))
			*(*uintptr)(unsafe.Add(p.obj, off)) = Addr(v)
			return
		}
	}
	storePtrSlow(p, v)
}

//go:noinline
func storePtrSlow(p, v Ptr) { storePtr(p, v, checkPtrWord(p)) }

func storePtr(p, v Ptr, a unsafe.Pointer) {
	k := p.info().kindAt(uintptr(p.off))
	switch k {
	case wScalar:
		fault("pointer store into a word the collector does not scan", uintptr(a))
	case wRO:
		fault(fmt.Sprintf("store into a word of a Go value with invariants (%s)", typeName(p.info())), uintptr(a))
	}
	if h := p.header(); h != nil {
		// The capability first, so that what it points to stays alive.
		c := capOf(v)
		if s := h.slot(uintptr(p.off), c != nil); s != nil {
			*s = c // as above
		}
		if k == wSplit {
			*(*uintptr)(a) = Addr(v)
			return
		}
		w, _ := encode(v, p.info().elemAt(uintptr(p.off)))
		*(*unsafe.Pointer)(a) = w // with the write barrier
		return
	}
	if k == wSplit {
		auxStore(p, a, v.obj != nil, Addr(v), v)
		*(*uintptr)(a) = Addr(v)
		return
	}
	want := p.info().elemAt(uintptr(p.off))
	w, tagged := encode(v, want)
	keep := tagged && v.obj != nil
	if !tagged && w != nil {
		// Keep the capability if the word does not give it back.
		d := derive(w, want)
		keep = d.obj == nil || Addr(Ptr{obj: d.obj}) != Addr(Ptr{obj: v.obj}) || d.size != v.size || d.ti != v.ti
	}
	auxStore(p, a, keep, uintptr(w), v)
	*(*unsafe.Pointer)(a) = w // with the write barrier
}

func Load8(p Ptr) uint8   { return *(*uint8)(check(p, 1)) }
func Load16(p Ptr) uint16 { return *(*uint16)(check(p, 2)) }
func Load32(p Ptr) uint32 { return *(*uint32)(check(p, 4)) }
func Load64(p Ptr) uint64 { return *(*uint64)(check(p, 8)) }

func LoadF32(p Ptr) float32 { return math.Float32frombits(Load32(p)) }
func LoadF64(p Ptr) float64 { return math.Float64frombits(Load64(p)) }

func Store8(p Ptr, v uint8)   { *(*uint8)(checkScalar(p, 1)) = v }
func Store16(p Ptr, v uint16) { *(*uint16)(checkScalar(p, 2)) = v }
func Store32(p Ptr, v uint32) { *(*uint32)(checkScalar(p, 4)) = v }
func Store64(p Ptr, v uint64) { *(*uint64)(checkScalar(p, 8)) = v }

func StoreF32(p Ptr, v float32) { Store32(p, math.Float32bits(v)) }
func StoreF64(p Ptr, v float64) { Store64(p, math.Float64bits(v)) }

// Memmove copies n bytes from src to dst. A word that is a pointer in
// both is copied as a pointer (with its capability); a pointer copied into
// a scalar word becomes its address; a scalar copied into a pointer word
// must be zero.
func Memmove(dst, src Ptr, n uintptr) {
	if n == wordSize && (Addr(dst)|Addr(src))%wordSize == 0 && Addr(dst) != Addr(src) {
		// One word (a union of C's, often), which moveWord checks.
		moveWord(dst, src)
		return
	}
	if n == 0 {
		return
	}
	d := check(dst, n)
	s := check(src, n)
	if src.info().scalarOnly && dst.info().scalarOnly || uintptr(d)%wordSize != 0 || uintptr(s)%wordSize != 0 {
		checkScalar(dst, n)
		copy(unsafe.Slice((*byte)(d), n), unsafe.Slice((*byte)(s), n))
		return
	}
	if dst.ti == src.ti && dst.ti&refC != 0 && n%wordSize == 0 {
		if ti := dst.info(); ti.size%wordSize == 0 && !ti.hasRO && ti.mod(uintptr(dst.off)) == ti.mod(uintptr(src.off)) {
			moveSame(dst, src, n, d, s, ti)
			return
		}
	}
	nw := n / wordSize
	if !overlaps(dst, src, n) {
		// Word by word: no copy aside needed.
		for i := uintptr(0); i < nw; i++ {
			moveWord(Add(dst, int(i*wordSize)), Add(src, int(i*wordSize)))
		}
		if t := n % wordSize; t > 0 {
			d2 := checkScalar(Add(dst, int(nw*wordSize)), t)
			copy(unsafe.Slice((*byte)(d2), t), unsafe.Slice((*byte)(unsafe.Add(s, nw*wordSize)), t))
		}
		return
	}
	type word struct {
		ptr bool
		p   Ptr
		u   uintptr
	}
	ws := make([]word, nw)
	for i := range nw {
		sp := Add(src, int(i*wordSize))
		switch src.info().kindAt(uintptr(sp.off)) {
		case wPtr:
			ws[i] = word{ptr: true, p: LoadPtr(sp)}
		case wSplit:
			if c := LoadPtr(sp); c.obj != nil {
				ws[i] = word{ptr: true, p: c}
			} else {
				ws[i] = word{u: loadWord(sp)}
			}
		default:
			ws[i] = word{u: loadWord(sp)}
		}
	}
	tail := make([]byte, n%wordSize)
	copy(tail, unsafe.Slice((*byte)(unsafe.Add(s, nw*wordSize)), n%wordSize))
	for i, w := range ws {
		dp := Add(dst, i*int(wordSize))
		switch {
		case dst.info().kindAt(uintptr(dp.off)) == wPtr:
			if !w.ptr && w.u != 0 {
				fault("non-zero scalar copied into a pointer word", Addr(dp))
			}
			StorePtr(dp, w.p)
		case w.ptr && dst.info().kindAt(uintptr(dp.off)) == wSplit:
			StorePtr(dp, w.p)
		case w.ptr:
			storeWord(dp, Addr(w.p))
		default:
			storeWord(dp, w.u)
		}
	}
	if len(tail) > 0 {
		t := checkScalar(Add(dst, int(nw*wordSize)), uintptr(len(tail)))
		copy(unsafe.Slice((*byte)(t), len(tail)), tail)
	}
}

// moveSame is Memmove of n bytes, whole words, at d and s, between C's
// objects of one type, ti, at the same offset in a value of it: a word is
// of one kind in both, so each is copied as it is, a pointer word with the
// write barrier, and the slots of the capabilities with them, as Fil-C
// copies its auxiliary words.
func moveSame(dst, src Ptr, n uintptr, d, s unsafe.Pointer, ti *tinfo) {
	nw := n / wordSize
	// The slots first, so that what the pointers point to stays alive.
	var ss unsafe.Pointer
	if t := atomic.LoadPointer(&src.header().aux); t != nil {
		ss = unsafe.Add(t, uintptr(src.off))
	}
	dh := dst.header()
	dt := atomic.LoadPointer(&dh.aux)
	if dt == nil && ss != nil {
		dt = dh.makeSlots()
	}
	if dt != nil {
		ds := unsafe.Slice((*unsafe.Pointer)(unsafe.Add(dt, uintptr(dst.off))), nw)
		if ss != nil {
			copy(ds, unsafe.Slice((*unsafe.Pointer)(ss), nw))
		} else {
			clear(ds)
		}
	}
	nk := uintptr(len(ti.kinds))
	k := ti.mod(uintptr(src.off)) / wordSize
	if uintptr(d) > uintptr(s) && uintptr(d) < uintptr(s)+n {
		// Overlapping, the destination later: from the end.
		k = (k + nw - 1) % nk
		for i := nw; i > 0; i-- {
			moveWordOf(ti.kinds[k], unsafe.Add(d, (i-1)*wordSize), unsafe.Add(s, (i-1)*wordSize))
			if k == 0 {
				k = nk
			}
			k--
		}
		return
	}
	for i := uintptr(0); i < nw; i++ {
		moveWordOf(ti.kinds[k], unsafe.Add(d, i*wordSize), unsafe.Add(s, i*wordSize))
		if k++; k == nk {
			k = 0
		}
	}
}

func moveWordOf(k wordKind, d, s unsafe.Pointer) {
	if k == wPtr {
		*(*unsafe.Pointer)(d) = *(*unsafe.Pointer)(s) // with the write barrier
		return
	}
	*(*uintptr)(d) = *(*uintptr)(s)
}

// overlaps reports whether n bytes at a and at b may overlap.
func overlaps(a, b Ptr, n uintptr) bool {
	x, y := Addr(a), Addr(b)
	return x < y+n && y < x+n
}

// MoveWord is Memmove of one word, which cmd/cc makes of the assignment
// of a union or struct of a word (Lua's TValue's value).
func MoveWord(dst, src Ptr) {
	if moveSplit(dst, src) {
		return
	}
	Memmove(dst, src, wordSize)
}

// moveSplit copies the word at src to dst if both are words of C's objects
// whose memory is all split (malloc's): the slot, then the bytes, as Fil-C
// copies a word with its auxiliary word, and as Memmove does with them: the
// capability goes with the bytes, whatever they are. It reports false,
// having done nothing, if not.
func moveSplit(dst, src Ptr) bool {
	do, so := uintptr(dst.off), uintptr(src.off)
	if dst.ti&src.ti&(refC|refSplit) != refC|refSplit || (do|so)%wordSize != 0 ||
		do >= dst.size || dst.size-do < wordSize || so >= src.size || src.size-so < wordSize {
		return false
	}
	var c unsafe.Pointer
	if t := atomic.LoadPointer(&(*header)(abi.NoEscape(unsafe.Add(src.obj, -int(hdrSize)))).aux); t != nil {
		c = atomic.LoadPointer((*unsafe.Pointer)(unsafe.Add(t, so)))
	}
	t := atomic.LoadPointer(&(*header)(abi.NoEscape(unsafe.Add(dst.obj, -int(hdrSize)))).aux)
	if t == nil {
		if c != nil {
			return false // the destination has no slots yet
		}
	} else {
		*(*unsafe.Pointer)(unsafe.Add(t, do)) = c // with the write barrier
	}
	*(*uintptr)(unsafe.Add(dst.obj, do)) = *(*uintptr)(unsafe.Add(src.obj, so))
	return true
}

// moveWord copies the word at src to dst, as Memmove does.
func moveWord(dst, src Ptr) {
	sk := src.info().kindAt(uintptr(src.off))
	dk := dst.info().kindAt(uintptr(dst.off))
	if sk == wSplit && dk == wSplit && dst.ti&src.ti&refC != 0 {
		// Split words of C's objects (a union copied): the bytes, and
		// the slot's capability, whatever the bytes are.
		a, b := checkPtrWord(dst), checkPtrWord(src)
		var c unsafe.Pointer
		if s := src.header().slot(uintptr(src.off), false); s != nil {
			c = atomic.LoadPointer(s)
		}
		if s := dst.header().slot(uintptr(dst.off), c != nil); s != nil {
			*s = c
		}
		*(*uintptr)(a) = *(*uintptr)(b)
		return
	}
	if sk != wPtr && sk != wSplit && dk != wPtr && dk != wSplit {
		storeWord(dst, loadWord(src))
		return
	}
	var p Ptr
	isPtr := false
	switch sk {
	case wPtr:
		p, isPtr = LoadPtr(src), true
	case wSplit:
		if c := LoadPtr(src); c.obj != nil {
			p, isPtr = c, true
		}
	}
	switch {
	case dk == wPtr:
		if !isPtr {
			if u := loadWord(src); u != 0 {
				fault("non-zero scalar copied into a pointer word", Addr(dst))
			}
		}
		StorePtr(dst, p)
	case isPtr && dk == wSplit:
		StorePtr(dst, p)
	case isPtr:
		storeWord(dst, Addr(p))
	default:
		storeWord(dst, loadWord(src))
	}
}

// Memset sets n bytes at p to c. Pointer words may only be set to zero.
func Memset(p Ptr, c int32, size uint64) Ptr {
	n := uintptr(size)
	a := check(p, n)
	for i := uintptr(0); i < n; {
		q := Add(p, int(i))
		if (uintptr(a)+i)%wordSize == 0 && i+wordSize <= n && p.info().kindAt(uintptr(q.off)) == wPtr {
			if c&0xff != 0 {
				fault("non-zero memset of a pointer word", Addr(q))
			}
			StorePtr(q, Ptr{})
			i += wordSize
			continue
		}
		Store8(q, uint8(c))
		i++
	}
	return p
}

// typeName is a type's name for messages: C's types by cgo's names.
func typeName(ti *tinfo) string {
	name := ti.t.String()
	if i := strings.Index(name, "._C23"); i >= 0 {
		return "C." + name[i+len("._C23"):]
	}
	return name
}
