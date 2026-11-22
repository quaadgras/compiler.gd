// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package unsafeheader contains header declarations for the Go runtime's slice
// and string implementations.
//
// This package allows packages that cannot import "reflect" to use types that
// are tested to be equivalent to reflect.SliceHeader and reflect.StringHeader.
package unsafeheader

import (
	"unsafe"
)

// Slice is the runtime representation of a slice.
// It cannot be used safely or portably and its representation may
// change in a later release.
//
// Unlike reflect.SliceHeader, its Data field is sufficient to guarantee the
// data it references will not be garbage collected.
type Slice struct {
	Data unsafe.Pointer
	Len  int
	Cap  int
}

// ReadSlice returns the pointer, the length and the capacity of the slice
// at p, of any element type. A slice in memory is not a Slice: with
// 8-byte words its length and capacity are 32-bit fields of one word, or
// it is the address of a header (doc/gd/slice16.md).
//
//go:nocheckptr
func ReadSlice(p unsafe.Pointer) Slice {
	s := *(*[]byte)(p)
	return Slice{unsafe.Pointer(unsafe.SliceData(s)), len(s), cap(s)}
}

// WriteSlice stores the slice s at p, of any element type.
//
//go:nocheckptr
func WriteSlice(p unsafe.Pointer, s Slice) {
	*(*[]byte)(p) = unsafe.Slice((*byte)(s.Data), s.Cap)[:s.Len]
}
