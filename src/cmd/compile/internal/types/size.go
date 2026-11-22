// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"fmt"
	"math"
	"slices"
	"sync"

	"cmd/compile/internal/base"
	"cmd/compile/internal/fatal"
	"cmd/internal/src"
	"internal/buildcfg"
	"internal/types/errors"
)

var PtrSize int

var RegSize int

// Slices in the runtime are represented by three components:
//
//	type slice struct {
//		ptr unsafe.Pointer
//		len int
//		cap int
//	}
//
// Strings in the runtime are represented by two components:
//
//	type string struct {
//		ptr unsafe.Pointer
//		len int
//	}
//
// These variables are the offsets of fields and sizes of these structs.
var (
	SlicePtrOffset int64
	SliceLenOffset int64
	SliceCapOffset int64

	SliceSize int64

	// The words of a string (see StringTwoWords). StringHashOffset is
	// the offset of the second value of a string in SSA when it has three
	// (GOWASM=gc), else StringLenOffset.
	StringPtrOffset  int64
	StringHashOffset int64
	StringLenOffset  int64
	StringSize       int64

	// StringInlineCap is the longest string kept in its header: 15
	// with 8-byte words and with the 4-byte words of wasm32
	// (StringWasm32), 0 on the other 32-bit targets, which have no
	// inline form (and stock Go's strings).
	StringInlineCap int64

	// FuncMaskOffset is the offset of the escape mask M of a function
	// value, and ClosureVarsOffset that of the first captured variable
	// (see InitTypes).
	FuncMaskOffset    int64
	ClosureVarsOffset int64
)

var SkipSizeForTracing bool

// StringTwoWords reports whether a string is two words, as in stock Go:
// on every target but GOARCH=wasm with GOWASM=gc. With 8-byte words
// (StringTagged) a string of 1 to 15 bytes is kept in them; with 4-byte
// words (386, arm, mips) a string is stock Go's, the address of its
// bytes and its length.
func StringTwoWords() bool {
	return !(buildcfg.GOARCH == "wasm" && buildcfg.GOWASM.GC)
}

// StringTagged reports whether a string has the tagged form of the
// targets with 8-byte words (doc/gd/sso-string.md): 16 bytes, words w0
// and w1.
//
//	heap:   w0 = the address of the bytes (nil for ""), w1 = the length.
//	inline: w0 = 1<<63 | n<<56 | bytes 8..14, w1 = bytes 0..7, of a
//	        string of n = 1..15 bytes, the unused bytes zero.
//
// Byte i of an inline string is bits 8i to 8i+7 of w1 (i < 8) or of w0
// (8 <= i < 15, as i-8), as if the 16 bytes w1, w0 were loaded little-
// endian; on a big-endian target they are where a big-endian load of
// the bytes would put them, w0's below the tag (StringTaggedWords).
// Bit 63 of w0 tells the forms apart: a canonical address never has it,
// so word 0, which the collector scans, is never in the heap when the
// string is inline.
func StringTagged() bool {
	return PtrSize == 8 && StringTwoWords()
}

// StringTaggedWords returns the words w0 and w1 of an inline string of
// 1 to 15 bytes, s (StringTagged): w1 bytes 0..7, w0 1<<63, the length
// at bit 56 and bytes 8..14, as little-endian loads would have them
// (big-endian loads with bigEndian, w0's bytes below the tag).
func StringTaggedWords(s string, bigEndian bool) (w0, w1 int64) {
	var b0, b1 [8]byte
	copy(b1[:], s)
	if len(s) > 8 {
		copy(b0[:], s[8:])
	}
	var u0, u1 uint64
	for i := 7; i >= 0; i-- {
		if bigEndian {
			u0 |= uint64(b0[i]) << (56 - 8*i)
			u1 |= uint64(b1[i]) << (56 - 8*i)
		} else {
			u0 |= uint64(b0[i]) << (8 * i)
			u1 |= uint64(b1[i]) << (8 * i)
		}
	}
	if bigEndian {
		u0 >>= 8
	}
	return int64(u0&(1<<56-1) | 1<<63 | uint64(len(s))<<56), int64(u1)
}

// StringAligned16 reports whether a string is aligned to 16 bytes, so
// that it is loaded and stored with one 16-byte access, tear-free
// (doc/gd/sso-string.md): on the targets with 8-byte words (StringTagged)
// but GOARCH=wasm, whose goroutines do not run in parallel.
func StringAligned16() bool {
	return StringTagged() && buildcfg.GOARCH != "wasm"
}

