// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"bytes"
	"cmd/internal/apepack"
	"cmd/internal/sys"
	"cmd/link/internal/sym"
	"debug/elf"
	"encoding/binary"
	"internal/buildcfg"
)

// A GOOS=ape program written as a PE or a Mach-O is an ELF executable
// too: its header area has the ELF header and program headers of the
// same segments, at the same addresses and file offsets, which
// cmd/internal/apepack puts at the start of the file it copies out for
// an ELF host. So one image of the program's code serves all its hosts.

// apeELFEntry is the entry of the ELF hosts.
func apeELFEntry() string {
	return "_rt0_" + buildcfg.GOARCH + "_ape"
}

// apeELFHeader returns the ELF header, program headers and notes of the
// image, to be at off in the file.
func apeELFHeader(ctxt *Link, off int64) []byte {
	var phs []elf.Prog64
	load := func(seg *sym.Segment, flags elf.ProgFlag) {
		if seg.Length == 0 {
			return
		}
		phs = append(phs, elf.Prog64{
			Type:   uint32(elf.PT_LOAD),
			Flags:  uint32(flags),
			Off:    seg.Fileoff,
			Vaddr:  seg.Vaddr,
			Paddr:  seg.Vaddr,
			Filesz: seg.Filelen,
			Memsz:  seg.Length,
			Align:  uint64(apepack.ELFAlign),
		})
	}
	load(&ctxt.Segtext, elf.PF_R|elf.PF_X)
	load(&ctxt.Segrodata, elf.PF_R)
	load(&ctxt.Segrelrodata, elf.PF_R)
	load(&ctxt.Segdata, elf.PF_R|elf.PF_W)

	// The FreeBSD ABI tag (a GOOS=ape program is a FreeBSD program
	// too; Linux does not look at it).
	var note bytes.Buffer
	binary.Write(&note, binary.LittleEndian, [3]uint32{ELF_NOTE_FREEBSD_NAMESZ, ELF_NOTE_FREEBSD_DESCSZ, ELF_NOTE_FREEBSD_ABI_TAG})
	note.WriteString(ELF_NOTE_FREEBSD_NAME)
	binary.Write(&note, binary.LittleEndian, uint32(ELF_NOTE_FREEBSD_VERSION))

	nph := len(phs) + 2
	phoff := off + int64(binary.Size(elf.Header64{}))
	noteoff := phoff + int64(nph*binary.Size(elf.Prog64{}))
	phs = append(phs,
		elf.Prog64{Type: uint32(elf.PT_NOTE), Flags: uint32(elf.PF_R), Off: uint64(noteoff), Filesz: uint64(note.Len()), Memsz: uint64(note.Len()), Align: 4},
		elf.Prog64{Type: uint32(elf.PT_GNU_STACK), Flags: uint32(elf.PF_R | elf.PF_W), Align: 8},
	)

	var eh elf.Header64
	copy(eh.Ident[:], elf.ELFMAG)
	eh.Ident[elf.EI_CLASS] = byte(elf.ELFCLASS64)
	eh.Ident[elf.EI_DATA] = byte(elf.ELFDATA2LSB)
	eh.Ident[elf.EI_VERSION] = byte(elf.EV_CURRENT)
	eh.Ident[elf.EI_OSABI] = byte(elf.ELFOSABI_FREEBSD)
	eh.Type = uint16(elf.ET_EXEC)
	switch ctxt.Arch.Family {
	case sys.AMD64:
		eh.Machine = uint16(elf.EM_X86_64)
	case sys.ARM64:
		eh.Machine = uint16(elf.EM_AARCH64)
	default:
		Exitf("GOOS=ape: no ELF machine for %s", ctxt.Arch.Name)
	}
	eh.Version = uint32(elf.EV_CURRENT)
	entry := ctxt.loader.Lookup(apeELFEntry(), 0)
	if entry == 0 {
		Exitf("GOOS=ape: no %s", apeELFEntry())
	}
	eh.Entry = uint64(ctxt.loader.SymValue(entry))
	eh.Phoff = uint64(phoff)
	eh.Ehsize = uint16(binary.Size(eh))
	eh.Phentsize = uint16(binary.Size(elf.Prog64{}))
	eh.Phnum = uint16(nph)
	eh.Shentsize = uint16(binary.Size(elf.Section64{}))

	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, &eh)
	binary.Write(&b, binary.LittleEndian, phs)
	b.Write(note.Bytes())
	return b.Bytes()
}
