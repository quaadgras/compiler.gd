// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Expressions. rv returns a Go expression of a C value (goType of its
// type; a struct's is the c23.Ptr to it), emitting first the statements
// it needs. Where C leaves the order unspecified, those statements may
// run before the operands they are beside.

// An lval is a C lvalue: a Go variable, or memory at a c23.Ptr.
type lval struct {
	goVar string
	addr  string
	t     *ctype
	f     *field // a bit-field
	// The Go expression of the value, if the object is known statically:
	// Go's own typed access, safe without c23's checks.
	direct string
}

func (g *gen) lv(e expr) lval {
	switch e := e.(type) {
	case *varRef:
		s := e.sym
		if s.kind != symVar {
			g.errorf(e.p, "%s is not an object", s.name)
		}
		if s.global {
			if s.tls {
				// The calling thread's.
				return lval{addr: g.globalName(s) + ".Get()", t: s.t}
			}
			def := s
			if d := g.ext[s.name]; s.linkage == 'e' && d != nil {
				def = d
			}
			return lval{addr: g.globalName(s), t: s.t, direct: g.objs[def]}
		}
		n := g.localName(s)
		if localKindOf(s) == lkVar {
			return lval{goVar: n, t: s.t}
		}
		return lval{addr: n, t: s.t, direct: g.objs[s]}
	case *deref:
		if l, ok := g.directElem(e); ok {
			return l
		}
		return lval{addr: g.rv(e.x), t: e.t}
	case *member:
		var base, direct string
		if isLvalue(e.x) {
			l := g.lv(e.x)
			if l.goVar != "" {
				g.errorf(e.p, "internal error: member of a register struct")
			}
			base = l.addr
			if l.direct != "" && e.f.bits < 0 {
				if r := e.x.ty().rec; !r.bytes && r.goName != "" {
					for i, f := range r.fields {
						if f == e.f {
							direct = l.direct + "." + g.fieldName(f, i)
						}
					}
				}
			}
		} else {
			base = g.rv(e.x)
		}
		l := lval{addr: addOff(base, e.f.off), t: e.t, direct: direct}
		if e.f.bits >= 0 {
			l.f = e.f
		}
		return l
	case *compoundLit:
		return lval{addr: g.compound(e), t: e.t}
	case *strLit:
		return lval{addr: g.str(e), t: e.t}
	}
	if e.ty().kind == kStruct {
		return lval{addr: g.rv(e), t: e.ty()}
	}
	g.errorf(e.at(), "internal error: %T is not an lvalue", e)
	return lval{}
}

func (g *gen) globalName(s *symbol) string {
	if def := g.ext[s.name]; s.linkage == 'e' && def != nil {
		s = def
	}
	n, ok := g.names[s]
	if !ok {
		g.errorf(s.p, "internal error: no name for %s", s.name)
	}
	if s.kind == symVar && !s.t.isComplete() {
		g.errorf(s.p, "undefined reference to %q", s.name)
	}
	if s.kind == symVar && s.storage == sExtern && !s.defined {
		g.errorf(s.p, "undefined reference to %q", s.name)
	}
	return n
}

// compound initializes a compound literal where it is evaluated: its
// object's c23.Ptr.
func (g *gen) compound(e *compoundLit) string {
	s := e.sym
	if s.global {
		if s.tls {
			return g.globalName(s) + ".Get()"
		}
		return g.globalName(s)
	}
	n := g.localName(s)
	if s.init != nil {
		if s.init.scalar == nil {
			g.p("c23.Memset(%s, 0, %d)", n, s.t.sizeof())
		}
		g.initObject(n, s.t, s.init)
	}
	return n
}

// bind makes l's address a temporary, so that it is evaluated once.
func (g *gen) bind(l *lval) {
	if l.goVar != "" || isSimple(l.addr) {
		return
	}
	t := g.tmp("c23.Ptr")
	g.p("%s = %s", t, l.addr)
	l.addr = t
}

