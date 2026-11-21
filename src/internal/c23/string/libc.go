// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package string

import (
	c23 "internal/c23"
	"strings"
	"unsafe"
)

func Strlen(p c23.Ptr) uint64 {
	n := uint64(0)
	for c23.Load8(c23.Add(p, int(n))) != 0 {
		n++
	}
	return n
}

func Strcmp(a, b c23.Ptr) int32 {
	for {
		x, y := c23.Load8(a), c23.Load8(b)
		if x != y || x == 0 {
			return int32(x) - int32(y)
		}
		a, b = c23.Add(a, 1), c23.Add(b, 1)
	}
}

func Strncmp(a, b c23.Ptr, n uint64) int32 {
	for ; n > 0; n-- {
		x, y := c23.Load8(a), c23.Load8(b)
		if x != y || x == 0 {
			return int32(x) - int32(y)
		}
		a, b = c23.Add(a, 1), c23.Add(b, 1)
	}
	return 0
}

func Strcpy(d, s c23.Ptr) c23.Ptr {
	r := d
	for {
		c := c23.Load8(s)
		c23.Store8(d, c)
		if c == 0 {
			return r
		}
		d, s = c23.Add(d, 1), c23.Add(s, 1)
	}
}

func Strncpy(d, s c23.Ptr, n uint64) c23.Ptr {
	r := d
	for ; n > 0; n-- {
		c := c23.Load8(s)
		c23.Store8(d, c)
		d = c23.Add(d, 1)
		if c != 0 {
			s = c23.Add(s, 1)
		}
	}
	return r
}

func Strcat(d, s c23.Ptr) c23.Ptr {
	Strcpy(c23.Add(d, int(Strlen(d))), s)
	return d
}

func Strncat(d, s c23.Ptr, n uint64) c23.Ptr {
	e := c23.Add(d, int(Strlen(d)))
	for ; n > 0; n-- {
		c := c23.Load8(s)
		if c == 0 {
			break
		}
		c23.Store8(e, c)
		e, s = c23.Add(e, 1), c23.Add(s, 1)
	}
	c23.Store8(e, 0)
	return d
}

func Strchr(s c23.Ptr, c int32) c23.Ptr {
	for {
		x := c23.Load8(s)
		if x == uint8(c) {
			return s
		}
		if x == 0 {
			return c23.Ptr{}
		}
		s = c23.Add(s, 1)
	}
}

func Strrchr(s c23.Ptr, c int32) c23.Ptr {
	var r c23.Ptr
	for {
		x := c23.Load8(s)
		if x == uint8(c) {
			r = s
		}
		if x == 0 {
			return r
		}
		s = c23.Add(s, 1)
	}
}

func Strstr(s, sub c23.Ptr) c23.Ptr {
	h, n := c23.GoString(s), c23.GoString(sub)
	i := strings.Index(h, n)
	if i < 0 {
		return c23.Ptr{}
	}
	return c23.Add(s, i)
}

func Strspn(s, set c23.Ptr) uint64 {
	h, cs := c23.GoString(s), c23.GoString(set)
	n := 0
	for n < len(h) && strings.IndexByte(cs, h[n]) >= 0 {
		n++
	}
	return uint64(n)
}

func Strcspn(s, set c23.Ptr) uint64 {
	h, cs := c23.GoString(s), c23.GoString(set)
	n := 0
	for n < len(h) && strings.IndexByte(cs, h[n]) < 0 {
		n++
	}
	return uint64(n)
}

func Strdup(s c23.Ptr) c23.Ptr { return c23.CString(c23.GoString(s)) }

func Strndup(s c23.Ptr, n uint64) c23.Ptr {
	g := c23.GoString(s)
	if uint64(len(g)) > n {
		g = g[:n]
	}
	return c23.CString(g)
}

func Strnlen(p c23.Ptr, max uint64) uint64 {
	n := uint64(0)
	for n < max && c23.Load8(c23.Add(p, int(n))) != 0 {
		n++
	}
	return n
}

func Strpbrk(s, set c23.Ptr) c23.Ptr {
	n := Strcspn(s, set)
	if c23.Load8(c23.Add(s, int(n))) == 0 {
		return c23.Ptr{}
	}
	return c23.Add(s, int(n))
}

// Strcoll is strcoll in the C locale: strcmp.
func Strcoll(a, b c23.Ptr) int32 { return Strcmp(a, b) }

// MemsetExplicit is memset_explicit: memset, which the Go compiler does
// not remove.
func MemsetExplicit(p c23.Ptr, c int32, n uint64) c23.Ptr { return c23.Memset(p, c, n) }

var strtokSave = c23.New[unsafe.Pointer]()

// Strtok is strtok, whose state is its last string's rest.
func Strtok(s, sep c23.Ptr) c23.Ptr { return StrtokR(s, sep, strtokSave) }

// StrtokR is strtok_r.
func StrtokR(s, sep, save c23.Ptr) c23.Ptr {
	if c23.Addr(s) == 0 {
		s = c23.LoadPtr(save)
		if c23.Addr(s) == 0 {
			return c23.Ptr{}
		}
	}
	s = c23.Add(s, int(Strspn(s, sep)))
	if c23.Load8(s) == 0 {
		c23.StorePtr(save, c23.Ptr{})
		return c23.Ptr{}
	}
	e := c23.Add(s, int(Strcspn(s, sep)))
	if c23.Load8(e) == 0 {
		c23.StorePtr(save, c23.Ptr{})
	} else {
		c23.Store8(e, 0)
		c23.StorePtr(save, c23.Add(e, 1))
	}
	return s
}

// Memcpy is memcpy and memmove.
func Memcpy(dst, src c23.Ptr, n uint64) c23.Ptr {
	c23.Memmove(dst, src, uintptr(n))
	return dst
}

func Memccpy(dst, src c23.Ptr, c int32, n uint64) c23.Ptr {
	for i := uint64(0); i < n; i++ {
		x := c23.Load8(c23.Add(src, int(i)))
		c23.Store8(c23.Add(dst, int(i)), x)
		if x == uint8(c) {
			return c23.Add(dst, int(i+1))
		}
	}
	return c23.Ptr{}
}

func Memchr(s c23.Ptr, c int32, n uint64) c23.Ptr {
	for i := uint64(0); i < n; i++ {
		if c23.Load8(c23.Add(s, int(i))) == uint8(c) {
			return c23.Add(s, int(i))
		}
	}
	return c23.Ptr{}
}
