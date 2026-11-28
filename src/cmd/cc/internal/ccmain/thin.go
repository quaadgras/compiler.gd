// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// The translation is 1:1 (doc/gd/cc.md): C's objects are Go variables of
// the types that mirror them, a C pointer is Go's (*T, or *c23.Void for
// void *, for a pointer to a function and for one to a type that is not
// complete), and a load or a store is Go's. What is checked is what the
// compiler checks of unsafe.Pointer (doc/gd/unsafe.md), at the
// conversions and the arithmetic, which is where this generates unsafe.
//
// An lvalue is an addressable Go expression (lval.x). The value of a
// struct or an array is the pointer to it, and a struct is passed by the
// pointer to it and returned in memory the caller gives.
//
// What Go has no variable for is reached by its address instead (lval.addr,
// a *c23.Void, and internal/c23's loads and stores): a bit-field, an
// _Atomic object, a long double or a _Complex in memory, and what va_arg
// reads. toOld and fromOld convert a value between the two, where every
// pointer of the second is a *c23.Void.

// voidPtr is the Go type of a void *, and of any pointer where the
// library, or a call through a pointer to a function, does not say what
// it points to.
const voidPtr = "*c23.Void"

// vptr is the pointer p, of C type t, as a *c23.Void.
func (g *gen) vptr(p string, t *ctype) string { return g.retype(p, g.gt(t), voidPtr) }

// gt is the Go type of a C value of type t.
func (g *gen) gt(t *ctype) string {
	switch t.kind {
	case kStruct, kArray:
		return "*" + g.mirror(t)
	case kPtr, kNullptr:
		return g.mirror(t)
	}
	return goType(t)
}

// zero is the Go expression of t's zero value.
func (g *gen) zero(t *ctype) string {
	switch t.kind {
	case kPtr, kNullptr, kStruct, kArray:
		return g.nilOf(t)
	}
	return zeroOf(t)
}

// nilOf is a null pointer of t's Go type: typed, for where Go would not
// know nil's type.
func (g *gen) nilOf(t *ctype) string {
	return "(" + g.gt(t) + ")(nil)"
}

func isNil(x string) bool { return x == "nil" || strings.HasSuffix(x, ")(nil)") }

// taddr is the address of the addressable expression x.
func taddr(x string) string {
	if strings.HasPrefix(x, "(*") && strings.HasSuffix(x, ")") && balanced(x[2:len(x)-1]) {
		return x[2 : len(x)-1]
	}
	return "&" + x
}

// tderef is what the pointer p points to, as an addressable expression.
func tderef(p string) string {
	if strings.HasPrefix(p, "&") {
		return p[1:]
	}
	return "(*" + p + ")"
}

// star is what the pointer p points to, as a value: x for &x.
func star(p string) string {
	if strings.HasPrefix(p, "&") && balanced(p[1:]) && !strings.ContainsAny(p[1:], " ") {
		return p[1:]
	}
	return "*" + p
}

