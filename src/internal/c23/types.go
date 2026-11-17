// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
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
	kinds      []wordKind     // per 8-byte word of one value
	elems      []reflect.Type // per word: the pointee of a wPtr word (nil: unsafe.Pointer)
	scalarOnly bool
}

var tinfos sync.Map // reflect.Type -> *tinfo

// tinfoTab holds every tinfo by id, copied when one is added (types are
// few), so that reading it needs no lock.
var (
	tinfoTab atomic.Pointer[[]*tinfo]
	tinfoMu  sync.Mutex
)

func init() { tinfoTab.Store(new([]*tinfo)) }

// registered holds every tinfo, so that a header found in memory can be
// trusted: safe Go code cannot make a *tinfo, so it cannot forge one.
var registered sync.Map // *tinfo -> struct{}

func infoOf(t reflect.Type) *tinfo {
	if ti, ok := tinfos.Load(t); ok {
		return ti.(*tinfo)
	}
	size := t.Size()
	n := (size + 7) / 8
	ti := &tinfo{t: t, size: max(size, 1), kinds: make([]wordKind, max(n, 1)), elems: make([]reflect.Type, max(n, 1))}
	ti.walk(t, 0)
	ti.scalarOnly = true
	for _, k := range ti.kinds {
		if k != wScalar {
			ti.scalarOnly = false
		}
	}
	tinfoMu.Lock()
	defer tinfoMu.Unlock()
	if old, ok := tinfos.Load(t); ok {
		return old.(*tinfo)
	}
	tab := append(append([]*tinfo(nil), *tinfoTab.Load()...), ti)
	ti.id = len(tab) - 1
	tinfoTab.Store(&tab)
	tinfos.Store(t, ti)
	registered.Store(ti, struct{}{})
	return ti
}

func tinfoOf[T any]() *tinfo { return infoOf(reflect.TypeFor[T]()) }

// rank orders the kinds a word may be given more than once: the most
// restrictive wins.
var rank = [...]int{wScalar: 0, wSplit: 1, wPtr: 2, wRO: 3}

func (ti *tinfo) mark(off uintptr, k wordKind, elem reflect.Type) {
	w := off / 8
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
		for b := off; b < off+t.Size(); b += 8 - b%8 {
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
				for b := off + f.Offset; b < off+f.Offset+f.Type.Size(); b += 8 - b%8 {
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
func (ti *tinfo) kindAt(off uintptr) wordKind { return ti.kinds[off%ti.size/8] }

// elemAt returns what the pointer word at off points to: nil for
// unsafe.Pointer.
func (ti *tinfo) elemAt(off uintptr) *tinfo {
	if e := ti.elems[off%ti.size/8]; e != nil {
		return infoOf(e)
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
	k := subKey{have, want, off % have.size}
	if r, ok := subCache.Load(k); ok {
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
	return r
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

// reflectArrayObj returns the type of C's allocation of n values of t:
// a header, then the array.
func reflectArrayObj(t reflect.Type, n int) reflect.Type {
	return reflect.StructOf([]reflect.StructField{
		{Name: "H", Type: reflect.TypeFor[header]()},
		{Name: "V", Type: reflect.ArrayOf(n, t)},
	})
}

func newObj(t reflect.Type) unsafe.Pointer { return reflect.New(t).UnsafePointer() }
