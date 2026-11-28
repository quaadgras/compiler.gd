// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import "strings"

// What C++ does with objects of class type (doc/gd/cxx.md):
// construction, temporaries and their destruction, passing and
// returning, conversions, overloaded operators, new and delete.

// A classPR is a class prvalue: not a value in the tree but how to
// initialize an object with it (C++17's meaning of one). It never
// reaches the generator: an object takes it, or the parser makes a
// temporary (materializePR).
type classPR struct {
	node
	build func(dst expr) expr
}

// A retSlot is the object a function returns, in its caller's memory: an
// lvalue, in a function that returns a class.
type retSlot struct {
	node
}

// A tempObj is a temporary with a destructor, destroyed when the full
// expression that made it ends.
type tempObj struct {
	sym  *symbol
	flag *symbol // set when it is constructed, if that is conditional
	// reused is set if sym was another full expression's temporary
	// (reuseTemp): the full expression makes it zero first, as a new
	// variable is. (A temporary whose construction the parser tried
	// and dropped is still destroyed: one that was never constructed
	// is zero, which destroys to nothing.)
	reused bool
}

const (
	mValue    = 1 << iota // value initialization: T(), T{}
	mImplicit             // a copy initialization: explicit constructors are not used
)

// copyBytes copies the object src to dst, of one type: its bytes.
func (p *parser) copyBytes(dst, src expr, at pos) expr {
	t := dst.ty()
	if t.kind == kArray {
		return &builtinCall{node{tVoid, at}, "cxx_copy", []expr{p.rvalue(dst), p.rvalue(src), p.intc(t.sizeof(), tULong, at)}}
	}
	if t.isRef() {
		return &assign{node{t, at}, "=", dst, src, t}
	}
	return &assign{node{t.unqual(), at}, "=", dst, p.rvalue(src), t}
}

// zeroObject zeroes the object dst.
func (p *parser) zeroObject(dst expr, at pos) expr {
	t := dst.ty()
	if t.isScalar() && !t.isRef() {
		return &assign{node{t.unqual(), at}, "=", dst, p.convertAt(p.intc(0, tInt, at), t.unqual(), at), t}
	}
	return &builtinCall{node{tVoid, at}, "cxx_zero", []expr{p.addressOf(dst, at), p.intc(t.sizeof(), tULong, at)}}
}

func (p *parser) nothing(at pos) expr {
	return &cast{node{tVoid, at}, p.intc(0, tInt, at)}
}

// eachElem is the statement that does what f makes for the object dst
// (and src, if not nil), or for each of its elements if it is an array.
func (p *parser) eachElem(dst, src expr, at pos, f func(d, s expr) expr) stmt {
	t := dst.ty()
	if t.kind != kArray {
		return &exprStmt{f(dst, src)}
	}
	i := p.newLocalOrStatic("i", tLong, at)
	iv := &varRef{node{tLong, at}, i}
	elem := func(a expr) expr {
		if a == nil {
			return nil
		}
		et := a.ty().elem
		return &deref{node{et, at}, &binary{node{ptrTo(et), at}, "+", p.rvalue(a), iv}}
	}
	body := p.eachElem(elem(dst), elem(src), at, f)
	return &forStmt{
		init: &exprStmt{&assign{node{tLong, at}, "=", iv, p.intc(0, tLong, at), tLong}},
		c:    &binary{node{tInt, at}, "<", iv, p.intc(t.n, tLong, at)},
		post: &incDec{node{tLong, at}, "++", true, iv},
		body: body,
	}
}

// initScalar initializes the object dst, not of class type, with v.
func (p *parser) initScalar(dst, v expr, at pos) expr {
	t := dst.ty()
	if t.isRef() {
		return &assign{node{t, at}, "=", dst, p.bindRef(t, v, at, "initializer"), t}
	}
	return &assign{node{t.unqual(), at}, "=", dst, p.initConvert(v, t.unqual(), at, "initializer"), t}
}

// destroy is the statement that destroys the object obj: nil if its
// destruction does nothing.
func (p *parser) destroy(obj expr, at pos) stmt {
	if !needsDtor(obj.ty()) {
		return nil
	}
	return p.eachElem(obj, nil, at, func(d, _ expr) expr {
		ci := classOf(d.ty())
		dtor := ci.scope.vars["~"+ci.t.rec.tag]
		return p.callFunc(dtor, d, nil, at)
	})
}

// requireComplete reports an error if the class t is not defined.
func (p *parser) requireComplete(t *ctype, at pos) {
	p.complete(t)
	if t.kind == kStruct && !t.rec.complete {
		p.errorAt(at, "invalid use of the incomplete type %s", t)
	}
}

// ctors is the set of the constructors of the class t.
func ctors(t *ctype) []*symbol {
	ci := classOf(t)
	if ci == nil {
		return nil
	}
	if head := ci.scope.vars["{ctor}"]; head != nil {
		return funcSet(head)
	}
	return nil
}

