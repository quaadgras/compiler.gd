// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasip1 && !wasm.split

package syscall

import "unsafe"

// hostBytes is for GOWASM=gc,split: see hostbytes_wasip1_split.go.
func hostBytes(p unsafe.Pointer, n int) {}
