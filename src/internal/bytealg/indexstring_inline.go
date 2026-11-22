// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build loong64 || s390x || ppc64le || ppc64

package bytealg

// IndexString returns the index of the first instance of b in a, or -1 if b is not present in a.
// Requires 2 <= len(b) <= MaxLen.
//
// The target's assembly reads a string as its address and its length:
// an inline string (doc/gd/sso-string.md) is copied to the frame first.
func IndexString(a, b string) int {
	var ab, bb [16]byte
	return Index(stringBytes(&a, &ab), stringBytes(&b, &bb))
}
