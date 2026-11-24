// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wchar

import (
	c23 "internal/c23"
	c23_stdio "internal/c23/stdio"
	"io"
	"unicode"
	"unicode/utf8"
)

// POSIX's wide string functions, and those of a locale (the _l
// functions), which are the global locale's: the library's locales
// compare and convert alike.

func WcscollL(a, b, _ c23.Ptr) int32 { return Wcscoll(a, b) }

func WcsxfrmL(d, s c23.Ptr, n uint64, _ c23.Ptr) uint64 { return Wcsxfrm(d, s, n) }

func WcsftimeL(s c23.Ptr, max uint64, format, tm, _ c23.Ptr) uint64 {
	return Wcsftime(s, max, format, tm)
}

func WcscasecmpL(a, b, _ c23.Ptr) int32 { return Wcscasecmp(a, b) }

func WcsncasecmpL(a, b c23.Ptr, n uint64, _ c23.Ptr) int32 { return Wcsncasecmp(a, b, n) }

// wideWidth are the characters of East Asian width W (wide) and F
// (full-width), as Unicode 15.1's EastAsianWidth.txt has them, in order.
// Go's unicode has no table of them.
var wideWidth = [][2]rune{
	{0x1100, 0x115f}, {0x231a, 0x231b}, {0x2329, 0x232a}, {0x23e9, 0x23ec}, {0x23f0, 0x23f0},
	{0x23f3, 0x23f3}, {0x25fd, 0x25fe}, {0x2614, 0x2615}, {0x2648, 0x2653}, {0x267f, 0x267f},
	{0x2693, 0x2693}, {0x26a1, 0x26a1}, {0x26aa, 0x26ab}, {0x26bd, 0x26be}, {0x26c4, 0x26c5},
	{0x26ce, 0x26ce}, {0x26d4, 0x26d4}, {0x26ea, 0x26ea}, {0x26f2, 0x26f3}, {0x26f5, 0x26f5},
	{0x26fa, 0x26fa}, {0x26fd, 0x26fd}, {0x2705, 0x2705}, {0x270a, 0x270b}, {0x2728, 0x2728},
	{0x274c, 0x274c}, {0x274e, 0x274e}, {0x2753, 0x2755}, {0x2757, 0x2757}, {0x2795, 0x2797},
	{0x27b0, 0x27b0}, {0x27bf, 0x27bf}, {0x2b1b, 0x2b1c}, {0x2b50, 0x2b50}, {0x2b55, 0x2b55},
	{0x2e80, 0x2e99}, {0x2e9b, 0x2ef3}, {0x2f00, 0x2fd5}, {0x2ff0, 0x303e},
	{0x3041, 0x3096}, {0x3099, 0x30ff}, {0x3105, 0x312f}, {0x3131, 0x318e}, {0x3190, 0x31e3},
	{0x31ef, 0x321e}, {0x3220, 0x3247}, {0x3250, 0x4dbf}, {0x4e00, 0xa48c}, {0xa490, 0xa4c6},
	{0xa960, 0xa97c}, {0xac00, 0xd7a3}, {0xf900, 0xfaff}, {0xfe10, 0xfe19}, {0xfe30, 0xfe52},
	{0xfe54, 0xfe66}, {0xfe68, 0xfe6b}, {0xff01, 0xff60}, {0xffe0, 0xffe6},
	{0x16fe0, 0x16fe4}, {0x16ff0, 0x16ff1}, {0x17000, 0x187f7}, {0x18800, 0x18cd5},
	{0x18d00, 0x18d08}, {0x1aff0, 0x1aff3}, {0x1aff5, 0x1affb}, {0x1affd, 0x1affe},
	{0x1b000, 0x1b122}, {0x1b132, 0x1b132}, {0x1b150, 0x1b152}, {0x1b155, 0x1b155},
	{0x1b164, 0x1b167}, {0x1b170, 0x1b2fb}, {0x1f004, 0x1f004}, {0x1f0cf, 0x1f0cf},
	{0x1f18e, 0x1f18e}, {0x1f191, 0x1f19a}, {0x1f200, 0x1f202}, {0x1f210, 0x1f23b},
	{0x1f240, 0x1f248}, {0x1f250, 0x1f251}, {0x1f260, 0x1f265}, {0x1f300, 0x1f320},
	{0x1f32d, 0x1f335}, {0x1f337, 0x1f37c}, {0x1f37e, 0x1f393}, {0x1f3a0, 0x1f3ca},
	{0x1f3cf, 0x1f3d3}, {0x1f3e0, 0x1f3f0}, {0x1f3f4, 0x1f3f4}, {0x1f3f8, 0x1f43e},
	{0x1f440, 0x1f440}, {0x1f442, 0x1f4fc}, {0x1f4ff, 0x1f53d}, {0x1f54b, 0x1f54e},
	{0x1f550, 0x1f567}, {0x1f57a, 0x1f57a}, {0x1f595, 0x1f596}, {0x1f5a4, 0x1f5a4},
	{0x1f5fb, 0x1f64f}, {0x1f680, 0x1f6c5}, {0x1f6cc, 0x1f6cc}, {0x1f6d0, 0x1f6d2},
	{0x1f6d5, 0x1f6d7}, {0x1f6dc, 0x1f6df}, {0x1f6eb, 0x1f6ec}, {0x1f6f4, 0x1f6fc},
	{0x1f7e0, 0x1f7eb}, {0x1f7f0, 0x1f7f0}, {0x1f90c, 0x1f93a}, {0x1f93c, 0x1f945},
	{0x1f947, 0x1f9ff}, {0x1fa70, 0x1fa7c}, {0x1fa80, 0x1fa88}, {0x1fa90, 0x1fabd},
	{0x1fabf, 0x1fac5}, {0x1face, 0x1fadb}, {0x1fae0, 0x1fae8}, {0x1faf0, 0x1faf8},
	{0x20000, 0x2fffd}, {0x30000, 0x3fffd},
}

