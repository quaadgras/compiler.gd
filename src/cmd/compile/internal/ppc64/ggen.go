// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ppc64

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/objw"
	"cmd/compile/internal/types"
	"cmd/internal/obj"
	"cmd/internal/obj/ppc64"
)

func zerorange(gd *base.Invocation, pp *objw.Progs, p *obj.Prog, off, cnt int64, _ *uint32) *obj.Prog {
	if cnt == 0 {
		return p
	}
	if cnt < int64(4*types.PtrSize) {
		for i := int64(0); i < cnt; i += int64(types.PtrSize) {
			p = pp.Append(gd, p, ppc64.AMOVD, obj.TYPE_REG, ppc64.REGZERO, 0, obj.TYPE_MEM, ppc64.REGSP, gd.Ctxt.Arch.FixedFrameSize+off+i)
		}
	} else if cnt <= int64(128*types.PtrSize) {
		p = pp.Append(gd, p, ppc64.AADD, obj.TYPE_CONST, 0, gd.Ctxt.Arch.FixedFrameSize+off-8, obj.TYPE_REG, ppc64.REGRT1, 0)
		p.Reg = ppc64.REGSP
		p = pp.Append(gd, p, obj.ADUFFZERO, obj.TYPE_NONE, 0, 0, obj.TYPE_MEM, 0, 0)
		p.To.Name = obj.NAME_EXTERN
		p.To.Sym = ir.Syms(gd).Duffzero
		p.To.Offset = 4 * (128 - cnt/int64(types.PtrSize))
	} else {
		p = pp.Append(gd, p, ppc64.AMOVD, obj.TYPE_CONST, 0, gd.Ctxt.Arch.FixedFrameSize+off-8, obj.TYPE_REG, ppc64.REGTMP, 0)
		p = pp.Append(gd, p, ppc64.AADD, obj.TYPE_REG, ppc64.REGTMP, 0, obj.TYPE_REG, ppc64.REGRT1, 0)
		p.Reg = ppc64.REGSP
		p = pp.Append(gd, p, ppc64.AMOVD, obj.TYPE_CONST, 0, cnt, obj.TYPE_REG, ppc64.REGTMP, 0)
		p = pp.Append(gd, p, ppc64.AADD, obj.TYPE_REG, ppc64.REGTMP, 0, obj.TYPE_REG, ppc64.REGRT2, 0)
		p.Reg = ppc64.REGRT1
		p = pp.Append(gd, p, ppc64.AMOVDU, obj.TYPE_REG, ppc64.REGZERO, 0, obj.TYPE_MEM, ppc64.REGRT1, int64(types.PtrSize))
		p1 := p
		p = pp.Append(gd, p, ppc64.ACMP, obj.TYPE_REG, ppc64.REGRT1, 0, obj.TYPE_REG, ppc64.REGRT2, 0)
		p = pp.Append(gd, p, ppc64.ABNE, obj.TYPE_NONE, 0, 0, obj.TYPE_BRANCH, 0, 0)
		p.To.SetTarget(p1)
	}

	return p
}

func ginsnop(gd *base.Invocation, pp *objw.Progs) *obj.Prog {
	// Generate the preferred hardware nop: ori 0,0,0
	p := pp.Prog(gd, ppc64.AOR)
	p.From = obj.Addr{Type: obj.TYPE_CONST, Offset: 0}
	p.To = obj.Addr{Type: obj.TYPE_REG, Reg: ppc64.REG_R0}
	return p
}
