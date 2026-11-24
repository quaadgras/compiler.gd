// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"fmt"
	"strings"
)

// C++'s overloaded functions (doc/gd/cxx.md): a name's functions are a
// list through their symbols; a call parses its arguments, then chooses.

// A defArg is a parameter's default argument: its tokens, parsed again
// for each call that uses it, in the scope of the declaration.
type defArg struct {
	start int // the index of its first token; 0 if there is none
	sc    *scope
}

// cxxDeclareFunc declares the function name of type t in C++: a new
// function, or one declared before with the same parameters.
func (p *parser) cxxDeclareFunc(name string, t *ctype, s specs, at pos) *symbol {
	sc := p.sc
	if sc.kind == skBlock {
		sc = p.nsScope()
	}
	qualified := p.declIn != nil
	if qualified {
		sc = p.declIn
	}
	if p.declTo != nil {
		sc, qualified = p.declTo, false
	}
	clink := p.cLinkage(at) && sc.kind == skNamespace || name == "main" && sc.parent == nil
	key := name
	if t.fn.ctor {
		key = "{ctor}"
	}
	head := sc.vars[key]
	if head != nil && head.kind != symFunc {
		if !head.tagName {
			p.errorAt(at, "%q redeclared as a function", name)
		}
		head = nil
	}
	var last *symbol
	for o := head; o != nil; o = o.next {
		last = o
		if o.tmpl != nil {
			continue
		}
		if o.cLink && clink || sameParams(o.t.fn, t.fn) && constThis(o.t.fn) == constThis(t.fn) {
			if !sameType(o.t.fn.ret, t.fn.ret) && !(o.cLink && compatible(o.t, t)) {
				p.errorAt(at, "conflicting return types for %q", name)
			}
			// Default arguments accumulate.
			if t.fn.defaults != nil {
				if o.t.fn.defaults == nil {
					o.t.fn.defaults = make([]defArg, len(o.t.fn.params))
				}
				for i, d := range t.fn.defaults {
					if d.start != 0 {
						o.t.fn.defaults[i] = d
					}
				}
			}
			if s.storage == sStatic && sc.kind == skNamespace {
				o.linkage = 'i'
			}
			if s.inline {
				o.comdat = true
			}
			return o
		}
	}
	if qualified {
		p.errorAt(at, "no declaration matches %s::%s", sc.name, name)
	}
	sym := &symbol{name: name, src: name, kind: symFunc, t: t, global: true, linkage: 'e', p: at, sc: sc, cLink: clink, comdat: s.inline}
	if !clink {
		sym.name = mangleFunc(sc, name, t.fn, p.tmplSuffix)
	}
	if s.storage == sStatic && sc.kind == skNamespace || inAnon(sc) {
		sym.linkage = 'i'
	}
	if sc.kind == skTemplate {
		// A function template's instance: not found by name, which
		// finds the template.
	} else if last != nil {
		last.next = sym
	} else {
		sc.vars[key] = sym
	}
	if sc.parent == nil {
		p.u.scope[sym.name] = sym
	}
	p.u.syms = append(p.u.syms, sym)
	return sym
}

func constThis(ft *funcType) bool {
	return ft.recv != nil && ft.recv.elem.q&qConst != 0
}

// inAnon reports whether sc is within an unnamed namespace.
func inAnon(sc *scope) bool {
	for ; sc != nil; sc = sc.parent {
		if sc.anon {
			return true
		}
	}
	return false
}

// funcSet is the set of functions that sym, a function, is found with.
func funcSet(sym *symbol) []*symbol {
	var set []*symbol
	for o := sym; o != nil; o = o.next {
		set = append(set, o)
	}
	if sym.sc != nil {
		for _, u := range sym.sc.usedFuncs {
			if u.src == sym.src {
				for o := u; o != nil; o = o.next {
					set = append(set, o)
				}
			}
		}
	}
	return set
}