// SlicePacked reports whether a slice is two words, 16 bytes
// (doc/gd/slice16.md): on the targets with 8-byte words but GOARCH=wasm.
//
//	w0 @0   the address of the array, or of a huge slice's header
//	w1 @8   len (uint32 @8) and cap (uint32 @12)
//
// A slice whose capacity is SliceHugeCap or more is huge: w0 is the
// address of an immutable header {ptr, len, cap} of three words in the
// heap, and w1 the marker, len 0 and cap 0xFFFFFFFF, which no other
// slice has. In SSA a slice is three values as in stock Go, its length
// and capacity full words: it is packed where it is stored, or passed in
// registers (OpSlicePack, OpSlicePackW1), and unpacked where it is loaded
// (OpSliceUnpackPtr, Len and Cap).
func SlicePacked() bool {
	return PtrSize == 8 && buildcfg.GOARCH != "wasm"
}

// SliceHugeCap is the least capacity of a huge slice (SlicePacked).
const SliceHugeCap = 1<<32 - 1

// StackAlignment is the alignment of a value of type t in a stack frame,
// which is aligned to a register: t's, but a register's for a string,
// whose alignment to 16 (StringAligned16) is for a 16-byte access that is
// one access, which a stack, where no other goroutine accesses it, does
// not need. (Such a type can be in the stack, not too aligned for it.)
func StackAlignment(t *Type) int64 {
	a := t.Alignment()
	if a <= int64(RegSize) || !StringAligned16() || overAligned(t) {
		return a
	}
	return int64(RegSize)
}

// overAligned reports whether t has a part aligned beyond a register's
// that is not a string.
func overAligned(t *Type) bool {
	if t.Alignment() <= int64(RegSize) {
		return false
	}
	switch {
	case t.IsString():
		return false
	case t.IsStruct():
		for _, f := range t.Fields() {
			if overAligned(f.Type) {
				return true
			}
		}
		return false
	case t.IsArray():
		return overAligned(t.Elem())
	}
	return true
}

// StringWasmGC64 reports whether a string has the form of GOWASM=gc
// with 8-byte words: three words, the address of the bytes (nil if
// inline), the cached hash or bytes 0..7, and the length with a tag of
// 0 or the tag (the length) in the top 4 bits over bytes 8..14.
func StringWasmGC64() bool {
	return PtrSize == 8 && !StringTwoWords()
}

// StringWasm32 reports whether a string has the form of wasm32
// (GOWASM=gc,split with 4-byte words; doc/gd/sso-string.md, stage 16
// of doc/gd/wasm-gc.md): 16 bytes, aligned to 4, seen as two uint64s,
// A at offset 0 and B at offset 8. A string of 1 to StringInlineCap
// (15) bytes is kept in them: its bytes at offsets 0 to 14, the rest
// zero, and its length in the top 4 bits of B (the tag, as with 8-byte
// words, whose words 1 and 2 are A and B). Any other string has the
// address of its bytes in word 0, its length in word 1, word 2 for the
// cached hash and word 3 zero (a tag of 0).
//
// In SSA a string is three values: word 0 as a pointer (nil for an
// inline string), A and B. A and the pointer overlap: the pointer is
// stored (OpStringStorePtr, which writes the word and its element of
// the array of pointers) before A, which writes the word again, and
// loaded only if the tag is 0 (OpStringLoadPtr). Word 0 of an inline
// string is text, which only a target whose collector does not scan
// the memory of the program can have: WebAssembly with split, where
// what keeps the bytes of a string alive is the pointer in the element
// of its block's array, which is nil for an inline string.
func StringWasm32() bool {
	return PtrSize == 4 && buildcfg.GOARCH == "wasm"
}

// StringWord1Type and StringWord2Type are the types of the second and
// third values of a string in SSA: a uintptr (word 1) and an int (word
// 2), or two uint64s (A and B) with StringWasm32.
func StringWord1Type() *Type {
	if StringTwoWords() {
		return Types[TINT]
	}
	if StringWasm32() {
		return Types[TUINT64]
	}
	return Types[TUINTPTR]
}

func StringWord2Type() *Type {
	if StringWasm32() {
		return Types[TUINT64]
	}
	return Types[TINT]
}

// StringWord1Offset is the offset in a string of its second value in
// SSA (the third's is 2*PtrSize).
func StringWord1Offset() int64 {
	if StringWasm32() {
		return 0
	}
	return int64(PtrSize)
}

// typePos returns the position associated with t.
// This is where t was declared or where it appeared as a type expression.
func typePos(t *Type) src.XPos {
	if pos := t.Pos(); pos.IsKnown() {
		return pos
	}
	panic(fmt.Sprintf("bad type: %v", t))
}

// MaxWidth is the maximum size of a value on the target architecture.
var MaxWidth int64

