// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !arm64

package runtime

import "internal/abi"

// strhashPort returns the fork's aeshash of s using the x86-style
// round function (AESENC). Matches runtime.aeshashbody on amd64/386
// and is used as the go-fallback on any other non-arm64 arch.
func strhashPort(s string) uint64 {
	return abi.AeshashString(s)
}
