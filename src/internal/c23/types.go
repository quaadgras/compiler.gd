// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"internal/abi"
	"math/bits"
	"reflect"
	"sync"
	"sync/atomic"
	"unsafe"
)

// What C may do with a word of memory, from the Go type of the memory.
type wordKind uint8

const (
	wScalar wordKind = iota // C may read and write it as bytes
	wPtr                    // a pointer: C reads and writes it with LoadPtr and StorePtr
	wRO                     // a word of a Go value with invariants (bool, string, slice,
	// interface, map, chan, func): C may read it, never write it
	wSplit // bytes of a union or of untyped memory: scalars to Go and the
	// collector, but C may store pointers in them, whose capabilities are
	// kept aside (side.go)
)

// Raw is the element of C's untyped memory (malloc'd bytes, pools that
// placement new carves up): bytes in which C may also keep pointers.
type Raw struct {
	b byte `c23:"union"`
}

// A tinfo describes a type for C's checks: what each word of one value of
// it is, and what its pointer words point to. Memory a capability covers is
// an array of its tinfo's type.
type tinfo struct {
	id         int // in tinfoTab
	t          reflect.Type
	size       uintptr
	kinds      []wordKind     // per word (wordSize bytes) of one value
	elems      []reflect.Type // per word: the pointee of a wPtr word (nil: unsafe.Pointer)
	scalarOnly bool
	// storesFree: no word a scalar store may not write (all are scalar
	// or split), so scalar stores need no check of the words.
	storesFree bool
	// noStore has a bit for each word a scalar store may not write
	// (wPtr, wRO), if the type has at most 64 words (fewWords).
	noStore  uint64
	fewWords bool
	hasRO    bool    // a word of a Go value with invariants (wRO)
	mask     uintptr // size-1, if size is a power of two
	recip    uint64  // ceil(2^64/size): off%size by multiplication (Lemire's)
	ref      uintptr // what a Ptr's ti is for it (tref)

	elemInfo []atomic.Pointer[tinfo] // elems' tinfos, found when first needed
	// Recent answers of holds for this type, as memory mostly holds a
	// few types of value at a few offsets; replaced round-robin.
	recentHolds [4]atomic.Pointer[holdsAns]
	nextHolds   atomic.Uint32

	arrays atomic.Pointer[[arrayBuckets]atomic.Pointer[arrayType]] // arrayObj's types, made when first needed
}

type arrayType struct{ t reflect.Type }

type holdsAns struct {
	want *tinfo
	off  uintptr
	r    bool
}

var tinfos sync.Map // reflect.Type -> *tinfo

// tinfoTab holds every tinfo by id, copied when one is added (types are
// few), so that reading it needs no lock.
var (
	tinfoTab = func() *atomic.Pointer[[]*tinfo] {
		var p atomic.Pointer[[]*tinfo]
		p.Store(new([]*tinfo))
		return &p
	}()
	tinfoMu sync.Mutex
)

// tinfoFast holds the first tinfos by id, as tinfoTab does, where Ptr.info
// finds them with one load (a program has a few thousand types at most).
var tinfoFast [4096]atomic.Pointer[tinfo]

// registered holds every tinfo, so that a header found in memory can be
// trusted: safe Go code cannot make a *tinfo, so it cannot forge one.
var registered sync.Map // *tinfo -> struct{}

func infoOf(t reflect.Type) *tinfo {
	if ti, ok := tinfos.Load(t); ok {
		return ti.(*tinfo)
	}
	size := t.Size()
	n := (size + wordSize - 1) / wordSize
	ti := &tinfo{t: t, size: max(size, 1), kinds: make([]wordKind, max(n, 1)), elems: make([]reflect.Type, max(n, 1)), elemInfo: make([]atomic.Pointer[tinfo], max(n, 1))}
	ti.walk(t, 0)
	if ti.size&(ti.size-1) == 0 {
		ti.mask = ti.size - 1
	}
	ti.recip = ^uint64(0)/uint64(ti.size) + 1
	ti.scalarOnly, ti.storesFree = true, true
	allSplit := true
	ti.fewWords = len(ti.kinds) <= 64
	for i, k := range ti.kinds {
		if k != wSplit {
			allSplit = false
		}
		if k == wRO {
			ti.hasRO = true
		}
		if (k == wPtr || k == wRO) && ti.fewWords {
			ti.noStore |= 1 << i
		}
		if k != wScalar {
			ti.scalarOnly = false
		}
		if k != wScalar && k != wSplit {
			ti.storesFree = false
		}
	}
	tinfoMu.Lock()
	defer tinfoMu.Unlock()
	if old, ok := tinfos.Load(t); ok {
		return old.(*tinfo)
	}
	tab := append(append([]*tinfo(nil), *tinfoTab.Load()...), ti)
	ti.id = len(tab) - 1
	ti.ref = uintptr(ti.id) << refShift
	if ti.storesFree {
		ti.ref |= refStoresFree
	}
	if allSplit {
		ti.ref |= refSplit
	}
	tinfoTab.Store(&tab)
	if ti.id < len(tinfoFast) {
		tinfoFast[ti.id].Store(ti)
	}
	tinfos.Store(t, ti)
	registered.Store(ti, struct{}{})
	return ti
}

