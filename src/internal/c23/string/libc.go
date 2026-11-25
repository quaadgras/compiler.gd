// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package string

import (
	"bytes"
	c23 "internal/c23"
	"strings"
	"unsafe"
)

// The functions here that walk a string or copy one look at their
// memory as c23 gives it to them: the bytes from a pointer to the end
// of what it points into (BytesFrom, StringAt), and the bytes to write
// (Writable). A string that does not end in its object, or one that
// does not fit where it is copied to, is a fault, and nothing outside
// the objects is read or written.

func Strlen(p *c23.Void) uint64 { return c23.Strlen1(p) }

func Strcmp(a, b *c23.Void) int32 { return c23.Strcmp1(a, b) }

func Strncmp(a, b *c23.Void, n uint64) int32 {
	if n == 0 {
		return 0
	}
	x, y := c23.BytesFrom(a), c23.BytesFrom(b)
	for i := uint64(0); i < n; i++ {
		if i >= uint64(len(x)) || i >= uint64(len(y)) {
			c23.RaiseFault("strncmp of strings with no end in their objects", c23.Addr(a))
		}
		if x[i] != y[i] || x[i] == 0 {
			return int32(x[i]) - int32(y[i])
		}
	}
	return 0
}

func Strcpy(d, s *c23.Void) *c23.Void {
	src := c23.StringAt(s)
	dst := c23.Writable(d, uintptr(len(src)+1))
	copy(dst, src)
	dst[len(src)] = 0
	return d
}

func Strncpy(d, s *c23.Void, n uint64) *c23.Void {
	if n == 0 {
		return d
	}
	src := limited(s, n, "strncpy")
	dst := c23.Writable(d, uintptr(n))
	clear(dst[copy(dst, src):])
	return d
}

// limited returns the bytes of the string at s, without the 0 that ends
// it, or its first n bytes if it has no end in them: they are in what s
// points into.
func limited(s *c23.Void, n uint64, what string) []byte {
	b := c23.BytesFrom(s)
	if uint64(len(b)) > n {
		b = b[:n]
	}
	if i := bytes.IndexByte(b, 0); i >= 0 {
		return b[:i]
	}
	if uint64(len(b)) < n {
		c23.RaiseFault(what+" of a string with no end in its object", c23.Addr(s))
	}
	return b
}

func Strcat(d, s *c23.Void) *c23.Void {
	Strcpy(c23.Add(d, len(c23.StringAt(d))), s)
	return d
}

func Strncat(d, s *c23.Void, n uint64) *c23.Void {
	src := limited(s, n, "strncat")
	dst := c23.Writable(c23.Add(d, len(c23.StringAt(d))), uintptr(len(src)+1))
	copy(dst, src)
	dst[len(src)] = 0
	return d
}

func Strchr(s *c23.Void, c int32) *c23.Void {
	b := c23.StringAt(s)
	if uint8(c) == 0 {
		return c23.Add(s, len(b))
	}
	if i := bytes.IndexByte(b, uint8(c)); i >= 0 {
		return c23.Add(s, i)
	}
	return (*c23.Void)(nil)
}

func Strrchr(s *c23.Void, c int32) *c23.Void {
	b := c23.StringAt(s)
	if uint8(c) == 0 {
		return c23.Add(s, len(b))
	}
	if i := bytes.LastIndexByte(b, uint8(c)); i >= 0 {
		return c23.Add(s, i)
	}
	return (*c23.Void)(nil)
}

func Strstr(s, sub *c23.Void) *c23.Void {
	h, n := c23.GoString(s), c23.GoString(sub)
	i := strings.Index(h, n)
	if i < 0 {
		return (*c23.Void)(nil)
	}
	return c23.Add(s, i)
}

func Strspn(s, set *c23.Void) uint64 {
	h, cs := c23.GoString(s), c23.GoString(set)
	n := 0
	for n < len(h) && strings.IndexByte(cs, h[n]) >= 0 {
		n++
	}
	return uint64(n)
}