// constructAt is the expression that initializes the object dst of the
// class t with args.
func (p *parser) constructAt(dst expr, t *ctype, args []expr, at pos, mode int) expr {
	t = t.unqual()
	p.requireComplete(t, at)
	if p.ctorDepth++; p.ctorDepth > 100 {
		p.ctorDepth = 0
		var ts []string
		for _, a := range args {
			ts = append(ts, a.ty().String())
		}
		p.errorAt(at, "the conversion to %s from (%s) does not end", t, strings.Join(ts, ", "))
	}
	defer func() { p.ctorDepth-- }()
	if len(args) == 1 {
		if pr, ok := args[0].(*classPR); ok && sameType(pr.t.unqual(), t) {
			return pr.build(dst) // constructed in place
		}
		if l, ok := args[0].(*initList); ok && mode&mImplicit != 0 {
			return p.listInit(dst, t, l, at) // T x = {...}, return {...}, f({...})
		}
	}
	set := ctors(t)
	if isPMF(t) && len(args) == 1 {
		return &assign{node{t, at}, "=", dst, p.rvalue(p.toMemPtr(args[0], t, at)), t}
	}
	if set == nil {
		// A struct that is being defined, or that has no class to it.
		switch {
		case len(args) == 0 && mode&mValue != 0:
			return p.zeroObject(dst, at)
		case len(args) == 0:
			return p.nothing(at)
		case len(args) == 1 && sameType(args[0].ty().unqual(), t):
			return &assign{node{t, at}, "=", dst, p.rvalue(args[0]), t}
		}
		p.errorAt(at, "no matching constructor for %s", t)
	}
	if mode&mImplicit != 0 {
		var impl []*symbol
		for _, s := range set {
			if !s.explicit {
				impl = append(impl, s)
			}
		}
		set = impl
	}
	if len(args) == 1 && args[0].ty().kind == kStruct && !sameType(args[0].ty().unqual(), t) && !isBaseOf(t, args[0].ty()) {
		// From another class: by a constructor, or by its conversion
		// function.
		p.userConv++
		direct := p.resolve(set, nil, args, at, t.String(), true)
		p.userConv--
		if direct == nil {
			if c := p.convertClass(args[0], t, at); c != nil {
				return p.constructAt(dst, t, []expr{c}, at, mode)
			}
		}
	}
	// A copy initialization from another type is a user-defined
	// conversion: its constructor's argument is not converted by
	// another.
	converting := mode&mImplicit != 0 && len(args) == 1 && !sameType(args[0].ty().unqual(), t) && !isBaseOf(t, args[0].ty())
	if converting {
		p.userConv++
	}
	ctor := p.resolve(set, nil, args, at, t.String(), false)
	if converting {
		p.userConv--
	}
	if ctor.deleted {
		p.errorAt(at, "use of a deleted constructor of %s", t)
	}
	if ctor.trivial {
		if len(args) == 0 {
			if mode&mValue != 0 {
				return p.zeroObject(dst, at)
			}
			return p.nothing(at)
		}
		// A copy of the bytes.
		src := args[0]
		if pr, ok := src.(*classPR); ok {
			src = p.materializePR(pr)
		}
		return &assign{node{t, at}, "=", dst, p.rvalue(p.baseObject(src, t, at)), t}
	}
	call := p.callFunc(ctor, dst, args, at)
	if mode&mValue != 0 && ctor.implicit && len(args) == 0 {
		return &binary{node{tVoid, at}, ",", p.zeroObject(dst, at), call}
	}
	return call
}

// construct is a prvalue of the class t, made of args: T(args).
func (p *parser) construct(t *ctype, args []expr, at pos, explicit bool) expr {
	t = t.unqual()
	p.requireComplete(t, at)
	if len(args) == 1 && sameType(t, args[0].ty().unqual()) {
		if _, ok := args[0].(*classPR); ok {
			return args[0]
		}
	}
	mode := mValue
	if !explicit {
		mode = mImplicit
	}
	return &classPR{node{t, at}, func(dst expr) expr { return p.constructAt(dst, t, args, at, mode) }}
}

// materializePR makes the temporary that the class prvalue pr
// initializes: an lvalue in the tree, an rvalue to C++, destroyed when
// the full expression ends (or, bound to a reference that is declared,
// when the reference's scope does).
func (p *parser) materializePR(pr *classPR) expr {
	at := pr.p
	t := pr.t.unqual()
	var reused bool
	var sym *symbol
	if p.extend == nil && p.fn != nil && needsDtor(t) {
		sym, reused = p.reuseTemp(t)
	}
	if sym == nil {
		sym = p.temporary(t, at)
	}
	ref := &varRef{node{sym.t, at}, sym}
	init := pr.build(ref)
	if p.extend != nil && p.fn != nil {
		// Its lifetime is the reference's: declared with it.
		sym.ctor = init
		sym.dtor = p.destroy(ref, at)
		*p.extend = append(*p.extend, sym)
		return p.xvalue(ref)
	}
	pre := []stmt{&exprStmt{init}}
	if needsDtor(t) && p.fn != nil {
		tmp := &tempObj{sym: sym, reused: reused}
		if p.condDepth > 0 {
			tmp.flag = p.newLocal("live", tBool, at)
			fv := &varRef{node{tBool, at}, tmp.flag}
			pre = append(pre, &exprStmt{&assign{node{tBool, at}, "=", fv, p.intc(1, tBool, at), tBool}})
		}
		p.temps = append(p.temps, tmp)
	}
	return p.xvalue(&seqExpr{node{sym.t, at}, pre, ref})
}

// reuseTemp returns a temporary of the class t that a full expression
// of the function destroyed, if there is one, and takes it from the
// free ones.
func (p *parser) reuseTemp(t *ctype) (*symbol, bool) {
	free := p.fn.freeTemps
	for i := len(free) - 1; i >= 0; i-- {
		if s := free[i]; sameType(s.t, t) {
			p.fn.freeTemps = append(free[:i:i], free[i+1:]...)
			return s, true
		}
	}
	return nil, false
}

// fullExpr is e, a full expression, followed by the destruction of the
// temporaries made since there were mark of them; its value is e's.
func (p *parser) fullExpr(mark int, e expr) expr {
	if len(p.temps) <= mark {
		return e
	}
	temps := p.temps[mark:]
	p.temps = p.temps[:mark]
	at := e.at()
	var list []stmt
	for _, tmp := range temps {
		if tmp.reused {
			list = append(list, &exprStmt{p.zeroObject(&varRef{node{tmp.sym.t, at}, tmp.sym}, at)})
		}
		if tmp.flag != nil {
			fv := &varRef{node{tBool, at}, tmp.flag}
			list = append(list, &exprStmt{&assign{node{tBool, at}, "=", fv, p.intc(0, tBool, at), tBool}})
		}
	}
	t := e.ty()
	var last expr
	if t.kind == kVoid {
		list = append(list, &exprStmt{e})
	} else {
		r := p.newLocal("full", t.unqual(), at)
		rv := &varRef{node{r.t, at}, r}
		list = append(list, &exprStmt{&assign{node{r.t, at}, "=", rv, e, r.t}})
		last = rv
	}
	for i := len(temps) - 1; i >= 0; i-- {
		tmp := temps[i]
		d := p.destroy(&varRef{node{tmp.sym.t, at}, tmp.sym}, at)
		if tmp.flag != nil {
			d = &ifStmt{&varRef{node{tBool, at}, tmp.flag}, d, nil}
		}
		list = append(list, d)
		if p.fn != nil && tmp.sym.owner == p.fn {
			// Dead from here on: another full expression's
			// temporary of the class may be it.
			p.fn.freeTemps = append(p.fn.freeTemps, tmp.sym)
		}
	}
	p.stmtExprs++ // as a statement expression's
	p.stmtExprs--
	return &stmtExpr{node{t.unqual(), at}, &block{list: list}, last}
}

