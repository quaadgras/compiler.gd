// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package riscv64

import (
	"cmd/internal/objabi"
	"cmd/internal/sys"
	"cmd/link/internal/ld"
)

func Init() (*sys.Arch, ld.Arch) {
	arch := sys.ArchRISCV64

	theArch := ld.Arch{
		Funcalign:  funcAlign,
		Maxalign:   maxAlign,
		Minalign:   minAlign,
		Dwarfregsp: dwarfRegSP,
		Dwarfreglr: dwarfRegLR,

		Adddynrel:        adddynrel,
		Archinit:         archinit,
		Archreloc:        archreloc,
		Archrelocvariant: archrelocvariant,
		Extreloc:         extreloc,

		// TrampLimit is set such that we always run the trampoline
		// generation code. This is necessary since calls to external
		// symbols require the use of trampolines, regardless of the
		// text size.
		TrampLimit: 1,
		Trampoline: trampoline,

		Gentext:     gentext,
		GenSymsLate: genSymsLate,
		Machoreloc1: machoreloc1,

		ELF: ld.ELFArch{
			Linuxdynld:     "/lib/ld-linux-riscv64-lp64d.so.1",
			LinuxdynldMusl: "/lib/ld-musl-riscv64.so.1",

			Freebsddynld:   "/usr/libexec/ld-elf.so.1",
			Netbsddynld:    "XXX",
			Openbsddynld:   "/usr/libexec/ld.so",
			Dragonflydynld: "XXX",
			Solarisdynld:   "XXX",

			Reloc1:    elfreloc1,
			RelocSize: 24,
			SetupPLT:  elfsetupplt,
		},
	}

	return arch, theArch
}

func archinit(ctxt *ld.Link) {
	switch ctxt.HeadType {
	case objabi.Hlinux, objabi.Hfreebsd, objabi.Hopenbsd:
		ld.Elfinit(ctxt)
		ctxt.HEADR = ld.ELFRESERVE
		if ctxt.FlagRound == -1 {
			ctxt.FlagRound = 0x10000
		}
		if ctxt.FlagTextAddr == -1 {
			ctxt.FlagTextAddr = ld.Rnd(0x10000, ctxt.FlagRound) + int64(ctxt.HEADR)
		}
	default:
		ld.Exitf("unknown -H option: %v", ctxt.HeadType)
	}
}
