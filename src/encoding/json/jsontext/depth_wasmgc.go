// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.jsonv2 && wasm.gc

package jsontext

// With GOWASM=gc Go's stack frames are WebAssembly's, and the engine's
// stack does not grow (see internal/wasmgc): decoding a value into an
// any recurses about 5000 levels deep before it overflows.
const maxNestingDepth = 2000
