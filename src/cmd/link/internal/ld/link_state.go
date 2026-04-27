// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"cmd/internal/dwarf"
	"cmd/link/internal/loader"
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
