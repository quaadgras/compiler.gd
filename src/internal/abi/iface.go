// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package abi

import (
	"internal/goarch"
	"unsafe"
)

// The first word of every non-empty interface type contains an *ITab.
// It records the underlying concrete type (Type), the interface type it
// is implementing (Inter), and some ancillary information.
//
// allocated in non-garbage-collected memory
type ITab struct {
	Inter  *InterfaceType
	Type   *Type
	Hash   uint32 // copy of Type.Hash. Used for type switches.
	Inline uint8  // gd: dispatch mode — see ITabInline* constants
	_      [3]byte
	Fun    [1]uintptr // variable sized. fun[0]==0 means Type does not implement Inter.
	// gd escape-bits: an unsafe-addressable array of uint64 follows
	// immediately after the Fun tail, with one entry per interface
	// method. EscMask(k) returns the k-th entry. Storage is reserved
	// by every ITab allocator (runtime persistentalloc, compiler
	// static emission, linker fixups) and initialised to zero, which
	// means "every pointer arg of method k escapes" — the same
	// pessimistic answer the compiler uses today for unknown callees.
	// Phase D will source real values from the concrete method's
	// escape profile.
}

// Size returns the size of the itab in memory, including the gd
// escape-mask tail. This must match the sizes used by every ITab
// allocator (runtime getitab's persistentalloc, the compiler's static
// emission, and the linker's .itab layout), because the runtime walks
// the module's itab section with it.
func (it *ITab) Size() int {
	if it.Fun[0] == 0 {
		// Type does not implement Inter: the compiler emits a single
		// zero Fun slot plus one escape-mask word (see
		// reflectdata.writeITab), regardless of Inter's method count.
		return ITabSize(goarch.PtrSize, 1)
	}
	return ITabSize(goarch.PtrSize, len(it.Inter.Methods))
}

// ITabEscMaskOff returns the byte offset of EscMask[k] relative to the
// start of the ITab, given the interface's method count and the
// target's pointer size. The masks sit immediately after the variable-
// length Fun array.
func ITabEscMaskOff(ptrSize, nmethods, k int) int {
	// Before Fun: Inter + Type (2 pointers) + Hash(4) + Inline(1) +
	// pad[3] = 2*ptrSize + 8.
	base := 2*ptrSize + 8
	// Fun is [nmethods]uintptr. Masks follow.
	return base + nmethods*ptrSize + k*8
}

// ITabSize returns the total size of an ITab carrying nmethods methods
// with the mask tail, for a target with the given pointer size.
func ITabSize(ptrSize, nmethods int) int {
	base := 2*ptrSize + 8
	return base + nmethods*ptrSize + nmethods*8
}

// Values for ITab.Inline, selecting how getClosureAndRcvr stages the
// receiver for an interface method call.
const (
	ITabInlineBoxed  uint8 = 0 // receiver = iface.Data (heap-boxed)
	ITabInlineInline uint8 = 1 // receiver = &stage16; stage16 ← iface.Inline
	ITabInlineSpread uint8 = 2 // receiver = &stage24; stage24 ← {iface.Data, iface.Inline}
)

// Under the gd fork's fat-interface layout an interface header carries a
// fixed 16-byte inline payload regardless of pointer size:
//
//	offset  0: tab/_type  (never scanned — rodata or persistentalloc)
//	offset  8: data       (scanned — boxed heap pointer, or nil when inline)
//	offset 16: inline[0]  (never scanned — inline payload or unused)
//	offset 24: inline[1]  (never scanned — inline payload or unused)
//
// Sizes: 32 bytes on 64-bit, 24 bytes on 32-bit (pointer-sized tab+data,
// fixed-width uint64 inline). Types satisfying TFlagInlineIface (pointer-
// free, size <= 16, align <= 8) store their value in Inline with Data ==
// nil. Everything else keeps the boxed representation with Inline zero.

// Inline is typed complex128 (not [2]uint64) so the ABIInternal register
// allocator carries it in two float registers instead of spilling the whole
// header to the stack — arrays of length > 1 are never register-passed
// (see cmd/compile/internal/types/size.go:CalcArraySize). The field holds
// 16 raw bytes; reinterpret via unsafe when Phase B starts storing actual
// payload bits.

// EmptyInterface describes the layout of a "interface{}" or a "any."
// These are represented differently than non-empty interface, as the first
// word always points to an abi.Type.
type EmptyInterface struct {
	Type   *Type
	Data   unsafe.Pointer
	Inline complex128
}

// NonEmptyInterface describes the layout of an interface that contains any methods.
type NonEmptyInterface struct {
	ITab   *ITab
	Data   unsafe.Pointer
	Inline complex128
}

// CommonInterface describes the layout of both [EmptyInterface] and [NonEmptyInterface].
type CommonInterface struct {
	// Either an *ITab or a *Type, unexported to avoid accidental use.
	_ unsafe.Pointer

	Data   unsafe.Pointer
	Inline complex128
}
