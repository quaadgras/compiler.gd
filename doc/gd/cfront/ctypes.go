package main

import (
	"fmt"
	"strings"
)

type kind int

const (
	kVoid kind = iota
	kChar
	kUChar
	kShort
	kUShort
	kInt
	kUInt
	kLong
	kULong
	kFloat
	kDouble
	kPtr
	kArray
	kStruct
	kFunc
)

type ctype struct {
	kind kind
	elem *ctype // kPtr, kArray
	n    int64  // kArray: length, -1 if not known

	// kStruct
	tag      string
	fields   []*field
	complete bool
	size     int64
	align    int64

	// kFunc
	ret        *ctype
	params     []*ctype
	paramNames []string
	variadic   bool
}

type field struct {
	name string
	t    *ctype
	off  int64
}

var (
	tVoid   = &ctype{kind: kVoid}
	tChar   = &ctype{kind: kChar}
	tUChar  = &ctype{kind: kUChar}
	tShort  = &ctype{kind: kShort}
	tUShort = &ctype{kind: kUShort}
	tInt    = &ctype{kind: kInt}
	tUInt   = &ctype{kind: kUInt}
	tLong   = &ctype{kind: kLong}
	tULong  = &ctype{kind: kULong}
	tFloat  = &ctype{kind: kFloat}
	tDouble = &ctype{kind: kDouble}
)

func ptrTo(t *ctype) *ctype { return &ctype{kind: kPtr, elem: t} }

func arrayOf(t *ctype, n int64) *ctype { return &ctype{kind: kArray, elem: t, n: n} }

func (t *ctype) isInteger() bool { return t.kind >= kChar && t.kind <= kULong }
func (t *ctype) isFloat() bool   { return t.kind == kFloat || t.kind == kDouble }
func (t *ctype) isArith() bool   { return t.isInteger() || t.isFloat() }
func (t *ctype) isPtr() bool     { return t.kind == kPtr }
func (t *ctype) isScalar() bool  { return t.isArith() || t.isPtr() }

func (t *ctype) isUnsigned() bool {
	switch t.kind {
	case kUChar, kUShort, kUInt, kULong:
		return true
	}
	return false
}

func (t *ctype) rank() int {
	switch t.kind {
	case kChar, kUChar:
		return 1
	case kShort, kUShort:
		return 2
	case kInt, kUInt:
		return 3
	case kLong, kULong:
		return 4
	}
	return 0
}

func (t *ctype) sizeof() int64 {
	switch t.kind {
	case kChar, kUChar:
		return 1
	case kShort, kUShort:
		return 2
	case kInt, kUInt, kFloat:
		return 4
	case kLong, kULong, kDouble, kPtr:
		return 8
	case kArray:
		if t.n < 0 {
			return 0
		}
		return t.n * t.elem.sizeof()
	case kStruct:
		return t.size
	}
	return 1 // void, for arithmetic on void*
}

func (t *ctype) alignof() int64 {
	switch t.kind {
	case kArray:
		return t.elem.alignof()
	case kStruct:
		return t.align
	}
	return max(t.sizeof(), 1)
}

// layout computes a struct's offsets as C (and Go) lay it out.
func (t *ctype) layout() {
	off, align := int64(0), int64(1)
	for _, f := range t.fields {
		a := f.t.alignof()
		off = (off + a - 1) / a * a
		f.off = off
		off += f.t.sizeof()
		align = max(align, a)
	}
	t.size = (off + align - 1) / align * align
	t.align = align
	t.complete = true
}

func (t *ctype) field(name string) *field {
	for _, f := range t.fields {
		if f.name == name {
			return f
		}
	}
	return nil
}

// unsignedOf returns the unsigned type of the same rank.
func (t *ctype) unsignedOf() *ctype {
	switch t.rank() {
	case 1:
		return tUChar
	case 2:
		return tUShort
	case 3:
		return tUInt
	}
	return tULong
}

func promote(t *ctype) *ctype {
	if t.isInteger() && t.rank() < 3 {
		return tInt
	}
	return t
}

// usual returns the type of the usual arithmetic conversions of a and b.
func usual(a, b *ctype) *ctype {
	if a.kind == kDouble || b.kind == kDouble {
		return tDouble
	}
	if a.kind == kFloat || b.kind == kFloat {
		return tFloat
	}
	a, b = promote(a), promote(b)
	if a.kind == b.kind {
		return a
	}
	if a.isUnsigned() == b.isUnsigned() {
		if a.rank() >= b.rank() {
			return a
		}
		return b
	}
	u, s := a, b
	if s.isUnsigned() {
		u, s = b, a
	}
	if u.rank() >= s.rank() {
		return u
	}
	if s.sizeof() > u.sizeof() {
		return s
	}
	return s.unsignedOf()
}

// goType is the Go type of a value of t in a Go variable.
func goType(t *ctype) string {
	switch t.kind {
	case kChar:
		return "int8"
	case kUChar:
		return "uint8"
	case kShort:
		return "int16"
	case kUShort:
		return "uint16"
	case kInt:
		return "int32"
	case kUInt:
		return "uint32"
	case kLong:
		return "int64"
	case kULong:
		return "uint64"
	case kFloat:
		return "float32"
	case kDouble:
		return "float64"
	case kPtr:
		return "ccap.Ptr"
	}
	panic(fmt.Sprintf("no Go type for %s", t))
}

// mirrorType is the Go type that has t's layout in memory, with pointers
// where t has pointers: what C's memory is allocated as, and what Go code
// sees it as.
func (g *gen) mirrorType(t *ctype) string {
	switch t.kind {
	case kPtr:
		if t.elem.kind == kVoid || t.elem.kind == kFunc {
			return "unsafe.Pointer"
		}
		return "*" + g.mirrorType(t.elem)
	case kArray:
		return fmt.Sprintf("[%d]%s", max(t.n, 0), g.mirrorType(t.elem))
	case kStruct:
		return g.structName(t)
	case kVoid:
		return "byte"
	}
	return goType(t)
}

func (t *ctype) String() string {
	switch t.kind {
	case kVoid:
		return "void"
	case kChar:
		return "char"
	case kUChar:
		return "unsigned char"
	case kShort:
		return "short"
	case kUShort:
		return "unsigned short"
	case kInt:
		return "int"
	case kUInt:
		return "unsigned int"
	case kLong:
		return "long"
	case kULong:
		return "unsigned long"
	case kFloat:
		return "float"
	case kDouble:
		return "double"
	case kPtr:
		return t.elem.String() + "*"
	case kArray:
		return fmt.Sprintf("%s[%d]", t.elem, t.n)
	case kStruct:
		return "struct " + t.tag
	case kFunc:
		var ps []string
		for _, p := range t.params {
			ps = append(ps, p.String())
		}
		return fmt.Sprintf("%s(%s)", t.ret, strings.Join(ps, ", "))
	}
	return "?"
}

// wrap truncates v to t's width and signedness (the bits, for ulong).
func wrap(t *ctype, v int64) int64 {
	switch t.kind {
	case kChar:
		return int64(int8(v))
	case kUChar:
		return int64(uint8(v))
	case kShort:
		return int64(int16(v))
	case kUShort:
		return int64(uint16(v))
	case kInt:
		return int64(int32(v))
	case kUInt:
		return int64(uint32(v))
	}
	return v
}

func constLit(t *ctype, v int64) string {
	if t.kind == kULong {
		return fmt.Sprintf("uint64(%d)", uint64(v))
	}
	return fmt.Sprintf("%s(%d)", goType(t), wrap(t, v))
}
