// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.ptr32

package abi

import "unsafe"

// The string of wasm32 (GOWASM=gc,split with 4-byte words; see
// doc/gd/sso-string.md and stage 16 of doc/gd/wasm-gc.md): 16 bytes,
// aligned to 4, seen as two uint64s, A at offset 0 and B at offset 8.
//
// A string of 1 to 15 bytes is kept in them (inline), as in words 1
// and 2 of the strings of 8-byte words (string.go): its bytes at
// offsets 0 to 14, the rest zero, and the length in the top 4 bits of
// B (the tag). Any other string has a tag of 0:
//
//	word0 @  0: the address of the bytes (nil for an empty string)
//	word1 @  4: the length
//	word2 @  8: the cached hash (0)
//	word3 @ 12: 0 (its top 4 bits are the tag)
//
// Two inline strings are equal exactly when their A and B are. Word 0
// of an inline string is text: it is read as a pointer only after the
// tag is found to be 0. It may hold text here because the engine never
// scans linear memory: what keeps the bytes of a string alive is the
// pointer in the element of its block's array for word 0, which is nil
// for an inline string.
const (
	StringInlineCap = 15

	// StringTagShift is the bit of B at which the tag begins;
	// StringLenMask masks the bytes of an inline string in B.
	StringTagShift = 60
	StringLenMask  = 1<<StringTagShift - 1
)

// StringInlineOff is the offset of the bytes of an inline string in
// its header.
const StringInlineOff = 0

type stringHeader struct {
	w0 unsafe.Pointer // the address of the bytes, if not inline
	n  uint32         // the length, if not inline
	b  uint64         // the hash and the tag; inline: bytes 8..14 and the tag
}

func (sh *stringHeader) tag() int { return int(sh.b >> StringTagShift) }

// StringIsInline reports whether s is kept in its header.
//
//go:nosplit
func StringIsInline(s string) bool {
	sh := (*stringHeader)(NoEscape(unsafe.Pointer(&s)))
	return sh.tag() != 0
}

// StringInlineWords returns the bytes of an inline string s as two
// uint64 words, lo bytes 0 to 7 and hi bytes 8 to 14 (the tag masked
// off), and its length. Caller must ensure StringIsInline(s).
//
//go:nosplit
func StringInlineWords(s string) (lo, hi uint64, n int) {
	sh := (*stringHeader)(unsafe.Pointer(&s))
	return *(*uint64)(unsafe.Pointer(&s)), sh.b & StringLenMask, sh.tag()
}

// StringHeapBytes returns s, not inline, as a []byte that aliases its
// bytes: NO COPY, DO NOT modify. Caller must ensure !StringIsInline(s).
//
//go:nosplit
func StringHeapBytes(s string) []byte {
	sh := (*stringHeader)(unsafe.Pointer(&s))
	return unsafe.Slice((*byte)(sh.w0), int(sh.n))
}

// StringBytes returns a []byte view of *sp regardless of representation:
// for an inline string it aliases *sp itself, so the slice must not
// outlive *sp. NO COPY, DO NOT modify.
//
//go:nosplit
func StringBytes(sp *string) []byte {
	sh := (*stringHeader)(unsafe.Pointer(sp))
	if n := sh.tag(); n != 0 {
		return unsafe.Slice((*byte)(unsafe.Pointer(sp)), n)
	}
	return unsafe.Slice((*byte)(sh.w0), int(sh.n))
}

// Heapify returns *sp as a string that is not inline: unchanged if it
// is not, else one whose bytes are those in *sp's header, which the
// result aliases (DO NOT let it outlive *sp). See string.go.
//
//go:nosplit
func Heapify(sp *string) string {
	sh := (*stringHeader)(unsafe.Pointer(sp))
	if n := sh.tag(); n != 0 {
		return unsafe.String((*byte)(unsafe.Pointer(sp)), n)
	}
	return unsafe.String((*byte)(sh.w0), int(sh.n))
}
