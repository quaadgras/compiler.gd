// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"fmt"
	"strings"
)

// C's types, as cmd/cc lays them out: LP64 (ILP32 on wasm32: target.go),
// little-endian, long double
// with x86-64's size and alignment (16) but a double's value
// (doc/gd/cc.md).

type kind uint8

const (
	kVoid kind = iota
	kBool
	kChar
	kSChar
	kUChar
	kShort
	kUShort
	kInt
	kUInt
	kLong
	kULong
	kLLong
	kULLong
	kInt128  // GCC's __int128
	kUInt128 // unsigned __int128
	kBitInt  // _BitInt(n)
	kUBitInt // unsigned _BitInt(n)
	kIntCap  // __intcap: intptr_t, an integer that carries a capability (CHERI C's)
	kUIntCap // unsigned __intcap: uintptr_t
	kFloat
	kDouble
	kLDouble
	kNullptr
	kComplex // elem: float, double or long double
	kPtr
	kArray
	kStruct // and union: rec.union
	kFunc
	kVaList // __builtin_va_list

	// C++, in a template's pattern only (cxxtmpl.go).
	kTParam    // a template parameter: n is its index
	kDependent // a type that depends on template parameters: dep
	kMemPtr    // a pointer to a member of type elem of the class mp.cls
)

type qual uint8

const (
	qConst qual = 1 << iota
	qVolatile
	qRestrict
	qAtomic
)

type ctype struct {
	kind kind
	q    qual
	elem *ctype // kPtr, kArray

	n   int64 // kArray: the length, or -1 if not known
	vla expr  // kArray: the length of a variable length array

	rec  *record   // kStruct
	fn   *funcType // kFunc
	enum *enumType // an integer kind that is an enumeration

	align int64 // _Alignas or aligned, if more than the type's own

	// C++ (cxx.go).
	ref     uint8     // kPtr: a reference (1: T &, 2: T &&)
	isAuto  bool      // the placeholder auto, until its initializer says
	isPackT bool      // kTParam: a parameter pack
	depN    int       // kArray, in a pattern: its length is the value parameter depN-1
	mp      *memPtr   // a pointer to data member (an integer: the offset); kMemPtr
	ctad    *template // isAuto: a class template's name, whose arguments the initializer gives
	dep     *depType  // kDependent
	chr     byte      // a character type that is its own type: 'w' wchar_t, 's' char16_t, 'i' char32_t

}

type record struct {
	tag      string
	union    bool
	fields   []*field
	complete bool
	size     int64
	align    int64
	packed   int64 // #pragma pack or packed: the maximum field alignment
	flex     bool  // a flexible array member last
	id       int   // unique in the translation unit
	goName   string
	tdName   string // C: the first typedef that names it, if it has no tag (its Go name)

	bytes bool // its mirror is bytes (gen.go's record)

	sc   *scope     // C++: the scope it is declared in
	cls  *classInfo // C++: a class's members that are not fields (cxxclass.go)
	mtag string     // C++: its name as mangled, if it is a template's instance
	inst *tinst     // C++: the template's instance it is
	mp   *memPtr    // C++: the pointer to member function it is (cxxmemptr.go)
}

type field struct {
	name   string // "" for an anonymous member or a padding bit-field
	t      *ctype
	off    int64
	bits   int // a bit-field's width; -1 if not a bit-field
	bitAt  int // a bit-field's first bit in its storage unit at off
	unit   *ctype
	packed bool  // __attribute__((packed)) on the member: alignment 1
	ubytes int64 // a packed bit-field's: the bytes it spans, from off

	mutable bool // C++: not const in a const object
	base    bool // C++: a base class's subobject
	vptr    bool // C++: the pointer to the class's table of virtual functions
}

type funcType struct {
	ret      *ctype
	params   []*ctype
	names    []string
	variadic bool
	proto    bool // has a prototype (not int f())
	noreturn bool
	psyms    []*symbol         // the parameters, as parsed: a definition's
	vlas     []*symbol         // the lengths of VLA types in the parameters
	tags     map[string]*ctype // the tags the parameters declare: a definition's body's too (C23 6.2.1)

	// C++.
	defaults []defArg    // the parameters' default arguments, if any has one
	packs    []paramPack // the parameter packs among the parameters
	recv     *ctype      // a non-static member function's: the type of this, its first parameter
	thisQ    qual        // the qualifiers after a member function's declarator: of *this
	noexcept bool
	ctor     bool
	dtor     bool
	conv     *ctype // a conversion function's type
}

