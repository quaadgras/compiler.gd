// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ir

import "sync/atomic"

// bitset8 / bitset16 wrap a plain uint32 accessed via the atomic
// package's free functions, NOT atomic.Uint32. The struct must be
// copyable (miniNode embeds it, and node_gen.go's `c := *n` shallow-
// copy methods rely on that); atomic.Uint32 carries a noCopy marker
// that would make every generated copy method fail vet's copylocks.
//
// Concurrent in-process compile invocations setting different bits
// on the same shared miniNode.bitset (predeclared *Names from
// BuiltinPkg/UnsafePkg reachable from any invocation's typecheck
// path) still race-free thanks to the atomic.LoadUint32 / CAS pair.
type bitset8 struct{ n uint32 }

func (f *bitset8) load() uint8 { return uint8(atomic.LoadUint32(&f.n)) }

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

func (f *bitset8) get2(shift uint8) uint8 {
	return uint8(atomic.LoadUint32(&f.n)>>shift) & 3
}

// set2 sets two bits in f using the bottom two bits of b.
func (f *bitset8) set2(shift uint8, b uint8) {
	mask := uint32(3) << shift
	bits := uint32(b&3) << shift
	for {
		old := atomic.LoadUint32(&f.n)
		new := (old &^ mask) | bits
		if old == new {
			return
		}
		if atomic.CompareAndSwapUint32(&f.n, old, new) {
			return
		}
	}
}

type bitset16 struct{ n uint32 }

func (f *bitset16) load() uint16 { return uint16(atomic.LoadUint32(&f.n)) }

func (f *bitset16) set(mask uint16, b bool) {
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
