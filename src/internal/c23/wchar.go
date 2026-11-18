// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"os"
	"sync"
	"unicode/utf8"
)

// wchar.h's wide strings, numbers and input and output, and the
// multibyte functions of stdlib.h. wchar_t is int32, UTF-32; a stream's
// wide characters are UTF-8 in it, as are its multibyte ones, so that
// wide output is narrow output of the same characters.

func wat(s Ptr, i int) int32          { return int32(Load32(Add(s, 4*i))) }
func wset(s Ptr, i int, c int32)      { Store32(Add(s, 4*i), uint32(c)) }
func wptr(s Ptr, i uint64) Ptr        { return Add(s, int(4*i)) }
func wcsLen(s Ptr) int                { return int(Wcslen(s)) }
func wideGo(s Ptr) string             { return string(wideRunes(s)) }
func Wcscoll(a, b Ptr) int32          { return Wcscmp(a, b) }
func Wmemcpy(d, s Ptr, n uint64) Ptr  { Memmove(d, s, uintptr(4*n)); return d }
func Wmemmove(d, s Ptr, n uint64) Ptr { Memmove(d, s, uintptr(4*n)); return d }

// wideRunes are the wide characters of the string at s.
func wideRunes(s Ptr) []rune {
	var r []rune
	for i := 0; ; i++ {
		c := wat(s, i)
		if c == 0 {
			return r
		}
		r = append(r, rune(c))
	}
}

// storeWide stores r and a terminating null at s.
func storeWide(s Ptr, r []rune) {
	for i, c := range r {
		wset(s, i, int32(c))
	}
	wset(s, len(r), 0)
}

func Wcsncpy(d, s Ptr, n uint64) Ptr {
	i := uint64(0)
	for ; i < n; i++ {
		c := wat(s, int(i))
		wset(d, int(i), c)
		if c == 0 {
			break
		}
	}
	for ; i < n; i++ {
		wset(d, int(i), 0)
	}
	return d
}

func Wcsncat(d, s Ptr, n uint64) Ptr {
	k := wcsLen(d)
	i := 0
	for ; uint64(i) < n; i++ {
		c := wat(s, i)
		if c == 0 {
			break
		}
		wset(d, k+i, c)
	}
	wset(d, k+i, 0)
	return d
}

func Wcsrchr(s Ptr, c int32) Ptr {
	var last Ptr
	for i := 0; ; i++ {
		x := wat(s, i)
		if x == c {
			last = Add(s, 4*i)
		}
		if x == 0 {
			return last
		}
	}
}

// wcsSpan is the length of the prefix of s whose characters are (in) or
// are not in set.
func wcsSpan(s, set Ptr, in bool) uint64 {
	cs := wideRunes(set)
	n := uint64(0)
	for {
		c := wat(s, int(n))
		if c == 0 {
			return n
		}
		found := false
		for _, x := range cs {
			if rune(c) == x {
				found = true
				break
			}
		}
		if found != in {
			return n
		}
		n++
	}
}

func Wcsspn(s, set Ptr) uint64  { return wcsSpan(s, set, true) }
func Wcscspn(s, set Ptr) uint64 { return wcsSpan(s, set, false) }

func Wcspbrk(s, set Ptr) Ptr {
	n := wcsSpan(s, set, false)
	if wat(s, int(n)) == 0 {
		return Ptr{}
	}
	return wptr(s, n)
}

func Wcsstr(h, n Ptr) Ptr {
	needle := wideRunes(n)
	for i := 0; ; i++ {
		match := true
		for j, c := range needle {
			x := wat(h, i+j)
			if rune(x) != c {
				match = false
				break
			}
		}
		if match {
			return Add(h, 4*i)
		}
		if wat(h, i) == 0 {
			return Ptr{}
		}
	}
}

// Wcstok is C's wcstok, which keeps its place in *ptr.
func Wcstok(s, delim, ptr Ptr) Ptr {
	if Addr(s) == 0 {
		s = LoadPtr(ptr)
		if Addr(s) == 0 {
			return Ptr{}
		}
	}
	s = wptr(s, wcsSpan(s, delim, true))
	if wat(s, 0) == 0 {
		StorePtr(ptr, Ptr{})
		return Ptr{}
	}
	e := wptr(s, wcsSpan(s, delim, false))
	if wat(e, 0) == 0 {
		StorePtr(ptr, Ptr{})
	} else {
		wset(e, 0, 0)
		StorePtr(ptr, Add(e, 4))
	}
	return s
}

func Wcsxfrm(d, s Ptr, n uint64) uint64 {
	k := Wcslen(s)
	if k < n {
		Wmemcpy(d, s, k+1)
	}
	return k
}