type enumType struct {
	tag      string
	complete bool
	fixed    bool      // C23's enum E : type
	consts   []*symbol // the constants, once complete

	sc     *scope // C++: the scope it is declared in
	scoped bool   // enum class
	names  *scope // C++: its constants, for E::name
}

// sameEnum reports whether two enumerated types with the same tag and
// constants are compatible, as C23 lets them be (6.2.7).
func sameEnum(a, b *enumType) bool {
	if a.tag == "" || a.tag != b.tag || !a.complete || !b.complete || a.fixed != b.fixed || len(a.consts) != len(b.consts) {
		return false
	}
	for i, c := range a.consts {
		if d := b.consts[i]; c.name != d.name || c.val != d.val {
			return false
		}
	}
	return true
}

var (
	tVoid    = &ctype{kind: kVoid}
	tBool    = &ctype{kind: kBool}
	tChar    = &ctype{kind: kChar}
	tSChar   = &ctype{kind: kSChar}
	tUChar   = &ctype{kind: kUChar}
	tShort   = &ctype{kind: kShort}
	tUShort  = &ctype{kind: kUShort}
	tInt     = &ctype{kind: kInt}
	tUInt    = &ctype{kind: kUInt}
	tLong    = &ctype{kind: kLong}
	tULong   = &ctype{kind: kULong}
	tIntCap  = &ctype{kind: kIntCap}
	tUIntCap = &ctype{kind: kUIntCap}
	tLLong   = &ctype{kind: kLLong}
	tULLong  = &ctype{kind: kULLong}
	tFloat   = &ctype{kind: kFloat}
	tDouble  = &ctype{kind: kDouble}
	tLDouble = &ctype{kind: kLDouble}
	tNullptr = &ctype{kind: kNullptr}
	tVaList  = &ctype{kind: kVaList}
)

func ptrTo(t *ctype) *ctype { return &ctype{kind: kPtr, elem: t} }

func arrayOf(t *ctype, n int64) *ctype { return &ctype{kind: kArray, elem: t, n: n} }

func funcOf(ret *ctype, params ...*ctype) *ctype {
	return &ctype{kind: kFunc, fn: &funcType{ret: ret, params: params, proto: true}}
}

func (t *ctype) isInteger() bool { return t.kind >= kBool && t.kind <= kUIntCap }

// isCap reports whether t is an intcap (intcap.go in internal/c23): an
// integer whose value is a *c23.Void, keeping a pointer's capability.
func (t *ctype) isCap() bool { return t.kind == kIntCap || t.kind == kUIntCap }

// capInt is the integer type of an intcap's address.
func capInt(t *ctype) *ctype {
	if t.kind == kUIntCap {
		return tULong
	}
	return tLong
}
func (t *ctype) isBitInt() bool { return t.kind == kBitInt || t.kind == kUBitInt }

// width is an integer type's width in bits.
func (t *ctype) width() int64 {
	if t.isBitInt() {
		return t.n
	}
	if t.kind == kBool {
		return 1
	}
	return t.sizeof() * 8
}

// bitInt is _BitInt(n), unsigned if u.
func bitInt(n int64, u bool) *ctype {
	if u {
		return &ctype{kind: kUBitInt, n: n}
	}
	return &ctype{kind: kBitInt, n: n}
}
func (t *ctype) isFloat() bool { return t.kind >= kFloat && t.kind <= kLDouble }
func (t *ctype) isArith() bool { return t.isInteger() || t.isFloat() || t.kind == kComplex }
func (t *ctype) isReal() bool  { return t.isInteger() || t.isFloat() }

