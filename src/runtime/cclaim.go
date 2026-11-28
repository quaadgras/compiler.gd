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

// Claims: what the words of C's memory are (doc/gd/cc.md).
//
// What malloc gives has no type, and the code that uses it is typed: it
// loads and stores through pointers that were converted to the types it
// has for them, and nothing checks a load or a store. So it is the
// conversion that says what the memory is. The first conversion of a
// pointer to *T over a word claims the word for what T has there: no
// pointer, a pointer to anything (unsafe.Pointer, C's void *), or a
// pointer of a type. A claim stays. A later conversion over the word is
// compared with it as a conversion is compared with the type of a Go
// object (checkptr_compat.go): the same, or compatible, and nothing
// changes. Bytes (a type of alignment 1) claim nothing and may be over
// anything.
//
// malloc that is told the type of what it allocates (c23.AllocOf)
// claims the object's words for it there and then, so the conversions
// that follow find what they expect.
//
// A conversion that is not compatible with the claim claims the word
// again: the memory is used for something else, as an allocator of C's
// does with the blocks it keeps. That is what makes the pointers that
// were converted before wrong, and it is counted (cClaimStats): the
// plan is to stop the world there and take those pointers away, which
// is not done yet. Until then a word that is claimed again keeps what
// it holds, where it will hold a pointer that the program cannot make
// (cPoison) if it is claimed for a pointer and held something that was
// no pointer.
//
// The collector skips a word that is claimed for no pointer, and looks
// at the others as it looks at a word it has no type for (scanObjectC).
// That it still looks at a word that nothing claimed is for now: the
// plan is that such a word holds bytes and is skipped, once every way
// that a pointer gets into C's memory claims the word for it.

// The bits of a word of C's that no address has, and that say what the
// word is to code that would otherwise have to look at the memory
// (doc/gd/cc.md):
//
//   - cPtrTag, of a pointer with no type (void *, char *): it is to a
//     union with pointers, or to the value of one ("Unions");
//   - cIntTag, of a uintptr_t or an intptr_t: it was made of a pointer,
//     and is one still;
//   - cEnd, of a pointer to the end of its object, one past the last
//     byte: C makes and keeps that pointer, compares with it and steps
//     back from it, and may not load or store through it;
//   - cPoison, of a pointer that the program was not given: an integer
//     or bytes that it made a pointer of.
//
// The first two are in the bits that an arm64 does not look at when it
// loads through a pointer, and compiled code and internal/c23 take them
// off before they use the address. The other two are where no machine
// has an address, so that a load or a store through such a pointer
// faults, with nothing checked: the compiled code takes cEnd off what it
// adds to, and puts it on a result that is the end again
// (checkptrArithmeticC). A word with any of the first three is one that
// the collector and the stack's copier look at as at any pointer, so they
// take the bits off too (cPtrBits): the pointer to an object's end keeps
// the object alive, as C expects of it.
const (
	cPtrTag  = (1 << 62) * (goarch.PtrSize / 8)
	cIntTag  = (1 << 63) * (goarch.PtrSize / 8)
	cEnd     = (1 << 54) * (goarch.PtrSize / 8)
	cPoison  = (1 << 55) * (goarch.PtrSize / 8)
	cPtrBits = cPtrTag | cIntTag | cEnd
)

// cClaimStats counts what the claims of C's memory found, for
// GODEBUG=cclaims=1, which prints them when the program exits.
var cClaimStats struct {
	claims   atomic.Uint64 // words claimed for the first time
	reclaims atomic.Uint64 // words claimed again for something that is not compatible
	narrowed atomic.Uint64 // words that held a pointer to anything, claimed for a typed pointer
	forged   atomic.Uint64 // words claimed for a pointer that held something else than a pointer, and not 0
	bytes    atomic.Uint64 // words claimed for a pointer that bytes were written in
}

// cClaimCounting is set if the first claims that are made at once
// (cClaimScalars, cClaimFresh) are counted too, as they are with
// GODEBUG=cclaims: a count is an atomic addition for each, which is as
// much as the claim. The tests that read the counts set it.
var cClaimCounting bool

// cClaimReport prints cClaimStats.
func cClaimReport() {
	if debug.cclaims == 0 || !cSpansUsed {
		return
	}
	st := &cClaimStats
	print("c claims: ", st.claims.Load(), " words claimed, ", st.reclaims.Load(), " claimed again, ",
		st.narrowed.Load(), " untyped pointers typed, ", st.forged.Load(), " pointers of what was none, ",
		st.bytes.Load(), " pointers written over with bytes, ", cIntPtrs.Load(), " integers that were addresses made pointers, ",
		cRepairs.Load(), " times the world was stopped for it\n")
}

// cTypeID returns the number that stands for the type t in the ids of a
// span, or 0 if it has none: a type that is not in the program's own
// module, which a pointer of C's is not of.
//
//go:nosplit
func cTypeID(t *_type) uint32 {
	md := &firstmoduledata
	if a := uintptr(unsafe.Pointer(t)); md.types <= a && a < md.etypes {
		return uint32(a-md.types) + 1
	}
	return 0
}

// cTypeOf returns the type that id stands for.
//
//go:nosplit
func cTypeOf(id uint32) *_type {
	return (*_type)(unsafe.Pointer(firstmoduledata.types + uintptr(id-1)))
}

// cGoID is the bit of the id of a word that is part of a string, a
// slice or an interface of Go's, or of a struct that is one whole thing
// to the checks (reflect.Value): the type is in the low 28 bits and the
// number of the word in the two above them. Such a word is claimed as a
// typed pointer is, whether it holds one or a length, and nothing but
// the same thing may be over it: an integer that C stored where Go has
// a length would be an index that Go trusts.
const (
	cGoID      = 0x40000000
	cGoIDShift = 28
)

// cWant returns the class, and the id if it is cTyped, that a word is
// claimed for by a type whose leaf there is (kind, leaf, in):
// compatLeaf's. If c is set the conversion is C's, to which a pointer to
// a byte is a pointer to anything (compatLeaves).
func cWant(kind uint8, leaf *_type, in uintptr, c bool) (uint32, uint32) {
	switch kind {
	case compatScalar:
		return cScalar, 0
	case compatPtr:
		if c && compatBytePtr(leaf) {
			return cUntyped, 0
		}
		fallthrough
	case compatFunc, compatMap, compatChan:
		if id := cTypeID(leaf); id != 0 && id < cGoID {
			return cTyped, id
		}
	case compatString, compatSlice, compatIface, compatStruct, compatCap:
		if id := cTypeID(leaf); id != 0 && id < 1<<cGoIDShift && in/goarch.PtrSize < 4 {
			return cTyped, cGoID | uint32(in/goarch.PtrSize)<<cGoIDShift | id
		}
		throw("checkptr: a type of Go's that C's memory cannot hold")
	}
	// An unsafe.Pointer, or a pointer of a type that the program made
	// as it ran: only that it is a pointer is kept.
	return cUntyped, 0
}

// cLeafOf returns what the id of a word that is cTyped stands for:
// compatLeaf's.
func cLeafOf(id uint32) (kind uint8, leaf *_type, in uintptr) {
	if id&cGoID != 0 {
		leaf = cTypeOf(id & (1<<cGoIDShift - 1))
		return compatLeaf(leaf, uintptr(id>>cGoIDShift&3)*goarch.PtrSize)
	}
	leaf = cTypeOf(id)
	return compatLeaf(leaf, 0)
}

// cShadeWord shows the collector v, which a word of C's that it looks
// at held or will hold, if it is marking.
//
//go:nosplit
func cShadeWord(v uintptr) {
	if writeBarrier.enabled && v != 0 {
		p := getg().m.p.ptr().wbBuf.get1()
		p[0] = v
	}
}

// cShade is the collector's barrier for the n words of C's from the w'th
// of a span, at dst, that it looks at, if it is marking: it sees what
// they hold, and what the words at src do, which are stored there next,
// if src is not 0.
//
//go:nosplit
func cShade(b *cBits, w, dst, src, n uintptr) {
	if !writeBarrier.enabled {
		return
	}
	buf := &getg().m.p.ptr().wbBuf
	for i := uintptr(0); i < n; i++ {
		if cClass(b, w+i) < cUntyped {
			continue
		}
		p := buf.get1()
		p[0] = *(*uintptr)(unsafe.Pointer(dst + i*goarch.PtrSize))
		if src != 0 {
			p := buf.get1()
			p[0] = *(*uintptr)(unsafe.Pointer(src + i*goarch.PtrSize))
		}
	}
}

