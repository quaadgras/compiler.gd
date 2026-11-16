// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

// Package wasmgc reports whether the program is built for GOWASM=gc,
// where Go's stack frames are WebAssembly's and the engine's stack
// does not grow: code that bounds recursion bounds it lower there.
package wasmgc

// Enabled reports whether the program is built for GOWASM=gc.
const Enabled = true
