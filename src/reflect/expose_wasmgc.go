// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package reflect

import "unsafe"

// With GOWASM=gc an integer is not made a pointer to an object again.
func exposeptr(p unsafe.Pointer) {}
