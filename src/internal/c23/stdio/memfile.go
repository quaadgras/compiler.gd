// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stdio

import (
	"internal/c23"
	"io"
)

// fmemopen and open_memstream: streams whose file is C's memory.

// A MemFile is the memory a memory stream reads and writes.
type MemFile struct {
	Buf  c23.Ptr // the memory
	Size int     // fmemopen's: the buffer's size; open_memstream's: what buf holds
	Pos  int     // the position
	End  int     // the end of the contents
	App  bool

	// open_memstream's: it grows buf, and keeps *bufp and *sizep up to
	// date with buf and the position.
	Dyn         bool
	Bufp, Sizep c23.Ptr
	Binary      bool
}

func (m *MemFile) Read(b []byte) (int, error) {
	if m.Pos >= m.End {
		return 0, io.EOF
	}
	n := copy(b, c23.BytesAt(c23.Add(m.Buf, m.Pos), uintptr(m.End-m.Pos)))
	m.Pos += n
	return n, nil
}

func (m *MemFile) Write(b []byte) (int, error) {
	if m.App {
		m.Pos = m.End
	}
	if m.Dyn {
		need := m.Pos + len(b) + 1
		if need > m.Size {
			n := max(need, 2*m.Size, 64)
			nb := c23.Malloc(uint64(n))
			if m.Size > 0 {
				c23.Memmove(nb, m.Buf, uintptr(m.Size))
			}
			m.Buf, m.Size = nb, n
		}
		if m.Pos > m.End { // a seek past the end: zeros between
			clear(c23.BytesAt(c23.Add(m.Buf, m.End), uintptr(m.Pos-m.End)))
		}
		copy(c23.BytesAt(c23.Add(m.Buf, m.Pos), uintptr(len(b))), b)
		m.Pos += len(b)
		m.End = max(m.End, m.Pos)
		c23.Store8(c23.Add(m.Buf, m.End), 0)
		m.Sync()
		return len(b), nil
	}
	n := min(len(b), max(m.Size-m.Pos, 0))
	if n > 0 {
		copy(c23.BytesAt(c23.Add(m.Buf, m.Pos), uintptr(n)), b[:n])
		m.Pos += n
		m.End = max(m.End, m.Pos)
	}
	// A null after what was written, where there is room, as glibc and
	// musl write it.
	if !m.Binary && m.End < m.Size {
		c23.Store8(c23.Add(m.Buf, m.End), 0)
	}
	if n < len(b) {
		return n, c23.ErrnoErr(c23.ENOSPC)
	}
	return n, nil
}

func (m *MemFile) Seek(off int64, whence int) (int64, error) {
	var base int64
	switch whence {
	case io.SeekCurrent:
		base = int64(m.Pos)
	case io.SeekEnd:
		base = int64(m.End)
	}
	p := base + off
	if p < 0 || !m.Dyn && p > int64(m.Size) {
		return 0, c23.ErrnoErr(c23.EINVAL)
	}
	m.Pos = int(p)
	m.Sync()
	return p, nil
}

// sync sets open_memstream's *bufp and *sizep.
func (m *MemFile) Sync() {
	if !m.Dyn {
		return
	}
	if m.Buf.Obj == nil {
		m.Buf, m.Size = c23.Malloc(1), 1
	}
	c23.StorePtr(m.Bufp, m.Buf)
	c23.StoreLong(m.Sizep, int64(min(m.Pos, m.End)))
}

func (m *MemFile) Close() { m.Sync() }
