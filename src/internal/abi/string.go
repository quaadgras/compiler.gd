// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !wasm.gc

package abi

import (
	"internal/goarch"
	"unsafe"
)

// The string of the gd fork (doc/gd/sso-string.md): two words, w0 and
// w1, as in stock Go.
//
//	not inline: w0 = the address of the bytes (nil for ""), w1 = the length.
//	inline:     w0 = 1<<63 | n<<56 | bytes 8..14, w1 = bytes 0..7,
//	            a string of n = 1..15 bytes, the unused bytes zero.
//
// Only with 8-byte words: with 4-byte words (386, arm, mips) a string
// is stock Go's, never inline. Byte i of an inline string is bits 8i to
// 8i+7 of w1 (i < 8) or of w0 (8 <= i < 15, as i-8) on a little-endian
// target: storing w1 and then w0 (without its tag) puts the bytes in
// order (StringInlineBytes). On a big-endian target w1 is bytes 0..7 as
// a big-endian load of them has them, and w0 << 8 bytes 8..15.
//
// Bit 63 of w0 tells the forms apart. No address in the heap, the data
// and bss or a stack has it, so the collector, which scans w0 as a
// pointer, ignores w0 of an inline string; the length is the tag's low
// bits. Two inline strings are equal exactly when their words are; a
// string not inline of 1 to 15 bytes (from unsafe.String, or a substring
// of a longer one) can equal an inline one, and hashes as one.
const (
	// StringInlineCap is the longest inline string, 0 with 4-byte words.
	StringInlineCap = 15 * (goarch.PtrSize / 8)

	// StringTagShift is the bit of w0 at which the length of an inline
	// string is; StringInlineBit says that w0 is inline.
	StringTagShift  = 56
	StringInlineBit = 1 << 63

	// StringInlineMask masks the bytes 8..14 of an inline string in w0.
	StringInlineMask = 1<<StringTagShift - 1
)

// stringHeader is a string's words.
type stringHeader struct {
	w0 uintptr
	w1 int
}

// StringIsInline reports whether s is inline.
//
//go:nosplit
func StringIsInline(s string) bool {
	sh := (*stringHeader)(NoEscape(unsafe.Pointer(&s)))
	return goarch.PtrSize == 8 && int64(sh.w0) < 0
}

// StringInlineBytes returns the bytes of the inline string s in order,
// the bytes past its length zero (and its length).
//
//go:nosplit
func StringInlineBytes(s string) (b [16]byte, n int) {
	sh := (*stringHeader)(NoEscape(unsafe.Pointer(&s)))
	w0 := uint64(sh.w0)
	n = int(w0 >> StringTagShift & 15)
	if goarch.BigEndian {
		w0 <<= 8
	} else {
		w0 &= StringInlineMask
	}
	*(*uint64)(unsafe.Pointer(&b[0])) = uint64(sh.w1)
	*(*uint64)(unsafe.Pointer(&b[8])) = w0
	return b, n
}

// StringFromInlineBytes returns the inline string of the n (1..15)
// bytes at the start of b, whose bytes past n must be zero.
//
//go:nosplit
func StringFromInlineBytes(b *[16]byte, n int) string {
	w1 := *(*uint64)(unsafe.Pointer(&b[0]))
	w0 := *(*uint64)(unsafe.Pointer(&b[8]))
	if goarch.BigEndian {
		w0 >>= 8
	}
	var s string
	sh := (*stringHeader)(NoEscape(unsafe.Pointer(&s)))
	sh.w0 = uintptr(w0 | StringInlineBit | uint64(n)<<StringTagShift)
	sh.w1 = int(w1)
	return s
}

// StringBytes returns the bytes of *sp: a view of them if it is not
// inline, else a copy (in the heap). It must not be modified.
func StringBytes(sp *string) []byte {
	if StringIsInline(*sp) {
		b, n := StringInlineBytes(*sp)
		return append([]byte(nil), b[:n]...)
	}
	return unsafe.Slice(unsafe.StringData(*sp), len(*sp))
}

// SpreadString and UnspreadString are those of wasm32 (string_ptr32.go),
// whose interfaces do not hold a string as its words in a row; with
// other words an interface holds a string as it is in memory, from the
// data word on, and they are not used.
func SpreadString(sp *string) (data unsafe.Pointer, words [2]uint64) {
	panic("abi.SpreadString")
}

func UnspreadString(sp *string, data unsafe.Pointer, words *[2]uint64) {
	panic("abi.UnspreadString")
}
