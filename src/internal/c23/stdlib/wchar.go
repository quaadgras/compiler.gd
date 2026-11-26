// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stdlib

import (
	c23 "internal/c23"
	c23_pthread "internal/c23/pthread"
	c23_stdio "internal/c23/stdio"
	"os"
	"sync"
	"unicode/utf8"
	"unsafe"
)

func Mblen(s *c23.Void, n uint64) int32 { return Mbtowc((*c23.Void)(nil), s, n) }

func Mbtowc(pwc, s *c23.Void, n uint64) int32 {
	if c23.Addr(s) == 0 {
		return 0 // no state
	}
	if n == 0 {
		return -1
	}
	if c23.Load8(s) == 0 {
		if c23.Addr(pwc) != 0 {
			c23.Wset(pwc, 0, 0)
		}
		return 0
	}
	r, size, ok := c23.DecodeMB(s, n)
	if !ok {
		c23.SetErrno(c23.EILSEQ)
		return -1
	}
	if c23.Addr(pwc) != 0 {
		c23.Wset(pwc, 0, int32(r))
	}
	return int32(size)
}

func Wctomb(s *c23.Void, wc int32) int32 {
	if c23.Addr(s) == 0 {
		return 0
	}
	n := c23.PutRune(s, uint32(wc))
	if n == c23.MbInvalid {
		return -1
	}
	return int32(n)
}

// Mbstowcs is mbstowcs: the characters of the multibyte string at src,
// at most n stored at dst (none if it is null), and how many.
func Mbstowcs(dst, src *c23.Void, n uint64) uint64 {
	k := uint64(0)
	for i := 0; c23.Addr(dst) == 0 || k < n; {
		if c23.Load8(c23.Add(src, i)) == 0 {
			if c23.Addr(dst) != 0 {
				c23.Wset(dst, int(k), 0)
			}
			return k
		}
		r, size, ok := c23.DecodeMB(c23.Add(src, i), utf8.UTFMax)
		if !ok {
			c23.SetErrno(c23.EILSEQ)
			return ^uint64(0)
		}
		if c23.Addr(dst) != 0 {
			c23.Wset(dst, int(k), int32(r))
		}
		k++
		i += size
	}
	return k
}

// Wcstombs is wcstombs: the multibyte characters of the wide string at
// src, at most n bytes of whole ones stored at dst (none if it is null),
// and how many bytes.
func Wcstombs(dst, src *c23.Void, n uint64) uint64 {
	k := uint64(0)
	for i := 0; ; i++ {
		c := c23.Wat(src, i)
		if c == 0 {
			if c23.Addr(dst) != 0 && k < n {
				c23.Store8(c23.Add(dst, int(k)), 0)
			}
			return k
		}
		if c < 0 || c > utf8.MaxRune || c >= 0xd800 && c < 0xe000 {
			c23.SetErrno(c23.EILSEQ)
			return ^uint64(0)
		}
		b := utf8.AppendRune(nil, rune(c))
		if c23.Addr(dst) != 0 {
			if k+uint64(len(b)) > n {
				return k
			}
			for j, x := range b {
				c23.Store8(c23.Add(dst, int(k)+j), x)
			}
		}
		k += uint64(len(b))
	}
}

// Strfromd is strfromd: snprintf of one double, of a format of a
// precision and a conversion.
func Strfromd(s *c23.Void, n uint64, format *c23.Void, fp float64) int32 {
	return c23_stdio.Snprintf((*c23.Char)(unsafe.Pointer(s)), n, (*c23.Char)(unsafe.Pointer(format)), fp)
}

func Strfromf(s *c23.Void, n uint64, format *c23.Void, fp float32) int32 {
	return c23_stdio.Snprintf((*c23.Char)(unsafe.Pointer(s)), n, (*c23.Char)(unsafe.Pointer(format)), float64(fp))
}

// FreeSized and FreeAlignedSized are free_sized and free_aligned_sized:
// free, which does not check the size or alignment.
func FreeSized(p *c23.Void, n uint64) { c23.Free(p) }

func FreeAlignedSized(p *c23.Void, align, n uint64) { c23.Free(p) }

// Memalignment is memalignment: the greatest power of two p's address is
// a multiple of.
func Memalignment(p *c23.Void) uint64 {
	a := uint64(c23.Addr(p))
	return a & -a
}

// CallOnce is call_once: pthread_once's, which a once_flag is.
func CallOnce(flag, f *c23.Void) { c23_pthread.PthreadOnce(flag, f) }

var quickMu sync.Mutex

var quickFn []*c23.Void

func AtQuickExit(f *c23.Void) int32 {
	c23.FuncOf[func()](f)
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
		c23.FuncOf[func()](f)()
	}
	c23.Fflush((*c23.Void)(nil))
	os.Exit(int(status))
}
