// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types2

import "internal/buildcfg"

type gcSizes struct {
	WordSize int64 // word size in bytes - must be >= 4 (32bits)
	MaxAlign int64 // maximum alignment in bytes - must be >= 1

	// gd: a string is four words, not three, with the 4-byte words
	// of wasm (its inline form: cmd/compile/internal/types's
	// StringWasm32).
	stringWords int64 // the words of a string: 2, but 3 with GOWASM=gc and 4 with its 4-byte words
	stringAlign int64 // the alignment of a string if not WordSize: 16 with 8-byte words but on wasm
	slicePacked bool  // a slice is two words, with 8-byte words but on wasm (cmd/compile/internal/types's SlicePacked)
}

func (s *gcSizes) Alignof(T Type) (result int64) {
	defer func() {
		assert(result >= 1)
	}()

	// For arrays and structs, alignment is defined in terms
	// of alignment of the elements and fields, respectively.
	switch t := T.Underlying().(type) {
	case *Array:
		// spec: "For a variable x of array type: unsafe.Alignof(x)
		// is the same as unsafe.Alignof(x[0]), but at least 1."
		return s.Alignof(t.elem)
	case *Struct:
		if len(t.fields) == 0 && IsSyncAtomicAlign64(T) {
			// Special case: sync/atomic.align64 is an
			// empty struct we recognize as a signal that
			// the struct it contains must be
			// 64-bit-aligned.
			//
			// This logic is equivalent to the logic in
			// cmd/compile/internal/types/size.go:calcStructOffset
			return 8
		}

		// spec: "For a variable x of struct type: unsafe.Alignof(x)
		// is the largest of the values unsafe.Alignof(x.f) for each
		// field f of x, but at least 1."
		max := int64(1)
		for _, f := range t.fields {
			if a := s.Alignof(f.typ); a > max {
				max = a
			}
		}
		return max
	case *Slice, *Interface:
		// Multiword data structures are effectively structs
		// in which each element has size WordSize.
		// Type parameters lead to variable sizes/alignments;
		// StdSizes.Alignof won't be called for them.
		assert(!isTypeParam(T))
		if _, ok := t.(*Interface); ok && s.WordSize < 8 {
			// gd: an interface ends in a complex128 (its inline
			// payload), and is aligned as that is, as
			// cmd/compile/internal/types aligns it: to 8 on wasm32
			// with its 4-byte words, to 4 on the other 32-bit targets.
			return min(8, s.MaxAlign)
		}
		if _, ok := t.(*Slice); ok && s.slicePacked {
			// gd: aligned to 16 where packed, so that a slice is
			// loaded and stored with one 16-byte access
			// (cmd/compile/internal/types's SliceAligned16).
			return 16
		}
		return s.WordSize
	case *Basic:
		// Strings are like slices and interfaces.
		if t.Info()&IsString != 0 {
			if s.stringAlign != 0 {
				// gd: aligned to 16 with 8-byte words, so that a
				// string is loaded and stored with one 16-byte access
				// (cmd/compile/internal/types's StringAligned16).
				return s.stringAlign
			}
			return s.WordSize
		}
	case *TypeParam, *Union:
		panic("unreachable")
	}
	a := s.Sizeof(T) // may be 0 or negative
	// spec: "For a variable x of any type: unsafe.Alignof(x) is at least 1."
	if a < 1 {
		return 1
	}
	// complex{64,128} are aligned like [2]float{32,64}.
	if isComplex(T) {
		a /= 2
	}
	if a > s.MaxAlign {
		return s.MaxAlign
	}
	return a
}

func (s *gcSizes) Offsetsof(fields []*Var) []int64 {
	offsets := make([]int64, len(fields))
	var offs int64
	for i, f := range fields {
		if offs < 0 {
			// all remaining offsets are too large
			offsets[i] = -1
			continue
		}
		// offs >= 0
		a := s.Alignof(f.typ)
		offs = align(offs, a) // possibly < 0 if align overflows
		offsets[i] = offs
		if d := s.Sizeof(f.typ); d >= 0 && offs >= 0 {
			offs += d // ok to overflow to < 0
		} else {
			offs = -1 // f.typ or offs is too large
		}
	}
	return offsets
}

func (s *gcSizes) Sizeof(T Type) int64 {
	switch t := T.Underlying().(type) {
	case *Basic:
		assert(isTyped(T))
		k := t.kind
		if int(k) < len(basicSizes) {
			if s := basicSizes[k]; s > 0 {
				return int64(s)
			}
		}
		if k == String {
			// gd: two words but with GOWASM=gc (doc/gd/sso-string.md).
			if s.stringWords != 0 {
				return s.WordSize * s.stringWords
			}
			return s.WordSize * 2
		}
	case *Array:
		n := t.len
		if n <= 0 {
			return 0
		}
		// n > 0
		esize := s.Sizeof(t.elem)
		if esize < 0 {
			return -1 // element too large
		}
		if esize == 0 {
			return 0 // 0-size element
		}
		// esize > 0
		// Final size is esize * n; and size must be <= maxInt64.
		const maxInt64 = 1<<63 - 1
		if esize > maxInt64/n {
			return -1 // esize * n overflows
		}
		return esize * n
	case *Slice:
		if s.slicePacked {
			// gd: its pointer, and its length and capacity as
			// uint32s (doc/gd/slice16.md).
			return 16
		}
		return s.WordSize * 3
	case *Struct:
		n := t.NumFields()
		if n == 0 {
			return 0
		}
		offsets := s.Offsetsof(t.fields)
		offs := offsets[n-1]
		size := s.Sizeof(t.fields[n-1].typ)
		if offs < 0 || size < 0 {
			return -1 // type too large
		}
		// gc: The last field of a non-zero-sized struct is not allowed to
		// have size 0.
		if offs > 0 && size == 0 {
			size = 1
		}
		// gc: Size includes alignment padding.
		return align(offs+size, s.Alignof(t)) // may overflow to < 0 which is ok
	case *Interface:
		// Type parameters lead to variable sizes/alignments;
		// StdSizes.Sizeof won't be called for them.
		assert(!isTypeParam(T))
		// gd fat-interface: { tab/_type, data, inline complex128 } = 2 words + 16 bytes.
		return s.WordSize*2 + 16
	case *TypeParam, *Union:
		panic("unreachable")
	}
	return s.WordSize // catch-all
}

// gcSizesFor returns the Sizes used by gc for an architecture.
// The result is a nil *gcSizes pointer (which is not a valid types.Sizes)
// if a compiler/architecture pair is not known.
func gcSizesFor(compiler, arch string) *gcSizes {
	if compiler != "gc" && compiler != "gd" { // gd: these sizes are the fork's layouts
		return nil
	}
	if arch == "wasm" {
		// gd: the word of GOARCH=wasm depends on GOWASM (4 bytes for
		// the GC port, 8 for Go's own); see buildcfg's PtrSize.
		ps := int64(buildcfg.GOWASM.PtrSize())
		words := int64(2)
		switch {
		case ps == 4:
			words = 4
		case buildcfg.GOWASM.GC:
			words = 3
		}
		return &gcSizes{WordSize: ps, MaxAlign: 8, stringWords: words}
	}
	return gcArchSizes[arch]
}