func complexOf(t *ctype) *ctype { return &ctype{kind: kComplex, elem: t} }
func (t *ctype) isPtr() bool    { return t.kind == kPtr }
func (t *ctype) isScalar() bool { return t.isArith() || t.isPtr() || t.kind == kNullptr }
func (t *ctype) isRecord() bool { return t.kind == kStruct }
func (t *ctype) isAggregate() bool {
	return t.kind == kArray || t.kind == kStruct
}
func (t *ctype) isVoid() bool { return t.kind == kVoid }
func (t *ctype) isFuncPtr() bool {
	return t.kind == kPtr && t.elem.kind == kFunc
}
func (t *ctype) isVLA() bool {
	for ; t.kind == kArray; t = t.elem {
		if t.vla != nil {
			return true
		}
	}
	return false
}

// isVM reports whether t is variably modified: a variable length array,
// or derived from one (a pointer to one).
func (t *ctype) isVM() bool {
	for ; t.kind == kArray || t.kind == kPtr; t = t.elem {
		if t.vla != nil {
			return true
		}
	}
	return false
}

func (t *ctype) isUnsigned() bool {
	switch t.kind {
	case kBool, kUChar, kUShort, kUInt, kULong, kULLong, kUBitInt, kUInt128, kUIntCap:
		return true
	}
	return false
}

func (t *ctype) isComplete() bool {
	switch t.kind {
	case kVoid:
		return false
	case kArray:
		return (t.n >= 0 || t.vla != nil) && t.elem.isComplete()
	case kStruct:
		return t.rec.complete
	case kFunc:
		return false
	}
	return true
}

// rank is the integer conversion rank: by width, a bit-precise type's
// below a standard type's of its width (C23 6.3.1.1).
func (t *ctype) rank() int {
	switch t.kind {
	case kBool:
		return 0
	case kChar, kSChar, kUChar:
		return 80
	case kShort, kUShort:
		return 160
	case kInt, kUInt:
		return 320
	case kLong, kULong, kIntCap, kUIntCap:
		return 640
	case kLLong, kULLong:
		return 641
	case kInt128, kUInt128:
		return 1280
	case kBitInt, kUBitInt:
		return int(t.n)*10 - 5
	}
	return -1
}

func (t *ctype) sizeof() int64 {
	switch t.kind {
	case kVoid, kBool, kChar, kSChar, kUChar:
		return 1
	case kShort, kUShort:
		return 2
	case kInt, kUInt, kFloat:
		return 4
	case kLong, kULong:
		return target.longSize
	case kPtr, kNullptr, kVaList, kIntCap, kUIntCap:
		return target.ptrSize
	case kLLong, kULLong, kDouble:
		return 8
	case kLDouble:
		// x86-64's layout, with a double's value (doc/gd/cc.md).
		return 16
	case kComplex:
		return 2 * t.elem.sizeof()
	case kInt128, kUInt128:
		return 16
	case kBitInt, kUBitInt:
		switch {
		case t.n <= 8:
			return 1
		case t.n <= 16:
			return 2
		case t.n <= 32:
			return 4
		}
		return 8
	case kArray:
		if t.n < 0 {
			return 0
		}
		return t.n * t.elem.sizeof()
	case kStruct:
		return t.rec.size
	case kFunc:
		return 1 // GNU: sizeof a function
	}
	return 0
}

// alignof is t's alignment, before _Alignas on a declaration.
func (t *ctype) alignof() int64 {
	a := int64(1)
	switch t.kind {
	case kArray:
		a = t.elem.alignof()
	case kStruct:
		a = t.rec.align
	case kComplex:
		a = t.elem.alignof()
	case kFunc, kVoid:
		a = 1
	default:
		a = t.sizeof()
	}
	return max(a, t.align)
}

func (t *ctype) qualified(q qual) *ctype {
	if q == 0 || t.q&q == q {
		return t
	}
	c := *t
	c.q |= q
	return &c
}

func (t *ctype) unqual() *ctype {
	if t.q == 0 {
		return t
	}
	c := *t
	c.q = 0
	return &c
}

