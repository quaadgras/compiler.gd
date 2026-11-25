// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package uio

import (
	c23 "internal/c23"
	"io"
)

// iovecs returns the cnt struct iovec {void *iov_base; size_t iov_len}
// at iov, or ok false (errno EINVAL).
func iovecs(iov *c23.Void, cnt int32) (bases []*c23.Void, lens []int, total int, ok bool) {
	if cnt < 0 || cnt > 1024 {
		c23.SetErrno(c23.EINVAL)
		return nil, nil, 0, false
	}
	for i := range int(cnt) {
		e := c23.Add(iov, i*2*int(c23.WordSize))
		n := c23.LoadLong(c23.Add(e, int(c23.WordSize)))
		if n < 0 || int64(total)+n > 1<<62 {
			c23.SetErrno(c23.EINVAL)
			return nil, nil, 0, false
		}
		bases = append(bases, c23.LoadPtr(e))
		lens = append(lens, int(n))
		total += int(n)
	}
	return bases, lens, total, true
}

// Readv is readv: one read, into the buffers in order.
func Readv(fd int32, iov *c23.Void, cnt int32) int64 {
	bases, lens, total, ok := iovecs(iov, cnt)
	if !ok {
		return -1
	}
	f := c23.FdFile(fd)
	if f == nil {
		c23.SetErrno(c23.EBADF)
		return -1
	}
	b := make([]byte, total)
	n, err := f.Read(b) // the descriptor's: a socket, what is read ahead
	if err != nil && n == 0 && total > 0 {
		if err == io.EOF {
			return 0
		}
		c23.SetErrnoOf(err)
		return -1
	}
	rest := b[:n]
	for i, base := range bases {
		k := min(lens[i], len(rest))
		copy(c23.Writable(base, uintptr(k)), rest[:k])
		rest = rest[k:]
	}
	return int64(n)
}

// Writev is writev: the buffers in order, with one write.
func Writev(fd int32, iov *c23.Void, cnt int32) int64 {
	bases, lens, total, ok := iovecs(iov, cnt)
	if !ok {
		return -1
	}
	f := c23.FdFile(fd)
	if f == nil {
		c23.SetErrno(c23.EBADF)
		return -1
	}
	b := make([]byte, 0, total)
	for i, base := range bases {
		b = append(b, c23.BytesAt(base, uintptr(lens[i]))...)
	}
	n, err := f.Write(b)
	if err != nil && n == 0 && total > 0 {
		c23.SetErrnoOf(err)
		return -1
	}
	return int64(n)
}
