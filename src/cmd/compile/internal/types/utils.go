// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import "sync/atomic"

const BADWIDTH = -1000000000

// bitset8 holds up to 8 boolean flags. The underlying word is
// updated via CAS so that concurrent in-process compile invocations
// setting different bits on the same shared Type/Sym/Field bitset
// (BuiltinPkg/UnsafePkg Syms, Types[...] universe Types, etc.) don't
// race on the byte. We use atomic.Uint32 (rather than atomic.Uint8,
// which would be ideal but requires Go 1.19+ on the bootstrap
// toolchain).
type bitset8 atomic.Uint32

// load returns the current bits as a uint8 so existing
// `t.flags & mask != 0` patterns can read via `t.flags.load() & mask`.
func (f *bitset8) load() uint8 { return uint8((*atomic.Uint32)(f).Load()) }

func (f *bitset8) set(mask uint8, b bool) {
	a := (*atomic.Uint32)(f)
	for {
		old := a.Load()
		var new uint32
		if b {
			new = old | uint32(mask)
		} else {
			new = old &^ uint32(mask)
		}
		if old == new {
			return
		}
		if a.CompareAndSwap(old, new) {
			return
		}
	}
}