func (t *ctype) unsignedOf() *ctype {
	if t.isBitInt() {
		return bitInt(t.n, true)
	}
	if t.kind == kInt128 {
		return &ctype{kind: kUInt128}
	}
	switch t.kind {
	case kChar, kSChar, kUChar:
		return tUChar
	case kShort, kUShort:
		return tUShort
	case kInt, kUInt:
		return tUInt
	case kLong, kULong:
		return tULong
	case kLLong, kULLong:
		return tULLong
	case kIntCap, kUIntCap:
		return tUIntCap
	}
	return t
}

// promote is the integer promotion of t.
func promote(t *ctype) *ctype {
	if t.chr != 0 {
		// wchar_t, char16_t, char32_t: as the integer type under it.
		c := *t
		c.chr, c.q = 0, 0
		t = &c
	}
	if t.isInteger() && !t.isBitInt() && t.rank() < 320 {
		return tInt
	}
	if t.enum != nil {
		// An enumeration promotes to its underlying type, no longer
		// the enumerated type.
		c := *t
		c.enum, c.q = nil, 0
		return &c
	}
	if t.isInteger() || t.isFloat() {
		return t.unqual()
	}
	return t
}

// usual is the common type of the usual arithmetic conversions.
func usual(a, b *ctype) *ctype {
	if a.kind == kComplex || b.kind == kComplex {
		ra, rb := a, b
		if a.kind == kComplex {
			ra = a.elem
		}
		if b.kind == kComplex {
			rb = b.elem
		}
		return complexOf(usual(ra, rb))
	}
	if a.isFloat() || b.isFloat() {
		if a.kind == kLDouble || b.kind == kLDouble {
			return tLDouble
		}
		if a.kind == kDouble || b.kind == kDouble {
			return tDouble
		}
		return tFloat
	}
	a, b = promote(a), promote(b)
	if c, o := a, b; c.isCap() || o.isCap() {
		// An intcap operand makes the result one, keeping its capability
		// (CHERI C's rule), unsigned as C's rules would make it; unless
		// the other is wider.
		if !c.isCap() {
			c, o = o, c
		}
		if o.width() <= c.width() {
			if c.kind == kUIntCap || o.kind == kUIntCap || o.isUnsigned() && o.width() == c.width() {
				return tUIntCap
			}
			return tIntCap
		}
		if a.isCap() {
			a = capInt(a)
		}
		if b.isCap() {
			b = capInt(b)
		}
	}
	if a.kind == b.kind && a.width() == b.width() {
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
	if s.width() > u.width() {
		return s
	}
	return s.unsignedOf()
}

// compatible reports whether a and b are compatible types (C23 6.2.7),
// ignoring top-level qualifiers if unq.
func compatible(a, b *ctype) bool { return compat(a, b, nil) }

// compat is compatible; seen holds the pairs of records being compared,
// for structs that point to themselves (assumed the same meanwhile).
func compat(a, b *ctype, seen map[[2]*record]bool) bool {
	if a == b {
		return true
	}
	if a.q != b.q {
		return false
	}
	if a.enum != nil && b.enum != nil && a.enum != b.enum && !sameEnum(a.enum, b.enum) {
		return false
	}
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case kBitInt, kUBitInt:
		return a.n == b.n
	case kComplex:
		return a.elem.kind == b.elem.kind
	case kPtr:
		return a.ref == b.ref && compat(a.elem, b.elem, seen)
	case kArray:
		if a.n >= 0 && b.n >= 0 && a.n != b.n {
			return false
		}
		return compat(a.elem, b.elem, seen)
	case kStruct:
		return a.rec == b.rec || sameRecord(a.rec, b.rec, seen)
	case kFunc:
		fa, fb := a.fn, b.fn
		if !compat(fa.ret, fb.ret, seen) {
			return false
		}
		if !fa.proto || !fb.proto {
			return true
		}
		if len(fa.params) != len(fb.params) || fa.variadic != fb.variadic {
			return false
		}
		for i := range fa.params {
			if !compat(fa.params[i].unqual(), fb.params[i].unqual(), seen) {
				return false
			}
		}
	}
	return true
}

// sameRecord reports whether two struct types with the same tag and
// members are compatible, as C23 lets them be.
func sameRecord(a, b *record, seen map[[2]*record]bool) bool {
	if a.tag == "" {
		return false
	}
	return sameMembers(a, b, seen)
}