func Wmemchr(s Ptr, c int32, n uint64) Ptr {
	for i := uint64(0); i < n; i++ {
		if wat(s, int(i)) == c {
			return wptr(s, i)
		}
	}
	return Ptr{}
}

func Wmemcmp(a, b Ptr, n uint64) int32 {
	for i := 0; uint64(i) < n; i++ {
		x, y := wat(a, i), wat(b, i)
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
	}
	return 0
}

func Wmemset(s Ptr, c int32, n uint64) Ptr {
	for i := 0; uint64(i) < n; i++ {
		wset(s, i, c)
	}
	return s
}

func Wcsftime(s Ptr, max uint64, format, tm Ptr) uint64 {
	buf := Malloc(4096)
	n := Strftime(buf, 4096, CString(wideGo(format)), tm)
	r := []rune(string(bytesAt(buf, uintptr(n))))
	if uint64(len(r)) >= max {
		return 0
	}
	storeWide(s, r)
	return uint64(len(r))
}

// The wide strings' numbers: the narrow functions', of the ASCII prefix
// of the wide string, which is all a number can be.

// wideNumber calls f on a C string of s's ASCII prefix, and stores at
// end where in s f's end is.
func wideNumber[T any](s, end Ptr, f func(ns, nend Ptr) T) T {
	var b []byte
	for i := 0; ; i++ {
		c := wat(s, i)
		if c <= 0 || c >= 0x80 {
			break
		}
		b = append(b, byte(c))
	}
	ns := CString(string(b))
	nend := Malloc(uint64(wordSize))
	v := f(ns, nend)
	if Addr(end) != 0 {
		StorePtr(end, Add(s, 4*Sub(LoadPtr(nend), ns)))
	}
	return v
}

func Wcstol(s, end Ptr, base int32) int64 {
	return wideNumber(s, end, func(ns, ne Ptr) int64 { return Strtol(ns, ne, base) })
}

func Wcstoul(s, end Ptr, base int32) uint64 {
	return wideNumber(s, end, func(ns, ne Ptr) uint64 { return Strtoul(ns, ne, base) })
}

func Wcstol32(s, end Ptr, base int32) int64 {
	return wideNumber(s, end, func(ns, ne Ptr) int64 { return Strtol32(ns, ne, base) })
}

func Wcstoul32(s, end Ptr, base int32) uint64 {
	return wideNumber(s, end, func(ns, ne Ptr) uint64 { return Strtoul32(ns, ne, base) })
}

func Wcstod(s, end Ptr) float64 {
	return wideNumber(s, end, func(ns, ne Ptr) float64 { return Strtod(ns, ne) })
}

func Wcstof(s, end Ptr) float32 {
	return wideNumber(s, end, func(ns, ne Ptr) float32 { return Strtof(ns, ne) })
}

// Wide input and output.

const weofC = 0xffffffff

func Fputwc(c int32, f Ptr) uint32 {
	if c < 0 || c > utf8.MaxRune || c >= 0xd800 && c < 0xe000 {
		SetErrno(EILSEQ)
		return weofC
	}
	if !fileOf(f).write(utf8.AppendRune(nil, rune(c))) {
		return weofC
	}
	return uint32(c)
}

func Putwchar(c int32) uint32 { return Fputwc(c, Stdio(1)) }

func Fputws(s, f Ptr) int32 {
	if !fileOf(f).write([]byte(wideGo(s))) {
		return -1
	}
	return 0
}

func Fgetwc(f Ptr) uint32 {
	fl := fileOf(f)
	var b []byte
	for {
		c := fl.getc()
		if c < 0 {
			if len(b) > 0 {
				SetErrno(EILSEQ)
			}
			return weofC
		}
		b = append(b, byte(c))
		if utf8.FullRune(b) {
			r, n := utf8.DecodeRune(b)
			if r == utf8.RuneError && n <= 1 {
				SetErrno(EILSEQ)
				return weofC
			}
			return uint32(r)
		}
	}
}

func Getwchar() uint32 { return Fgetwc(Stdio(0)) }

func Ungetwc(c uint32, f Ptr) uint32 {
	if c == weofC {
		return weofC
	}
	b := utf8.AppendRune(nil, rune(c))
	fl := fileOf(f)
	for i := len(b) - 1; i >= 0; i-- {
		fl.unget = append(fl.unget, b[i])
	}
	return c
}

func Fgetws(s Ptr, n int32, f Ptr) Ptr {
	if n <= 0 {
		return Ptr{}
	}
	i := 0
	for i < int(n)-1 {
		c := Fgetwc(f)
		if c == weofC {
			break
		}
		wset(s, i, int32(c))
		i++
		if c == '\n' {
			break
		}
	}
	if i == 0 {
		return Ptr{}
	}
	wset(s, i, 0)
	return s
}

