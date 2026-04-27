// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"cmd/internal/dwarf"
	"cmd/link/internal/loader"
	"cmd/link/internal/sym"
)

// linkState holds per-Invocation linker state that was previously
// stored in package-level vars in dwarf.go, elf.go, lib.go, etc.
// Embedded into *Link by the gd fork so concurrent in-process linker
// runs (cmd/link/host.Run) don't alias each other.
//
// Fields are populated by internal-tooling/link-state-rewrite, which
// rewrites unqualified reads/writes of the original package vars
// into selector accesses against the *Link in scope.
type linkState struct {
	// dwarf state (was in dwarf.go)
	gdbscript     string                  // gdb auto-load script path
	dwarfp        []dwarfSecInfo          // collected DWARF symbols
	dwtypes       dwarf.DWDie             // root DWARF type DIE
	prototypedies map[string]*dwarf.DWDie // prototype DWARF DIEs
	dwsectCUSize  map[string]uint64       // per-CU section sizes (mutex-guarded by dwsectCUSizeMu)

	// elf state (was in elf.go)
	elfstrdat     []byte // contents of .shstrtab
	buildinfoData []byte // .note.gnu.build-id payload (renamed from var "buildinfo" to avoid colliding with (*Link).buildinfo method)
	elfverneed    int    // count of .gnu.version_r entries

	// segments (was in lib.go)
	Segtext      sym.Segment
	Segrodata    sym.Segment
	Segrelrodata sym.Segment
	Segdata      sym.Segment
	Segdwarf     sym.Segment
	Segpdata     sym.Segment // windows-only
	Segxdata     sym.Segment // windows-only
	Segments     []*sym.Segment // initialised in linknew with pointers to the above

	// lib.go scalars
	dynlib          []string
	ldflag          []string
	havedynamic     int
	Funcalign       int
	iscgo           bool
	elfglobalsymndx int
	interpreter     string
	debug_s         bool
	HEADR           int32
	hostobj         []Hostobj
	hostobjcounter  int
	lcSize          int32
	spSize          int32
	symSize         int32
	abiInternalVer  int    // sym.SymVerABIInternal in linknew
	rpath           Rpath

	// misc state migrated from various files
	pkglistfornote   []byte    // was in main.go
	windowsgui       bool      // was in main.go
	ownTmpDir        bool      // was in main.go
	fipsinfo         loader.Sym // was in fips140.go
	fipsSyms         []fipsSym  // initialised in linknew via newFipsSyms
	seenlib          map[string]bool // was in go.go
	sehp             sehTables       // was sehp anon struct in seh.go
	theline          string          // was in lib.go
	externalobj      bool            // was in lib.go
	dynimportfail    []string        // was in lib.go
	preferlinkext    []string        // was in lib.go
	unknownObjFormat bool            // was in lib.go

	// pe state (was in pe.go)
	PEBASE      int64
	PESECTALIGN int64 // initialised to 0x1000 by linknew
	PEFILEALIGN int64 // initialised to 0x200 by linknew
	rsrcsyms    []loader.Sym
	PESECTHEADR int32
	PEFILEHEADR int32
	pe64        bool
	dr          *Dll
	dexport     []loader.Sym
	isLabel     map[loader.Sym]bool
	pefile      peFile

	// mach-o state (was in macho.go)
	machohdr      MachoHdr
	load          []MachoLoad
	machoPlatform MachoPlatform
	seg           [16]MachoSeg
	nseg          int
	ndebug        int
	nsect         int
	nkind         [NumSymKind]int
	sortsym       []loader.Sym
	nsortsym      int
	loadBudget    int // initialised to INITIAL_MACHO_HEADR-2*1024 by linknew
	dylib         []string
	linkoff       int64
	machorebase   []machoRebaseRecord
	machobind     []machoBindRecord
}
