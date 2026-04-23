// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"unsafe"
)

// materializeToHeap copies the typ.Size_ bytes at src into a freshly
// heap-allocated object of type typ and returns a pointer to the
// copy. The source may live on the stack; after this call the callee
// can keep the returned pointer indefinitely without fear of stack
// corruption.
//
// This is the fork's escape-bit fallback for dynamic indirect calls:
// when a closure or interface method's compile-time EscMask bit
// indicates the callee retains its arg, the caller synthesises a
// heap copy at the call site and passes that. See
// doc/gd/escape-bits.md.
//
// typ.Size_ must be > 0. Empty-struct args don't need materialisation
// (their "value" is a zero-byte address); callers must elide the
// materialisation at compile time for those.
//
// Pointer fields inside typ are handled by typedmemmove's write
// barrier, which lets the GC trace the freshly-populated heap object.
// go:nosplit: inserted into argument-evaluation sequences where a
// stack grow between reading src and calling typedmemmove would
// invalidate the pointer we just captured.
//
//go:nosplit
func materializeToHeap(src unsafe.Pointer, typ *abi.Type) unsafe.Pointer {
	// mallocgc refuses needzero=false for types with pointers because
	// a GC pass between the alloc and the memmove could scan the
	// uninitialised buffer and chase stale words. We always zero:
	// cheap, and typedmemmove overwrites everything in a breath.
	dst := mallocgc(typ.Size_, typ, true)
	typedmemmove(typ, dst, src)
	return dst
}

// maybeEscapeArg returns src as-is when bit argIdx+1 of mask is clear
// and a heap copy of *src when the bit is set. Compile-time
// call-site wrappers at indirect calls pass the closure or itab mask
// through here: when the specific callee's escape profile says this
// arg stays put, we avoid the alloc; otherwise we materialize before
// the call so the callee can retain the pointer safely.
//
// Bit 0 of mask is the dynamic-mask-fn discriminator (doc/gd/
// escape-bits.md Phase F) and is ignored here — caller must resolve
// that before invoking this helper.
//
//go:nosplit
func maybeEscapeArg(mask uint64, argIdx int, src unsafe.Pointer, typ *abi.Type) unsafe.Pointer {
	if mask&(1<<uint(argIdx+1)) != 0 {
		return materializeToHeap(src, typ)
	}
	return src
}
