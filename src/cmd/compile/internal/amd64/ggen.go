// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package amd64

import (
	"math/bits"

	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/objw"
	"cmd/compile/internal/ssa"
	"cmd/internal/obj"
	"cmd/internal/obj/x86"
)

func zerorange(gd *base.Invocation, pp *objw.Progs, p *obj.Prog, off, cnt int64, state *uint32) *obj.Prog {
	if cnt%8 != 0 {
		panic("zeroed region not aligned")
	}
	for cnt >= 16 {
		p = pp.Append(gd, p, x86.AMOVUPS, obj.TYPE_REG, x86.REG_X15, 0, obj.TYPE_MEM, x86.REG_SP, off)
		off += 16
		cnt -= 16
	}
	if cnt != 0 {
		p = pp.Append(gd, p, x86.AMOVQ, obj.TYPE_REG, x86.REG_X15, 0, obj.TYPE_MEM, x86.REG_SP, off)
	}
	return p
}

// cshadowrange writes bytes to the shadow of C's memory from the byte
// of the word that n begins in: the address of the word, shifted, with
// the bit of the shadow's base, which no shifted address has.
func cshadowrange(gd *base.Invocation, pp *objw.Progs, p *obj.Prog, n *ir.Name, bytes []byte, set []bool) *obj.Prog {
	p = pp.Append(gd, p, x86.ALEAQ, obj.TYPE_MEM, x86.REG_SP, n.FrameOffset(), obj.TYPE_REG, x86.REG_R12, 0)
	p.From.Sym = n.Linksym()
	p.From.Name = obj.NAME_AUTO
	if n.Class == ir.PPARAM || n.Class == ir.PPARAMOUT && !n.IsOutputParamInRegisters() {
		p.From.Name = obj.NAME_PARAM
	}
	p = pp.Append(gd, p, x86.ASHRQ, obj.TYPE_CONST, 0, 3, obj.TYPE_REG, x86.REG_R12, 0)
	p = pp.Append(gd, p, x86.ABTSQ, obj.TYPE_CONST, 0, int64(bits.TrailingZeros64(ssa.CShadowBase)), obj.TYPE_REG, x86.REG_R12, 0)
	store := func(bytes []byte, set func(int) bool) {
		for i := 0; i < len(bytes); {
			if !set(i) {
				i++
				continue
			}
			switch left := len(bytes) - i; {
			case left >= 4:
				v := uint32(bytes[i]) | uint32(bytes[i+1])<<8 | uint32(bytes[i+2])<<16 | uint32(bytes[i+3])<<24
				p = pp.Append(gd, p, x86.AMOVL, obj.TYPE_CONST, 0, int64(int32(v)), obj.TYPE_MEM, x86.REG_R12, int64(i))
				i += 4
			case left >= 2:
				v := uint16(bytes[i]) | uint16(bytes[i+1])<<8
				p = pp.Append(gd, p, x86.AMOVW, obj.TYPE_CONST, 0, int64(int16(v)), obj.TYPE_MEM, x86.REG_R12, int64(i))
				i += 2
			default:
				p = pp.Append(gd, p, x86.AMOVB, obj.TYPE_CONST, 0, int64(int8(bytes[i])), obj.TYPE_MEM, x86.REG_R12, int64(i))
				i++
			}
		}
	}
	store(bytes, func(i int) bool { return set[i] })
	return p
}

func ginsnop(gd *base.Invocation, pp *objw.Progs) *obj.Prog {
	// This is a hardware nop (1-byte 0x90) instruction,
	// even though we describe it as an explicit XCHGL here.
	// Particularly, this does not zero the high 32 bits
	// like typical *L opcodes.
	// (gas assembles "xchg %eax,%eax" to 0x87 0xc0, which
	// does zero the high 32 bits.)
	p := pp.Prog(gd, x86.AXCHGL)
	p.From.Type = obj.TYPE_REG
	p.From.Reg = x86.REG_AX
	p.To.Type = obj.TYPE_REG
	p.To.Reg = x86.REG_AX
	return p
}
