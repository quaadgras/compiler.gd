// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stdlib

import (
	c23 "internal/c23"
	c23_string "internal/c23/string"
	"os"
)

// Reallocarray is reallocarray: realloc of nmemb*size bytes, or null
// (errno ENOMEM) if that is more than an object may have.
func Reallocarray(p c23.Ptr, nmemb, size uint64) c23.Ptr {
	const maxSize = uint64(^uintptr(0) >> 1) // size_t's, which an object's size must not pass
	if size != 0 && nmemb > maxSize/size {
		c23.SetErrno(c23.ENOMEM)
		return c23.Ptr(nil)
	}
	return c23.Realloc(p, nmemb*size)
}

// QsortR is glibc's qsort_r: qsort with a comparison function that
// takes arg, after the elements.
func QsortR(base c23.Ptr, n, size uint64, cmp, arg c23.Ptr) {
	if n < 2 || size == 0 {
		return
	}
	f := c23.FuncOf[func(c23.Ptr, c23.Ptr, c23.Ptr) int32](cmp)
	q := &qsorter{base: base, size: int(size), tmp: c23.Malloc(size), cmp: func(a, b c23.Ptr) int32 { return f(a, b, arg) }}
	k := int(n)
	for i := k/2 - 1; i >= 0; i-- {
		q.siftDown(i, k)
	}
	for end := k - 1; end > 0; end-- {
		q.swap(0, end)
		q.siftDown(0, end)
	}
}

// Mkdtemp is mkdtemp: it replaces the XXXXXX that ends template with
// what makes the name of a new directory, which it creates (mode 0700).
func Mkdtemp(template c23.Ptr) c23.Ptr {
	n := c23_string.Strlen(template)
	if n < 6 || c23.GoString(c23.Add(template, int(n-6))) != "XXXXXX" {
		c23.SetErrno(c23.EINVAL)
		return c23.Ptr(nil)
	}
	suffix := c23.Add(template, int(n-6))
	const digits = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	for range 238328 { // TMP_MAX
		x := c23.RuntimeRand()
		for i := range 6 {
			c23.Store8(c23.Add(suffix, i), digits[x%62])
			x /= 62
		}
		err := os.Mkdir(c23.GoString(template), 0o700)
		if err == nil {
			return template
		}
		if !c23.IsErr(err, os.ErrExist) {
			c23.SetErrnoOf(err)
			return c23.Ptr(nil)
		}
	}
	c23.SetErrno(c23.EEXIST)
	return c23.Ptr(nil)
}
