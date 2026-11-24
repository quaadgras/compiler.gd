// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stdio

import (
	c23 "internal/c23"
)

// Getdelim is getdelim: it reads up to and including delim into *lineptr,
// which it grows with realloc (*n is its size), and ends it with a null.
// It returns the number of bytes read, or -1 at the end of the file or
// for an error.
func Getdelim(lineptr, n *c23.Void, delim int32, stream *c23.Void) int64 {
	f := c23.FileOf(stream)
	if c23.Addr(lineptr) == 0 || c23.Addr(n) == 0 {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	if !f.Readable() {
		return -1
	}
	var b []byte
	for {
		c := f.Getc()
		if c < 0 {
			break
		}
		b = append(b, byte(c))
		if byte(c) == byte(delim) {
			break
		}
	}
	if len(b) == 0 {
		return -1
	}
	buf, size := c23.LoadPtr(lineptr), c23.LoadLong(n)
	if c23.Addr(buf) == 0 {
		size = 0
	}
	if int64(len(b)+1) > size {
		size = max(int64(len(b)+1), 120)
		buf = c23.Realloc(buf, uint64(size))
		c23.StorePtr(lineptr, buf)
		c23.StoreLong(n, size)
	}
	copy(c23.BytesAt(buf, uintptr(len(b)+1)), append(b, 0))
	return int64(len(b))
}

// Getline is getline: getdelim of '\n'.
func Getline(lineptr, n, stream *c23.Void) int64 { return Getdelim(lineptr, n, '\n', stream) }

func Asprintf(strp, format *c23.Void, va ...any) int32 {
	return Vasprintf(strp, format, c23.VaStart(va))
}

// Vasprintf is vasprintf: it formats into memory it allocates, whose
// address it stores at *strp.
func Vasprintf(strp, format, ap *c23.Void) int32 {
	b := c23.FormatC(c23.GoString(format), c23.VaOf(ap))
	c23.StorePtr(strp, c23.CString(string(b)))
	return int32(len(b))
}

func Dprintf(fd int32, format *c23.Void, va ...any) int32 {
	return Vdprintf(fd, format, c23.VaStart(va))
}

// Vdprintf is vdprintf: it formats to the file descriptor fd, with one
// write.
func Vdprintf(fd int32, format, ap *c23.Void) int32 {
	b := c23.FormatC(c23.GoString(format), c23.VaOf(ap))
	f := c23.FdOS(fd)
	if f == nil {
		return -1
	}
	n, err := f.Write(b)
	if err != nil && n == 0 {
		c23.SetErrnoOf(err)
		return -1
	}
	return int32(n)
}
