// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import "unicode/utf8"

// uchar.h and wchar.h's conversions, between UTF-8, the multibyte
// encoding of every locale, and UTF-8, UTF-16 and UTF-32 code units.
// wchar_t is UTF-32.
//
// An mbstate_t is struct { int count; unsigned value; }: count bytes of
// a character not yet complete, in value's low bytes first, or, if count
// is negative, -count code units yet to deliver, in value.

const (
	mbIncomplete = ^uint64(1) // (size_t)-2
	mbInvalid    = ^uint64(0) // (size_t)-1
	mbStored     = ^uint64(2) // (size_t)-3
	weof         = 0xffffffff
)

type mbstate struct{ p Ptr }

func stateOf(ps Ptr, internal *Ptr) mbstate {
	if Addr(ps) != 0 {
		return mbstate{ps}
	}
	if internal.obj == nil {
		*internal = Malloc(8)
	}
	return mbstate{*internal}
}

func (s mbstate) count() int32  { return int32(Load32(s.p)) }
func (s mbstate) value() uint32 { return Load32(Add(s.p, 4)) }
func (s mbstate) set(c int32, v uint32) {
	Store32(s.p, uint32(c))
	Store32(Add(s.p, 4), v)
}
func (s mbstate) reset() { s.set(0, 0) }

// pending returns the bytes of an incomplete character in s.
func (s mbstate) pending() []byte {
	c, v := s.count(), s.value()
	var b []byte
	for i := int32(0); i < c && i < 4; i++ {
		b = append(b, byte(v>>(8*i)))
	}
	return b
}

func (s mbstate) setPending(b []byte) {
	var v uint32
	for i, c := range b {
		v |= uint32(c) << (8 * i)
	}
	s.set(int32(len(b)), v)
}

// mbDecode decodes a character from at most n bytes at p, after the
// incomplete character in st: the character, and how many of the n
// bytes it took, or mbIncomplete or mbInvalid.
func mbDecode(p Ptr, n uint64, st mbstate) (rune, uint64) {
	b := st.pending()
	for i := uint64(0); i < n; i++ {
		b = append(b, Load8(Add(p, int(i))))
		if !utf8.FullRune(b) {
			continue
		}
		r, size := utf8.DecodeRune(b)
		if r == utf8.RuneError && size == 1 {
			st.reset()
			SetErrno(EILSEQ)
			return 0, mbInvalid
		}
		st.reset()
		return r, i + 1
	}
	st.setPending(b)
	return 0, mbIncomplete
}

// mbrto is mbrtowc and mbrtoc32 without the store.
func mbrto(s Ptr, n uint64, ps Ptr, internal *Ptr) (rune, uint64) {
	if Addr(s) == 0 {
		s, n = String[int8]("\x00"), 1
	}
	st := stateOf(ps, internal)
	if st.count() < 0 {
		st.reset()
	}
	r, k := mbDecode(s, n, st)
	if k < mbStored && r == 0 {
		k = 0
	}
	return r, k
}

var mbrtoc32State, mbrtowcState, mbrlenState Ptr

func Mbrtoc32(pc Ptr, s Ptr, n uint64, ps Ptr) uint64 {
	r, k := mbrto(s, n, ps, &mbrtoc32State)
	if k < mbStored && Addr(pc) != 0 {
		Store32(pc, uint32(r))
	}
	return k
}

func Mbrtowc(pc Ptr, s Ptr, n uint64, ps Ptr) uint64 {
	r, k := mbrto(s, n, ps, &mbrtowcState)
	if k < mbStored && Addr(pc) != 0 {
		Store32(pc, uint32(r))
	}
	return k
}

func Mbrlen(s Ptr, n uint64, ps Ptr) uint64 {
	_, k := mbrto(s, n, ps, &mbrlenState)
	return k
}

var mbrtoc16State Ptr

func Mbrtoc16(pc Ptr, s Ptr, n uint64, ps Ptr) uint64 {
	st := stateOf(ps, &mbrtoc16State)
	if st.count() < 0 {
		if Addr(pc) != 0 {
			Store16(pc, uint16(st.value()))
		}
		st.reset()
		return mbStored
	}
	r, k := mbrto(s, n, ps, &mbrtoc16State)
	if k >= mbStored {
		return k
	}
	if r > 0xffff {
		r -= 0x10000
		st.set(-1, uint32(0xdc00+r&0x3ff))
		r = 0xd800 + r>>10
	}
	if Addr(pc) != 0 {
		Store16(pc, uint16(r))
	}
	return k
}

var mbrtoc8State Ptr

