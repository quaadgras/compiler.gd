// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

import (
	"internal/abi"
	"internal/goarch"
	"unsafe"
)

// An interface value is four words (see doc/gd/fat-iface.md): the itab
// (or the type, for an empty interface), which is an address in linear
// memory; the data pointer; and two words of payload for a value kept
// in the interface itself. In an object they are an integer, a
// reference and two floats, which is what these mirror.

type eface struct {
	_type  uintptr
	data   unsafe.Pointer
	w0, w1 float64
}

type iface struct {
	tab    uintptr
	data   unsafe.Pointer
	w0, w1 float64
}

// noescape hides a pointer from escape analysis, so that what it
// points to stays on the stack. Here the stack is in linear memory and
// only holds what has no pointers, so this is also how to get the
// bytes of such a value: p must be a pointer to a variable without
// pointers.
//
//go:nosplit
func noescape(p unsafe.Pointer) unsafe.Pointer {
	x := uintptr(p)
	return unsafe.Pointer(x ^ 0)
}

// The itabs of the program: those the compiler emitted, and those
// built here for conversions it could not resolve. Never freed.
//
// TODO: a hash table.
var (
	itabs       []*itab
	itabsLoaded bool
)

func itabsinit() {
	itabsLoaded = true
	md := &firstmoduledata
	p := md.types + md.itaboffset
	end := p + md.itabsize
	for p < end {
		m := (*itab)(unsafe.Pointer(p))
		itabs = append(itabs, m)
		p += uintptr(m.Size())
	}
}

func getitab(inter *interfacetype, typ *_type, canfail bool) *itab {
	if len(inter.Methods) == 0 {
		throw("internal error - misuse of itab")
	}
	if !itabsLoaded {
		itabsinit()
	}
	if typ.TFlag&abi.TFlagUncommon == 0 {
		if canfail {
			return nil
		}
		itabFail(inter, typ, toRType(&inter.Type).nameOff(inter.Methods[0].Name).Name())
	}
	for _, m := range itabs {
		if m.Inter == inter && m.Type == typ {
			if m.Fun[0] != 0 {
				return m
			}
			if canfail {
				return nil
			}
			itabFail(inter, typ, itabInit(m, false))
		}
	}

	// Not found: build one. As in an itab the compiler emits, the
	// functions are followed by a word of escape mask for each.
	size := uintptr(abi.ITabSize(goarch.PtrSize, len(inter.Methods)))
	var m *itab
	if splitMode {
		// (With the pointers in it: itabs keeps it.)
		m = (*itab)(splitalloc(size, splitslots(size)))
	} else {
		m = (*itab)(unsafe.Pointer(linalloc(size)))
	}
	m.Inter = inter
	m.Type = typ
	m.Hash = 0
	missing := itabInit(m, true)
	itabs = append(itabs, m)
	if m.Fun[0] != 0 {
		return m
	}
	if canfail {
		return nil
	}
	itabFail(inter, typ, missing)
	return nil
}

func itabFail(inter *interfacetype, typ *_type, missing string) {
	panic(&TypeAssertionError{concrete: typ, asserted: &inter.Type, missingMethod: missing})
}

// A TypeAssertionError explains a failed type assertion.
type TypeAssertionError struct {
	_interface    *_type
	concrete      *_type
	asserted      *_type
	missingMethod string // one method needed by Interface, missing from Concrete
}

func (*TypeAssertionError) RuntimeError() {}

func (e *TypeAssertionError) Error() string {
	inter := "interface"
	if e._interface != nil {
		inter = toRType(e._interface).string()
	}
	as := toRType(e.asserted).string()
	if e.concrete == nil {
		return "interface conversion: " + inter + " is nil, not " + as
	}
	cs := toRType(e.concrete).string()
	if e.missingMethod == "" {
		msg := "interface conversion: " + inter + " is " + cs + ", not " + as
		if cs == as {
			msg += " (types from different scopes)"
		}
		return msg
	}
	return "interface conversion: " + cs + " is not " + as +
		": missing method " + e.missingMethod
}

