// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build 386 || arm || mips || mipsle

package c23

// Tag marks a pointer word whose address is not a whole value of the
// type it points to. cmd/cc has no data model for the 32-bit targets
// but wasm32 (cmd/cc/internal/ccmain/target.go), so cmd/go builds no C
// with it there (load.ccContext), and nothing makes a tagged address
// fault or keeps the collector from taking it for a pointer: the
// package only compiles there, with the top bit as its tag.
const Tag = 1 << 31

// splitModel: GOWASM=gc,split (tag_split.go).
const splitModel = false
