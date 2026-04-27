// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"cmd/internal/dwarf"
	"cmd/internal/quoted"
	"cmd/link/internal/loader"
	"cmd/link/internal/sym"
	"flag"
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

	// elf header state (was in elf.go's var block)
	Nelfsym    int     // initialised to 1 by linknew
	elf64      bool
	elfRelType string  // ".rel" or ".rela"
	ehdr       ElfEhdr
	phdr       []*ElfPhdr // initialised in linknew
	shdr       []*ElfShdr // initialised in linknew
	shdrSorted bool
	interp     string

	// thearch and other lib.go state
	thearch Arch

	// atExit hook list and in-process status pointer (was util.go
	// package-level vars). Per-Link so concurrent host.Run invocations
	// don't race on append/drain or pointer assignment.
	atExitFuncs     []func()
	inProcessStatus *int // set by cmd/link/host.Run; Exit writes here + Goexit instead of os.Exit

	// Error reporting state (was package-level vars in lib.go).
	// Read from currentLink by free Errorf/Exitf/afterErrorAction.
	nerrors           int
	liveness          int64 // size of liveness data (funcdata)
	strictDupMsgCount int
	checkStrictDups   int // 0=off 1=warning 2=error

	// flagSet is the per-Link *flag.FlagSet — replaces the previous
	// pattern of mutating flag.CommandLine in Main. Created fresh in
	// Main, populated by setupFlags, parsed against args. Concurrent
	// in-process Main calls don't race on flag.CommandLine.formal
	// (which previously triggered "concurrent map writes" inside
	// flag.(*FlagSet).Var).
	flagSet *flag.FlagSet

	// Flags. Were package-level *T pointers reassigned per Main call;
	// concurrent invocations clobbered each other. Per-Link storage,
	// populated by setupFlags via flagSet.StringVar/BoolVar/etc binding.
	flagBuildid         string
	flagBindNow         bool
	flagOutfile         string
	flagPluginPath      string
	flagFipso           string
	flagInstallSuffix   string
	flagDumpDep         bool
	flagRace            bool
	flagMsan            bool
	flagAsan            bool
	flagAslr            bool
	flagFieldTrack      string
	flagLibGCC          string
	flagTmpdir          string
	flagExtar           string
	flagCaptureHostObjs string
	flagA               bool
	FlagC               bool
	FlagD               bool
	flagF               bool
	flagG               bool
	flagH               bool
	flagN               bool
	FlagS               bool
	flagHostBuildid     string
	flagInterpreter     string
	flagCheckLinkname   bool
	FlagDebugTramp      int
	FlagDebugTextSize   int
	flagDebugNosplit    bool
	FlagStrictDups      int
	FlagRound           int64
	FlagTextAddr        int64
	FlagDataAddr        int64
	FlagFuncAlign       int
	flagEntrySymbol     string
	flagPruneWeakMap    bool
	flagRandLayout      int64
	flagAllErrors       bool
	cpuprofile          string
	memprofile          string
	memprofilerate      int64
	benchmarkFlag       string
	benchmarkFileFlag   string
	flagW               ternaryFlag
	FlagW               bool // -w flag, computed in main from flagW
	flag8               bool // use 64-bit addresses in symbol table
	flagExtld           quoted.Flag
	flagExtldflags      quoted.Flag

	// CarrierSymByType tracks carrier symbols and their sizes (was symtab.go).
	CarrierSymByType [sym.SFirstUnallocated]struct {
		Sym  loader.Sym
		Size int64
	}

	// data.go state
	covCounterDataStartOff uint64
	covCounterDataLen      uint64
	strdata                map[string]string // initialised in linknew
	strnames               []string

	// xcoff.go state (windows-only/AIX-only but always allocated)
	xfile          xcoffFile
	currDwscnoff   map[string]uint64    // initialised in linknew
	currSymSrcFile xcoffSymSrcFile
	outerSymSize   map[string]int64     // initialised in linknew

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
