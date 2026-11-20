// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"bytes"
	"cmd/internal/portpack"
	"cmd/internal/sys"
	"cmd/link/internal/loader"
	"cmd/link/internal/sym"
	"debug/elf"
	"encoding/binary"
	"internal/buildcfg"
	"slices"
	"strings"
)

// A GOOS=portable program written as a PE or a Mach-O is an ELF executable
// too: its header area has the ELF header and program headers of the
// same segments, at the same addresses and file offsets, which
// cmd/internal/portpack puts at the start of the file it copies out for
// an ELF host. So one image of the program's code serves all its hosts.

// portableELFEntry is the entry of the ELF hosts.
func portableELFEntry() string {
	return "_rt0_" + buildcfg.GOARCH + "_portable"
}

// portableELFHeader returns the ELF header, program headers and notes of the
// image, to be at off in the file.
func portableELFHeader(ctxt *Link, off int64) []byte {
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
			Align:  uint64(portpack.ELFAlign),
		})
	}
	load(&ctxt.Segtext, elf.PF_R|elf.PF_X)
	load(&ctxt.Segrodata, elf.PF_R)
	load(&ctxt.Segrelrodata, elf.PF_R)
	load(&ctxt.Segdata, elf.PF_R|elf.PF_W)

	// The FreeBSD ABI tag (a GOOS=portable program is a FreeBSD program
	// too; Linux does not look at it).
	var note bytes.Buffer
	binary.Write(&note, binary.LittleEndian, [3]uint32{ELF_NOTE_FREEBSD_NAMESZ, ELF_NOTE_FREEBSD_DESCSZ, ELF_NOTE_FREEBSD_ABI_TAG})
	note.WriteString(ELF_NOTE_FREEBSD_NAME)
	binary.Write(&note, binary.LittleEndian, uint32(ELF_NOTE_FREEBSD_VERSION))

	// Where darwin's loader finds what the image imports of darwin's
	// libraries (see portableDarwinTable): no other host looks at it.
	var darwin []elf.Prog64
	if t := ctxt.loader.Lookup(portableDarwinTableName, 0); t != 0 && ctxt.loader.AttrReachable(t) {
		v, n := uint64(ctxt.loader.SymValue(t)), uint64(ctxt.loader.SymSize(t))
		darwin = append(darwin, elf.Prog64{Type: portpack.PT_PORTABLE_DARWIN, Flags: uint32(elf.PF_R), Vaddr: v, Paddr: v, Filesz: n, Memsz: n, Align: 8})
	}

	nph := len(phs) + 2 + len(darwin)
	phoff := off + int64(binary.Size(elf.Header64{}))
	noteoff := phoff + int64(nph*binary.Size(elf.Prog64{}))
	phs = append(phs,
		elf.Prog64{Type: uint32(elf.PT_NOTE), Flags: uint32(elf.PF_R), Off: uint64(noteoff), Filesz: uint64(note.Len()), Memsz: uint64(note.Len()), Align: 4},
		elf.Prog64{Type: uint32(elf.PT_GNU_STACK), Flags: uint32(elf.PF_R | elf.PF_W), Align: 8},
	)
	phs = append(phs, darwin...)

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
		Exitf("GOOS=portable: no ELF machine for %s", ctxt.Arch.Name)
	}
	eh.Version = uint32(elf.EV_CURRENT)
	entry := ctxt.loader.Lookup(portableELFEntry(), 0)
	if entry == 0 {
		Exitf("GOOS=portable: no %s", portableELFEntry())
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

// A GOOS=portable program written as a PE that has darwin for a host has no
// Mach-O for dyld to bind darwin's imports in: darwin's loader of the
// program's file (cmd/internal/portpack/loader/loader_darwin.c) maps the
// image as the ELF executable it is and binds them itself. An import is
// a function that jumps to the address in a word of data, its slot, and
// portableDarwinTableName is the table of the slots and their names, which
// the PT_PORTABLE_DARWIN program header points at:
//
//	the address of the entry point of darwin
//	the number of libraries
//	the number of imports
//	for each library, the address of its name
//	for each import, the address of its slot and of its name
//
// (words of 8 bytes, names as C strings).

const portableDarwinTableName = "go:portable.darwin"

// portableDarwinEntry is the entry point darwin's loader calls.
func portableDarwinEntry() string {
	return "_rt0_" + buildcfg.GOARCH + "_portable_darwin"
}

// portableDarwinHost reports whether darwin's loader is to bind the imports
// of the image.
func portableDarwinHost(ctxt *Link) bool {
	if !ctxt.portablePE {
		return false
	}
	for _, h := range buildcfg.PortableHosts(buildcfg.GOARCH) {
		if h == "darwin" {
			return true
		}
	}
	return false
}

// portableDarwinLib reports whether lib is a library of darwin.
func portableDarwinLib(lib string) bool {
	return strings.HasSuffix(lib, ".dylib") || strings.HasPrefix(lib, "/System/Library/Frameworks/")
}

type portableDarwinImp struct {
	stub, slot loader.Sym
	name       string
}

// portableDarwinImport makes local the import of darwin's name from lib.
func portableDarwinImport(ctxt *Link, local, name, lib string) {
	if lib != "" && !slices.Contains(ctxt.portableDarwinLibs, lib) {
		ctxt.portableDarwinLibs = append(ctxt.portableDarwinLibs, lib)
	}
	if local == "_" {
		return
	}
	if ctxt.Arch.Family != sys.AMD64 {
		Exitf("GOOS=portable: no darwin imports for %s", ctxt.Arch.Name)
	}
	if lib == "/usr/lib/libSystem.B.dylib" {
		// The functions of 64-bit inodes (see machosymorder).
		switch name {
		case "fdopendir", "readdir_r", "getfsstat":
			name += "$INODE64"
		}
	}
	l := ctxt.loader
	s := l.LookupOrCreateSym(local, 0)
	if st := l.SymType(s); st != 0 && st != sym.SXREF {
		return
	}
	slot := l.CreateSymForUpdate(local+".portable.slot", 0)
	slot.SetType(sym.SNOPTRDATA)
	slot.AddUint64(ctxt.Arch, 0)
	slot.SetLocal(true)

	su := l.MakeSymbolUpdater(s)
	su.SetType(sym.STEXT)
	su.AddUint8(0xff) // JMP *slot(IP)
	su.AddUint8(0x25)
	su.AddPCRelPlus(ctxt.Arch, slot.Sym(), 0)
	su.SetLocal(true)
	ctxt.Textp = append(ctxt.Textp, s)
	ctxt.portableDarwinImps = append(ctxt.portableDarwinImps, portableDarwinImp{s, slot.Sym(), name})
}

// portableDarwinTable makes the table of the imports the program uses.
func portableDarwinTable(ctxt *Link) {
	if !portableDarwinHost(ctxt) {
		return
	}
	l := ctxt.loader
	entry := l.Lookup(portableDarwinEntry(), 0)
	if entry == 0 {
		Exitf("GOOS=portable: no %s", portableDarwinEntry())
	}
	var imps []portableDarwinImp
	for _, imp := range ctxt.portableDarwinImps {
		if l.AttrReachable(imp.stub) {
			imps = append(imps, imp)
		}
	}
	names := l.CreateSymForUpdate(portableDarwinTableName+".names", 0)
	names.SetType(sym.SNOPTRDATA)
	names.SetLocal(true)
	names.SetReachable(true)
	t := l.CreateSymForUpdate(portableDarwinTableName, 0)
	t.SetType(sym.SNOPTRDATA)
	t.SetLocal(true)
	t.SetReachable(true)
	t.AddAddrPlus(ctxt.Arch, entry, 0)
	t.AddUint64(ctxt.Arch, uint64(len(ctxt.portableDarwinLibs)))
	t.AddUint64(ctxt.Arch, uint64(len(imps)))
	for _, lib := range ctxt.portableDarwinLibs {
		t.AddAddrPlus(ctxt.Arch, names.Sym(), names.Addstring(lib))
	}
	for _, imp := range imps {
		t.AddAddrPlus(ctxt.Arch, imp.slot, 0)
		t.AddAddrPlus(ctxt.Arch, names.Sym(), names.Addstring(imp.name))
	}
}
