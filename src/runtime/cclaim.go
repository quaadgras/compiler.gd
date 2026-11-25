// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/goarch"
	"internal/runtime/atomic"
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

// cClaimStats counts what the claims of C's memory found, for
// GODEBUG=cclaims=1, which prints them when the program exits.
var cClaimStats struct {
	claims   atomic.Uint64 // words claimed for the first time
	reclaims atomic.Uint64 // words claimed again for something that is not compatible
	narrowed atomic.Uint64 // words that held a pointer to anything, claimed for a typed pointer
	forged   atomic.Uint64 // words claimed for a pointer that held something else than a pointer, and not 0
	bytes    atomic.Uint64 // words claimed for a pointer that bytes were written in
}

// cClaimReport prints cClaimStats.
func cClaimReport() {
	if debug.cclaims == 0 || !cSpansUsed {
		return
	}
	st := &cClaimStats
	print("c claims: ", st.claims.Load(), " words claimed, ", st.reclaims.Load(), " claimed again, ",
		st.narrowed.Load(), " untyped pointers typed, ", st.forged.Load(), " pointers of what was none, ",
		st.bytes.Load(), " pointers written over with bytes\n")
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

// cWant returns the class, and the id if it is cTyped, that a word is
// claimed for by a type whose leaf there is (kind, leaf): compatLeaf's.
func cWant(kind uint8, leaf *_type) (uint32, uint32) {
	switch kind {
	case compatScalar:
		return cScalar, 0
	case compatPtr, compatFunc, compatMap, compatChan:
		if id := cTypeID(leaf); id != 0 {
			return cTyped, id
		}
	}
	// An unsafe.Pointer, and the pointers of a string, a slice or an
	// interface of Go's, which C's memory is not for: only that they
	// are pointers is kept.
	return cUntyped, 0
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
		if cClass(b, w+i) == cScalar {
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
// has pointers.
func cClaim(s *mspan, i, p uintptr, elem *_type, n uintptr, load bool) {
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
		} else if w%cClassPer+k <= cClassPer {
			mask := uint32(uint64(1)<<(2*k)-1) << (w % cClassPer * 2)
			if atomic.Load(b.word(w/cClassPer))&mask == 0x55555555*cScalar&mask {
				return
			}
		}
	}
	aligned := p&(word-1) == 0 && elem.Pointers()
	var mask *byte
	if aligned {
		mask = getGCMask(elem)
	}
	for a := lo; a < hi; a += word {
		w := (a - sbase) / word
		want, id := uint32(cScalar), uint32(0)
		if aligned {
			if off := (a - p) % elem.Size_; compatMaskBit(elem, mask, off) {
				k, leaf, _ := compatLeaf(elem, off)
				want, id = cWant(k, leaf)
			}
		}
		have := cClass(b, w)
		switch {
		case have == want:
			if want != cTyped {
				continue
			}
			if ids := s.cIDsOf(); ids != nil {
				if old := atomic.Load(ids.word(w)); old == id || cCompatIDs(old, id) {
					continue
				}
			}
		case want == cScalar && load:
			// What is loaded as an integer, whatever it is: a
			// load makes nothing of the word.
			continue
		case have == cUnclaimed:
			cClaimStats.claims.Add(1)
			cClaimWord(s, b, w, a, have, want, id)
			continue
		case want == cUntyped && have == cTyped:
			// An unsafe.Pointer over a pointer of a type.
			continue
		case want == cTyped && have == cUntyped:
			cClaimStats.narrowed.Add(1)
			cClaimWord(s, b, w, a, have, want, id)
			continue
		}
		cReclaim(s, b, w, a, have, want, id, elem)
	}
}

// cCompatIDs reports whether a pointer of the type that id stands for
// may be over one of the type that old stands for: checkptr_compat.go's
// rule for the words of a view.
func cCompatIDs(old, id uint32) bool {
	if old == 0 {
		return false
	}
	mt, vt := cTypeOf(old), cTypeOf(id)
	mk, _, _ := compatLeaf(mt, 0)
	vk, _, _ := compatLeaf(vt, 0)
	var seen compatSeen
	return compatLeaves(mk, mt, 0, vk, vt, 0, true, &seen) == compatOK
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
		if have < cUntyped && v != 0 {
			// What was written there as bytes, or as an integer,
			// is not a pointer that the program was given. (To be
			// cPoison: see the top of this file.)
			if have == cUnclaimed {
				cClaimStats.bytes.Add(1)
			} else {
				cClaimStats.forged.Add(1)
			}
		}
	}
	// The collector sees what the word holds, whichever way its
	// class goes: it looked at it and no longer will, or it will from
	// here on.
	cShadeWord(v)
	cClassSet(b, w, 1, want)
	releasem(mp)
}