// classCond is c ? x : y where the result is a class prvalue: the
// operands are of one class and not both lvalues, or one converts to
// the other's class. It is nil for any other conditional.
func (p *parser) classCond(c, x, y expr, at pos) expr {
	xt, yt := x.ty(), y.ty()
	xs, ys := xt.kind == kStruct, yt.kind == kStruct
	if !xs && !ys || xt.kind == kVoid || yt.kind == kVoid || isPMF(xt) || isPMF(yt) {
		return nil
	}
	if _, ok := x.(*initList); ok {
		return nil
	}
	if _, ok := y.(*initList); ok {
		return nil
	}
	var t *ctype
	switch {
	case xs && ys && sameType(xt.unqual(), yt.unqual()):
		_, xp := x.(*classPR)
		_, yp := y.(*classPR)
		if !xp && !yp && (!nontrivialABI(xt.unqual()) || p.isLvalueCxx(x) && p.isLvalueCxx(y)) {
			return nil // an lvalue, or the bytes
		}
		t = xt.unqual()
	case xs && p.convRank(y, xt.unqual()) != rkNone:
		t = xt.unqual()
	case ys && p.convRank(x, yt.unqual()) != rkNone:
		t = yt.unqual()
	default:
		return nil
	}
	return &classPR{node{t, at}, func(dst expr) expr {
		// The temporaries of each are made only if it is chosen.
		p.condDepth++
		bx := p.constructAt(dst, t, []expr{x}, at, mImplicit)
		by := p.constructAt(dst, t, []expr{y}, at, mImplicit)
		p.condDepth--
		return &cond{node{tVoid, at}, c, bx, by}
	}}
}

// classTemp is a temporary of the class t initialized with e.
func (p *parser) classTemp(t *ctype, e expr, at pos, what string) expr {
	t = t.unqual()
	if pr, ok := e.(*classPR); ok && sameType(pr.t.unqual(), t) {
		return p.materializePR(pr)
	}
	return p.materializePR(&classPR{node{t, at}, func(dst expr) expr { return p.constructAt(dst, t, []expr{e}, at, mImplicit) }})
}

// classArg is a passed by value to a parameter of the class pt.
func (p *parser) classArg(a expr, pt *ctype, at pos) expr {
	t := pt.unqual()
	if _, ok := a.(*classPR); !ok && !nontrivialABI(t) && sameType(a.ty().unqual(), t) {
		return p.rvalue(a) // its bytes
	}
	return p.rvalue(p.classTemp(t, a, at, "argument"))
}

// classResult is the value of the call c, which returns a class.
func (p *parser) classResult(c *call, at pos) expr {
	t := c.ft.ret.unqual()
	if !nontrivialABI(t) {
		return c
	}
	// Constructed by the callee, in the object that takes it.
	return &classPR{node{t, at}, func(dst expr) expr {
		if p.u.callInto == nil {
			p.u.callInto = map[*call]expr{}
		}
		p.addressOf(dst, at) // in memory
		p.u.callInto[c] = dst
		return &cast{node{tVoid, at}, c}
	}}
}

// classReturn is the return statement of e from a function that returns
// the class rt.
func (p *parser) classReturn(e expr, rt *ctype, at pos, mark int) stmt {
	rt = rt.unqual()
	_, isPR := e.(*classPR)
	if !isPR && !nontrivialABI(rt) && sameType(e.ty().unqual(), rt) {
		return &returnStmt{p.fullExpr(mark, p.rvalue(e))}
	}
	var local *symbol
	if v, ok := e.(*varRef); ok && !v.sym.global && sameType(v.sym.t.unqual(), rt) {
		// A local that is returned is moved from, if it can be.
		p.xvalue(e)
		if !v.sym.isParam && v.sym.t.q&qVolatile == 0 && len(p.temps) == mark {
			local = v.sym
		}
	}
	slot := &retSlot{node{rt, at}}
	init := p.constructAt(slot, rt, []expr{e}, at, mImplicit)
	b := &block{list: []stmt{&exprStmt{p.fullExpr(mark, init)}, &returnStmt{}}}
	p.fn.rets = append(p.fn.rets, b)
	p.fn.retVars = append(p.fn.retVars, local)
	return b
}

// nrvo makes the local that every return statement of f returns, if
// there is one, the function's result itself: constructed in the
// caller's memory, neither copied nor destroyed (the named return value
// optimization, which C++ permits and compilers do).
func (p *parser) nrvo(f *funcDecl) {
	if len(f.rets) == 0 || f.retVars[0] == nil || !nontrivialABI(f.sym.t.fn.ret) {
		return
	}
	v := f.retVars[0]
	for _, o := range f.retVars {
		if o != v {
			return
		}
	}
	if v.t.alignof() > 8 {
		return
	}
	v.nrvo, v.dtor = true, nil
	for _, b := range f.rets {
		b.list = []stmt{&returnStmt{}}
	}
}

// classInit parses the initializer of the class object sym, if C++
// initializes it otherwise than C. It reports whether it did.
func (p *parser) classInit(sym *symbol, s specs, at pos) bool {
	t := sym.t
	if isPMF(t) && (p.is("=") && !p.isAt(1, "{") || p.is("(")) {
		// A pointer to member function: from &C::f, another, or nullptr.
		paren := p.next().text == "("
		e := p.initExpr()
		if paren {
			p.expect(")")
		}
		sym.init = &initializer{items: []initItem{{t: t, x: p.rvalue(p.toMemPtr(e, t, at))}}}
		sym.defined = true
		if sym.storage == sExtern {
			sym.storage = sAuto
		}
		return true
	}
	ci := classOf(t)
	if ci == nil || !ci.complete {
		return false
	}
	ref := &varRef{node{t, at}, sym}
	switch {
	case p.is("("):
		p.next()
		args := p.callArgs()
		sym.ctor = p.constructAt(ref, t, args, at, mValue)
	case p.is("{") || p.is("=") && p.isAt(1, "{"):
		if !ci.userCtor && (ci.nontrivCtor || ci.nontrivDtor || needsCopy(t)) {
			// An aggregate with members that constructors initialize:
			// member by member.
			p.accept("=")
			sym.ctor = p.listInit(ref, t, p.braceList(), at)
			break
		}
		if !ci.userCtor {
			// An aggregate: C's initializer.
			if p.is("{") {
				if sym.global {
					p.staticInit++
				}
				sym.init = p.initializer(sym)
				if sym.global {
					p.staticInit--
				}
				break
			}
			return false
		}
		p.accept("=")
		sym.ctor = p.listInit(ref, t, p.braceList(), at)
	case p.is("="):
		p.next()
		p.classInitFrom(sym, p.initExpr(), at)
	default:
		if s.storage == sExtern || !ci.nontrivCtor {
			return false
		}
		sym.ctor = p.constructAt(ref, t, nil, at, 0)
	}
	sym.defined = true
	if sym.storage == sExtern {
		sym.storage = sAuto
	}
	return true
}

