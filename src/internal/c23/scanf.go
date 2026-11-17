// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"math"
	"strconv"
	"strings"
)

// The scanf family.

// A scanSrc is what scanf reads: a string or a stream, a byte at a time,
// with one byte of push back.
type scanSrc interface {
	get() int32 // -1 at the end of the input
	unget(c int32)
}

type strSrc struct {
	s Ptr
	i int
}

func (s *strSrc) get() int32 {
	c := Load8(Add(s.s, s.i))
	if c == 0 {
		return -1
	}
	s.i++
	return int32(c)
}

func (s *strSrc) unget(c int32) {
	if c >= 0 {
		s.i--
	}
}

type fileSrc struct{ f *File }

func (s fileSrc) get() int32 { return s.f.getc() }

func (s fileSrc) unget(c int32) {
	if c >= 0 {
		s.f.unget = append(s.f.unget, byte(c))
	}
}

func Sscanf(s, format Ptr, va ...any) int32 { return Vsscanf(s, format, VaStart(va)) }
func Scanf(format Ptr, va ...any) int32     { return Vscanf(format, VaStart(va)) }
func Fscanf(f, format Ptr, va ...any) int32 { return Vfscanf(f, format, VaStart(va)) }

func Vsscanf(s, format, ap Ptr) int32 {
	return scanC(&strSrc{s: s}, GoString(format), vaOf(ap))
}

func Vscanf(format, ap Ptr) int32 { return Vfscanf(Stdio(0), format, ap) }

func Vfscanf(f, format, ap Ptr) int32 {
	fl := fileOf(f)
	if !fl.readable() {
		return -1
	}
	return scanC(fileSrc{fl}, GoString(format), vaOf(ap))
}

// A scanner reads the input of a conversion: it counts what it reads,
// and stops at the conversion's width.
type scanner struct {
	src   scanSrc
	n     int // bytes read
	width int // left for this conversion; < 0 if unlimited
	eof   bool
}

func (s *scanner) get() int32 {
	if s.width == 0 {
		return -1
	}
	c := s.src.get()
	if c < 0 {
		s.eof = true
		return -1
	}
	s.n++
	if s.width > 0 {
		s.width--
	}
	return c
}

func (s *scanner) unget(c int32) {
	if c < 0 {
		return
	}
	s.src.unget(c)
	s.n--
	if s.width >= 0 {
		s.width++
	}
}

func isSpaceC(c int32) bool { return c == ' ' || c >= '\t' && c <= '\r' }

// skipSpace reads the white space ahead.
func (s *scanner) skipSpace() {
	w := s.width
	s.width = -1
	for {
		c := s.get()
		if !isSpaceC(c) {
			s.unget(c)
			break
		}
	}
	s.width = w
}

// accept reads the next byte if it is one of set.
func (s *scanner) accept(b []byte, set string) ([]byte, bool) {
	c := s.get()
	if c >= 0 && strings.IndexByte(set, byte(c)) >= 0 {
		return append(b, byte(c)), true
	}
	s.unget(c)
	return b, false
}

// scanInt reads an integer of base (0: C's prefixes) as strtol does.
func (s *scanner) scanInt(base int) (string, bool) {
	b, _ := s.accept(nil, "+-")
	var digits string
	switch base {
	case 0, 16:
		var zero bool
		if b, zero = s.accept(b, "0"); zero {
			var x bool
			if b, x = s.accept(b, "xX"); x {
				base = 16
			} else if base == 0 {
				base = 8
			}
		} else if base == 0 {
			base = 10
		}
	}
	switch base {
	case 8:
		digits = "01234567"
	case 10:
		digits = "0123456789"
	case 16:
		digits = "0123456789abcdefABCDEF"
	}
	start := len(b)
	for {
		var ok bool
		if b, ok = s.accept(b, digits); !ok {
			break
		}
	}
	if len(b) == start && (start == 0 || b[start-1] != '0') {
		return "", false
	}
	return string(b), true
}

