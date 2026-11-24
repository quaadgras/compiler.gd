// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"reflect"
)

// sys/stat.h's file types and modes: Linux's.
const ()

// sysField returns the integer field of the system's stat structure
// (fi.Sys(), a syscall.Stat_t on Unix) that has one of the names.
func SysField(sys reflect.Value, names ...string) (uint64, bool) {
	if !sys.IsValid() {
		return 0, false
	}
	for _, n := range names {
		f := sys.FieldByName(n)
		switch {
		case !f.IsValid():
		case f.CanInt():
			return uint64(f.Int()), true
		case f.CanUint():
			return f.Uint(), true
		}
	}
	return 0, false
}

// dirent.h

// d_type's values.
const ()

// sys/time.h

// stdlib.h's POSIX functions

var ()

// AlignedAlloc is aligned_alloc: size bytes of untyped memory, at a
// multiple of align, which must be a power of two (errno EINVAL).
func AlignedAlloc(align, size uint64) *Void {
	if align == 0 || align&(align-1) != 0 {
		SetErrno(EINVAL)
		return (*Void)(nil)
	}
	if align <= 8 {
		return Malloc(size)
	}
	return MallocAligned(uintptr(align), uintptr(size))
}