// Ranks of implicit conversions: less is better.
const (
	rkExact    = 0
	rkQual     = 1 // a qualification added
	rkPromo    = 10
	rkConv     = 20
	rkUser     = 30
	rkEllipsis = 40
	rkNone     = -1
)

// valueType is the type of e as a value: arrays and functions as
// pointers, without qualifiers.
func valueType(e expr) *ctype {
	t := e.ty()
	switch t.kind {
	case kArray:
		return ptrTo(t.elem)
	case kFunc:
		return ptrTo(t)
	}
	return t.unqual()
}

// convRank ranks the implicit conversion of the argument a to a
// parameter of type pt: rkNone if there is none.
func (p *parser) convRank(a expr, pt *ctype) int {
	if l, ok := a.(*initList); ok {
		return p.listRank(l, pt)
	}
	if !pt.isRef() || pt.elem.q&qConst != 0 {
		to := pt
		if to.isRef() {
			to = to.elem
		}
		if r, ok := p.memPtrRank(a, to); ok {
			return r
		}
	}
	at := a.ty()
	p.completeFor(at)
	p.completeFor(pt)
	if pt.isRef() {
		to := pt.elem
		if to.kind == kFunc {
			if at.kind == kFunc && sameType(at, to) {
				return rkExact
			}
			return rkNone
		}
		lv := p.isLvalueCxx(a) && at.kind != kFunc
		same := sameType(to.unqual(), at.unqual()) || similarArrays(to, at)
		base := !same && isBaseOf(to, at)
		if same || base {
			r := rkExact
			if base {
				r = rkConv
			}
			if lv {
				if pt.ref == 2 {
					return rkNone
				}
				if to.q&at.q != at.q {
					return rkNone
				}
				if to.q != at.q && r == rkExact {
					r = rkQual
				}
				return r
			}
			// An rvalue: T && before const T &.
			if pt.ref == 2 {
				return r
			}
			if to.q&qConst == 0 {
				return rkNone
			}
			if r == rkExact {
				r = rkQual
			}
			return r
		}
		if pt.ref == 1 && to.q&qConst == 0 {
			return rkNone
		}
		// A temporary of the referred type: an rvalue, which T && binds
		// better than const T &.
		r := p.convRank(a, to.unqual())
		if r != rkNone && pt.ref == 1 {
			r++
		}
		return r
	}
	vt := valueType(a)
	pt = pt.unqual()
	if pt.kind == kStruct || vt.kind == kStruct {
		return p.classConvRank(a, vt, pt)
	}
	if sameType(vt, pt) {
		return rkExact
	}
	switch {
	case pt.isPtr() && vt.isPtr():
		if sameType(pt.elem.unqual(), vt.elem.unqual()) {
			if pt.elem.q&vt.elem.q != vt.elem.q {
				return rkNone
			}
			return rkQual
		}
		if pt.elem.kind == kVoid && vt.elem.kind != kFunc && pt.elem.q&vt.elem.q == vt.elem.q {
			return rkConv + 2 // after a conversion to a base
		}
		if isBaseOf(pt.elem, vt.elem) && pt.elem.q&vt.elem.q == vt.elem.q {
			return rkConv
		}
		if compatible(pt.elem.unqual(), vt.elem.unqual()) {
			return rkQual // C's compatible types (an array of unknown length)
		}
		return rkNone
	case pt.isPtr():
		if vt.kind == kNullptr || isNullConst(a) {
			return rkConv
		}
		return rkNone
	case pt.kind == kNullptr:
		if isNullConst(a) {
			return rkConv
		}
		return rkNone
	case pt.kind == kBool:
		if vt.isArith() || vt.isPtr() || vt.kind == kNullptr {
			if vt.enum != nil && vt.enum.scoped {
				return rkNone
			}
			if vt.isPtr() || vt.kind == kNullptr {
				return rkConv + 3 // a pointer to bool: after its other conversions
			}
			return rkConv
		}
		return rkNone
	case pt.isArith() && vt.isArith():
		if vt.enum != nil && vt.enum.scoped || pt.enum != nil {
			return rkNone // no conversion from a scoped enumeration, or to an enumeration
		}
		if vt.isInteger() && pt.isInteger() && sameType(promote(vt), pt) && (vt.rank() < 320 || vt.enum != nil) {
			return rkPromo
		}
		if vt.kind == kFloat && pt.kind == kDouble {
			return rkPromo
		}
		return rkConv
	}
	return rkNone
}

