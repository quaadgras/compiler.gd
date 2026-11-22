// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package abi

import "internal/goarch"

// SlicePacked reports whether a slice is two words (doc/gd/slice16.md):
// with 8-byte words but on wasm, its pointer and a word of its length
// and capacity as uint32s (the length first in memory). A slice whose
// capacity is SliceHugeCap or more is huge: its pointer is the address
// of an immutable header {ptr, len, cap} and its length and capacity are
// SliceHugeMarkerLen and SliceHugeMarkerCap.
const SlicePacked = goarch.PtrSize == 8 && goarch.IsWasm == 0

// SliceHugeCap is the least capacity of a huge slice (SlicePacked).
const SliceHugeCap = 1<<32 - 1

// The length and capacity fields of a huge slice.
const (
	SliceHugeMarkerLen = 0
	SliceHugeMarkerCap = 0xFFFFFFFF
)