// cClaim is for the conversion (*[n]elem)(p), where p points into the
// i'th object of s, a span of C's objects: the words of the object that
// the type is over are claimed for what it has there, if nothing
// claimed them, and are compared with it otherwise. If load is set the
// result is only loaded through, and may have integers where the memory
// has pointers. If c is set the conversion is C's (cWant).
func cClaim(s *mspan, i, p uintptr, elem *_type, n uintptr, load, c bool) {
	if !elem.Pointers() && elem.Align_ == 1 {
		return // bytes
	}
	b := s.cBitsOf()
	size := n * elem.Size_
	if b == nil || size == 0 {
		return
	}
	const word = goarch.PtrSize
	sbase := s.base()
	end := sbase + i*s.elemsize + s.cSizeOf(i)
	if p >= end || s.isFree(i) {
		// A pointer to the object's end, which nothing is loaded
		// or stored through, or one kept from before.
		return
	}
	lo, hi := alignDown(p, word), min(alignUp(p+size, word), alignUp(end, word))
	if !elem.Pointers() {
		// The usual case: integers, in words that are claimed for
		// them or that nothing claimed.
		w, k := (lo-sbase)/word, (hi-lo)/word
		if k == 1 {
			if cClass(b, w) == cScalar {
				return
			}
		} else if k <= cClassPer {
			if mask := cClassesOf(k); cClassWindow(b, w)&mask == cClassLows*cScalar&mask {
				return
			}
		}
		if load {
			// What is loaded as an integer, whatever it is: a load
			// makes nothing of the words.
			return
		}
		if k <= 2*cClassPer && cClaimScalars(b, w, k) {
			return
		}
	}
	aligned := p&(word-1) == 0 && elem.Pointers()
	var mask *byte
	var plan *cPlan
	if aligned {
		// (Not for a type that does not fit: only the words in the
		// object are looked at then.)
		if p+size <= end {
			plan = cPlanOf(elem, c)
		}
		if plan == nil {
			mask = getGCMask(elem)
		}
	}
	if plan != nil && n == 1 && !load && cClaimFresh(s, b, (lo-sbase)/word, lo, plan) {
		return
	}
	// direct: what is stored through the pointer is looked at where
	// it is stored (c23PtrStore), or nothing is, so it may be over a
	// pointer of a type.
	direct := load || cDirect(elem, c)
	repair := false // words were claimed again: see cReclaim
	for a := lo; a < hi; a += word {
		w := (a - sbase) / word
		want, id := uint32(cScalar), uint32(0)
		if plan != nil {
			e := &plan.words[(a-p)%elem.Size_/word]
			want, id = e.want, e.id
			if e.union {
				ids := s.cIDsOf()
				if ids == nil {
					ids = s.newCIDs()
				}
				if atomic.Load(ids.word(w)) != id {
					atomic.Store(ids.word(w), id)
				}
			}
		} else if aligned {
			off := (a - p) % elem.Size_
			if k, leaf, in := compatLeaf(elem, off); k != compatScalar && (compatMaskBit(elem, mask, off) || k >= compatString) {
				want, id = cWant(k, leaf, in, c)
			}
			if c, uid := cUnionID(elem, off); uid != 0 {
				// A union's first word, or its pointer to its
				// value: the id says so, whatever claimed the
				// word before (cunion.go).
				want, id = c, uid
				ids := s.cIDsOf()
				if ids == nil {
					ids = s.newCIDs()
				}
				if atomic.Load(ids.word(w)) != uid {
					atomic.Store(ids.word(w), uid)
				}
			}
		}
		have := cClass(b, w)
		var old uint32
		if have == cTyped {
			if ids := s.cIDsOf(); ids != nil {
				old = atomic.Load(ids.word(w))
			}
		}
		switch {
		case have == want && want != cTyped:
			continue
		case have == cTyped && want == cTyped:
			if old == id || cCompatIDs(old, id, c) {
				continue
			}
		case want == cScalar && load:
			// What is loaded as an integer, whatever it is: a
			// load makes nothing of the word.
			continue
		case have == cUnclaimed:
			if mp := getg().m; mp.cprobe && mp.cprobeStrict {
				mp.cprobeFail = true
				continue
			}
			cClaimStats.claims.Add(1)
			cClaimWord(s, b, w, a, have, want, id)
			continue
		case want == cUntyped && have == cTyped:
			// An unsafe.Pointer over a pointer of a type: what
			// is stored through it is checked where it is
			// stored. (Not over a word of a string of Go's, and
			// not as a member of a struct, whose stores are not
			// looked at.)
			if direct && old != 0 && old < cGoID {
				continue
			}
		case want == cTyped && have == cUntyped:
			// A pointer of a type over a pointer to anything:
			// what the word holds is converted to it, as the
			// program would have to.
			if id < cGoID {
				if getg().m.cprobe {
					if !cNarrowOK(a, id) {
						getg().m.cprobeFail = true
					}
					continue
				}
				cClaimStats.narrowed.Add(1)
				cNarrow(a, id)
				cClaimWord(s, b, w, a, have, want, id)
				continue
			}
			if id >= cUnionStartID || id == cCapID {
				// An intptr_t over a pointer to anything, which
				// is an integer to it (cClaimWord). Or a
				// union's pointer to its value, in a word that
				// was copied or looked at as a pointer to
				// anything before the union was: what it holds is
				// looked at each time the union is (c23UnionBox).
				cClaimWord(s, b, w, a, have, want, id)
				continue
			}
		}
		if cReclaim(s, b, w, a, have, want, old, id, elem) {
			repair = true
		}
	}
	if repair {
		cRepair(sbase+i*s.elemsize, s.cSizeOf(i))
	}
}

// cCompatIDs reports whether a word that id stands for may be over one
// that old stands for: checkptr_compat.go's rule for the words of a
// view.
func cCompatIDs(old, id uint32, c bool) bool {
	if old == 0 || old >= cUnionStartID || id >= cUnionStartID {
		// (No type, or what says where a union is: cunion.go.)
		return false
	}
	mk, mt, min := cLeafOf(old)
	vk, vt, vin := cLeafOf(id)
	var seen compatSeen
	// (A word is only claimed for a pointer to a byte by Go.)
	seen.c = c && !(vk == compatPtr && compatBytePtr(vt))
	return compatLeaves(mk, mt, min, vk, vt, vin, true, &seen) == compatOK
}

// cProbeMax is how deep a conversion that is only asked about asks
// about what the memory points to.
const cProbeMax = 256

// cConvProbe returns what the pointer v, which a word of C's holds, is
// as a pointer of the type that id stands for: v, or v with cEnd, as
// checkptrConvC gives it, also where that conversion would stop the
// program: a pointer that nothing is loaded through. It claims what the
// conversion would claim for the first time, and nothing again.
func cConvProbe(v uintptr, id uint32) uintptr {
	// (A word is only claimed for a pointer to a byte by Go, to
	// which it is to a byte that is no part of a pointer.)
	t := cTypeOf(id)
	return cConvProbeType(v, t, !(t.Kind() == abi.Pointer && compatBytePtr(t)))
}

// cConvProbeType is cConvProbe for the pointer type t. If c is not set
// the conversion is asked about as one of Go's: a pointer to a byte is
// then to a byte that is no part of a pointer.
func cConvProbeType(v uintptr, t *_type, c bool) uintptr {
	if v < minLegalPointer || v&(cPoison|cEnd) != 0 {
		return v
	}
	if t.Kind() != abi.Pointer {
		// A func, a map or a chan that was kept as a pointer to
		// anything: nothing says that it is one.
		return v | cPoison
	}
	return cConvProbeElem(v, (*ptrtype)(unsafe.Pointer(t)).Elem, c)
}

// c23ConvSoft is the conversion of p to a pointer to elem that C wrote
// as a cast of what follows an object, (U *)(t + 1) for a t that points
// to a struct: the address after it, which the program may never use
// (the arguments after a message that has none). It claims what nothing
// claimed, as any conversion does, and takes nothing from what the
// memory holds: if that is not what elem says, the pointer is one that
// nothing is loaded through (cEnd), which may still be converted to
// what is there.
//
//go:linkname c23ConvSoft
func c23ConvSoft(p unsafe.Pointer, elem *_type) unsafe.Pointer {
	return unsafe.Pointer(cConvProbeElem(uintptr(p), elem, true))
}

// cConvProbeElem is cConvProbeType for a pointer to elem.
func cConvProbeElem(v uintptr, elem *_type, c bool) uintptr {
	if v < minLegalPointer || v&(cPoison|cEnd) != 0 || elem.Size_ == 0 {
		return v
	}
	mp := acquirem()
	was, wasFail := mp.cprobe, mp.cprobeFail
	if mp.cprobeDepth >= cProbeMax {
		releasem(mp)
		return v&^cPtrBits | cEnd
	}
	mp.cprobeDepth++
	mp.cprobe, mp.cprobeFail = true, false
	r := v
	switch {
	case c:
		r = uintptr(checkptrConvC1(unsafe.Pointer(v), elem, false))
	case !elem.Pointers() && elem.Align_ == 1:
		if cByteOver(v&^cPtrBits, elem.Size_) {
			mp.cprobeFail = true
		}
	default:
		checkptrAlign(unsafe.Pointer(v&^cPtrBits), elem, 1, false, false)
	}
	fail := mp.cprobeFail
	mp.cprobe, mp.cprobeFail = was, wasFail
	mp.cprobeDepth--
	releasem(mp)
	if fail {
		// A pointer that the program was given, to what is not
		// what the type says: nothing is loaded through it, and it
		// is a pointer still, which may be converted to what is
		// there (cEnd).
		return v&^cPtrBits | cEnd
	}
	return r
}

// cNarrowOK reports whether the word at a, which holds a pointer to
// anything, holds what a pointer of the type that id stands for may be.
func cNarrowOK(a uintptr, id uint32) bool {
	v := atomic.Loaduintptr((*uintptr)(unsafe.Pointer(a)))
	return cConvProbe(v, id)&(cPoison|cEnd) == 0 || v&(cPoison|cEnd) != 0
}