// CalcSizeDisabled is per-Invocation: gd.CalcSizeDisabled. Set
// true by gd.compileFunctions during the parallel backend phase to
// catch CalcSize calls that would race on shared Type fields. The
// package-level var is kept as a no-op for legacy callers; new
// code should toggle gd.CalcSizeDisabled directly.
var CalcSizeDisabled bool

// defercalc / deferredTypeStack live on *base.Invocation
// (gd.Defercalc / gd.DeferredTypeStack). Nil-gd CalcSize/CheckSize
// callers (Type method defensive paths, ssa.CanSSA, etc.) skip
// the bracket entirely — they operate on already-calculated types
// in practice (the widthCalculated() fast path returns
// immediately), and the t.width=-2 sentinel still loudly catches
// the rare case where a nil-gd caller hits an uncalculated
// recursive type.
//
// The per-Invocation move matters because concurrent in-process
// compile invocations would each DeferCheckSize / ResumeCheckSize
// independently; with shared counters one invocation's
// ResumeCheckSize fires when defercalc hits 1 — but that 1 might
// be the OTHER invocation's bracket; types get processed at the
// wrong time, and the originating invocation's frontend exits
// without having calc'd its deferred types.

// RoundUp rounds o to a multiple of r, r is a power of 2.
func RoundUp(o int64, r int64) int64 {
	if r < 1 || r > 64 || r&(r-1) != 0 { // (a string is aligned to 16: StringTagged)
		panic(fmt.Sprintf("Round %d", r))
	}
	return (o + r - 1) &^ (r - 1)
}

// expandiface computes the method set for interface type t by
// expanding embedded interfaces.
func expandiface(gd *base.Invocation, t *Type) {
	// Defer CheckSize calls during this expansion. Without the
	// bracket, CheckSize on a direct method's signature
	// (line below) recursively CalcSize's the parameter and
	// result types — which for mutually-recursive generic
	// interfaces reaches another expandiface call before this
	// one has finished setting t.allMethods. The recursive
	// expandiface then iterates t.Methods()->m.Type.AllMethods()
	// and gets the unfinished empty slice, freezing both
	// interfaces with allMethods = []. See test/fixedbugs/
	// issue65362.go (Vector[V] embeds ReadVector[V] embeds
	// Comparisons[ReadVector[V], Vector[V]]).
	if gd != nil {
		DeferCheckSize(gd)
		defer ResumeCheckSize(gd)
	}
	seen := make(map[*Sym]*Field)
	var methods []*Field

	addMethod := func(m *Field, explicit bool) {
		switch prev := seen[m.Sym]; {
		case prev == nil:
			seen[m.Sym] = m
		case !explicit && Identical(m.Type, prev.Type):
			return
		default:
			fatal.ErrorAt(m.Pos, errors.DuplicateDecl, "duplicate method %s", m.Sym.Name)
		}
		methods = append(methods, m)
	}

	{
		methods := t.Methods()
		slices.SortStableFunc(methods, func(a, b *Field) int {
			// Sort embedded types by type name (if any).
			if a.Sym == nil && b.Sym == nil {
				return CompareSyms(a.Type.Sym(), b.Type.Sym())
			}

			// Sort methods before embedded types.
			if a.Sym == nil {
				return -1
			} else if b.Sym == nil {
				return +1
			}

			// Sort methods by symbol name.
			return CompareSyms(a.Sym, b.Sym)
		})
	}

	for _, m := range t.Methods() {
		if m.Sym == nil {
			continue
		}

		CheckSize(gd, m.Type)
		addMethod(m, true)
	}

	for _, m := range t.Methods() {
		if m.Sym != nil || m.Type == nil {
			continue
		}

		// In 1.18, embedded types can be anything. In Go 1.17, we disallow
		// embedding anything other than interfaces. This requirement was caught
		// by types2 already, so allow non-interface here.
		if !m.Type.IsInterface() {
			continue
		}

		// Embedded interface: duplicate all methods
		// and add to t's method set.
		//
		// Force m.Type's expansion through *our* gd so the inner
		// expandiface participates in our DeferCheckSize bracket.
		// Without this, m.Type.AllMethods() below would call
		// CalcSize(nil, m.Type) (see (*Type).AllMethods) and the
		// nested expandiface would run unprotected — for mutually
		// recursive generic interfaces it then iterates *our*
		// (still incomplete) allMethods, freezing m.Type with a
		// truncated method set.
		if gd != nil {
			CalcSize(gd, m.Type)
		}
		for _, t1 := range m.Type.AllMethods() {
			f := NewField(m.Pos, t1.Sym, t1.Type)
			addMethod(f, false)

			// Clear position after typechecking, for consistency with types2.
			f.Pos = src.NoXPos
		}

		// Clear position after typechecking, for consistency with types2.
		m.Pos = src.NoXPos
	}

	slices.SortFunc(methods, CompareFields)

	if int64(len(methods)) >= MaxWidth/int64(PtrSize) {
		fatal.ErrorAt(typePos(t), 0, "interface too large")
	}
	for i, m := range methods {
		m.Offset = int64(i) * int64(PtrSize)
	}

	t.SetAllMethods(methods)
}

