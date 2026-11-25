// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"unsafe"
)

// The table of the variables of a module's data, which the linker writes
// (cmd/link/internal/ld/datavars.go; stackvar.go says what it is for):
// the symbols that Go code can have a pointer to, which are its global
// variables, the bytes of its strings and its other read-only data.
//
// The table has a part for each section that such symbols are in. It
// begins with the index of the first entry of each part, the number of
// entries after them, and where the last symbol of each part ends. An
// entry is a symbol's offset from the start of its section, and either
// the offset of its type in the module's types, which says its size, or
// its size.

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

// A dataVarEntry is a symbol of the table.
type dataVarEntry struct {
	off uint32 // from the start of the section
	x   uint32 // the type, or with dataVarNoType the size
}

const (
	dataVarNoType  = 1 << 31 // x is a size: the symbol is bytes
	dataVarUnknown = 1 << 30 // with dataVarNoType: what the symbol holds is not known
	dataVarMaxSize = 1<<30 - 1
)

// dataVarOf returns the variable of the program's data that addr is in:
// where it begins, its size and its type. typ is nil for a variable
// that is bytes to the program, a string's and what has no Go type, and
// for one whose type is not kept, as those of the runtime's variables
// are not (dataVarAt says which). ok is false if addr is in no variable
// that the table of a module has.
func dataVarOf(addr uintptr) (base, size uintptr, typ *_type, ok bool) {
	base, size, typ, _, state := dataVarAt(addr)
	return base, size, typ, state == varFound
}

// dataVarAt is dataVarOf, and says more: state is varFound for a
// variable, varUnknown for an address in a part of the data that a table
// is of and in no variable of it, as one between two variables is, and
// varNone for any other address. typed reports whether the type of the
// variable is known: if it is not set, typ is nil and says nothing of
// what the variable holds, which is so of a variable whose type is in
// another module.
//
//go:nosplit
func dataVarAt(addr uintptr) (base, size uintptr, typ *_type, typed bool, state int) {
	if addr == uintptr(unsafe.Pointer(&zerobase)) {
		// What has no size is allocated there: it is a pointer to
		// nothing, of any type, and not to that variable.
		return 0, 0, nil, false, varNone
	}
	for datap := &firstmoduledata; datap != nil; datap = datap.next {
		if datap.datavars == 0 || datap.bad {
			continue
		}
		starts := (*[dataVarSects + 1]uint32)(unsafe.Pointer(datap.datavars))
		ends := (*[dataVarSects]uint32)(unsafe.Pointer(datap.datavars + unsafe.Sizeof(*starts)))
		entries := datap.datavars + unsafe.Sizeof(*starts) + unsafe.Sizeof(*ends)
		sects := [dataVarSects]uintptr{
			dataVarRodata:    datap.rodata,
			dataVarRelro:     datap.rodatarelro,
			dataVarNoptrdata: datap.noptrdata,
			dataVarData:      datap.data,
			dataVarBss:       datap.bss,
			dataVarNoptrbss:  datap.noptrbss,
		}
		for i, start := range sects {
			lo, hi := uintptr(starts[i]), uintptr(starts[i+1])
			if lo == hi || addr < start {
				continue
			}
			// The last symbol of the section that begins at addr
			// or before it.
			off := addr - start
			if off >= uintptr(ends[i]) || uintptr(dataVarEntryAt(entries, lo).off) > off {
				continue
			}
			end := hi
			for lo+1 < hi {
				m := lo + (hi-lo)/2
				if uintptr(dataVarEntryAt(entries, m).off) <= off {
					lo = m
				} else {
					hi = m
				}
			}
			e := dataVarEntryAt(entries, lo)
			var t *_type
			known := true
			var n uintptr
			if e.x&dataVarNoType == 0 {
				t = (*_type)(unsafe.Pointer(datap.types + uintptr(e.x)))
				n = t.Size_
			} else {
				known = e.x&dataVarUnknown == 0
				n = uintptr(e.x & dataVarMaxSize)
				if n == dataVarMaxSize {
					// A symbol too long to say: it ends where
					// the next one begins.
					n = uintptr(ends[i] - e.off)
					if lo+1 < end {
						n = uintptr(dataVarEntryAt(entries, lo+1).off - e.off)
					}
				}
			}
			if off-uintptr(e.off) < n {
				return start + uintptr(e.off), n, t, known, varFound
			}
			// Between two symbols of the section.
			return 0, 0, nil, false, varUnknown
		}
	}
	return 0, 0, nil, false, varNone
}

// dataVarEntryAt returns the i'th entry of the table whose entries are
// at entries.
//
//go:nosplit
func dataVarEntryAt(entries, i uintptr) *dataVarEntry {
	return (*dataVarEntry)(unsafe.Pointer(entries + i*unsafe.Sizeof(dataVarEntry{})))
}