func isSimple(s string) bool {
	for _, c := range s {
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func (g *gen) load(l lval) string {
	if l.goVar != "" {
		return l.goVar
	}
	if l.direct != "" && directScalar(l.t) && l.f == nil {
		return l.direct
	}
	if l.f != nil {
		return g.bitLoad(l.addr, l.f)
	}
	if isAtomic(l) {
		return atomicLoad(l.addr, l.t)
	}
	if isAtomicAgg(l) {
		// An _Atomic struct: copied whole, under its lock.
		t := g.cellTmp(l.t.unqual())
		g.p("c23.AtomicLoadAgg(%s, %s, %d)", t, l.addr, l.t.sizeof())
		return t
	}
	return loadCode(l.addr, l.t)
}

// isAtomicAgg reports whether l is an _Atomic struct in memory, which
// internal/c23 copies under a lock.
func isAtomicAgg(l lval) bool {
	return l.t.q&qAtomic != 0 && l.goVar == "" && l.f == nil && l.t.kind == kStruct
}

// isAtomic reports whether l is an _Atomic object in memory.
func isAtomic(l lval) bool {
	return l.t.q&qAtomic != 0 && l.goVar == "" && l.f == nil && l.t.isScalar()
}

func atomicLoad(addr string, t *ctype) string {
	if goType(t) == "c23.Ptr" {
		return fmt.Sprintf("c23.AtomicLoadP(%s)", addr)
	}
	return fmt.Sprintf("c23.AtomicLoad[%s](%s)", goType(t), addr)
}

func atomicCAS(addr string, t *ctype, old, new string) string {
	if goType(t) == "c23.Ptr" {
		return fmt.Sprintf("c23.AtomicCASP(%s, %s, %s)", addr, old, new)
	}
	return fmt.Sprintf("c23.AtomicCAS(%s, %s, %s)", addr, old, new)
}

// atomicUpdate replaces the _Atomic object l's value v with next(v),
// atomically: the old and new values' temporaries.
func (g *gen) atomicUpdate(l lval, next func(cur string) string) (string, string) {
	t := goType(l.t)
	old, new := g.tmp(t), g.tmp(t)
	g.p("for {")
	g.p("%s = %s", old, atomicLoad(l.addr, l.t))
	g.p("%s = %s", new, next(old))
	g.p("if %s { break }", atomicCAS(l.addr, l.t, old, new))
	g.p("}")
	return old, new
}

func loadCode(addr string, t *ctype) string {
	switch t.kind {
	case kBool, kUChar:
		return fmt.Sprintf("c23.Load8(%s)", addr)
	case kChar, kSChar:
		return fmt.Sprintf("int8(c23.Load8(%s))", addr)
	case kShort:
		return fmt.Sprintf("int16(c23.Load16(%s))", addr)
	case kUShort:
		return fmt.Sprintf("c23.Load16(%s)", addr)
	case kInt:
		return fmt.Sprintf("int32(c23.Load32(%s))", addr)
	case kUInt:
		return fmt.Sprintf("c23.Load32(%s)", addr)
	case kLong, kLLong:
		return fmt.Sprintf("%s(c23.Load%d(%s))", goType(t), t.sizeof()*8, addr)
	case kULong, kULLong:
		return fmt.Sprintf("c23.Load%d(%s)", t.sizeof()*8, addr)
	case kFloat:
		return fmt.Sprintf("c23.LoadF32(%s)", addr)
	case kDouble, kLDouble:
		return fmt.Sprintf("c23.LoadF64(%s)", addr)
	case kPtr, kNullptr, kVaList, kIntCap, kUIntCap:
		return fmt.Sprintf("c23.LoadPtr(%s)", addr)
	case kStruct, kArray:
		return addr
	case kInt128:
		return fmt.Sprintf("c23.LoadI128(%s)", addr)
	case kUInt128:
		return fmt.Sprintf("c23.LoadU128(%s)", addr)
	case kComplex:
		switch t.elem.kind {
		case kFloat:
			return fmt.Sprintf("c23.LoadC64(%s)", addr)
		case kLDouble:
			return fmt.Sprintf("c23.LoadCL(%s)", addr)
		}
		return fmt.Sprintf("c23.LoadC128(%s)", addr)
	}
	panic(fmt.Sprintf("load of %s", t))
}

// bitsOf is v, a Go expression of t's Go type, as an unsigned integer of
// n bits; c is v's node, if a constant.
func bitsOf(n int, t *ctype, v string, c expr) string {
	if k, ok := c.(*intConst); ok {
		return fmt.Sprintf("uint%d(%d)", n, uint64(k.v)&(^uint64(0)>>(64-n)))
	}
	return fmt.Sprintf("uint%d(%s)", n, v)
}

// store stores v, of type t, at addr; c is v's node, for constants.
func (g *gen) store(addr string, t *ctype, v string, c expr) {
	switch t.kind {
	case kBool, kChar, kSChar, kUChar:
		g.p("c23.Store8(%s, %s)", addr, bitsOf(8, t, v, c))
	case kShort, kUShort:
		g.p("c23.Store16(%s, %s)", addr, bitsOf(16, t, v, c))
	case kInt, kUInt:
		g.p("c23.Store32(%s, %s)", addr, bitsOf(32, t, v, c))
	case kLong, kULong, kLLong, kULLong:
		g.p("c23.Store%d(%s, %s)", t.sizeof()*8, addr, bitsOf(int(t.sizeof()*8), t, v, c))
	case kFloat:
		g.p("c23.StoreF32(%s, %s)", addr, v)
	case kDouble, kLDouble:
		g.p("c23.StoreF64(%s, %s)", addr, v)
	case kPtr, kNullptr, kVaList, kIntCap, kUIntCap:
		g.p("c23.StorePtr(%s, %s)", addr, v)
	case kStruct, kArray:
		g.p("%s", moveCode(addr, v, t))
	case kInt128:
		g.p("c23.StoreI128(%s, %s)", addr, v)
	case kUInt128:
		g.p("c23.StoreU128(%s, %s)", addr, v)
	case kComplex:
		switch t.elem.kind {
		case kFloat:
			g.p("c23.StoreC64(%s, %s)", addr, v)
		case kLDouble:
			g.p("c23.StoreCL(%s, %s)", addr, v)
		default:
			g.p("c23.StoreC128(%s, %s)", addr, v)
		}
	default:
		panic(fmt.Sprintf("store of %s", t))
	}
}

func (g *gen) storeL(l lval, v string, c expr) {
	switch {
	case l.goVar != "":
		g.p("%s = %s", l.goVar, v)
	case l.direct != "" && directScalar(l.t) && l.f == nil:
		if k, ok := c.(*intConst); ok {
			v = constLit(l.t, k.v)
		}
		g.p("%s = %s", l.direct, v)
	case l.f != nil:
		g.bitStore(l.addr, l.f, v, c)
	case isAtomicAgg(l):
		g.p("c23.AtomicStoreAgg(%s, %s, %d)", l.addr, v, l.t.sizeof())
	case isAtomic(l):
		if goType(l.t) == "c23.Ptr" {
			g.p("c23.AtomicStoreP(%s, %s)", l.addr, v)
		} else {
			if k, ok := c.(*intConst); ok {
				v = constLit(l.t, k.v)
			}
			g.p("c23.AtomicStore(%s, %s(%s))", l.addr, goType(l.t), v)
		}
	default:
		g.store(l.addr, l.t, v, c)
	}
}

// Bit-fields: in a unit of their type's size at the field's offset.

func (g *gen) bitLoad(addr string, f *field) string {
	n := int(f.unit.sizeof() * 8)
	raw := fmt.Sprintf("c23.Load%d(%s)", n, addr)
	if f.ubytes > 0 {
		n = 64
		raw = fmt.Sprintf("c23.LoadBytes(%s, %d)", addr, f.ubytes)
	}
	t := f.t
	if t.isUnsigned() {
		v := fmt.Sprintf("((%s >> %d) & %d)", raw, f.bitAt, uint64(1)<<f.bits-1)
		if f.bits == 64 {
			v = raw
		}
		return fmt.Sprintf("%s(%s)", goType(t), v)
	}
	// Signed: shift it to the top, then back down with its sign.
	return fmt.Sprintf("%s(int%d(%s << %d) >> %d)", goType(t), n, raw, n-f.bitAt-f.bits, n-f.bits)
}

func (g *gen) bitStore(addr string, f *field, v string, c expr) {
	n := int(f.unit.sizeof() * 8)
	if f.ubytes > 0 {
		n = 64
	}
	if !isSimple(addr) {
		t := g.tmp("c23.Ptr")
		g.p("%s = %s", t, addr)
		addr = t
	}
	mask := (uint64(1)<<f.bits - 1) << f.bitAt
	if f.bits == 64 {
		mask = ^uint64(0)
	}
	m := mask
	if n < 64 {
		m &= uint64(1)<<n - 1
	}
	load, store := fmt.Sprintf("c23.Load%d(%s)", n, addr), fmt.Sprintf("c23.Store%d(%s, ", n, addr)
	if f.ubytes > 0 {
		load, store = fmt.Sprintf("c23.LoadBytes(%s, %d)", addr, f.ubytes), fmt.Sprintf("c23.StoreBytes(%s, %d, ", addr, f.ubytes)
	}
	if k, ok := c.(*intConst); ok {
		g.p("%s%s&^%d | %d)", store, load, m, (uint64(k.v)<<f.bitAt)&m)
		return
	}
	g.p("%s%s&^%d | (%s<<%d)&%d)", store, load, m, bitsOf(n, f.t, v, c), f.bitAt, m)
}

// effect evaluates e for its effects.
func (g *gen) effect(e expr) {
	switch e := e.(type) {
	case *assign:
		g.assign(e, false)
	case *incDec:
		g.incDec(e, false)
	case *binary:
		switch e.op {
		case ",":
			g.effect(e.x)
			g.effect(e.y)
			return
		case "&&", "||":
			c := g.cond(e.x)
			if e.op == "||" {
				c = "!(" + c + ")"
			}
			g.p("if %s {", c)
			g.effect(e.y)
			g.p("}")
			return
		}
		g.effect(e.x)
		g.effect(e.y)
	case *cond:
		c := g.cond(e.c)
		g.p("if %s {", c)
		g.effect(e.x)
		g.p("} else {")
		g.effect(e.y)
		g.p("}")
	case *call:
		v := g.fenvCall(e, g.callCode(e))
		if strings.HasPrefix(v, "t") && isSimple(v) {
			return
		}
		if e.t.kind != kVoid && e.t.kind != kStruct {
			g.p("_ = %s", v)
		} else if v != "" {
			g.p("%s", v)
		}
	case *cast:
		g.effect(e.x)
	case *intConst, *floatConst, *strLit, *varRef:
	case *stmtExpr:
		g.stmtExprCode(e, false)
	case *vaOp:
		if v := g.vaOp(e); v != "" {
			g.p("_ = %s", v) // va_arg moves ap on
		}
	case *builtinCall:
		if v := g.builtinCode(e); v != "" && isSimple(v) {
			g.p("_ = %s", v)
		} else if v != "" {
			g.p("%s", v)
		}
	case *unary:
		g.effect(e.x)
	case *decay:
		g.effect(e.x)
	case *addrOf:
		if _, ok := e.x.(*compoundLit); ok {
			g.rv(e)
		}
		g.effect(e.x)
	case *member:
		g.effect(e.x)
	case *deref:
		v := g.rv(e)
		if e.t.kind != kStruct && e.t.kind != kArray {
			g.p("_ = %s", v)
		}
	case *compoundLit:
		g.compound(e)
	case *seqExpr:
		for _, s := range e.pre {
			g.stmt(s)
		}
		g.effect(e.x)
	default:
		v := g.rv(e)
		if !isSimple(v) {
			g.p("_ = %s", v)
		}
	}
}

// cond returns e, a scalar, as a Go bool.
func (g *gen) cond(e expr) string {
	switch e := e.(type) {
	case *binary:
		switch e.op {
		case "&&", "||":
			x := g.cond(e.x)
			var y string
			pre := g.capture(func() { y = g.cond(e.y) })
			if pre == "" {
				return fmt.Sprintf("(%s %s %s)", x, e.op, y)
			}
			t := g.tmp("bool")
			g.p("%s = %s", t, x)
			if e.op == "&&" {
				g.p("if %s {", t)
			} else {
				g.p("if !%s {", t)
			}
			g.out.WriteString(pre)
			g.p("%s = %s", t, y)
			g.p("}")
			return t
		case "==", "!=", "<", ">", "<=", ">=":
			return g.compare(e)
		}
	case *unary:
		if e.op == "!" {
			return "!(" + g.cond(e.x) + ")"
		}
	case *intConst:
		if e.v != 0 {
			return "true"
		}
		return "false"
	}
	v := g.rv(e)
	t := e.ty()
	if t.isPtr() || t.kind == kNullptr || t.kind == kVaList || t.isCap() {
		return fmt.Sprintf("(c23.Addr(%s) != 0)", v)
	}
	if is128(t) {
		return fmt.Sprintf("!%s.IsZero()", v)
	}
	return fmt.Sprintf("(%s != 0)", v)
}

func (g *gen) compare(e *binary) string {
	x, y := g.rv(e.x), g.rv(e.y)
	if is128(e.x.ty()) {
		return fmt.Sprintf("(%s.Cmp(%s) %s 0)", x, y, e.op)
	}
	if e.x.ty().isPtr() || e.x.ty().kind == kNullptr {
		return fmt.Sprintf("(%s %s %s)", g.addrCode(e.x, x), e.op, g.addrCode(e.y, y))
	}
	if s, ok := g.fenvOf(e); ok {
		if code := fenvCompare(e.op, x, y, e.x.ty(), s); code != "" {
			return code
		}
	}
	if t := e.x.ty(); t.isCap() {
		if t.kind == kIntCap {
			return fmt.Sprintf("(int(c23.Addr(%s)) %s int(c23.Addr(%s)))", x, e.op, y)
		}
		return fmt.Sprintf("(c23.Addr(%s) %s c23.Addr(%s))", x, e.op, y)
	}
	return fmt.Sprintf("(%s %s %s)", x, e.op, y)
}

// addrCode is a pointer's address, for comparisons.
func (g *gen) addrCode(e expr, v string) string {
	if c, ok := e.(*intConst); ok {
		return fmt.Sprintf("uintptr(%d)", uint64(c.v)&(1<<(8*target.ptrSize)-1))
	}
	return "c23.Addr(" + v + ")"
}

// rv returns e's value.
func (g *gen) rv(e expr) string {
	switch e := e.(type) {
	case *intConst:
		return constLit(e.t, e.v)
	case *floatConst:
		return floatLit(e.t, e.v)
	case *complexConst:
		return fmt.Sprintf("%s(complex(%s, %s))", goType(e.t), floatLit(tDouble, e.re), floatLit(tDouble, e.im))
	case *strLit:
		return g.str(e)
	case *varRef:
		if e.sym.kind == symFunc {
			return g.funcPtr(e.sym)
		}
		return g.load(g.lv(e))
	case *deref, *member, *compoundLit:
		return g.load(g.lv(e))
	case *decay:
		if e.x.ty().kind == kFunc {
			if v, ok := e.x.(*varRef); ok {
				return g.funcPtr(v.sym)
			}
			return g.rv(e.x)
		}
		return g.lv(e.x).addr
	case *addrOf:
		if v, ok := e.x.(*varRef); ok && v.sym.kind == symFunc {
			return g.funcPtr(v.sym)
		}
		if isLvalue(e.x) {
			return g.lv(e.x).addr
		}
		return g.rv(e.x) // a struct value's memory
	case *unary:
		switch e.op {
		case "!":
			return "c23.B2I(" + g.cond(e) + ")"
		case "-":
			if is128(e.t) {
				return g.rv(e.x) + ".Neg()"
			}
			if e.t.isCap() {
				return "c23.CapUnary('-', " + g.rv(e.x) + ")"
			}
			return wrapBits("(-"+g.rv(e.x)+")", e.t)
		case "~":
			if is128(e.t) {
				return g.rv(e.x) + ".Not()"
			}
			if e.t.isCap() {
				return "c23.CapUnary('~', " + g.rv(e.x) + ")"
			}
			return wrapBits("(^"+g.rv(e.x)+")", e.t)
		}
	case *binary:
		return g.binary(e)
	case *assign:
		return g.assign(e, true)
	case *incDec:
		return g.incDec(e, true)
	case *cond:
		return g.condExpr(e)
	case *call:
		return g.fenvCall(e, g.callCode(e))
	case *cast:
		return g.castCode(e)
	case *stmtExpr:
		return g.stmtExprCode(e, true)
	case *vlaSizeof:
		return g.vlaSize(e.of)
	case *goVal:
		return e.code
	case *labelAddr:
		return fmt.Sprintf("c23.FromInt(%d)", e.l.id)
	case *seqExpr:
		for _, s := range e.pre {
			g.stmt(s)
		}
		return g.rv(e.x)
	case *vaOp:
		return g.vaOp(e)
	case *builtinCall:
		return g.builtinCode(e)
	}
	g.errorf(e.at(), "internal error: unexpected expression %T", e)
	return ""
}

func (g *gen) binary(e *binary) string {
	switch e.op {
	case ",":
		g.effect(e.x)
		return g.rv(e.y)
	case "&&", "||", "==", "!=", "<", ">", "<=", ">=":
		return "c23.B2I(" + g.cond(e) + ")"
	}
	xt := e.x.ty()
	if xt.isPtr() {
		p := g.rv(e.x)
		if e.y.ty().isPtr() {
			q := g.rv(e.y)
			return fmt.Sprintf("%s(c23.Sub(%s, %s) / %d)", goType(e.t), p, q, max(g.elemSize(xt), 1))
		}
		return g.ptrAdd(p, xt, e.y, e.op == "-")
	}
	x, y := g.rv(e.x), g.rv(e.y)
	if (e.op == "/" || e.op == "%" || e.op == "<<" || e.op == ">>") && isConstExpr(e.y) {
		// A constant Go would reject at compile time (division by zero,
		// a negative shift), which C rejects only if it runs.
		if c, ok := e.y.(*intConst); ok && (c.v == 0 && (e.op == "/" || e.op == "%") || c.v < 0) {
			y = "c23.Val(" + y + ")"
		}
		if c, ok := e.y.(*floatConst); ok && c.v == 0 {
			y = "c23.Val(" + y + ")"
		}
	}
	if is128(e.t) {
		return arith128(e.op, x, y)
	}
	if e.t.isCap() {
		return capArith(e.op, x, y, e.t)
	}
	if s, ok := g.fenvOf(e); ok {
		if code := fenvArith(e.op, x, y, e.t, s); code != "" {
			return code
		}
	}
	return wrapBits(fmt.Sprintf("(%s %s %s)", x, e.op, y), e.t)
}

// complexConvert converts to or from a complex type.
func complexConvert(x string, from, to *ctype) string {
	switch {
	case to.kind == kComplex && from.kind == kComplex:
		if goType(from) == goType(to) {
			return x
		}
		return fmt.Sprintf("%s(%s)", goType(to), x)
	case to.kind == kComplex:
		f := goType(to.elem)
		return fmt.Sprintf("complex(%s(%s), 0)", f, convertCode(x, from, to.elem))
	case to.kind == kBool:
		return fmt.Sprintf("c23.B2U8(%s != 0)", x)
	}
	// C takes the real part.
	return convertCode(fmt.Sprintf("real(%s)", x), from.elem, to)
}

// wrapBits wraps x, a Go value of t's Go type, to t's width if t is a
// _BitInt narrower than its Go type.
func wrapBits(x string, t *ctype) string {
	if !t.isBitInt() || t.n == t.sizeof()*8 {
		return x
	}
	w := t.sizeof() * 8
	if t.kind == kUBitInt {
		return fmt.Sprintf("(%s & %d)", x, uint64(1)<<t.n-1)
	}
	return fmt.Sprintf("(%s << %d >> %d)", x, w-t.n, w-t.n)
}

// elemSize is the size of what a pointer of type t points to.
func (g *gen) elemSize(t *ctype) int64 {
	if t.elem.kind == kVoid || t.elem.kind == kFunc {
		return 1
	}
	return t.elem.sizeof()
}

// ptrAdd is p plus or minus i elements.
func (g *gen) ptrAdd(p string, pt *ctype, i expr, neg bool) string {
	if pt.elem.isVLA() {
		size := g.vlaSize(pt.elem)
		sign := ""
		if neg {
			sign = "-"
		}
		return fmt.Sprintf("c23.Add(%s, %sint(%s)*int(%s))", p, sign, g.rv(i), size)
	}
	size := g.elemSize(pt)
	if c, ok := i.(*intConst); ok {
		n := c.v * size
		if neg {
			n = -n
		}
		if n == 0 {
			return p
		}
		return fmt.Sprintf("c23.Add(%s, %d)", p, addrOff(n))
	}
	sign := ""
	if neg {
		sign = "-"
	}
	if size == 1 {
		return fmt.Sprintf("c23.Add(%s, %sint(%s))", p, sign, g.rv(i))
	}
	return fmt.Sprintf("c23.Add(%s, %sint(%s)*%d)", p, sign, g.rv(i), size)
}

func (g *gen) castCode(e *cast) string {
	to := e.t
	from := e.x.ty()
	if to.kind == kVoid {
		g.effect(e.x)
		return ""
	}
	if to.isPtr() {
		if c, ok := e.x.(*call); ok {
			if code, ok := g.allocCall(c, to); ok {
				return code
			}
		}
	}
	x := g.rv(e.x)
	if s, ok := g.fenvOf(e); ok {
		return convertUnder(x, from, to, s)
	}
	return convertCode(x, from, to)
}

// convertCode converts the Go value x of C type from to C type to.
var constConv = regexp.MustCompile(`^(u?int(8|16|32|64))\((-?[0-9]+)\)$`)

func convertCode(x string, from, to *ctype) string {
	if is128(to) && from.isCap() {
		x, from = fmt.Sprintf("%s(c23.Addr(%s))", goType(capInt(from)), x), capInt(from)
	}
	if is128(from) && to.isCap() {
		return convertCode(convertCode(x, from, capInt(to)), capInt(to), to)
	}
	if is128(to) || is128(from) {
		if m := constConv.FindStringSubmatch(x); m != nil && is128(to) {
			return constLitStr(to, m[3])
		}
		return convert128(x, from, to)
	}
	if to.kind == kComplex || from.kind == kComplex {
		return complexConvert(x, from, to)
	}
	if m := constConv.FindStringSubmatch(x); m != nil && to.isInteger() && !to.isBitInt() {
		// A constant: converted here, as Go would reject an overflow.
		if v, err := strconv.ParseInt(m[3], 10, 64); err == nil {
			return constLit(to, v)
		}
		if v, err := strconv.ParseUint(m[3], 10, 64); err == nil {
			return constLit(to, int64(v))
		}
	}
	fromPtr := from.isPtr() || from.kind == kNullptr || from.kind == kVaList || from.isCap()
	switch {
	case to.kind == kBool:
		if fromPtr {
			return fmt.Sprintf("c23.B2U8(c23.Addr(%s) != 0)", x)
		}
		return fmt.Sprintf("c23.B2U8(%s != 0)", x)
	case to.isCap():
		// A pointer's or an intcap's capability stays; an integer has none.
		switch {
		case fromPtr:
			return x
		case from.isUnsigned() || from.isFloat() && to.kind == kUIntCap:
			return fmt.Sprintf("c23.UintToCap(uint64(%s))", x)
		}
		return fmt.Sprintf("c23.IntToCap(int64(%s))", x)
	case to.isInteger():
		if fromPtr {
			return wrapBits(fmt.Sprintf("%s(c23.Addr(%s))", goType(to), x), to)
		}
		if goType(from) == goType(to) {
			if to.isBitInt() && !(from.isBitInt() && from.n <= to.n && from.isUnsigned() == to.isUnsigned()) {
				return wrapBits(x, to)
			}
			return x
		}
		if from.isFloat() || to.isBitInt() {
			return wrapBits(fmt.Sprintf("%s(%s)", goType(to), x), to)
		}
		return fmt.Sprintf("%s(%s)", goType(to), x)
	case to.isFloat():
		if from.kind == kIntCap {
			return fmt.Sprintf("%s(int(c23.Addr(%s)))", goType(to), x)
		}
		if from.kind == kUIntCap {
			return fmt.Sprintf("%s(c23.Addr(%s))", goType(to), x)
		}
		if goType(from) == goType(to) {
			return x
		}
		return fmt.Sprintf("%s(%s)", goType(to), x)
	case to.isPtr() || to.kind == kNullptr || to.kind == kVaList:
		if fromPtr {
			return x
		}
		return fmt.Sprintf("c23.FromInt(uintptr(%s))", x)
	case to.kind == kStruct:
		return x
	}
	panic(fmt.Sprintf("conversion of %s to %s", from, to))
}

func (g *gen) assign(e *assign, value bool) string {
	t := e.x.ty()
	if t.kind == kStruct {
		l := g.lv(e.x)
		g.bind(&l)
		src := g.rv(e.y)
		g.p("%s", moveCode(l.addr, src, t))
		return l.addr
	}
	l := g.lv(e.x)
	if isAtomic(l) && e.op != "=" {
		// x op= y: atomically.
		g.bind(&l)
		op := e.op[:len(e.op)-1]
		y := g.tmp(goType(e.y.ty()))
		g.p("%s = %s", y, g.rv(e.y))
		_, new := g.atomicUpdate(l, func(cur string) string {
			if t.isPtr() {
				return g.ptrAdd(cur, t, &goVal{node{e.y.ty(), e.p}, y}, op == "-")
			}
			x := convertCode(cur, t.unqual(), e.ct)
			if e.ct.isCap() {
				return convertCode(capArith(op, x, y, e.ct), e.ct, t.unqual())
			}
			return convertCode(fmt.Sprintf("(%s %s %s)", x, op, y), e.ct, t.unqual())
		})
		return new
	}
	var v string
	if e.op == "=" {
		v = g.rv(e.y)
		if l.goVar == "" {
			g.bind(&l)
		}
	} else {
		g.bind(&l)
		cur := g.load(l)
		op := e.op[:len(e.op)-1]
		if t.isPtr() {
			v = g.ptrAdd(cur, t, e.y, op == "-")
		} else {
			s, fe := g.fenvOf(e)
			x := convertCode(cur, t.unqual(), e.ct)
			if fe {
				x = convertUnder(cur, t.unqual(), e.ct, s)
			}
			y := g.rv(e.y)
			if (op == "/" || op == "%") && isConstExpr(e.y) {
				y = "c23.Val(" + y + ")"
			}
			switch r := fenvArith(op, x, y, e.ct, s); {
			case is128(e.ct):
				v = convertCode(arith128(op, x, y), e.ct, t.unqual())
			case e.ct.isCap():
				v = convertCode(capArith(op, x, y, e.ct), e.ct, t.unqual())
			case fe && r != "":
				v = convertUnder(r, e.ct, t.unqual(), s)
			case fe:
				v = convertUnder(fmt.Sprintf("(%s %s %s)", x, op, y), e.ct, t.unqual(), s)
			default:
				v = convertCode(fmt.Sprintf("(%s %s %s)", x, op, y), e.ct, t.unqual())
			}
		}
	}
	var c expr
	if e.op == "=" {
		c = e.y
	}
	if !value {
		g.storeL(l, v, c)
		return ""
	}
	if l.goVar != "" {
		g.storeL(l, v, c)
		return l.goVar
	}
	tv := g.tmp(goType(t))
	if c, ok := e.y.(*intConst); ok && e.op == "=" {
		v = constLit(t, c.v)
	}
	g.p("%s = %s", tv, v)
	g.storeL(l, tv, nil)
	if l.f != nil {
		return g.bitLoad(l.addr, l.f)
	}
	return tv
}

// moveCode copies a struct (or array) of type t from src to dst: one word
// with c23.MoveWord (a union of C's, Lua's TValue's value), whose fast path
// cmd/compile inlines.
func moveCode(dst, src string, t *ctype) string {
	if t.sizeof() == target.ptrSize {
		return fmt.Sprintf("c23.MoveWord(%s, %s)", dst, src)
	}
	return fmt.Sprintf("c23.Memmove(%s, %s, %d)", dst, src, t.sizeof())
}

func (g *gen) incDec(e *incDec, value bool) string {
	t := e.t
	l := g.lv(e.x)
	if isAtomic(l) {
		g.bind(&l)
		old, new := g.atomicUpdate(l, func(cur string) string {
			switch {
			case t.isPtr():
				n := g.elemSize(t)
				if e.op == "--" {
					n = -n
				}
				return fmt.Sprintf("c23.Add(%s, %d)", cur, addrOff(n))
			case t.isCap():
				return fmt.Sprintf("c23.Add(%s, %s1)", cur, map[bool]string{true: "-"}[e.op == "--"])
			case t.kind == kBool:
				if e.op == "++" {
					return "uint8(1)"
				}
				return fmt.Sprintf("(1 - %s)", cur)
			}
			return fmt.Sprintf("(%s %s 1)", cur, e.op[:1])
		})
		if e.post {
			return old
		}
		return new
	}
	s, fe := g.fenvOf(e)
	fe = fe && t.isFloat() && s.runtime()
	if !value && l.goVar != "" && t.isArith() && t.kind != kBool && !t.isBitInt() && !is128(t) && !fe && !t.isCap() {
		g.p("%s%s", l.goVar, e.op)
		return ""
	}
	g.bind(&l)
	old := g.load(l)
	var res string
	if value && e.post {
		o := g.tmp(goType(t))
		g.p("%s = %s", o, old)
		old, res = o, o
	}
	var nv string
	switch {
	case t.isPtr():
		nv = g.ptrAdd(old, t, &intConst{node{tLong, e.p}, 1}, e.op == "--")
	case t.isCap():
		nv = fmt.Sprintf("c23.Add(%s, %s1)", old, map[bool]string{true: "-"}[e.op == "--"])
	case t.kind == kBool:
		if e.op == "++" {
			nv = "uint8(1)"
		} else {
			nv = fmt.Sprintf("(1 - %s)", old)
		}
	case is128(t):
		nv = arith128(e.op[:1], old, constLit(t, 1))
	case fe:
		nv = fenvArith(e.op[:1], old, "1", t, s)
	default:
		op := e.op[:1]
		nv = wrapBits(fmt.Sprintf("(%s %s 1)", old, op), t)
	}
	if value && !e.post {
		n := g.tmp(goType(t))
		g.p("%s = %s", n, nv)
		nv, res = n, n
	}
	g.storeL(l, nv, nil)
	if l.f != nil && value && !e.post {
		return g.bitLoad(l.addr, l.f)
	}
	return res
}

func (g *gen) condExpr(e *cond) string {
	if e.x == e.c && e.t.kind != kVoid {
		return g.elvis(e)
	}
	c := g.cond(e.c)
	if e.t.kind == kVoid {
		g.p("if %s {", c)
		g.effect(e.x)
		g.p("} else {")
		g.effect(e.y)
		g.p("}")
		return ""
	}
	var x, y string
	preX := g.capture(func() { x = g.rv(e.x) })
	preY := g.capture(func() { y = g.rv(e.y) })
	if preX == "" && preY == "" && isPure(x) && isPure(y) {
		return fmt.Sprintf("c23.If(%s, %s, %s)", c, x, y)
	}
	t := g.tmp(goType(e.t))
	g.p("if %s {", c)
	g.out.WriteString(preX)
	g.p("%s = %s", t, x)
	g.p("} else {")
	g.out.WriteString(preY)
	g.p("%s = %s", t, y)
	g.p("}")
	return t
}

// elvis generates GNU's c ?: y, with c evaluated once.
func (g *gen) elvis(e *cond) string {
	{
		t := g.tmp(goType(e.t))
		g.p("%s = %s", t, g.rv(e.x))
		var y string
		preY := g.capture(func() { y = g.rv(e.y) })
		truth := t + " != 0"
		if goType(e.t) == "c23.Ptr" {
			truth = "c23.Addr(" + t + ") != 0"
		}
		g.p("if %s {", truth)
		g.p("} else {")
		g.out.WriteString(preY)
		g.p("%s = %s", t, y)
		g.p("}")
		return t
	}
}

// isPure reports whether evaluating the Go expression s can have no
// effect and cannot fail: no call but conversions, no memory.
func isPure(s string) bool {
	return !strings.Contains(s, "(") || strings.HasPrefix(s, "int") && !strings.Contains(s[1:], "(") ||
		isConvLit(s)
}

func isConvLit(s string) bool {
	i := strings.Index(s, "(")
	if i < 0 || !strings.HasSuffix(s, ")") {
		return false
	}
	switch s[:i] {
	case "int8", "uint8", "int16", "uint16", "int32", "uint32", "int64", "uint64", "float32", "float64":
		return !strings.ContainsAny(s[i+1:len(s)-1], "()")
	}
	return false
}

func (g *gen) stmtExprCode(e *stmtExpr, value bool) string {
	body := func() {
		for _, s := range e.body.list {
			if g.flatSt != nil {
				g.flatStmt(s)
			} else {
				g.stmt(s)
			}
		}
	}
	if e.t.kind == kVoid || !value {
		body()
		if e.last != nil {
			g.effect(e.last)
		}
		return ""
	}
	t := g.tmp(goType(e.t))
	body()
	g.p("%s = %s", t, g.rv(e.last))
	return t
}

// Calls.

// funcName is the Go function a call of s calls.
func (g *gen) funcName(s *symbol, at pos) string {
	if s.nested {
		return g.localName(s)
	}
	def := s
	if s.linkage == 'e' {
		if d := g.ext[s.name]; d != nil {
			def = d
		}
	}
	if !def.defined {
		if n, ok := libc[s.name]; ok {
			return "c23." + n
		}
		if s.weak || def.weak {
			return ""
		}
		g.errorf(at, "undefined reference to %q", s.name)
	}
	return g.names[def]
}

// funcPtr is the c23.Ptr to a function: its descriptor.
func (g *gen) funcPtr(s *symbol) string {
	name := g.funcName(s, s.p)
	if name == "" {
		return "c23.Ptr{}" // weak, and not defined
	}
	if s.nested {
		return fmt.Sprintf("c23.NewFunc(%s)", name)
	}
	d := "fd_" + strings.ReplaceAll(name, ".", "_")
	if !g.descs[d] {
		// Set in an init of its own, before C's initializers use it: a
		// package-level initializer would make a cycle if the function
		// uses its own address.
		g.descs[d] = true
		fmt.Fprintf(&g.vars, "var %s c23.Ptr\n", d)
		fmt.Fprintf(&g.descInit, "%s = c23.NewFunc(%s)\n", d, g.libcAdapter(name, s))
	}
	return d
}

func (g *gen) callCode(c *call) string {
	var fn string
	direct := false
	callee := c.ft // the type the function has where it is defined
	if d, ok := c.fn.(*decay); ok {
		if v, ok := d.x.(*varRef); ok && v.sym.kind == symFunc {
			fn = g.funcName(v.sym, c.p)
			direct = true
			if t := g.calleeType(v.sym); t != nil {
				callee = t.fn
			}
		}
	}
	if !direct || fn == "" {
		fn = fmt.Sprintf("c23.FuncOf[%s](%s)", g.funcSig(c.ft, nil), g.rv(c.fn))
	}
	var args []string
	var sret string
	if c.ft.ret.kind == kStruct {
		sret = g.cellTmp(c.ft.ret)
		args = append(args, sret)
	}
	for i, a := range c.args {
		v := g.rv(a)
		if i >= len(callee.params) && a.ty().kind == kStruct {
			// By value, in variable arguments: a copy.
			v = fmt.Sprintf("c23.Copy(%s, %d)", v, a.ty().sizeof())
		}
		if callee != c.ft {
			// Declared here with another type than where it is
			// defined: what the ABI would pass.
			switch {
			case i < len(callee.params):
				v = abiConvert(v, a, callee.params[i])
			case !callee.variadic && (callee.proto || len(callee.params) > 0):
				continue // an extra argument: ignored
			}
		}
		if direct && strings.HasPrefix(fn, "c23.") && i < len(callee.params) {
			v = libcConv(v, callee.params[i], true)
		}
		if !direct && !isSimple(v) && !isPure(v) {
			// C evaluates the arguments before the call, whose pointer
			// is checked when Go evaluates the function.
			t := g.tmp(goType(a.ty()))
			if i >= len(c.ft.params) && a.ty().kind == kStruct {
				t = g.tmp("c23.Ptr")
			}
			g.p("%s = %s", t, v)
			v = t
		}
		args = append(args, v)
	}
	code := fmt.Sprintf("%s(%s)", fn, strings.Join(args, ", "))
	if direct && strings.HasPrefix(fn, "c23.") {
		code = libcConv(code, callee.ret, false)
	}
	if callee != c.ft && !compatible(callee.ret.unqual(), c.ft.ret.unqual()) {
		switch {
		case callee.ret.kind == kVoid:
			g.p("%s", code)
			return zeroOf(c.ft.ret)
		case c.ft.ret.kind == kVoid:
			g.p("_ = %s", code)
			return ""
		case c.ft.ret.kind == kBool && callee.ret.isInteger():
			// The low byte, as on x86-64.
			return fmt.Sprintf("uint8(%s)", code)
		case c.ft.ret.isScalar() && callee.ret.isScalar():
			return convertCode(code, callee.ret, c.ft.ret)
		}
	}
	if sret != "" {
		g.p("%s", code)
		return sret
	}
	if c.ft.noreturn || fn == "c23.Exit" || fn == "c23.Abort" {
		return code
	}
	return code
}

// calleeType is the type of the function s calls: as its definition (or
// its library header) declares it.
func (g *gen) calleeType(s *symbol) *ctype {
	if s.linkage == 'e' {
		if d := g.ext[s.name]; d != nil && d.defined {
			return d.t
		}
	}
	if s.defined {
		return s.t
	}
	if _, ok := libc[s.name]; ok {
		return libcType(s.name)
	}
	return nil
}

// abiConvert converts an argument a (Go value v) to a parameter of type to.
func abiConvert(v string, a expr, to *ctype) string {
	from := a.ty()
	if compatible(from.unqual(), to.unqual()) || !from.isScalar() || !to.isScalar() {
		return v
	}
	if c, ok := a.(*intConst); ok && to.isInteger() {
		return constLit(to, c.v)
	}
	return convertCode(v, from, to)
}

// allocCall generates malloc, calloc or realloc whose result is converted
// to the pointer type to: memory typed as to's elements, so that C keeps
// its pointers in it as Go pointers.
func (g *gen) allocCall(c *call, to *ctype) (string, bool) {
	d, ok := c.fn.(*decay)
	if !ok {
		return "", false
	}
	v, ok := d.x.(*varRef)
	if !ok || v.sym.kind != symFunc || v.sym.defined || g.ext[v.sym.name] != nil && g.ext[v.sym.name].defined {
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
	if !hasPointers(e) {
		// Scalars only: untyped memory, which may still come to hold
		// pointers (a header before the memory a pointer to it gives
		// out, as allocators do).
		return "", false
	}
	m := g.mirror(e)
	switch v.sym.name {
	case "malloc":
		return fmt.Sprintf("c23.NewArrayT(%s, uintptr(%s))", g.typeDesc(m), g.rv(c.args[0])), true
	case "calloc":
		return fmt.Sprintf("c23.NewArrayT(%s, uintptr(%s) * uintptr(%s))", g.typeDesc(m), g.rv(c.args[0]), g.rv(c.args[1])), true
	case "realloc":
		return fmt.Sprintf("c23.ReallocOf[%s](%s, uintptr(%s))", m, g.rv(c.args[0]), g.rv(c.args[1])), true
	}
	return "", false
}

func (g *gen) vaOp(e *vaOp) string {
	switch e.op {
	case "start":
		l := g.lv(e.ap)
		g.storeL(l, "c23.VaStart(va)", nil)
	case "end":
		g.effect(e.ap)
	case "copy":
		l := g.lv(e.ap)
		g.storeL(l, fmt.Sprintf("c23.VaCopy(%s)", g.rv(e.src)), nil)
	case "arg":
		ap := g.rv(e.ap)
		if e.t.kind == kStruct {
			return fmt.Sprintf("c23.VaArgPtr(%s)", ap)
		}
		return fmt.Sprintf("c23.VaArg[%s](%s)", goType(e.t), ap)
	}
	return ""
}

func (g *gen) builtinCode(e *builtinCall) string {
	switch e.name {
	case "trap":
		return "c23.Trap()"
	case "conj":
		x := g.tmp(goType(e.t))
		g.p("%s = %s", x, g.rv(e.args[0]))
		return fmt.Sprintf("complex(real(%s), -imag(%s))", x, x)
	case "creal":
		return fmt.Sprintf("real(%s)", g.rv(e.args[0]))
	case "cimag":
		return fmt.Sprintf("imag(%s)", g.rv(e.args[0]))
	case "complex":
		return fmt.Sprintf("complex(%s, %s)", g.rv(e.args[0]), g.rv(e.args[1]))
	case "overflow":
		op := e.args[0].(*intConst).v
		a, b := e.args[1], e.args[2]
		rt := e.args[4].ty()
		pred := e.args[5].(*intConst).v != 0
		code := fmt.Sprintf("c23.Overflow(%d, int64(%s), %v, int64(%s), %v, %d, %v)", op,
			g.rv(a), a.ty().isUnsigned(), g.rv(b), b.ty().isUnsigned(), rt.width(), rt.isUnsigned())
		v, over := g.tmp("int64"), g.tmp("bool")
		g.p("%s, %s = %s", v, over, code)
		if !pred {
			g.store(g.rv(e.args[3]), rt, convertCode(v, tLLong, rt), nil)
		} else {
			g.effect(e.args[3])
		}
		return fmt.Sprintf("c23.B2U8(%s)", over)
	case "atomic_load":
		if e.t.kind == kStruct {
			return g.load(lval{addr: g.rv(e.args[0]), t: e.t.qualified(qAtomic)})
		}
		return atomicLoad(g.rv(e.args[0]), e.t)
	case "atomic_store":
		p, v := g.rv(e.args[0]), g.rv(e.args[1])
		g.storeL(lval{addr: p, t: e.args[0].ty().elem.qualified(qAtomic)}, v, e.args[1])
		return ""
	case "atomic_fetch":
		p := g.rv(e.args[0])
		op := e.args[1].(*intConst).v
		v := g.rv(e.args[2])
		et := e.args[0].ty().elem
		if et.kind == kStruct {
			old := g.cellTmp(et.unqual())
			g.p("c23.AtomicSwapAgg(%s, %s, %s, %d)", p, old, v, et.sizeof())
			return old
		}
		if goType(et) == "c23.Ptr" {
			if op == '=' {
				return fmt.Sprintf("c23.AtomicFetchP(%s, '=', 0, %s)", p, v)
			}
			n := fmt.Sprintf("int(%s)*%d", v, max(elemSizeOf(et.unqual()), 1))
			if op == '-' {
				n = "-" + n
			}
			return fmt.Sprintf("c23.AtomicFetchP(%s, '+', %s, c23.Ptr{})", p, n)
		}
		return fmt.Sprintf("c23.AtomicFetch(%s, %d, %s(%s))", p, op, goType(et), v)
	case "atomic_cmpxchg":
		p, ex, d := g.rv(e.args[0]), g.rv(e.args[1]), g.rv(e.args[2])
		et := e.args[0].ty().elem
		if et.kind == kStruct {
			return fmt.Sprintf("c23.B2U8(c23.AtomicCmpXchgAgg(%s, %s, %s, %d))", p, ex, d, et.sizeof())
		}
		if goType(et) == "c23.Ptr" {
			return fmt.Sprintf("uint8(c23.AtomicCmpXchgP(%s, %s, %s))", p, ex, d)
		}
		return fmt.Sprintf("uint8(c23.AtomicCmpXchg(%s, %s, %s(%s)))", p, ex, goType(et), d)
	case "atomic_fence":
		return "c23.Fence()"
	case "alloca":
		return fmt.Sprintf("c23.Malloc(%s)", libcConv(g.rv(e.args[0]), e.args[0].ty(), true))
	case "setjmp":
		k := e.args[1].(*intConst).v
		t := g.tmp("int32")
		g.p("%s = c23.Setjmp(%s, jf, %d)", t, g.rv(e.args[0]), k)
		// longjmp comes back here.
		g.p("J%d:", k)
		g.p("if jsite == %d {", k)
		g.p("%s, jsite = jval, 0", t)
		g.p("}")
		return t
	case "assert":
		return fmt.Sprintf("c23.AssertC(%s, %s, %s, %s)", g.cond(e.args[0]), g.rv(e.args[1]), g.rv(e.args[2]), g.rv(e.args[3]))
	}
	if strings.HasPrefix(e.name, "bits:") {
		return fmt.Sprintf("c23.%s(%s)", e.name[5:], g.rv(e.args[0]))
	}
	g.errorf(e.p, "internal error: builtin %s", e.name)
	return ""
}

// hasPointers reports whether a value of t holds a pointer.
func hasPointers(t *ctype) bool {
	switch t.kind {
	case kPtr, kVaList, kNullptr, kIntCap, kUIntCap:
		return true
	case kArray:
		return hasPointers(t.elem)
	case kStruct:
		for _, f := range t.rec.fields {
			if hasPointers(f.t) {
				return true
			}
		}
	}
	return false
}

// directScalar reports whether a t in an object Go reaches directly is
// loaded and stored as Go's value of it: a scalar that is not a pointer
// (whose capability c23 keeps) or atomic, and whose mirror is its Go type.
func directScalar(t *ctype) bool {
	if t.q&qAtomic != 0 {
		return false
	}
	switch {
	case t.isInteger():
		return !is128(t) && !t.isCap() // an intcap's memory is an Intcap
	case t.kind == kFloat || t.kind == kDouble:
		return true
	}
	return false
}

// directElem is the lvalue of *(a + i) or *a, where a is an array whose
// object Go reaches directly: its element, by Go's index (which Go
// checks).
func (g *gen) directElem(e *deref) (lval, bool) {
	var arr, idx expr
	switch x := e.x.(type) {
	case *decay:
		arr = x.x
	case *binary:
		if x.op != "+" {
			return lval{}, false
		}
		d, ok := x.x.(*decay)
		if !ok || !x.y.ty().isInteger() {
			return lval{}, false
		}
		arr, idx = d.x, x.y
	default:
		return lval{}, false
	}
	at := arr.ty()
	if at.kind != kArray || at.n < 0 || at.isVLA() || !isLvalue(arr) {
		return lval{}, false
	}
	if _, ok := arr.(*strLit); ok {
		return lval{}, false
	}
	al := g.lv(arr)
	if al.direct == "" {
		return lval{}, false
	}
	if c, ok := idx.(*intConst); ok && (c.v < 0 || c.v >= at.n) {
		return lval{}, false // Go would reject the constant index
	}
	i := "0"
	if idx != nil {
		i = g.rv(idx)
		if !isSimple(i) && !isConvLit(i) {
			t := g.tmp(goType(idx.ty()))
			g.p("%s = %s", t, i)
			i = t
		}
	}
	pt := ptrTo(at.elem)
	addr := al.addr
	if idx != nil {
		addr = g.ptrAdd(al.addr, pt, &goVal{node{idx.ty(), e.p}, i}, false)
	}
	return lval{addr: addr, t: e.t, direct: fmt.Sprintf("%s[%s]", al.direct, i)}, true
}

// capArith is x op y in intcap type t: on the addresses, keeping a
// capability (internal/c23's intcap.go).
func capArith(op, x, y string, t *ctype) string {
	signed := t.kind == kIntCap
	if op == "<<" || op == ">>" {
		return fmt.Sprintf("c23.CapShift('%c', %s, uint64(%s), %t)", op[0], x, y, signed)
	}
	return fmt.Sprintf("c23.CapArith('%s', %s, %s, %t)", op, x, y, signed)
}