// calcStructOffset computes the offsets of a sequence of fields,
// starting at the given offset. It returns the resulting offset and
// maximum field alignment.
func calcStructOffset(gd *base.Invocation, t *Type, fields []*Field, offset int64) int64 {
	for _, f := range fields {
		CalcSize(gd, f.Type)
		offset = RoundUp(offset, int64(f.Type.align))

		if t.IsStruct() { // param offsets depend on ABI
			f.Offset = offset

			// If type T contains a field F marked as not-in-heap,
			// then T must also be a not-in-heap type. Otherwise,
			// you could heap allocate T and then get a pointer F,
			// which would be a heap pointer to a not-in-heap type.
			if f.Type.NotInHeap() {
				t.SetNotInHeap(true)
			}
		}

		offset += f.Type.width

		maxwidth := MaxWidth
		// On 32-bit systems, reflect tables impose an additional constraint
		// that each field start offset must fit in 31 bits.
		if maxwidth < 1<<32 {
			maxwidth = 1<<31 - 1
		}
		if offset >= maxwidth {
			fatal.ErrorAt(typePos(t), 0, "type %L too large", t)
			offset = 8 // small but nonzero
		}
	}

	return offset
}

func isAtomicStdPkg(p *Pkg) bool {
	if p.Prefix == `""` {
		panic("bad package prefix")
	}
	return p.Prefix == "sync/atomic" || p.Prefix == "internal/runtime/atomic"
}

// Coordination for concurrent CalcSize on the same shared Type
// (Types[...] universe types, etc.). Earlier this was a
// map[*Type]*sync.Once which leaked — every Type ever passed to
// CalcSize stayed alive as a map key for the lifetime of the
// process, so a long-running cmd/go (in-process compile across
// many packages in one build) saw live heap growing roughly
// linearly with package count. Switched to inline coordination
// via the Type.width sentinel: CAS 0→-2 to claim, spin until
// widthCalculated() for everyone else.
//
// Reentrant CalcSize calls (CheckSize → CalcSize on the same Type
// in the same goroutine — happens for legitimate cycles like
// chan/chanargs with gd=nil, where CheckSize doesn't defer) bypass
// the CAS via calcSizeReenter so the goroutine doesn't deadlock
// waiting on its own in-progress mark.
var (
	calcSizeInProgressMu sync.Mutex
	calcSizeInProgress   = map[int64]map[*Type]struct{}{}
)

// calcSizeReenter reports whether the current goroutine is already
// computing CalcSize for t. If false, it records t as in-progress and
// returns a release func the caller must defer.
func calcSizeReenter(t *Type) (reenter bool, release func()) {
	gid := calcSizeGoroutineID()
	calcSizeInProgressMu.Lock()
	defer calcSizeInProgressMu.Unlock()
	if _, ok := calcSizeInProgress[gid][t]; ok {
		return true, nil
	}
	set := calcSizeInProgress[gid]
	if set == nil {
		set = map[*Type]struct{}{}
		calcSizeInProgress[gid] = set
	}
	set[t] = struct{}{}
	return false, func() {
		calcSizeInProgressMu.Lock()
		defer calcSizeInProgressMu.Unlock()
		delete(calcSizeInProgress[gid], t)
		if len(calcSizeInProgress[gid]) == 0 {
			delete(calcSizeInProgress, gid)
		}
	}
}

