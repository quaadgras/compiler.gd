// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"reflect"
	"unsafe"
)

// What Go that imports "C" calls, as cmd/cc rewrites it (cmd/cc's
// rewrite.go).

// Arg returns the C pointer for p, a Go pointer passed to C: a capability
// for the value its type says it points to (or the whole of C's object, if
// it points into one). An unsafe.Pointer has a capability only into C's
// objects; an integer has none.
func Arg[P any](p P) *Void {
	t := reflect.TypeFor[P]()
	switch t.Kind() {
	case reflect.Pointer, reflect.UnsafePointer:
		return (*Void)(*(*unsafe.Pointer)(unsafe.Pointer(&p)))
	case reflect.Uintptr:
		return IntPtr(*(*uintptr)(unsafe.Pointer(&p)))
	}
	panic("c23.Arg: not a pointer: " + t.String())
}

// whole array.
func FromSliceAt[T any](s []T, i int) *Void {
	if i < 0 || i >= len(s) {
		_ = s[i] // Go's own bounds check
	}
	var z T
	return Add(FromSlice(s), i*int(unsafe.Sizeof(z)))
}

// CStringGo is C.CString: s copied into C's memory.
func CStringGo(s string) *int8 { return ToGo[int8](CString(s)) }

// GoStringGo is C.GoString.
func GoStringGo[P any](p P) string { return GoString(Arg(p)) }

// FreeGo is C.free: Free, for which Go's heap is never what malloc
// returned, though an unsafe.Pointer into it has no capability.
func FreeGo[P any](p P) { Free(Arg(p)) }

// MallocGo is C.malloc.
func MallocGo[N ~int | ~int32 | ~int64 | ~uint | ~uint32 | ~uint64 | ~uintptr](n N) unsafe.Pointer {
	return ToGoUnsafe(Malloc(uint64(n)))
}
