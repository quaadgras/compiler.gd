// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package stdio_ext is stdio_ext.h's functions for cmd/cc: what a
// stream's buffer holds and its mode, from internal/c23's File.
package stdio_ext

import (
	c23 "internal/c23"
)

type Ptr = c23.Ptr

const FSETLOCKING_INTERNAL = 1

func b2i(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

// Fbufsize is __fbufsize: the size of the stream's buffer (1 if it is
// unbuffered, as glibc's).
func Fbufsize(p Ptr) uint64 {
	f := c23.FileOf(p)
	if f.Unbuf {
		return 1
	}
	if f.R != nil {
		return uint64(f.R.Size())
	}
	return uint64(f.W.Size())
}

// Fpending is __fpending: the bytes written to the buffer and not yet to
// the file.
func Fpending(p Ptr) uint64 { return uint64(c23.FileOf(p).W.Buffered()) }

// Flbf is __flbf: streams are fully buffered or unbuffered, never line
// buffered.
func Flbf(p Ptr) int32 {
	c23.FileOf(p)
	return 0
}

func Freadable(p Ptr) int32 { return b2i(c23.FileOf(p).Rd) }

func Fwritable(p Ptr) int32 { return b2i(c23.FileOf(p).Wr) }

// Freading is __freading: whether the stream is read-only or its last
// operation was a read.
func Freading(p Ptr) int32 {
	f := c23.FileOf(p)
	return b2i(f.Rd && !f.Wr || f.R != nil || len(f.Unget) > 0)
}

// Fwriting is __fwriting: whether the stream is write-only or its last
// operation was a write (its buffer holds what was written).
func Fwriting(p Ptr) int32 {
	f := c23.FileOf(p)
	return b2i(f.Wr && !f.Rd || f.R == nil && f.W.Buffered() > 0)
}

// Fsetlocking is __fsetlocking: the library locks for each call, which a
// program cannot change.
func Fsetlocking(p Ptr, typ int32) int32 {
	c23.FileOf(p)
	return FSETLOCKING_INTERNAL
}

// Flushlbf is _flushlbf: it flushes every stream, which includes the
// line-buffered ones.
func Flushlbf() { c23.Fflush(Ptr{}) }

// Fpurge is __fpurge: it drops what the buffer holds, written or read
// ahead, and what ungetc pushed back.
func Fpurge(p Ptr) {
	f := c23.FileOf(p)
	f.W.Reset(c23.FdIO{F: f})
	f.R, f.Unget = nil, nil
}
