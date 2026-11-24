// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wchar

import (
	c23 "internal/c23"
)

const weof = 0xffffffff

func Mbrtowc(pc c23.Ptr, s c23.Ptr, n uint64, ps c23.Ptr) uint64 {
	r, k := c23.Mbrto(s, n, ps, &c23.MbrtowcState)
	if k < c23.MbStored && c23.Addr(pc) != 0 {
		c23.Store32(pc, uint32(r))
	}
	return k
}

func Mbrlen(s c23.Ptr, n uint64, ps c23.Ptr) uint64 {
	_, k := c23.Mbrto(s, n, ps, &c23.MbrlenState)
	return k
}

func Wcrtomb(s c23.Ptr, c int32, ps c23.Ptr) uint64 {
	st := c23.StateOf(ps, &c23.WcrtombState)
	st.Reset()
	if c23.Addr(s) == 0 {
		return 1
	}
	return c23.PutRune(s, uint32(c))
}

// Mbsinit is mbsinit: whether ps is null or an initial state.
func Mbsinit(ps c23.Ptr) int32 {
	return c23.B2I(c23.Addr(ps) == 0 || c23.Load32(ps) == 0)
}

func Btowc(c int32) uint32 {
	if c < 0 || c >= 0x80 {
		return weof
	}
	return uint32(c)
}

func Wctob(c uint32) int32 {
	if c >= 0x80 {
		return -1
	}
	return int32(c)
}

func Wcslen(s c23.Ptr) uint64 {
	n := uint64(0)
	for c23.Load32(c23.Add(s, int(4*n))) != 0 {
		n++
	}
	return n
}

func Wcscmp(a, b c23.Ptr) int32 { return Wcsncmp(a, b, ^uint64(0)) }

func Wcsncmp(a, b c23.Ptr, n uint64) int32 {
	for ; n > 0; n-- {
		x, y := int32(c23.Load32(a)), int32(c23.Load32(b))
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		case x == 0:
			return 0
		}
		a, b = c23.Add(a, 4), c23.Add(b, 4)
	}
	return 0
}

func Wcscpy(d, s c23.Ptr) c23.Ptr {
	c23.Memmove(d, s, uintptr(4*(Wcslen(s)+1)))
	return d
}

func Wcscat(d, s c23.Ptr) c23.Ptr {
	Wcscpy(c23.Add(d, int(4*Wcslen(d))), s)
	return d
}

func Wcschr(s c23.Ptr, c int32) c23.Ptr {
	for {
		x := int32(c23.Load32(s))
		if x == c {
			return s
		}
		if x == 0 {
			return c23.Ptr(nil)
		}
		s = c23.Add(s, 4)
	}
}
