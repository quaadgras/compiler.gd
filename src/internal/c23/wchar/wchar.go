// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wchar

import (
	c23 "internal/c23"
	c23_stdio "internal/c23/stdio"
	c23_stdlib "internal/c23/stdlib"
	c23_time "internal/c23/time"
	"unicode/utf8"
)

func wptr(s *c23.Void, i uint64) *c23.Void { return c23.Add(s, int(4*i)) }

func wcsLen(s *c23.Void) int { return int(Wcslen(s)) }

func wideGo(s *c23.Void) string { return string(wideRunes(s)) }

func Wcscoll(a, b *c23.Void) int32 { return Wcscmp(a, b) }

func Wmemcpy(d, s *c23.Void, n uint64) *c23.Void { c23.Memmove(d, s, uintptr(4*n)); return d }

func Wmemmove(d, s *c23.Void, n uint64) *c23.Void { c23.Memmove(d, s, uintptr(4*n)); return d }

// wideRunes are the wide characters of the string at s.
func wideRunes(s *c23.Void) []rune {
	var r []rune
	for i := 0; ; i++ {
		c := c23.Wat(s, i)
		if c == 0 {
			return r
		}
		r = append(r, rune(c))
	}
}

// storeWide stores r and a terminating null at s.
func storeWide(s *c23.Void, r []rune) {
	for i, c := range r {
		c23.Wset(s, i, int32(c))
	}
	c23.Wset(s, len(r), 0)
}

func Wcsncpy(d, s *c23.Void, n uint64) *c23.Void {
	i := uint64(0)
	for ; i < n; i++ {
		c := c23.Wat(s, int(i))
		c23.Wset(d, int(i), c)
		if c == 0 {
			break
		}
	}
	for ; i < n; i++ {
		c23.Wset(d, int(i), 0)
	}
	return d
}

func Wcsncat(d, s *c23.Void, n uint64) *c23.Void {
	k := wcsLen(d)
	i := 0
	for ; uint64(i) < n; i++ {
		c := c23.Wat(s, i)
		if c == 0 {
			break
		}
		c23.Wset(d, k+i, c)
	}
	c23.Wset(d, k+i, 0)
	return d
}

func Wcsrchr(s *c23.Void, c int32) *c23.Void {
	var last *c23.Void
	for i := 0; ; i++ {
		x := c23.Wat(s, i)
		if x == c {
			last = c23.Add(s, 4*i)
		}
		if x == 0 {
			return last
		}
	}
}

