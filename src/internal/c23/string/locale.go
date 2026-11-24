// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package string

import (
	"bytes"
	c23 "internal/c23"
	"strings"
)

// The functions of a locale (POSIX's _l functions), which are the global
// locale's: the library's locales collate alike, and have the same
// messages.

func StrcollL(a, b, _ c23.Ptr) int32 { return Strcoll(a, b) }

func StrxfrmL(d, s c23.Ptr, n uint64, _ c23.Ptr) uint64 { return Strxfrm(d, s, n) }

func StrerrorL(e int32, _ c23.Ptr) c23.Ptr { return c23.Strerror(e) }

// Memmem is memmem: the first of the n bytes at needle in the hn at
// haystack, or null; an empty needle is at haystack.
func Memmem(haystack c23.Ptr, hn uint64, needle c23.Ptr, n uint64) c23.Ptr {
	if n == 0 {
		return haystack
	}
	if n > hn {
		return c23.Ptr(nil)
	}
	i := bytes.Index(c23.BytesAt(haystack, uintptr(hn)), c23.BytesAt(needle, uintptr(n)))
	if i < 0 {
		return c23.Ptr(nil)
	}
	return c23.Add(haystack, i)
}

// StrerrorR is POSIX's (XSI's) strerror_r: it copies e's message to buf,
// or as much of it as fits with a null (ERANGE), and returns 0, or, as
// glibc does, EINVAL for a number that is no error's (with its "Unknown
// error" message).
func StrerrorR(e int32, buf c23.Ptr, n uint64) int32 {
	msg := c23.ErrnoString(e)
	r := int32(0)
	if strings.HasPrefix(msg, "Unknown error") {
		r = c23.EINVAL
	}
	if uint64(len(msg)) >= n {
		if n > 0 {
			b := c23.BytesAt(buf, uintptr(n))
			copy(b, msg[:n-1])
			b[n-1] = 0
		}
		return c23.ERANGE
	}
	copy(c23.BytesAt(buf, uintptr(len(msg)+1)), msg+"\x00")
	return r
}

// GnuStrerrorR is glibc's strerror_r (with _GNU_SOURCE): it returns e's
// message, which is strerror's array for an error's number, or, for
// another number, its "Unknown error" message at buf (cut to fit).
func GnuStrerrorR(e int32, buf c23.Ptr, n uint64) c23.Ptr {
	msg := c23.ErrnoString(e)
	if !strings.HasPrefix(msg, "Unknown error") {
		return c23.Strerror(e)
	}
	if n == 0 {
		return c23.Strerror(e)
	}
	b := c23.BytesAt(buf, uintptr(n))
	k := copy(b[:n-1], msg)
	b[k] = 0
	return buf
}