// cNarrow makes of the pointer to anything that the word at a holds the
// pointer of the type that id stands for, which the word is claimed for
// next: the same pointer, if what it points to is what the type says or
// can be claimed for it, and otherwise one that nothing is loaded
// through.
func cNarrow(a uintptr, id uint32) {
	p := (*uintptr)(unsafe.Pointer(a))
	for {
		v := atomic.Loaduintptr(p)
		nv := cConvProbe(v, id)
		if nv == v || atomic.Casuintptr(p, v, nv) {
			return
		}
	}
}

// cClaimScalars claims for no pointer the k words from the w'th, if
// each of them is claimed so or by nothing, and reports whether it did.
// That is the first conversion of what malloc gave, the usual one. It
// is what cClaimWord does for each word, the words of a uint64 of b at
// once, and only while the collector is not marking, when what the
// words hold is nothing that it is to be shown. (Words that it claimed
// before it met one that is a pointer's stay claimed: they were no
// pointer's.)
//
//go:nosplit
func cClaimScalars(b *cBits, w, k uintptr) bool {
	if writeBarrier.enabled || getg().m.cprobe {
		return false
	}
	for k > 0 {
		c := min(k, cClassPer-w%cClassPer)
		p := b.group(w / cClassPer)
		mask := cClassesOf(c) << (w % cClassPer * 8)
		want := uint64(cClassLows*cScalar) & mask
		for {
			old := atomic.Load64(p)
			if old&mask&cClassHighs != 0 {
				// A word that is claimed for a pointer.
				return false
			}
			if old&mask == want {
				break
			}
			if atomic.Cas64(p, old, old|want) {
				if cClaimCounting || debug.cclaims != 0 {
					// Each word that nothing claimed is a claim.
					cClaimStats.claims.Add(int64(c) - int64(sys.OnesCount64(old&mask)))
				}
				break
			}
		}
		w += c
		k -= c
	}
	return true
}

// cClaimWord claims the w'th word of s, at a, whose class is have, for
// want, and id if that is cTyped.
func cClaimWord(s *mspan, b *cBits, w, a uintptr, have, want, id uint32) {
	mp := acquirem()
	v := *(*uintptr)(unsafe.Pointer(a))
	if want >= cUntyped {
		if want == cTyped {
			ids := s.cIDsOf()
			if ids == nil {
				ids = s.newCIDs()
			}
			atomic.Store(ids.word(w), id)
		}
		if id == cCapID && id != 0 {
			// An intptr_t: what was there is an integer, which
			// does not say that it is a pointer.
			if v&cIntTag != 0 {
				atomic.Casuintptr((*uintptr)(unsafe.Pointer(a)), v, v&^cIntTag)
			}
			v = 0
		} else if have < cUntyped && v != 0 && v&cPoison == 0 {
			// What was written there as bytes, or as an integer,
			// is not a pointer that the program was given, and
			// the collector did not look at it: it is one that
			// cannot be used.
			if have == cUnclaimed {
				cClaimStats.bytes.Add(1)
			} else {
				cClaimStats.forged.Add(1)
			}
			if v >= minLegalPointer {
				atomic.Casuintptr((*uintptr)(unsafe.Pointer(a)), v, v&^cPtrBits|cPoison)
				v = 0
			}
		}
	}
	// The collector sees what the word holds, whichever way its
	// class goes: it looked at it and no longer will, or it will from
	// here on.
	cShadeWord(v &^ cPtrBits)
	cClassSet(b, w, 1, want)
	releasem(mp)
}

// cReclaim is for a conversion to elem over the w'th word of s, which
// was claimed for something that elem is not compatible with there. The
// memory is used for something else than it was, as an allocator of C's
// does with the blocks that it keeps. The word is claimed again, and
// what it holds is what a word that is claimed for the first time would:
// a pointer of the new type, or one that cannot be used.
//
// The pointers that were converted before are wrong for the word now,
// and the code that has them is not checked again: they are taken away
// (crepair.go), once for all the words of a conversion, which cClaim
// sees to if cReclaim reports true. GODEBUG=creclaim=1 only counts, and
// creclaim=2 stops the program, as it is stopped where the world cannot
// be: in the runtime's own code.
func cReclaim(s *mspan, b *cBits, w, a uintptr, have, want, old, id uint32, elem *_type) (repair bool) {
	if mp := getg().m; mp.cprobe {
		mp.cprobeFail = true
		return false
	}
	n := cClaimStats.reclaims.Add(1)
	mode := debug.creclaim
	if mode == 0 && !cCanRepair() {
		mode = 2
	}
	if mode == 2 || debug.cclaims > 1 && n <= 64 {
		print("c claim: word ", w, " at ", hex(a), " of class ", have)
		if have == cTyped && old != 0 && old < cUnionStartID {
			_, t, _ := cLeafOf(old)
			print(" (", toRType(t).string(), ")")
		}
		print(" claimed for class ", want)
		if want == cTyped && id < cUnionStartID {
			_, t, _ := cLeafOf(id)
			print(" (", toRType(t).string(), ")")
		}
		print(" by a conversion to *", toRType(elem).string(), "\n")
	}
	if mode == 2 {
		throw("checkptr: converted pointer type is not compatible with what the memory was claimed for")
	}
	if want == cTyped && have >= cUntyped && id < cGoID {
		// A pointer of another type, or to anything: what it is
		// as one of this type.
		cNarrow(a, id)
	}
	cClaimWord(s, b, w, a, have, want, id)
	return mode == 0
}

// cConvMax is the size of the largest type that compiled code converts
// a pointer to with no more than c23Header's word for it: what is that
// far from the end of the stack, the data or the bss is in it whole.
// (cmd/compile/internal/ssa's CConvMax.)
const cConvMax = 4096

// What c23Header gives as the classes of what is not C's, where it has
// a shift that is not 3: every word is then the first, and the uint64s
// are for what asks. The first is what a conversion looks at, where it
// looks (a shift of 59): nothing claimed, so that the runtime is asked.
// The second is for a store of a pointer to anything, which is let
// through if the class is that of one; the third for a store of bytes,
// which is let through if it is no pointer's. (The fourth is only
// loaded.)
var (
	// What the runtime knows nothing of: a store is let through.
	cStoreFree = [4]uint64{0, cClassLows * cUntyped, 0, 0}
	// An object of Go's without pointers, and a variable without:
	// bytes are stored, and a pointer is the runtime's to refuse.
	cStoreNoPtr = [4]uint64{0, cClassLows * cTyped, 0, 0}
	// An object of Go's with pointers, and a variable with: the
	// runtime looks at its type.
	cStoreTyped = [4]uint64{0, cClassLows * cTyped, cClassLows * cTyped, 0}
)

// c23Header returns what does not change of where p points for as long
// as what it points into is alive, for the checks that compiled code
// makes of the pointers that are made of p
// (cmd/compile/internal/ssa/cheaders.go): it is looked for once for p,
// before a loop if p is from before it, and each check is then a
// comparison in the compiled code, with the call it was for what the
// comparison does not let through.
//
// For a pointer q made of p, both without cEnd, let
//
//	off = uintptr(q) - uintptr(p) + d
//
// which is where q is in what p points into, if it is in it: the object
// of the heap that p is in, as far as it was asked for if it is one of
// C's; the goroutine's stack from p on; or the section of data or bss
// that p is in. (The numbers are relative to p, which the compiled code
// has: a stack that moves takes p with it, and they stay as they are.)
// Of anything else, only p itself is.
//
// q is into it, and not to its end, if off < n: what arithmetic gave is
// then a pointer that may be loaded and stored through as bytes are. A
// pointer to the end, or out of it, is the runtime's to say
// (checkptrArithmeticC).
//
// A q that is converted to a pointer to a type of size bytes, cConvMax
// or less, has the whole of a value of the type in it if
//
//	off < max(m+1, size) - size
//
// m is 0 for an object of Go's, which has a type to look at, so that
// its conversions are the runtime's.
//
// If p is in an object of C's, sh is 3, and the class of the word that
// q is at (cClass: what the word was claimed for) is the low two bits
// of the byte at bits + w, where
//
//	w = (off + b8) >> sh
//
// A conversion to a type of a few words needs no more than that the
// words are claimed for what the type has there. For what is not C's,
// sh is 63 or 59, w is 0, and bits is of uint64s that say what a store
// is to do (cStoreFree): a store of a pointer to anything looks at the
// classes in the second, at bits + w + (sh>>2)&8, and a store of bytes
// at those in the third, 8 further. A variable of the stack or
// of the data is c23HeaderVar's to say.
//
//go:nosplit
func c23Header(p unsafe.Pointer) (d, n, m, b8, sh, bits uintptr) {
	sh, bits = 63, uintptr(unsafe.Pointer(&cStoreFree))
	a := uintptr(p) &^ cPtrBits
	if uintptr(p)&cPoison != 0 || a < minLegalPointer {
		return
	}
	// la is an address in what p points into: a pointer to the end
	// of something that is not C's is the address of what is next.
	la := a
	if uintptr(p)&cEnd != 0 {
		la = cEndAddr(uintptr(p))
	}
	gp := getg()
	if gp.stack.lo <= la && la < gp.stack.hi {
		return c23HeaderVar(a, la)
	}
	// The span of a small object of C's that was looked up before
	// is remembered with all that is asked of it here (cHeaderEnt).
	var he *cHeaderEnt
	epoch := cHeaderEpoch.Load()
	if pp := gp.m.p.ptr(); pp != nil {
		he = &pp.cHeaders[la/pageSize%uintptr(len(pp.cHeaders))]
		if he.page == la/pageSize && he.epoch == epoch && la < he.limit && !he.large {
			i := uintptr(uint64(la-he.base) * uint64(he.divMul) >> 32)
			base := he.base + i*he.elemsize
			size := uintptr(*(*uint16)(unsafe.Pointer(he.sizes + i*2)))
			return a - base, size, size, base - he.base, 3, he.bits
		}
	}
	if s := spanOfHeap(la); s != nil {
		if s.isUserArenaChunk {
			return
		}
		i := s.objIndex(la)
		base := s.base() + i*s.elemsize
		if !s.spanclass.c() {
			bits = uintptr(unsafe.Pointer(&cStoreTyped))
			if s.spanclass.noscan() {
				bits = uintptr(unsafe.Pointer(&cStoreNoPtr))
			}
			return a - base, s.elemsize, 0, 0, sh, bits
		}
		b := s.cBitsOf()
		if b == nil {
			return
		}
		size := s.cSizeOf(i)
		if he != nil && s.spanclass.sizeclass() != 0 {
			// (The epoch is from before the span was found:
			// if it was freed since, this is not found again.)
			*he = cHeaderEnt{
				page: la / pageSize, epoch: epoch, base: s.base(), limit: s.limit, elemsize: s.elemsize, divMul: s.divMul,
				span: uintptr(unsafe.Pointer(s)),
				bits: uintptr(unsafe.Pointer(b)), sizes: uintptr(s.cSizesOf()),
			}
		}
		return a - base, size, size, base - s.base(), 3, uintptr(unsafe.Pointer(b))
	}
	return c23HeaderVar(a, la)
}

