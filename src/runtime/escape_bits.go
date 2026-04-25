// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"internal/goarch"
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
	// Idempotence: if src doesn't sit on the current goroutine's
	// stack, it's already on the heap (or a global). Return it
	// unchanged — migrating an already-heap pointer would break
	// caller-visible identity (e.g. Builder.copyCheck) on
	// subsequent wrap calls, and waste an allocation.
	ptr := uintptr(src)
	stk := getg().stack
	if ptr < stk.lo || ptr >= stk.hi {
		return src
	}
	// mallocgc refuses needzero=false for types with pointers because
	// a GC pass between the alloc and the memmove could scan the
	// uninitialised buffer and chase stale words. We always zero:
	// cheap, and typedmemmove overwrites everything in a breath.
	dst := mallocgc(typ.Size_, typ, true)
	typedmemmove(typ, dst, src)
	return dst
}

// maybeInPlace picks storage for a callee's about-to-be-returned
// pointer-typed result. When outBuf is non-nil the caller has
// reserved a buffer of typ.Size_ bytes (typically on its own
// stack) and is promising the returned pointer doesn't need to
// outlive the caller's frame; we zero the buffer so the callee
// sees fresh storage and return outBuf. When outBuf is nil the
// caller has determined that the result escapes its frame (or
// declined to opt in), so we fall back to heap allocation — same
// cost as a plain new(T) in stock Go.
//
// Usage pattern in a gd-compiled callee:
//
//	func f(outBuf *T) *T {
//	    p := (*T)(runtime.maybeInPlace(unsafe.Pointer(outBuf), typeOfT))
//	    // ... fill *p ...
//	    return p
//	}
//
// The caller emits either &stackBuf or nil as the extra argument
// based on its escape analysis of the result. This mirrors the
// arg-side wrap (materializeToHeap) — every alloc the caller
// can prove unnecessary turns into a stack buffer reuse instead.
//
//go:nosplit
func maybeInPlace(outBuf unsafe.Pointer, typ *abi.Type) unsafe.Pointer {
	if outBuf != nil {
		// Caller's buffer may carry stale bytes from an earlier
		// iteration of the enclosing scope; zero it so the
		// callee sees the same fresh state stock Go's new(T)
		// would have produced.
		memclrNoHeapPointers(outBuf, typ.Size_)
		// If typ has pointer fields we must also avoid GC-
		// scanning stale word values; typedmemclr uses the
		// type's ptrmask to emit write-barriered clears for
		// the pointer words. memclrNoHeapPointers above
		// handles the non-pointer words; typedmemclr fixes up
		// the pointer words (it also zeros non-pointer words,
		// but the redundant zeroing is cheaper than scanning
		// stale values in a mid-cycle GC).
		if typ.Pointers() {
			typedmemclr(typ, outBuf)
		}
		return outBuf
	}
	return mallocgc(typ.Size_, typ, true)
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

// maxComputeMaskDepth caps how deeply the Phase-F dynamic-mask
// resolution can chain (wrapper A's compute fn asks wrapper B's
// compute fn, which asks wrapper C's, etc.). Real wrapper chains
// are typically 2–3 levels; we leave generous headroom before
// declaring "probable cycle" and falling back to a conservative
// mask. Larger limits make no practical difference on acyclic
// graphs and slow down the cycle-hit case; smaller limits risk
// false positives on legitimate deep chains.
const maxComputeMaskDepth = 8

// conservativeAllEscapeMask is the fallback resolveMask returns
// when the depth limit is hit (probable cycle). Every argument
// bit 1..63 set, bit 0 cleared (reserved). maybeEscapeArg
// reading any arg index sees "this arg escapes," so the caller
// materialises unconditionally — inefficient but always safe.
const conservativeAllEscapeMask uint64 = ^uint64(1)

// computeMaskFn is the signature of a Phase-F dynamic-mask function.
//
// Arguments:
//
//   - carrier: the closure value or itab pointer that owns the
//     mask slot. The fn typically reads captured variables
//     (closure) or delegated-to itab masks (iface wrapper) out
//     of it to decide what to return.
//
//   - heapMask: per-argument "I already know this arg is on the
//     heap" bits, with the same bit-k+1 layout as the returned
//     mask. The caller sets bit k+1 when it knows arg k's
//     current storage is already heap (so the fn need not
//     "escape" it again — the materialise would be a no-op or
//     wasteful). A conservative caller passes 0 ("treat all args
//     as stack for reasoning purposes").
//
//   - depth: remaining resolution budget for this compute-fn
//     chain. The fn MUST pass depth (not a hard-coded starting
//     value) into any inner resolveMask call; the runtime
//     decrements before handing control to the fn. When a fn
//     forwards to another carrier's raw mask word, call
//     resolveMask(innerRaw, innerCarrier, heapMask, depth) — the
//     runtime handles the depth check and the cycle fallback.
//
// Return value: the full resolved mask the caller should obey.
// Bit 0 stays reserved (must be 0 in the returned value). Bit
// k+1 set iff argument k must end up on the heap after the wrap
// runs. If the fn wants to say "arg A must escape because arg B
// is on the heap," it reads (heapMask >> (B+1)) & 1 and factors
// that into the output; that's the inter-argument relationship
// the plain static-mask encoding can't express.
//
// The caller is free to "overwrite" the heapMask bits in its
// output — if the fn returns bit k+1 set, that's the final word
// regardless of whether heapMask had k+1 set. (A fn that wants
// to preserve input heap decisions OR-s heapMask into its
// output; a fn that wants to shrink escape requirements ignores
// heapMask in the output.)
type computeMaskFn func(carrier unsafe.Pointer, heapMask uint64, depth int) uint64

// resolveMask returns the resolved static mask for the raw mask
// word read from a closure second-word or an itab Fun-tail slot.
// When bit 0 of rawMask is clear, the word is itself the static
// mask and heapMask has no effect. When bit 0 is set, the upper
// bits are a Phase-F computeMaskFn pointer (stored with bit 0
// set so a plain static value of 0 stays unambiguously static);
// we call it with carrier, heapMask, and the decremented depth.
// When depth reaches 0 before we can resolve, we return a
// conservative "every arg escapes" mask — safe under any
// wrapper cycle.
//
// Walk-emitted call sites prefer to open-code the bit-0 fast
// path (a single load + test) and tail into resolveMaskSlow
// only when the dynamic-mask discriminator fires. resolveMask
// is kept as the single-entry helper for the per-arg helpers
// below and for tests; the open-coded path skips it to keep the
// fast path call-free.
//
//go:nosplit
func resolveMask(rawMask uint64, carrier unsafe.Pointer, heapMask uint64, depth int) uint64 {
	if rawMask&1 == 0 {
		return rawMask
	}
	return resolveMaskSlow(rawMask, carrier, heapMask, depth)
}

// resolveMaskSlow is the dynamic-discriminator branch of
// resolveMask, factored out so callers (chiefly walk-emitted
// indirect-call sequences) can open-code the bit-0 test and
// CALL into the runtime only when bit 0 is actually set.
// rawMask MUST have bit 0 set; the function does not re-check.
//
//go:nosplit
func resolveMaskSlow(rawMask uint64, carrier unsafe.Pointer, heapMask uint64, depth int) uint64 {
	if depth <= 0 {
		return conservativeAllEscapeMask
	}
	fnPtr := uintptr(rawMask) &^ 1
	fn := *(*computeMaskFn)(unsafe.Pointer(&fnPtr))
	// Clear bit 0 on the fn's output. A compute fn that
	// accidentally returns a value with bit 0 set would otherwise
	// confuse a caller that re-feeds the mask into resolveMask
	// without noticing — bit 0 is exclusively the rawMask
	// discriminator.
	return fn(carrier, heapMask, depth-1) &^ 1
}

// isOnHeap reports whether p points outside the current
// goroutine's stack — i.e. into the heap, a global, or another
// goroutine's stack (rare and treated as heap-equivalent for
// escape-bit purposes). Used by walk-emitted indirect-call
// sequences to build a precise heapMask for resolveMaskSlow:
// each candidate arg's box pointer is tested in turn and the
// corresponding bit set when off-stack. The compute fn can then
// express inter-arg relationships ("arg A escapes iff arg B is
// on heap") that the static-mask encoding cannot.
//
//go:nosplit
func isOnHeap(p unsafe.Pointer) bool {
	if p == nil {
		return false
	}
	ptr := uintptr(p)
	stk := getg().stack
	return ptr < stk.lo || ptr >= stk.hi
}

// resolveForwardedRecvFieldMask is the parameterised backbone for
// Phase F4's synthesised compute fns. Each detected trivial
// forwarder (escape.DetectForwarders) gets a tiny synthesised
// `func(carrier, heapMask, depth) uint64` that hardcodes the
// receiver-field byte offset and the inner method's index, then
// tail-calls this helper. The helper:
//
//  1. Reinterprets carrier as the wrapper's receiver pointer.
//  2. Loads the iface header at carrier+fieldOffset (a 2-word
//     {itab, data} pair).
//  3. Reads the inner itab's per-method mask slot for methodIdx.
//  4. Forwards to resolveMask with the inner itab as the new
//     carrier and the same heapMask.
//
// On any nil itab we return conservativeAllEscapeMask — the
// forwarder will materialize all candidate args, matching what a
// well-behaved static-mask wrapper would have done in stock Go.
//
// fieldOffset and methodIdx are compile-time constants supplied
// by the synthesised wrapper, so this function compiles down to
// straight pointer arithmetic + one resolveMask call.
//
//go:nosplit
func resolveForwardedRecvFieldMask(carrier unsafe.Pointer, fieldOffset uintptr, methodIdx int, heapMask uint64, depth int) uint64 {
	if depth <= 0 {
		return conservativeAllEscapeMask
	}
	// iface header: matches abi.EmptyInterface / iface in
	// internal/abi/iface.go: itab pointer first, then data
	// pointer. We only need the itab.
	type ifaceHdr struct {
		itab *itab
		data unsafe.Pointer
	}
	inner := (*ifaceHdr)(unsafe.Add(carrier, fieldOffset))
	if inner.itab == nil {
		return conservativeAllEscapeMask
	}
	tab := inner.itab
	ni := len(tab.Inter.Methods)
	maskBase := unsafe.Add(unsafe.Pointer(&tab.Fun[0]), uintptr(ni)*goarch.PtrSize)
	raw := *(*uint64)(unsafe.Add(maskBase, uintptr(methodIdx)*8))
	// Pass depth (not depth-1): resolveMask/Slow decrement
	// internally before calling the inner compute fn (if any).
	// Subtracting here would double-count.
	return resolveMask(raw, unsafe.Pointer(tab), heapMask, depth)
}

