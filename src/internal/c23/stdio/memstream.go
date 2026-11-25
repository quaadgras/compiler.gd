// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stdio

import (
	"bufio"
	c23 "internal/c23"
)

// newMemFile returns a stream of m.
func newMemFile(m *MemFile, rd, wr bool) *c23.Void { return NewMemStream(m, rd, wr) }

// NewMemStream returns a stream of m, which it reads and writes as a
// file.
func NewMemStream(m c23.MemStream, rd, wr bool) *c23.Void {
	fl := &c23.File{Fd: -1, Mem: m, Rd: rd, Wr: wr}
	fl.W = bufio.NewWriter(c23.FdIO{F: fl})
	return c23.NewStream(fl)
}

// Fmemopen is fmemopen: a stream of the size bytes at buf, or of size
// bytes it allocates if buf is null. Mode r reads them all; w starts with
// none (it stores a null at buf); a starts after the first null. A write
// stores a null after what it wrote where there is room, unless the mode
// has b.
func Fmemopen(buf *c23.Void, size uint64, mode *c23.Void) *c23.Void {
	md := c23.GoString(mode)
	_, rd, wr, ok := openMode(md)
	if !ok || size == 0 {
		c23.SetErrno(c23.EINVAL)
		return (*c23.Void)(nil)
	}
	m := &MemFile{Buf: buf, Size: int(size)}
	for i := 1; i < len(md); i++ {
		m.Binary = m.Binary || md[i] == 'b'
	}
	if c23.Addr(buf) == 0 {
		m.Buf = c23.Malloc(size)
	}
	switch md[0] {
	case 'r':
		m.End = m.Size
	case 'w':
		c23.Store8(m.Buf, 0)
	case 'a':
		b := c23.BytesAt(m.Buf, uintptr(m.Size))
		for m.End < m.Size && b[m.End] != 0 {
			m.End++
		}
		m.Pos = m.End
		m.App = true
	}
	return newMemFile(m, rd, wr)
}

// OpenMemstream is open_memstream: a stream that writes to memory it
// allocates and grows, whose address and the position (the size written)
// fflush and fclose store at *bufp and *sizep. What it holds ends with a
// null; C frees it after fclose.
func OpenMemstream(bufp, sizep *c23.Void) *c23.Void {
	m := &MemFile{Dyn: true, Bufp: bufp, Sizep: sizep}
	m.Sync()
	return newMemFile(m, false, true)
}
