// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

// Package maps is the runtime's map: with GOWASM=gc it is in package
// runtime (runtime/wasmgc/map.go), over the engine's objects, and this
// package only says how wide a hash is, for hash/maphash.
package maps

import "internal/goarch"

// Use64BitHash reports whether hashes are 64 bits: as a uintptr is.
const Use64BitHash = goarch.PtrSize == 8