// cReclaim claims the w'th word of s again, for a conversion to elem
// that is not compatible with what it was claimed for.
func cReclaim(s *mspan, b *cBits, w, a uintptr, have, want, id uint32, elem *_type) {
	if n := cClaimStats.reclaims.Add(1); debug.cclaims > 1 && n <= 64 {
		print("c claim: word ", w, " at ", hex(a), " of class ", have)
		if have == cTyped {
			if ids := s.cIDsOf(); ids != nil {
				if old := atomic.Load(ids.word(w)); old != 0 {
					print(" (", toRType(cTypeOf(old)).string(), ")")
				}
			}
		}
		print(" claimed for class ", want)
		if want == cTyped {
			print(" (", toRType(cTypeOf(id)).string(), ")")
		}
		print(" by a conversion to *", toRType(elem).string(), "\n")
	}
	cClaimWord(s, b, w, a, have, want, id)
}

// cConvMax is the size of the largest type that compiled code converts
// a pointer to with no more than c23Header's word for it: what is that
// far from the end of the stack, the data or the bss is in it whole.
// (cmd/compile/internal/ssa's CConvMax.)
const cConvMax = 4096

// cNoBits is the classes that c23Header gives for what is not C's.
var cNoBits uint32

// c23Header returns what does not change of where p points for as long
// as what it points into is alive, for the checks that compiled code
// makes of the pointers that are made of p
// (cmd/compile/internal/ssa/cheaders.go): it is looked for once for p,
// before a loop if p is from before it, and each check is then a
// comparison in the compiled code, with the call it was for what the
// comparison does not let through.
//
// A pointer q made of p is into what p points into, or to its end, if
//
//	uintptr(q) - uintptr(p) + d <= n
//
// which is so of nothing else. That is the object of the heap that p is
// in, as far as it was asked for if it is one of C's; the goroutine's
// stack from p on; or the section of data or bss that p is in. (The numbers
// are relative to p, which the compiled code has: a stack that moves
// takes p with it, and they stay as they are.) Of anything else, only
// p itself is.
//
// A q that is converted to a pointer to a type of cConvMax bytes or
// less, for which
//
//	uintptr(q) - uintptr(p) + d < nc
//
// is in an object of C's or at its end, or what the type covers is in
// the stack, the data or the bss: bytes need no other check than that
// of their alignment (checkptrView). nc is 0 for an object of Go's,
// which has a type to look at.
//
// If p is in an object of C's, sh is 3, and the class of the word that
// q is at (cClass: what the word was claimed for) is the two bits from
// bit 2*(w%16) of the uint32 at bits + w/16*4, where
//
//	w = (uintptr(q) - uintptr(p) + d + b8) >> sh
//
// A conversion to a type of one word needs no more than that the word
// is claimed for what the type has there. For what is not C's, sh is
// 63, and there are no claims to look at.
//
//go:nosplit
func c23Header(p unsafe.Pointer) (d, n, nc, b8, sh, bits uintptr) {
	sh, bits = 63, uintptr(unsafe.Pointer(&cNoBits))
	a := uintptr(p)
	if gp := getg(); gp.stack.lo <= a && a < gp.stack.hi {
		n = gp.stack.hi - a - 1
		return 0, n, max(n+1, cConvMax) - cConvMax, 0, sh, bits
	}
	if s := spanOfHeap(a); s != nil {
		if s.isUserArenaChunk {
			return
		}
		i := s.objIndex(a)
		base := s.base() + i*s.elemsize
		if !s.spanclass.c() {
			return a - base, s.elemsize - 1, 0, 0, sh, bits
		}
		b := s.cBitsOf()
		if b == nil {
			return
		}
		size := s.cSizeOf(i)
		return a - base, size, size + 1, base - s.base(), 3, uintptr(unsafe.Pointer(b))
	}
	for _, datap := range activeModules() {
		for _, r := range [...][2]uintptr{
			{datap.data, datap.edata}, {datap.bss, datap.ebss},
			{datap.noptrdata, datap.enoptrdata}, {datap.noptrbss, datap.enoptrbss},
		} {
			if r[0] <= a && a < r[1] {
				n = r[1] - r[0] - 1
				return a - r[0], n, max(n+1, cConvMax) - cConvMax, 0, sh, bits
			}
		}
	}
	return
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

// c23New and c23TypedMove are for the box of a union's member
// (internal/c23's UnionClone): a new object of typ, and a copy of one.
//
//go:linkname c23New
func c23New(typ *_type) unsafe.Pointer { return mallocgc(typ.Size_, typ, true) }

//go:linkname c23TypedMove
func c23TypedMove(typ *_type, dst, src unsafe.Pointer) { typedmemmove(typ, dst, src) }

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
	// The whole values of typ that fit in size, and no more: what is
	// left of the slot is anything.
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
			if compatMaskBit(typ, mask, f*word) {
				k, leaf, _ := compatLeaf(typ, f*word)
				class, id = cWant(k, leaf)
			}
			if f < uintptr(len(first)) {
				first[f].class, first[f].id = class, id
			}
		} else {
			class, id = first[f].class, first[f].id
		}
		if class == cTyped {
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
	publicationBarrier()
	return x
}