// itabInit fills in the m.Fun array with all the code pointers for the
// m.Inter/m.Type pair. If the type does not implement the interface, it
// sets m.Fun[0] to 0 and returns the name of an interface function that
// is missing.
func itabInit(m *itab, firstTime bool) string {
	inter := m.Inter
	typ := m.Type
	if firstTime {
		switch {
		case typ.TFlag&abi.TFlagInlineIface != 0:
			m.Inline = abi.ITabInlineInline
		case typ.TFlag&abi.TFlagSpreadIface != 0:
			m.Inline = abi.ITabInlineSpread
		}
	}
	x := typ.Uncommon()

	// Both inter and typ have methods sorted by name, and interface
	// names are unique, so the methods can be iterated over in lock
	// step.
	ni := len(inter.Methods)
	nt := int(x.Mcount)
	xmhdr := unsafe.Slice((*abi.Method)(add(unsafe.Pointer(x), uintptr(x.Moff))), nt)
	// gd: the escape mask of each method, copied into a new itab
	// (abi.ITab), or every argument escaping for a type with none.
	escMasks := x.EscMasks()
	j := 0
	methods := unsafe.Slice(&m.Fun[0], ni)
	var fun0 uintptr
imethods:
	for k := 0; k < ni; k++ {
		i := &inter.Methods[k]
		itype := toRType(&inter.Type).typeOff(i.Typ)
		name := toRType(&inter.Type).nameOff(i.Name)
		iname := name.Name()
		ipkg := pkgPath(name)
		if ipkg == "" {
			ipkg = inter.PkgPath.Name()
		}
		for ; j < nt; j++ {
			t := &xmhdr[j]
			rtyp := toRType(typ)
			tname := rtyp.nameOff(t.Name)
			if rtyp.typeOff(t.Mtyp) == itype && tname.Name() == iname {
				pkgPath := pkgPath(tname)
				if pkgPath == "" {
					pkgPath = rtyp.nameOff(x.PkgPath).Name()
				}
				if tname.IsExported() || pkgPath == ipkg {
					ifn := rtyp.textOff(t.Ifn)
					if k == 0 {
						fun0 = ifn // we'll set m.Fun[0] at the end
					} else if firstTime {
						methods[k] = ifn
					}
					if firstTime {
						mask := abi.ITabEscAll
						if escMasks != nil {
							mask = escMasks[j]
						}
						*(*uint64)(add(unsafe.Pointer(m), uintptr(abi.ITabEscMaskOff(goarch.PtrSize, ni, k)))) = mask
					}
					continue imethods
				}
			}
		}
		// didn't find method
		return iname
	}
	if firstTime {
		m.Fun[0] = fun0
	}
	return ""
}

func assertE2I(inter *interfacetype, t *_type) *itab {
	if t == nil {
		// explicit conversions require non-nil interface value.
		panic(&TypeAssertionError{nil, nil, &inter.Type, ""})
	}
	return getitab(inter, t, false)
}

func assertE2I2(inter *interfacetype, t *_type) *itab {
	if t == nil {
		return nil
	}
	return getitab(inter, t, true)
}

// typeAssert builds an itab for the concrete type t and the interface
// type s.Inter. If the conversion is not possible it panics if
// s.CanFail is false and returns nil if s.CanFail is true.
//
// TODO: the cache.
func typeAssert(s *abi.TypeAssert, t *_type) *itab {
	if t == nil {
		if !s.CanFail {
			panic(&TypeAssertionError{nil, nil, &s.Inter.Type, ""})
		}
		return nil
	}
	return getitab(s.Inter, t, s.CanFail)
}

var emptyTypeAssertCache = abi.TypeAssertCache{Mask: 0}

// interfaceSwitch compares t against the list of cases in s. If t
// matches case i, interfaceSwitch returns the case index i and an itab
// for the pair <t, s.Cases[i]>. If there is no match, it returns N, nil,
// where N is the length of s.Cases.
//
// TODO: the cache.
func interfaceSwitch(s *abi.InterfaceSwitch, t *_type) (int, *itab) {
	cases := unsafe.Slice(&s.Cases[0], s.NCases)
	for i, c := range cases {
		if tab := getitab(c, t, true); tab != nil {
			return i, tab
		}
	}
	return len(cases), nil
}

var emptyInterfaceSwitchCache = abi.InterfaceSwitchCache{Mask: 0}

// panicdottypeE is called when doing an e.(T) conversion and the conversion fails.
// have = the dynamic type we have.
// want = the static type we're trying to convert to.
// iface = the static type we're converting from.
func panicdottypeE(have, want, iface *_type) {
	panic(&TypeAssertionError{iface, have, want, ""})
}

// panicdottypeI is called when doing an i.(T) conversion and the conversion fails.
// Same args as panicdottypeE, but "have" is the dynamic itab we have.
func panicdottypeI(have *itab, want, iface *_type) {
	var t *_type
	if have != nil {
		t = have.Type
	}
	panicdottypeE(t, want, iface)
}

// panicnildottype is called when doing an i.(T) conversion and the interface i is nil.
// want = the static type we're trying to convert to.
func panicnildottype(want *_type) {
	panic(&TypeAssertionError{nil, nil, want, ""})
}

func efaceOf(ep *any) *eface { return (*eface)(unsafe.Pointer(ep)) }

func unsafePointerOf(addr uintptr) unsafe.Pointer { return unsafe.Pointer(addr) }
