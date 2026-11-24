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
	MbInvalid    = ^uint64(0) // (size_t)-1
	MbStored     = ^uint64(2) // (size_t)-3

)

type mbstate struct{ p Ptr }

func StateOf(ps Ptr, internal *Ptr) mbstate {
	if Addr(ps) != 0 {
		return mbstate{ps}
	}
	if internal == nil {
		*internal = Malloc(8)
	}
	return mbstate{*internal}
}

func (s mbstate) Count() int32  { return int32(Load32(s.p)) }
func (s mbstate) Value() uint32 { return Load32(Add(s.p, 4)) }
func (s mbstate) Set(c int32, v uint32) {
	Store32(s.p, uint32(c))
	Store32(Add(s.p, 4), v)
}
func (s mbstate) Reset() { s.Set(0, 0) }

// pending returns the bytes of an incomplete character in s.
func (s mbstate) Pending() []byte {
	c, v := s.Count(), s.Value()
	var b []byte
	for i := int32(0); i < c && i < 4; i++ {
		b = append(b, byte(v>>(8*i)))
	}
	return b
}

func (s mbstate) SetPending(b []byte) {
	var v uint32
	for i, c := range b {
		v |= uint32(c) << (8 * i)
	}
	s.Set(int32(len(b)), v)
}

// mbDecode decodes a character from at most n bytes at p, after the
// incomplete character in st: the character, and how many of the n
// bytes it took, or mbIncomplete or mbInvalid.
func mbDecode(p Ptr, n uint64, st mbstate) (rune, uint64) {
	b := st.Pending()
	for i := uint64(0); i < n; i++ {
		b = append(b, Load8(Add(p, int(i))))
		if !utf8.FullRune(b) {
			continue
		}
		r, size := utf8.DecodeRune(b)
		if r == utf8.RuneError && size == 1 {
			st.Reset()
			SetErrno(EILSEQ)
			return 0, MbInvalid
		}
		st.Reset()
		return r, i + 1
	}
	st.SetPending(b)
	return 0, mbIncomplete
}

// mbrto is mbrtowc and mbrtoc32 without the store.
func Mbrto(s Ptr, n uint64, ps Ptr, internal *Ptr) (rune, uint64) {
	if Addr(s) == 0 {
		s, n = String[int8]("\x00"), 1
	}
	st := StateOf(ps, internal)
	if st.Count() < 0 {
		st.Reset()
	}
	r, k := mbDecode(s, n, st)
	if k < MbStored && r == 0 {
		k = 0
	}
	return r, k
}

var Mbrtoc32State, MbrtowcState, MbrlenState Ptr

// putRune writes r's UTF-8 at s: how many bytes, or mbInvalid.
func PutRune(s Ptr, r uint32) uint64 {
	if r > utf8.MaxRune || r >= 0xd800 && r < 0xe000 {
		SetErrno(EILSEQ)
		return MbInvalid
	}
	b := utf8.AppendRune(nil, rune(r))
	for i, c := range b {
		Store8(Add(s, i), c)
	}
	return uint64(len(b))
}

var C32rtombState, WcrtombState Ptr

// wchar.h's strings