// An adlRef is an unqualified function name that is called: set are
// the functions ordinary lookup finds.
type adlRef struct {
	node
	name  string
	set   []*symbol
	targs []targ // explicit template arguments
	noADL bool   // the name is qualified
}

// A candidate is a function a call may be to, with the rank of each
// argument's conversion (the object's first).
type candidate struct {
	sym   *symbol
	ranks []int
	tmpl  bool // a function template's instance
}

// viable ranks a call of sym with the arguments args, on the object obj
// if sym is a member function.
func (p *parser) viable(sym *symbol, obj expr, args []expr) (candidate, bool) {
	if sym.tmpl != nil {
		// A function template: its instance for these arguments.
		inst := p.instantiateFor(sym, args, sym.p)
		if inst == nil {
			return candidate{}, false
		}
		c, ok := p.viable(inst, obj, args)
		c.tmpl = true
		return c, ok
	}
	ft := sym.t.fn
	c := candidate{sym: sym}
	params := ft.params
	objRank := rkExact
	if ft.recv != nil {
		params = params[1:]
		if obj != nil {
			oq, rq := obj.ty().q, ft.recv.elem.q
			if rq&oq != oq {
				return c, false
			}
			if rq != oq {
				objRank = rkQual
			}
		}
	}
	c.ranks = append(c.ranks, objRank)
	if len(args) > len(params) && !ft.variadic {
		return c, false
	}
	for i := len(args); i < len(params); i++ {
		k := i + len(ft.params) - len(params)
		if k >= len(ft.defaults) || ft.defaults[k].start == 0 {
			return c, false
		}
	}
	for i, a := range args {
		if i >= len(params) {
			c.ranks = append(c.ranks, rkEllipsis)
			continue
		}
		r := p.convRank(a, params[i])
		if r == rkNone {
			return c, false
		}
		c.ranks = append(c.ranks, r)
	}
	return c, true
}

// better reports whether a is a better candidate than b.
func better(a, b candidate) bool {
	strictly := false
	for i := range a.ranks {
		if a.ranks[i] > b.ranks[i] {
			return false
		}
		if a.ranks[i] < b.ranks[i] {
			strictly = true
		}
	}
	return strictly
}

// resolve chooses the function of set that a call with args (on obj, for
// member functions) is to: nil, with no error, if none is viable and
// quiet.
func (p *parser) resolve(set []*symbol, obj expr, args []expr, at pos, name string, quiet bool) *symbol {
	var cands []candidate
	for _, s := range set {
		if c, ok := p.viable(s, obj, args); ok {
			cands = append(cands, c)
		}
	}
	if len(cands) == 0 {
		if quiet {
			return nil
		}
		var ts []string
		for _, a := range args {
			ts = append(ts, a.ty().String())
		}
		var decls []string
		for _, s := range set {
			decls = append(decls, fmt.Sprintf("\n\t%s:%d: %s", s.p.file, s.p.line, s.t))
		}
		p.errorAt(at, "no matching function for call to %s(%s)%s", name, strings.Join(ts, ", "), strings.Join(decls, ""))
	}
	best := cands[0]
	for _, c := range cands[1:] {
		if better(c, best) || !better(best, c) && best.tmpl && !c.tmpl {
			best = c
		}
	}
	for _, c := range cands {
		if c.sym != best.sym && !better(best, c) {
			if c.tmpl {
				continue // a function that is not a template's is better; of two templates, the first
			}
			if quiet {
				return nil
			}
			p.errorAt(at, "call of overloaded %s is ambiguous:\n\t%s:%d: %s\n\t%s:%d: %s", name,
				best.sym.p.file, best.sym.p.line, best.sym.t, c.sym.p.file, c.sym.p.line, c.sym.t)
		}
	}
	return best.sym
}

