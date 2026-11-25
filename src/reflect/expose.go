// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !wasm.gc

package reflect

import "unsafe"

// exposeptr marks what p points into as something whose address the
// program has as an integer (runtime/checkptr.go).
//
//go:linkname exposeptr runtime.exposeptr
func exposeptr(p unsafe.Pointer)