// A cHeaderEnt is what c23Header says of the objects of a span of C's
// small objects, for one page of the span: where the span begins, the
// size of its slots and the number that divides by it, where the
// classes of its words are and where the sizes that its objects were
// asked for are. Finding them takes loads from the heap's table of
// spans, from the span and from its cBits, each of memory that is
// seldom in a cache; a P keeps them for the pages that it looked up.
//
// What is kept holds while the span is in use, which it is for as long
// as a pointer of the program's points into it. cHeaderEpoch changes
// when a span of C's gives its cBits back (freeCBits), and nothing
// that was kept before that is used after it.
type cHeaderEnt struct {
	page     uintptr // the number of the page; no page of the heap is 0
	epoch    uint64
	base     uintptr
	limit    uintptr
	elemsize uintptr
	bits     uintptr // *cBits
	span     uintptr // *mspan
	sizes    uintptr // the uint16s after the classes; of a large span, the address of its csize
	divMul   uint32
	large    bool // a span of one large object: c23Header does not use it
}

var cHeaderEpoch atomic.Uint64

// c23HeaderVar is c23Header for an address a that is in no object of
// the heap: what it says is of the variable of the stack or of the
// program's data that la is in (stackvar.go), and nothing if it is in
// none that is known.
//
// A variable ends where it does, as an object of C's: n and m are its
// size, which is all that arithmetic and a conversion to bytes ask. The
// other conversions are compared with the variable's type by the
// runtime (checkptrConvVar), but for one: if the variable has no
// pointer, a type of a few words without pointers that is in it whole
// is let through by the compiled code, and so is a store of bytes. What
// says so to that code is the classes of cVarScalar, as if they were
// the variable's: b8 is such that the last whole word of the variable
// has the last class of them that is for no pointer, and the words
// after it have none, so that a type that does not end in the
// variable's whole words is the runtime's to look at. For any other
// variable the classes are those of an object of Go's (cStoreTyped,
// cStoreNoPtr), which let no conversion through but one to bytes.
//
//go:nosplit
func c23HeaderVar(a, la uintptr) (d, n, m, b8, sh, bits uintptr) {
	if gp := getg(); cShadowOn && gp.stack.lo <= la && la < gp.stack.hi {
		// A variable that the shadow has: its words' bytes are
		// there as an object of C's has them, those that hold a
		// pointer as pointers of a type, which the compiled code
		// lets nothing through for.
		if base, size, ok := cShadowStackVar(la); ok {
			return a - base, size, size, 0, 3, uintptr(unsafe.Pointer(cShadowOf(base)))
		}
	}
	base, size, typ, typed, state := checkptrVar(la)
	if state != varFound {
		return 0, 0, 0, 0, 63, uintptr(unsafe.Pointer(&cStoreFree))
	}
	// (base is of the stack as it is now, and d is not: it is where a
	// is in the variable. b8 has of base only how it is aligned, which
	// a stack that moves keeps.)
	if !typed || typ != nil && typ.Pointers() {
		// (With a shift of 59 every word is the first, and bit 2
		// of it is not set: the classes are looked at, as for an
		// object of C's.)
		return a - base, size, size, 0, 59, uintptr(unsafe.Pointer(&cStoreTyped))
	}
	if end := base + size; end>>3-base>>3 <= cVarWords {
		return a - base, size, size, base&7 + 8*(cVarWords-(end>>3-base>>3)), 3, uintptr(unsafe.Pointer(&cVarScalar))
	}
	return a - base, size, size, 0, 59, uintptr(unsafe.Pointer(&cStoreNoPtr))
}

// cVarWords is the number of words of the largest variable without
// pointers that compiled code is told the classes of.
const cVarWords = 8192

// cVarScalar is the classes of cVarWords words that are claimed for no
// pointer, and after them of words that nothing claimed: as many as the
// compiled code loads the classes of at a time (cheaders.go).
var cVarScalar [cVarWords/cClassPer + 4]uint64

func init() {
	for i := 0; i < cVarWords/cClassPer; i++ {
		cVarScalar[i] = cClassLows * cScalar
	}
}

// cEndAddr returns an address in what the pointer p, which may have
// cEnd, points into or to the end of. An object of C's has its end in
// its own memory (cPad). The end of anything else is where the next
// thing begins, and what p is the end of has the byte before.
//
//go:nosplit
func cEndAddr(p uintptr) uintptr {
	a := p &^ cPtrBits
	if p&cEnd == 0 {
		return a
	}
	if s := spanOfHeap(a); s != nil && s.spanclass.c() {
		return a
	}
	return a - 1
}

// c23slow reports whether a check of the pointer q, which is made of p,
// is to be made by the runtime: compiled code calls it where it has
// nothing of c23Header's for p, and the compiler makes of the call what
// it knows.
func c23slow(q, p unsafe.Pointer) bool { return true }

// c23Convert is the check of the conversion of p to a pointer to n
// values of elem that a package of C's makes, for the conversions that
// internal/c23 makes for one: of the pointer that a union's members
// share.
//
//go:linkname c23Convert
func c23Convert(p unsafe.Pointer, elem *_type, n uintptr) {
	checkptrAlignmentC(p, elem, n)
}

// c23InObject says where the address r is in what p points into: 0 if
// out of it, 1 if in it, 2 if at its end. It is for arithmetic on an
// intptr_t that is a pointer, whose result is a pointer still if it
// stays there, with cEnd if it is the end (internal/c23's intcap.go).
//
//go:linkname c23InObject
func c23InObject(p, r uintptr) int {
	base, size, kind := cObjectOf(cEndAddr(p))
	switch {
	case kind == cObjNone:
		return 0
	case r-base < size:
		return 1
	case r-base == size:
		return 2
	}
	return 0
}

// cIntPtrs counts the integers that C converted to pointers and that
// were the addresses of something of the program's: pointers that
// nothing says the program was given (internal/c23's IntPtr).
var cIntPtrs atomic.Uint64

// c23IntPtr is the conversion of the integer v to a pointer, for C. An
// integer that is no address of the program's is a pointer that C may
// keep and compare. One that is an address, of an object of the heap
// that is allocated, of a stack or of the program's data, is counted,
// and is given cPoison, unless GODEBUG=cintptr=0.
//
//go:linkname c23IntPtr
//go:nosplit
func c23IntPtr(v uintptr) unsafe.Pointer {
	if v < minLegalPointer || v&(cPtrBits|cPoison) != 0 {
		return unsafe.Pointer(v)
	}
	addr := false
	if s := spanOf(v); s != nil {
		switch s.state.get() {
		case mSpanInUse:
			// (Anywhere in the span: what is after its last
			// object is where a span of small objects keeps
			// its mark bits.)
			addr = true
		case mSpanManual:
			addr = true // a stack
		}
	} else {
		// The program's own code and data, and what the runtime
		// keeps for itself (rtmem.go).
		addr = rtMemOwned(v)
		for datap := &firstmoduledata; datap != nil && !addr; datap = datap.next {
			addr = datap.text <= v && v < datap.etext
		}
	}
	if addr {
		cIntPtrs.Add(1)
		if debug.cintptr != 0 {
			return unsafe.Pointer(v | cPoison)
		}
	}
	return unsafe.Pointer(v)
}

// The stores that the compiled code of a package of C's asks about
// (cmd/compile/internal/ssagen's cStoreCheck). A store through a pointer
// to a type is to a word that the pointer's conversion claimed for what
// the type has there, and is a plain store. Two kinds of pointer claim
// nothing that says what may be stored:
//
//   - a pointer to bytes is over anything, and bytes stored over a word
//     that holds a pointer would make a pointer that the program was not
//     given;
//   - a pointer to a pointer to anything (void **, char **) may be over
//     a pointer of a type, where what is stored is to be one of that
//     type.
//
// So such a store looks at the class of the word first, in the compiled
// code where it has the header of what the pointer points into, and
// asks here for what that does not let through.

