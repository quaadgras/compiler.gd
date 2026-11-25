// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc && !wasm.split

package runtime

import "unsafe"

// splitMode is set with GOWASM=gc,split: see split.go.
const splitMode = false

// What split.go defines, for the code under splitMode, which the
// compiler leaves out here.

func splitalloc(n, nslots uintptr) unsafe.Pointer  { panic("split") }
func ptroff(p unsafe.Pointer) uintptr              { panic("split") }
func ptrlim(p unsafe.Pointer) uintptr              { panic("split") }
func c23ObjectFreed(addr uintptr)                  {}
func splitcheck(p unsafe.Pointer, n uintptr)       {}
func splitcheckw(p unsafe.Pointer, n uintptr)      {}
func splitbase(p unsafe.Pointer) uintptr           { panic("split") }
func newarray(t *_type, n int) unsafe.Pointer      { panic("split") }
func splitmove(dst, src unsafe.Pointer, n uintptr) { panic("split") }
func splitzero(p unsafe.Pointer, n uintptr)        { panic("split") }
func splitslots(n uintptr) uintptr                 { panic("split") }

// typeWord is the word of an interface that holds its type or its
// table: an integer in memory.
type typeWord = uintptr

func typeWordOf(p unsafe.Pointer) typeWord  { return uintptr(p) }
func typeWordPtr(w typeWord) unsafe.Pointer { return unsafe.Pointer(w) }

func staticptr(addr uintptr) unsafe.Pointer { return unsafe.Pointer(addr) }