func sameMembers(a, b *record, seen map[[2]*record]bool) bool {
	k := [2]*record{a, b}
	if seen[k] {
		return true
	}
	if seen == nil {
		seen = map[[2]*record]bool{}
	}
	seen[k] = true
	defer delete(seen, k)
	if a.tag != b.tag || a.union != b.union || !a.complete || !b.complete || len(a.fields) != len(b.fields) {
		return false
	}
	for i, f := range a.fields {
		g := b.fields[i]
		if f.name != g.name || f.bits != g.bits || f.off != g.off {
			return false
		}
		if f.name == "" && f.t.kind == kStruct && g.t.kind == kStruct && f.t.rec.tag == "" && g.t.rec.tag == "" {
			if !sameMembers(f.t.rec, g.t.rec, seen) {
				return false
			}
			continue
		}
		if !compat(f.t, g.t, seen) {
			return false
		}
	}
	return true
}

// composite is the composite type of compatible a and b.
func composite(a, b *ctype) *ctype {
	switch a.kind {
	case kPtr:
		if b.kind == kPtr {
			if e := composite(a.elem, b.elem); e != a.elem {
				c := *a
				c.elem = e
				return &c
			}
		}
	case kArray:
		if a.n < 0 && (b.n >= 0 || a.vla == nil && b.vla != nil) {
			c := *b
			c.elem = composite(a.elem, b.elem)
			return &c
		}
		if e := composite(a.elem, b.elem); e != a.elem {
			c := *a
			c.elem = e
			return &c
		}
	case kFunc:
		ft := *a.fn
		if !a.fn.proto && b.fn.proto {
			ft = *b.fn
		}
		ft.ret = composite(a.fn.ret, b.fn.ret)
		if a.fn.proto && b.fn.proto && len(a.fn.params) == len(b.fn.params) {
			ft.params = make([]*ctype, len(a.fn.params))
			for i := range ft.params {
				ft.params[i] = composite(a.fn.params[i], b.fn.params[i])
			}
		}
		c := *a
		c.fn = &ft
		return &c
	}
	return a
}

// lookupField finds a member, through anonymous members: the path of
// fields to it.
func (r *record) lookupField(name string) []*field {
	// A C++ class's own members hide its bases': the bases last.
	for _, bases := range []bool{false, true} {
		for _, f := range r.fields {
			if f.base != bases {
				continue
			}
			if f.name == name {
				return []*field{f}
			}
			if f.name == "" && f.t.kind == kStruct {
				if p := f.t.rec.lookupField(name); p != nil {
					return append([]*field{f}, p...)
				}
			}
		}
	}
	return nil
}

func (t *ctype) String() string {
	var q string
	if t.q&qConst != 0 {
		q += "const "
	}
	if t.q&qVolatile != 0 {
		q += "volatile "
	}
	switch t.kind {
	case kPtr:
		if t.elem.kind == kFunc {
			return t.elem.funcString("(*)")
		}
		if t.ref != 0 {
			return t.elem.String() + " " + "&&"[:t.ref]
		}
		s := t.elem.String() + " *"
		if q != "" {
			s += strings.TrimSpace(q)
		}
		return s
	case kArray:
		if t.n < 0 {
			return t.elem.String() + "[]"
		}
		return fmt.Sprintf("%s[%d]", t.elem, t.n)
	case kFunc:
		return t.funcString("")
	case kStruct:
		kw := "struct"
		if t.rec.union {
			kw = "union"
		}
		tag := t.rec.tag
		if tag == "" {
			tag = "<anonymous>"
		}
		return q + kw + " " + tag
	}
	if t.enum != nil {
		return q + "enum " + t.enum.tag
	}
	if t.kind == kComplex {
		return q + "_Complex " + t.elem.String()
	}
	if t.isBitInt() {
		u := ""
		if t.kind == kUBitInt {
			u = "unsigned "
		}
		return fmt.Sprintf("%s%s_BitInt(%d)", q, u, t.n)
	}
	switch t.kind {
	case kTParam:
		return q + "<template parameter " + itoa(t.n) + ">"
	case kDependent, kMemPtr:
		return q + "<dependent type>"
	}
	return q + kindNames[t.kind]
}