// CalcSize calculates and stores the size, alignment, eq/hash algorithm,
// and ptrBytes for t.
// If CalcSizeDisabled is set, and the size/alignment
// have not already been calculated, it calls Fatal.
// This is used to prevent data races in the back end.
func CalcSize(gd *base.Invocation, t *Type) {
	// Calling CalcSize when typecheck tracing enabled is not safe.
	// See issue #33658.
	if base.EnableTrace && SkipSizeForTracing {
		return
	}
	if PtrSize == 0 {
		// Assume this is a test.
		return
	}

	if t == nil {
		return
	}

	// Fast path: if the type is already fully calculated, no work
	// to do.
	if t.widthCalculated() {
		return
	}

	// Recursive same-goroutine call (e.g. TCHANARGS calcSizeBody
	// triggers CalcSize on the source TCHAN type while that TCHAN's
	// CalcSize is still on the goroutine's call stack — happens with
	// gd=nil because CheckSize takes the no-defer path). The outer
	// calcSizeBody handles the cycle itself (legitimate cycles
	// complete; illegal ones fatal via the width=-2 sentinel).
	reenter, release := calcSizeReenter(t)
	if reenter {
		return
	}
	defer release()

	// Per-Type sync.Once stored on the Type itself (no global map,
	// so the Type is GC-eligible once nothing else references it —
	// fixes the unbounded RSS growth a process-global
	// map[*Type]*sync.Once caused under in-process compile). Multiple
	// goroutines hitting the same shared Type serialise inside Do;
	// same-goroutine reentry is excluded by the calcSizeReenter check
	// above so we never deadlock the Once on its own goroutine.
	t.calcSizeOnce.Do(func() {
		calcSizeBody(gd, t)
	})
}

