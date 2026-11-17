// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"internal/abi"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"
)

//go:linkname stackBounds runtime.c23Stack
func stackBounds() (lo, hi uintptr)

//go:linkname gTableSlot runtime.c23GTable
func gTableSlot() *unsafe.Pointer

//go:linkname setStackMove runtime.c23SetStackMove
func setStackMove(f func(t unsafe.Pointer, lo, hi, delta uintptr))

func init() { setStackMove(stackMove) }

// A gTable holds the capabilities kept for words in one goroutine's stack.
// Only its goroutine uses it, and the runtime re-keys it while the
// goroutine is stopped, so it needs no lock.
type gTable struct {
	e []gEntry
}

type gEntry struct {
	addr uintptr
	auxEntry
}

// stackMove re-keys t for a stack moved by delta. It runs on the system
// stack, so it may not allocate or write pointers.
//
//go:nosplit
func stackMove(t unsafe.Pointer, lo, hi, delta uintptr) {
	tb := (*gTable)(t)
	for i := range tb.e {
		if a := tb.e[i].addr; a >= lo && a < hi {
			tb.e[i].addr = a + delta
		}
	}
}

// goTables holds the tables of Go's heap objects, by base address (0 for
// globals, by address).
var (
	goTables sync.Map // uintptr -> *auxTable
	goTableN atomic.Int64
)

// where finds the table of the word at a in p's memory and its key.
// kind: 'c' C's object, 's' this goroutine's stack, 'h' Go's heap (obj is
// the object) or globals (obj is nil, and the key the address).
func where(p Ptr, a unsafe.Pointer) (kind byte, obj unsafe.Pointer, key uintptr) {
	if p.header() != nil {
		return 'c', nil, uintptr(p.off)
	}
	if lo, hi := stackBounds(); uintptr(a) >= lo && uintptr(a) < hi {
		return 's', nil, uintptr(a)
	}
	obj, _ = object(a)
	return 'h', obj, uintptr(a) - uintptr(obj)
}

// auxLoad returns the capability kept for the word at a, if the word still
// holds w (or whatever it holds, if !exact: a split word).
func auxLoad(p Ptr, a unsafe.Pointer, w uintptr, exact bool) (Ptr, bool) {
	kind, obj, key := where(p, a)
	base := uintptr(obj)
	var e auxEntry
	var ok bool
	switch kind {
	case 'c':
		t := p.header().table(false)
		if t == nil {
			return Ptr{}, false
		}
		t.mu.Lock()
		e, ok = t.m[key]
		t.mu.Unlock()
	case 's':
		tb := (*gTable)(*gTableSlot())
		if tb == nil {
			return Ptr{}, false
		}
		for i := range tb.e {
			if tb.e[i].addr == key {
				e, ok = tb.e[i].auxEntry, true
				break
			}
		}
	case 'h':
		if goTableN.Load() == 0 {
			return Ptr{}, false
		}
		v, found := goTables.Load(base)
		if !found {
			return Ptr{}, false
		}
		t := v.(*auxTable)
		t.mu.Lock()
		e, ok = t.m[key]
		t.mu.Unlock()
	}
	if !ok || exact && e.word != w {
		return Ptr{}, false
	}
	return e.p, true
}

// auxStore keeps v for the word at a, which is being set to w, if keep;
// otherwise it forgets what was kept for it.
func auxStore(p Ptr, a unsafe.Pointer, keep bool, w uintptr, v Ptr) {
	kind, obj, key := where(p, a)
	base := uintptr(obj)
	switch kind {
	case 'c':
		if t := p.header().table(keep); t != nil {
			t.set(key, keep, auxEntry{w, v})
		}
	case 's':
		slot := gTableSlot()
		tb := (*gTable)(*slot)
		if tb == nil {
			if !keep {
				return
			}
			tb = new(gTable)
			*slot = unsafe.Pointer(tb)
		}
		// Entries below this frame are in frames that have returned.
		sp := uintptr(abi.NoEscape(unsafe.Pointer(&kind)))
		j := 0
		for _, e := range tb.e {
			if e.addr >= sp && e.addr != key {
				tb.e[j] = e
				j++
			}
		}
		clear(tb.e[j:])
		tb.e = tb.e[:j]
		if keep {
			tb.e = append(tb.e, gEntry{key, auxEntry{w, v}})
		}
	case 'h':
		var t *auxTable
		if v, ok := goTables.Load(base); ok {
			t = v.(*auxTable)
		} else if keep {
			nt := &auxTable{m: map[uintptr]auxEntry{}}
			v, loaded := goTables.LoadOrStore(base, nt)
			t = v.(*auxTable)
			if !loaded {
				goTableN.Add(1)
				if base != 0 {
					runtime.AddCleanup((*byte)(obj), func(b uintptr) {
						goTables.Delete(b)
						goTableN.Add(-1)
					}, base)
				}
			}
		}
		if t != nil {
			t.set(key, keep, auxEntry{w, v})
		}
	}
}

func (t *auxTable) set(key uintptr, keep bool, e auxEntry) {
	t.mu.Lock()
	if keep {
		t.m[key] = e
	} else {
		delete(t.m, key)
	}
	t.mu.Unlock()
}