// callArgs parses a call's arguments, after its "(", unconverted.
func (p *parser) callArgs() []expr { return p.exprList(")") }

// defaultArg parses parameter i's default argument again.
func (p *parser) defaultArg(ft *funcType, i int) expr {
	d := ft.defaults[i]
	save, saveSc, saveThis := p.pos, p.sc, p.this
	p.pos, p.sc, p.this = d.start, d.sc, nil
	e := p.assignment()
	p.pos, p.sc, p.this = save, saveSc, saveThis
	return e
}

// passArg is the argument a as passed to a parameter of type pt.
func (p *parser) passArg(a expr, pt *ctype, at pos) expr {
	if pt.kind == kStruct {
		return p.classArg(a, pt, at)
	}
	return p.initConvert(a, pt, a.at(), "argument")
}

// callFunc is the call of the function sym with args: on the object obj,
// if sym is a non-static member function.
func (p *parser) callFunc(sym *symbol, obj expr, args []expr, at pos) expr {
	ft := sym.t.fn
	var out []expr
	params := ft.params
	if ft.recv != nil {
		if obj == nil {
			p.errorAt(at, "call of the member function %s without an object", sym.src)
		}
		out = append(out, &cast{node{ft.recv, at}, p.baseAddr(p.addressOf(obj, at), ft.recv.elem, at)})
		params = params[1:]
	}
	for i, pt := range params {
		if i < len(args) {
			out = append(out, p.passArg(args[i], pt, at))
		} else {
			out = append(out, p.passArg(p.defaultArg(ft, i+len(ft.params)-len(params)), pt, at))
		}
	}
	for _, a := range args[min(len(args), len(params)):] {
		v := p.rvalue(a)
		out = append(out, p.convert(v, promoteArg(v.ty())))
	}
	sym.used = true
	p.use(sym)
	direct := p.direct
	p.direct = false
	if sym.virtual && !ft.dtor && ft.recv != nil && !direct && !p.exactObject(obj) {
		// A virtual call: of the function in the object's table.
		if slot, ok := classOf(ft.recv.elem).vslot[sym]; ok {
			var pre []stmt
			if !noEffects(out[0]) {
				tmp := p.newLocalOrStatic("this", out[0].ty().unqual(), at)
				tv := &varRef{node{tmp.t, at}, tmp}
				pre = []stmt{&exprStmt{&assign{node{tmp.t, at}, "=", tv, out[0], tmp.t}}}
				out[0] = tv
			}
			fn := p.virtualFn(out[0], slot, sym.t, at)
			return p.wrapPre(pre, p.callResult(&call{node{ft.ret.unqual(), at}, fn, ft, out}, ft, at))
		}
	}
	fn := &decay{node{ptrTo(sym.t), at}, &varRef{node{sym.t, at}, sym}}
	return p.callResult(&call{node{ft.ret.unqual(), at}, fn, ft, out}, ft, at)
}

// callResult is a call's value to C++: what a returned reference refers
// to, or a class's object.
func (p *parser) callResult(c *call, ft *funcType, at pos) expr {
	p.complete(ft.ret)
	if ft.ret.kind == kStruct && !ft.ret.rec.complete {
		p.errorAt(at, "calling a function returning an incomplete type")
	}
	if ft.ret.isRef() {
		c.t = ft.ret
		e := p.unref(c)
		if ft.ret.ref == 2 {
			p.xvalue(e)
		}
		return e
	}
	if ft.ret.kind == kStruct {
		return p.classResult(c, at)
	}
	return p.fenvMark(c)
}

// cxxCall types a call of x in C++, after its "(".
func (p *parser) cxxCall(x expr, at pos) expr {
	return p.cxxCallArgs(x, p.callArgs(), at)
}

