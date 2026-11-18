// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

// streqmixed reports whether s and t are equal, for strings of either
// representation (gd's 3-word strings), as a leaf with no frame
// (asm_arm64.s): the compiler's OpStrEqFast calls it on arm64, where
// a string compare is an op that writes no memory, for the case of an
// inline string and a heap string of 15 bytes or fewer.
func streqmixed(s, t string) bool