// cByteOver reports whether one of the n bytes at a is part of a word
// that holds a pointer, of an object of the heap.
func cByteOver(a, n uintptr) bool {
	const word = goarch.PtrSize
	if n == 0 {
		return false
	}
	s := spanOfHeap(a)
	if gp := getg(); s == nil || gp.stack.lo <= a && a < gp.stack.hi {
		// A variable of the stack or of the data.
		base, size, typ, typed, state := checkptrVar(a)
		if state != varFound || !typed || typ == nil || !typ.Pointers() {
			return false
		}
		mask := getGCMask(typ)
		for off := alignDown(a-base, word); off < min(a-base+n, size); off += word {
			if compatMaskBit(typ, mask, off) {
				return true
			}
			if k, _, _ := compatLeaf(typ, off); k >= compatString {
				return true
			}
		}
		return false
	}
	if s.isUserArenaChunk {
		return false
	}
	lo, hi := alignDown(a, word), alignUp(a+n, word)
	if s.spanclass.c() {
		b := s.cBitsOf()
		if b == nil {
			return false
		}
		i := s.objIndex(a)
		hi = min(hi, alignUp(s.base()+i*s.elemsize+s.cSizeOf(i), word))
		for x := lo; x < hi; x += word {
			if cClass(b, (x-s.base())/word) >= cUntyped {
				return true
			}
		}
		return false
	}
	if s.spanclass.noscan() || s.isFree(s.objIndex(a)) {
		return false
	}
	hi = min(hi, s.base()+(s.objIndex(a)+1)*s.elemsize)
	if lo >= hi {
		return false
	}
	tp := s.typePointersOf(lo, hi-lo)
	_, slot := tp.next(hi)
	return slot != 0
}

// c23ByteStore is called before n bytes are stored at p through a
// pointer to bytes, where the compiled code does not know that no word
// of them holds a pointer.
func c23ByteStore(p unsafe.Pointer, n uintptr) {
	a := uintptr(p)
	if a < minLegalPointer || a&(cPoison|cEnd) != 0 {
		return // the store faults
	}
	if cByteOver(a&^cPtrBits, n) && !cStoreReclaim(a&^cPtrBits, n, cUnclaimed) {
		throw("checkptr: bytes stored over a pointer")
	}
}

// cStoreReclaim is for a store that the words it is to do not let
// through, in an object of C's: bytes over words that hold pointers
// (want is cUnclaimed), or a pointer to anything over a word that holds
// none (cUntyped). The memory is used for something else than it was,
// as it is where a conversion says so (cReclaim): the words are claimed
// again, for nothing or for a pointer to anything, and the pointers
// that were converted before are taken away (crepair.go). It reports
// false if that is not to be: the store is not to an object of C's, or
// GODEBUG=creclaim=2, or the world cannot be stopped here.
func cStoreReclaim(a, n uintptr, want uint32) bool {
	const word = goarch.PtrSize
	s := spanOfHeap(a)
	if s == nil || !s.spanclass.c() || s.isUserArenaChunk {
		return false
	}
	b := s.cBitsOf()
	mode := debug.creclaim
	if b == nil || mode == 2 || mode == 0 && !cCanRepair() {
		return false
	}
	i := s.objIndex(a)
	base := s.base() + i*s.elemsize
	size := s.cSizeOf(i)
	lo, hi := alignDown(a, word), min(alignUp(a+n, word), alignUp(base+size, word))
	for x := lo; x < hi; x += word {
		w := (x - s.base()) / word
		have := cClass(b, w)
		if want == cUnclaimed && have < cUntyped || want == cUntyped && have == cUntyped {
			continue
		}
		cClaimStats.reclaims.Add(1)
		cClaimWord(s, b, w, x, have, want, 0)
	}
	if mode == 0 {
		cRepair(base, size)
	}
	return true
}

// c23PtrStore is called before the pointer v is stored at p through a
// pointer to a pointer to anything, where the compiled code does not
// know that the word is one that holds a pointer to anything. It
// returns what is to be stored: v, or if the word holds a pointer of a
// type, v as a pointer of that type, which is one that cannot be used
// if what v points to is not what the type says (cConvProbe).
func c23PtrStore(p unsafe.Pointer, v unsafe.Pointer) unsafe.Pointer {
	const word = goarch.PtrSize
	a := uintptr(p)
	if a < minLegalPointer || a&(cPoison|cEnd) != 0 {
		return v // the store faults
	}
	a &^= cPtrBits
	s := spanOfHeap(a)
	if gp := getg(); s == nil || gp.stack.lo <= a && a < gp.stack.hi {
		// A variable of the stack or of the data: what its type
		// has there.
		if _, _, _, typed, state := checkptrVar(a); state != varFound || !typed || a&(word-1) != 0 {
			return v
		}
		switch c, id := cWordOf(a); {
		case c == cUntyped || c == cUnknown:
			return v
		case c == cTyped && id != 0 && id < cGoID:
			return unsafe.Pointer(cConvProbe(uintptr(v), id))
		}
		throw("checkptr: pointer stored over a word that holds none, or part of something else")
	}
	if s.isUserArenaChunk || a&(word-1) != 0 {
		return v
	}
	if s.spanclass.c() {
		b := s.cBitsOf()
		if b == nil {
			return v
		}
		w := (a - s.base()) / word
		switch cClass(b, w) {
		case cUntyped:
			return v
		case cUnclaimed:
			cClaimStats.claims.Add(1)
			cClaimWord(s, b, w, a, cUnclaimed, cUntyped, 0)
			return v
		case cTyped:
			if ids := s.cIDsOf(); ids != nil {
				if id := atomic.Load(ids.word(w)); id != 0 && id < cGoID {
					return unsafe.Pointer(cConvProbe(uintptr(v), id))
				}
			}
		case cScalar:
			// The word is used for something else than it was.
			if cStoreReclaim(a, word, cUntyped) {
				return v
			}
		}
		throw("checkptr: pointer stored over a word that holds none, or part of something else")
	}
	if s.spanclass.noscan() {
		// What is no address of the heap or of the stack is an
		// integer, which the object is for (sync/atomic's own tests
		// store such in a uint64): a pointer there is one that the
		// collector would not see.
		x := uintptr(v) &^ cPtrBits
		if gp := getg(); spanOfHeap(x) == nil && (x < gp.stack.lo || x >= gp.stack.hi) {
			return v
		}
		throw("checkptr: pointer stored in an object that has no pointer")
	}
	base := s.base() + s.objIndex(a)*s.elemsize
	typ, tbase := checkptrObjectType(s, base)
	if typ == nil || a < tbase {
		// An object that only has a bitmap: a pointer where it
		// has one.
		tp := s.typePointersOf(a, word)
		if _, slot := tp.next(a + word); slot != a {
			throw("checkptr: pointer stored over a word that holds none")
		}
		return v
	}
	off := (a - tbase) % typ.Size_
	if !compatMaskBit(typ, getGCMask(typ), off) {
		throw("checkptr: pointer stored over a word that holds none")
	}
	switch k, leaf, _ := compatLeaf(typ, off); k {
	case compatUnsafe:
		return v
	case compatPtr:
		if compatBytePtr(leaf) && cObjectIsC(typ) {
			return v // C's char *
		}
		return unsafe.Pointer(cConvProbeType(uintptr(v), leaf, false))
	}
	throw("checkptr: pointer stored over part of something else")
	return nil
}

// checkptrStore is c23PtrStore for a package of Go's, and for
// sync/atomic's stores of pointers: what is stored through a
// *unsafe.Pointer that is over a pointer of a type is one of that type,
// or the program stops.
func checkptrStore(p unsafe.Pointer, v unsafe.Pointer) unsafe.Pointer {
	if v == nil || uintptr(v) < minLegalPointer {
		return v
	}
	if s := spanOfHeap(uintptr(p)); s == nil || s.isUserArenaChunk {
		return v // (the stack, the data: to come)
	}
	if c23PtrStore(p, v) != v {
		throw("checkptr: pointer stored through an unsafe.Pointer is not one that the memory's pointer may be")
	}
	return v
}

// c23PtrStores is c23PtrStore for n bytes of pointers to anything that
// are copied to p: each word is one that holds a pointer to anything.
func c23PtrStores(p unsafe.Pointer, n uintptr) {
	const word = goarch.PtrSize
	a := uintptr(p)
	if a < minLegalPointer || a&(cPoison|cEnd) != 0 {
		return
	}
	a &^= cPtrBits
	s := spanOfHeap(a)
	if s == nil || s.isUserArenaChunk {
		return
	}
	for x := a; x < a+n; x += word {
		if q := c23PtrStore(unsafe.Pointer(x), nil); q != nil {
			throw("checkptr: pointers stored over pointers of a type")
		}
		if s.spanclass.c() {
			if b := s.cBitsOf(); b != nil && cClass(b, (x-s.base())/word) != cUntyped {
				throw("checkptr: pointers stored over pointers of a type")
			}
		}
	}
}

