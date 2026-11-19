// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"strings"
	"unicode/utf8"
)

// iconv.h: conversions among UTF-8, ASCII, ISO-8859-1, UTF-16 and UTF-32
// (UCS-4) of either byte order, and WCHAR_T (wchar_t's, UTF-32 in the
// target's order, little-endian).

// An encoding is one of iconv's character sets.
type encoding int

const (
	encUTF8 encoding = iota
	encASCII
	encLatin1
	encUTF16 // with a byte order mark: decoding reads one (big-endian if none), encoding writes one, then little-endian, as glibc does
	encUTF16LE
	encUTF16BE
	encUTF32 // as UTF-16
	encUTF32LE
	encUTF32BE
)

// encodings are the names of the character sets, upper case, without
// - and _.
var encodings = map[string]encoding{
	"UTF8": encUTF8, "": encUTF8,
	"ASCII": encASCII, "USASCII": encASCII, "ANSIX3.41968": encASCII, "646": encASCII,
	"ISO88591": encLatin1, "LATIN1": encLatin1, "L1": encLatin1, "ISO885911987": encLatin1,
	"UTF16": encUTF16, "UTF16LE": encUTF16LE, "UTF16BE": encUTF16BE,
	"UTF32": encUTF32, "UTF32LE": encUTF32LE, "UTF32BE": encUTF32BE,
	"UCS4": encUTF32BE, "UCS4BE": encUTF32BE, "UCS4LE": encUTF32LE, "WCHART": encUTF32LE,
}

// encodingOf returns the character set named name, and its //TRANSLIT
// and //IGNORE suffixes.
func encodingOf(name string) (e encoding, translit, ignore, ok bool) {
	name = strings.ToUpper(name)
	base, suffix, _ := strings.Cut(name, "//")
	for _, s := range strings.Split(suffix, "//") {
		for _, s := range strings.Split(s, ",") {
			translit = translit || s == "TRANSLIT"
			ignore = ignore || s == "IGNORE"
		}
	}
	base = strings.NewReplacer("-", "", "_", "").Replace(base)
	e, ok = encodings[base]
	return e, translit, ignore, ok
}

// iconvT is an iconv_t: a conversion and its state.
type iconvT struct {
	from, to  encoding
	translit  bool
	ignore    bool
	fromOrder encoding // the byte order UTF-16's or UTF-32's input has, once known
	bomDone   bool     // the output's byte order mark is written
}

var iconvInfo = tinfoOf[iconvT]()

// IconvOpen is iconv_open: a conversion from fromcode to tocode, or
// (iconv_t)-1 (errno EINVAL) if one is not a character set iconv has.
func IconvOpen(tocode, fromcode Ptr) Ptr {
	to, translit, ignore, ok1 := encodingOf(GoString(tocode))
	from, _, _, ok2 := encodingOf(GoString(fromcode))
	if !ok1 || !ok2 {
		SetErrno(EINVAL)
		return FromInt(^uintptr(0))
	}
	p := New[iconvT]()
	*(*iconvT)(p.obj) = iconvT{from: from, to: to, translit: translit, ignore: ignore}
	return p
}

func iconvOf(p Ptr) *iconvT {
	if p.obj == nil || p.info() != iconvInfo || p.off != 0 {
		return nil
	}
	return (*iconvT)(p.obj)
}

// IconvClose is iconv_close.
func IconvClose(cd Ptr) int32 {
	if iconvOf(cd) == nil {
		SetErrno(EBADF)
		return -1
	}
	return 0
}

// decode statuses.
const (
	decOK = iota
	decIlseq
	decIncomplete
)

// decode reads a character of the input b: it returns it, the bytes it
// takes and a status.
func (cv *iconvT) decode(b []byte) (rune, int, int) {
	switch cv.from {
	case encUTF8:
		if b[0] < utf8.RuneSelf {
			return rune(b[0]), 1, decOK
		}
		r, n := utf8.DecodeRune(b)
		if r != utf8.RuneError || n > 1 {
			return r, n, decOK
		}
		if !utf8.FullRune(b) {
			return 0, 0, decIncomplete
		}
		return 0, 0, decIlseq
	case encASCII:
		if b[0] >= 0x80 {
			return 0, 0, decIlseq
		}
		return rune(b[0]), 1, decOK
	case encLatin1:
		return rune(b[0]), 1, decOK
	}
	order := cv.from
	if order == encUTF16 || order == encUTF32 {
		order = cv.fromOrder
	}
	switch order {
	case encUTF16LE, encUTF16BE:
		u := func(i int) rune {
			if order == encUTF16LE {
				return rune(b[i]) | rune(b[i+1])<<8
			}
			return rune(b[i])<<8 | rune(b[i+1])
		}
		if len(b) < 2 {
			return 0, 0, decIncomplete
		}
		r := u(0)
		switch {
		case r >= 0xDC00 && r < 0xE000:
			return 0, 0, decIlseq
		case r >= 0xD800 && r < 0xDC00:
			if len(b) < 4 {
				return 0, 0, decIncomplete
			}
			r2 := u(2)
			if r2 < 0xDC00 || r2 >= 0xE000 {
				return 0, 0, decIlseq
			}
			return 0x10000 + (r-0xD800)<<10 + (r2 - 0xDC00), 4, decOK
		}
		return r, 2, decOK
	default: // UTF-32
		if len(b) < 4 {
			return 0, 0, decIncomplete
		}
		var r uint32
		if order == encUTF32LE {
			r = uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
		} else {
			r = uint32(b[3]) | uint32(b[2])<<8 | uint32(b[1])<<16 | uint32(b[0])<<24
		}
		if r > 0x10FFFF || r >= 0xD800 && r < 0xE000 {
			return 0, 0, decIlseq
		}
		return rune(r), 4, decOK
	}
}