// classInitFrom initializes the class object sym with e.
func (p *parser) classInitFrom(sym *symbol, e expr, at pos) {
	sym.ctor = p.constructAt(&varRef{node{sym.t, at}, sym}, sym.t, []expr{e}, at, mImplicit)
}

// finishVar ends the declaration of the variable sym: the temporaries
// its initializer made are destroyed, and a destructor is run when its
// scope is left.
func (p *parser) finishVar(sym *symbol, mark int, at pos) {
	if len(p.temps) > mark && p.fn != nil {
		switch {
		case sym.ctor != nil:
			sym.ctor = p.fullExpr(mark, sym.ctor)
		case sym.init != nil && sym.init.scalar != nil:
			sym.init.scalar = p.fullExpr(mark, sym.init.scalar)
		default:
			sym.ctor = p.fullExpr(mark, p.nothing(at))
		}
	}
	if sym.kind == symVar && !sym.global && !sym.isParam && needsDtor(sym.t) && sym.t.ref == 0 {
		sym.addr = true
		sym.dtor = p.destroy(&varRef{node{sym.t, at}, sym}, at)
	}
	if sym.kind == symVar && sym.global && p.fn != nil && sym.storage != sExtern {
		// A static local that is not initialized with constants is
		// initialized the first time its declaration is reached.
		ref := &varRef{node{sym.t, at}, sym}
		var init []stmt
		if sym.init != nil && sym.init.scalar != nil && !staticInit(sym.init.scalar) {
			init = append(init, &exprStmt{&assign{node{sym.t, at}, "=", ref, sym.init.scalar, sym.t}})
			sym.init = nil
		}
		if sym.ctor != nil {
			init = append(init, &exprStmt{sym.ctor})
			sym.ctor = nil
		}
		if len(init) > 0 {
			p.symN++
			guard := &symbol{name: "guard." + itoa(int64(p.symN)), kind: symVar, t: tBool, global: true, storage: sStatic, p: at, defined: true}
			if sym.staticIn != nil {
				guard.staticIn, guard.staticN = sym.staticIn, -1-sym.staticN
			}
			p.u.statics = append(p.u.statics, guard)
			gv := &varRef{node{tBool, at}, guard}
			init = append([]stmt{&exprStmt{&assign{node{tBool, at}, "=", gv, p.intc(1, tBool, at), tBool}}}, init...)
			sym.once = &ifStmt{&unary{node{tInt, at}, "!", gv}, &block{list: init}, nil}
		}
	}
}

// staticInit reports whether e can initialize an object of static
// duration before the program starts: it is made of constants and
// addresses of such objects.
func staticInit(e expr) bool {
	switch e := e.(type) {
	case *intConst, *floatConst, *complexConst, *strLit:
		return true
	case *cast:
		return staticInit(e.x)
	case *decay:
		return staticAddr(e.x)
	case *addrOf:
		return staticAddr(e.x)
	case *unary:
		return staticInit(e.x)
	case *binary:
		return e.op != "," && staticInit(e.x) && staticInit(e.y)
	case *cond:
		return staticInit(e.c) && staticInit(e.x) && staticInit(e.y)
	}
	return false
}

func staticAddr(e expr) bool {
	switch e := e.(type) {
	case *varRef:
		return e.sym.global || e.sym.kind == symFunc
	case *strLit:
		return true
	case *compoundLit:
		return e.sym.global
	case *member:
		return staticAddr(e.x)
	case *deref:
		return staticInit(e.x)
	}
	return false
}

// Conversions.

// convFuncs are the conversion functions of the class t.
func (p *parser) convFuncs(t, to *ctype) []*symbol {
	ci := classOf(t)
	if ci == nil {
		return nil
	}
	var out []*symbol
	for _, name := range sorted(ci.scope.vars) {
		s := ci.scope.vars[name]
		if s.kind != symFunc {
			continue
		}
		for _, o := range funcSet(s) {
			switch {
			case o.tmpl != nil && name == "operator " && to != nil:
				// A conversion function template: its instance for the
				// type that is wanted.
				tm := o.tmpl
				p.funcPattern(tm)
				if tm.convPat == nil {
					continue
				}
				bound, set := make([]targ, len(tm.params)), make([]bool, len(tm.params))
				want := to
				if want.isRef() {
					want = want.elem
				}
				if !p.unify(tm.convPat, want.unqual(), bound, set) {
					continue
				}
				// (The parameters that the type does not name have
				// their defaults: template <class U = T, class O, ...>
				// operator Ref<O>().)
				if targs, ok := p.deduceRest(tm, bound, set, o.p); ok {
					if inst := p.instFunc(tm, targs, o.p); inst != nil && inst.t.fn.conv != nil {
						out = append(out, inst)
					}
				}
			case o.tmpl == nil && o.t.fn.conv != nil:
				out = append(out, o)
			}
		}
	}
	for _, b := range ci.bases {
		out = append(out, p.convFuncs(b.t, to)...)
	}
	return out
}

// countSet is the number of leading parameters that are set.
func countSet(set []bool) int {
	n := 0
	for n < len(set) && set[n] {
		n++
	}
	return n
}

// convertClass is e, of class type, converted by a conversion function
// towards the type t that is not its class: nil if it has none that
// can be.
func (p *parser) convertClass(e expr, t *ctype, at pos) expr {
	p.complete(e.ty())
	best, bestRank := (*symbol)(nil), rkNone
	oq := e.ty().q
	target := t
	if target.isRef() {
		target = target.elem
	}
	for _, s := range p.convFuncs(e.ty(), t) {
		if rq := s.t.fn.recv.elem.q; rq&oq != oq {
			continue
		}
		ct := s.t.fn.conv
		if ct.isRef() {
			ct = ct.elem
		}
		r := rkNone
		switch {
		case sameType(ct.unqual(), target.unqual()):
			r = rkExact
		case target.kind != kStruct:
			r = p.convRank(&goVal{node{ct.unqual(), at}, ""}, target.unqual())
			if r != rkNone {
				r++
			}
		}
		if r != rkNone && (bestRank == rkNone || r < bestRank) {
			best, bestRank = s, r
		}
	}
	if best == nil {
		return nil
	}
	if pr, ok := e.(*classPR); ok {
		e = p.materializePR(pr)
	}
	return p.callFunc(best, e, nil, at)
}

