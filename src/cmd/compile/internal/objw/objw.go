// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package objw

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/bitvec"
	"cmd/compile/internal/types"
	"cmd/internal/obj"
	"encoding/binary"
)

// Uint8 writes an unsigned byte v into s at offset off,
// and returns the next unused offset (i.e., off+1).
func Uint8(gd *base.Invocation, s *obj.LSym, off int, v uint8) int {
	return UintN(gd, s, off, uint64(v), 1)
}

func Uint16(gd *base.Invocation, s *obj.LSym, off int, v uint16) int {
	return UintN(gd, s, off, uint64(v), 2)
}

func Uint32(gd *base.Invocation, s *obj.LSym, off int, v uint32) int {
	return UintN(gd, s, off, uint64(v), 4)
}

func Uintptr(gd *base.Invocation, s *obj.LSym, off int, v uint64) int {
	return UintN(gd, s, off, v, types.PtrSize)
}

// Uvarint writes a varint v into s at offset off,
// and returns the next unused offset.
func Uvarint(gd *base.Invocation, s *obj.LSym, off int, v uint64) int {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], v)
	return int(s.WriteBytes(gd.Ctxt, int64(off), buf[:n]))
}

func Bool(gd *base.Invocation, s *obj.LSym, off int, v bool) int {
	w := 0
	if v {
		w = 1
	}
	return UintN(gd, s, off, uint64(w), 1)
}

// UintN writes an unsigned integer v of size wid bytes into s at offset off,
// and returns the next unused offset.
func UintN(gd *base.Invocation, s *obj.LSym, off int, v uint64, wid int) int {
	if off&(wid-1) != 0 {
		gd.Fatalf("duintxxLSym: misaligned: v=%d wid=%d off=%d", v, wid, off)
	}
	s.WriteInt(gd.Ctxt, int64(off), wid, int64(v))
	return off + wid
}

func SymPtr(gd *base.Invocation, s *obj.LSym, off int, x *obj.LSym, xoff int) int {
	off = int(types.RoundUp(int64(off), int64(types.PtrSize)))
	s.WriteAddr(gd.Ctxt, int64(off), types.PtrSize, x, int64(xoff))
	off += types.PtrSize
	return off
}

func SymPtrWeak(gd *base.Invocation, s *obj.LSym, off int, x *obj.LSym, xoff int) int {
	off = int(types.RoundUp(int64(off), int64(types.PtrSize)))
	s.WriteWeakAddr(gd.Ctxt, int64(off), types.PtrSize, x, int64(xoff))
	off += types.PtrSize
	return off
}

func SymPtrOff(gd *base.Invocation, s *obj.LSym, off int, x *obj.LSym) int {
	s.WriteOff(gd.Ctxt, int64(off), x, 0)
	off += 4
	return off
}

func SymPtrWeakOff(gd *base.Invocation, s *obj.LSym, off int, x *obj.LSym) int {
	s.WriteWeakOff(gd.Ctxt, int64(off), x, 0)
	off += 4
	return off
}

func Global(gd *base.Invocation, s *obj.LSym, width int32, flags int16) {
	if flags&obj.LOCAL != 0 {
		s.Set(obj.AttrLocal, true)
		flags &^= obj.LOCAL
	}
	gd.Ctxt.Globl(s, int64(width), int(flags))
}

// BitVec writes the contents of bv into s as sequence of bytes
// in little-endian order, and returns the next unused offset.
func BitVec(gd *base.Invocation, s *obj.LSym, off int, bv bitvec.BitVec) int {
	// Runtime reads the bitmaps as byte arrays. Oblige.
	for j := 0; int32(j) < bv.N; j += 8 {
		word := bv.B[j/32]
		off = Uint8(gd, s, off, uint8(word>>(uint(j)%32)))
	}
	return off
}