// Fwide is fwide: streams take both wide and narrow characters, and keep
// no orientation but the one asked for.
func Fwide(f Ptr, mode int32) int32 {
	switch {
	case mode > 0:
		return 1
	case mode < 0:
		return -1
	}
	return 0
}

func Fwprintf(f, format Ptr, va ...any) int32 { return Vfwprintf(f, format, VaStart(va)) }
func Wprintf(format Ptr, va ...any) int32     { return Vfwprintf(Stdio(1), format, VaStart(va)) }
func Vwprintf(format, ap Ptr) int32           { return Vfwprintf(Stdio(1), format, ap) }

func Vfwprintf(f, format, ap Ptr) int32 {
	b := formatC(wideGo(format), vaOf(ap))
	if !fileOf(f).write(b) {
		return -1
	}
	return int32(utf8.RuneCount(b))
}

func Swprintf(s Ptr, n uint64, format Ptr, va ...any) int32 {
	return Vswprintf(s, n, format, VaStart(va))
}

func Vswprintf(s Ptr, n uint64, format, ap Ptr) int32 {
	r := []rune(string(formatC(wideGo(format), vaOf(ap))))
	if uint64(len(r)) >= n {
		if n > 0 {
			storeWide(s, r[:n-1])
		}
		return -1
	}
	storeWide(s, r)
	return int32(len(r))
}

func Fwscanf(f, format Ptr, va ...any) int32 { return Vfwscanf(f, format, VaStart(va)) }
func Wscanf(format Ptr, va ...any) int32     { return Vfwscanf(Stdio(0), format, VaStart(va)) }
func Vwscanf(format, ap Ptr) int32           { return Vfwscanf(Stdio(0), format, ap) }
func Swscanf(s, format Ptr, va ...any) int32 { return Vswscanf(s, format, VaStart(va)) }

func Vfwscanf(f, format, ap Ptr) int32 {
	return Vfscanf(f, CString(wideGo(format)), ap)
}

func Vswscanf(s, format, ap Ptr) int32 {
	return Vsscanf(CString(wideGo(s)), CString(wideGo(format)), ap)
}

// The conversions of whole strings, and stdlib.h's of one character,
// which keep no state (UTF-8 needs none between characters).

var mbtowcMu sync.Mutex