// c23Memmove is memmove for C: the words of it that may be whole
// pointers are copied as pointers are, with the collector's barrier, and
// the bytes before and after them as bytes. A word of C's that it
// copies to whole, which nothing claimed, is claimed for what the word
// it is copied from was, if that is one of C's: an object that is
// copied keeps what it is. A word that was claimed stays as it is.
//
//go:linkname c23Memmove
//go:nosplit
func c23Memmove(dst, src unsafe.Pointer, n uintptr) {
	if dst == src || n == 0 {
		return
	}
	const word = goarch.PtrSize
	d, sr := uintptr(dst), uintptr(src)
	lo, hi := alignDown(d, word), alignUp(d+n, word)
	wlo, whi := alignUp(d, word), alignDown(d+n, word)
	aligned := (d^sr)&(word-1) == 0
	ds := cSpanOf(d)
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
	ss := spanOfHeap(sw)
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
	var ids *cBits
	for done := uintptr(0); done < words; {
		// The words of one uint32 of the destination's classes.
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
		all := uint32(uint64(1)<<(2*k) - 1)
		have := atomic.Load(b.word((w+i)/cClassPer)) >> ((w + i) % cClassPer * 2) & all
		// free has both bits of the words that nothing claimed.
		free := ^(have | have>>1) & 0x55555555 & all * 3
		if free != 0 {
			var from uint32 // the classes of the words copied from
			switch {
			case sb != nil:
				v := uint64(atomic.Load(sb.word((sw0 + i) / cClassPer)))
				if (sw0+i)%cClassPer+k > cClassPer {
					v |= uint64(atomic.Load(sb.word((sw0+i)/cClassPer+1))) << 32
				}
				from = uint32(v>>((sw0+i)%cClassPer*2)) & all
			case ss != nil && ss.spanclass.noscan():
				from = 0x55555555 * cScalar & all
			}
			if take := from & free; take != 0 {
				// The types of the pointers first.
				for j := uintptr(0); j < k; j++ {
					if take>>(2*j)&3 != cTyped {
						continue
					}
					if ids == nil {
						ids = ds.cIDsOf()
					}
					if ids == nil || sids == nil {
						// (Not here: this cannot allocate.)
						take &^= 1 << (2 * j) // cUntyped
						continue
					}
					atomic.Store(ids.word(w+i+j), atomic.Load(sids.word(sw0+i+j)))
				}
				// (Only where nothing claimed the word
				// meanwhile.)
				p, sh := b.word((w+i)/cClassPer), (w+i)%cClassPer*2
				for {
					old := atomic.Load(p)
					now := old >> sh & all
					t := take & (^(now | now>>1) & 0x55555555 & all * 3)
					if t == 0 || atomic.Cas(p, old, old|t<<sh) {
						have = now | t
						break
					}
				}
			}
		}
		if writeBarrier.enabled && have != 0x55555555*cScalar&all {
			for j := uintptr(0); j < k; j++ {
				if have>>(2*j)&3 != cScalar {
					cShadeWord(*(*uintptr)(unsafe.Pointer(sw + (i+j)*word)))
				}
			}
		}
	}
	memmove(dst, src, n)
}

// c23Memset is memset for C: the pointers that it writes over are the
// collector's to see first. It claims nothing: what it writes is bytes.
//
//go:linkname c23Memset
//go:nosplit
func c23Memset(dst unsafe.Pointer, c byte, n uintptr) {
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
