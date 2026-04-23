// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build arm64

package runtime

import "internal/abi"

// strhashPort returns the fork's aeshash of s using the arm64-style
// round function (AESE+AESMC). Used when useAeshash is false and the
// runtime has to compute a hash in Go — must match what the compiler
// emitted for static string literals on this GOARCH.
func strhashPort(s string) uint64 {
	return abi.AeshashStringARM64(s)
}