// balanced reports whether s's parentheses and brackets match, with
// none closed before it is opened.
func balanced(s string) bool {
	depth := 0
	for _, c := range s {
		switch c {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

// simpleLvalue reports whether x can be evaluated twice: names and
// fields of them.
func simpleLvalue(x string) bool {
	for _, c := range x {
		if !(c == '_' || c == '.' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// tlv is e, an lvalue: an addressable Go expression. For forAddr the
// expression's address is what is wanted, which may be one past an
// array's end: no index that Go checks is used.
func (g *gen) tlv(e expr, forAddr bool) lval {
	l := g.tlv1(e, forAddr)
	if l.x == "" {
		return l // at an unsafe.Pointer (a bit-field)
	}
	if l.t.q&qAtomic != 0 && !forAddr && (l.t.isScalar() || l.t.kind == kStruct) {
		// _Atomic: through its address, by internal/c23's functions.
		return lval{addr: g.vptr(taddr(l.x), ptrTo(l.t)), t: l.t}
	}
	l.addr = taddr(l.x)
	return l
}

// memValue reports whether a value of type t is another Go type in
// memory than in a Go variable (long double, _Complex, intptr_t): it is
// loaded and stored through its address.
func memValue(t *ctype) bool {
	return t.kind == kLDouble || t.kind == kComplex || t.isCap()
}

// fromOld is v, a value that the code shared with the older translation
// made, where every pointer is an unsafe.Pointer, as the 1:1
// translation has a value of type t.
func (g *gen) fromOld(v string, t *ctype) string {
	if v != "" && (t.isPtr() || t.kind == kNullptr || t.kind == kStruct || t.kind == kArray) {
		return g.retype(v, voidPtr, g.gt(t))
	}
	return v
}

// toOld is the reverse of fromOld.
func (g *gen) toOld(v string, t *ctype) string {
	if t.isPtr() || t.kind == kNullptr || t.kind == kStruct || t.kind == kArray {
		return g.vptr(v, t)
	}
	return v
}

// orv is e's value for the code shared with the older translation.
func (g *gen) orv(e expr) string { return g.toOld(g.rv(e), e.ty()) }

func (g *gen) tlv1(e expr, forAddr bool) lval {
	switch e := e.(type) {
	case *varRef:
		s := e.sym
		if s.kind != symVar {
			g.errorf(e.p, "%s is not an object", s.name)
		}
		if s.global {
			if s == g.tlsSelf {
				return lval{x: "(*p)", t: s.t}
			}
			if s.tls {
				// The calling thread's.
				return lval{x: fmt.Sprintf("(*(*%s)(unsafe.Pointer(%s.Get())))", g.mirror(s.t), g.globalName(s)), t: s.t}
			}
			return lval{x: g.tglobal(s), t: s.t}
		}
		if s.t.isVLA() || overAligned(s) {
			// The Go variable is the pointer to its object.
			return lval{x: g.held(g.localName(s), s.t), t: s.t}
		}
		return lval{x: g.localName(s), t: s.t}
	case *deref:
		if x, ok := g.tindex(e, forAddr); ok {
			return lval{x: x, t: e.t}
		}
		p := g.rv(e.x)
		if want := "*" + g.mirror(e.t); g.gt(e.x.ty()) != want {
			// A pointer whose Go type does not say what it points to.
			p = fmt.Sprintf("(%s)(%s)", want, g.unsafePtr(p, e.x.ty()))
		}
		return lval{x: tderef(p), t: e.t}
	case *member:
		xt := e.x.ty()
		g.mirror(xt) // its record's fields are named
		var base string
		if isLvalue(e.x) {
			base = g.tlv(e.x, forAddr).x
		} else {
			base = tderef(g.rv(e.x))
		}
		if e.f.bits >= 0 {
			// In a unit of its type's size at its offset: internal/c23's
			// loads and stores, through an unsafe.Pointer.
			return lval{addr: addOff(g.vptr(taddr(base), ptrTo(xt)), e.f.off), t: e.t, f: e.f}
		}
		if r := xt.rec; !r.bytes {
			for i, f := range r.fields {
				if f == e.f {
					if strings.HasPrefix(base, "(*") && strings.HasSuffix(base, ")") && balanced(base[2:len(base)-1]) && isSimple(base[2:len(base)-1]) {
						base = base[2 : len(base)-1] // p.f is (*p).f
					}
					return lval{x: base + "." + g.fieldName(f, i), t: e.t}
				}
			}
		}
		if xt.rec.uptr && hasPointers(e.t) {
			// A union's member that has a pointer: in the union's
			// interface value.
			l := lval{x: g.unionHeld(base, e.t), t: e.t}
			if unionPtrPun(xt.rec, e.f) && simpleLvalue(base) {
				l.ubase = base
			}
			return l
		}
		// A union's member, or one of a record Go sees as bytes: at its
		// offset.
		if e.f.off == 0 {
			l := lval{x: fmt.Sprintf("(*(*%s)(unsafe.Pointer(%s)))", g.mirror(e.t), taddr(base)), t: e.t}
			if unionPtrPun(xt.rec, e.f) && simpleLvalue(base) {
				l.ubase = base
			}
			return l
		}
		return lval{x: fmt.Sprintf("(*(*%s)(unsafe.Add(unsafe.Pointer(%s), %d)))", g.mirror(e.t), taddr(base), e.f.off), t: e.t}
	case *compoundLit:
		return lval{x: g.tcompound(e), t: e.t}
	case *strLit:
		if q, ok := cString(e); ok {
			// The Go constant itself, read-only as C's literal is.
			return lval{x: fmt.Sprintf("(*(*%s)(unsafe.Pointer(c23.String(%s))))", g.mirror(e.t), q), t: e.t}
		}
		return lval{x: g.tstr(e), t: e.t}
	case *retSlot:
		return lval{x: "(*sret)", t: e.t}
	}
	if k := e.ty().kind; k == kStruct || k == kArray {
		return lval{x: tderef(g.rv(e)), t: e.ty()}
	}
	g.errorf(e.at(), "internal error: %T is not an lvalue", e)
	return lval{}
}

// tindex is a[i], the dereference e of an array lvalue's element's
// address, as Go's index expression: checked by Go, with no unsafe.
func (g *gen) tindex(e *deref, forAddr bool) (string, bool) {
	if forAddr {
		return "", false
	}
	b, ok := e.x.(*binary)
	if !ok || b.op != "+" {
		return "", false
	}
	d, ok := b.x.(*decay)
	if !ok || !b.y.ty().isInteger() {
		return "", false
	}
	at := d.x.ty()
	if at.kind != kArray || at.n <= 0 || at.isVLA() || !isLvalue(d.x) || g.mirror(at.elem) != g.mirror(e.t) {
		return "", false
	}
	if _, isStr := d.x.(*strLit); isStr {
		return "", false
	}
	if m, ok := d.x.(*member); ok && shortStruct(m.x.ty()) && at.sizeof() >= shortMin {
		// The array that the struct may be allocated without all
		// of: left to the arithmetic, which is checked.
		return "", false
	}
	a := g.tlv(d.x, false).x
	if c, ok := b.y.(*intConst); ok {
		if c.v < 0 || c.v >= at.n {
			return "", false // not an element: left to the arithmetic
		}
		return fmt.Sprintf("%s[%d]", a, c.v), true
	}
	return fmt.Sprintf("%s[%s]", a, g.rv(b.y)), true
}

// tglobal is the Go variable of the object with linkage s.
func (g *gen) tglobal(s *symbol) string {
	if v, lib := libcVars[s.name]; lib && s.storage == sExtern && !s.defined {
		// The library's object, where the library keeps it.
		return fmt.Sprintf("(*(*%s)(unsafe.Pointer(%s())))", g.mirror(s.t), g.libcFunc(v))
	}
	def := s
	if d := g.ext[s.name]; s.linkage == 'e' && d != nil {
		def = d
	}
	if g.refs != nil {
		g.refs[def] = true
	}
	if def.allocSize > def.t.sizeof() || overAligned(def) {
		return g.held(g.globalName(s), s.t)
	}
	return g.globalName(s)
}

// held is the object of type t that the Go variable n is the pointer
// to: one that no Go variable can be.
func (g *gen) held(n string, t *ctype) string {
	return fmt.Sprintf("(*(*%s)(unsafe.Pointer(%s)))", g.mirror(t), n)
}

// overAligned reports whether the object s is aligned more than Go
// aligns a variable, as the program asked: it is allocated
// (c23.MallocAligned), and its Go variable is the pointer to it.
func overAligned(s *symbol) bool {
	return s.kind == symVar && s.t.align > 8 || s.t.alignof() > 16 || storageArray(s)
}

// storageArray reports whether the object s is an array of bytes that
// is declared with an alignment, of a pointer's or more: memory that the
// program keeps objects of other types in, as
//
//	alignas(T) unsigned char buf[sizeof(T) * 4];
//	T *p = new (buf) T;
//
// does. An array of bytes of Go's holds no pointer that the collector
// sees, so such an array is in C's memory, where a word is what it is
// claimed for (runtime/cclaim.go), and its Go variable is the pointer to
// it, as an over-aligned object's is.
func storageArray(s *symbol) bool {
	t := s.t
	if s.kind != symVar || t.kind != kArray || t.isVLA() || t.align < target.ptrSize {
		return false
	}
	for t.kind == kArray {
		t = t.elem
	}
	return t.isInteger() && t.sizeof() == 1
}

// allocAligned is the allocation of the over-aligned object s.
func allocAligned(s *symbol) string {
	return fmt.Sprintf("c23.MallocAligned(%d, %d)", s.t.alignof(), max(s.t.sizeof(), 1))
}

// unsafePtr is the pointer p, of C type t, as an unsafe.Pointer.
func (g *gen) unsafePtr(p string, t *ctype) string {
	if isNil(p) {
		return "unsafe.Pointer(nil)"
	}
	return "unsafe.Pointer(" + p + ")"
}

// tbind makes l's expression one that can be evaluated twice.
func (g *gen) tbind(l *lval) {
	if simpleLvalue(l.x) {
		return
	}
	t := g.tmp("*" + g.mirror(l.t))
	g.p("%s = %s", t, taddr(l.x))
	l.x = "(*" + t + ")"
	l.addr = t
}

// shortMin is the runtime's cShortMin: the size from which the array
// that a struct ends with may be one that the struct is allocated
// without all of.
const shortMin = 64 << 10

// shortStruct reports whether t is a struct that ends with such an
// array: a pointer to one may be to less than the type has. The array's
// elements are not indexed as Go's (tindex), and a value of the struct
// is whole where it is copied (wholeValue).
func shortStruct(t *ctype) bool {
	if t == nil || t.kind != kStruct || t.rec == nil || t.rec.union || len(t.rec.fields) == 0 {
		return false
	}
	f := t.rec.fields[len(t.rec.fields)-1]
	return f.t.kind == kArray && f.bits < 0 && f.t.n > 0 && f.t.sizeof() >= shortMin
}

// wholeValue is the check before the value of the lvalue l is copied:
// if it is a struct that may be allocated short, and no variable, all of
// it is in its object.
func (g *gen) wholeValue(l *lval) {
	if !shortStruct(l.t) || simpleLvalue(l.x) {
		return
	}
	g.tbind(l)
	g.p("c23.Whole(unsafe.Pointer(%s), %d)", taddr(l.x), l.t.sizeof())
}

func (g *gen) tload(l lval) string {
	switch {
	case l.t.kind == kStruct || l.t.kind == kArray:
		g.wholeValue(&l)
		return taddr(l.x)
	case memValue(l.t):
		return loadCode(g.vptr(taddr(l.x), ptrTo(l.t)), l.t)
	}
	return l.x
}

// unionPtrPun reports whether f, a member of the union r whose pointers
// are in an object of their own (types.go's unionPtr), is a pointer
// where another member is an integer of a pointer's size, or that
// integer:
//
//	union { uint64_t object; CallableCustom *custom; };
//
// C reads the pointer's address in the integer after it stored the
// pointer, and no pointer after it stored the integer (Godot's Callable
// is null if object is 0, and has a custom if custom is not null). The
// union's bytes hold the integer and its pointer holds the other. So a
// pointer that is stored in the member has its address stored in the
// bytes too (c23.UnionAddr): what the integer is read as, an integer
// there, which no pointer is made of. And when the integer is stored
// the union holds no pointer from then on.
func unionPtrPun(r *record, f *field) bool {
	if !r.uptr || !r.union || f.off != 0 || f.bits >= 0 || r.ubytes < target.ptrSize {
		return false
	}
	word := func(m *field) bool {
		return m.off == 0 && m.bits < 0 && m.t.isInteger() && m.t.kind != kBool && m.t.sizeof() == target.ptrSize
	}
	for _, m := range r.fields {
		if m != f && (f.t.isPtr() && word(m) || word(f) && m.off == 0 && m.t.isPtr()) {
			return true
		}
	}
	return false
}

func (g *gen) tstore(l lval, v string, c expr) {
	switch {
	case l.t.kind == kStruct || l.t.kind == kArray:
		if v != taddr(l.x) {
			g.wholeValue(&l)
			g.p("%s = %s", l.x, star(v))
			if hasUnionPtr(l.t) {
				g.tbind(&l)
				g.unionClones(l.x, l.t)
			}
		}
		return
	case memValue(l.t):
		g.store(g.vptr(taddr(l.x), ptrTo(l.t)), l.t, v, c)
		return
	}
	if k, ok := c.(*intConst); ok && !l.t.isPtr() && l.t.kind != kNullptr {
		v = constLit(l.t, k.v)
	}
	g.p("%s = %s", l.x, v)
	switch {
	case l.ubase != "" && l.t.isPtr():
		g.p("c23.UnionAddr(unsafe.Pointer(&%s), unsafe.Pointer(%s))", l.ubase, l.x)
	case l.ubase != "":
		g.p("%s.p = nil", l.ubase) // the union holds the integer now
	}
}

// trv is e's value, if the 1:1 translation has its own for it.
func (g *gen) trv(e expr) (string, bool) {
	switch e := e.(type) {
	case *intConst:
		if e.t.isPtr() || e.t.kind == kNullptr {
			if e.v == 0 {
				return g.nilOf(e.t), true
			}
			return g.retype(fmt.Sprintf("c23.FromInt(%d)", uint64(e.v)&(1<<(8*target.ptrSize)-1)), voidPtr, g.gt(e.t)), true
		}
	case *strLit:
		return taddr(g.tlv(e, true).x), true
	case *varRef:
		if e.sym.kind == symFunc {
			return g.tfuncPtr(e.sym), true
		}
		return g.load(g.tlv(e, false)), true
	case *deref, *member, *compoundLit:
		return g.load(g.tlv(e, false)), true
	case *decay:
		if e.x.ty().kind == kFunc {
			if v, ok := e.x.(*varRef); ok {
				return g.tfuncPtr(v.sym), true
			}
			return g.rv(e.x), true
		}
		if lit, ok := e.x.(*strLit); ok {
			if q, ok := cString(lit); ok {
				if g.refs != nil {
					// In an object's declaration (staticInit): the
					// address of an array, which is data, where a
					// call would be code that runs as the program
					// starts.
					return g.retype("&"+g.cstrVar(lit)+"[0]", "*c23.Char", g.gt(e.t)), true
				}
				return g.retype("c23.String("+q+")", "*c23.Char", g.gt(e.t)), true
			}
		}
		return g.tdecay(g.tlv(e.x, true), e.t), true
	case *addrOf:
		if v, ok := e.x.(*varRef); ok && v.sym.kind == symFunc {
			return g.tfuncPtr(v.sym), true
		}
		if isLvalue(e.x) {
			l := g.tlv(e.x, true)
			return g.retype(taddr(l.x), "*"+g.mirror(l.t), g.gt(e.t)), true
		}
		return g.rv(e.x), true // a struct value's memory
	case *call:
		return g.fenvCall(e, g.tcall(e)), true
	case *cast:
		return g.tcast(e), true
	case *labelAddr:
		return fmt.Sprintf("c23.LabelAddr(%d)", e.l.id), true
	case *vaOp:
		return g.fromOld(g.vaOp(e), e.ty()), true
	case *builtinCall:
		return g.fromOld(g.builtinCode(e), e.ty()), true
	}
	return "", false
}

// retype is the pointer p, of Go type from, as the Go type to.
func (g *gen) retype(p, from, to string) string {
	switch {
	case from == to:
		return p
	case isNil(p):
		return "(" + to + ")(nil)"
	case to == "unsafe.Pointer":
		return "unsafe.Pointer(" + p + ")"
	case from == "unsafe.Pointer":
		return "(" + to + ")(" + p + ")"
	}
	return "(" + to + ")(unsafe.Pointer(" + p + "))"
}

// tdecay is the pointer to the first element of the array l, of C type
// pt.
func (g *gen) tdecay(l lval, pt *ctype) string {
	want := g.gt(pt)
	if l.t.kind == kArray && l.t.n > 0 && !l.t.isVLA() && want == "*"+g.mirror(l.t.elem) {
		if strings.HasPrefix(l.x, "(*") && strings.HasSuffix(l.x, ")") && balanced(l.x[2:len(l.x)-1]) {
			return "&" + l.x[2:len(l.x)-1] + "[0]" // &p[0] is &(*p)[0]
		}
		return "&" + l.x + "[0]"
	}
	return g.retype(taddr(l.x), "*"+g.mirror(l.t), want)
}

// tconv converts the Go value x of C type from to C type to.
func (g *gen) tconv(x string, from, to *ctype) string {
	fromP := from.isPtr() || from.kind == kNullptr
	toP := to.isPtr() || to.kind == kNullptr
	if from.isCap() || to.isCap() || from.kind == kVaList || to.kind == kVaList {
		return g.fromOld(convertCode(g.toOld(x, from), from, to), to)
	}
	switch {
	case fromP && toP:
		return g.retype(x, g.gt(from), g.gt(to))
	case toP:
		if x == "0" || constConv.MatchString(x) && strings.HasSuffix(x, "(0)") {
			return g.nilOf(to)
		}
		// Through internal/c23: an integer that is no address (a
		// sentinel) is still a pointer C may compare.
		return g.retype(fmt.Sprintf("c23.IntPtr(uintptr(%s))", x), voidPtr, g.gt(to))
	case fromP && to.kind == kBool:
		return fmt.Sprintf("c23.B2U8(%s != nil)", x)
	case fromP && to.isInteger():
		return wrapBits(fmt.Sprintf("%s(uintptr(%s))", goType(to), g.unsafePtr(x, from)), to)
	case to.kind == kStruct || to.kind == kArray:
		return x
	}
	return convertCode(x, from, to)
}

func (g *gen) tcast(e *cast) string {
	to, from := e.t, e.x.ty()
	if to.kind == kVoid {
		g.effect(e.x)
		return ""
	}
	if to.isPtr() {
		if c, ok := e.x.(*call); ok {
			if code, ok := g.tallocCall(c, to); ok {
				return code
			}
		}
		if code, ok := g.unionCast(e.x, to); ok {
			return code
		}
	}
	x := g.rv(e.x)
	if s, ok := g.fenvOf(e); ok && !from.isPtr() && !to.isPtr() {
		return convertUnder(x, from, to, s)
	}
	return g.tconv(x, from, to)
}

// tptrAdd is p, of C type pt, plus or minus i elements.
func (g *gen) tptrAdd(p string, pt *ctype, i expr, neg bool) string {
	var off string
	size := int64(1)
	if !pt.elem.isVLA() {
		size = g.elemSize(pt)
	}
	if pt.elem.isVLA() {
		sign := ""
		if neg {
			sign = "-"
		}
		off = fmt.Sprintf("%sint(%s)*int(%s)", sign, g.rv(i), g.vlaSize(pt.elem))
	} else if c, ok := i.(*intConst); ok {
		n := c.v * size
		if neg {
			n = -n
		}
		if n == 0 {
			return p
		}
		off = strconv.FormatInt(n, 10)
	} else {
		sign := ""
		if neg {
			sign = "-"
		}
		off = fmt.Sprintf("%sint(%s)", sign, g.rv(i))
		if size != 1 {
			off += fmt.Sprintf("*%d", size)
		}
	}
	gt := g.gt(pt)
	return g.retype(fmt.Sprintf("unsafe.Add(%s, %s)", g.unsafePtr(p, pt), off), "unsafe.Pointer", gt)
}

// tptrSub is p - q in elements, as the integer type t.
func (g *gen) tptrSub(p, q string, pt, t *ctype) string {
	return fmt.Sprintf("%s((int64(uintptr(%s)) - int64(uintptr(%s))) / %d)", goType(t), g.unsafePtr(p, pt), g.unsafePtr(q, pt), max(g.elemSize(pt), 1))
}

// tcompare compares the pointers of e.
func (g *gen) tcompare(e *binary) string {
	x, y := g.rv(e.x), g.rv(e.y)
	xt, yt := e.x.ty(), e.y.ty()
	if !xt.isPtr() && xt.kind != kNullptr {
		x, xt = g.tconv(x, xt, yt), yt // an integer: a null pointer constant
	}
	if !yt.isPtr() && yt.kind != kNullptr {
		y, yt = g.tconv(y, yt, xt), xt
	}
	if e.op == "==" || e.op == "!=" {
		if isNil(y) {
			return fmt.Sprintf("(%s %s nil)", x, e.op)
		}
		if isNil(x) {
			return fmt.Sprintf("(nil %s %s)", e.op, y)
		}
		if g.gt(xt) == g.gt(yt) {
			return fmt.Sprintf("(%s %s %s)", x, e.op, y)
		}
		return fmt.Sprintf("(%s %s %s)", g.unsafePtr(x, xt), e.op, g.unsafePtr(y, yt))
	}
	return fmt.Sprintf("(uintptr(%s) %s uintptr(%s))", g.unsafePtr(x, xt), e.op, g.unsafePtr(y, yt))
}

// Objects.

func (g *gen) tdeclareLocal(s *symbol) {
	if s.nrvo {
		g.names[s] = "(*sret)"
		return
	}
	g.localName(s)
	g.pending = append(g.pending, s) // declared where C declares it, or at the top (funcLit)
}

// localType is the Go type of the variable of the local s.
func (g *gen) localType(s *symbol) string {
	if s.t.isVLA() || overAligned(s) {
		return voidPtr // the pointer to its object
	}
	return g.mirror(s.t)
}

// thoist declares the local s at the function's top.
func (g *gen) thoist(s *symbol) {
	n := g.localName(s)
	fmt.Fprintf(&g.locals, "var %s %s\n_ = %s\n", n, g.localType(s), n)
}

// declareHere reports whether the local s, whose declaration is being
// generated, is declared here by Go too, and takes it off the list of
// those that are not yet.
func (g *gen) declareHere(s *symbol) bool {
	if !g.inPlace || g.fn == nil {
		return false
	}
	if n := len(g.frames); n > 0 && g.frames[n-1].kind == frSwitch {
		return false // a later case, which Go gives a scope of its own, may name it
	}
	if s.inExpr || !slices.Contains(g.declSyms, s) {
		// Not a declaration's, or one inside an expression (the length
		// of a variably modified type), which may be in a Go block of
		// its own.
		return false
	}
	for i, p := range g.pending {
		if p == s {
			g.pending = append(g.pending[:i:i], g.pending[i+1:]...)
			g.inBody[g.localName(s)] = true
			return true
		}
	}
	return false
}

// declare declares the local named n, of the Go type t, here: with the
// value v if that is not "". The blank use is taken out again if
// something reads the variable (usedLocals).
func (g *gen) declare(n, t, v string) {
	inner, conv := wholeConv(v, t)
	switch {
	case v == "" || conv && (inner == "0" || inner == "nil"):
		g.p("var %s %s", n, t) // Go's zero
	case conv && numLit.MatchString(inner):
		g.p("var %s %s = %s", n, t, inner)
	case conv:
		g.p("%s := %s", n, v)
	default:
		g.p("var %s %s = %s", n, t, v)
	}
	g.p("_ = %s", n)
}

var numLit = regexp.MustCompile(`^-?[0-9][0-9a-fA-FxX_.+eEpP-]*$`)

// wholeConv reports whether the Go expression v is a conversion to the
// type t, T(x) or (*T)(x), and gives its x.
func wholeConv(v, t string) (string, bool) {
	rest, ok := strings.CutPrefix(v, t+"(")
	if !ok {
		rest, ok = strings.CutPrefix(v, "("+t+")(")
	}
	if !ok || !strings.HasSuffix(rest, ")") {
		return "", false
	}
	rest = rest[:len(rest)-1]
	depth := 0
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth--; depth < 0 {
				return "", false // the conversion's parenthesis closes before the end
			}
		case '"', '\'', '`':
			return "", false // not looked into
		}
	}
	return rest, depth == 0
}

// names reports whether the Go expression x names the variable n.
func names(x, n string) bool {
	for i := 0; ; {
		j := strings.Index(x[i:], n)
		if j < 0 {
			return false
		}
		i += j
		before := i == 0 || !identByte(x[i-1]) && x[i-1] != '.'
		after := i+len(n) == len(x) || !identByte(x[i+len(n)])
		if before && after {
			return true
		}
		i += len(n)
	}
}

func identByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

func (g *gen) tlocalInit(s *symbol) {
	n := g.localName(s)
	here := !s.global && !s.nrvo && g.declareHere(s)
	if s.t.isVLA() {
		alloc := fmt.Sprintf("c23.NewArrayT(%s, uintptr(%s))", g.typeDesc(g.mirror(baseElem(s.t))), g.vlaSize(s.t))
		if a := s.t.alignof(); a > 8 {
			alloc = fmt.Sprintf("c23.MallocAligned(%d, uintptr(%s))", a, g.vlaSize(s.t))
		}
		if here {
			g.declare(n, voidPtr, alloc)
		} else {
			g.p("%s = %s", n, alloc)
		}
		return
	}
	l := lval{x: n, t: s.t}
	m := g.mirror(s.t)
	if overAligned(s) {
		// A new one each time the declaration is reached.
		if here {
			g.declare(n, voidPtr, allocAligned(s))
		} else {
			g.p("%s = %s", n, allocAligned(s))
		}
		l.x = g.held(n, s.t)
		if s.init != nil {
			g.tinitObject(l.x, s.t, s.init)
		}
		if s.ctor != nil {
			g.effect(s.ctor)
		}
		return
	}
	switch {
	case s.init != nil && s.init.scalar != nil:
		v := g.rv(s.init.scalar)
		// Go's declaration is not in scope in its own value, as C's is
		// (void *p = &p).
		if here && !s.t.isAggregate() && !memValue(s.t) && !names(v, n) {
			if k, ok := s.init.scalar.(*intConst); ok && !s.t.isPtr() && s.t.kind != kNullptr {
				v = constLit(s.t, k.v)
			}
			g.declare(n, m, v)
			break
		}
		if here {
			g.declare(n, m, "")
		}
		g.tstore(l, v, s.init.scalar)
	case s.init != nil:
		// As one Go literal, if its values need no statements of their
		// own and do not name the object itself.
		var lit string
		ok := false
		if pre := g.capture(func() { lit, ok = g.initLit(s.t, s.init) }); pre != "" || names(lit, n) {
			ok = false
		}
		switch {
		case ok && here && lit == m+"{}":
			g.declare(n, m, "")
		case ok && here:
			g.p("%s := %s", n, lit)
			g.p("_ = %s", n)
		case ok:
			g.p("%s = %s", n, lit)
		case here:
			g.declare(n, m, "") // zero, each time it is reached
			g.tinitObject(n, s.t, s.init)
		default:
			g.p("%s = %s{}", n, m)
			g.tinitObject(n, s.t, s.init)
		}
	case here:
		// Without an initializer its value is indeterminate each time
		// the declaration is reached: Go's zero.
		g.declare(n, m, "")
	case !s.global && !s.t.isAggregate():
		// Declared at the function's top (a goto may pass this): set
		// here, so that the Go compiler does not keep its value from
		// one pass of a loop to the next (see localInit).
		g.p("%s = %s", n, g.zero(s.t))
	}
	if s.ctor != nil {
		g.effect(s.ctor)
	}
}

func (g *gen) tcompound(e *compoundLit) string {
	s := e.sym
	if s.global && g.refs != nil {
		g.refs[s] = true
	}
	if s.global {
		if s.tls {
			return fmt.Sprintf("(*(*%s)(unsafe.Pointer(%s.Get())))", g.mirror(s.t), g.globalName(s))
		}
		return g.tglobal(s)
	}
	n := g.localName(s)
	if overAligned(s) {
		g.p("%s = %s", n, allocAligned(s))
		n = g.held(n, s.t)
		if s.init != nil {
			g.tinitObject(n, s.t, s.init)
		}
		return n
	}
	if s.init != nil {
		if s.init.scalar != nil {
			g.tstore(lval{x: n, t: s.t}, g.rv(s.init.scalar), s.init.scalar)
		} else {
			g.p("%s = %s{}", n, g.mirror(s.t))
			g.tinitObject(n, s.t, s.init)
		}
	}
	return n
}

// unionHeld is the member of type t, which has a pointer, of the union
// u, whose pointers are in an object of the heap (types.go's unionPtr):
// the object that the union's pointer points to (internal/c23's
// UnionBox).
func (g *gen) unionHeld(u string, t *ctype) string {
	return fmt.Sprintf("(*c23.UnionBox[%s](&%s.p))", g.mirror(t), u)
}

// unionCast is the cast to the pointer type to of the address of the
// bytes of a union that has an interface value for its pointers, if e
// is one and to points to a type with pointers: C uses a union's bytes
// for a value that is none of its members (Godot's Variant keeps a
// String, an Array, a Callable in an array of bytes), and a pointer in
// those bytes would be one that nothing sees. The value is where a
// member with pointers is: in an object of its own, which the union's
// pointer points to.
func (g *gen) unionCast(e expr, to *ctype) (string, bool) {
	t := to.elem
	if t == nil || !to.isPtr() || !t.isComplete() || t.kind == kVoid || t.kind == kFunc || !hasPointers(t) {
		return "", false
	}
	// Through casts to other pointer types, to the address itself.
	for {
		c, ok := e.(*cast)
		if !ok || !c.t.isPtr() || !c.x.ty().isPtr() {
			break
		}
		e = c.x
	}
	var of expr
	switch e := e.(type) {
	case *decay:
		of = e.x
	case *addrOf:
		of = e.x
	}
	var u string
	if pt := e.ty(); of == nil && pt.isPtr() && pt.elem != nil && pt.elem.kind == kStruct && pt.elem.rec.complete && pt.elem.rec.uptr {
		if t.kind == kStruct && t.rec == pt.elem.rec {
			return "", false // to the union's own type
		}
		// A pointer to the union, whatever it is the value of: C
		// lets it be converted to a pointer to a member.
		g.mirror(pt.elem)
		u = tderef(g.rv(e))
		return g.unionAs(u, t, to), true
	}
	if of == nil || !isLvalue(of) {
		return "", false
	}
	if m, ok := of.(*member); ok && m.f.bits < 0 && !hasPointers(m.t) {
		// A member with no pointer: the bytes.
		if xt := m.x.ty(); xt.kind != kStruct || !xt.rec.uptr || !isLvalue(m.x) {
			return "", false
		}
		g.mirror(m.x.ty())
		u = g.tlv(m.x, true).x
	} else if ot := of.ty(); ot.kind == kStruct && ot.rec.uptr {
		if t.kind == kStruct && t.rec == ot.rec {
			return "", false // to the union's own type
		}
		// The union itself.
		g.mirror(ot)
		u = g.tlv(of, true).x
	} else {
		return "", false
	}
	return g.unionAs(u, t, to), true
}

// unionAs is the pointer, of type to, to what the union u holds as a t,
// a type with pointers.
func (g *gen) unionAs(u string, t, to *ctype) string {
	return g.retype(fmt.Sprintf("c23.UnionBox[%s](&%s.p)", g.mirror(t), u), "*"+g.mirror(t), g.gt(to))
}

// unionClones gives the unions of x, an object of type t that was
// copied whole from another, boxes of their own for the members that
// are in one (internal/c23's UnionClone).
func (g *gen) unionClones(x string, t *ctype) {
	switch {
	case t.kind == kArray && t.n > 0 && !t.isVLA() && hasUnionPtr(t.elem):
		i := g.tmp("int")
		g.p("for %s = 0; %s < %d; %s++ {", i, i, t.n, i)
		g.unionClones(fmt.Sprintf("%s[%s]", x, i), t.elem)
		g.p("}")
	case t.kind == kStruct && t.rec.complete:
		g.mirror(t)
		if t.rec.uptr {
			g.p("c23.UnionClone(&%s.p)", x)
			return
		}
		if t.rec.bytes || t.rec.union {
			return
		}
		for i, f := range t.rec.fields {
			if f.bits < 0 && hasUnionPtr(f.t) {
				g.unionClones(x+"."+g.fieldName(f, i), f.t)
			}
		}
	}
}

// hasUnionPtr reports whether a value of type t has in it, where Go
// has a field or an element for it, a union that has an interface value
// for its pointers.
func hasUnionPtr(t *ctype) bool {
	switch {
	case t.kind == kArray:
		return t.n > 0 && hasUnionPtr(t.elem)
	case t.kind == kStruct && t.rec.complete:
		if t.rec.uptr {
			return true
		}
		if t.rec.union {
			return false
		}
		for _, f := range t.rec.fields {
			if f.bits < 0 && hasUnionPtr(f.t) {
				return true
			}
		}
	}
	return false
}

// tat is the object of type ft at the offset off of x, an object of type
// t: a path of fields and elements where Go has one, so that nothing is
// unsafe.
func (g *gen) tat(x string, t *ctype, off int64, ft *ctype) string {
	want := g.mirror(ft)
	orig, origOff := x, off
	for {
		if off == 0 && g.mirror(t) == want {
			return x
		}
		switch {
		case t.kind == kArray && t.n > 0 && !t.isVLA() && t.elem.sizeof() > 0:
			i := off / t.elem.sizeof()
			if i >= t.n {
				break
			}
			x, off, t = fmt.Sprintf("%s[%d]", x, i), off-i*t.elem.sizeof(), t.elem
			continue
		case t.kind == kStruct && t.rec.complete:
			g.mirror(t)
			if t.rec.uptr {
				// What an initializer gives a union with
				// pointers: its member that is being
				// initialized, if that has one.
				if f := g.initUnion[origOff-off]; f != nil && hasPointers(f.t) {
					x, t = g.unionHeld(x, f.t), f.t
					continue
				}
				if off == 0 && hasPointers(ft) {
					return g.unionHeld(x, ft)
				}
			}
			if t.rec.bytes || t.rec.union {
				break
			}
			found := false
			for i, f := range t.rec.fields {
				if f.bits < 0 && off >= f.off && off < f.off+max(f.t.sizeof(), 1) {
					x, off, t, found = x+"."+g.fieldName(f, i), off-f.off, f.t, true
					break
				}
			}
			if found {
				continue
			}
		}
		break
	}
	if origOff == 0 {
		return fmt.Sprintf("(*(*%s)(unsafe.Pointer(%s)))", want, taddr(orig))
	}
	return fmt.Sprintf("(*(*%s)(unsafe.Add(unsafe.Pointer(%s), %d)))", want, taddr(orig), origOff)
}

// tinitObject stores an initializer into the object x of type t, which
// is zero.
func (g *gen) tinitObject(x string, t *ctype, in *initializer) {
	defer func(m map[int64]*field) { g.initUnion = m }(g.initUnion)
	g.initUnion = in.unions
	if in.scalar != nil {
		g.tstore(lval{x: x, t: t}, g.rv(in.scalar), in.scalar)
		return
	}
	if g.fillFuncs(x, t, in) {
		return
	}
	for _, it := range in.items {
		switch {
		case it.f != nil && it.f.bits >= 0:
			g.bitStore(addOff("(*c23.Void)(unsafe.Pointer("+taddr(x)+"))", it.off), it.f, g.rv(it.x), it.x)
		case it.zero:
			g.p("%s = %s{}", g.tat(x, t, it.off, it.t), g.mirror(it.t))
		default:
			dst := g.tat(x, t, it.off, it.t)
			if s, ok := it.x.(*strLit); ok && it.t.kind == kArray {
				n := min(it.t.sizeof(), s.t.sizeof())
				src := taddr(g.tlv(s, true).x)
				if q, ok := cString(s); ok {
					src = "c23.String(" + q + ")"
				}
				g.p("c23.Memcpy1((*c23.Void)(unsafe.Pointer(%s)), (*c23.Void)(unsafe.Pointer(%s)), %d)", taddr(dst), src, n)
				continue
			}
			g.tstore(lval{x: dst, t: it.t}, g.rv(it.x), it.x)
		}
	}
}

var fdName = regexp.MustCompile(`^fd_[A-Za-z0-9_]+$`)

// fillFuncs initializes x, of type t, if it is an array of pointers to
// functions whose elements are all functions' addresses or null, as a
// table of virtual functions is: with one call, and a table of the
// descriptors' variables, where a statement for each element would be
// code. It reports whether it did.
func (g *gen) fillFuncs(x string, t *ctype, in *initializer) bool {
	if t.kind != kArray || t.elem.kind != kPtr || t.elem.elem.kind != kFunc || len(in.items) < 4 || int64(len(in.items)) != t.n {
		return false
	}
	w := t.elem.sizeof()
	var elems []string
	any := false
	for i, it := range in.items {
		if it.f != nil || it.zero || it.off != int64(i)*w {
			return false
		}
		v := "nil"
		if c, ok := it.x.(*intConst); !ok || c.v != 0 {
			v = g.rv(it.x)
			if !fdName.MatchString(v) {
				return false
			}
			v, any = "&"+v, true
		}
		elems = append(elems, v)
	}
	if !any {
		return false
	}
	g.fillN++
	tab := fmt.Sprintf("%sfill_%s%d", g.o.prefix, g.unitPre(), g.fillN)
	fmt.Fprintf(&g.vars, "var %s = [...]**c23.Void{%s}\n", tab, strings.Join(elems, ", "))
	g.p("c23.FillFuncs(unsafe.Pointer(%s), unsafe.Pointer(&%s), %d)", taddr(x), tab, len(elems))
	return true
}

func (g *gen) tglobalVar(s *symbol) {
	name := g.names[s]
	if _, ok := libcVars[s.name]; ok && s.storage == sExtern && !s.defined {
		return // the library's object: found where it is used (tglobal)
	}
	if s.t.kind == kArray && s.t.n < 0 && s.storage == sExtern && !s.defined {
		if g.o.unit != "" && s.t.elem.isComplete() {
			// Another unit's array, whose length this one does not
			// know: declared with one element, which the linker makes
			// the array that the other unit defines. (It is used
			// through its address only.)
			fmt.Fprintf(&g.vars, "var %s [1]%s\n", name, g.mirror(s.t.elem))
			if g.declared == nil {
				g.declared = map[*symbol]bool{}
			}
			g.declared[s] = true
		}
		return // declared, never defined: an error only if used
	}
	if s.t.kind == kArray && s.t.n < 0 {
		g.errorf(s.p, "array %q has no length", s.name)
	}
	if !s.t.isComplete() {
		if s.storage == sExtern && !s.defined {
			return
		}
		g.errorf(s.p, "storage size of %q isn't known", s.name)
	}
	shared := g.o.unit != "" && sharedVar(s)
	if s.tls {
		// Each thread's, made when it first uses it.
		w := &g.vars
		if g.o.unit != "" {
			// (A unit's variables have no initializers that run: unitInits.)
			fmt.Fprintf(&g.vars, "var %s *c23.TLS\n", name)
			w = &g.prelude
			fmt.Fprintf(w, "if %s == nil {\n%s = c23.NewTLS(func() *c23.Void {\n", name, name)
		} else {
			fmt.Fprintf(w, "var %s = c23.NewTLS(func() *c23.Void {\n", name)
		}
		fmt.Fprintf(w, "p := new(%s)\n", g.mirror(s.t))
		if s.init != nil || s.ctor != nil {
			// Its initializer runs for each thread, and names the
			// object being made.
			save, saveInd, saveSelf := g.out, g.ind, g.tlsSelf
			g.out, g.tlsSelf = w, s
			if s.init != nil {
				g.tinitObject("(*p)", s.t, s.init)
			}
			if s.ctor != nil {
				g.effect(s.ctor)
			}
			g.out, g.ind, g.tlsSelf = save, saveInd, saveSelf
		}
		w.WriteString("return (*c23.Void)(unsafe.Pointer(p))\n})\n")
		if g.o.unit != "" {
			w.WriteString("}\n" + initSplit)
		}
		return
	}
	if g.declared == nil {
		g.declared = map[*symbol]bool{}
	}
	x := name
	inDecl := false
	// Another's initializer may be in its declaration if it names this
	// one, whose value its own declaration gives: not if init does.
	defer func() { g.declared[s] = inDecl || s.init == nil && s.ctor == nil }()
	if s.allocSize > s.t.sizeof() {
		// With an initialized flexible array member: longer than its
		// type. The Go variable is the pointer to it.
		if g.o.unit != "" {
			fmt.Fprintf(&g.vars, "var %s *c23.Void\n", name)
			fmt.Fprintf(&g.prelude, "if %s == nil {\n%s = c23.AllocRaw(%d)\n}\n%s", name, name, s.allocSize, initSplit)
		} else {
			fmt.Fprintf(&g.vars, "var %s = c23.AllocRaw(%d)\n", name, s.allocSize)
		}
		x = g.tglobal(s)
	} else if overAligned(s) {
		if g.o.unit != "" {
			fmt.Fprintf(&g.vars, "var %s *c23.Void\n", name)
			fmt.Fprintf(&g.prelude, "if %s == nil {\n%s = %s\n}\n%s", name, name, allocAligned(s), initSplit)
		} else {
			fmt.Fprintf(&g.vars, "var %s = %s\n", name, allocAligned(s))
		}
		x = g.tglobal(s)
	} else if lit, ok := g.staticInitFor(s, shared); ok {
		// Initialized where Go declares it.
		if shared {
			// By every unit that has the object, the same: the
			// linker keeps one.
			g.vars.WriteString("//go:dupok\n")
		}
		if s.t.isAggregate() {
			fmt.Fprintf(&g.vars, "var %s = %s\n", name, lit)
		} else {
			fmt.Fprintf(&g.vars, "var %s %s = %s\n", name, g.mirror(s.t), lit)
		}
		inDecl = true
	} else {
		fmt.Fprintf(&g.vars, "var %s %s\n", name, g.mirror(s.t))
	}
	if x == name && hasUnionPtr(s.t) {
		// The runtime knows where the unions of the heap are, from
		// its types and claims: of a variable's it is told.
		if g.o.unit != "" {
			fmt.Fprintf(&g.prelude, "\tc23.UnionGlobal(&%s)\n%s", name, initSplit)
		} else {
			fmt.Fprintf(&g.vars, "var _ = c23.UnionGlobal(&%s)\n", name)
		}
	}
	if g.o.unit != "" && !s.defined && s.storage == sExtern {
		return // another unit's: declared
	}
	save, saveInd := g.out, g.ind
	g.out, g.ind = &g.inits, 1
	if s.init != nil && !inDecl {
		if g.o.unit != "" && !constInit(s.init) {
			// A value that code computes (int n = f();): with the
			// initializers that run, in the order of the declarations,
			// when every unit's objects have their constants.
			g.out = &g.dynInits
		}
		g.tinitObject(x, s.t, s.init)
		g.out = &g.inits
	}
	if s.ctor != nil {
		if g.o.unit != "" {
			// What runs code is run when every unit's objects are
			// made and have their constants (unitInits); and once, if
			// other units have the object too.
			g.out = &g.dynInits
			if shared {
				fmt.Fprintf(&g.vars, "var %s__once bool\n", name)
				g.p("if !%s__once {", name)
				g.p("%s__once = true", name)
			}
			g.effect(s.ctor)
			if shared {
				g.p("}")
			}
		} else {
			g.effect(s.ctor)
		}
	}
	g.out, g.ind = save, saveInd
	for _, b := range []*strings.Builder{&g.inits, &g.dynInits} {
		if n := b.Len(); n > 0 && !strings.HasSuffix(b.String()[max(n-len(initSplit), 0):], initSplit) {
			b.WriteString(initSplit) // the next object's is on its own
		}
	}
}

// constInit reports whether the initializer is of constants and
// addresses: what C++ initializes an object with before any code runs.
func constInit(in *initializer) bool {
	if in.scalar != nil {
		return staticInit(in.scalar)
	}
	for _, it := range in.items {
		if it.x != nil && !staticInit(it.x) {
			return false
		}
	}
	return true
}

// staticInitFor is staticInit. An object that several units define
// (shared) is declared with its value by each, //go:dupok.
func (g *gen) staticInitFor(s *symbol, shared bool) (string, bool) {
	lit, ok := g.staticInit(s)
	if ok && g.o.unit != "" {
		// What Go would compute as the package starts (a call, an
		// address that is not an element's or a field's): a unit's
		// variables are given such values by its own function.
		for _, call := range []string{"unsafe.Add(", "c23.String(", "c23.LabelAddr(", "c23.Addr(", "math."} {
			if strings.Contains(lit, call) {
				return "", false
			}
		}
	}
	return lit, ok
}

// cString is the string literal s as a Go string constant, if its
// characters are bytes: the compiler puts the 0 byte after it.
func cString(s *strLit) (string, bool) {
	if s.t.elem.sizeof() != 1 {
		return "", false
	}
	return strconv.Quote(s.data), true
}

// staticInit is the initializer of the object s as a Go expression for
// its declaration, if it can be one: a constant, or a composite literal
// of them and of addresses. It cannot where Go would see a cycle that C
// does not (an object whose initializer has its own address, or that of
// an object declared after it, which may have this one's): the
// expression may only name objects declared before, which makes the
// references of declarations go one way. A function's address is left
// to func init too: its descriptor is made there, and a function may
// use the object. So are a union's members, and what Go sees as bytes.
func (g *gen) staticInit(s *symbol) (string, bool) {
	if s.init == nil || s.tls {
		return "", false
	}
	g.refs, g.refFunc = map[*symbol]bool{}, false
	var lit string
	ok := false
	pre := g.capture(func() { lit, ok = g.initLit(s.t, s.init) })
	refs, refFunc := g.refs, g.refFunc
	g.refs, g.refFunc = nil, false
	if !ok || pre != "" || refFunc {
		return "", false
	}
	for r := range refs {
		if r == s || !g.declared[r] {
			return "", false
		}
	}
	return lit, true
}

// An initNode is an object's initializer as a tree: a value, or the
// members and elements that have one.
type initNode struct {
	t    *ctype
	leaf string
	kids map[int64]*initNode // by field index, or by element
}

// initLit is the initializer in of an object of type t as a Go
// expression.
func (g *gen) initLit(t *ctype, in *initializer) (string, bool) {
	if in.scalar != nil {
		if t.isAggregate() {
			return "", false
		}
		return g.initLeaf(t, in.scalar)
	}
	root := &initNode{t: t}
	for _, it := range in.items {
		switch {
		case it.zero:
			continue
		case it.f != nil && it.f.bits >= 0:
			return "", false
		}
		leaf, ok := g.initLeaf(it.t, it.x)
		if !ok {
			return "", false
		}
		// Down to the member or element at the item's offset.
		n, off := root, it.off
		for !(off == 0 && g.mirror(n.t) == g.mirror(it.t)) {
			var i int64
			var kt *ctype
			switch {
			case n.t.kind == kArray && n.t.n > 0 && !n.t.isVLA() && n.t.elem.sizeof() > 0:
				i = off / n.t.elem.sizeof()
				if i >= n.t.n {
					return "", false
				}
				kt, off = n.t.elem, off-i*n.t.elem.sizeof()
			case n.t.kind == kStruct && n.t.rec.complete:
				g.mirror(n.t)
				if n.t.rec.bytes || n.t.rec.union {
					return "", false
				}
				i = -1
				for k, f := range n.t.rec.fields {
					if f.bits < 0 && off >= f.off && off < f.off+max(f.t.sizeof(), 1) {
						i, kt, off = int64(k), f.t, off-f.off
						break
					}
				}
				if i < 0 {
					return "", false
				}
			default:
				return "", false
			}
			if n.kids == nil {
				n.kids = map[int64]*initNode{}
			}
			if n.kids[i] == nil {
				n.kids[i] = &initNode{t: kt}
			}
			n = n.kids[i]
		}
		n.leaf, n.kids = leaf, nil // a later initializer of the same object wins
	}
	return g.initNodeLit(root), true
}

// initLeaf is the value x that initializes an object of type t, as a Go
// expression of the object's type in memory.
func (g *gen) initLeaf(t *ctype, x expr) (string, bool) {
	if s, ok := x.(*strLit); ok && t.kind == kArray {
		if _, ok := cString(s); ok && t.n > 0 {
			return g.mirror(t) + "{" + arrayElems(s.data, 1, t.elem.isUnsigned()) + "}", true
		}
		return "", false
	}
	if t.isAggregate() || memValue(t) || t.q&qAtomic != 0 {
		return "", false
	}
	v := g.rv(x)
	// A constant has the type of what it initializes: 1, not int32(1).
	if m := constConv.FindStringSubmatch(v); m != nil && m[1] == goType(t) {
		v = m[2]
	} else if m := floatConv.FindStringSubmatch(v); m != nil && m[1] == goType(t) {
		v = m[2]
	}
	return v, true
}

var floatConv = regexp.MustCompile(`^(c23\.(?:Float|Double)|float(?:32|64))\(([-+0-9.e]+)\)$`)

func (g *gen) initNodeLit(n *initNode) string {
	if n.kids == nil {
		if n.leaf == "" {
			return g.mirror(n.t) + "{}"
		}
		return n.leaf
	}
	var keys []int64
	for k := range n.kids {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	var parts []string
	inOrder := true // elements 0, 1, 2...: no keys
	for i, k := range keys {
		inOrder = inOrder && k == int64(i)
	}
	for _, k := range keys {
		v := g.initNodeLit(n.kids[k])
		switch {
		case n.t.kind == kStruct:
			parts = append(parts, g.fieldName(n.t.rec.fields[k], int(k))+": "+v)
		case inOrder:
			parts = append(parts, v)
		default:
			parts = append(parts, fmt.Sprintf("%d: %s", k, v))
		}
	}
	return g.mirror(n.t) + "{" + strings.Join(parts, ", ") + "}"
}

// tstr is the Go variable of the string literal s, whose characters are
// wider than a byte: an array.
func (g *gen) tstr(s *strLit) string {
	key := s.t.elem.String() + "|" + s.data
	if n, ok := g.strs[key]; ok {
		return n
	}
	n := fmt.Sprintf("%ss_%s%d", g.o.prefix, g.unitPre(), len(g.strs))
	g.strs[key] = n
	esz := s.t.elem.sizeof()
	data := s.data + strings.Repeat("\x00", int(esz))
	fmt.Fprintf(&g.vars, "var %s = [%d]%s{%s}\n", n, int64(len(data))/esz, g.mirror(s.t.elem), arrayElems(data, esz, s.t.elem.isUnsigned()))
	return n
}

// cstrVar is the Go variable of the string literal s of bytes, for the
// declarations that have its address.
func (g *gen) cstrVar(s *strLit) string {
	key := "decl|" + s.data
	if n, ok := g.strs[key]; ok {
		return n
	}
	n := fmt.Sprintf("%ss_%s%d", g.o.prefix, g.unitPre(), len(g.strs))
	g.strs[key] = n
	fmt.Fprintf(&g.vars, "var %s = [%d]c23.Char{%s}\n", n, len(s.data)+1, arrayElems(s.data+"\x00", 1, false))
	return n
}

// arrayElems is the elements of a Go array literal whose elements, of
// size bytes each, are the characters of data: a letter where one is
// meant, and nothing for the zeros it ends with, which Go's array has.
// The array is data of the program then, as C's is. (A call that made
// it of a Go string, c23.Lit, was a statement of the package's
// initialization for each literal: ten thousand of them in Godot's
// core, in one function that took the compiler a minute and a half.)
func arrayElems(data string, size int64, unsigned bool) string {
	n := int64(len(data)) / size
	val := func(i int64) uint64 {
		var v uint64
		for k := int64(0); k < size; k++ {
			v |= uint64(data[i*size+k]) << (8 * k)
		}
		return v
	}
	for n > 0 && val(n-1) == 0 {
		n--
	}
	var b strings.Builder
	for i := int64(0); i < n; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		switch v := val(i); {
		case v >= ' ' && v < 0x7f && v != '\'' && v != '\\':
			fmt.Fprintf(&b, "'%c'", rune(v))
		case !unsigned && size < 8 && v>>(8*size-1) != 0:
			fmt.Fprintf(&b, "%d", int64(v)-1<<(8*size))
		default:
			fmt.Fprintf(&b, "%d", v)
		}
	}
	return b.String()
}

// Functions.

func (g *gen) tfuncSig(ft *funcType, names []string) string {
	var ps []string
	if ft.ret.kind == kStruct && names != nil {
		ps = append(ps, "sret "+g.gt(ft.ret))
	} else if ft.ret.kind == kStruct {
		ps = append(ps, g.gt(ft.ret))
	}
	for i, pt := range ft.params {
		if names != nil {
			ps = append(ps, names[i]+" "+g.gt(pt))
		} else {
			ps = append(ps, g.gt(pt))
		}
	}
	if ft.variadic || !ft.proto && len(ft.params) == 0 {
		if names != nil {
			ps = append(ps, "va ...any")
		} else {
			ps = append(ps, "...any")
		}
	}
	res := ""
	if ft.ret.kind != kVoid && ft.ret.kind != kStruct {
		res = " " + g.gt(ft.ret)
	}
	return fmt.Sprintf("func(%s)%s", strings.Join(ps, ", "), res)
}

// tfuncPtr is the pointer to the function s: to its descriptor, a
// c23.Func.
func (g *gen) tfuncPtr(s *symbol) string {
	g.refFunc = true
	name := g.funcName(s, s.p)
	if name == "" {
		return "(*c23.Void)(nil)" // weak, and not defined
	}
	f := name
	if isLibcGo(name) {
		// The library's: its pointers are unsafe.Pointer already.
		f = g.libcAdapter(name, s)
	} else {
		ft := s.t.fn
		if t := g.calleeType(s); t != nil {
			ft = t.fn
		}
		f = g.erased(name, ft)
	}
	if s.nested {
		return fmt.Sprintf("c23.NewFunc(%s)", f)
	}
	d := "fd_" + strings.ReplaceAll(name, ".", "_")
	// The function itself is what its descriptor holds, as a function
	// of the type that calls through pointers have, if the two types
	// differ only in what their pointers point to: no function is made
	// to stand between.
	direct := ""
	if !isLibcGo(name) {
		ft := s.t.fn
		if t := g.calleeType(s); t != nil {
			ft = t.fn
		}
		if sameCall(ft) {
			direct = name
		}
	}
	if !g.descs[d] {
		g.descs[d] = true
		fmt.Fprintf(&g.vars, "var %s *c23.Void\n", d)
		ft := s.t.fn
		if t := g.calleeType(s); t != nil {
			ft = t.fn
		}
		switch {
		case direct != "":
			// In the table of the functions of its Go type.
			g.fdEntry(g.funcSig(ft, nil), g.erasedSig(ft), name, d)
		case strings.HasPrefix(f, "func(") && g.o.unit != "":
			// The function that the descriptor calls it through is
			// one function, which every unit that makes the
			// descriptor defines: a function literal would be one
			// more function for each unit.
			fa := "fa_" + strings.TrimPrefix(d, "fd_")
			fmt.Fprintf(&g.funcs, "//go:dupok\nfunc %s%s\n\n", fa, strings.TrimPrefix(f, "func"))
			g.fdEntry(g.erasedSig(ft), g.erasedSig(ft), fa, d)
		case g.o.unit != "":
			fmt.Fprintf(&g.descInit, "if %s == nil {\n%s = c23.NewFunc(%s)\n}\n%s", d, d, f, initSplit)
		default:
			fmt.Fprintf(&g.descInit, "%s = c23.NewFunc(%s)\n%s", d, f, initSplit)
		}
	}
	return d
}

// tbuiltin is the builtin e, if the 1:1 translation has its own.
func (g *gen) tbuiltin(e *builtinCall) (string, bool) {
	// whole reports whether p, an argument, points to an object of n
	// bytes whose Go type is its pointer's: one Go's assignment copies.
	whole := func(p expr, n int64) (string, bool) {
		t := p.ty()
		if !t.isPtr() || !t.elem.isComplete() || t.elem.sizeof() != n || n == 0 || g.gt(t) != "*"+g.mirror(t.elem) {
			return "", false
		}
		return g.mirror(t.elem), true
	}
	switch e.name {
	case "cxx_zero":
		n := e.args[1].(*intConst).v
		p := g.rv(e.args[0])
		if m, ok := whole(e.args[0], n); ok {
			g.p("%s = %s{}", tderef(p), m)
		} else if n > 0 {
			g.p("c23.Memset1(%s, 0, %d)", g.vptr(p, e.args[0].ty()), n)
		}
		return "", true
	case "cxx_copy":
		n := e.args[2].(*intConst).v
		dst, src := g.rv(e.args[0]), g.rv(e.args[1])
		m, ok := whole(e.args[0], n)
		if m2, ok2 := whole(e.args[1], n); ok && ok2 && m == m2 {
			g.p("%s = %s", tderef(dst), tderef(src))
		} else if n > 0 {
			g.p("c23.Memcpy1(%s, %s, %d)", g.vptr(dst, e.args[0].ty()), g.vptr(src, e.args[1].ty()), n)
		}
		return "", true
	case "alloca":
		return fmt.Sprintf("c23.AllocRaw(uint64(%s))", g.rv(e.args[0])), true
	}
	return "", false
}

// et is the Go type of a C value of type t in a call through a pointer
// to a function: every pointer a *c23.Void, so that functions
// whose types differ only in what their pointers point to, which C and
// C++ let a pointer call, have one Go type.
func (g *gen) et(t *ctype) string {
	switch t.kind {
	case kPtr, kNullptr, kStruct, kArray:
		return voidPtr
	}
	return goType(t)
}

// erasedSig is the Go type of a function of type ft as a pointer to a
// function calls it.
func (g *gen) erasedSig(ft *funcType) string {
	var ps []string
	if ft.ret.kind == kStruct {
		ps = append(ps, voidPtr)
	}
	for _, pt := range ft.params {
		ps = append(ps, g.et(pt))
	}
	if ft.variadic || !ft.proto && len(ft.params) == 0 {
		ps = append(ps, "...any")
	}
	res := ""
	if ft.ret.kind != kVoid && ft.ret.kind != kStruct {
		res = " " + g.et(ft.ret)
	}
	return fmt.Sprintf("func(%s)%s", strings.Join(ps, ", "), res)
}

// sameCall reports whether a function of type ft is called as one of
// erasedSig's type is: its Go type has pointers where that has
// pointers, and nothing else of them differs. Not so if it takes a
// struct, which a call through a pointer passes the address of, or has
// no prototype.
func sameCall(ft *funcType) bool {
	if !ft.proto && len(ft.params) == 0 {
		return false
	}
	for _, pt := range ft.params {
		if pt.kind == kStruct || pt.kind == kArray {
			return false
		}
	}
	return true
}

// erased is the Go function literal that calls the function name, of
// type ft, with erasedSig's type.
func (g *gen) erased(name string, ft *funcType) string {
	var ps, args []string
	if ft.ret.kind == kStruct {
		ps = append(ps, "sret "+voidPtr)
		args = append(args, g.retype("sret", voidPtr, g.gt(ft.ret)))
	}
	for i, pt := range ft.params {
		a := fmt.Sprintf("a%d", i)
		ps = append(ps, a+" "+g.et(pt))
		if g.et(pt) == voidPtr {
			a = g.retype(a, voidPtr, g.gt(pt))
		}
		args = append(args, a)
	}
	if ft.variadic || !ft.proto && len(ft.params) == 0 {
		ps = append(ps, "va ...any")
		args = append(args, "va...")
	}
	call := fmt.Sprintf("%s(%s)", name, strings.Join(args, ", "))
	switch {
	case ft.ret.kind == kVoid || ft.ret.kind == kStruct:
		return fmt.Sprintf("func(%s) { %s }", strings.Join(ps, ", "), call)
	case g.et(ft.ret) == voidPtr:
		return fmt.Sprintf("func(%s) %s { return %s }", strings.Join(ps, ", "), voidPtr, g.retype(call, g.gt(ft.ret), voidPtr))
	}
	return fmt.Sprintf("func(%s) %s { return %s }", strings.Join(ps, ", "), g.et(ft.ret), call)
}

// thinLibc are the library's functions that 1:1 code calls on Go's
// pointers, without a *c23.Void made of each.
var thinLibc = map[string]string{
	"strlen": "c23.Strlen1", "strcmp": "c23.Strcmp1", "memcpy": "c23.Memcpy1", "memmove": "c23.Memcpy1",
	"memset": "c23.Memset1", "free": "c23.Free1", "malloc": "c23.AllocRaw", "realloc": "c23.ReallocRaw",
}

// libcCallee reports whether the call c is to a function of the
// library, by its name: the name.
func (g *gen) libcCallee(c *call) (string, bool) {
	d, ok := c.fn.(*decay)
	if !ok {
		return "", false
	}
	v, ok := d.x.(*varRef)
	if !ok || v.sym.kind != symFunc || v.sym.defined || g.ext[v.sym.name] != nil && g.ext[v.sym.name].defined {
		return "", false
	}
	if _, ok := libc[v.sym.name]; !ok {
		return "", false
	}
	return v.sym.name, true
}

func (g *gen) tcall(c *call) string {
	g.refFunc = true
	var fn string
	direct := false
	callee := c.ft
	lib, isLib := g.libcCallee(c)
	thin := ""
	if isLib {
		thin = thinLibc[lib]
		if lib == "calloc" {
			return g.retype(fmt.Sprintf("c23.AllocRaw(c23.MulSize(uint64(%s), uint64(%s)))", g.rv(c.args[0]), g.rv(c.args[1])), voidPtr, g.gt(c.ft.ret))
		}
	}
	if d, ok := c.fn.(*decay); ok {
		if v, ok := d.x.(*varRef); ok && v.sym.kind == symFunc {
			if thin == "" {
				fn = g.funcName(v.sym, c.p)
			}
			direct = true
			if t := g.calleeType(v.sym); t != nil {
				callee = t.fn
			}
		}
	}
	if thin != "" {
		// On Go's pointers: each as an unsafe.Pointer.
		var args []string
		for i, a := range c.args {
			v := g.rv(a)
			if at := a.ty(); at.isPtr() || at.kind == kNullptr {
				v = g.vptr(v, at)
			} else if i < len(callee.params) {
				// (The library's size_t has 64 bits everywhere:
				// libcConv.)
				v = libcConv(g.tconv(v, a.ty(), callee.params[i]), callee.params[i], true)
			}
			args = append(args, v)
		}
		code := fmt.Sprintf("%s(%s)", thin, strings.Join(args, ", "))
		if c.ft.ret.isPtr() {
			return g.retype(code, voidPtr, g.gt(c.ft.ret))
		}
		return libcConv(code, c.ft.ret, false)
	}
	if !direct || fn == "" {
		return g.tcallPtr(c)
	}
	libGo := direct && isLibcGo(fn)
	var args []string
	var sret string
	if into := g.u.callInto[c]; into != nil {
		// C++: the object the call constructs.
		sret = taddr(g.tlv(into, true).x)
		if !isSimple(sret) {
			t := g.tmp(g.gt(c.ft.ret))
			g.p("%s = %s", t, sret)
			sret = t
		}
	} else if c.ft.ret.kind == kStruct {
		t := g.tmp(g.mirror(c.ft.ret))
		sret = "&" + t
	}
	if sret != "" {
		if libGo {
			args = append(args, "(*c23.Void)(unsafe.Pointer("+sret+"))")
		} else {
			args = append(args, sret)
		}
	}
	for i, a := range c.args {
		v := g.rv(a)
		at := a.ty()
		isP := at.isPtr() || at.kind == kNullptr || at.kind == kStruct || at.kind == kArray
		switch {
		case at.kind == kStruct && i >= len(callee.params):
			// By value, in variable arguments: a copy.
			v = fmt.Sprintf("c23.ByValue{P: c23.Copy(%s, %d)}", g.vptr(v, at), at.sizeof())
		case libGo && isP && isLib && libcTyped[lib] && i < len(callee.params) && at.kind != kStruct:
			// The library's, with the type that C gives it.
			v = g.retype(v, g.gt(at), g.libcPtr(callee.params[i]))
		case libGo && isP:
			// The library's: any pointer.
			v = g.vptr(v, at)
		case libGo:
			if i < len(callee.params) {
				v = libcConv(abiConvert(v, a, callee.params[i]), callee.params[i], true)
			}
		case i < len(callee.params):
			pt := callee.params[i]
			if isP {
				v = g.retype(v, g.gt(at), g.gt(pt))
			} else if callee != c.ft {
				v = abiConvert(v, a, pt)
			}
		case !callee.variadic && (callee.proto || len(callee.params) > 0):
			continue // an extra argument: ignored
		case at.isPtr() || at.kind == kNullptr || at.kind == kArray:
			v = g.vptr(v, at) // in variable arguments: any pointer is one type
		}
		if !direct && !isSimple(v) && !isPure(v) {
			t := g.tmp(g.gt(at))
			g.p("%s = %s", t, v)
			v = t
		}
		args = append(args, v)
	}
	code := fmt.Sprintf("%s(%s)", fn, strings.Join(args, ", "))
	rt := c.ft.ret
	switch {
	case sret != "":
		g.p("%s", code)
		return sret
	case rt.kind == kVoid:
		return code
	case libGo && isLib && libcTyped[lib] && rt.isPtr():
		return g.retype(code, g.libcPtr(callee.ret), g.gt(rt))
	case libGo && (rt.isPtr() || rt.kind == kNullptr):
		return g.retype(code, voidPtr, g.gt(rt))
	case libGo:
		return libcConv(code, callee.ret, false)
	case rt.isPtr() || rt.kind == kNullptr:
		return g.retype(code, g.gt(callee.ret), g.gt(rt))
	case callee != c.ft && !compatible(callee.ret.unqual(), rt.unqual()):
		switch {
		case callee.ret.kind == kVoid:
			g.p("%s", code)
			return g.zero(rt)
		case rt.kind == kBool && callee.ret.isInteger():
			return fmt.Sprintf("uint8(%s)", code)
		case rt.isScalar() && callee.ret.isScalar():
			return convertCode(code, callee.ret, rt)
		}
	}
	return code
}

// tcallPtr is the call c through a pointer to a function: with every
// pointer an unsafe.Pointer (erasedSig).
func (g *gen) tcallPtr(c *call) string {
	fp := g.vptr(g.rv(c.fn), c.fn.ty())
	var args []string
	var sret string
	if into := g.u.callInto[c]; into != nil {
		sret = taddr(g.tlv(into, true).x)
		if !isSimple(sret) {
			t := g.tmp(g.gt(c.ft.ret))
			g.p("%s = %s", t, sret)
			sret = t
		}
	} else if c.ft.ret.kind == kStruct {
		sret = "&" + g.tmp(g.mirror(c.ft.ret))
	}
	if sret != "" {
		args = append(args, "(*c23.Void)(unsafe.Pointer("+sret+"))")
	}
	for _, a := range c.args {
		v := g.rv(a)
		at := a.ty()
		if g.et(at) == voidPtr {
			v = g.vptr(v, at)
		}
		if !isSimple(v) && !isPure(v) {
			// C evaluates the arguments before the call.
			t := g.tmp(g.et(at))
			g.p("%s = %s", t, v)
			v = t
		}
		args = append(args, v)
	}
	code := fmt.Sprintf("c23.FuncOf[%s](%s)(%s)", g.erasedSig(c.ft), fp, strings.Join(args, ", "))
	switch rt := c.ft.ret; {
	case sret != "":
		g.p("%s", code)
		return sret
	case rt.kind != kVoid && g.et(rt) == voidPtr:
		return g.retype(code, voidPtr, g.gt(rt))
	}
	return code
}

// tallocCall is malloc, calloc or realloc whose result is converted to
// the pointer type to: an allocation whose words begin as those of to's
// elements, pointers where the elements have them and none elsewhere
// (c23.AllocOf). The runtime follows what C stores there after.
func (g *gen) tallocCall(c *call, to *ctype) (string, bool) {
	name, ok := g.libcCallee(c)
	if !ok {
		return "", false
	}
	e := to.elem
	switch e.kind {
	case kVoid, kFunc:
		return "", false
	case kStruct:
		if !e.rec.complete {
			return "", false
		}
	case kArray:
		if e.isVLA() {
			return "", false
		}
	}
	// (A type without pointers too: the runtime then knows that the
	// words hold none, until C stores one.)
	if g.gt(to) != "*"+g.mirror(e) {
		return "", false
	}
	m := g.mirror(e)
	switch name {
	case "malloc":
		return fmt.Sprintf("c23.AllocOf[%s](uint64(%s))", m, g.rv(c.args[0])), true
	case "calloc":
		return fmt.Sprintf("c23.AllocOf[%s](c23.MulSize(uint64(%s), uint64(%s)))", m, g.rv(c.args[0]), g.rv(c.args[1])), true
	case "realloc":
		return fmt.Sprintf("c23.ReallocOf1[%s](%s, uint64(%s))", m, g.retype(g.rv(c.args[0]), g.gt(c.args[0].ty()), "*"+m), g.rv(c.args[1])), true
	}
	return "", false
}
