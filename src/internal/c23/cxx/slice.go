// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package cxx is what cmd/cc's C++ library headers are made of that Go
// has already: a std::vector<T>'s elements are a []T, and so are a
// std::string's bytes and a std::deque's. The headers are C++, which
// constructs, copies and destroys the elements as C++ says; the memory
// they are in is Go's, with Go's type, made and grown here.
//
// A function of this package is called from the headers by the name
// __go_ and its own (cmd/cc's funcName), with the C++ type
// std::__go_slice<T> for a []T.
package cxx

import (
	"internal/c23"
	"unsafe"
)

// A slice that holds elements has one more than its capacity says: the
// address of its end, which C++ makes an iterator of and compares with,
// is then in the array, and is the address of nothing else.

// SliceLen is len(*s).
func SliceLen[T any](s *[]T) c23.ULong { return c23.ULong(len(*s)) }

// SliceCap is the number of elements that *s has room for.
func SliceCap[T any](s *[]T) c23.ULong {
	if c := cap(*s); c > 0 {
		return c23.ULong(c - 1)
	}
	return 0
}

// SliceData is the address of the first element, nil if there is no
// array.
func SliceData[T any](s *[]T) *T { return unsafe.SliceData(*s) }

// SliceEnd is the address after the last element.
func SliceEnd[T any](s *[]T) *T {
	p := unsafe.SliceData(*s)
	if p == nil {
		return nil
	}
	return (*T)(unsafe.Add(unsafe.Pointer(p), uintptr(len(*s))*unsafe.Sizeof(*p)))
}

// SliceAt is &(*s)[i], which Go checks.
func SliceAt[T any](s *[]T, i c23.ULong) *T { return &(*s)[i] }

// SliceReset makes *s the slice of nothing: what a constructor starts
// with, in memory that held anything, and what a destructor leaves.
func SliceReset[T any](s *[]T) { *s = nil }

// SliceRealloc gives *s a new array with room for c elements, all zero,
// and its length again. It returns the old array's first element: the
// caller moves the elements over.
func SliceRealloc[T any](s *[]T, c c23.ULong) *T {
	old := unsafe.SliceData(*s)
	n := len(*s)
	if uint64(c) < uint64(n) {
		c = c23.ULong(n)
	}
	*s = make([]T, n, int(c)+1)
	return old
}

// SliceSetLen makes the length n, which the array has room for. The
// elements that it drops are zeroed (the caller destroyed them): the
// collector does not see what they pointed to; those that it adds are
// zero, for the caller to construct.
func SliceSetLen[T any](s *[]T, n c23.ULong) {
	if uint64(n) >= uint64(cap(*s)) && n != 0 {
		panic("cxx: a length that the array has no room for")
	}
	old := len(*s)
	if int(n) < old {
		clear((*s)[n:old])
	}
	*s = (*s)[:n]
}

// SliceSwap exchanges two slices.
func SliceSwap[T any](a, b *[]T) { *a, *b = *b, *a }

// SliceTake moves src's array to dst, whose own is dropped, and leaves
// src with none.
func SliceTake[T any](dst, src *[]T) {
	if dst != src {
		*dst, *src = *src, nil
	}
}
