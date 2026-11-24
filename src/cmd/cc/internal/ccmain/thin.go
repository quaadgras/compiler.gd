// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"fmt"
	"strconv"
	"strings"
)

// The 1:1 translation (cc -thin, doc/gd/cc.md): C's objects are Go
// variables of the types that mirror them, a C pointer is Go's (*T, or
// unsafe.Pointer for void * and for a pointer to a type that is not
// complete), and a load or a store is Go's. Nothing carries a
// capability: what is checked is what the compiler checks of
// unsafe.Pointer (doc/gd/unsafe.md), at the conversions and the
// arithmetic, which is where this generates unsafe.
//
// It is a mode of the generator, not another one: statements and the
// arithmetic of numbers are the same. An lvalue is an addressable Go
// expression (lval.x) instead of a c23.Ptr, the value of a struct or an
// array is still the pointer to it, and a struct is still passed by the
// pointer to it and returned in memory the caller gives.
//
// Not there yet, and an error: bit-fields, variable length arrays,
// thread-local objects, _Atomic, va_arg, long double and _Complex in
// memory.

// gt is the Go type of a C value of type t.
func (g *gen) gt(t *ctype) string {
	if !g.o.thin {
		return goType(t)
	}
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
	if !g.o.thin {
		return zeroOf(t)
	}
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

func (g *gen) unsupported(at pos, what string) {
	g.errorf(at, "%s: not in the 1:1 translation yet", what)
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
		return lval{addr: g.unsafePtr(taddr(l.x), ptrTo(l.t)), t: l.t}
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
	if g.o.thin && v != "" && (t.isPtr() || t.kind == kNullptr || t.kind == kStruct || t.kind == kArray) {
		return g.retype(v, "unsafe.Pointer", g.gt(t))
	}
	return v
}

// toOld is the reverse of fromOld.
func (g *gen) toOld(v string, t *ctype) string {
	if g.o.thin && (t.isPtr() || t.kind == kNullptr || t.kind == kStruct || t.kind == kArray) {
		return g.unsafePtr(v, t)
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
			if s.tls {
				// The calling thread's.
				return lval{x: fmt.Sprintf("(*(*%s)(%s.Get()))", g.mirror(s.t), g.globalName(s)), t: s.t}
			}
			return lval{x: g.tglobal(s), t: s.t}
		}
		if s.t.isVLA() {
			// The Go variable is the pointer to its array.
			return lval{x: fmt.Sprintf("(*(*%s)(%s))", g.mirror(s.t), g.localName(s)), t: s.t}
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
			return lval{addr: addOff(g.unsafePtr(taddr(base), ptrTo(xt)), e.f.off), t: e.t, f: e.f}
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
		// A union's member, or one of a record Go sees as bytes: at its
		// offset.
		return lval{x: fmt.Sprintf("(*(*%s)(unsafe.Add(unsafe.Pointer(%s), %d)))", g.mirror(e.t), taddr(base), e.f.off), t: e.t}
	case *compoundLit:
		return lval{x: g.tcompound(e), t: e.t}
	case *strLit:
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
		return fmt.Sprintf("(*(*%s)(%s()))", g.mirror(s.t), g.libcFunc(v))
	}
	def := s
	if d := g.ext[s.name]; s.linkage == 'e' && d != nil {
		def = d
	}
	if def.allocSize > def.t.sizeof() {
		return fmt.Sprintf("(*(*%s)(%s))", g.mirror(s.t), g.globalName(s))
	}
	return g.globalName(s)
}

// unsafePtr is the pointer p, of C type t, as an unsafe.Pointer.
func (g *gen) unsafePtr(p string, t *ctype) string {
	if g.gt(t) == "unsafe.Pointer" {
		return p
	}
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

func (g *gen) tload(l lval) string {
	switch {
	case l.t.kind == kStruct || l.t.kind == kArray:
		return taddr(l.x)
	case memValue(l.t):
		return loadCode(g.unsafePtr(taddr(l.x), ptrTo(l.t)), l.t)
	}
	return l.x
}

func (g *gen) tstore(l lval, v string, c expr) {
	switch {
	case l.t.kind == kStruct || l.t.kind == kArray:
		if v != taddr(l.x) {
			g.p("%s = *%s", l.x, v)
		}
		return
	case memValue(l.t):
		g.store(g.unsafePtr(taddr(l.x), ptrTo(l.t)), l.t, v, c)
		return
	}
	if k, ok := c.(*intConst); ok && !l.t.isPtr() && l.t.kind != kNullptr {
		v = constLit(l.t, k.v)
	}
	g.p("%s = %s", l.x, v)
}

// trv is e's value, if the 1:1 translation has its own for it.
func (g *gen) trv(e expr) (string, bool) {
	switch e := e.(type) {
	case *intConst:
		if e.t.isPtr() || e.t.kind == kNullptr {
			if e.v == 0 {
				return g.nilOf(e.t), true
			}
			return g.retype(fmt.Sprintf("c23.FromInt(%d)", uint64(e.v)), "unsafe.Pointer", g.gt(e.t)), true
		}
	case *strLit:
		return "&" + g.tstr(e), true
	case *varRef:
		if e.sym.kind == symFunc {
			return g.tfuncPtr(e.sym), true
		}
		return g.tload(g.tlv(e, false)), true
	case *deref, *member, *compoundLit:
		return g.tload(g.tlv(e, false)), true
	case *decay:
		if e.x.ty().kind == kFunc {
			if v, ok := e.x.(*varRef); ok {
				return g.tfuncPtr(v.sym), true
			}
			return g.rv(e.x), true
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
		return g.retype(fmt.Sprintf("c23.FromInt(uintptr(%s))", x), "unsafe.Pointer", g.gt(to))
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
	n := g.localName(s)
	if s.t.isVLA() {
		fmt.Fprintf(&g.locals, "var %s unsafe.Pointer\n_ = %s\n", n, n)
		return
	}
	fmt.Fprintf(&g.locals, "var %s %s\n_ = %s\n", n, g.mirror(s.t), n)
}

func (g *gen) tlocalInit(s *symbol) {
	n := g.localName(s)
	if s.t.isVLA() {
		g.p("%s = c23.NewArrayT(%s, uintptr(%s))", n, g.typeDesc(g.mirror(baseElem(s.t))), g.vlaSize(s.t))
		return
	}
	l := lval{x: n, t: s.t}
	switch {
	case s.init != nil && s.init.scalar != nil:
		g.tstore(l, g.rv(s.init.scalar), s.init.scalar)
	case s.init != nil:
		g.p("%s = %s{}", n, g.mirror(s.t))
		g.tinitObject(n, s.t, s.init)
	case !s.global && !s.t.isAggregate():
		// Without an initializer its value is indeterminate each time
		// the declaration is reached: zero (see localInit).
		g.p("%s = %s", n, g.zero(s.t))
	}
	if s.ctor != nil {
		g.effect(s.ctor)
	}
}

func (g *gen) tcompound(e *compoundLit) string {
	s := e.sym
	if s.global {
		if s.tls {
			return fmt.Sprintf("(*(*%s)(%s.Get()))", g.mirror(s.t), g.globalName(s))
		}
		return g.globalName(s)
	}
	n := g.localName(s)
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
	if in.scalar != nil {
		g.tstore(lval{x: x, t: t}, g.rv(in.scalar), in.scalar)
		return
	}
	for _, it := range in.items {
		switch {
		case it.f != nil && it.f.bits >= 0:
			g.bitStore(addOff("unsafe.Pointer("+taddr(x)+")", it.off), it.f, g.rv(it.x), it.x)
		case it.zero:
			g.p("%s = %s{}", g.tat(x, t, it.off, it.t), g.mirror(it.t))
		default:
			dst := g.tat(x, t, it.off, it.t)
			if s, ok := it.x.(*strLit); ok && it.t.kind == kArray {
				n := min(it.t.sizeof(), s.t.sizeof())
				g.p("c23.Memcpy1(unsafe.Pointer(%s), unsafe.Pointer(&%s), %d)", taddr(dst), g.tstr(s), n)
				continue
			}
			g.tstore(lval{x: dst, t: it.t}, g.rv(it.x), it.x)
		}
	}
}

func (g *gen) tglobalVar(s *symbol) {
	name := g.names[s]
	if _, ok := libcVars[s.name]; ok && s.storage == sExtern && !s.defined {
		return // the library's object: found where it is used (tglobal)
	}
	if s.t.kind == kArray && s.t.n < 0 && s.storage == sExtern && !s.defined {
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
	if s.tls {
		// Each thread's, made when it first uses it.
		fmt.Fprintf(&g.vars, "var %s = c23.NewTLS(func() c23.Ptr {\n", name)
		fmt.Fprintf(&g.vars, "p := new(%s)\n", g.mirror(s.t))
		if s.init != nil {
			save, saveInd := g.out, g.ind
			g.out = &g.vars
			g.tinitObject("(*p)", s.t, s.init)
			g.out, g.ind = save, saveInd
		}
		g.vars.WriteString("return unsafe.Pointer(p)\n})\n")
		return
	}
	x := name
	if s.allocSize > s.t.sizeof() {
		// With an initialized flexible array member: longer than its
		// type. The Go variable is the pointer to it.
		fmt.Fprintf(&g.vars, "var %s = c23.AllocRaw(%d)\n", name, s.allocSize)
		x = g.tglobal(s)
	} else {
		fmt.Fprintf(&g.vars, "var %s %s\n", name, g.mirror(s.t))
	}
	save, saveInd := g.out, g.ind
	g.out, g.ind = &g.inits, 1
	if s.init != nil {
		g.tinitObject(x, s.t, s.init)
	}
	if s.ctor != nil {
		g.effect(s.ctor)
	}
	g.out, g.ind = save, saveInd
}

// tstr is the Go variable of the string literal s: an array.
func (g *gen) tstr(s *strLit) string {
	key := s.t.elem.String() + "|" + s.data
	if n, ok := g.strs[key]; ok {
		return n
	}
	n := fmt.Sprintf("%ss_%d", g.o.prefix, len(g.strs))
	g.strs[key] = n
	esz := s.t.elem.sizeof()
	data := s.data + strings.Repeat("\x00", int(esz))
	fmt.Fprintf(&g.vars, "var %s = c23.Lit[[%d]%s](%s)\n", n, int64(len(data))/esz, g.mirror(s.t.elem), strconv.Quote(data))
	return n
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
	name := g.funcName(s, s.p)
	if name == "" {
		return "unsafe.Pointer(nil)" // weak, and not defined
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
	if !g.descs[d] {
		g.descs[d] = true
		fmt.Fprintf(&g.vars, "var %s unsafe.Pointer\n", d)
		fmt.Fprintf(&g.descInit, "%s = c23.NewFunc(%s)\n", d, f)
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
			g.p("c23.Memset1(%s, 0, %d)", g.unsafePtr(p, e.args[0].ty()), n)
		}
		return "", true
	case "cxx_copy":
		n := e.args[2].(*intConst).v
		dst, src := g.rv(e.args[0]), g.rv(e.args[1])
		m, ok := whole(e.args[0], n)
		if m2, ok2 := whole(e.args[1], n); ok && ok2 && m == m2 {
			g.p("%s = %s", tderef(dst), tderef(src))
		} else if n > 0 {
			g.p("c23.Memcpy1(%s, %s, %d)", g.unsafePtr(dst, e.args[0].ty()), g.unsafePtr(src, e.args[1].ty()), n)
		}
		return "", true
	case "cxx_size":
		return fmt.Sprintf("%s(c23.SizeOf1(%s))", goType(tULong), g.unsafePtr(g.rv(e.args[0]), e.args[0].ty())), true
	case "alloca":
		return fmt.Sprintf("c23.AllocRaw(uint64(%s))", g.rv(e.args[0])), true
	}
	return "", false
}

// et is the Go type of a C value of type t in a call through a pointer
// to a function: every pointer an unsafe.Pointer, so that functions
// whose types differ only in what their pointers point to, which C and
// C++ let a pointer call, have one Go type.
func (g *gen) et(t *ctype) string {
	switch t.kind {
	case kPtr, kNullptr, kStruct, kArray:
		return "unsafe.Pointer"
	}
	return goType(t)
}

// erasedSig is the Go type of a function of type ft as a pointer to a
// function calls it.
func (g *gen) erasedSig(ft *funcType) string {
	var ps []string
	if ft.ret.kind == kStruct {
		ps = append(ps, "unsafe.Pointer")
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

// erased is the Go function literal that calls the function name, of
// type ft, with erasedSig's type.
func (g *gen) erased(name string, ft *funcType) string {
	var ps, args []string
	if ft.ret.kind == kStruct {
		ps = append(ps, "sret unsafe.Pointer")
		args = append(args, g.retype("sret", "unsafe.Pointer", g.gt(ft.ret)))
	}
	for i, pt := range ft.params {
		a := fmt.Sprintf("a%d", i)
		ps = append(ps, a+" "+g.et(pt))
		if g.et(pt) == "unsafe.Pointer" {
			a = g.retype(a, "unsafe.Pointer", g.gt(pt))
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
	case g.et(ft.ret) == "unsafe.Pointer":
		return fmt.Sprintf("func(%s) unsafe.Pointer { return %s }", strings.Join(ps, ", "), g.retype(call, g.gt(ft.ret), "unsafe.Pointer"))
	}
	return fmt.Sprintf("func(%s) %s { return %s }", strings.Join(ps, ", "), g.et(ft.ret), call)
}

// thinLibc are the library's functions that 1:1 code calls on Go's
// pointers, without a c23.Ptr made of each.
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
	var fn string
	direct := false
	callee := c.ft
	lib, isLib := g.libcCallee(c)
	thin := ""
	if isLib {
		thin = thinLibc[lib]
		if lib == "calloc" {
			return g.retype(fmt.Sprintf("c23.AllocRaw(uint64(%s) * uint64(%s))", g.rv(c.args[0]), g.rv(c.args[1])), "unsafe.Pointer", g.gt(c.ft.ret))
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
				v = g.unsafePtr(v, at)
			} else if i < len(callee.params) {
				v = g.tconv(v, a.ty(), callee.params[i])
			}
			args = append(args, v)
		}
		code := fmt.Sprintf("%s(%s)", thin, strings.Join(args, ", "))
		if c.ft.ret.isPtr() {
			return g.retype(code, "unsafe.Pointer", g.gt(c.ft.ret))
		}
		return code
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
			args = append(args, "unsafe.Pointer("+sret+")")
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
			v = fmt.Sprintf("c23.ByValue{P: c23.Copy(%s, %d)}", g.unsafePtr(v, at), at.sizeof())
		case libGo && isP:
			// The library's: any pointer.
			v = g.unsafePtr(v, at)
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
			v = g.unsafePtr(v, at) // in variable arguments: any pointer is one type
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
	case libGo && (rt.isPtr() || rt.kind == kNullptr):
		return g.retype(code, "unsafe.Pointer", g.gt(rt))
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
	fp := g.unsafePtr(g.rv(c.fn), c.fn.ty())
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
		args = append(args, "unsafe.Pointer("+sret+")")
	}
	for _, a := range c.args {
		v := g.rv(a)
		at := a.ty()
		if g.et(at) == "unsafe.Pointer" {
			v = g.unsafePtr(v, at)
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
	case rt.kind != kVoid && g.et(rt) == "unsafe.Pointer":
		return g.retype(code, "unsafe.Pointer", g.gt(rt))
	}
	return code
}

// tallocCall is malloc, calloc or realloc whose result is converted to
// the pointer type to: an allocation of to's elements, which the
// collector knows the pointers of. Memory for elements that have no
// pointers is not given their type: it may come to hold pointers (a
// header before what an allocator gives out), so it is memory in which
// any word may be one.
func (g *gen) tallocCall(c *call, to *ctype) (string, bool) {
	name, ok := g.libcCallee(c)
	if !ok {
		return "", false
	}
	e := to.elem
	switch e.kind {
	case kVoid, kChar, kSChar, kUChar, kFunc:
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
	if !hasPointers(e) || g.gt(to) != "*"+g.mirror(e) {
		return "", false
	}
	m := g.mirror(e)
	switch name {
	case "malloc":
		return fmt.Sprintf("c23.AllocOf[%s](uint64(%s))", m, g.rv(c.args[0])), true
	case "calloc":
		return fmt.Sprintf("c23.AllocOf[%s](uint64(%s) * uint64(%s))", m, g.rv(c.args[0]), g.rv(c.args[1])), true
	case "realloc":
		return fmt.Sprintf("c23.ReallocOf1[%s]((*%s)(%s), uint64(%s))", m, m, g.rv(c.args[0]), g.rv(c.args[1])), true
	}
	return "", false
}
