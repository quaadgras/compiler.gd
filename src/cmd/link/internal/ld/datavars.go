// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"cmd/internal/objabi"
	"cmd/link/internal/loader"
	"cmd/link/internal/sym"
	"cmp"
	"slices"
)

// The variables of the program's data (doc/gd/unsafe.md).
//
// The runtime's checks of unsafe pointers keep arithmetic on a pointer
// to a variable in the variable, and compare a conversion of one with
// the variable's type, as they do for an object of the heap. For a
// variable of the program's data that is the symbol: runtime.datavars is
// a table of the symbols that Go code can have a pointer to, each with
// where it is, its size and its Go type, if the object file has one for
// it. The runtime's dataVarOf looks an address up in it.
//
// The table has a part for each section that such symbols are in, and a
// symbol's place is its offset from the start of its section, which
// moduledata has: sections do not move relative to their own start
// when another linker lays the program out. A part is sorted by offset.
//
// The format must match runtime/datavar.go.

// The sections that the table has a part for.
const (
	dataVarRodata = iota
	dataVarRelro
	dataVarNoptrdata
	dataVarData
	dataVarBss
	dataVarNoptrbss
	dataVarSects
)

const (
	// The table begins with the index of the first entry of each
	// section, the number of entries after them, and then where
	// the last symbol of each section ends.
	dataVarHeader = 4 * (dataVarSects + 1 + dataVarSects)
	// An entry is the symbol's offset in its section, and either the
	// offset of its type in the module's types, or with dataVarNoType
	// its size.
	dataVarEntry = 8

	dataVarNoType  = 1 << 31 // the entry has a size and no type: the symbol is bytes
	dataVarUnknown = 1 << 30 // with dataVarNoType: what the symbol holds is not known
	dataVarMaxSize = 1<<30 - 1
)

// dataVarKind reports whether a symbol of kind t is one that the table
// has: a variable, a string's bytes or other read-only data, and not what
// is the runtime's and the linker's own. (The bits that the collector
// has for a type are neither, but the bytes of a string are in such a
// symbol if they are the same bytes: symbols that are named by what
// they hold are one symbol.)
func dataVarKind(t sym.SymKind) bool {
	switch t {
	case sym.SGOSTRING, sym.SGCBITS, sym.SRODATA, sym.SRODATAFIPS, sym.SRODATARELRO,
		sym.SNOPTRDATA, sym.SNOPTRDATAFIPS, sym.SDATA, sym.SDATAFIPS,
		sym.SBSS, sym.SNOPTRBSS, sym.SCOVERAGE_COUNTER:
		return true
	}
	return false
}

// dataVarsEnabled reports whether the link has the table.
func (ctxt *Link) dataVarsEnabled() bool {
	return ctxt.FlagDataVars && !ctxt.IsWasm()
}

// dataVarTypes reports whether the Go type of the symbol s is kept for
// the table, if s is one of its symbols: deadcode marks it then. The
// types of the runtime's own variables are not: the runtime's packages
// are not checked, a pointer to a variable of theirs is seldom given to
// code that is, and the descriptors of their types, which nothing else
// asks for, are several times the table. Such a variable is in the table
// with its size, and what it holds is not known.
func (ctxt *Link) dataVarTypes(s loader.Sym) bool {
	ldr := ctxt.loader
	return ctxt.dataVarsEnabled() && dataVarKind(ldr.SymType(s)) && !ctxt.DynlinkingGo() &&
		!objabi.LookupPkgSpecial(ldr.SymPkg(s)).Runtime
}

// dataVarsSym makes runtime.datavars, with nothing in it yet: symtab
// calls it, since moduledata has its address.
func (ctxt *Link) dataVarsSym() loader.Sym {
	if !ctxt.dataVarsEnabled() {
		return 0
	}
	ldr := ctxt.loader
	s := ctxt.createGeneratorSymbol("runtime.datavars", 0, sym.SRODATA, dataVarHeader, writeDataVars)
	ldr.SetAttrReachable(s, true)
	ldr.SetAttrLocal(s, true)
	ldr.SetSymAlign(s, 4)
	ctxt.dataVarsTab = s
	return s
}

// dataVarsSize gives runtime.datavars room for the symbols that dodata
// is to lay out, which are the ones of data by their kinds: dodata calls
// it when it has them, before it gives anything a place. The table is
// written when the addresses are known.
func (ctxt *Link) dataVarsSize(data *[sym.SFirstUnallocated][]loader.Sym) {
	tab := ctxt.dataVarsTab
	if tab == 0 {
		return
	}
	ldr := ctxt.loader
	// A symbol of the table has a byte after it, which is in no
	// symbol: dodata leaves it when it gives the symbol its place. So
	// the end of a variable, which a pointer of C's may be to and the
	// checks of arithmetic allow, is not the address of the variable
	// after it, as it is not for a variable of a frame or an object
	// of C's on the heap. (The counters of a coverage build are read
	// as one array by their own runtime, and stay as they are: they
	// are one variable in the table.)
	ctxt.dataVarGap = loader.MakeBitmap(ldr.NSym())
	ctxt.dataVarCov = loader.MakeBitmap(ldr.NSym())
	for kind, syms := range data {
		if !dataVarKind(sym.SymKind(kind)) {
			continue
		}
		for _, s := range syms {
			if s == tab || ldr.SymSize(s) <= 0 {
				continue
			}
			ctxt.dataVars = append(ctxt.dataVars, s)
			if sym.SymKind(kind) == sym.SCOVERAGE_COUNTER {
				ctxt.dataVarCov.Set(s)
			} else {
				ctxt.dataVarGap.Set(s)
			}
		}
	}
	ldr.MakeSymbolUpdater(tab).SetSize(dataVarHeader + dataVarEntry*int64(len(ctxt.dataVars)))
}

