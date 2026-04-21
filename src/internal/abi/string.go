// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package abi

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
//               is the length. Max heap length is 2^60 − 1, well above
//               runtime.maxAlloc.
//   - 1..15   : inline rep. Tag equals the inline byte count. word0 is
//               nil; the 15 bytes are split 8+7 across word1 (all 8
//               bytes) and the low 56 bits of word2.
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
