// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"internal/goarch"
	"internal/runtime/sys"
	"unsafe"
)

// The variables of the stack and of the program's data
// (doc/gd/unsafe.md).
//
// An object of the heap has its bounds and its type where the checks of
// unsafe pointers find them (checkptr.go): in its span. A variable of a
// stack frame, and one of the program's data, have theirs in tables that
// the compiler and the linker write:
//
//   - a function has the variables of its frame, as a funcdata
//     (cmd/compile/internal/liveness's emitStackVars): each that a
//     pointer can be to, with its offset in the frame and its type. The
//     frame that an address of the stack is in is found by unwinding the
//     stack from the check's caller (stackVarOf);
//   - a module has the symbols of its data, sorted by address, with
//     their sizes and their types (cmd/link/internal/ld/datavars.go;
//     dataVarOf, in datavar.go).
//
// The runtime's own functions have no table, and neither does assembly:
// an address in one of their frames, as one between two variables, is in
// no variable that is known.

// A stackVarRecord is a variable of a frame, as the compiler writes it:
// the table of a function is their number, a uint32, and then as many of
// them, from the lowest address to the highest.
type stackVarRecord struct {
	// off is the variable's offset in the frame: from varp if it is
	// negative, and from argp otherwise, as a stackObjectRecord's.
	off int32
	// typ is the variable's type, as its offset in the types of the
	// function's module. Its size is the variable's.
	typ int32
}

// stackVarOf returns the variable of a frame of gp's stack that addr
// is in: where it begins, its size and its type. ok is false if addr is
// in no variable that a table has: in a frame of the runtime's or of
// assembly, or between the variables of a frame. gp is the goroutine
// that is running, on its own stack, and the frames looked at are those
// of the caller of stackVarOf and of its callers.
//
// base is an address of the stack as it is when stackVarOf returns: it
// means nothing after a call that may grow the stack. So stackVarOf
// does not grow it either, and its callers compare base with what they
// have before they call anything else.
//
//go:nosplit
func stackVarOf(gp *g, addr uintptr) (base, size uintptr, typ *_type, ok bool) {
	if gp != getg() || gp != gp.m.curg || addr < gp.stack.lo || addr >= gp.stack.hi {
		return
	}
	pc, sp := sys.GetCallerPC(), sys.GetCallerSP()
	if addr < sp {
		// Below every frame that is looked at.
		return
	}
	systemstack(func() {
		base, size, typ, ok = stackVarIn(gp, pc, sp, addr)
	})
	return
}

// stackVarIn is stackVarOf for the frames of gp's stack from the one
// whose pc and sp are given on. It runs on the system stack: gp's stack
// does not move under it.
//
//go:systemstack
func stackVarIn(gp *g, pc, sp, addr uintptr) (base, size uintptr, typ *_type, ok bool) {
	var u unwinder
	for u.initAt(pc, sp, 0, gp, unwindSilentErrors); u.valid(); u.next() {
		frame := &u.frame
		if addr < frame.fp {
			// One of the frame's own variables, or nothing: what
			// is below a frame is not a caller's.
			if addr >= frame.varp {
				return
			}
			return stackVarLookup(frame.fn, frame.varp, addr)
		}
		// An argument or a result of the frame, which is in the
		// frame of its caller.
		if addr >= frame.argp {
			if base, size, typ, ok = stackVarLookup(frame.fn, frame.argp, addr); ok {
				return
			}
		}
	}
	return
}

