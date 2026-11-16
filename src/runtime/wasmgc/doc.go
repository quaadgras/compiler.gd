// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

// Package runtime, in this directory, is the runtime for GOWASM=gc: Go
// objects in the WebAssembly garbage-collected heap, which the engine
// manages. There is no allocator, no collector and no stack to grow
// here; see doc/gd/wasm-gc.md.
//
// It is far from complete. The functions the compiler calls must keep
// the signatures declared in cmd/compile/internal/typecheck/_builtin,
// except those that take a type descriptor to allocate with, which the
// compiler (cmd/compile/internal/wasm/gc.go) replaces by an allocation
// of the WebAssembly type or by a call to one of the functions here
// that are named after the kind of element: b, h, w and l for integers
// of 8 to 64 bits, f and d for floats, r for references.
package runtime