func calcSizeBody(gd *base.Invocation, t *Type) {
	if t.widthCalculated() {
		return
	}

	if t.width == -2 {
		t.width = 0
		t.align = 1
		fatal.Error("invalid recursive type %v", t)
		return
	}

	if gd != nil && gd.CalcSizeDisabled {
		fatal.Error("width not calculated: %v", t)
	}

	// defer CheckSize calls until after we're done. Nil-gd
	// callers (Type.Size/Alignment defensive paths, CanSSA, etc.)
	// skip the bracket: by the time these run, the type tree
	// has been calculated by frontend, so child CheckSize calls
	// resolve via the t.widthCalculated() fast path with no
	// recursion. The width=-2 sentinel still catches the (rare)
	// case where a nil-gd caller hits an uncalculated recursive
	// type — the resulting fatal makes the precondition violation
	// loud rather than racy.
	if gd != nil {
		DeferCheckSize(gd)
	}

	t.width = -2
	t.align = 0  // 0 means use t.Width, below
	t.alg = AMEM // default
	// default t.ptrBytes is 0.
	if t.Noalg() {
		t.setAlg(ANOALG)
	}

	et := t.Kind()
	switch et {
	case TFUNC, TCHAN, TMAP, TSTRING:
		break

	// SimType == 0 during bootstrap
	default:
		if SimType[t.Kind()] != 0 {
			et = SimType[t.Kind()]
		}
	}

	var w int64
	switch et {
	default:
		fatal.ErrorAt(t.Pos(), 0, "CalcSize: unknown type: %v", t)

	// compiler-specific stuff
	case TINT8, TUINT8, TBOOL:
		// bool is int8
		w = 1
		t.intRegs = 1

	case TINT16, TUINT16:
		w = 2
		t.intRegs = 1

	case TINT32, TUINT32:
		w = 4
		t.intRegs = 1

	case TINT64, TUINT64:
		w = 8
		t.align = uint8(RegSize)
		t.intRegs = uint8(8 / RegSize)

	case TFLOAT32:
		w = 4
		t.floatRegs = 1
		t.setAlg(AFLOAT32)

	case TFLOAT64:
		w = 8
		t.align = uint8(RegSize)
		t.floatRegs = 1
		t.setAlg(AFLOAT64)

	case TCOMPLEX64:
		w = 8
		t.align = 4
		t.floatRegs = 2
		t.setAlg(ACPLX64)

	case TCOMPLEX128:
		w = 16
		t.align = uint8(RegSize)
		t.floatRegs = 2
		t.setAlg(ACPLX128)

	case TPTR:
		w = int64(PtrSize)
		t.intRegs = 1
		CheckSize(gd, t.Elem())
		t.ptrBytes = int64(PtrSize) // See PtrDataSize

	case TUNSAFEPTR:
		w = int64(PtrSize)
		t.intRegs = 1
		t.ptrBytes = int64(PtrSize)

	case TINTER:
		// gd fat-interface layout: { tab/_type, data, inline complex128 }.
		// Size is 2*PtrSize + 16: 32 on 64-bit, 24 on 32-bit. It is
		// aligned as the complex128 is, to a register: to 8 on wasm32
		// too, to 4 on the other 32-bit targets, whose stacks are aligned
		// to no more (escape.HeapAllocReason would put every interface
		// variable in the heap).
		// ptrBytes stays at 2*PtrSize: only the tab and data words live
		// in the pointer-bitmap prefix (typebits.set marks data only;
		// the inline payload is pointer-free by construction under the
		// TFlagInlineIface eligibility rule). The ABI carries the
		// complex128 in two float registers, so TINTER needs 2 int +
		// 2 float regs — matches synthIface.
		w = 2*int64(PtrSize) + 16
		t.align = uint8(RegSize)
		t.intRegs = 2
		t.floatRegs = 2
		expandiface(gd, t)
		if len(t.allMethods.Slice()) == 0 {
			t.setAlg(ANILINTER)
		} else {
			t.setAlg(AINTER)
		}
		t.ptrBytes = int64(2 * PtrSize)

	case TCHAN: // implemented as pointer
		w = int64(PtrSize)
		t.intRegs = 1
		t.ptrBytes = int64(PtrSize)

		CheckSize(gd, t.Elem())

		// Make fake type to trigger channel element size check after
		// any top-level recursive type has been completed.
		t1 := NewChanArgs(t)
		CheckSize(gd, t1)

	case TCHANARGS:
		t1 := t.ChanArgs()
		CalcSize(gd, t1) // just in case
		// Make sure size of t1.Elem() is calculated at this point. We can
		// use CalcSize() here rather than CheckSize(), because the top-level
		// (possibly recursive) type will have been calculated before the fake
		// chanargs is handled.
		CalcSize(gd, t1.Elem())
		if t1.Elem().width >= 1<<16 {
			fatal.Error("channel element type too large (>64kB)")
		}
		w = 1 // anything will do

	case TMAP: // implemented as pointer
		w = int64(PtrSize)
		t.intRegs = 1
		CheckSize(gd, t.Elem())
		CheckSize(gd, t.Key())
		if t.Elem().width >= 1<<31 {
			gd.Errorf("map element type too large")
		}
		if t.Key().width >= 1<<31 {
			gd.Errorf("map key type too large")
		}
		t.setAlg(ANOEQ)
		t.ptrBytes = int64(PtrSize)

	case TFORW: // should have been filled in
		fatal.ErrorAt(typePos(t), 0, "invalid recursive type %v", t)

	case TANY: // not a real type; should be replaced before use.
		fatal.ErrorAt(typePos(t), 0, "CalcSize any")

	case TSTRING:
		if StringSize == 0 {
			fatal.ErrorAt(typePos(t), 0, "early CalcSize string")
		}
		w = StringSize
		t.align = uint8(PtrSize)
		if StringAligned16() {
			// Aligned to 16, so that a string is loaded and stored
			// with one 16-byte access (doc/gd/sso-string.md).
			t.align = 16
		}
		t.intRegs = 3 // gd: {word0=ptr/nil, word1=hash/bytes, word2=tag|len/bytes}
		if StringTwoWords() {
			t.intRegs = 2 // {w0, w1} (StringTagged)
		}
		t.setAlg(ASTRING)
		t.ptrBytes = int64(PtrSize) // word0 is the only pointer slot

	case TARRAY:
		if t.Elem() == nil {
			break
		}
		CalcArraySize(gd, t)
		w = t.width

	case TSLICE:
		if t.Elem() == nil {
			break
		}
		w = SliceSize
		CheckSize(gd, t.Elem())
		t.align = uint8(PtrSize)
		t.intRegs = 3
		if SlicePacked() {
			t.intRegs = 2 // {w0, w1} (SlicePacked)
		}
		t.setAlg(ANOEQ)
		if !t.Elem().NotInHeap() {
			t.ptrBytes = int64(PtrSize)
		}

	case TSTRUCT:
		if t.IsFuncArgStruct() {
			fatal.ErrorAt(typePos(t), 0, "CalcSize fn struct %v", t)
		}
		CalcStructSize(gd, t)
		w = t.width

	// make fake type to check later to
	// trigger function argument computation.
	case TFUNC:
		t1 := NewFuncArgs(t)
		CheckSize(gd, t1)
		w = int64(PtrSize) // width of func type is pointer
		t.intRegs = 1
		t.setAlg(ANOEQ)
		t.ptrBytes = int64(PtrSize)

	// function is 3 cated structures;
	// compute their widths as side-effect.
	case TFUNCARGS:
		t1 := t.FuncArgs()
		// TODO(mdempsky): Should package abi be responsible for computing argwid?
		w = calcStructOffset(gd, t1, t1.Recvs(), 0)
		w = calcStructOffset(gd, t1, t1.Params(), w)
		w = RoundUp(w, int64(RegSize))
		w = calcStructOffset(gd, t1, t1.Results(), w)
		w = RoundUp(w, int64(RegSize))
		t1.extra.(*Func).Argwid = w
		t.align = 1
	}

	if PtrSize == 4 && w != int64(int32(w)) {
		fatal.ErrorAt(typePos(t), 0, "type %v too large", t)
	}

	t.width = w
	if t.align == 0 {
		if w == 0 || w > 8 || w&(w-1) != 0 {
			fatal.ErrorAt(typePos(t), 0, "invalid alignment for %v", t)
		}
		t.align = uint8(w)
	}

	if gd != nil {
		ResumeCheckSize(gd)
	}
}

