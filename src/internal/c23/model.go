// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"math"
	"unsafe"
)

// The data model of the C that cmd/cc compiles for this target (cmd/cc's
// target.go): LP64, or ILP32 on wasm32 (GOWASM=gc,split, the tool tag
// wasm.ptr32), where a pointer word and C's long are 4 bytes.
const (
	// wordSize is the size of a pointer word in memory: the unit in which
	// a tinfo says what memory holds and the side tables keep
	// capabilities.
	wordSize = unsafe.Sizeof(uintptr(0))

	// longSize is the size of C's long (and size_t, ptrdiff_t), which is
	// a word in both models cmd/cc has.
	longSize = unsafe.Sizeof(uintptr(0))
)

// loadWord and storeWord load and store a word as a scalar.
func loadWord(p Ptr) uintptr     { return *(*uintptr)(check(p, wordSize)) }
func storeWord(p Ptr, v uintptr) { *(*uintptr)(checkScalar(p, wordSize)) = v }

// loadLong and storeLong load and store a C long.
func loadLong(p Ptr) int64 {
	if longSize == 4 {
		return int64(int32(Load32(p)))
	}
	return int64(Load64(p))
}

func storeLong(p Ptr, v int64) {
	if longSize == 4 {
		Store32(p, uint32(v))
		return
	}
	Store64(p, uint64(v))
}

// Strtol32 is strtol where long is 32 bits (cmd/cc's libcLong32): its
// range is long's.
func Strtol32(s, end Ptr, base int32) int64 {
	v, neg, of, n := strtoParse(GoString(s), int(base))
	setEnd(end, s, n)
	switch {
	case neg && (of || v > 1<<31):
		SetErrno(ERANGE)
		return math.MinInt32
	case !neg && (of || v > math.MaxInt32):
		SetErrno(ERANGE)
		return math.MaxInt32
	case neg:
		return -int64(v)
	}
	return int64(v)
}

// Strtoul32 is strtoul where unsigned long is 32 bits.
func Strtoul32(s, end Ptr, base int32) uint64 {
	v, neg, of, n := strtoParse(GoString(s), int(base))
	setEnd(end, s, n)
	if of || v > math.MaxUint32 {
		SetErrno(ERANGE)
		return math.MaxUint32
	}
	if neg {
		return uint64(-uint32(v))
	}
	return v
}

// Ldiv32 is ldiv where long is 32 bits: the ldiv_t it writes is two of
// them.
func Ldiv32(sret Ptr, n, d int64) {
	storeLong(sret, n/d)
	storeLong(Add(sret, int(longSize)), n%d)
}
