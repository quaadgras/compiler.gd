// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import "unsafe"

// C's types, by the names C gives them, as the Go types cmd/cc
// translates them to: a C object of one of them is a Go variable of it.
// They are aliases, so that C's long long and Go's int64 are one type to
// Go, as cmd/cc's Go takes them to be. The types whose size is the data
// model's (long, and size_t and the others that follow it) are with the
// model (Long, in types_64.go and its siblings).
type (
	Bool      = uint8 // _Bool, and C++'s bool: 0 or 1
	Char      = int8  // char is signed in cmd/cc's C
	SChar     = int8
	UChar     = uint8
	Short     = int16
	UShort    = uint16
	Int       = int32
	UInt      = uint32
	LongLong  = int64
	ULongLong = uint64

	Float  = float32
	Double = float64
	// LongDouble is long double in memory: 16 bytes that hold a double
	// (LDouble). Its value in a Go variable is a Double.
	LongDouble = LDouble

	ComplexFloat  = complex64
	ComplexDouble = complex128

	Char8   = uint8  // char8_t
	Char16  = uint16 // char16_t
	Char32  = uint32 // char32_t
	Wchar   = int32  // wchar_t
	Intmax  = int64  // intmax_t
	Uintmax = uint64 // uintmax_t

	// Nullptr is nullptr_t.
	Nullptr = unsafe.Pointer
)

// Void is what a void * points to: nothing that can be loaded or
// stored. A void * is a *Void, and so is any pointer where C's library,
// or a call through a pointer to a function, does not say what it points
// to. It is converted to and from the pointer to what is there through
// unsafe.Pointer, which is where the compiler checks it
// (doc/gd/unsafe.md).
type Void [0]byte

// Incomplete is the one field of a struct that stands, in a file of C
// that Go compiles by itself (cmd/cc's units), for a class that the
// file has only declared: the type is the one of the file that defines
// the class, which this file has pointers to and nothing else.
type Incomplete struct{}