func isWide(r rune) bool {
	lo, hi := 0, len(wideWidth)
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		if wideWidth[m][1] < r {
			lo = m + 1
		} else {
			hi = m
		}
	}
	return lo < len(wideWidth) && wideWidth[lo][0] <= r
}

// width is wcwidth's, as glibc's in a UTF-8 locale: 0 for the null
// character, nonspacing and enclosing marks, format characters (but the
// soft hyphen) and Hangul's medial vowels and final consonants; 2 for
// the East Asian wide and full-width; -1 for controls, surrogates and
// what Unicode has not assigned; 1 for the rest.
func width(c int32) int {
	r := rune(c)
	switch {
	case r == 0:
		return 0
	case r < 0x20 || r >= 0x7f && r < 0xa0:
		return -1
	case r < 0x300: // Latin-1 and Latin Extended, and U+00AD, the soft hyphen
		return 1
	case r > unicode.MaxRune || r >= 0xd800 && r < 0xe000:
		return -1
	case unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf),
		r >= 0x1160 && r <= 0x11ff, r >= 0xd7b0 && r <= 0xd7ff:
		return 0
	case !unicode.IsPrint(r) && !unicode.In(r, unicode.Zs, unicode.Co):
		return -1
	case isWide(r):
		return 2
	}
	return 1
}

// Wcwidth is wcwidth: the columns c takes (width).
func Wcwidth(c int32) int32 { return int32(width(c)) }

// Wcswidth is wcswidth: the columns of the first n wide characters at s
// (to its null), or -1 if one of them is not printable.
func Wcswidth(s c23.Ptr, n uint64) int32 {
	w := 0
	for i := 0; uint64(i) < n; i++ {
		c := c23.Wat(s, i)
		if c == 0 {
			break
		}
		k := width(c)
		if k < 0 {
			return -1
		}
		w += k
	}
	return int32(w)
}

// Wcsdup is wcsdup.
func Wcsdup(s c23.Ptr) c23.Ptr {
	n := Wcslen(s) + 1
	d := c23.Malloc(4 * n)
	c23.Memmove(d, s, uintptr(4*n))
	return d
}

// Wcpcpy is wcpcpy: wcscpy returning the end of what it copied.
func Wcpcpy(d, s c23.Ptr) c23.Ptr {
	n := Wcslen(s)
	c23.Memmove(d, s, uintptr(4*(n+1)))
	return wptr(d, n)
}

// Wcpncpy is wcpncpy: wcsncpy returning where the first null it stored
// is, or d+n.
func Wcpncpy(d, s c23.Ptr, n uint64) c23.Ptr {
	k := Wcsnlen(s, n)
	Wcsncpy(d, s, n)
	return wptr(d, k)
}

// Wcsnlen is wcsnlen.
func Wcsnlen(s c23.Ptr, max uint64) uint64 {
	n := uint64(0)
	for n < max && c23.Wat(s, int(n)) != 0 {
		n++
	}
	return n
}

func wlower(c int32) int32 {
	if c < 0 || c > unicode.MaxRune {
		return c
	}
	return int32(unicode.ToLower(rune(c)))
}

// Wcscasecmp is wcscasecmp: wcscmp of the characters' lower cases.
func Wcscasecmp(a, b c23.Ptr) int32 { return Wcsncasecmp(a, b, ^uint64(0)) }

// Wcsncasecmp is wcsncasecmp.
func Wcsncasecmp(a, b c23.Ptr, n uint64) int32 {
	for i := 0; uint64(i) < n; i++ {
		x, y := wlower(c23.Wat(a, i)), wlower(c23.Wat(b, i))
		if x != y || x == 0 {
			return x - y
		}
	}
	return 0
}