// simdify marks as type as "SIMD", either as a tag field,
// or having the SIMD attribute.  The tag field is a marker
// type used to identify a struct that is not really a struct.
// A SIMD type is allocated to a vector register (on amd64,
// xmm, ymm, or zmm).  The fields of a SIMD type are ignored
// by the compiler except for the space that they reserve.
func simdify(st *Type, isTag bool) {
	st.align = 8
	st.alg = ANOALG // not comparable with ==
	st.intRegs = 0
	st.isSIMD = true
	if isTag {
		st.width = 0
		st.isSIMDTag = true
		st.floatRegs = 0
	} else {
		st.floatRegs = 1
	}
}

// CalcStructSize calculates the size of t,
// filling in t.width, t.align, t.intRegs, and t.floatRegs,
// even if size calculation is otherwise disabled.
func CalcStructSize(gd *base.Invocation, t *Type) {
	var maxAlign uint8 = 1

	// Recognize special types. This logic is duplicated in go/types and
	// cmd/compile/internal/types2.
	if sym := t.Sym(); sym != nil {
		switch {
		case sym.Name == "align64" && isAtomicStdPkg(sym.Pkg):
			maxAlign = 8

		case buildcfg.Experiment.SIMD && (sym.Pkg.Path == "simd/archsimd") && len(t.Fields()) >= 1:
			// This gates the experiment -- without it, no user-visible types can be "simd".
			// The SSA-visible SIMD types remain.
			switch sym.Name {
			case "v128":
				simdify(t, true)
				return
			case "v256":
				simdify(t, true)
				return
			case "v512":
				simdify(t, true)
				return
			}
		}
	}

	fields := t.Fields()

	size := calcStructOffset(gd, t, fields, 0)

	// For non-zero-sized structs which end in a zero-sized field, we
	// add an extra byte of padding to the type. This padding ensures
	// that taking the address of a zero-sized field can't manufacture a
	// pointer to the next object in the heap. See issue 9401.
	if size > 0 && fields[len(fields)-1].Type.width == 0 {
		size++
	}

	var intRegs, floatRegs uint64
	for _, field := range fields {
		typ := field.Type

		// The alignment of a struct type is the maximum alignment of its
		// field types.
		if align := typ.align; align > maxAlign {
			maxAlign = align
		}

		// Each field needs its own registers.
		// We sum in uint64 to avoid possible overflows.
		intRegs += uint64(typ.intRegs)
		floatRegs += uint64(typ.floatRegs)
	}

	// Final size includes trailing padding.
	size = RoundUp(size, int64(maxAlign))

	if intRegs > math.MaxUint8 || floatRegs > math.MaxUint8 {
		intRegs = math.MaxUint8
		floatRegs = math.MaxUint8
	}

	t.width = size
	t.align = maxAlign
	t.intRegs = uint8(intRegs)
	t.floatRegs = uint8(floatRegs)

	// Compute eq/hash algorithm type.
	t.alg = AMEM // default
	if t.Noalg() {
		t.setAlg(ANOALG)
	}
	if len(fields) == 1 && !fields[0].Sym.IsBlank() {
		// One-field struct is same as that one field alone.
		t.setAlg(fields[0].Type.alg)
	} else {
		for i, f := range fields {
			a := f.Type.alg
			switch a {
			case ANOEQ, ANOALG:
			case AMEM:
				// Blank fields and padded fields need a special compare.
				if f.Sym.IsBlank() || IsPaddedField(t, i) {
					a = ASPECIAL
				}
			default:
				// Fields with non-memory equality need a special compare.
				a = ASPECIAL
			}
			t.setAlg(a)
		}
	}
	// Compute ptrBytes.
	for i := len(fields) - 1; i >= 0; i-- {
		f := fields[i]
		if size := PtrDataSize(gd, f.Type); size > 0 {
			t.ptrBytes = f.Offset + size
			break
		}
	}

	if len(t.Fields()) >= 1 && t.Fields()[0].Type.isSIMDTag {
		// this catches `type Foo simd.Whatever` -- Foo is also SIMD.
		simdify(t, false)
	}
}