// tinfoOf returns T's tinfo, from fastTinfos, a table by the address of
// Go's type, without hashing a reflect.Type.
func tinfoOf[T any]() *tinfo {
	k := unsafe.Pointer(abi.TypeFor[T]())
	if tab := fastTinfos.Load(); tab != nil {
		for i := fastHash(k) & tab.mask; ; i = (i + 1) & tab.mask {
			e := tab.e[i].Load()
			if e == nil {
				break
			}
			if e.k == k {
				return e.ti
			}
		}
	}
	ti := infoOf(reflect.TypeFor[T]())
	addFast(k, ti)
	return ti
}

// fastTinfos is an open-addressed table, replaced when it gets half full.
var fastTinfos atomic.Pointer[fastTab]

type fastTab struct {
	e    []atomic.Pointer[fastEntry]
	mask uintptr
	n    int
}

type fastEntry struct {
	k  unsafe.Pointer
	ti *tinfo
}

func fastHash(k unsafe.Pointer) uintptr {
	return uintptr(uint64(uintptr(k)>>4) * 0x9E3779B97F4A7C15 >> 32)
}

func addFast(k unsafe.Pointer, ti *tinfo) {
	tinfoMu.Lock()
	defer tinfoMu.Unlock()
	tab := fastTinfos.Load()
	put := func(tab *fastTab, e *fastEntry) bool {
		for i := fastHash(e.k) & tab.mask; ; i = (i + 1) & tab.mask {
			switch old := tab.e[i].Load(); {
			case old == nil:
				tab.e[i].Store(e)
				tab.n++
				return true
			case old.k == e.k:
				return false
			}
		}
	}
	if tab == nil || 2*(tab.n+1) > len(tab.e) {
		size := 64
		if tab != nil {
			size = 2 * len(tab.e)
		}
		nt := &fastTab{e: make([]atomic.Pointer[fastEntry], size), mask: uintptr(size - 1)}
		if tab != nil {
			for i := range tab.e {
				if e := tab.e[i].Load(); e != nil {
					put(nt, e)
				}
			}
		}
		tab = nt
	}
	put(tab, &fastEntry{k, ti})
	fastTinfos.Store(tab)
}

// rank orders the kinds a word may be given more than once: the most
// restrictive wins.
var rank = [...]int{wScalar: 0, wSplit: 1, wPtr: 2, wRO: 3}

func (ti *tinfo) mark(off uintptr, k wordKind, elem reflect.Type) {
	w := off / wordSize
	if rank[ti.kinds[w]] > rank[k] {
		return
	}
	ti.kinds[w] = k
	ti.elems[w] = elem
}

func (ti *tinfo) walk(t reflect.Type, off uintptr) {
	switch t.Kind() {
	case reflect.Pointer:
		ti.mark(off, wPtr, t.Elem())
	case reflect.UnsafePointer:
		ti.mark(off, wPtr, nil)
	case reflect.Bool, reflect.String, reflect.Slice, reflect.Interface, reflect.Map, reflect.Chan, reflect.Func:
		for b := off; b < off+t.Size(); b += wordSize - b%wordSize {
			ti.mark(b, wRO, nil)
		}
	case reflect.Array:
		for i := 0; i < t.Len(); i++ {
			ti.walk(t.Elem(), off+uintptr(i)*t.Elem().Size())
		}
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.Tag.Get("c23") == "union" {
				for b := off + f.Offset; b < off+f.Offset+f.Type.Size(); b += wordSize - b%wordSize {
					ti.mark(b, wSplit, nil)
				}
				continue
			}
			ti.walk(f.Type, off+f.Offset)
		}
	}
}

// kindAt returns the kind of the byte at off in memory that is an array of
// ti's type.
func (ti *tinfo) kindAt(off uintptr) wordKind {
	return ti.kinds[ti.mod(off)/wordSize]
}