func Mbrtoc8(pc Ptr, s Ptr, n uint64, ps Ptr) uint64 {
	st := stateOf(ps, &mbrtoc8State)
	if c := st.count(); c < 0 {
		v := st.value()
		if Addr(pc) != 0 {
			Store8(pc, uint8(v))
		}
		if c == -1 {
			st.reset()
		} else {
			st.set(c+1, v>>8)
		}
		return mbStored
	}
	r, k := mbrto(s, n, ps, &mbrtoc8State)
	if k >= mbStored {
		return k
	}
	b := utf8.AppendRune(nil, r)
	if len(b) > 1 {
		var v uint32
		for i, c := range b[1:] {
			v |= uint32(c) << (8 * i)
		}
		st.set(-int32(len(b)-1), v)
	}
	if Addr(pc) != 0 {
		Store8(pc, b[0])
	}
	return k
}

// putRune writes r's UTF-8 at s: how many bytes, or mbInvalid.
func putRune(s Ptr, r uint32) uint64 {
	if r > utf8.MaxRune || r >= 0xd800 && r < 0xe000 {
		SetErrno(EILSEQ)
		return mbInvalid
	}
	b := utf8.AppendRune(nil, rune(r))
	for i, c := range b {
		Store8(Add(s, i), c)
	}
	return uint64(len(b))
}

var c32rtombState, wcrtombState Ptr

func C32rtomb(s Ptr, c uint32, ps Ptr) uint64 {
	st := stateOf(ps, &c32rtombState)
	st.reset()
	if Addr(s) == 0 {
		return 1
	}
	return putRune(s, c)
}

func Wcrtomb(s Ptr, c int32, ps Ptr) uint64 {
	st := stateOf(ps, &wcrtombState)
	st.reset()
	if Addr(s) == 0 {
		return 1
	}
	return putRune(s, uint32(c))
}

var c16rtombState Ptr

func C16rtomb(s Ptr, c uint16, ps Ptr) uint64 {
	st := stateOf(ps, &c16rtombState)
	if Addr(s) == 0 {
		st.reset()
		return 1
	}
	high := st.count() == -1
	hv := st.value()
	st.reset()
	switch {
	case c >= 0xd800 && c < 0xdc00 && !high:
		st.set(-1, uint32(c))
		return 0
	case c >= 0xdc00 && c < 0xe000 && high:
		return putRune(s, 0x10000+(hv-0xd800)<<10+uint32(c)-0xdc00)
	case high || c >= 0xd800 && c < 0xe000:
		SetErrno(EILSEQ)
		return mbInvalid
	}
	return putRune(s, uint32(c))
}

var c8rtombState Ptr

func C8rtomb(s Ptr, c uint8, ps Ptr) uint64 {
	st := stateOf(ps, &c8rtombState)
	if Addr(s) == 0 {
		st.reset()
		return 1
	}
	if st.count() < 0 {
		st.reset()
	}
	b := append(st.pending(), c)
	if !utf8.FullRune(b) {
		st.setPending(b)
		return 0
	}
	st.reset()
	r, size := utf8.DecodeRune(b)
	if r == utf8.RuneError && size == 1 {
		SetErrno(EILSEQ)
		return mbInvalid
	}
	return putRune(s, uint32(r))
}

// Mbsinit is mbsinit: whether ps is null or an initial state.
func Mbsinit(ps Ptr) int32 {
	return B2I(Addr(ps) == 0 || Load32(ps) == 0)
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

// wchar.h's strings

func Wcslen(s Ptr) uint64 {
	n := uint64(0)
	for Load32(Add(s, int(4*n))) != 0 {
		n++
	}
	return n
}

func Wcscmp(a, b Ptr) int32 { return Wcsncmp(a, b, ^uint64(0)) }

func Wcsncmp(a, b Ptr, n uint64) int32 {
	for ; n > 0; n-- {
		x, y := int32(Load32(a)), int32(Load32(b))
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		case x == 0:
			return 0
		}
		a, b = Add(a, 4), Add(b, 4)
	}
	return 0
}

func Wcscpy(d, s Ptr) Ptr {
	Memmove(d, s, uintptr(4*(Wcslen(s)+1)))
	return d
}

func Wcscat(d, s Ptr) Ptr {
	Wcscpy(Add(d, int(4*Wcslen(d))), s)
	return d
}

func Wcschr(s Ptr, c int32) Ptr {
	for {
		x := int32(Load32(s))
		if x == c {
			return s
		}
		if x == 0 {
			return Ptr{}
		}
		s = Add(s, 4)
	}
}
