// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

import (
	_ "internal/bytealg" // for cmpstring
	"unsafe"
)

// A string is three words (see doc/gd/sso-string.md): up to 15 bytes
// are in the second and third words, with the length in the top four
// bits of the third; a longer string is a pointer to its bytes, which
// are in linear memory, and its length in the third word.

const (
	stringTagShift = 60
	stringLenMask  = 1<<stringTagShift - 1
	stringInline   = 15 // the longest string kept in the words
)

type stringStruct struct {
	str  unsafe.Pointer // the bytes, or nil
	hash uint           // the cached hash, or bytes 0 to 7
	len  uint           // the length, or tag<<60 and bytes 8 to 14
}

// tmpBuf is the buffer on the caller's stack that the compiler offers
// for a result that does not escape. It is not used here.
type tmpBuf [32]byte

// inlineString returns the string of the n <= 15 bytes in the words.
func inlineString(w1, w2 uint64, n int) string {
	var r stringStruct
	r.hash = uint(w1)
	r.len = uint(w2&stringLenMask | uint64(n)<<stringTagShift)
	return *(*string)(unsafe.Pointer(&r))
}

// A stringBuilder builds a string of a known length a byte at a time.
type stringBuilder struct {
	w1, w2 uint64  // the bytes, if the string is short
	addr   uintptr // otherwise, where they go
	p      unsafe.Pointer
	n, l   int
}

func newString(l int) stringBuilder {
	b := stringBuilder{l: l}
	if l > stringInline {
		memstatsMallocs++
		b.p = linnew(uintptr(l))
		b.addr = linaddr(b.p)
	}
	return b
}

func (b *stringBuilder) byte(c byte) {
	switch {
	case b.l > stringInline:
		linstore8(b.addr+uintptr(b.n), c)
	case b.n < 8:
		b.w1 |= uint64(c) << (8 * uint(b.n))
	default:
		b.w2 |= uint64(c) << (8 * uint(b.n-8))
	}
	b.n++
}

func (b *stringBuilder) string(s string) {
	if b.l > stringInline {
		if src := trylinaddr((*stringStruct)(unsafe.Pointer(&s)).str); src != 0 {
			lincopy(b.addr+uintptr(b.n), src, uintptr(len(s)))
			b.n += len(s)
			return
		}
	}
	for i := 0; i < len(s); i++ {
		b.byte(s[i])
	}
}

func (b *stringBuilder) result() string {
	if b.l > stringInline {
		return unsafe.String((*byte)(b.p), b.l)
	}
	return inlineString(b.w1, b.w2, b.l)
}

func concatstrings(buf *tmpBuf, a []string) string {
	l := 0
	for _, x := range a {
		if l+len(x) < l {
			fatal("string concatenation too long")
		}
		l += len(x)
	}
	b := newString(l)
	for _, x := range a {
		b.string(x)
	}
	return b.result()
}

func concatstring2(buf *tmpBuf, a0, a1 string) string {
	b := newString(len(a0) + len(a1))
	b.string(a0)
	b.string(a1)
	return b.result()
}

func concatstring3(buf *tmpBuf, a0, a1, a2 string) string {
	b := newString(len(a0) + len(a1) + len(a2))
	b.string(a0)
	b.string(a1)
	b.string(a2)
	return b.result()
}

func concatstring4(buf *tmpBuf, a0, a1, a2, a3 string) string {
	b := newString(len(a0) + len(a1) + len(a2) + len(a3))
	b.string(a0)
	b.string(a1)
	b.string(a2)
	b.string(a3)
	return b.result()
}

func concatstring5(buf *tmpBuf, a0, a1, a2, a3, a4 string) string {
	b := newString(len(a0) + len(a1) + len(a2) + len(a3) + len(a4))
	b.string(a0)
	b.string(a1)
	b.string(a2)
	b.string(a3)
	b.string(a4)
	return b.result()
}

// sliceinlinestring returns s[lo:hi] for a string s kept in its words.
func sliceinlinestring(s string, lo, hi int) string {
	b := newString(hi - lo)
	for i := lo; i < hi; i++ {
		b.byte(s[i])
	}
	return b.result()
}

func slicebytetostring(buf *tmpBuf, ptr *byte, n int) string {
	b := newString(n)
	if buf != nil && n <= len(buf) && n > stringInline {
		memstatsMallocs-- // (Go would use buf)
	}
	if n > stringInline {
		memmove(b.p, unsafe.Pointer(ptr), uintptr(n))
		return b.result()
	}
	for i := 0; i < n; i++ {
		b.byte(*(*byte)(add(unsafe.Pointer(ptr), uintptr(i))))
	}
	return b.result()
}

func slicebytetostringtmp(ptr *byte, n int) string {
	return slicebytetostring(nil, ptr, n)
}

func stringtoslicebyte(buf *tmpBuf, s string) []byte {
	b := make([]byte, len(s))
	if buf != nil && len(s) <= len(buf) {
		memstatsMallocs-- // (Go would use buf)
	}
	if src := trylinaddr((*stringStruct)(unsafe.Pointer(&s)).str); src != 0 && len(s) > 0 {
		lincopy(linaddr(unsafe.Pointer(unsafe.SliceData(b))), src, uintptr(len(s)))
		return b
	}
	for i := 0; i < len(s); i++ {
		b[i] = s[i]
	}
	return b
}

func intstring(buf *[4]byte, v int64) string {
	if int64(rune(v)) != v {
		v = runeError
	}
	var tmp [4]byte
	n := encoderune(tmp[:], rune(v))
	b := newString(n)
	for i := 0; i < n; i++ {
		b.byte(tmp[i])
	}
	return b.result()
}

func stringtoslicerune(buf *[32]rune, s string) []rune {
	n := 0
	for range s {
		n++
	}
	a := make([]rune, n)
	n = 0
	for _, r := range s {
		a[n] = r
		n++
	}
	return a
}

func slicerunetostring(buf *tmpBuf, a []rune) string {
	l := 0
	var tmp [4]byte
	for _, r := range a {
		l += encoderune(tmp[:], r)
	}
	b := newString(l)
	for _, r := range a {
		n := encoderune(tmp[:], r)
		for i := 0; i < n; i++ {
			b.byte(tmp[i])
		}
	}
	return b.result()
}

// streqfast is what the compiler calls for == on strings whose words
// differ.
func streqfast(s, t string) bool {
	if len(s) != len(t) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] != t[i] {
			return false
		}
	}
	return true
}

// cmpstring is implemented in internal/bytealg.
func cmpstring(a, b string) int