func (t *ctype) funcString(name string) string {
	var ps []string
	for _, p := range t.fn.params {
		ps = append(ps, p.String())
	}
	if t.fn.variadic {
		ps = append(ps, "...")
	}
	if len(ps) == 0 && t.fn.proto {
		ps = []string{"void"}
	}
	return fmt.Sprintf("%s %s(%s)", t.fn.ret, name, strings.Join(ps, ", "))
}

var kindNames = [...]string{
	kVoid: "void", kBool: "bool", kChar: "char", kSChar: "signed char", kUChar: "unsigned char",
	kShort: "short", kUShort: "unsigned short", kInt: "int", kUInt: "unsigned int",
	kLong: "long", kULong: "unsigned long", kLLong: "long long", kULLong: "unsigned long long",
	kFloat: "float", kDouble: "double", kLDouble: "long double", kNullptr: "nullptr_t",
	kVaList: "__builtin_va_list", kInt128: "__int128", kUInt128: "unsigned __int128",
	kIntCap: "__intcap", kUIntCap: "unsigned __intcap",
}

// wrap truncates v to t's width and signedness (the bits, for 64-bit
// unsigned types).
func wrap(t *ctype, v int64) int64 {
	switch t.kind {
	case kBool:
		if v != 0 {
			return 1
		}
		return 0
	case kChar, kSChar:
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
	case kLong, kULong:
		if t.sizeof() == 4 {
			return wrap(&ctype{kind: t.kind - kLong + kInt}, v)
		}
	case kBitInt:
		return v << (64 - t.n) >> (64 - t.n)
	case kUBitInt:
		if t.n < 64 {
			return v & (1<<t.n - 1)
		}
	}
	return v
}

// layout lays out a struct or union, as the SysV ABI does, bit-fields
// included.
func (r *record) layout() {
	var off, bit int64 // the next free bit is at off*8 + bit
	align := int64(1)
	size := int64(0)
	for _, f := range r.fields {
		fa := f.t.alignof()
		if r.packed > 0 {
			fa = min(fa, r.packed)
		}
		if f.packed {
			fa = 1
		}
		if f.t.align > 0 && r.packed == 0 {
			fa = max(fa, f.t.align)
		}
		if f.bits >= 0 {
			// A bit-field: in a unit of its type's size, if it fits.
			usz := f.t.sizeof()
			f.unit = f.t
			if r.union {
				f.off, f.bitAt = 0, 0
				size = max(size, (int64(f.bits)+7)/8)
				if f.name != "" {
					align = max(align, fa)
				}
				continue
			}
			pos := off*8 + bit
			if f.bits == 0 {
				// Ends the unit.
				pos = (pos + usz*8 - 1) / (usz * 8) * (usz * 8)
				off, bit = pos/8, pos%8
				continue
			}
			unitStart := pos / (usz * 8) * (usz * 8)
			if r.packed == 0 && pos+int64(f.bits) > unitStart+usz*8 {
				unitStart = (pos + usz*8 - 1) / (usz * 8) * (usz * 8)
				pos = unitStart
			} else if r.packed > 0 || f.packed {
				// Packed: from the byte it starts in, in as many bytes as
				// it needs, a power of two of them.
				unitStart = pos / 8 * 8
				need := (pos%8 + int64(f.bits) + 7) / 8
				f.ubytes = min(need, 8) // a 64-bit field across 9 bytes: not supported
			}
			f.off = unitStart / 8
			f.bitAt = int(pos - unitStart)
			pos += int64(f.bits)
			off, bit = pos/8, pos%8
			if f.name != "" {
				align = max(align, fa)
			}
			continue
		}
		if bit > 0 {
			off, bit = off+1, 0
		}
		if r.union {
			f.off = 0
			size = max(size, f.t.sizeof())
		} else {
			off = (off + fa - 1) / fa * fa
			f.off = off
			off += f.t.sizeof()
		}
		align = max(align, fa)
	}
	if !r.union {
		size = off
		if bit > 0 {
			size++
		}
	}
	r.align = align
	r.size = (size + align - 1) / align * align
	r.complete = true
}
