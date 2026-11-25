// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"internal/goarch"
	"internal/runtime/atomic"
	"internal/runtime/sys"
	"unsafe"
)

// The repair of the pointers into memory of C's that is claimed again
// (cclaim.go, doc/gd/cc.md).
//
// A claim says what a word of malloc's memory is, and the code that has
// a pointer of a type to it loads and stores through that pointer with
// nothing checked: the conversion that made the pointer was. So when a
// conversion claims words for something that they were not, as an
// allocator of C's does that hands out a block again, every pointer that
// was converted before is wrong for them: one of them would store an
// integer where a pointer is now, or load a pointer of another type.
//
// Those pointers are taken away. The world stops, and every word of the
// program that holds a pointer of a type into the object is looked at:
// the conversion that made it is asked about again (cConvProbeType),
// with the claims as they are now, and a pointer that it would refuse is
// made one that nothing is loaded or stored through (cEnd): it is a
// pointer still, which the program may add to and convert, to what is
// there now. A pointer to anything stays:
// a void *, a char * of C's, an unsafe.Pointer, an intptr_t that is a
// pointer, which are converted before they are used. So does a pointer
// to a part of the object that was not claimed again.
//
// The words are found by their types, since a pointer to anything is to
// stay:
//
//   - in an object of C's, by the claims of its words;
//   - in an object of Go's, by the type that it records, and one that
//     records none loses its pointers into the object;
//   - in the program's data, by the table of its variables (datavar.go);
//   - in the frames of every goroutine, by the tables of their variables
//     (stackvar.go). A goroutine that was stopped where the compiler has
//     no map has pointers in registers that have no type: what was saved
//     of them is looked at as words, and one that is an address in the
//     object is taken away too.
//
// This costs a look at all of the heap for each conversion that claims
// memory again. A program that does it often runs slowly, and runs.

// cRepairs counts the times that the world was stopped for it.
var cRepairs atomic.Uint64

// cCanRepair reports whether the goroutine that is running may stop the
// world: it is a goroutine of the program's, and holds nothing of the
// runtime's.
func cCanRepair() bool {
	gp := getg()
	mp := gp.m
	return gp == mp.curg && mp.locks == 0 && mp.preemptoff == "" && mp.mallocing == 0 && !mp.cprobe
}

// cRepair takes away the pointers of a type into the object of C's of
// size bytes at base that the claims of its words no longer let be, as
// they are now.
func cRepair(base, size uintptr) {
	gp := getg()
	pc, sp := sys.GetCallerPC(), sys.GetCallerSP()
	stw := stopTheWorldGC(stwCRepair)
	systemstack(func() {
		cRepairWorld(gp, pc, sp, base, base+size)
	})
	cRepairs.Add(1)
	startTheWorldGC(stw)
}

// cRepairIn reports whether v is a pointer into the object from lo to
// hi, its end with it, that may be used.
//
//go:nosplit
func cRepairIn(v, lo, hi uintptr) bool {
	if v&(cPoison|cEnd) != 0 {
		return false
	}
	v &^= cPtrBits
	return lo <= v && v <= hi
}

// cRepairWord looks at the word at a, which holds a pointer to elem:
// if that is one into the object and elem is no longer what the memory
// is there, the word holds a pointer that nothing is loaded through from
// here on.
// If c is set the pointer is C's.
func cRepairWord(a, lo, hi uintptr, elem *_type, c bool) {
	v := *(*uintptr)(unsafe.Pointer(a))
	if !cRepairIn(v, lo, hi) || elem.Size_ == 0 {
		return
	}
	if nv := cConvProbeElem(v, elem, c); nv != v {
		*(*uintptr)(unsafe.Pointer(a)) = nv
	}
}

// cRepairDrop makes the word at a, if it holds a pointer into the
// object, hold one that nothing is loaded through: nothing says what it
// is a pointer to.
func cRepairDrop(a, lo, hi uintptr) {
	if v := *(*uintptr)(unsafe.Pointer(a)); cRepairIn(v, lo, hi) && v&cIntTag == 0 {
		*(*uintptr)(unsafe.Pointer(a)) = v&^cPtrBits | cEnd
	}
}

// cRepairLeaf looks at the word at a, which is at offset off of a value
// of type typ. If c is set the value is C's: a pointer to a byte in it
// is a pointer to anything.
func cRepairLeaf(a, lo, hi uintptr, typ *_type, off uintptr, c bool) {
	if !cRepairIn(*(*uintptr)(unsafe.Pointer(a)), lo, hi) {
		return
	}
	switch k, leaf, in := compatLeaf(typ, off); k {
	case compatPtr:
		if c && compatBytePtr(leaf) {
			return
		}
		cRepairWord(a, lo, hi, (*ptrtype)(unsafe.Pointer(leaf)).Elem, c)
	case compatSlice:
		if in == 0 {
			cRepairWord(a, lo, hi, (*slicetype)(unsafe.Pointer(leaf)).Elem, false)
		}
	case compatString:
		// The bytes of a string of Go's are no part of a pointer.
		if in == 0 && cByteOver(*(*uintptr)(unsafe.Pointer(a))&^cPtrBits, 1) {
			cRepairDrop(a, lo, hi)
		}
	case compatUnsafe, compatCap, compatScalar:
		// A pointer to anything, which is converted before it is
		// used.
	default:
		// A func, a map, a chan, what an interface holds: none of
		// them is in an object of C's.
		cRepairDrop(a, lo, hi)
	}
}

