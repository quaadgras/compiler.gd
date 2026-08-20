// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build 386 || arm || mips || mipsle || wasm

package runtime

import "unsafe"

// strhashPort on 32-bit arches delegates to memhashFallback (wyhash)
// instead of the software aeshash port in internal/abi. The port uses
// uint64 operations whose SSA lowering on 386/arm/mips/mipsle/wasm
// produces Int64Make shapes the 32-bit lowering rules can't handle,
// so abi.AeshashString isn't compiled into these targets (see
// internal/abi/aeshash.go's build tag).
//
// The hash-cache optimisation is already off on 32-bit: staticdata
// leaves literal string headers' word 1 unsealed (it only writes a
// baked hash when types.PtrSize == 8), so rodata strings fall through
// to strhashFallback's compute path just like runtime-built ones. No
// caller requires strhashPort's output to match a particular AES
// hash, only that it be a deterministic function of the bytes — which
// memhashFallback (seed=0) delivers.
func strhashPort(s string) uint64 {
	if len(s) == 0 {
		return uint64(memhash(nil, 0, 0))
	}
	return uint64(memhash(unsafe.Pointer(unsafe.StringData(s)), 0, uintptr(len(s))))
}
