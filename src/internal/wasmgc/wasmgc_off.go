// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !wasm.gc

package wasmgc

// Enabled reports whether the program is built for GOWASM=gc.
const Enabled = false