// c23Malloc is malloc for C translated by cmd/cc (internal/c23): size
// bytes, zeroed. If typ is not nil the object is for values of typ: the
// words of the values that fit in it are claimed for what typ has
// there, unless typ is bytes, which claim nothing. If scalar is set,
// every word of it is claimed for no pointer.
//
//go:linkname c23Malloc
func c23Malloc(size uintptr, typ *_type, scalar bool) unsafe.Pointer {
	x := mallocC(size)
	if x == nil || !scalar && (typ == nil || typ.Size_ == 0 || !typ.Pointers() && typ.Align_ == 1) {
		return x
	}
	// The object is not reached yet by anything that looks at claims.
	const word = goarch.PtrSize
	s := spanOfUnchecked(uintptr(x))
	b := s.cBitsOf()
	w := (uintptr(x) - s.base()) / word
	if scalar || !typ.Pointers() {
		cClassSet(b, w, s.elemsize/word, cScalar)
		return x
	}
	if typ.Size_%word != 0 {
		return x
	}
	cClaimValues(s, b, w, typ, size)
	publicationBarrier()
	return x
}

// cClaimValues claims the words from the w'th of s, in an object that
// is not reached yet by anything that looks at claims, for the whole
// values of typ that fit in size bytes: what typ has there.
func cClaimValues(s *mspan, b *cBits, w uintptr, typ *_type, size uintptr) {
	const word = goarch.PtrSize
	if typ.Size_ == 0 || typ.Size_%word != 0 {
		return
	}
	if !typ.Pointers() {
		if typ.Align_ != 1 {
			cClassSet(b, w, size/typ.Size_*(typ.Size_/word), cScalar)
		}
		return
	}
	mask := getGCMask(typ)
	each := typ.Size_ / word
	total := size / typ.Size_ * each
	var ids *cBits
	// What a value's words are claimed for, once: they repeat.
	var first [16]struct{ class, id uint32 }
	for i := uintptr(0); i < total; i++ {
		f := i % each
		var class, id uint32
		if i < each || f >= uintptr(len(first)) {
			class = cScalar
			if k, leaf, in := compatLeaf(typ, f*word); k != compatScalar && (compatMaskBit(typ, mask, f*word) || k >= compatString) {
				class, id = cWant(k, leaf, in, true)
			}
			if c, uid := cUnionID(typ, f*word); uid != 0 {
				class, id = c, uid
			}
			if f < uintptr(len(first)) {
				first[f].class, first[f].id = class, id
			}
		} else {
			class, id = first[f].class, first[f].id
		}
		if id != 0 {
			if ids == nil {
				if ids = s.cIDsOf(); ids == nil {
					ids = s.newCIDs()
				}
			}
			atomic.Store(ids.word(w+i), id)
		}
		if class != cUnclaimed {
			cClassSet(b, w+i, 1, class)
		}
	}
}

// cUnknown is the class that cWordOf gives for a word that the runtime
// has no claim and no type for: one of the stack or of the data.
const cUnknown = 4

// cWordOf returns what the word at a is: its class and, if that is
// cTyped, its id, as a word of C's has them. A word of an object of Go's
// is what the type that the object was allocated as has there, with a
// pointer to a byte as a pointer of a type, which it is to Go, unless C
// allocated the object; or, of an object that only has a bitmap, a
// pointer to anything where that has a pointer.
func cWordOf(a uintptr) (uint32, uint32) {
	const word = goarch.PtrSize
	a = alignDown(a, word)
	s := spanOfHeap(a)
	if gp := getg(); s == nil || s.isUserArenaChunk || gp.stack.lo <= a && a < gp.stack.hi {
		// A variable of the stack or of the data has its type
		// (stackvar.go). A pointer to a byte in one is taken for
		// C's, a pointer to anything: the variable is C's where
		// C copies to it or from it.
		base, _, typ, typed, state := checkptrVar(a)
		if state != varFound || !typed {
			return cUnknown, 0
		}
		if typ == nil || !typ.Pointers() {
			return cScalar, 0
		}
		off := a - base
		k, leaf, in := compatLeaf(typ, off)
		if k == compatScalar || k < compatString && !compatMaskBit(typ, getGCMask(typ), off) {
			return cScalar, 0
		}
		return cWant(k, leaf, in, true)
	}
	if s.spanclass.c() {
		b := s.cBitsOf()
		if b == nil {
			return cUnclaimed, 0
		}
		w := (a - s.base()) / word
		c := cClass(b, w)
		if c == cTyped {
			if ids := s.cIDsOf(); ids != nil {
				return c, atomic.Load(ids.word(w))
			}
		}
		return c, 0
	}
	if s.spanclass.noscan() {
		return cScalar, 0
	}
	i := s.objIndex(a)
	if s.isFree(i) {
		return cUnknown, 0
	}
	base := s.base() + i*s.elemsize
	if typ, tbase := checkptrObjectType(s, base); typ != nil && a >= tbase {
		off := (a - tbase) % typ.Size_
		k, leaf, in := compatLeaf(typ, off)
		if k == compatScalar || k < compatString && !compatMaskBit(typ, getGCMask(typ), off) {
			return cScalar, 0
		}
		return cWant(k, leaf, in, cObjectIsC(typ))
	}
	tp := s.typePointersOf(a, word)
	if _, slot := tp.next(a + word); slot == a {
		return cUntyped, 0
	}
	return cScalar, 0
}

// cRevive returns p as a pointer to bytes: without cEnd, if it has it
// and is not to the end of what it points into. A pointer that nothing
// is loaded through as what its type says, since the memory is used for
// something else (crepair.go), is one to the bytes that are there all
// the same.
func cRevive(p unsafe.Pointer) unsafe.Pointer {
	a := uintptr(p)
	if a&cEnd == 0 || a&cPoison != 0 {
		return p
	}
	x := a &^ cPtrBits
	if base, size, kind := cObjectOf(cEndAddr(a)); kind != cObjNone && x-base < size {
		return unsafe.Pointer(x)
	}
	return p
}

// cMoveBounds stops the program if the n bytes at a, which memcpy or
// memset is given, are not in one object. It returns the span of the
// object, if it is one of the heap.
func cMoveBounds(a, n uintptr) *mspan {
	if a < minLegalPointer || a&(cPoison|cEnd) != 0 {
		return nil // the copy faults
	}
	a &^= cPtrBits
	if gp := getg(); gp.stack.lo <= a && a < gp.stack.hi {
		// A variable of a frame (stackvar.go).
		if base, size, _ := cObjectOf(a); n > size-(a-base) {
			throw("checkptr: memory copied or set past the end of its object")
		}
		return nil
	}
	if s := spanOfHeap(a); s != nil && !s.isUserArenaChunk {
		i := s.objIndex(a)
		size := s.elemsize
		if s.spanclass.c() {
			size = s.cSizeOf(i)
		} else if cGoObjectWhole(s, s.base()+i*s.elemsize) {
			// What holds a string, a slice or an interface of
			// Go's is not bytes to C: what the library keeps
			// for itself is made of them (c23Extent).
			throw("checkptr: memory copied or set in an object of Go's that is not bytes")
		}
		if n > size-(a-s.base()-i*s.elemsize) {
			throw("checkptr: memory copied or set past the end of its object")
		}
		return s
	}
	if base, size, kind := cObjectOf(a); kind != cObjNone && n > size-(a-base) {
		throw("checkptr: memory copied or set past the end of its object")
	}
	return nil
}

// cMovePtrs reports whether a word that the n bytes at a are in, of an
// object of the span s, may hold a pointer: any of an object of Go's
// that has pointers, and for one of C's the words that are claimed for
// one.
func cMovePtrs(s *mspan, a, n uintptr) bool {
	const word = goarch.PtrSize
	if !s.spanclass.c() {
		return !s.spanclass.noscan()
	}
	b := s.cBitsOf()
	if b == nil {
		return false
	}
	w, end := (alignDown(a, word)-s.base())/word, (alignUp(a+n, word)-s.base())/word
	for w < end {
		// The words of one uint64 of the classes.
		k := min(end-w, cClassPer-w%cClassPer)
		if atomic.Load64(b.group(w/cClassPer))>>(w%cClassPer*8)&cClassesOf(k)&cClassHighs != 0 {
			return true
		}
		w += k
	}
	return false
}

