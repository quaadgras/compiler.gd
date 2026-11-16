// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

import (
	"internal/abi"
	"unsafe"
)

// Type descriptors and itabs are in linear memory, where the linker
// put them: a *_type is an address there.

type (
	_type         = abi.Type
	itab          = abi.ITab
	interfacetype = abi.InterfaceType
	name          = abi.Name
	nameOff       = abi.NameOff
	typeOff       = abi.TypeOff
	textOff       = abi.TextOff
)

// moduledata is the record the linker fills in: it must match what
// cmd/link/internal/ld/symtab.go writes. Only a few of the fields are
// used here.
type moduledata struct {
	pcHeader     *byte
	funcnametab  []byte
	cutab        []uint32
	filetab      []byte
	pctab        []byte
	pclntable    []byte
	ftab         []byte
	findfunctab  uintptr
	minpc, maxpc uintptr

	text, etext                uintptr
	noptrdata, enoptrdata      uintptr
	data, edata                uintptr
	bss, ebss                  uintptr
	noptrbss, enoptrbss        uintptr
	covctrs, ecovctrs          uintptr
	end, gcdata, gcbss         uintptr
	types, typedesclen, etypes uintptr
	itaboffset, itabsize       uintptr
	rodata                     uintptr
	gofunc                     uintptr
	epclntab                   uintptr

	textsectmap []byte
	ptab        []byte

	pluginpath string
	pkghashes  []byte

	inittasks []*byte

	modulename   string
	modulehashes []byte

	hasmain uint8
	bad     bool

	gcdatamask, gcbssmask struct {
		n        int32
		bytedata *uint8
	}

	typemap unsafe.Pointer

	next *moduledata
}

var firstmoduledata moduledata

type rtype struct {
	*abi.Type
}

func toRType(t *abi.Type) rtype { return rtype{t} }

func (t rtype) string() string {
	s := t.nameOff(t.Str).Name()
	if t.TFlag&abi.TFlagExtraStar != 0 {
		return s[1:]
	}
	return s
}

func (t rtype) nameOff(off nameOff) name {
	if off == 0 {
		return name{}
	}
	if off < 0 {
		return name{Bytes: (*byte)(reflectOffs[-1-off])}
	}
	return name{Bytes: (*byte)(unsafe.Pointer(firstmoduledata.types + uintptr(off)))}
}

func (t rtype) typeOff(off typeOff) *_type {
	if off == 0 || off == -1 {
		return nil
	}
	if off < 0 {
		return (*_type)(reflectOffs[-1-off])
	}
	return (*_type)(unsafe.Pointer(firstmoduledata.types + uintptr(off)))
}

// textOff returns the function at off: its index in the table, shifted
// as a program counter is on wasm.
func (t rtype) textOff(off textOff) uintptr {
	if off == -1 {
		return abi.FuncPCABIInternal(unreachableMethod)
	}
	return (firstmoduledata.text + uintptr(off)) << 16
}

func pkgPath(n name) string {
	if n.Bytes == nil || *n.Data(0)&(1<<2) == 0 {
		return ""
	}
	i, l := n.ReadVarint(1)
	off := 1 + i + l
	if *n.Data(0)&(1<<1) != 0 {
		i2, l2 := n.ReadVarint(off)
		off += i2 + l2
	}
	var nameOff nameOff
	for i := 0; i < 4; i++ {
		nameOff |= abi.NameOff(*n.Data(off + i)) << (8 * uint(i))
	}
	return toRType(nil).nameOff(nameOff).Name()
}