// classConvRank ranks the conversion of a (of value type vt) to pt,
// where one of the types is a class.
func (p *parser) classConvRank(a expr, vt, pt *ctype) int {
	if vt.kind == kStruct && pt.kind == kStruct {
		if sameType(vt, pt) {
			return rkExact
		}
		if isBaseOf(pt, vt) {
			return rkConv
		}
	}
	if p.userConv > 0 {
		return rkNone // one user-defined conversion only
	}
	p.userConv++
	defer func() { p.userConv-- }()
	if pt.kind == kStruct {
		// A converting constructor.
		for _, s := range ctors(pt) {
			if s.explicit || s.deleted {
				continue
			}
			if _, ok := p.viable(s, nil, []expr{a}); ok {
				return rkUser
			}
		}
	}
	if vt.kind == kStruct {
		oq := a.ty().q
		for _, s := range p.convFuncs(vt, pt) {
			if rq := s.t.fn.recv.elem.q; rq&oq != oq {
				continue
			}
			if s.explicit {
				continue
			}
			ct := s.t.fn.conv
			if ct.isRef() {
				ct = ct.elem
			}
			if pt.kind == kStruct {
				if sameType(ct.unqual(), pt) {
					return rkUser
				}
				continue
			}
			if p.convRank(&goVal{node{ct.unqual(), a.at()}, ""}, pt) != rkNone {
				return rkUser
			}
		}
	}
	return rkNone
}

// Operators.

// overloadable reports whether an operand of type t makes an operator
// one that may be overloaded.
func overloadable(t *ctype) bool {
	return t.kind == kStruct || t.enum != nil
}

// operatorSets are the functions named name that an operator
// expression with the operands xs may call: its first operand's
// members, and the functions found by ordinary lookup and in the
// namespaces of the operands' types.
func (p *parser) operatorSets(name string, xs []expr) (members, others []*symbol) {
	if ci := classOf(xs[0].ty()); ci != nil && ci.complete {
		if s := ci.scope.find(name); s != nil && s.kind == symFunc {
			members = funcSet(s)
		}
	}
	seen := map[*symbol]bool{}
	add := func(s *symbol) {
		if s == nil || s.kind != symFunc {
			return
		}
		for _, o := range funcSet(s) {
			if !seen[o] && o.t.fn.recv == nil {
				seen[o] = true
				others = append(others, o)
			}
		}
	}
	// Not a member found from within a class: those are the first
	// operand's only.
	for s := p.sc; s != nil; s = s.parent {
		if s.kind == skClass {
			continue
		}
		if v := s.find(name); v != nil {
			add(v)
			break
		}
	}
	for _, x := range xs {
		var sc *scope
		t := x.ty()
		switch {
		case t.kind == kStruct:
			sc = t.rec.sc
		case t.enum != nil:
			sc = t.enum.sc
		}
		for sc != nil && sc.kind != skNamespace {
			sc = sc.parent
		}
		if sc != nil {
			add(sc.find(name))
		}
	}
	return
}

// overloaded is the call of the overloaded operator op with the
// operands xs: nil if no operator function takes them.
func (p *parser) overloaded(op string, xs []expr, at pos) expr {
	any := false
	for _, x := range xs {
		any = any || overloadable(x.ty())
	}
	if !any {
		return nil
	}
	for _, x := range xs {
		p.complete(x.ty())
	}
	name := "operator" + op
	members, others := p.operatorSets(name, xs)
	var cands []candidate
	for _, s := range members {
		if s.t.fn.recv == nil {
			continue
		}
		if c, ok := p.viable(s, xs[0], xs[1:]); ok {
			if ot := xs[0].ty(); ot.kind == kStruct && s.t.fn.recv.elem.kind == kStruct && ot.rec != s.t.fn.recv.elem.rec && len(c.ranks) > 0 && c.ranks[0] < rkConv {
				// A base's operator: the object is converted to the
				// base, as it is for a function that is no member
				// and takes the base.
				c.ranks[0] = rkConv
			}
			cands = append(cands, c)
		}
	}
	for _, s := range others {
		if c, ok := p.viable(s, nil, xs); ok {
			c.ranks = c.ranks[1:]
			cands = append(cands, c)
		}
	}
	if len(cands) == 0 {
		return nil
	}
	best := cands[0]
	for _, c := range cands[1:] {
		if better(c, best) {
			best = c
		}
	}
	for _, c := range cands {
		if c.sym != best.sym && !better(best, c) {
			p.errorAt(at, "use of overloaded %s is ambiguous:\n\t%s:%d: %s\n\t%s:%d: %s", name,
				best.sym.p.file, best.sym.p.line, best.sym.t, c.sym.p.file, c.sym.p.line, c.sym.t)
		}
	}
	if best.sym.deleted {
		p.errorAt(at, "use of the deleted %s", name)
	}
	class := false
	for _, x := range xs {
		class = class || x.ty().kind == kStruct
	}
	if !class {
		// Enumerations only: the built-in operator takes them as they
		// are, and is better than a function that converts one.
		for _, r := range best.ranks {
			if r > rkQual {
				return nil
			}
		}
	}
	if best.sym.t.fn.recv != nil {
		x := xs[0]
		if pr, ok := x.(*classPR); ok {
			x = p.materializePR(pr)
		}
		if best.sym.trivial && op == "=" {
			return p.classAssign(op, x, xs[1], at)
		}
		return p.callFunc(best.sym, x, xs[1:], at)
	}
	return p.callFunc(best.sym, nil, xs, at)
}