// Mbsnrtowcs is mbsnrtowcs: mbsrtowcs of at most the nms bytes at *src.
// A character that the nms bytes end within is not converted: *src is
// left at it (glibc keeps its first bytes in *ps, and moves past them).
func Mbsnrtowcs(dst, src c23.Ptr, nms, n uint64, ps c23.Ptr) uint64 {
	s := c23.LoadPtr(src)
	k := uint64(0)
	i := uint64(0)
	for (c23.Addr(dst) == 0 || k < n) && i < nms {
		var b []byte
		for j := i; j < nms && j < i+utf8.UTFMax; j++ {
			b = append(b, c23.Load8(c23.Add(s, int(j))))
			if utf8.FullRune(b) {
				break
			}
		}
		if b[0] == 0 {
			if c23.Addr(dst) != 0 {
				c23.Wset(dst, int(k), 0)
				c23.StorePtr(src, c23.Ptr(nil))
			}
			return k
		}
		if !utf8.FullRune(b) {
			break
		}
		r, size := utf8.DecodeRune(b)
		if r == utf8.RuneError && size <= 1 {
			c23.SetErrno(c23.EILSEQ)
			if c23.Addr(dst) != 0 {
				c23.StorePtr(src, c23.Add(s, int(i)))
			}
			return ^uint64(0)
		}
		if c23.Addr(dst) != 0 {
			c23.Wset(dst, int(k), int32(r))
		}
		k++
		i += uint64(size)
	}
	if c23.Addr(dst) != 0 {
		c23.StorePtr(src, c23.Add(s, int(i)))
	}
	return k
}

// Wcsnrtombs is wcsnrtombs: wcsrtombs of at most the nwc wide
// characters at *src.
func Wcsnrtombs(dst, src c23.Ptr, nwc, n uint64, ps c23.Ptr) uint64 {
	s := c23.LoadPtr(src)
	k := uint64(0)
	for i := 0; uint64(i) < nwc; i++ {
		c := c23.Wat(s, i)
		if c == 0 {
			if c23.Addr(dst) != 0 {
				if k < n {
					c23.Store8(c23.Add(dst, int(k)), 0)
					c23.StorePtr(src, c23.Ptr(nil))
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
			copy(c23.BytesAt(c23.Add(dst, int(k)), uintptr(len(b))), b)
		}
		k += uint64(len(b))
	}
	if c23.Addr(dst) != 0 {
		c23.StorePtr(src, c23.Add(s, int(4*nwc)))
	}
	return k
}

// A wmemFile is open_wmemstream's memory: the stream's bytes, UTF-8,
// are stored as the wide characters they encode, in an array it grows,
// whose address and the position (in wide characters) it keeps at *bufp
// and *sizep.
type wmemFile struct {
	buf         c23.Ptr
	size        int // the wide characters buf holds
	pos, end    int
	pend        []byte // a character's first bytes
	bufp, sizep c23.Ptr
}

func (m *wmemFile) Read(b []byte) (int, error) { return 0, io.EOF }

func (m *wmemFile) Write(b []byte) (int, error) {
	m.pend = append(m.pend, b...)
	for len(m.pend) > 0 && utf8.FullRune(m.pend) {
		r, n := utf8.DecodeRune(m.pend)
		if r == utf8.RuneError && n <= 1 {
			m.pend = m.pend[1:]
			m.sync()
			return len(b), c23.ErrnoErr(c23.EILSEQ)
		}
		m.pend = m.pend[n:]
		m.put(r)
	}
	m.sync()
	return len(b), nil
}

// put stores r at the position, growing buf.
func (m *wmemFile) put(r rune) {
	if need := m.pos + 2; need > m.size {
		n := max(need, 2*m.size, 16)
		nb := c23.Malloc(uint64(4 * n))
		if m.size > 0 {
			c23.Memmove(nb, m.buf, uintptr(4*m.size))
		}
		m.buf, m.size = nb, n
	}
	for i := m.end; i < m.pos; i++ { // a seek past the end: nulls between
		c23.Wset(m.buf, i, 0)
	}
	c23.Wset(m.buf, m.pos, int32(r))
	m.pos++
	m.end = max(m.end, m.pos)
	c23.Wset(m.buf, m.end, 0)
}

// Seek moves to a position in wide characters.
func (m *wmemFile) Seek(off int64, whence int) (int64, error) {
	var base int64
	switch whence {
	case io.SeekCurrent:
		base = int64(m.pos)
	case io.SeekEnd:
		base = int64(m.end)
	}
	p := base + off
	if p < 0 {
		return 0, c23.ErrnoErr(c23.EINVAL)
	}
	m.pend = nil
	m.pos = int(p)
	m.sync()
	return p, nil
}

func (m *wmemFile) sync() {
	if m.buf == nil {
		m.buf, m.size = c23.Malloc(4), 1
	}
	c23.StorePtr(m.bufp, m.buf)
	c23.StoreLong(m.sizep, int64(min(m.pos, m.end)))
}

func (m *wmemFile) Close() { m.sync() }

// OpenWmemstream is open_wmemstream: open_memstream of wide characters.
// Its positions (fseek's and ftell's) are in wide characters.
func OpenWmemstream(bufp, sizep c23.Ptr) c23.Ptr {
	m := &wmemFile{bufp: bufp, sizep: sizep}
	m.sync()
	return c23_stdio.NewMemStream(m, false, true)
}