// cMoveCheck is what c23Memmove asks before it copies n bytes from sr
// to d, into an object of the heap: what a store would be asked
// (c23ByteStore, c23PtrStore), for every word. Bytes are not copied over
// part of a word that holds a pointer. A word that holds a pointer gets
// a pointer of its kind: what is copied from a word that is claimed for
// one that is compatible, or what that word holds as a pointer of the
// type (cConvProbe), and a pointer that cannot be used for anything
// else that is not zero. It reports whether there is such a word; if
// apply is set the bytes were copied, and it puts in those words what
// they are to hold.
func cMoveCheck(d, sr, n uintptr, apply bool) (fix bool) {
	const word = goarch.PtrSize
	if d < minLegalPointer || d&(cPoison|cEnd) != 0 {
		return false
	}
	ds := spanOfHeap(d)
	if gp := getg(); gp.stack.lo <= d && d < gp.stack.hi {
		ds = nil
	}
	if ds != nil && (ds.isUserArenaChunk || !ds.spanclass.c() && ds.spanclass.noscan()) {
		return false
	}
	wlo, whi := alignUp(d, word), alignDown(d+n, word)
	if (d^sr)&(word-1) != 0 || wlo >= whi {
		if !apply && cByteOver(d, n) && !cStoreReclaim(d, n, cUnclaimed) {
			throw("checkptr: bytes copied over a pointer")
		}
		return false
	}
	if !apply && (d < wlo && cByteOver(d, wlo-d) && !cStoreReclaim(d, wlo-d, cUnclaimed) ||
		whi < d+n && cByteOver(whi, d+n-whi) && !cStoreReclaim(whi, d+n-whi, cUnclaimed)) {
		throw("checkptr: bytes copied over a pointer")
	}
	if ds != nil && !ds.spanclass.c() {
		// From an object of Go's of the same type, at the same
		// place in a value: as an assignment.
		if ss := spanOfHeap(sr); ss != nil && !ss.spanclass.c() && !ss.spanclass.noscan() && !ss.isUserArenaChunk {
			dt, dbase := checkptrObjectType(ds, ds.base()+ds.objIndex(d)*ds.elemsize)
			st, sbase := checkptrObjectType(ss, ss.base()+ss.objIndex(sr)*ss.elemsize)
			if dt != nil && dt == st && d >= dbase && sr >= sbase && (d-dbase)%dt.Size_ == (sr-sbase)%dt.Size_ {
				return false
			}
		}
	}
	for a := wlo; a < whi; a += word {
		dc, did := cWordOf(a)
		if dc < cUntyped {
			// No pointer, or nothing yet: it takes the claim
			// of what is copied.
			continue
		}
		// (What was copied, if it was: the words that it was
		// copied from may be the ones it was copied to.)
		v := *(*uintptr)(unsafe.Pointer(sr + (a - d)))
		if apply {
			v = *(*uintptr)(unsafe.Pointer(a))
		}
		if v == 0 || v&cPoison != 0 {
			continue
		}
		sc, sid := cWordOf(sr + (a - d))
		nv := v
		switch {
		case sc == cUnknown:
			// (The stack, the data: to come.)
		case dc == cTyped && did == cCapID:
			// To an intptr_t: what is not copied from one is
			// an integer.
			if sid != did && v&cIntTag != 0 {
				nv = v &^ cIntTag
			}
		case sc == cTyped && sid == cCapID:
			// From an intptr_t, to a word that holds a pointer:
			// the pointer that it is, or an integer, which is
			// none that the program was given.
			switch {
			case v&cIntTag == 0:
				if v >= minLegalPointer {
					nv = v&^cPtrBits | cPoison
				}
			case dc == cUntyped:
				nv = v &^ cIntTag
			default:
				nv = cConvProbe(v&^cIntTag, did)
			}
		case dc == cTyped && did >= cGoID && did < cUnionStartID:
			if sid != did {
				throw("checkptr: memory copied over a string, a slice or an interface of Go's")
			}
		case sc < cUntyped:
			nv = v&^cPtrBits | cPoison
		case dc == cUntyped:
		case did >= cUnionStartID:
			if sid != did {
				nv = v&^cPtrBits | cPoison
			}
		case sc == cTyped && (sid == did || cCompatIDs(sid, did, true)):
		default:
			nv = cConvProbe(v, did)
		}
		if nv != v {
			fix = true
			if apply {
				if nv&cPoison != 0 {
					cClaimStats.forged.Add(1)
				}
				*(*uintptr)(unsafe.Pointer(a)) = nv
			}
		}
	}
	return fix
}

// c23Memmove is memmove for C. The bytes are in the objects that the
// two pointers point into, or the program stops; and what is copied
// into an object of the heap is what its words may hold (cMoveCheck).
//
//go:linkname c23Memmove
func c23Memmove(dst, src unsafe.Pointer, n uintptr) {
	if dst == src || n == 0 {
		return
	}
	dst, src = cRevive(dst), cRevive(src)
	if cMoveFast(uintptr(dst), uintptr(src), n) {
		return
	}
	ds := cMoveBounds(uintptr(dst), n)
	ss := cMoveBounds(uintptr(src), n)
	// (Nothing to ask where no word that is copied to holds a
	// pointer: bytes, and words that take the claims of what is
	// copied.)
	fix := (ds != nil && cMovePtrs(ds, uintptr(dst), n) || ds == nil && cByteOver(uintptr(dst)&^cPtrBits, n)) && cMoveCheck(uintptr(dst), uintptr(src), n, false)
	if ds != nil && ss != nil && ds.spanclass.c() && ds.cBitsOf() != nil && ds.cIDsOf() == nil &&
		(ss.spanclass.c() && ss.cIDsOf() != nil || !ss.spanclass.c() && !ss.spanclass.noscan()) ||
		ds != nil && ss == nil && ds.spanclass.c() && ds.cBitsOf() != nil && ds.cIDsOf() == nil {
		// For the types of the pointers that are copied.
		ds.newCIDs()
	}
	// The collector does not come between what it is shown and the
	// copy.
	mp := acquirem()
	c23Memmove1(dst, src, n, ds, ss)
	if fix {
		cMoveCheck(uintptr(dst), uintptr(src), n, true)
	}
	releasem(mp)
}

// c23Memmove1 is the copy of c23Memmove: the words of it that may be whole
// pointers are copied as pointers are, with the collector's barrier, and
// the bytes before and after them as bytes. A word of C's that it
// copies to whole, which nothing claimed, is claimed for what the word
// it is copied from was, if that is one of C's: an object that is
// copied keeps what it is. A word that was claimed stays as it is.
// (c23Memmove has the m: the collector does not come in between.)
func c23Memmove1(dst, src unsafe.Pointer, n uintptr, ds, ss *mspan) {
	if dst == src || n == 0 {
		return
	}
	const word = goarch.PtrSize
	d, sr := uintptr(dst), uintptr(src)
	lo, hi := alignDown(d, word), alignUp(d+n, word)
	wlo, whi := alignUp(d, word), alignDown(d+n, word)
	aligned := (d^sr)&(word-1) == 0
	// ds and ss are the spans of the heap that the two are in, if
	// they are.
	if ds != nil && !ds.spanclass.c() {
		ds = nil
	}
	b := ds.cBitsOfOrNil()
	if b == nil {
		// A destination of Go's: its own bitmap says where its
		// pointers are.
		if writeBarrier.enabled && aligned && wlo < whi {
			bulkBarrierPreWrite(wlo, sr+(wlo-d), whi-wlo, nil)
		}
		memmove(dst, src, n)
		return
	}
	base := ds.base()
	// No further than the object's slot, whatever is copied.
	limit := base + (ds.objIndex(d)+1)*ds.elemsize
	hi, whi = min(hi, limit), min(whi, limit)
	// What is written over, in whole words or in part.
	cShade(b, (lo-base)/word, lo, 0, (hi-lo)/word)
	if !aligned || wlo >= whi {
		// No word arrives whole: bytes.
		memmove(dst, src, n)
		return
	}
	sw := sr + (wlo - d)
	var sb, sids *cBits
	if ss != nil && ss.spanclass.c() {
		sb, sids = ss.cBitsOf(), ss.cIDsOf()
	}
	w, words := (wlo-base)/word, (whi-wlo)/word
	sw0 := uintptr(0)
	if sb != nil {
		sw0 = (sw - ss.base()) / word
	}
	// In one span the claims are copied as the words are, from the end
	// that leaves what is still to be read as it was.
	back := ss == ds && sw < wlo
	// typedSrc: what is copied from has pointers where its type says:
	// an object of Go's, or a variable of the stack or of the data.
	// (What has none claims nothing where it is copied to: bytes.)
	typedSrc := false
	if ss == nil {
		_, _, typ, typed, state := checkptrVar(sw)
		typedSrc = state == varFound && typed && typ != nil && typ.Pointers()
	} else {
		typedSrc = !ss.spanclass.c() && !ss.spanclass.noscan() && !ss.isUserArenaChunk
	}
	var ids *cBits
	for done := uintptr(0); done < words; {
		// The words of one uint64 of the destination's classes.
		var i, k uintptr
		if back {
			end := words - done
			k = min((w+end-1)%cClassPer+1, end)
			i = end - k
		} else {
			i = done
			k = min(words-i, cClassPer-(w+i)%cClassPer)
		}
		done += k
		all := cClassesOf(k)
		have := atomic.Load64(b.group((w+i)/cClassPer)) >> ((w + i) % cClassPer * 8) & all
		// free has both bits of the words that nothing claimed.
		free := ^(have | have>>1) & cClassLows & all * 3
		if free != 0 {
			var from uint64             // the classes of the words copied from
			var goids [cClassPer]uint32 // their ids, if they are of an object of Go's
			switch {
			case typedSrc:
				// What its type has there.
				for j := uintptr(0); j < k; j++ {
					if c, id := cWordOf(sw + (i+j)*word); c != cUnknown {
						from |= uint64(c) << (8 * j)
						goids[j] = id
					}
				}
			case sb != nil:
				from = cClassWindow(sb, sw0+i) & all
			}
			if take := from & free; take != 0 {
				// The types of the pointers first, and what
				// says where a union is (cunion.go).
				for j := uintptr(0); j < k; j++ {
					c := uint32(take >> (8 * j) & 3)
					if c == cUnclaimed || c != cTyped && sids == nil {
						continue
					}
					if ids == nil {
						ids = ds.cIDsOf()
					}
					if ids == nil || sids == nil && goids[j] == 0 {
						// (No ids to put it in, or none to take.)
						if c == cTyped {
							take &^= 1 << (8 * j) // cUntyped
						}
						continue
					}
					id := goids[j]
					if sids != nil {
						id = atomic.Load(sids.word(sw0 + i + j))
					}
					if c == cTyped || id != 0 {
						atomic.Store(ids.word(w+i+j), id)
					}
				}
				// (Only where nothing claimed the word
				// meanwhile.)
				p, sh := b.group((w+i)/cClassPer), (w+i)%cClassPer*8
				for {
					old := atomic.Load64(p)
					now := old >> sh & all
					t := take & (^(now | now>>1) & cClassLows & all * 3)
					if t == 0 || atomic.Cas64(p, old, old|t<<sh) {
						have = now | t
						break
					}
				}
			}
		}
		if writeBarrier.enabled && have != cClassLows*cScalar&all {
			for j := uintptr(0); j < k; j++ {
				if have>>(8*j)&3 != cScalar {
					cShadeWord(*(*uintptr)(unsafe.Pointer(sw + (i+j)*word)))
				}
			}
		}
	}
	memmove(dst, src, n)
}