// classAssign is the assignment x op y, where x is of class type.
func (p *parser) classAssign(op string, x, y expr, at pos) expr {
	t := x.ty()
	p.complete(t)
	if isPMF(t) && op == "=" {
		return &assign{node{t.unqual(), at}, "=", x, p.rvalue(p.toMemPtr(y, t, at)), t}
	}
	ci := classOf(t)
	if pr, ok := x.(*classPR); ok {
		x = p.materializePR(pr)
	}
	if ci != nil && ci.complete && op == "=" {
		if head := ci.scope.vars["operator="]; head != nil {
			p.userConv++
			sym := p.resolve(funcSet(head), x, []expr{y}, at, "operator=", true)
			p.userConv--
			if sym == nil {
				// By a conversion to the class first.
				sym = p.resolve(funcSet(head), x, []expr{y}, at, "operator=", false)
			}
			if sym.deleted {
				p.errorAt(at, "use of the deleted operator= of %s", t)
			}
			if !sym.trivial {
				return p.callFunc(sym, x, []expr{y}, at)
			}
			// A copy of the bytes.
			if t.q&qConst != 0 {
				p.errorAt(at, "assignment of read-only location")
			}
			if isBaseOf(t, y.ty()) {
				y = p.baseObject(p.rvalue(y), t, at)
			} else if !sameType(y.ty().unqual(), t.unqual()) {
				y = p.classTemp(t, y, at, "assignment")
			} else if pr, ok := y.(*classPR); ok {
				y = p.materializePR(pr)
			}
			return &assign{node{t.unqual(), at}, "=", x, p.rvalue(y), t}
		}
	}
	if e := p.overloaded(op, []expr{x, y}, at); e != nil {
		return e
	}
	if ci == nil || !ci.complete {
		if op == "=" && sameType(y.ty().unqual(), t.unqual()) {
			return &assign{node{t.unqual(), at}, "=", x, p.rvalue(y), t}
		}
	}
	p.errorAt(at, "no match for operator%s (operand types are %s and %s)", op, t, y.ty())
	return nil
}

// boolOf is e, of class type, converted to bool where a condition is.
func (p *parser) boolOf(e expr, at pos) expr {
	if isPMF(e.ty()) {
		return p.rvalue(p.memPtrTruth(e, at))
	}
	c := p.convertClass(e, tBool, at)
	if c == nil {
		p.errorAt(at, "used %s where a scalar is required", e.ty())
	}
	return p.rvalue(c)
}

// new and delete.

// libcFunc is the C library's function name, declared if it is not.
func (p *parser) libcFunc(name string, at pos) expr {
	fs := p.fileScope()
	sym := fs.vars[name]
	if sym == nil || sym.kind != symFunc {
		sym = &symbol{name: name, src: name, kind: symFunc, t: libcType(name), global: true, linkage: 'e', p: at, cLink: true, sc: fs}
		if g := p.u.scope[name]; g != nil && g.kind == symFunc {
			sym = g
		} else {
			p.u.scope[name] = sym
			p.u.syms = append(p.u.syms, sym)
		}
		if fs.vars[name] == nil {
			fs.vars[name] = sym
		}
	}
	sym.used = true
	return &decay{node{ptrTo(sym.t), at}, &varRef{node{sym.t, at}, sym}}
}

// allocFuncs are the allocation or deallocation functions named name
// for objects of type t: its class's, else the program's.
func (p *parser) allocFuncs(name string, t *ctype) []*symbol {
	if ci := classOf(baseElem(t)); ci != nil && ci.complete {
		if s := ci.scope.find(name); s != nil && s.kind == symFunc {
			return funcSet(s)
		}
	}
	if s := p.fileScope().find(name); s != nil && s.kind == symFunc {
		return funcSet(s)
	}
	return nil
}