// wcsSpan is the length of the prefix of s whose characters are (in) or
// are not in set.
func wcsSpan(s, set *c23.Void, in bool) uint64 {
	cs := wideRunes(set)
	n := uint64(0)
	for {
		c := c23.Wat(s, int(n))
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

func Wcsspn(s, set *c23.Void) uint64 { return wcsSpan(s, set, true) }

func Wcscspn(s, set *c23.Void) uint64 { return wcsSpan(s, set, false) }

func Wcspbrk(s, set *c23.Void) *c23.Void {
	n := wcsSpan(s, set, false)
	if c23.Wat(s, int(n)) == 0 {
		return (*c23.Void)(nil)
	}
	return wptr(s, n)
}

func Wcsstr(h, n *c23.Void) *c23.Void {
	needle := wideRunes(n)
	for i := 0; ; i++ {
		match := true
		for j, c := range needle {
			x := c23.Wat(h, i+j)
			if rune(x) != c {
				match = false
				break
			}
		}
		if match {
			return c23.Add(h, 4*i)
		}
		if c23.Wat(h, i) == 0 {
			return (*c23.Void)(nil)
		}
	}
}

// Wcstok is C's wcstok, which keeps its place in *ptr.
func Wcstok(s, delim, ptr *c23.Void) *c23.Void {
	if c23.Addr(s) == 0 {
		s = c23.LoadPtr(ptr)
		if c23.Addr(s) == 0 {
			return (*c23.Void)(nil)
		}
	}
	s = wptr(s, wcsSpan(s, delim, true))
	if c23.Wat(s, 0) == 0 {
		c23.StorePtr(ptr, (*c23.Void)(nil))
		return (*c23.Void)(nil)
	}
	e := wptr(s, wcsSpan(s, delim, false))
	if c23.Wat(e, 0) == 0 {
		c23.StorePtr(ptr, (*c23.Void)(nil))
	} else {
		c23.Wset(e, 0, 0)
		c23.StorePtr(ptr, c23.Add(e, 4))
	}
	return s
}

func Wcsxfrm(d, s *c23.Void, n uint64) uint64 {
	k := Wcslen(s)
	if k < n {
		Wmemcpy(d, s, k+1)
	}
	return k
}

func Wmemchr(s *c23.Void, c int32, n uint64) *c23.Void {
	for i := uint64(0); i < n; i++ {
		if c23.Wat(s, int(i)) == c {
			return wptr(s, i)
		}
	}
	return (*c23.Void)(nil)
}

func Wmemcmp(a, b *c23.Void, n uint64) int32 {
	for i := 0; uint64(i) < n; i++ {
		x, y := c23.Wat(a, i), c23.Wat(b, i)
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
	}
	return 0
}

func Wmemset(s *c23.Void, c int32, n uint64) *c23.Void {
	for i := 0; uint64(i) < n; i++ {
		c23.Wset(s, i, c)
	}
	return s
}

func Wcsftime(s *c23.Void, max uint64, format, tm *c23.Void) uint64 {
	buf := c23.Malloc(4096)
	n := c23_time.Strftime(buf, 4096, c23.CString(wideGo(format)), tm)
	r := []rune(string(c23.BytesAt(buf, uintptr(n))))
	if uint64(len(r)) >= max {
		return 0
	}
	storeWide(s, r)
	return uint64(len(r))
}

func Wcstol(s, end *c23.Void, base int32) int64 {
	return c23.WideNumber(s, end, func(ns, ne *c23.Void) int64 { return c23_stdlib.Strtol(ns, ne, base) })
}

func Wcstoul(s, end *c23.Void, base int32) uint64 {
	return c23.WideNumber(s, end, func(ns, ne *c23.Void) uint64 { return c23_stdlib.Strtoul(ns, ne, base) })
}

func Wcstod(s, end *c23.Void) float64 {
	return c23.WideNumber(s, end, func(ns, ne *c23.Void) float64 { return c23_stdlib.Strtod(ns, ne) })
}

func Wcstof(s, end *c23.Void) float32 {
	return c23.WideNumber(s, end, func(ns, ne *c23.Void) float32 { return c23_stdlib.Strtof(ns, ne) })
}

const weofC = 0xffffffff

func Fputwc(c int32, f *c23.Void) uint32 {
	if c < 0 || c > utf8.MaxRune || c >= 0xd800 && c < 0xe000 {
		c23.SetErrno(c23.EILSEQ)
		return weofC
	}
	if !c23.FileOf(f).Write(utf8.AppendRune(nil, rune(c))) {
		return weofC
	}
	return uint32(c)
}

func Putwchar(c int32) uint32 { return Fputwc(c, c23.Stdio(1)) }

func Fputws(s, f *c23.Void) int32 {
	if !c23.FileOf(f).Write([]byte(wideGo(s))) {
		return -1
	}
	return 0
}

func Fgetwc(f *c23.Void) uint32 {
	fl := c23.FileOf(f)
	var b []byte
	for {
		c := fl.Getc()
		if c < 0 {
			if len(b) > 0 {
				c23.SetErrno(c23.EILSEQ)
			}
			return weofC
		}
		b = append(b, byte(c))
		if utf8.FullRune(b) {
			r, n := utf8.DecodeRune(b)
			if r == utf8.RuneError && n <= 1 {
				c23.SetErrno(c23.EILSEQ)
				return weofC
			}
			return uint32(r)
		}
	}
}

func Getwchar() uint32 { return Fgetwc(c23.Stdio(0)) }

func Ungetwc(c uint32, f *c23.Void) uint32 {
	if c == weofC {
		return weofC
	}
	b := utf8.AppendRune(nil, rune(c))
	fl := c23.FileOf(f)
	for i := len(b) - 1; i >= 0; i-- {
		fl.Unget = append(fl.Unget, b[i])
	}
	return c
}

func Fgetws(s *c23.Void, n int32, f *c23.Void) *c23.Void {
	if n <= 0 {
		return (*c23.Void)(nil)
	}
	i := 0
	for i < int(n)-1 {
		c := Fgetwc(f)
		if c == weofC {
			break
		}
		c23.Wset(s, i, int32(c))
		i++
		if c == '\n' {
			break
		}
	}
	if i == 0 {
		return (*c23.Void)(nil)
	}
	c23.Wset(s, i, 0)
	return s
}

// Fwide is fwide: streams take both wide and narrow characters, and keep
// no orientation but the one asked for.
func Fwide(f *c23.Void, mode int32) int32 {
	switch {
	case mode > 0:
		return 1
	case mode < 0:
		return -1
	}
	return 0
}

func Fwprintf(f, format *c23.Void, va ...any) int32 { return Vfwprintf(f, format, c23.VaStart(va)) }

func Wprintf(format *c23.Void, va ...any) int32 {
	return Vfwprintf(c23.Stdio(1), format, c23.VaStart(va))
}

func Vwprintf(format, ap *c23.Void) int32 { return Vfwprintf(c23.Stdio(1), format, ap) }

func Vfwprintf(f, format, ap *c23.Void) int32 {
	b := c23.FormatC(wideGo(format), c23.VaOf(ap))
	if !c23.FileOf(f).Write(b) {
		return -1
	}
	return int32(utf8.RuneCount(b))
}

func Swprintf(s *c23.Void, n uint64, format *c23.Void, va ...any) int32 {
	return Vswprintf(s, n, format, c23.VaStart(va))
}

func Vswprintf(s *c23.Void, n uint64, format, ap *c23.Void) int32 {
	r := []rune(string(c23.FormatC(wideGo(format), c23.VaOf(ap))))
	if uint64(len(r)) >= n {
		if n > 0 {
			storeWide(s, r[:n-1])
		}
		return -1
	}
	storeWide(s, r)
	return int32(len(r))
}

func Fwscanf(f, format *c23.Void, va ...any) int32 { return Vfwscanf(f, format, c23.VaStart(va)) }

func Wscanf(format *c23.Void, va ...any) int32 {
	return Vfwscanf(c23.Stdio(0), format, c23.VaStart(va))
}

func Vwscanf(format, ap *c23.Void) int32 { return Vfwscanf(c23.Stdio(0), format, ap) }

func Swscanf(s, format *c23.Void, va ...any) int32 { return Vswscanf(s, format, c23.VaStart(va)) }

func Vfwscanf(f, format, ap *c23.Void) int32 {
	return c23_stdio.Vfscanf(f, c23.CString(wideGo(format)), ap)
}

func Vswscanf(s, format, ap *c23.Void) int32 {
	return c23_stdio.Vsscanf(c23.CString(wideGo(s)), c23.CString(wideGo(format)), ap)
}

// Mbsrtowcs is mbsrtowcs: mbstowcs from *src, which it moves past what
// it converted (null if it reached the end).
func Mbsrtowcs(dst, src *c23.Void, n uint64, ps *c23.Void) uint64 {
	s := c23.LoadPtr(src)
	k := uint64(0)
	i := 0
	for c23.Addr(dst) == 0 || k < n {
		if c23.Load8(c23.Add(s, i)) == 0 {
			if c23.Addr(dst) != 0 {
				c23.Wset(dst, int(k), 0)
				c23.StorePtr(src, (*c23.Void)(nil))
			}
			return k
		}
		r, size, ok := c23.DecodeMB(c23.Add(s, i), utf8.UTFMax)
		if !ok {
			c23.SetErrno(c23.EILSEQ)
			if c23.Addr(dst) != 0 {
				c23.StorePtr(src, c23.Add(s, i))
			}
			return ^uint64(0)
		}
		if c23.Addr(dst) != 0 {
			c23.Wset(dst, int(k), int32(r))
		}
		k++
		i += size
	}
	c23.StorePtr(src, c23.Add(s, i))
	return k
}

// Wcsrtombs is wcsrtombs: wcstombs from *src, which it moves past what
// it converted (null if it reached the end).
func Wcsrtombs(dst, src *c23.Void, n uint64, ps *c23.Void) uint64 {
	s := c23.LoadPtr(src)
	k := uint64(0)
	for i := 0; ; i++ {
		c := c23.Wat(s, i)
		if c == 0 {
			if c23.Addr(dst) != 0 {
				if k < n {
					c23.Store8(c23.Add(dst, int(k)), 0)
					c23.StorePtr(src, (*c23.Void)(nil))
				} else {
					c23.StorePtr(src, c23.Add(s, 4*i))
				}
			}
			return k
		}
		if c < 0 || c > utf8.MaxRune || c >= 0xd800 && c < 0xe000 {
			c23.SetErrno(c23.EILSEQ)
			if c23.Addr(dst) != 0 {
				c23.StorePtr(src, c23.Add(s, 4*i))
			}
			return ^uint64(0)
		}
		b := utf8.AppendRune(nil, rune(c))
		if c23.Addr(dst) != 0 {
			if k+uint64(len(b)) > n {
				c23.StorePtr(src, c23.Add(s, 4*i))
				return k
			}
			for j, x := range b {
				c23.Store8(c23.Add(dst, int(k)+j), x)
			}
		}
		k += uint64(len(b))
	}
}
