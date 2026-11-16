// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !wasm.gc

package chacha8rand

// block is the chacha8rand block function.
func block(seed *[4]uint64, blocks *[32]uint64, counter uint32)