// newExpr parses a new expression, after "new".
func (p *parser) newExpr(at pos) expr {
	var placement []expr
	if p.is("(") {
		// Placement arguments, unless it is new (type): a type follows
		// arguments.
		depth, m := 0, 0
		for ; ; m++ {
			if p.isAt(m, "(") {
				depth++
			} else if p.isAt(m, ")") {
				depth--
				if depth == 0 {
					break
				}
			} else if p.peekAt(m).kind == tkEOF {
				p.errorAt(at, "unbalanced parentheses after new")
			}
		}
		if !p.isTypeStart(1) || p.isTypeStart(m+1) || p.isAt(m+1, "(") {
			p.next()
			placement = p.callArgs()
		}
	}
	// The type: a type specifier and pointers, then [n] for an array.
	var t *ctype
	paren := p.accept("(")
	if paren {
		t = p.typeName()
		p.expect(")")
	} else {
		p.exprType++
		t = p.specQualType()
		p.exprType--
		for p.is("*") {
			p.next()
			t = ptrTo(t)
			for p.is("const") || p.is("volatile") {
				p.next()
			}
		}
	}
	var count expr
	if !paren && p.accept("[") {
		count = p.convert(p.rvalue(p.expr()), tULong)
		p.expect("]")
		for p.accept("[") {
			n := p.constInt(p.conditional())
			p.expect("]")
			t = arrayOf(t, n)
		}
	}
	p.requireComplete(baseElem(t), at)
	size := expr(p.intc(t.sizeof(), tULong, at))
	var pre []stmt
	cookie := int64(0)
	if count != nil {
		if _, ok := p.foldInt(count); !ok {
			// Evaluated once.
			n := p.newLocalOrStatic("n", tULong, at)
			nv := &varRef{node{tULong, at}, n}
			pre = append(pre, &exprStmt{&assign{node{tULong, at}, "=", nv, count, tULong}})
			count = nv
		}
		size = &binary{node{tULong, at}, "*", count, size}
		if needsDtor(t) && len(placement) == 0 {
			// delete[] destroys as many elements as new[] made: their
			// number is kept in front of the array.
			cookie = arrayCookie(t)
			size = &binary{node{tULong, at}, "+", size, p.intc(cookie, tULong, at)}
		}
	}
	pt := ptrTo(t)
	// The memory: an allocation function of the program's, the memory
	// a placement gives, or the C library's.
	var mem expr
	opName := "operatornew"
	if count != nil {
		opName = "operatornew[]"
	}
	args := append([]expr{size}, placement...)
	// What is allocated with the number in front is bytes until the
	// elements are where they are: an allocation of elements would
	// have the first of them where the number is.
	mt := pt
	if cookie != 0 {
		mt = ptrTo(tChar)
	}
	if sym := p.resolve(p.allocFuncs(opName, t), nil, args, at, opName, true); sym != nil && !stdPlacementNew(sym, placement) {
		mem = &cast{node{mt, at}, p.rvalue(p.callFunc(sym, nil, args, at))}
	} else if len(placement) == 1 && p.rvalue(placement[0]).ty().isPtr() {
		mem = &cast{node{mt, at}, p.rvalue(placement[0])}
	} else if len(placement) == 0 {
		malloc := p.libcFunc("malloc", at)
		mem = &cast{node{mt, at}, &call{node{ptrTo(tVoid), at}, malloc, malloc.ty().elem.fn, []expr{size}}}
	} else {
		p.errorAt(at, "no matching %s", opName)
	}
	ptr := p.newLocalOrStatic("new", pt, at)
	pv := &varRef{node{pt, at}, ptr}
	if cookie != 0 {
		mem = &cast{node{pt, at}, p.bytesFrom(mem, cookie, at)}
	}
	pre = append(pre, &exprStmt{&assign{node{pt, at}, "=", pv, mem, pt}})
	if cookie != 0 {
		n := p.cookieOf(pv, at)
		pre = append(pre, &exprStmt{&assign{node{tULong, at}, "=", n, count, tULong}})
	}
	obj := &deref{node{t, at}, pv}
	// The initializer.
	var init stmt
	mark := len(p.temps)
	switch {
	case p.is("(") || p.is("{"):
		end := ")"
		if p.next().text == "{" {
			end = "}"
		}
		as := p.exprList(end)
		switch {
		case end == "}" && count == nil && t.kind == kStruct:
			init = &exprStmt{p.listInit(obj, t, &initList{node: node{tVoid, at}, args: as}, at)}
		case count != nil:
			// The elements given, then the rest value-initialized.
			var list []stmt
			for i, a := range as {
				elem := &deref{node{t, at}, &binary{node{ptrTo(t), at}, "+", pv, p.intc(int64(i), tLong, at)}}
				if t.kind == kStruct {
					list = append(list, &exprStmt{p.constructAt(elem, t, []expr{a}, at, mImplicit)})
				} else {
					list = append(list, &exprStmt{p.initScalar(elem, a, at)})
				}
			}
			if n, ok := p.foldInt(count); !ok || n > int64(len(as)) {
				list = append(list, p.eachNew(pv, t, count, at, mValue, int64(len(as))))
			}
			init = &block{list: list}
		case t.kind == kStruct:
			init = &exprStmt{p.constructAt(obj, t, as, at, mValue)}
		case len(as) == 0:
			init = &exprStmt{p.zeroObject(obj, at)}
		case len(as) == 1:
			init = &exprStmt{p.initScalar(obj, as[0], at)}
		default:
			p.errorAt(at, "too many initializers for a %s", t)
		}
	case count != nil:
		if needsCtor(t) {
			init = p.eachNew(pv, t, count, at, 0, 0)
		}
	case needsCtor(t):
		init = p.eachElem(obj, nil, at, func(d, _ expr) expr { return p.constructAt(d, d.ty(), nil, at, 0) })
	}
	if init != nil {
		if len(p.temps) > mark && p.fn != nil {
			init = &exprStmt{p.fullExpr(mark, &stmtExpr{node{tVoid, at}, &block{list: []stmt{init}}, nil})}
		}
		pre = append(pre, init)
	}
	return &seqExpr{node{pt, at}, pre, pv}
}

// newLocalOrStatic makes an unnamed variable: a local, or at file scope
// one of static duration.
func (p *parser) newLocalOrStatic(what string, t *ctype, at pos) *symbol {
	if p.fn != nil {
		return p.newLocal(what, t, at)
	}
	p.symN++
	sym := &symbol{name: what + "." + itoa(int64(p.symN)), kind: symVar, t: t, global: true, storage: sStatic, p: at, defined: true}
	p.u.statics = append(p.u.statics, sym)
	return sym
}

// eachNew initializes the count elements of type t at ptr, as new T[n]
// does.
func (p *parser) eachNew(ptr expr, t *ctype, count expr, at pos, mode int, start int64) stmt {
	n := p.newLocalOrStatic("n", tULong, at)
	i := p.newLocalOrStatic("i", tULong, at)
	nv, iv := &varRef{node{tULong, at}, n}, &varRef{node{tULong, at}, i}
	elem := &deref{node{t, at}, &binary{node{ptrTo(t), at}, "+", ptr, &cast{node{tLong, at}, iv}}}
	var body stmt
	if ci := elemClass(t); ci != nil && (ci.nontrivCtor || ci.userCtor) {
		body = p.eachElem(elem, nil, at, func(d, _ expr) expr { return p.constructAt(d, d.ty(), nil, at, mode) })
	} else if mode&mValue != 0 {
		body = &exprStmt{p.zeroObject(elem, at)}
	} else {
		return &emptyStmt{}
	}
	return &block{list: []stmt{
		&exprStmt{&assign{node{tULong, at}, "=", nv, count, tULong}},
		&forStmt{
			init: &exprStmt{&assign{node{tULong, at}, "=", iv, p.intc(start, tULong, at), tULong}},
			c:    &binary{node{tInt, at}, "<", iv, nv},
			post: &incDec{node{tULong, at}, "++", true, iv},
			body: body,
		},
	}}
}

// deleteExpr parses a delete expression, after "delete".
func (p *parser) deleteExpr(at pos) expr {
	array := false
	if p.is("[") && p.isAt(1, "]") {
		p.pos += 2
		array = true
	}
	x := p.castExpr()
	if x.ty().kind == kStruct {
		// By a conversion to a pointer.
		for _, s := range p.convFuncs(x.ty(), nil) {
			if s.t.fn.conv.isPtr() && s.t.fn.conv.ref == 0 {
				x = p.callFunc(s, x, nil, at)
				break
			}
		}
	}
	v := p.rvalue(x)
	pt := v.ty()
	if !pt.isPtr() {
		p.errorAt(at, "delete of a %s", pt)
	}
	t := pt.elem
	ptr := p.newLocalOrStatic("del", pt.unqual(), at)
	pv := &varRef{node{ptr.t, at}, ptr}
	var body []stmt
	virtual := false
	if t.kind != kVoid {
		p.requireComplete(baseElem(t), at)
		if ci := classOf(t); ci != nil && !array {
			if dtor := ci.scope.vars["~"+t.rec.tag]; dtor != nil && dtor.virtual {
				// The object's own class destroys and frees it.
				virtual = true
				slot := ci.vslot[dtor] + 1
				this := &cast{node{ptrTo(t.unqual()), at}, pv}
				body = append(body, &exprStmt{&call{node{tVoid, at}, p.virtualFn(this, slot, dtor.t, at), dtor.t.fn, []expr{this}}})
			}
		}
		if needsDtor(t) && !virtual {
			obj := &deref{node{t.unqual(), at}, &cast{node{ptrTo(t.unqual()), at}, pv}}
			if array {
				body = append(body, p.destroyArray(pv, t.unqual(), at))
			} else {
				body = append(body, p.destroy(obj, at))
			}
		}
	}
	if !virtual {
		mem := expr(pv)
		if array && t.kind != kVoid && needsDtor(t) {
			mem = p.bytesFrom(pv, -arrayCookie(t.unqual()), at)
		}
		body = append(body, p.deallocate(&cast{node{ptrTo(tVoid), at}, mem}, t, array, at))
	}
	list := []stmt{
		&exprStmt{&assign{node{ptr.t, at}, "=", pv, v, ptr.t}},
		&ifStmt{pv, &block{list: body}, nil},
	}
	return &stmtExpr{node{tVoid, at}, &block{list: list}, nil}
}