// stackVarLookup returns the variable of a frame of f that addr is in.
// ref is the frame's argp if addr is not below it, and its varp
// otherwise.
func stackVarLookup(f funcInfo, ref, addr uintptr) (base, size uintptr, typ *_type, ok bool) {
	p := funcdata(f, abi.FUNCDATA_StackVars)
	if p == nil {
		return
	}
	n := int(*(*uint32)(p))
	recs := unsafe.Slice((*stackVarRecord)(add(p, 4)), n)
	// The last variable that begins at addr or before it. (An offset
	// from varp is negative, and one from argp is not: the difference
	// is of the same sign as the offsets it is compared with.)
	off := int64(addr) - int64(ref)
	lo, hi := 0, n
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		if int64(recs[m].off) <= off {
			lo = m + 1
		} else {
			hi = m
		}
	}
	if lo == 0 {
		return
	}
	r := &recs[lo-1]
	if (r.off < 0) != (off < 0) {
		return
	}
	t := (*_type)(unsafe.Pointer(f.datap.types + uintptr(r.typ)))
	if uintptr(off-int64(r.off)) >= t.Size_ {
		return
	}
	return uintptr(int64(ref) + int64(r.off)), t.Size_, t, true
}

// What an address is to the tables of variables.
const (
	varNone    = iota // not of the stack that is running, nor of the data
	varUnknown        // of the stack or the data, and in no variable that is known
	varFound          // in a variable
)

// checkptrVarOf returns the variable that addr is in: where it begins,
// its size, and its type, which is nil for a variable that is bytes to
// the program (a string's, or what has no Go type), and for one whose
// type is not kept (a variable of the runtime's: checkptrVar says which
// of the two it is). It is a variable of a frame of the stack of the
// goroutine that is running (stackVarOf, which says what its caller is
// to know of base), or one of the program's data (dataVarOf). ok is
// false for any other address, and for one of the stack or the data
// that is in no variable that is known.
//
//go:nosplit
func checkptrVarOf(addr uintptr) (base, size uintptr, typ *_type, ok bool) {
	base, size, typ, _, state := checkptrVar(addr)
	return base, size, typ, state == varFound
}

// checkptrVar is checkptrVarOf, and says more: state is varFound for a
// variable, varUnknown for an address of the stack or of the data that
// is in no variable that is known, and varNone for any other address.
// typed reports whether the type of the variable is known: if it is not
// set, typ is nil and says nothing of what the variable holds.
//
// A loop that steps a pointer through a variable asks for the same one
// each time. So the P keeps the last few that were found (varCacheEnt),
// and one of those is found again without a call: for a variable of the
// stack, by the frame pointers that the frames save, which say what
// frame the address is in with two loads for each frame below it.
//
//go:nosplit
func checkptrVar(addr uintptr) (base, size uintptr, typ *_type, typed bool, state int) {
	gp := getg()
	mp := gp.m
	pp := mp.p.ptr()
	if addr < gp.stack.lo || addr >= gp.stack.hi {
		if pp == nil {
			return dataVarAt(addr)
		}
		for i := range pp.varCache {
			if e := &pp.varCache[i]; addr-e.base < e.size && e.next == 0 {
				return e.base, e.size, (*_type)(unsafe.Pointer(e.typ)), e.typed, varFound
			}
		}
		base, size, typ, typed, state = dataVarAt(addr)
		if state == varFound {
			pp.varCachePut(base, size, typ, typed, 0, 0, 0)
		}
		return
	}
	if gp != mp.curg {
		// The system's stack, which no checked code runs on.
		return 0, 0, nil, false, varNone
	}
	pc, sp := sys.GetCallerPC(), sys.GetCallerSP()
	if addr < sp {
		// Below every frame that is looked at.
		return 0, 0, nil, false, varUnknown
	}
	// The frame that addr is in, by the saved frame pointers: fr.next
	// is 0 if it is not found so.
	var fr varFrame
	if stackVarByFP {
		const word = goarch.PtrSize
		fp := getfp()
		var fpc, prev uintptr
		for fp >= gp.stack.lo && fp <= gp.stack.hi-2*word && fp&(word-1) == 0 && addr >= fp {
			next := *(*uintptr)(unsafe.Pointer(fp))
			retpc := *(*uintptr)(unsafe.Pointer(fp + word))
			if next < fp+2*word || next > gp.stack.hi-2*word {
				break
			}
			if addr < next {
				fr = varFrame{prev: prev, fp: fp, pc: fpc, next: next, retpc: retpc}
				break
			}
			prev, fp, fpc = fp, next, retpc
		}
		if fr.next != 0 && pp != nil {
			for i := range pp.varCache {
				if e := &pp.varCache[i]; addr-e.base < e.size && e.next == fr.next && e.fp == fr.fp && e.retpc == fr.retpc {
					return e.base, e.size, (*_type)(unsafe.Pointer(e.typ)), true, varFound
				}
			}
		}
	}
	var ok bool
	systemstack(func() {
		if fr.next != 0 {
			if base, size, typ, ok = stackVarFP(pp, &fr, addr); ok {
				return
			}
		}
		base, size, typ, ok = stackVarIn(gp, pc, sp, addr)
	})
	if !ok {
		return 0, 0, nil, false, varUnknown
	}
	return base, size, typ, true, varFound
}

