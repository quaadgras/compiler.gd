// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/runtime/atomic"
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
	base, size, typ, typed, state, _, _ = dataVarLookup(addr)
	return
}

// dataVarLookup is dataVarAt, and says which variable it found: the
// idx'th of the table of the mod'th module.
//
//go:nosplit
func dataVarLookup(addr uintptr) (base, size uintptr, typ *_type, typed bool, state int, mod int, idx uintptr) {
	if addr == uintptr(unsafe.Pointer(&zerobase)) {
		// What has no size is allocated there: it is a pointer to
		// nothing, of any type, and not to that variable.
		return 0, 0, nil, false, varNone, 0, 0
	}
	mod = -1
	for datap := &firstmoduledata; datap != nil; datap = datap.next {
		mod++
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
				if cShadowOn && known {
					cShadowVar(start+uintptr(e.off), n, t, i == dataVarRodata || i == dataVarRelro)
				}
				return start + uintptr(e.off), n, t, known, varFound, mod, lo
			}
			// Between two symbols of the section.
			return 0, 0, nil, false, varUnknown, 0, 0
		}
	}
	return 0, 0, nil, false, varNone, 0, 0
}

// Exposed variables.
//
// A variable of the data whose address the program made an integer of,
// and kept, is exposed as an object of the heap is (checkptr.go): an
// integer that is an address in it may be made a pointer again, and one
// that is an address in a variable that never gave its address away may
// not. dataExposed has a bit for each variable of the tables of the
// first modules; the variables of any others are taken to be exposed.
const dataExposedMods = 8

var dataExposed [dataExposedMods]unsafe.Pointer // of the bits of a module, or nil

// dataVarExpose marks the variable of the data that addr is in, if it
// is in one, as exposed.
//
//go:nosplit
func dataVarExpose(addr uintptr) {
	_, _, _, _, state, mod, idx := dataVarLookup(addr)
	if state != varFound || mod >= dataExposedMods {
		return
	}
	bits := (*gcBits)(atomic.Loadp(unsafe.Pointer(&dataExposed[mod])))
	if bits == nil {
		systemstack(func() {
			datap := &firstmoduledata
			for i := 0; i < mod; i++ {
				datap = datap.next
			}
			n := uintptr((*[dataVarSects + 1]uint32)(unsafe.Pointer(datap.datavars))[dataVarSects])
			b := persistentalloc(n/8+1, 1, &memstats.other_sys)
			// (If another was first, these are not used.)
			atomic.Casp1(&dataExposed[mod], nil, b)
		})
		bits = (*gcBits)(atomic.Loadp(unsafe.Pointer(&dataExposed[mod])))
	}
	if mask := uint8(1) << (idx % 8); atomic.Load8(bits.bytep(idx/8))&mask == 0 {
		atomic.Or8(bits.bytep(idx/8), mask)
	}
}

// dataVarExposed reports whether addr is in a variable of the data that
// is exposed.
func dataVarExposed(addr uintptr) bool {
	_, _, _, _, state, mod, idx := dataVarLookup(addr)
	if state != varFound {
		return false
	}
	if mod >= dataExposedMods {
		return true
	}
	bits := (*gcBits)(atomic.Loadp(unsafe.Pointer(&dataExposed[mod])))
	return bits != nil && atomic.Load8(bits.bytep(idx/8))&(uint8(1)<<(idx%8)) != 0
}

// dataVarEntryAt returns the i'th entry of the table whose entries are
// at entries.
//
//go:nosplit
func dataVarEntryAt(entries, i uintptr) *dataVarEntry {
	return (*dataVarEntry)(unsafe.Pointer(entries + i*unsafe.Sizeof(dataVarEntry{})))
}
