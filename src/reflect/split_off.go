// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !wasm.split

package reflect

// wasmSplitMode is set with GOWASM=gc,split (see split_on.go).
const wasmSplitMode = false