// mod returns off % ti.size, without dividing for the offsets memory has.
func (ti *tinfo) mod(off uintptr) uintptr {
	switch {
	case off < ti.size:
		return off
	case ti.mask != 0:
		return off & ti.mask
	case uint64(off) < 1<<32:
		hi, _ := bits.Mul64(ti.recip*uint64(off), uint64(ti.size))
		return uintptr(hi)
	}
	return off % ti.size
}

// elemAt returns what the pointer word at off points to: nil for
// unsafe.Pointer.
func (ti *tinfo) elemAt(off uintptr) *tinfo {
	i := ti.mod(off) / wordSize
	if e := ti.elemInfo[i].Load(); e != nil {
		return e
	}
	if e := ti.elems[i]; e != nil {
		ei := infoOf(e)
		ti.elemInfo[i].Store(ei)
		return ei
	}
	return nil
}

type subKey struct {
	have, want *tinfo
	off        uintptr
}

var subCache sync.Map // subKey -> bool

// holds reports whether memory that is an array of have, at off, holds a
// whole value of want: what a Go pointer to want may point at. A value
// with no pointer or read-only words may be any bytes with none either.
func holds(have *tinfo, off uintptr, want *tinfo) bool {
	off = have.mod(off)
	for i := range have.recentHolds {
		if l := have.recentHolds[i].Load(); l != nil && l.want == want && l.off == off {
			return l.r
		}
	}
	k := subKey{have, want, off}
	if r, ok := subCache.Load(k); ok {
		have.remember(&holdsAns{want, off, r.(bool)})
		return r.(bool)
	}
	var r bool
	if want.scalarOnly {
		r = true
		// Split words are bytes to Go: a Go pointer to scalars may point
		// into them.
		for b := off; b < off+want.size; b++ {
			if k := have.kindAt(b); k != wScalar && k != wSplit {
				r = false
				break
			}
		}
	} else {
		r = sub(have.t, off%have.size, want.t)
	}
	subCache.Store(k, r)
	have.remember(&holdsAns{want, off, r})
	return r
}

func (ti *tinfo) remember(a *holdsAns) {
	ti.recentHolds[ti.nextHolds.Add(1)%uint32(len(ti.recentHolds))].Store(a)
}

// sub reports whether t has a sub-object of type want at off.
func sub(t reflect.Type, off uintptr, want reflect.Type) bool {
	if off == 0 && t == want {
		return true
	}
	switch t.Kind() {
	case reflect.Array:
		e := t.Elem()
		if e.Size() == 0 || off >= t.Size() {
			return false
		}
		return sub(e, off%e.Size(), want)
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if off >= f.Offset && off < f.Offset+f.Type.Size() {
				return sub(f.Type, off-f.Offset, want)
			}
		}
	}
	return false
}

const arrayBuckets = 128

// arrayBucket returns the count n is rounded up to for an allocation of
// n values of size sz, and its bucket in tinfo.arrays: n itself up to 16;
// beyond, a count of eight steps in each power of two (at most 1/8 more,
// as Go's size classes round anyway). -1: not cached (over 32kB, where
// Go rounds to pages only).
func arrayBucket(n, sz uintptr) (uintptr, int) {
	if n <= 16 {
		return n, int(n)
	}
	if n*sz > 32<<10 || sz > 32<<10 {
		return n, -1
	}
	k := bits.Len64(uint64(n - 1)) // 2^(k-1) < n <= 2^k
	step := uintptr(1) << (k - 4)
	q := (n + step - 1) / step // 9..16
	i := 17 + (k-5)*8 + int(q-9)
	if i >= arrayBuckets {
		return n, -1
	}
	return q * step, i
}

// arrayObj returns the type of C's allocation of n values of ti's type, or
// a few more.
func (ti *tinfo) arrayObj(n uintptr) reflect.Type {
	n, i := arrayBucket(n, ti.t.Size())
	if i < 0 {
		return reflectArrayObj(ti.t, int(n))
	}
	tab := ti.arrays.Load()
	if tab == nil {
		ti.arrays.CompareAndSwap(nil, new([arrayBuckets]atomic.Pointer[arrayType]))
		tab = ti.arrays.Load()
	}
	if a := tab[i].Load(); a != nil {
		return a.t
	}
	t := reflectArrayObj(ti.t, int(n))
	tab[i].Store(&arrayType{t})
	return t
}

// reflectArrayObj returns the type of C's allocation of n values of t:
// a header, then the array.
func reflectArrayObj(t reflect.Type, n int) reflect.Type {
	return reflect.StructOf([]reflect.StructField{
		{Name: "H", Type: reflect.TypeFor[header]()},
		{Name: "V", Type: reflect.ArrayOf(n, t)},
	})
}

func newObj(t reflect.Type) unsafe.Pointer { return reflect.New(t).UnsafePointer() }
