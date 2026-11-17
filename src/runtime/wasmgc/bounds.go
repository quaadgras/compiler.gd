// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

import "internal/abi"

// Index and slice bounds errors, as in ../error.go and ../panic.go.

// A boundsError represents an indexing or slicing operation gone wrong.
type boundsError struct {
	x      int64
	y      int
	signed bool
	code   abi.BoundsErrorCode
}

var boundsErrorFmts = [...]string{
	abi.BoundsIndex:      "index out of range [%x] with length %y",
	abi.BoundsSliceAlen:  "slice bounds out of range [:%x] with length %y",
	abi.BoundsSliceAcap:  "slice bounds out of range [:%x] with capacity %y",
	abi.BoundsSliceB:     "slice bounds out of range [%x:%y]",
	abi.BoundsSlice3Alen: "slice bounds out of range [::%x] with length %y",
	abi.BoundsSlice3Acap: "slice bounds out of range [::%x] with capacity %y",
	abi.BoundsSlice3B:    "slice bounds out of range [:%x:%y]",
	abi.BoundsSlice3C:    "slice bounds out of range [%x:%y:]",
	abi.BoundsConvert:    "cannot convert slice with length %y to array or pointer to array with length %x",
}

var boundsNegErrorFmts = [...]string{
	abi.BoundsIndex:      "index out of range [%x]",
	abi.BoundsSliceAlen:  "slice bounds out of range [:%x]",
	abi.BoundsSliceAcap:  "slice bounds out of range [:%x]",
	abi.BoundsSliceB:     "slice bounds out of range [%x:]",
	abi.BoundsSlice3Alen: "slice bounds out of range [::%x]",
	abi.BoundsSlice3Acap: "slice bounds out of range [::%x]",
	abi.BoundsSlice3B:    "slice bounds out of range [:%x:]",
	abi.BoundsSlice3C:    "slice bounds out of range [%x::]",
}

func (e boundsError) RuntimeError() {}

func (e boundsError) Error() string {
	fmt := boundsErrorFmts[e.code]
	if e.signed && e.x < 0 {
		fmt = boundsNegErrorFmts[e.code]
	}
	s := "runtime error: "
	for i := 0; i < len(fmt); i++ {
		c := fmt[i]
		if c != '%' {
			s += string(rune(c))
			continue
		}
		i++
		switch fmt[i] {
		case 'x':
			if e.signed {
				s += itoa(e.x)
			} else {
				s += utoa(uint64(e.x))
			}
		case 'y':
			s += itoa(int64(e.y))
		}
	}
	return s
}

func utoa(v uint64) string {
	if v <= 1<<63-1 {
		return itoa(int64(v))
	}
	return utoa(v/10) + string(rune('0'+v%10))
}

func goPanicIndex(x int, y int) {
	panic(boundsError{x: int64(x), signed: true, y: y, code: abi.BoundsIndex})
}
func goPanicIndexU(x uint, y int) {
	panic(boundsError{x: int64(x), signed: false, y: y, code: abi.BoundsIndex})
}
func goPanicSliceAlen(x int, y int) {
	panic(boundsError{x: int64(x), signed: true, y: y, code: abi.BoundsSliceAlen})
}
func goPanicSliceAlenU(x uint, y int) {
	panic(boundsError{x: int64(x), signed: false, y: y, code: abi.BoundsSliceAlen})
}
func goPanicSliceAcap(x int, y int) {
	panic(boundsError{x: int64(x), signed: true, y: y, code: abi.BoundsSliceAcap})
}
func goPanicSliceAcapU(x uint, y int) {
	panic(boundsError{x: int64(x), signed: false, y: y, code: abi.BoundsSliceAcap})
}
func goPanicSliceB(x int, y int) {
	panic(boundsError{x: int64(x), signed: true, y: y, code: abi.BoundsSliceB})
}
func goPanicSliceBU(x uint, y int) {
	panic(boundsError{x: int64(x), signed: false, y: y, code: abi.BoundsSliceB})
}
func goPanicSlice3Alen(x int, y int) {
	panic(boundsError{x: int64(x), signed: true, y: y, code: abi.BoundsSlice3Alen})
}
func goPanicSlice3AlenU(x uint, y int) {
	panic(boundsError{x: int64(x), signed: false, y: y, code: abi.BoundsSlice3Alen})
}
func goPanicSlice3Acap(x int, y int) {
	panic(boundsError{x: int64(x), signed: true, y: y, code: abi.BoundsSlice3Acap})
}
func goPanicSlice3AcapU(x uint, y int) {
	panic(boundsError{x: int64(x), signed: false, y: y, code: abi.BoundsSlice3Acap})
}
func goPanicSlice3B(x int, y int) {
	panic(boundsError{x: int64(x), signed: true, y: y, code: abi.BoundsSlice3B})
}
func goPanicSlice3BU(x uint, y int) {
	panic(boundsError{x: int64(x), signed: false, y: y, code: abi.BoundsSlice3B})
}
func goPanicSlice3C(x int, y int) {
	panic(boundsError{x: int64(x), signed: true, y: y, code: abi.BoundsSlice3C})
}
func goPanicSlice3CU(x uint, y int) {
	panic(boundsError{x: int64(x), signed: false, y: y, code: abi.BoundsSlice3C})
}
func goPanicSliceConvert(x int, y int) {
	panic(boundsError{x: int64(x), signed: true, y: y, code: abi.BoundsConvert})
}

// goPanicExtendWide panics for an index of 64 bits that an int of 32
// does not hold (wasm32: the compiler's extendIndexWide).
func goPanicExtendWide(x int64, signed bool, code abi.BoundsErrorCode, y int) {
	panic(boundsError{x: x, signed: signed, y: y, code: code})
}
