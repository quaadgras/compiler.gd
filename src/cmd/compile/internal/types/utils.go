// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import "sync/atomic"

const BADWIDTH = -1000000000

// bitset8 holds up to 8 boolean flags. Wraps a plain uint32 accessed
// via atomic.LoadUint32 / CompareAndSwapUint32 so concurrent in-process
// compile invocations setting different bits on the same shared
// Type/Sym/Field bitset (BuiltinPkg/UnsafePkg Syms, Types[...] universe
// Types, etc.) don't race on the byte.
//
// We avoid atomic.Uint32 because its noCopy marker would make
// (*Type).copy / (*Field).Copy fail vet's copylocks check — both
// methods do shallow copies of structs that embed bitset8.
type bitset8 struct{ n uint32 }

// load returns the current bits as a uint8 so existing
// `t.flags & mask != 0` patterns can read via `t.flags.load() & mask`.
func (f *bitset8) load() uint8 { return uint8(atomic.LoadUint32(&f.n)) }

// copyTo atomically reads f's bits and stores them into dst.
func (f *bitset8) copyTo(dst *bitset8) {
	atomic.StoreUint32(&dst.n, atomic.LoadUint32(&f.n))
}

func (f *bitset8) set(mask uint8, b bool) {
	for {
		old := atomic.LoadUint32(&f.n)
		var new uint32
		if b {
			new = old | uint32(mask)
		} else {
			new = old &^ uint32(mask)
		}
		if old == new {
			return
		}
		if atomic.CompareAndSwapUint32(&f.n, old, new) {
			return
		}
	}
}