// scanFloat reads the longest prefix of a floating constant, as strtod
// takes them; it fails if that is not a whole one.
func (s *scanner) scanFloat() (float64, bool) {
	b, _ := s.accept(nil, "+-")
	sign := len(b)
	c := s.get()
	s.unget(c)
	switch c {
	case 'i', 'I', 'n', 'N':
		word := "infinity"
		if c|0x20 == 'n' {
			word = "nan"
		}
		for i := 0; i < len(word); i++ {
			var ok bool
			if b, ok = s.accept(b, word[i:i+1]+strings.ToUpper(word[i:i+1])); !ok {
				break
			}
		}
		w := strings.ToLower(string(b[sign:]))
		switch {
		case w == "nan":
			if b, _ = s.accept(b, "("); b[len(b)-1] == '(' {
				for {
					var ok bool
					if b, ok = s.accept(b, "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ_"); !ok {
						break
					}
				}
				if b, _ = s.accept(b, ")"); b[len(b)-1] != ')' {
					return 0, false
				}
			}
			return math.NaN(), true
		case w == "inf" || w == "infinity":
			if sign > 0 && b[0] == '-' {
				return math.Inf(-1), true
			}
			return math.Inf(1), true
		}
		return 0, false
	}
	digits, exp := "0123456789", "eE"
	var hex bool
	if b, _ = s.accept(b, "0"); len(b) > sign {
		if b, hex = s.accept(b, "xX"); hex {
			digits, exp = "0123456789abcdefABCDEF", "pP"
		}
	}
	nd := 0
	for {
		var ok bool
		if b, ok = s.accept(b, digits); !ok {
			break
		}
		nd++
	}
	var dot bool
	if b, dot = s.accept(b, "."); dot {
		for {
			var ok bool
			if b, ok = s.accept(b, digits); !ok {
				break
			}
			nd++
		}
	}
	if nd == 0 && !(len(b) > sign && b[sign] == '0' && !hex) {
		return 0, false
	}
	var e bool
	if b, e = s.accept(b, exp); e {
		b, _ = s.accept(b, "+-")
		k := len(b)
		for {
			var ok bool
			if b, ok = s.accept(b, "0123456789"); !ok {
				break
			}
		}
		if len(b) == k {
			return 0, false
		}
	}
	t := string(b)
	if hex && !e {
		t += "p0"
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil && err.(*strconv.NumError).Err != strconv.ErrRange {
		return 0, false
	}
	return f, true
}

// storeInt stores v at p, as an integer of size bytes.
func storeInt(p Ptr, size int, v uint64) {
	switch size {
	case 1:
		Store8(p, uint8(v))
	case 2:
		Store16(p, uint16(v))
	case 8:
		Store64(p, v)
	default:
		Store32(p, uint32(v))
	}
}

// scanC scans as C's scanf: it returns how many conversions it assigned,
// or EOF if the input ended before the first.
func scanC(src scanSrc, f string, ap *VaList) int32 {
	s := &scanner{src: src, width: -1}
	assigned := int32(0)
	result := func() int32 {
		if assigned == 0 && s.eof {
			return -1
		}
		return assigned
	}
	for i := 0; i < len(f); i++ {
		s.width = -1
		c := f[i]
		if isSpaceC(int32(c)) {
			s.skipSpace()
			continue
		}
		if c != '%' || i+1 < len(f) && f[i+1] == '%' {
			if c == '%' {
				i++
				s.skipSpace()
			}
			in := s.get()
			if in != int32(c) {
				s.unget(in)
				return result()
			}
			continue
		}
		i++
		suppress := false
		if i < len(f) && f[i] == '*' {
			suppress = true
			i++
		}
		width := -1
		for i < len(f) && f[i] >= '0' && f[i] <= '9' {
			if width < 0 {
				width = 0
			}
			width = width*10 + int(f[i]-'0')
			i++
		}
		size := 4 // 1 hh, 2 h, 8 l ll j z t, 16 L
		for i < len(f) && strings.IndexByte("hlLqjzt", f[i]) >= 0 {
			switch f[i] {
			case 'h':
				if size == 2 {
					size = 1
				} else {
					size = 2
				}
			case 'L':
				size = 16
			default:
				size = 8
			}
			i++
		}
		isize := min(size, 8) // an integer's
		if k := i - 1; size == 8 && strings.IndexByte("lzt", f[k]) >= 0 && f[k-1] != 'l' {
			isize = int(longSize) // long, size_t, ptrdiff_t; long long is "ll"
		}
		if i >= len(f) {
			return result()
		}
		conv := f[i]
		var dst Ptr
		if !suppress && conv != 'n' {
			dst, _ = ap.next().(Ptr)
		}
		if conv != 'c' && conv != '[' && conv != 'n' {
			s.skipSpace()
		}
		s.width = width
		switch conv {
		case 'n':
			if !suppress {
				p, _ := ap.next().(Ptr)
				storeInt(p, isize, uint64(s.n))
			}
			continue
		case 'd', 'i', 'u', 'x', 'X', 'o', 'p':
			base := 16
			switch conv {
			case 'd', 'u':
				base = 10
			case 'i':
				base = 0
			case 'o':
				base = 8
			}
			t, ok := s.scanInt(base)
			if !ok {
				return result()
			}
			v, neg, _, _ := strtoParse(t, base)
			if neg {
				v = -v
			}
			if !suppress {
				if conv == 'p' {
					StorePtr(dst, FromInt(uintptr(v)))
				} else {
					storeInt(dst, isize, v)
				}
			}
		case 'a', 'A', 'e', 'E', 'f', 'F', 'g', 'G':
			v, ok := s.scanFloat()
			if !ok {
				return result()
			}
			if !suppress {
				if size == 4 {
					StoreF32(dst, float32(v))
				} else {
					StoreF64(dst, v)
				}
			}
		case 'c':
			if width < 0 {
				s.width = 1
			}
			k := 0
			for {
				in := s.get()
				if in < 0 {
					break
				}
				if !suppress {
					Store8(Add(dst, k), uint8(in))
				}
				k++
			}
			if k == 0 || s.width > 0 {
				return result()
			}
		case 's', '[':
			var set string
			negate := false
			if conv == '[' {
				j := i + 1
				if j < len(f) && f[j] == '^' {
					negate = true
					j++
				}
				k := j
				if k < len(f) && f[k] == ']' {
					k++
				}
				for k < len(f) && f[k] != ']' {
					k++
				}
				if k >= len(f) {
					return result()
				}
				set = expandSet(f[j:k])
				i = k
			}
			k := 0
			for {
				in := s.get()
				if in < 0 {
					break
				}
				var match bool
				if conv == 's' {
					match = !isSpaceC(in)
				} else {
					match = strings.IndexByte(set, byte(in)) >= 0 != negate
				}
				if !match {
					s.unget(in)
					break
				}
				if !suppress {
					Store8(Add(dst, k), uint8(in))
				}
				k++
			}
			if k == 0 {
				return result()
			}
			if !suppress {
				Store8(Add(dst, k), 0)
			}
		default:
			return result()
		}
		if !suppress {
			assigned++
		}
	}
	return assigned
}

// expandSet expands the ranges (a-z) of a scanset's characters.
func expandSet(set string) string {
	var b []byte
	for i := 0; i < len(set); i++ {
		if i+2 < len(set) && set[i+1] == '-' && set[i] <= set[i+2] {
			for c := int(set[i]); c <= int(set[i+2]); c++ {
				b = append(b, byte(c))
			}
			i += 2
			continue
		}
		b = append(b, set[i])
	}
	return string(b)
}