// cxxCallArgs is the call of x with the arguments args.
func (p *parser) cxxCallArgs(x expr, args []expr, at pos) expr {
	switch x := x.(type) {
	case *adlRef:
		set := x.set
		seen := map[*symbol]bool{}
		for _, s := range set {
			seen[s] = true
		}
		for _, a := range args {
			if x.noADL {
				break
			}
			t := a.ty()
			for t.kind == kPtr || t.kind == kArray {
				t = t.elem
			}
			var sc *scope
			switch {
			case t.kind == kStruct:
				sc = t.rec.sc
			case t.enum != nil:
				sc = t.enum.sc
			}
			for sc != nil && sc.kind != skNamespace {
				sc = sc.parent
			}
			if sc == nil {
				continue
			}
			if s := sc.find(x.name); s != nil && s.kind == symFunc {
				for _, o := range funcSet(s) {
					if !seen[o] {
						seen[o] = true
						set = append(set, o)
					}
				}
			}
		}
		if len(set) == 0 {
			p.errorAt(at, "%q undeclared", x.name)
		}
		p.explicitT = x.targs
		sym := p.resolve(set, p.thisOrNil(set, at), args, at, x.name, false)
		p.explicitT = nil
		if sym.t.fn.recv != nil {
			return p.callFunc(sym, p.thisObject(at), args, at)
		}
		return p.callFunc(sym, nil, args, at)
	case *varRef:
		if x.sym.kind == symFunc {
			set := funcSet(x.sym)
			sym := p.resolve(set, p.thisOrNil(set, at), args, at, x.sym.src, false)
			if sym.t.fn.recv != nil {
				// A member function named within its class: on *this.
				p.direct = p.qualified[x]
				return p.callFunc(sym, p.thisObject(at), args, at)
			}
			return p.callFunc(sym, nil, args, at)
		}
	case *pmfCall:
		return p.callPMF(x, args, at)
	case *methodRef:
		p.explicitT = x.targs
		sym := p.resolve(x.set, x.obj, args, at, x.set[0].src, false)
		p.explicitT = nil
		if sym.t.fn.recv == nil {
			return p.callFunc(sym, nil, args, at)
		}
		p.direct = x.direct
		return p.callFunc(sym, x.obj, args, at)
	}
	if x.ty().kind == kStruct {
		// An object with operator().
		if e := p.memberCall(x, "operator()", args, at); e != nil {
			return e
		}
		p.errorAt(at, "no match for call to an object of type %s", x.ty())
	}
	fx := p.rvalue(x)
	t := fx.ty()
	if !t.isPtr() || t.elem.kind != kFunc {
		p.errorAt(at, "called object is not a function or function pointer")
	}
	ft := t.elem.fn
	if len(args) < len(ft.params) || len(args) > len(ft.params) && !ft.variadic {
		p.errorAt(at, "wrong number of arguments to function")
	}
	var out []expr
	for i, a := range args {
		if i < len(ft.params) {
			out = append(out, p.passArg(a, ft.params[i], at))
		} else {
			v := p.rvalue(a)
			out = append(out, p.convert(v, promoteArg(v.ty())))
		}
	}
	return p.callResult(&call{node{ft.ret.unqual(), at}, fx, ft, out}, ft, at)
}

// hasTemplate reports whether a set of functions has a function
// template.
func hasTemplate(set []*symbol) bool {
	for _, s := range set {
		if s.tmpl != nil {
			return true
		}
	}
	return false
}

// thisOrNil is *this, if the functions of set may be member functions
// called on it.
func (p *parser) thisOrNil(set []*symbol, at pos) expr {
	if p.this == nil || p.lambda != nil && p.lambda.classThis == nil {
		return nil
	}
	for _, s := range set {
		if s.sc != nil {
			sc := s.sc
			for sc.kind == skTemplate && sc.parent != nil {
				sc = sc.parent
			}
			if sc.kind == skClass {
				return p.thisObject(at)
			}
		}
	}
	return nil
}
