// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.split

package reflect

// wasmSplitMode is set with GOWASM=gc,split, where values are in linear
// memory as they are elsewhere (see runtime/wasmgc/split.go): what
// wasmGCMode does for the objects of GOWASM=gc does not apply.
const wasmSplitMode = true
