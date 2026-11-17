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

// A Ptr holds its *tinfo as an index into tinfoTab (and bits saying the
// memory is C's object, and holds no pointers, which checks read without
// the table): escape analysis, which sees a Ptr as one value,
// would otherwise take every use of the type as a use of the memory and
// move what the memory points to off the stack; and an index, unlike a
// pointer made of an integer, is the same on every target.
func tref(ti *tinfo) uintptr {
	r := uintptr(ti.id) << 2
	if ti.scalarOnly {
		r |= 2
	}
	return r
}

func (p Ptr) info() *tinfo { return (*tinfoTab.Load())[p.ti>>2] }

// cref is tref for a capability of C's object, which has a header.
func cref(ti *tinfo) uintptr { return tref(ti) | 1 }

func (p Ptr) header() *header {
	if p.ti&1 == 0 {
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
	ti := (*tinfoTab.Load())[b.ti>>2]
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

// The header of C's allocations.
type header struct {
	magic uintptr
	ti    *tinfo
	size  uintptr
	aux   unsafe.Pointer // *auxTable, made when first needed
}

const magic = 0xC0FFEE_CAB_C0DE

const hdrSize = unsafe.Sizeof(header{})

// Obj is a C object: a header, then the value. C's locals are Go
// variables of this type, which escape analysis keeps on the stack unless
// C stores their address.
type Obj[T any] struct {
	h header
	V T // the value: what Go that cmd/cc generates reaches directly, when it knows the object
}

// Local returns the C pointer for a local C object.
func Local[T any](o *Obj[T]) Ptr {
	ti := tinfoOf[T]()
	o.h = header{magic: magic, ti: ti, size: unsafe.Sizeof(o.V)}
	return Ptr{obj: unsafe.Pointer(&o.V), size: unsafe.Sizeof(o.V), ti: cref(ti)}
}

// New allocates a T for C in the heap.
func New[T any]() Ptr { return Local(new(Obj[T])) }

// NewArray allocates n bytes for C as an array of T, in the heap.
func NewArray[T any](n uintptr) Ptr {
	ti := tinfoOf[T]()
	sz := ti.t.Size()
	count := n
	if sz > 0 {
		count = (n + sz - 1) / sz
	}
	st := reflectArrayObj(ti.t, int(count))
	base := newObj(st)
	h := (*header)(base)
	*h = header{magic: magic, ti: ti, size: count * sz}
	return Ptr{obj: unsafe.Add(base, hdrSize), size: count * sz, ti: cref(ti)}
}

// Malloc is C's malloc: n bytes of untyped memory, in which C may keep
// pointers.
func Malloc(n uint64) Ptr { return NewArray[Raw](uintptr(n)) }

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
		off := *(*uintptr)(unsafe.Add(base, 8))
		if off < hdrSize || off > size {
			return Ptr{}, false
		}
		base = unsafe.Add(base, off-hdrSize)
	}
	h := (*header)(base)
	if h.magic != magic || h.ti == nil {
		return Ptr{}, false
	}
	if _, ok := registered.Load(h.ti); !ok {
		return Ptr{}, false
	}
	data := unsafe.Add(base, hdrSize)
	off := int(uintptr(w) - uintptr(data))
	if off < 0 || uintptr(off) > h.size {
		return Ptr{}, false
	}
	return Ptr{obj: data, size: h.size, off: off, ti: cref(h.ti)}, true
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

// The capabilities that words do not give back by themselves: tagged
// words, and capabilities wider than the word's type (a slice's array
// stored in an int*). An entry is used only while its word still holds
// what was stored with it; a stale one can only give back a capability C
// once had for that exact word. Where the table is (side.go):
//
//   - in C's object: in its header, by the word's offset;
//   - in a goroutine's stack: in the goroutine, by address, re-keyed by
//     the runtime when the stack moves;
//   - in Go's heap: by the object, removed when the object dies;
//   - in Go's globals: by address.
//
// A C object's table is dense, an entry for each word, found by index;
// the others are maps.
type auxTable struct {
	mu    sync.Mutex
	m     map[uintptr]auxEntry
	dense []auxEntry // C's object's: by offset/8, valid if ok
	// seq is odd while dense is being written: a reader reads without
	// the lock, and again if seq changed (a seqlock).
	seq atomic.Uint64
}

type auxEntry struct {
	word uintptr
	p    Ptr
	ok   bool
}

func (t *auxTable) get(key uintptr) (auxEntry, bool) {
	if t.dense != nil {
		i := key / 8
		if i >= uintptr(len(t.dense)) {
			return auxEntry{}, false
		}
		for {
			s := t.seq.Load()
			if s&1 != 0 {
				continue
			}
			e := t.dense[i]
			if t.seq.Load() == s {
				return e, e.ok
			}
		}
	}
	t.mu.Lock()
	e := t.m[key]
	t.mu.Unlock()
	return e, e.ok
}

func (h *header) table(create bool) *auxTable {
	if t := (*auxTable)(atomic.LoadPointer(&h.aux)); t != nil || !create {
		return t
	}
	t := &auxTable{dense: make([]auxEntry, (h.size+7)/8)}
	if atomic.CompareAndSwapPointer(&h.aux, nil, unsafe.Pointer(t)) {
		return t
	}
	return (*auxTable)(atomic.LoadPointer(&h.aux))
}

func check(p Ptr, n uintptr) unsafe.Pointer {
	// A pointer with no capability has size 0; a negative offset is
	// past any size.
	if off := uintptr(p.off); off > p.size || n > p.size-off {
		panic(boundsFault{uintptr(p.obj), p.size, p.ti, n, p.off})
	}
	return unsafe.Add(p.obj, p.off)
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
	a := check(p, 8)
	// Aligned, and the object's types are aligned too: one word.
	if uintptr(a)%8 != 0 || uintptr(p.off)%8 != 0 {
		fault("misaligned pointer access", uintptr(a))
	}
	return a
}

// checkScalar checks a store of n bytes at p.
func checkScalar(p Ptr, n uintptr) unsafe.Pointer {
	a := check(p, n)
	if p.ti&2 != 0 { // scalars only
		return a
	}
	return checkScalarWords(p, n, a)
}

func checkScalarWords(p Ptr, n uintptr, a unsafe.Pointer) unsafe.Pointer {
	ti := p.info()
	end := uintptr(p.off) + n
	for b := uintptr(p.off); b < end; b = (b + 8) &^ 7 {
		switch ti.kindAt(b) {
		case wPtr:
			fault("scalar store over a pointer word", uintptr(a))
		case wRO:
			fault(fmt.Sprintf("store into a word of a Go value with invariants (%s)", typeName(p.info())), uintptr(a))
		}
	}
	return a
}

// splitLoad loads a pointer from a split word: its capability is the one
// kept for the word, whatever the bytes hold now, as in Fil-C; every access
// is checked against it.
func splitLoad(p Ptr, a unsafe.Pointer) Ptr {
	w := *(*uintptr)(a)
	if c, ok := auxLoad(p, a, w, false); ok {
		return Ptr{obj: c.obj, size: c.size, off: int(w - uintptr(c.obj)), ti: c.ti}
	}
	return FromInt(w)
}

// splitStore stores v in a split word: its capability aside, first, so
// that what it points to stays alive, then its address as bytes.
func splitStore(p Ptr, a unsafe.Pointer, v Ptr) {
	auxStore(p, a, v.obj != nil, Addr(v), v)
	*(*uintptr)(a) = Addr(v)
}

// LoadPtr loads the pointer at p.
func LoadPtr(p Ptr) Ptr {
	a := checkPtrWord(p)
	switch p.info().kindAt(uintptr(p.off)) {
	case wSplit:
		return splitLoad(p, a)
	case wPtr:
	default:
		// An integer, or a word of a Go value, read as a pointer: no
		// capability.
		return FromInt(*(*uintptr)(a))
	}
	w := *(*unsafe.Pointer)(a)
	if c, ok := auxLoad(p, a, uintptr(w), true); ok {
		return c
	}
	if uintptr(w)&Tag != 0 {
		// Copied by Go, or a stale word: the capability is lost.
		return FromInt(uintptr(w) &^ Tag)
	}
	return derive(w, p.info().elemAt(uintptr(p.off)))
}

// StorePtr stores the pointer v at p.
func StorePtr(p, v Ptr) {
	a := checkPtrWord(p)
	switch p.info().kindAt(uintptr(p.off)) {
	case wSplit:
		splitStore(p, a, v)
		return
	case wScalar:
		fault("pointer store into a word the collector does not scan", uintptr(a))
	case wRO:
		fault(fmt.Sprintf("store into a word of a Go value with invariants (%s)", typeName(p.info())), uintptr(a))
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
	if n == 0 {
		return
	}
	d := check(dst, n)
	s := check(src, n)
	if src.info().scalarOnly && dst.info().scalarOnly || uintptr(d)%8 != 0 || uintptr(s)%8 != 0 {
		checkScalar(dst, n)
		copy(unsafe.Slice((*byte)(d), n), unsafe.Slice((*byte)(s), n))
		return
	}
	nw := n / 8
	if !overlaps(dst, src, n) {
		// Word by word: no copy aside needed.
		for i := uintptr(0); i < nw; i++ {
			moveWord(Add(dst, int(i*8)), Add(src, int(i*8)))
		}
		if t := n % 8; t > 0 {
			d2 := checkScalar(Add(dst, int(nw*8)), t)
			copy(unsafe.Slice((*byte)(d2), t), unsafe.Slice((*byte)(unsafe.Add(s, nw*8)), t))
		}
		return
	}
	type word struct {
		ptr bool
		p   Ptr
		u   uint64
	}
	ws := make([]word, nw)
	for i := range nw {
		sp := Add(src, int(i*8))
		switch src.info().kindAt(uintptr(sp.off)) {
		case wPtr:
			ws[i] = word{ptr: true, p: LoadPtr(sp)}
		case wSplit:
			if c := LoadPtr(sp); c.obj != nil {
				ws[i] = word{ptr: true, p: c}
			} else {
				ws[i] = word{u: Load64(sp)}
			}
		default:
			ws[i] = word{u: Load64(sp)}
		}
	}
	tail := make([]byte, n%8)
	copy(tail, unsafe.Slice((*byte)(unsafe.Add(s, nw*8)), n%8))
	for i, w := range ws {
		dp := Add(dst, i*8)
		switch {
		case dst.info().kindAt(uintptr(dp.off)) == wPtr:
			if !w.ptr && w.u != 0 {
				fault("non-zero scalar copied into a pointer word", Addr(dp))
			}
			StorePtr(dp, w.p)
		case w.ptr && dst.info().kindAt(uintptr(dp.off)) == wSplit:
			StorePtr(dp, w.p)
		case w.ptr:
			Store64(dp, uint64(Addr(w.p)))
		default:
			Store64(dp, w.u)
		}
	}
	if len(tail) > 0 {
		t := checkScalar(Add(dst, int(nw*8)), uintptr(len(tail)))
		copy(unsafe.Slice((*byte)(t), len(tail)), tail)
	}
}

// overlaps reports whether n bytes at a and at b may overlap.
func overlaps(a, b Ptr, n uintptr) bool {
	x, y := Addr(a), Addr(b)
	return x < y+n && y < x+n
}

// moveWord copies the word at src to dst, as Memmove does.
func moveWord(dst, src Ptr) {
	sk := src.info().kindAt(uintptr(src.off))
	dk := dst.info().kindAt(uintptr(dst.off))
	if sk != wPtr && sk != wSplit && dk != wPtr && dk != wSplit {
		Store64(dst, Load64(src))
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
			if u := Load64(src); u != 0 {
				fault("non-zero scalar copied into a pointer word", Addr(dst))
			}
		}
		StorePtr(dst, p)
	case isPtr && dk == wSplit:
		StorePtr(dst, p)
	case isPtr:
		Store64(dst, uint64(Addr(p)))
	default:
		Store64(dst, Load64(src))
	}
}

// Memset sets n bytes at p to c. Pointer words may only be set to zero.
func Memset(p Ptr, c int32, size uint64) Ptr {
	n := uintptr(size)
	a := check(p, n)
	for i := uintptr(0); i < n; {
		q := Add(p, int(i))
		if (uintptr(a)+i)%8 == 0 && i+8 <= n && p.info().kindAt(uintptr(q.off)) == wPtr {
			if c&0xff != 0 {
				fault("non-zero memset of a pointer word", Addr(q))
			}
			StorePtr(q, Ptr{})
			i += 8
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