// cRepairTyped looks at the pointers of the value of type typ at base.
func cRepairTyped(base, lo, hi uintptr, typ *_type, c bool) {
	const word = goarch.PtrSize
	if typ == nil || !typ.Pointers() {
		return
	}
	mask := getGCMask(typ)
	for off := uintptr(0); off < typ.PtrBytes; off += word {
		if compatMaskBit(typ, mask, off) {
			cRepairLeaf(base+off, lo, hi, typ, off, c)
		}
	}
}

// cRepairWorld is cRepair with the world stopped, on the system stack:
// cur is the goroutine that asked, which goes on at pc and sp.
//
//go:systemstack
func cRepairWorld(cur *g, pc, sp, lo, hi uintptr) {
	const word = goarch.PtrSize

	// The heap.
	for _, s := range mheap_.allspans {
		if s.state.get() != mSpanInUse || s.isUserArenaChunk {
			continue
		}
		if s.spanclass.c() {
			b, ids := s.cBitsOf(), s.cIDsOf()
			if b == nil || ids == nil {
				// No word of it is a pointer of a type.
				continue
			}
			for i := uintptr(0); i < uintptr(s.nelems); i++ {
				if s.isFree(i) {
					continue
				}
				a := s.base() + i*s.elemsize
				w, end := (a-s.base())/word, (a-s.base()+alignUp(s.cSizeOf(i), word))/word
				for ; w < end; w++ {
					if cClass(b, w) != cTyped {
						continue
					}
					if id := atomic.Load(ids.word(w)); id != 0 && id < cGoID {
						t := cTypeOf(id)
						if t.Kind() != abi.Pointer {
							continue
						}
						// (A word is only claimed for a
						// pointer to a byte by Go.)
						cRepairWord(s.base()+w*word, lo, hi, (*ptrtype)(unsafe.Pointer(t)).Elem, !compatBytePtr(t))
					}
				}
			}
			continue
		}
		if s.spanclass.noscan() {
			continue
		}
		for i := uintptr(0); i < uintptr(s.nelems); i++ {
			if s.isFree(i) {
				continue
			}
			base := s.base() + i*s.elemsize
			limit := base + s.elemsize
			typ, tbase := checkptrObjectType(s, base)
			c := cObjectIsC(typ)
			tp := s.typePointersOfUnchecked(base)
			for {
				var a uintptr
				if tp, a = tp.next(limit); a == 0 {
					break
				}
				if typ == nil || a < tbase {
					cRepairDrop(a, lo, hi)
					continue
				}
				cRepairLeaf(a, lo, hi, typ, (a-tbase)%typ.Size_, c)
			}
		}
	}

	// The program's data.
	for datap := &firstmoduledata; datap != nil; datap = datap.next {
		if datap.datavars == 0 || datap.bad {
			continue
		}
		starts := (*[dataVarSects + 1]uint32)(unsafe.Pointer(datap.datavars))
		entries := datap.datavars + unsafe.Sizeof(*starts) + unsafe.Sizeof([dataVarSects]uint32{})
		sects := [dataVarSects]uintptr{
			dataVarRodata:    0, // (nothing is stored there)
			dataVarRelro:     0,
			dataVarNoptrdata: 0,
			dataVarData:      datap.data,
			dataVarBss:       datap.bss,
			dataVarNoptrbss:  0,
		}
		for i, start := range sects {
			if start == 0 {
				continue
			}
			for j := uintptr(starts[i]); j < uintptr(starts[i+1]); j++ {
				if e := dataVarEntryAt(entries, j); e.x&dataVarNoType == 0 {
					cRepairTyped(start+uintptr(e.off), lo, hi, (*_type)(unsafe.Pointer(datap.types+uintptr(e.x))), true)
				}
			}
		}
	}

	// The stacks.
	forEachGRace(func(gp *g) {
		if st := readgstatus(gp) &^ _Gscan; st == _Gdead || st == _Gidle || gp.stack.lo == 0 {
			return
		}
		var u unwinder
		if gp == cur {
			u.initAt(pc, sp, 0, gp, unwindSilentErrors)
		} else {
			u.init(gp, unwindSilentErrors)
		}
		for ; u.valid(); u.next() {
			frame := &u.frame
			if frame.fn.funcID == abi.FuncID_asyncPreempt || frame.fn.funcID == abi.FuncID_debugCallV2 {
				// What was in registers, which has no type.
				for a := alignUp(frame.sp, word); a+word <= frame.fp; a += word {
					cRepairDrop(a, lo, hi)
				}
				continue
			}
			p := funcdata(frame.fn, abi.FUNCDATA_StackVars)
			if p == nil {
				continue
			}
			recs := unsafe.Slice((*stackVarRecord)(add(p, 4)), int(*(*uint32)(p)))
			for k := range recs {
				r := &recs[k]
				ref := frame.varp
				if r.off >= 0 {
					ref = frame.argp
				}
				if ref == 0 {
					continue
				}
				cRepairTyped(uintptr(int64(ref)+int64(r.off)), lo, hi, (*_type)(unsafe.Pointer(frame.fn.datap.types+uintptr(r.typ))), true)
			}
		}
	})
}