// c23Memset is memset for C. The bytes are in the object that the
// pointer points into, or the program stops. A word that holds a pointer
// is set whole, or not at all: to null, or by a c that is not 0 to a
// pointer that cannot be used.
//
//go:linkname c23Memset
func c23Memset(dst unsafe.Pointer, c byte, n uintptr) {
	if n == 0 {
		return
	}
	const word = goarch.PtrSize
	dst = cRevive(dst)
	d := uintptr(dst)
	poison := false
	if ds := cMoveBounds(d, n); ds != nil && cMovePtrs(ds, d, n) && cByteOver(d, n) {
		wlo, whi := alignUp(d, word), alignDown(d+n, word)
		switch {
		case wlo >= whi:
			if !cStoreReclaim(d, n, cUnclaimed) {
				throw("checkptr: bytes set over part of a pointer")
			}
		case d < wlo && cByteOver(d, wlo-d) && !cStoreReclaim(d, wlo-d, cUnclaimed),
			whi < d+n && cByteOver(whi, d+n-whi) && !cStoreReclaim(whi, d+n-whi, cUnclaimed):
			throw("checkptr: bytes set over part of a pointer")
		}
		poison = c != 0
	}
	mp := acquirem()
	c23Memset1(dst, c, n)
	if poison {
		for a := alignUp(d, word); a < alignDown(d+n, word); a += word {
			if dc, _ := cWordOf(a); dc >= cUntyped && dc != cUnknown {
				*(*uintptr)(unsafe.Pointer(a)) |= cPoison
			}
		}
	}
	releasem(mp)
}

// c23Memset1 is the setting of c23Memset: the pointers that it writes
// over are the collector's to see first. It claims nothing: what it
// writes is bytes.
//
//go:nosplit
func c23Memset1(dst unsafe.Pointer, c byte, n uintptr) {
	if n == 0 {
		return
	}
	const word = goarch.PtrSize
	d := uintptr(dst)
	lo, hi := alignDown(d, word), alignUp(d+n, word)
	if s := cSpanOf(d); s != nil {
		if b := s.cBitsOf(); b != nil {
			// Every word that is written in, whole or not.
			cShade(b, (lo-s.base())/word, lo, 0, (min(hi, s.limit)-lo)/word)
		}
	} else if writeBarrier.enabled {
		bulkBarrierPreWrite(lo, 0, hi-lo, nil)
	}
	if c == 0 {
		memclrNoHeapPointers(dst, n)
	} else {
		// The first bytes, and then twice what is there until it is all.
		k := min(n, 16)
		for i := uintptr(0); i < k; i++ {
			*(*byte)(add(dst, i)) = c
		}
		for k < n {
			m := min(k, n-k)
			memmove(add(dst, k), dst, m)
			k += m
		}
	}
}

// cHeaderOf returns what the P remembers of the span of C's small
// objects that a is in (cHeaderEnt), which it looks up if it does not
// remember it, with where a's object begins and the size it was asked
// for; ok is false if a is in no span of C's.
//
//go:nosplit
func cHeaderOf(a uintptr) (h cHeaderEnt, base, size uintptr, ok bool) {
	pp := getg().m.p.ptr()
	if pp == nil {
		return
	}
	he := &pp.cHeaders[a/pageSize%uintptr(len(pp.cHeaders))]
	epoch := cHeaderEpoch.Load()
	if he.page != a/pageSize || a >= he.limit || he.epoch != epoch {
		s := spanOfHeap(a)
		if s == nil || !s.spanclass.c() || s.isUserArenaChunk {
			return
		}
		b := s.cBitsOf()
		if b == nil {
			return
		}
		*he = cHeaderEnt{
			page: a / pageSize, epoch: epoch, base: s.base(), limit: s.limit, elemsize: s.elemsize, divMul: s.divMul,
			span: uintptr(unsafe.Pointer(s)),
			bits: uintptr(unsafe.Pointer(b)), sizes: uintptr(s.cSizesOf()),
		}
		if s.spanclass.sizeclass() == 0 {
			// One object, whose size is the span's to say.
			he.large, he.sizes = true, uintptr(unsafe.Pointer(&s.csize))
		}
	}
	h = *he // (another lookup may take its place)
	if h.large {
		return h, h.base, atomic.Loaduintptr((*uintptr)(unsafe.Pointer(h.sizes))), true
	}
	i := uintptr(uint64(a-h.base) * uint64(h.divMul) >> 32)
	return h, h.base + i*h.elemsize, uintptr(*(*uint16)(unsafe.Pointer(h.sizes + i*2))), true
}

// cNoPtrClasses reports whether none of the n words from the w'th of b
// is claimed for a pointer.
//
//go:nosplit
func cNoPtrClasses(b *cBits, w, n uintptr) bool {
	for end := w + n; w < end; {
		k := min(end-w, cClassPer-w%cClassPer)
		if atomic.Load64(b.group(w/cClassPer))>>(w%cClassPer*8)&cClassesOf(k)&cClassHighs != 0 {
			return false
		}
		w += k
	}
	return true
}

// cMoveFast is c23Memmove for the copy that most are: bytes that hold
// no pointer, into a small object of C's that the P remembers, from
// another such object or from a variable of the program's data that has
// no pointer (a string of C's that is a constant). It reports whether it
// made the copy; if not, nothing was done.
//
// No word that is written over is claimed for a pointer, and none that
// is read: so nothing is to be shown to the collector, and nothing is
// to be asked of the words (cMoveCheck). The words that arrive whole in
// words that nothing claimed are claimed for no pointer if those they
// are copied from are, as the copy of c23Memmove1 does, so that what is
// copied is what it was. That is only done while the collector is not
// marking: it then looks at no word that it looked at before.
func cMoveFast(d, s, n uintptr) bool {
	const word = goarch.PtrSize
	if writeBarrier.enabled || d+n < d || s+n < s {
		return false
	}
	if gp := getg(); gp.stack.lo <= s && s < gp.stack.hi || gp.stack.lo <= d && d < gp.stack.hi {
		return false // a variable of a frame: found another way
	}
	dh, dbase, dsize, ok := cHeaderOf(d)
	if !ok || d-dbase > dsize || n > dsize-(d-dbase) {
		return false // (past the end: the program is stopped there)
	}
	db := (*cBits)(unsafe.Pointer(dh.bits))
	lo, hi := alignDown(d, word), alignUp(d+n, word)
	if !cNoPtrClasses(db, (lo-dh.base)/word, (hi-lo)/word) {
		return false
	}
	var sb *cBits
	var sbase0 uintptr
	if sh, sbase, ssize, ok := cHeaderOf(s); ok {
		if s-sbase > ssize || n > ssize-(s-sbase) {
			return false
		}
		sb, sbase0 = (*cBits)(unsafe.Pointer(sh.bits)), sh.base
		slo, shi := alignDown(s, word), alignUp(s+n, word)
		if !cNoPtrClasses(sb, (slo-sbase0)/word, (shi-slo)/word) {
			return false
		}
	} else if cInData(s) {
		base, size, typ, typed, state := checkptrVar(s)
		if state != varFound || n > size-(s-base) || !typed || typ != nil && typ.Pointers() {
			return false
		}
	} else {
		return false
	}
	// The classes of the words that arrive whole.
	if wlo, whi := alignUp(d, word), alignDown(d+n, word); sb != nil && (d^s)&(word-1) == 0 && wlo < whi && !getg().m.cprobe {
		w, sw := (wlo-dh.base)/word, (s+(wlo-d)-sbase0)/word
		for words := (whi - wlo) / word; words > 0; {
			k := min(words, cClassPer-w%cClassPer)
			all := cClassesOf(k)
			from := cClassWindow(sb, sw) & all & cClassLows
			if from != 0 {
				p := db.group(w / cClassPer)
				shift := w % cClassPer * 8
				for {
					old := atomic.Load64(p)
					have := old >> shift & all
					if have&cClassHighs != 0 {
						// Claimed for a pointer meanwhile, by
						// another thread: the copy that asks.
						return false
					}
					take := from &^ (have | have>>1&cClassLows)
					if take == 0 || atomic.Cas64(p, old, old|take<<shift) {
						break
					}
				}
			}
			w, sw, words = w+k, sw+k, words-k
		}
	}
	memmove(unsafe.Pointer(d), unsafe.Pointer(s), n)
	return true
}