// CalcArraySize calculates the size of t,
// filling in t.width, t.align, t.alg, and t.ptrBytes,
// even if size calculation is otherwise disabled.
func CalcArraySize(gd *base.Invocation, t *Type) {
	elem := t.Elem()
	n := t.NumElem()
	CalcSize(gd, elem)
	t.SetNotInHeap(elem.NotInHeap())
	if elem.width != 0 {
		cap := (uint64(MaxWidth) - 1) / uint64(elem.width)
		if uint64(n) > cap {
			fatal.Error("type %L larger than address space", t)
		}
	}

	t.width = elem.width * n
	t.align = elem.align
	// ABIInternal only allows "trivial" arrays (i.e., length 0 or 1)
	// to be passed by register.
	switch n {
	case 0:
		t.intRegs = 0
		t.floatRegs = 0
	case 1:
		t.intRegs = elem.intRegs
		t.floatRegs = elem.floatRegs
	default:
		t.intRegs = math.MaxUint8
		t.floatRegs = math.MaxUint8
		if buildcfg.GOARCH == "wasm" && buildcfg.GOWASM.GC {
			// GOWASM=gc has no stack to pass arguments on: an array
			// is passed in registers, element by element, which are
			// WebAssembly parameters and results.
			i, f := uint64(n)*uint64(elem.intRegs), uint64(n)*uint64(elem.floatRegs)
			if i < math.MaxUint8 && f < math.MaxUint8 {
				t.intRegs, t.floatRegs = uint8(i), uint8(f)
			}
		}
	}
	t.alg = AMEM // default
	if t.Noalg() {
		t.setAlg(ANOALG)
	}
	switch a := elem.alg; a {
	case AMEM, ANOEQ, ANOALG:
		t.setAlg(a)
	default:
		switch n {
		case 0:
			// We checked above that the element type is comparable.
			t.setAlg(AMEM)
		case 1:
			// Single-element array is same as its lone element.
			t.setAlg(a)
		default:
			t.setAlg(ASPECIAL)
		}
	}
	if n > 0 {
		x := PtrDataSize(gd, elem)
		if x > 0 {
			t.ptrBytes = elem.width*(n-1) + x
		}
	}
}

func (t *Type) widthCalculated() bool {
	return t.align > 0
}

// when a type's width should be known, we call CheckSize
// to compute it.  during a declaration like
//
//	type T *struct { next T }
//
// it is necessary to defer the calculation of the struct width
// until after T has been initialized to be a pointer to that struct.
// similarly, during import processing structs may be used
// before their definition.  in those situations, calling
// DeferCheckSize() stops width calculations until
// ResumeCheckSize() is called, at which point all the
// CalcSizes that were deferred are executed.
// CalcSize should only be called when the type's size
// is needed immediately.  CheckSize makes sure the
// size is evaluated eventually.

func CheckSize(gd *base.Invocation, t *Type) {
	if t == nil {
		return
	}

	// function arg structs should not be checked
	// outside of the enclosing function.
	if t.IsFuncArgStruct() {
		fatal.Error("CheckSize %v", t)
	}

	// Nil-gd path: no defer bracket; fast-CalcSize directly.
	// This is safe because nil-gd callers operate on already-
	// calculated types in practice — the CalcSize call short-
	// circuits at widthCalculated.
	if gd == nil {
		CalcSize(nil, t)
		return
	}

	if gd.Defercalc == 0 {
		CalcSize(gd, t)
		return
	}

	// if type has not yet been pushed on deferredTypeStack yet, do it now
	if !t.Deferwidth() {
		t.SetDeferwidth(true)
		gd.DeferredTypeStack = append(gd.DeferredTypeStack, t)
	}
}

func DeferCheckSize(gd *base.Invocation) {
	gd.Defercalc++
}

func ResumeCheckSize(gd *base.Invocation) {
	if gd.Defercalc == 1 {
		for len(gd.DeferredTypeStack) > 0 {
			t := gd.DeferredTypeStack[len(gd.DeferredTypeStack)-1].(*Type)
			gd.DeferredTypeStack = gd.DeferredTypeStack[:len(gd.DeferredTypeStack)-1]
			t.SetDeferwidth(false)
			CalcSize(gd, t)
		}
	}

	gd.Defercalc--
}

// PtrDataSize returns the length in bytes of the prefix of t
// containing pointer data. Anything after this offset is scalar data.
//
// PtrDataSize is only defined for actual Go types. It's an error to
// use it on compiler-internal types (e.g., TSSA, TRESULTS).
func PtrDataSize(gd *base.Invocation, t *Type) int64 {
	CalcSize(gd, t)
	x := t.ptrBytes
	if t.Kind() == TPTR && t.Elem().NotInHeap() {
		// Note: this is done here instead of when we're setting
		// the ptrBytes field, because at that time (in NewPtr, usually)
		// the NotInHeap bit of the element type might not be set yet.
		x = 0
	}
	return x
}