// dataVarsGap reports whether s is a symbol of the table that dodata is
// to leave a byte after.
func (ctxt *Link) dataVarsGap(s loader.Sym) bool {
	return int(s) < ctxt.dataVarGap.Len() && ctxt.dataVarGap.Has(s)
}

// writeDataVars writes runtime.datavars.
func writeDataVars(ctxt *Link, tab loader.Sym) {
	ldr := ctxt.loader
	sb := ldr.MakeSymbolUpdater(tab)
	room := (ldr.SymSize(tab) - dataVarHeader) / dataVarEntry

	var sects [dataVarSects]*sym.Section
	sectOf := func(name string) *sym.Section {
		if s := ldr.Lookup(name, 0); s != 0 {
			return ldr.SymSect(s)
		}
		return nil
	}
	sects[dataVarRodata] = sectOf("runtime.rodata")
	if s := ldr.Lookup("runtime.rodatarelro", 0); s != 0 && ldr.SymSect(s) != ldr.SymSect(ldr.Lookup("runtime.types", 0)) {
		sects[dataVarRelro] = ldr.SymSect(s)
	}
	sects[dataVarNoptrdata] = sectOf("runtime.noptrdata")
	sects[dataVarData] = sectOf("runtime.data")
	sects[dataVarBss] = sectOf("runtime.bss")
	sects[dataVarNoptrbss] = sectOf("runtime.noptrbss")
	var types *sym.Section
	var typesBase int64
	if s := ldr.Lookup("runtime.types", 0); s != 0 {
		types, typesBase = ldr.SymSect(s), ldr.SymValue(s)
	}

	type entry struct {
		sect int
		off  int64
		size int64
		typ  uint32
	}
	var entries []entry
	// The counters of a coverage build are one variable: their
	// runtime reads them as one array, from runtime.covctrs to
	// runtime.ecovctrs.
	if ctxt.covCounterDataLen > 0 {
		entries = append(entries, entry{sect: dataVarNoptrbss, off: int64(ctxt.covCounterDataStartOff), size: int64(ctxt.covCounterDataLen), typ: dataVarNoType})
	}
	for _, s := range ctxt.dataVars {
		size := ldr.SymSize(s)
		sect := ldr.SymSect(s)
		if size <= 0 || sect == nil {
			continue
		}
		i := slices.Index(sects[:], sect)
		if i < 0 {
			continue
		}
		off := ldr.SymValue(s) - int64(sect.Vaddr)
		if off < 0 || off != int64(uint32(off)) {
			continue
		}
		if ctxt.dataVarCov.Has(s) {
			continue
		}
		e := entry{sect: i, off: off, size: size}
		// The symbol's type, if it has one whose descriptor is in
		// the module and that is as long as the symbol.
		if typ := ldr.SymGoType(s); typ != 0 {
			e.typ = dataVarNoType | dataVarUnknown
			if ldr.AttrReachable(typ) && types != nil && ldr.SymSect(typ) == types && len(ldr.Data(typ)) > 0 {
				if toff := ldr.SymValue(typ) - typesBase; toff > 0 && toff < dataVarUnknown && decodetypeSize(ctxt.Arch, ldr.Data(typ)) == size {
					e.typ = uint32(toff)
				}
			}
		} else {
			e.typ = dataVarNoType
		}
		entries = append(entries, e)
	}
	slices.SortFunc(entries, func(a, b entry) int {
		if c := cmp.Compare(a.sect, b.sect); c != 0 {
			return c
		}
		return cmp.Compare(a.off, b.off)
	})

	var start [dataVarSects + 1]uint32
	var ends [dataVarSects]uint32
	n := int64(0)
	sect, end := -1, int64(0)
	for _, e := range entries {
		if e.sect != sect {
			for sect < e.sect {
				sect++
				start[sect] = uint32(n)
			}
			end = 0
		}
		if e.off < end || n == room {
			// Not a symbol with a place of its own, or one that
			// was made after the table was given its size: the
			// linker's.
			continue
		}
		end = e.off + e.size
		ends[e.sect] = uint32(min(end, 1<<32-1))
		x := e.typ
		if x&dataVarNoType != 0 {
			x |= uint32(min(e.size, dataVarMaxSize))
		}
		sb.SetUint32(ctxt.Arch, dataVarHeader+n*dataVarEntry, uint32(e.off))
		sb.SetUint32(ctxt.Arch, dataVarHeader+n*dataVarEntry+4, x)
		n++
	}
	for sect < dataVarSects {
		sect++
		start[sect] = uint32(n)
	}
	for i, v := range start {
		sb.SetUint32(ctxt.Arch, int64(4*i), v)
	}
	for i, v := range ends {
		sb.SetUint32(ctxt.Arch, int64(4*(dataVarSects+1+i)), v)
	}
	// What is left of the table is in no section.
	for ; n < room; n++ {
		sb.SetUint32(ctxt.Arch, dataVarHeader+n*dataVarEntry, ^uint32(0))
		sb.SetUint32(ctxt.Arch, dataVarHeader+n*dataVarEntry+4, dataVarNoType)
	}
}