// decodeMB decodes the character at s, of at most n bytes.
func decodeMB(s Ptr, n uint64) (r rune, size int, ok bool) {
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

func Mblen(s Ptr, n uint64) int32 { return Mbtowc(Ptr{}, s, n) }

func Mbtowc(pwc, s Ptr, n uint64) int32 {
	if Addr(s) == 0 {
		return 0 // no state
	}
	if n == 0 {
		return -1
	}
	if Load8(s) == 0 {
		if Addr(pwc) != 0 {
			wset(pwc, 0, 0)
		}
		return 0
	}
	r, size, ok := decodeMB(s, n)
	if !ok {
		SetErrno(EILSEQ)
		return -1
	}
	if Addr(pwc) != 0 {
		wset(pwc, 0, int32(r))
	}
	return int32(size)
}

func Wctomb(s Ptr, wc int32) int32 {
	if Addr(s) == 0 {
		return 0
	}
	n := putRune(s, uint32(wc))
	if n == mbInvalid {
		return -1
	}
	return int32(n)
}

// Mbstowcs is mbstowcs: the characters of the multibyte string at src,
// at most n stored at dst (none if it is null), and how many.
func Mbstowcs(dst, src Ptr, n uint64) uint64 {
	k := uint64(0)
	for i := 0; Addr(dst) == 0 || k < n; {
		if Load8(Add(src, i)) == 0 {
			if Addr(dst) != 0 {
				wset(dst, int(k), 0)
			}
			return k
		}
		r, size, ok := decodeMB(Add(src, i), utf8.UTFMax)
		if !ok {
			SetErrno(EILSEQ)
			return ^uint64(0)
		}
		if Addr(dst) != 0 {
			wset(dst, int(k), int32(r))
		}
		k++
		i += size
	}
	return k
}

// Wcstombs is wcstombs: the multibyte characters of the wide string at
// src, at most n bytes of whole ones stored at dst (none if it is null),
// and how many bytes.
func Wcstombs(dst, src Ptr, n uint64) uint64 {
	k := uint64(0)
	for i := 0; ; i++ {
		c := wat(src, i)
		if c == 0 {
			if Addr(dst) != 0 && k < n {
				Store8(Add(dst, int(k)), 0)
			}
			return k
		}
		if c < 0 || c > utf8.MaxRune || c >= 0xd800 && c < 0xe000 {
			SetErrno(EILSEQ)
			return ^uint64(0)
		}
		b := utf8.AppendRune(nil, rune(c))
		if Addr(dst) != 0 {
			if k+uint64(len(b)) > n {
				return k
			}
			for j, x := range b {
				Store8(Add(dst, int(k)+j), x)
			}
		}
		k += uint64(len(b))
	}
}

// Mbsrtowcs is mbsrtowcs: mbstowcs from *src, which it moves past what
// it converted (null if it reached the end).
func Mbsrtowcs(dst, src Ptr, n uint64, ps Ptr) uint64 {
	s := LoadPtr(src)
	k := uint64(0)
	i := 0
	for Addr(dst) == 0 || k < n {
		if Load8(Add(s, i)) == 0 {
			if Addr(dst) != 0 {
				wset(dst, int(k), 0)
				StorePtr(src, Ptr{})
			}
			return k
		}
		r, size, ok := decodeMB(Add(s, i), utf8.UTFMax)
		if !ok {
			SetErrno(EILSEQ)
			if Addr(dst) != 0 {
				StorePtr(src, Add(s, i))
			}
			return ^uint64(0)
		}
		if Addr(dst) != 0 {
			wset(dst, int(k), int32(r))
		}
		k++
		i += size
	}
	StorePtr(src, Add(s, i))
	return k
}

// Wcsrtombs is wcsrtombs: wcstombs from *src, which it moves past what
// it converted (null if it reached the end).
func Wcsrtombs(dst, src Ptr, n uint64, ps Ptr) uint64 {
	s := LoadPtr(src)
	k := uint64(0)
	for i := 0; ; i++ {
		c := wat(s, i)
		if c == 0 {
			if Addr(dst) != 0 {
				if k < n {
					Store8(Add(dst, int(k)), 0)
					StorePtr(src, Ptr{})
				} else {
					StorePtr(src, Add(s, 4*i))
				}
			}
			return k
		}
		if c < 0 || c > utf8.MaxRune || c >= 0xd800 && c < 0xe000 {
			SetErrno(EILSEQ)
			if Addr(dst) != 0 {
				StorePtr(src, Add(s, 4*i))
			}
			return ^uint64(0)
		}
		b := utf8.AppendRune(nil, rune(c))
		if Addr(dst) != 0 {
			if k+uint64(len(b)) > n {
				StorePtr(src, Add(s, 4*i))
				return k
			}
			for j, x := range b {
				Store8(Add(dst, int(k)+j), x)
			}
		}
		k += uint64(len(b))
	}
}

// stdlib.h's other C23 functions, and stdio.h's fgetpos and fsetpos.

// Strfromd is strfromd: snprintf of one double, of a format of a
// precision and a conversion.
func Strfromd(s Ptr, n uint64, format Ptr, fp float64) int32 {
	return Snprintf(s, n, format, fp)
}

func Strfromf(s Ptr, n uint64, format Ptr, fp float32) int32 {
	return Snprintf(s, n, format, float64(fp))
}

func FreeSized(p Ptr, n uint64)               {}
func FreeAlignedSized(p Ptr, align, n uint64) {}

// Memalignment is memalignment: the greatest power of two p's address is
// a multiple of.
func Memalignment(p Ptr) uint64 {
	a := uint64(Addr(p))
	return a & -a
}

// CallOnce is call_once: pthread_once's, which a once_flag is.
func CallOnce(flag, f Ptr) { PthreadOnce(flag, f) }

var (
	quickMu sync.Mutex
	quickFn []Ptr
)

func AtQuickExit(f Ptr) int32 {
	FuncOf[func()](f)
	quickMu.Lock()
	quickFn = append(quickFn, f)
	quickMu.Unlock()
	return 0
}

// QuickExit is quick_exit: the functions at_quick_exit registered, the
// last first, and _Exit.
func QuickExit(status int32) {
	for {
		quickMu.Lock()
		if len(quickFn) == 0 {
			quickMu.Unlock()
			break
		}
		f := quickFn[len(quickFn)-1]
		quickFn = quickFn[:len(quickFn)-1]
		quickMu.Unlock()
		FuncOf[func()](f)()
	}
	Fflush(Ptr{})
	os.Exit(int(status))
}

func Strxfrm(d, s Ptr, n uint64) uint64 {
	k := uint64(Strlen(s))
	if k < n {
		Memmove(d, s, uintptr(k+1))
	}
	return k
}

// An fpos_t is a stream's offset.

func Fgetpos(f, pos Ptr) int32 {
	off := Ftell(f)
	if off < 0 {
		return -1
	}
	Store64(pos, uint64(off))
	return 0
}

func Fsetpos(f, pos Ptr) int32 { return Fseek(f, int64(Load64(pos)), 0) }
