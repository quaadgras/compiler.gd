// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"sync"
	"unicode/utf8"
)

// wchar.h's wide strings, numbers and input and output, and the
// multibyte functions of stdlib.h. wchar_t is int32, UTF-32; a stream's
// wide characters are UTF-8 in it, as are its multibyte ones, so that
// wide output is narrow output of the same characters.

func Wat(s Ptr, i int) int32     { return int32(Load32(Add(s, 4*i))) }
func Wset(s Ptr, i int, c int32) { Store32(Add(s, 4*i), uint32(c)) }

// The wide strings' numbers: the narrow functions', of the ASCII prefix
// of the wide string, which is all a number can be.

// wideNumber calls f on a C string of s's ASCII prefix, and stores at
// end where in s f's end is.
func WideNumber[T any](s, end Ptr, f func(ns, nend Ptr) T) T {
	var b []byte
	for i := 0; ; i++ {
		c := Wat(s, i)
		if c <= 0 || c >= 0x80 {
			break
		}
		b = append(b, byte(c))
	}
	ns := CString(string(b))
	nend := Malloc(uint64(WordSize))
	v := f(ns, nend)
	if Addr(end) != 0 {
		StorePtr(end, Add(s, 4*Sub(LoadPtr(nend), ns)))
	}
	return v
}

func Wcstol32(s, end Ptr, base int32) int64 {
	return WideNumber(s, end, func(ns, ne Ptr) int64 { return Strtol32(ns, ne, base) })
}

func Wcstoul32(s, end Ptr, base int32) uint64 {
	return WideNumber(s, end, func(ns, ne Ptr) uint64 { return Strtoul32(ns, ne, base) })
}

// Wide input and output.

// The conversions of whole strings, and stdlib.h's of one character,
// which keep no state (UTF-8 needs none between characters).

var mbtowcMu sync.Mutex

// decodeMB decodes the character at s, of at most n bytes.
func DecodeMB(s Ptr, n uint64) (r rune, size int, ok bool) {
	var b []byte
	for i := uint64(0); i < n && i < utf8.UTFMax; i++ {
		b = append(b, Load8(Add(s, int(i))))
		if utf8.FullRune(b) {
			r, size = utf8.DecodeRune(b)
			return r, size, r != utf8.RuneError || size > 1
		}
	}
	return 0, 0, false
}

// stdlib.h's other C23 functions, and stdio.h's fgetpos and fsetpos.

var ()

// An fpos_t is a stream's offset.