// deallocate is the statement that frees the memory at ptr (a void *)
// of an object of type t, as delete does: by a deallocation function of
// the program's, else the C library's.
func (p *parser) deallocate(ptr expr, t *ctype, array bool, at pos) stmt {
	opName := "operatordelete"
	if array {
		opName = "operatordelete[]"
	}
	if sym := p.resolve(p.allocFuncs(opName, t), nil, []expr{ptr}, at, opName, true); sym != nil {
		return &exprStmt{p.rvalueVoid(p.callFunc(sym, nil, []expr{ptr}, at))}
	}
	free := p.libcFunc("free", at)
	return &exprStmt{&call{node{tVoid, at}, free, free.ty().elem.fn, []expr{ptr}}}
}

// arrayCookie is the number of bytes that new[] allocates in front of an
// array of t's, whose elements have a destructor: for their number,
// which is in the 8 bytes before the array, and for the alignment of t.
func arrayCookie(t *ctype) int64 { return max(8, t.alignof()) }

// bytesFrom is the pointer n bytes from ptr: a char *.
func (p *parser) bytesFrom(ptr expr, n int64, at pos) expr {
	pc := ptrTo(tChar)
	return &binary{node{pc, at}, "+", &cast{node{pc, at}, ptr}, p.intc(n, tLong, at)}
}

// cookieOf is the number of elements that new[] kept in front of the
// array at ptr.
func (p *parser) cookieOf(ptr expr, at pos) expr {
	pn := ptrTo(tULong)
	return &deref{node{tULong, at}, &binary{node{pn, at}, "+", &cast{node{pn, at}, ptr}, p.intc(-1, tLong, at)}}
}

// destroyArray is the statement that destroys the objects of type t of
// the array that new[] made at ptr, the last first.
func (p *parser) destroyArray(ptr expr, t *ctype, at pos) stmt {
	i := p.newLocalOrStatic("i", tLong, at)
	iv := &varRef{node{tLong, at}, i}
	count := &cast{node{tLong, at}, p.rvalue(p.cookieOf(ptr, at))}
	elem := &deref{node{t, at}, &binary{node{ptrTo(t), at}, "+", &cast{node{ptrTo(t), at}, ptr}, iv}}
	return &forStmt{
		init: &exprStmt{&assign{node{tLong, at}, "=", iv, count, tLong}},
		c:    &binary{node{tInt, at}, ">", iv, p.intc(0, tLong, at)},
		body: &block{list: []stmt{&exprStmt{&incDec{node{tLong, at}, "--", false, iv}}, p.destroy(elem, at)}},
	}
}

// typeidExpr parses typeid's operand, after "typeid": the type_info of
// a type, or of an expression's static type (cmd/cc keeps no run-time
// type information: the dynamic type of an object is not known).
func (p *parser) typeidExpr(at pos) expr {
	p.expect("(")
	var t *ctype
	if p.typeArgAhead() {
		t = p.typeName()
	} else {
		e := p.expr()
		if pr, ok := e.(*classPR); ok {
			t = pr.t
		} else {
			t = e.ty()
		}
	}
	p.expect(")")
	if t.isRef() {
		t = t.elem
	}
	t = t.unqual()
	std := p.fileScope().vars["std"]
	var ti *symbol
	if std != nil && std.kind == symNamespace {
		ti = std.ns.vars["type_info"]
	}
	if ti == nil || ti.kind != symTypedef || ti.t.kind != kStruct {
		p.errorAt(at, "typeid needs <typeinfo>")
	}
	tt := ti.t.qualified(qConst)
	if hasTParam(t) {
		return &deref{node{tt, at}, &goVal{node{ptrTo(tt), at}, ""}}
	}
	name := "_ZTI" + rawType(t)
	if p.typeinfos == nil {
		p.typeinfos = map[string]*symbol{}
	}
	sym := p.typeinfos[name]
	if sym == nil {
		sym = &symbol{name: name, src: name, kind: symVar, t: ti.t, global: true, linkage: 'e', defined: true, comdat: true, p: at, addr: true}
		p.typeinfos[name] = sym
		p.u.syms = append(p.u.syms, sym)
		raw := rawType(t)
		str := &strLit{node{arrayOf(tChar.qualified(qConst), int64(len(raw)+1)), at}, raw}
		st := p.saveFunc()
		sym.ctor = p.constructAt(&varRef{node{sym.t, at}, sym}, ti.t, []expr{str}, at, 0)
		p.restoreFunc(st)
	}
	return &varRef{node{tt, at}, sym}
}

// stdPlacementNew reports whether sym is the placement allocation
// function of the language, operator new(size_t, void *), which a
// program may not replace and which returns its second argument: the
// object is made at that pointer, with no call, so that what is made in
// the bytes of a union is seen for what it is (thin.go's unionCast).
func stdPlacementNew(sym *symbol, placement []expr) bool {
	if len(placement) != 1 || sym.t == nil || sym.t.kind != kFunc || sym.t.fn == nil {
		return false
	}
	ps := sym.t.fn.params
	return len(ps) == 2 && !sym.t.fn.variadic && ps[1].kind == kPtr && ps[1].elem.kind == kVoid && sym.t.fn.ret.kind == kPtr && sym.t.fn.ret.elem.kind == kVoid
}
