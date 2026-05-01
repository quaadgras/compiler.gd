// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package abi

import "unsafe"

// String header layout (gd small-string optimization).
//
// The gd fork grows the string header from 2 words (16 B) to 3 words
// (24 B) and packs small strings inline in the header to avoid heap
// allocation. See doc/gd/sso-string.md.
//
// Layout (64-bit offsets):
//
//	word0 @  0: heap data pointer, or nil when inline (always a valid
//	            pointer-or-nil; never arbitrary bytes — preserves
//	            stock GC ptrmask semantics).
//	word1 @  8: heap: cached string hash (64-bit). Sentinel 0 = uncomputed.
//	                  Reserved slot — Phase A keeps it zero; a later
//	                  phase populates it lazily for map / hash-seed
//	                  integration.
//	            inline: bytes[0:8] (8 bytes at indices 0..7).
//	word2 @ 16: heap: length in low 60 bits (upper 4 bits are the tag,
//	                  always 0 for heap rep).
//	            inline: (tag<<60) | bytes[8:15] packed in the low 56 bits
//	                    (7 bytes at indices 8..14). Bits 56..59 are
//	                    padding (zero).
//
// The upper 4 bits of word2 are the length tag:
//   - 0       : heap rep. word0 is the data pointer; word2 (low 60 bits)
//     is the length. Max heap length is 2^60 − 1, well above
//     runtime.maxAlloc.
//   - 1..15   : inline rep. Tag equals the inline byte count. word0 is
//     nil; the 15 bytes are split 8+7 across word1 (all 8
//     bytes) and the low 56 bits of word2.
//
// Inline bytes are contiguous in memory on little-endian architectures:
// at offsets 8..22 of the header. The tag+pad byte lives at offset 23
// (the high byte of word2). &s.word1 is therefore a valid pointer to
// the first inline byte, and the 15 inline bytes can be read as a
// contiguous byte range.
//
// Length derivation (branch lowers to cmov on mainstream arches):
//
//	w2  := word2
//	tag := w2 >> StringTagShift
//	if tag != 0 { return int(tag) }
//	return int(w2 & StringLenMask)
//
// Pointer derivation (single nil-compare, cmov-able):
//
//	if word0 != 0 { return unsafe.Pointer(word0) }       // heap
//	return unsafe.Pointer(&s.word1)                      // inline
//
// Inline strings of length 0 are never constructed — the canonical empty
// string is heap-rep {nil, 0, 0}, matching stock Go.
const (
	StringInlineCap = 15

	// StringTagShift is the bit offset of the length tag within word2.
	StringTagShift = 60

	// StringLenMask masks the length field of word2 for heap-rep strings.
	StringLenMask = 1<<StringTagShift - 1
)

// stringHeader is the gd-fork string layout (3 × uint64 = 24 B).
// It mirrors stringStruct in runtime/string.go but is exported here so
// SSO-aware fast-path helpers below can be used by any package without
// going through linkname or runtime calls.
type stringHeader struct {
	w0 uint64 // heap data ptr (0 if inline)
	w1 uint64 // heap: cached hash; inline: bytes 0..7
	w2 uint64 // upper 4 bits = tag; heap: low 60 = len; inline: low 56 = bytes 8..14
}

// StringIsInline reports whether s uses the fork's inline (SSO) rep.
// The check is a single shift+test; the result is cmov-able.
//
//go:nosplit
func StringIsInline(s string) bool {
	sh := (*stringHeader)(NoEscape(unsafe.Pointer(&s)))
	return sh.w2>>StringTagShift != 0
}

// StringInlineWords returns the inline payload of s as two uint64 words
// plus the byte count. lo holds bytes 0..7, hi's low 56 bits hold bytes
// 8..14 (high 8 bits of hi are length-tag and padding; mask them off
// when reading). Caller must ensure StringIsInline(s) — otherwise lo/hi
// are unspecified.
//
//go:nosplit
func StringInlineWords(s string) (lo, hi uint64, n int) {
	sh := (*stringHeader)(unsafe.Pointer(&s))
	return sh.w1, sh.w2 & StringLenMask, int(sh.w2 >> StringTagShift)
}

// StringHeapBytes returns s as a []byte view aliasing the same backing
// storage. NO COPY. The returned slice's backing is the string's data
// pointer, so mutating it would violate string immutability — DO NOT
// modify. Caller must ensure !StringIsInline(s) (use StringIsInline
// first) — for inline strings the heap data pointer is nil and the
// returned slice would be empty.
//
// Intended for byte-scanning fast paths in unicode/utf8, strings,
// bytes, regexp, etc.: dispatch on StringIsInline at the top of the
// function, do bitwise ops on the inline words, and delegate to the
// existing []byte implementation for the heap branch.
//
//go:nosplit
func StringHeapBytes(s string) []byte {
	sh := (*stringHeader)(unsafe.Pointer(&s))
	n := sh.w2 & StringLenMask
	return unsafe.Slice((*byte)(unsafe.Pointer(uintptr(sh.w0))), int(n))
}

// StringBytes returns a []byte view of *sp regardless of representation.
// NO COPY. For heap-rep strings the slice aliases the heap data
// pointer (stable for the string's lifetime in the heap). For
// inline-rep strings it aliases the *sp variable's header (bytes
// start at &(*sp).w1) — so the slice is only valid for *sp's
// lifetime in the calling frame.
//
// CALLER LIFETIME RULE: for inline rep, the slice's backing is *sp's
// stack storage. The slice must not outlive *sp. Pass &s rather than
// s by value — taking the parameter by pointer ensures the alias is
// to the caller's local, not StringBytes's. (If StringBytes took s
// by value and were not inlined, the slice would alias StringBytes's
// frame and dangle on return.)
//
// The returned slice MUST NOT be modified; strings are immutable.
//
//go:nosplit
func StringBytes(sp *string) []byte {
	sh := (*stringHeader)(unsafe.Pointer(sp))
	if sh.w0 != 0 {
		// heap: data pointer in w0, length in low 60 bits of w2.
		return unsafe.Slice((*byte)(unsafe.Pointer(uintptr(sh.w0))), int(sh.w2&StringLenMask))
	}
	// inline: bytes start at &sh.w1 inside *sp.
	// Empty heap-rep strings have w0==0 and tag==0, yielding length 0
	// — also fine, returns an empty slice.
	return unsafe.Slice((*byte)(unsafe.Pointer(&sh.w1)), int(sh.w2>>StringTagShift))
}