// stackVarByFP reports whether the frame that an address of the stack is
// in is looked for by the frame pointers that the frames save, before
// the stack is unwound for it. This is for amd64, where a frame pointer
// is the address of the saved one, which the return address follows, and
// then the frame's arguments; the frame's variables are below it.
const stackVarByFP = GOARCH == "amd64"

// A varFrame is where an address of the stack is by the saved frame
// pointers: at fp or after it, and below next, which is the frame
// pointer that the frame of fp saved, its caller's. So it is an argument
// of the frame of fp, which goes on at pc, or a variable of its caller,
// which goes on at retpc. prev is the frame pointer of the callee of the
// frame of fp.
type varFrame struct {
	prev, fp, pc uintptr
	next, retpc  uintptr
}

// A varCacheEnt is a variable that was looked up: one of the data, if
// next is 0, and otherwise one of the frame whose frame pointer is next,
// which goes on at retpc and whose callee's frame pointer is fp. That
// says what function the frame is of, and so what its variables are,
// for as long as the three are found again: a stack that moves has
// other frame pointers.
type varCacheEnt struct {
	base, size      uintptr
	typ             uintptr // a *_type of the program's types, which the collector need not see
	typed           bool
	fp, next, retpc uintptr
}

// varCachePut keeps a variable in pp's cache, in place of the oldest.
//
//go:nosplit
func (pp *p) varCachePut(base, size uintptr, typ *_type, typed bool, fp, next, retpc uintptr) {
	e := &pp.varCache[pp.varCacheNext%uint8(len(pp.varCache))]
	e.base, e.size, e.typ, e.typed = base, size, uintptr(unsafe.Pointer(typ)), typed
	e.fp, e.next, e.retpc = fp, next, retpc
	pp.varCacheNext++
}

// stackVarFP is stackVarIn for an address whose frame was found by the
// saved frame pointers, without unwinding.
//
// A frame that saves no frame pointer is not seen that way, and what is
// taken for its caller's is then another frame's: a function that calls
// nothing has none, and one that faults is "called" by sigpanic all the
// same. So what is found is kept only if the frame is where the
// function's frame size says it is, from its callee's; and if nothing is
// found ok is false, and the caller unwinds.
//
//go:systemstack
func stackVarFP(pp *p, fr *varFrame, addr uintptr) (base, size uintptr, typ *_type, ok bool) {
	const word = goarch.PtrSize
	// A variable of the caller.
	if f := findfunc(fr.retpc); f.valid() {
		if base, size, typ, ok = stackVarLookup(f, fr.next, addr); ok {
			if fr.fp != fr.next-word-uintptr(funcspdelta(f, fr.retpc)) {
				return 0, 0, nil, false
			}
			if pp != nil {
				pp.varCachePut(base, size, typ, true, fr.fp, fr.next, fr.retpc)
			}
			return
		}
	}
	// An argument of the frame.
	if argp := fr.fp + 2*word; addr >= argp && fr.pc != 0 {
		if f := findfunc(fr.pc); f.valid() {
			if base, size, typ, ok = stackVarLookup(f, argp, addr); ok && fr.prev != fr.fp-word-uintptr(funcspdelta(f, fr.pc)) {
				return 0, 0, nil, false
			}
		}
	}
	return
}
