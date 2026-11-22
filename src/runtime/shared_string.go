// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/goarch"
	"unsafe"
)

// Strings.
//
// A string is two words (doc/gd/sso-string.md): one torn between two
// stores, the address of one and the length of another, or an inline
// string's tag and bytes with another's address, is not memory safe. So
// strings are loaded and stored whole wherever they are but in the
// stack, as interfaces are, whatever the compiler knows of the memory.
// With 8-byte words a string is aligned to 16, and where the processor
// makes an aligned 16-byte access single-copy atomic (stringAtomic: AVX
// on amd64, FEAT_LSE2 on arm64) compiled code loads and stores one with
// one 16-byte access, and the runtime copies and clears values that hold
// strings 16 bytes at a time (memmove16, memclr16), each string whole,
// with no lock. Elsewhere, and under GOEXPERIMENT=sharing (whose compiled
// code takes the locks), strings take the sequence locks interfaces take:
// compiled code checks them inline when it loads one and calls stringstore
// to store one; the runtime copies and clears under them.

// stringsWhole is whether strings are loaded and stored whole at all:
// with 8-byte words, but on wasm, as cmd/compile/internal/types's
// StringAligned16.
const stringsWhole = tearEnabled && goarch.PtrSize == 8 && !sharingEnabled

// stringAtomic is whether a 16-byte access to an aligned string is single-
// copy atomic, set by cpuinit. Compiled code reads it.
var stringAtomic bool

// stringsLocked reports whether strings that are loaded and stored whole
// are under the sequence locks.
//
//go:nosplit
func stringsLocked() bool {
	return stringsWhole && !stringAtomic
}

// stringload is called by compiled code to load the string at p, which
// may not be in the stack, where a 16-byte access is not atomic: under
// the sequence lock of its block.
func stringload(p *string) (v string) {
	shareRead(nil, unsafe.Pointer(&v), unsafe.Pointer(p), unsafe.Sizeof(v))
	return
}

// stringstore is called by compiled code to store the string v at dst,
// which may not be in the stack, where a 16-byte access is not atomic:
// under the sequence lock of its block, as ifacestore stores.
func stringstore(dst *string, v string) {
	mp := acquirem()
	// (The stack may move while this waits: the index stays.)
	i := uintptr(unsafe.Pointer(dst)) >> shareBlockShift & (shareStripes - 1)
	shareLockStripe(i)
	*dst = v
	shareUnlockStripe(i)
	releasem(mp)
}

// slicestore is called by compiled code to store the slice of words w0
// and w1 at dst, which may not be in the stack, where a 16-byte access is
// not atomic: under the sequence lock of its block, as stringstore
// stores. The slice is packed already (doc/gd/slice16.md): a huge one's
// header is made before the lock is taken.
func slicestore(dst unsafe.Pointer, w0 unsafe.Pointer, w1 uint64) {
	mp := acquirem()
	i := uintptr(dst) >> shareBlockShift & (shareStripes - 1)
	shareLockStripe(i)
	*(*unsafe.Pointer)(dst) = w0
	*(*uint64)(add(dst, 8)) = w1
	shareUnlockStripe(i)
	releasem(mp)
}

// moveWhole copies size bytes of values of type typ from src to dst, as
// memmove does: 16 bytes at a time if typ holds strings and stringAtomic,
// so that each string, which is aligned to 16, is read and written whole.
//
//go:nosplit
func moveWhole(typ *_type, dst, src unsafe.Pointer, size uintptr) {
	if stringAtomic && typ != nil && tearKind(typ)&tearString != 0 {
		memmove16(dst, src, size)
		return
	}
	memmove(dst, src, size)
}

// clearWhole clears size bytes of values of type typ at ptr, as
// memclrNoHeapPointers does: 16 bytes at a time if typ holds strings and
// stringAtomic.
//
//go:nosplit
func clearWhole(typ *_type, ptr unsafe.Pointer, size uintptr) {
	if stringAtomic && typ != nil && tearKind(typ)&tearString != 0 {
		memclr16(ptr, size)
		return
	}
	memclrNoHeapPointers(ptr, size)
}