// encode appends r to b in the output's character set, or returns false
// if the set has no r.
func (cv *iconvT) encode(b []byte, r rune) ([]byte, bool) {
	to := cv.to
	switch to {
	case encUTF8:
		return utf8.AppendRune(b, r), true
	case encASCII:
		if r >= 0x80 {
			return b, false
		}
		return append(b, byte(r)), true
	case encLatin1:
		if r >= 0x100 {
			return b, false
		}
		return append(b, byte(r)), true
	case encUTF16, encUTF32:
		if !cv.bomDone {
			b = cv.encodeIn(b, to+1, 0xFEFF)
		}
		to++ // little-endian
	}
	return cv.encodeIn(b, to, r), true
}

// encodeIn appends r in UTF-16 or UTF-32 of the byte order of to.
func (cv *iconvT) encodeIn(b []byte, to encoding, r rune) []byte {
	unit16 := func(u rune) []byte {
		if to == encUTF16LE {
			return append(b, byte(u), byte(u>>8))
		}
		return append(b, byte(u>>8), byte(u))
	}
	switch to {
	case encUTF16LE, encUTF16BE:
		if r >= 0x10000 {
			r -= 0x10000
			b = unit16(0xD800 + r>>10)
			return unit16(0xDC00 + r&0x3FF)
		}
		return unit16(r)
	case encUTF32LE:
		return append(b, byte(r), byte(r>>8), byte(r>>16), byte(r>>24))
	}
	return append(b, byte(r>>24), byte(r>>16), byte(r>>8), byte(r))
}

// Iconv is iconv: it converts the *inbytesleft bytes at *inbuf to
// *outbuf, which has *outbytesleft bytes, and moves both on over what it
// converted. It returns the number of characters converted irreversibly
// (with //TRANSLIT, as ?, or skipped with //IGNORE), or (size_t)-1 with
// errno EILSEQ (an invalid input sequence, or a character the output's
// set lacks), EINVAL (an incomplete one at the end of the input) or E2BIG
// (no room in the output); *inbuf is then at that character. A null
// inbuf or *inbuf resets the conversion's state.
func Iconv(cd, inbuf, inleft, outbuf, outleft Ptr) uint64 {
	cv := iconvOf(cd)
	if cv == nil {
		SetErrno(EBADF)
		return ^uint64(0)
	}
	if Addr(inbuf) == 0 || Addr(LoadPtr(inbuf)) == 0 {
		cv.fromOrder = 0
		return 0
	}
	inp, outp := LoadPtr(inbuf), LoadPtr(outbuf)
	nin, nout := loadLong(inleft), loadLong(outleft)
	in := bytesAt(inp, uintptr(nin))
	var out []byte
	if nout > 0 {
		out = bytesAt(outp, uintptr(nout))
	}
	i, o := 0, 0
	irreversible := uint64(0)
	errno := int32(0)
	var buf []byte
	for i < len(in) {
		if (cv.from == encUTF16 || cv.from == encUTF32) && cv.fromOrder == 0 {
			// A byte order mark, or big-endian.
			if cv.from == encUTF16 {
				if len(in)-i < 2 {
					errno = EINVAL
					break
				}
				cv.fromOrder = encUTF16BE
				switch {
				case in[i] == 0xFF && in[i+1] == 0xFE:
					cv.fromOrder = encUTF16LE
					i += 2
				case in[i] == 0xFE && in[i+1] == 0xFF:
					i += 2
				}
			} else {
				if len(in)-i < 4 {
					errno = EINVAL
					break
				}
				cv.fromOrder = encUTF32BE
				switch string(in[i : i+4]) {
				case "\xFF\xFE\x00\x00":
					cv.fromOrder = encUTF32LE
					i += 4
				case "\x00\x00\xFE\xFF":
					i += 4
				}
			}
			continue
		}
		r, n, st := cv.decode(in[i:])
		if st == decIncomplete {
			errno = EINVAL
			break
		}
		if st == decIlseq {
			if cv.ignore {
				i++
				irreversible++
				continue
			}
			errno = EILSEQ
			break
		}
		buf, ok := cv.encode(buf[:0], r)
		if !ok {
			switch {
			case cv.translit:
				buf, _ = cv.encode(buf[:0], '?')
				irreversible++
			case cv.ignore:
				i += n
				irreversible++
				continue
			default:
				errno = EILSEQ
			}
			if errno != 0 {
				break
			}
		}
		if len(buf) > len(out)-o {
			errno = E2BIG
			break
		}
		copy(out[o:], buf)
		o += len(buf)
		i += n
		if cv.to == encUTF16 || cv.to == encUTF32 {
			cv.bomDone = true
		}
	}
	StorePtr(inbuf, Add(inp, i))
	storeLong(inleft, nin-int64(i))
	StorePtr(outbuf, Add(outp, o))
	storeLong(outleft, nout-int64(o))
	if errno != 0 {
		SetErrno(errno)
		return ^uint64(0)
	}
	return irreversible
}
