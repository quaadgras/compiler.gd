// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"bufio"
	"io"
)

// fmemopen and open_memstream: streams whose file is C's memory.

// A memFile is the memory a memory stream reads and writes.
type memFile struct {
	buf  Ptr // the memory
	size int // fmemopen's: the buffer's size; open_memstream's: what buf holds
	pos  int // the position
	end  int // the end of the contents
	app  bool

	// open_memstream's: it grows buf, and keeps *bufp and *sizep up to
	// date with buf and the position.
	dyn         bool
	bufp, sizep Ptr
	binary      bool
}

func (m *memFile) read(b []byte) (int, error) {
	if m.pos >= m.end {
		return 0, io.EOF
	}
	n := copy(b, bytesAt(Add(m.buf, m.pos), uintptr(m.end-m.pos)))
	m.pos += n
	return n, nil
}

func (m *memFile) write(b []byte) (int, error) {
	if m.app {
		m.pos = m.end
	}
	if m.dyn {
		need := m.pos + len(b) + 1
		if need > m.size {
			n := max(need, 2*m.size, 64)
			nb := Malloc(uint64(n))
			if m.size > 0 {
				Memmove(nb, m.buf, uintptr(m.size))
			}
			m.buf, m.size = nb, n
		}
		if m.pos > m.end { // a seek past the end: zeros between
			clear(bytesAt(Add(m.buf, m.end), uintptr(m.pos-m.end)))
		}
		copy(bytesAt(Add(m.buf, m.pos), uintptr(len(b))), b)
		m.pos += len(b)
		m.end = max(m.end, m.pos)
		Store8(Add(m.buf, m.end), 0)
		m.sync()
		return len(b), nil
	}
	n := min(len(b), max(m.size-m.pos, 0))
	if n > 0 {
		copy(bytesAt(Add(m.buf, m.pos), uintptr(n)), b[:n])
		m.pos += n
		m.end = max(m.end, m.pos)
	}
	// A null after what was written, where there is room, as glibc and
	// musl write it.
	if !m.binary && m.end < m.size {
		Store8(Add(m.buf, m.end), 0)
	}
	if n < len(b) {
		return n, errnoErr(ENOSPC)
	}
	return n, nil
}

func (m *memFile) seek(off int64, whence int) (int64, error) {
	var base int64
	switch whence {
	case io.SeekCurrent:
		base = int64(m.pos)
	case io.SeekEnd:
		base = int64(m.end)
	}
	p := base + off
	if p < 0 || !m.dyn && p > int64(m.size) {
		return 0, errnoErr(EINVAL)
	}
	m.pos = int(p)
	m.sync()
	return p, nil
}

// sync sets open_memstream's *bufp and *sizep.
func (m *memFile) sync() {
	if !m.dyn {
		return
	}
	if m.buf.obj == nil {
		m.buf, m.size = Malloc(1), 1
	}
	StorePtr(m.bufp, m.buf)
	storeLong(m.sizep, int64(min(m.pos, m.end)))
}

func (m *memFile) close() { m.sync() }

// newMemFile returns a stream of m.
func newMemFile(m *memFile, rd, wr bool) Ptr {
	p := New[File]()
	fl := (*File)(p.obj)
	fl.fd = -1
	fl.mem = m
	fl.w = bufio.NewWriter(fdIO{fl})
	fl.rd, fl.wr = rd, wr
	filesMu.Lock()
	files = append(files, fl)
	filesMu.Unlock()
	return p
}

// Fmemopen is fmemopen: a stream of the size bytes at buf, or of size
// bytes it allocates if buf is null. Mode r reads them all; w starts with
// none (it stores a null at buf); a starts after the first null. A write
// stores a null after what it wrote where there is room, unless the mode
// has b.
func Fmemopen(buf Ptr, size uint64, mode Ptr) Ptr {
	md := GoString(mode)
	_, rd, wr, ok := openMode(md)
	if !ok || size == 0 {
		SetErrno(EINVAL)
		return Ptr{}
	}
	m := &memFile{buf: buf, size: int(size)}
	for i := 1; i < len(md); i++ {
		m.binary = m.binary || md[i] == 'b'
	}
	if Addr(buf) == 0 {
		m.buf = Malloc(size)
	}
	switch md[0] {
	case 'r':
		m.end = m.size
	case 'w':
		Store8(m.buf, 0)
	case 'a':
		b := bytesAt(m.buf, uintptr(m.size))
		for m.end < m.size && b[m.end] != 0 {
			m.end++
		}
		m.pos = m.end
		m.app = true
	}
	return newMemFile(m, rd, wr)
}

// OpenMemstream is open_memstream: a stream that writes to memory it
// allocates and grows, whose address and the position (the size written)
// fflush and fclose store at *bufp and *sizep. What it holds ends with a
// null; C frees it after fclose.
func OpenMemstream(bufp, sizep Ptr) Ptr {
	m := &memFile{dyn: true, bufp: bufp, sizep: sizep}
	m.sync()
	return newMemFile(m, false, true)
}