func Strcspn(s, set *c23.Void) uint64 {
	h, cs := c23.GoString(s), c23.GoString(set)
	n := 0
	for n < len(h) && strings.IndexByte(cs, h[n]) < 0 {
		n++
	}
	return uint64(n)
}

func Strdup(s *c23.Void) *c23.Void { return c23.CString(c23.GoString(s)) }

func Strndup(s *c23.Void, n uint64) *c23.Void {
	g := c23.GoString(s)
	if uint64(len(g)) > n {
		g = g[:n]
	}
	return c23.CString(g)
}

func Strnlen(p *c23.Void, max uint64) uint64 {
	if max == 0 {
		return 0
	}
	return uint64(len(limited(p, max, "strnlen")))
}

func Strpbrk(s, set *c23.Void) *c23.Void {
	n := Strcspn(s, set)
	if c23.Load8(c23.Add(s, int(n))) == 0 {
		return (*c23.Void)(nil)
	}
	return c23.Add(s, int(n))
}

// Strcoll is strcoll in the C locale: strcmp.
func Strcoll(a, b *c23.Void) int32 { return Strcmp(a, b) }

// MemsetExplicit is memset_explicit: memset, which the Go compiler does
// not remove.
func MemsetExplicit(p *c23.Void, c int32, n uint64) *c23.Void { return c23.Memset(p, c, n) }

var strtokSave = c23.New[unsafe.Pointer]()

// Strtok is strtok, whose state is its last string's rest.
func Strtok(s, sep *c23.Void) *c23.Void { return StrtokR(s, sep, strtokSave) }

// StrtokR is strtok_r.
func StrtokR(s, sep, save *c23.Void) *c23.Void {
	if c23.Addr(s) == 0 {
		s = c23.LoadPtr(save)
		if c23.Addr(s) == 0 {
			return (*c23.Void)(nil)
		}
	}
	s = c23.Add(s, int(Strspn(s, sep)))
	if c23.Load8(s) == 0 {
		c23.StorePtr(save, (*c23.Void)(nil))
		return (*c23.Void)(nil)
	}
	e := c23.Add(s, int(Strcspn(s, sep)))
	if c23.Load8(e) == 0 {
		c23.StorePtr(save, (*c23.Void)(nil))
	} else {
		c23.Store8(e, 0)
		c23.StorePtr(save, c23.Add(e, 1))
	}
	return s
}

// Memcpy is memcpy and memmove.
func Memcpy(dst, src *c23.Void, n uint64) *c23.Void {
	c23.Memmove(dst, src, uintptr(n))
	return dst
}

func Memccpy(dst, src *c23.Void, c int32, n uint64) *c23.Void {
	if n == 0 {
		return (*c23.Void)(nil)
	}
	b := upTo(src, n)
	i := bytes.IndexByte(b, uint8(c))
	if i < 0 {
		if uint64(len(b)) < n {
			c23.RaiseFault("memccpy past the end of an object", c23.Addr(src))
		}
		copy(c23.Writable(dst, uintptr(n)), b)
		return (*c23.Void)(nil)
	}
	copy(c23.Writable(dst, uintptr(i+1)), b[:i+1])
	return c23.Add(dst, i+1)
}

// upTo returns the n bytes at s, or as many of them as what s points
// into has: a function that reads them one after the other, and stops
// at one that it looks for, may be given a count that is more.
func upTo(s *c23.Void, n uint64) []byte {
	b := c23.BytesFrom(s)
	if uint64(len(b)) > n {
		b = b[:n]
	}
	return b
}

func Memchr(s *c23.Void, c int32, n uint64) *c23.Void {
	if n == 0 {
		return (*c23.Void)(nil)
	}
	b := upTo(s, n)
	if i := bytes.IndexByte(b, uint8(c)); i >= 0 {
		return c23.Add(s, i)
	}
	if uint64(len(b)) < n {
		c23.RaiseFault("memchr past the end of an object", c23.Addr(s))
	}
	return (*c23.Void)(nil)
}
