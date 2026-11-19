// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package uchar

import (
	c23 "internal/c23"
	"unicode/utf8"
)

func Mbrtoc32(pc c23.Ptr, s c23.Ptr, n uint64, ps c23.Ptr) uint64 {
	r, k := c23.Mbrto(s, n, ps, &c23.Mbrtoc32State)
	if k < c23.MbStored && c23.Addr(pc) != 0 {
		c23.Store32(pc, uint32(r))
	}
	return k
}

var mbrtoc16State c23.Ptr

func Mbrtoc16(pc c23.Ptr, s c23.Ptr, n uint64, ps c23.Ptr) uint64 {
	st := c23.StateOf(ps, &mbrtoc16State)
	if st.Count() < 0 {
		if c23.Addr(pc) != 0 {
			c23.Store16(pc, uint16(st.Value()))
		}
		st.Reset()
		return c23.MbStored
	}
	r, k := c23.Mbrto(s, n, ps, &mbrtoc16State)
	if k >= c23.MbStored {
		return k
	}
	if r > 0xffff {
		r -= 0x10000
		st.Set(-1, uint32(0xdc00+r&0x3ff))
		r = 0xd800 + r>>10
	}
	if c23.Addr(pc) != 0 {
		c23.Store16(pc, uint16(r))
	}
	return k
}

var mbrtoc8State c23.Ptr

func Mbrtoc8(pc c23.Ptr, s c23.Ptr, n uint64, ps c23.Ptr) uint64 {
	st := c23.StateOf(ps, &mbrtoc8State)
	if c := st.Count(); c < 0 {
		v := st.Value()
		if c23.Addr(pc) != 0 {
			c23.Store8(pc, uint8(v))
		}
		if c == -1 {
			st.Reset()
		} else {
			st.Set(c+1, v>>8)
		}
		return c23.MbStored
	}
	r, k := c23.Mbrto(s, n, ps, &mbrtoc8State)
	if k >= c23.MbStored {
		return k
	}
	b := utf8.AppendRune(nil, r)
	if len(b) > 1 {
		var v uint32
		for i, c := range b[1:] {
			v |= uint32(c) << (8 * i)
		}
		st.Set(-int32(len(b)-1), v)
	}
	if c23.Addr(pc) != 0 {
		c23.Store8(pc, b[0])
	}
	return k
}

func C32rtomb(s c23.Ptr, c uint32, ps c23.Ptr) uint64 {
	st := c23.StateOf(ps, &c23.C32rtombState)
	st.Reset()
	if c23.Addr(s) == 0 {
		return 1
	}
	return c23.PutRune(s, c)
}

var c16rtombState c23.Ptr

func C16rtomb(s c23.Ptr, c uint16, ps c23.Ptr) uint64 {
	st := c23.StateOf(ps, &c16rtombState)
	if c23.Addr(s) == 0 {
		st.Reset()
		return 1
	}
	high := st.Count() == -1
	hv := st.Value()
	st.Reset()
	switch {
	case c >= 0xd800 && c < 0xdc00 && !high:
		st.Set(-1, uint32(c))
		return 0
	case c >= 0xdc00 && c < 0xe000 && high:
		return c23.PutRune(s, 0x10000+(hv-0xd800)<<10+uint32(c)-0xdc00)
	case high || c >= 0xd800 && c < 0xe000:
		c23.SetErrno(c23.EILSEQ)
		return c23.MbInvalid
	}
	return c23.PutRune(s, uint32(c))
}

var c8rtombState c23.Ptr

func C8rtomb(s c23.Ptr, c uint8, ps c23.Ptr) uint64 {
	st := c23.StateOf(ps, &c8rtombState)
	if c23.Addr(s) == 0 {
		st.Reset()
		return 1
	}
	if st.Count() < 0 {
		st.Reset()
	}
	b := append(st.Pending(), c)
	if !utf8.FullRune(b) {
		st.SetPending(b)
		return 0
	}
	st.Reset()
	r, size := utf8.DecodeRune(b)
	if r == utf8.RuneError && size == 1 {
		c23.SetErrno(c23.EILSEQ)
		return c23.MbInvalid
	}
	return c23.PutRune(s, uint32(r))
}
