// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ir

import "sync/atomic"

// bitset8 / bitset16 use atomic.Uint32 so concurrent in-process
// compile invocations setting different bits on the same shared
// miniNode.bitset (predeclared *Names from BuiltinPkg/UnsafePkg
// reachable from any invocation's typecheck path) don't race on
// the underlying byte/word.
type bitset8 atomic.Uint32

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

func (f *bitset8) get2(shift uint8) uint8 {
	return uint8((*atomic.Uint32)(f).Load()>>shift) & 3
}

// set2 sets two bits in f using the bottom two bits of b.
func (f *bitset8) set2(shift uint8, b uint8) {
	a := (*atomic.Uint32)(f)
	mask := uint32(3) << shift
	bits := uint32(b&3) << shift
	for {
		old := a.Load()
		new := (old &^ mask) | bits
		if old == new {
			return
		}
		if a.CompareAndSwap(old, new) {
			return
		}
	}
}

type bitset16 atomic.Uint32

func (f *bitset16) load() uint16 { return uint16((*atomic.Uint32)(f).Load()) }

func (f *bitset16) set(mask uint16, b bool) {
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
