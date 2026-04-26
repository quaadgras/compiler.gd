// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typebits

import (
	"cmd/compile/internal/bitvec"
	"cmd/compile/internal/fatal"
	"cmd/compile/internal/types"
)

// NOTE: The bitmap for a specific type t could be cached in t after
// the first run and then simply copied into bv at the correct offset
// on future calls with the same type t.
func Set(t *types.Type, off int64, bv bitvec.BitVec) {
	set(t, off, bv, false)
}

// SetNoCheck is like Set, but do not check for alignment.
func SetNoCheck(t *types.Type, off int64, bv bitvec.BitVec) {
	set(t, off, bv, true)
}

func set(t *types.Type, off int64, bv bitvec.BitVec, skip bool) {
	if !skip && uint8(t.Alignment()) > 0 && off&int64(uint8(t.Alignment())-1) != 0 {
		fatal.Error("typebits.Set: invalid initial alignment: type %v has alignment %d, but offset is %v", t, uint8(t.Alignment()), off)
	}
	if !t.HasPointers() {
		// Note: this case ensures that pointers to not-in-heap types
		// are not considered pointers by garbage collection and stack copying.
		return
	}

	switch t.Kind() {
	case types.TPTR, types.TUNSAFEPTR, types.TFUNC, types.TCHAN, types.TMAP:
		if off&int64(types.PtrSize-1) != 0 {
			fatal.Error("typebits.Set: invalid alignment, %v", t)
		}
		bv.Set(int32(off / int64(types.PtrSize))) // pointer

	case types.TSTRING:
		// struct { byte *str; intgo len; }
		if off&int64(types.PtrSize-1) != 0 {
			fatal.Error("typebits.Set: invalid alignment, %v", t)
		}
		bv.Set(int32(off / int64(types.PtrSize))) //pointer in first slot

	case types.TINTER:
		// Under gd's fat-interface layout:
		//   struct { Itab *tab; void *data; complex128 inline; }
		// or, when isnilinter(t)==true:
		//   struct { Type *type; void *data; complex128 inline; }
		//
		// The first word (tab/_type) is a pointer but never scanned — it
		// points into persistentalloc (itab) or into rodata / heap-held
		// reflect-allocated types, which are kept live by the central
		// reflect map. The inline slot is typed complex128 (16 raw bytes,
		// carried in two float registers) and is never scanned because
		// TFlagInlineIface eligibility forbids pointers in the payload.
		// Only the data word needs a pointer bit; boxed types hold a live
		// heap pointer there, inline types hold nil.
		if off&int64(types.PtrSize-1) != 0 {
			fatal.Error("typebits.Set: invalid alignment, %v", t)
		}
		bv.Set(int32(off/int64(types.PtrSize) + 1)) // pointer in second slot (data)

	case types.TSLICE:
		// struct { byte *array; uintgo len; uintgo cap; }
		if off&int64(types.PtrSize-1) != 0 {
			fatal.Error("typebits.Set: invalid TARRAY alignment, %v", t)
		}
		bv.Set(int32(off / int64(types.PtrSize))) // pointer in first slot (BitsPointer)

	case types.TARRAY:
		elt := t.Elem()
		if elt.Size() == 0 {
			// Short-circuit for #20739.
			break
		}
		for i := int64(0); i < t.NumElem(); i++ {
			set(elt, off, bv, skip)
			off += elt.Size()
		}

	case types.TSTRUCT:
		for _, f := range t.Fields() {
			set(f.Type, off+f.Offset, bv, skip)
		}

	default:
		fatal.Error("typebits.Set: unexpected type, %v", t)
	}
}
