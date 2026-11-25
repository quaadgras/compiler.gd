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
func splitbase(p unsafe.Pointer) uintptr           { panic("split") }
func newarray(t *_type, n int) unsafe.Pointer      { panic("split") }
func splitmove(dst, src unsafe.Pointer, n uintptr) { panic("split") }
func splitzero(p unsafe.Pointer, n uintptr)        { panic("split") }
func splitslots(n uintptr) uintptr                 { panic("split") }
