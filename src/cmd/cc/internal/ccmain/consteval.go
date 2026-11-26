// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

// The calls of constexpr functions that are constants (C++ [expr.const]):
// what a constexpr function returns for constant arguments is computed
// here, so that a constexpr variable that it initializes is a constant
// and not a global that is set when the program starts. Godot's
// CowData::DATA_OFFSET is get_aligned_address(...): as a constant, the
// offset of each access to a CowData's header folds into the access.
//
// This is a part of what C++ allows in a constant expression: integers,
// and functions that are not members, whose bodies declare integer
// locals, assign them, and branch, loop, and return. Anything else is
// not evaluated, and the call stays a call, as before.

// constEvalSteps bounds the statements and expressions that the calls of
// one constant expression evaluate.
const constEvalSteps = 1 << 16

// constEvalDepth bounds the calls that are nested.
const constEvalDepth = 64

type constEval struct {
	p     *parser
	steps int
	depth int
}

// errConstEval is how the evaluation stops when what it meets is not a
// constant.
type errConstEval struct{}

// constFrame is the locals and parameters of a call.
type constFrame struct {
	vals map[*symbol]int64
	ret  int64
}

// stmt results.
const (
	ceNext = iota
	ceBreak
	ceContinue
	ceReturn
)

// foldConstCall is the value of c, a call of a constexpr function, if it
// is a constant.
func (p *parser) foldConstCall(c *call) (v int64, ok bool) {
	if !c.t.isInteger() || is128(c.t) {
		return 0, false
	}
	ev := &constEval{p: p}
	defer func() {
		if r := recover(); r != nil {
			if _, is := r.(errConstEval); !is {
				panic(r)
			}
			ok = false
		}
	}()
	return ev.call(c, nil), true
}

func (ev *constEval) fail() { panic(errConstEval{}) }

// constFnBody is the definition of the function that c calls, if it is a
// constexpr function that is not a member and is defined.
func constFnBody(c *call) *funcDecl {
	r, ok := c.fn.(*varRef)
	if !ok {
		if d, isDecay := c.fn.(*decay); isDecay {
			r, ok = d.x.(*varRef)
		}
	}
	if !ok || r.sym.kind != symFunc || !r.sym.constFn || r.sym.fn == nil || r.sym.fn.body == nil {
		return nil
	}
	if r.sym.t.fn.recv != nil || r.sym.t.fn.variadic {
		return nil
	}
	return r.sym.fn
}

func (ev *constEval) call(c *call, fr *constFrame) int64 {
	fd := constFnBody(c)
	if fd == nil || len(c.args) != len(fd.params) || ev.depth >= constEvalDepth {
		ev.fail()
	}
	args := make([]int64, len(c.args))
	for i, a := range c.args {
		args[i] = ev.expr(a, fr)
	}
	nf := &constFrame{vals: map[*symbol]int64{}}
	for i, ps := range fd.params {
		if ps == nil || !ps.t.isInteger() || is128(ps.t) {
			ev.fail()
		}
		nf.vals[ps] = wrap(ps.t, args[i])
	}
	ev.depth++
	if ev.stmt(fd.body, nf) != ceReturn {
		ev.fail()
	}
	ev.depth--
	return wrap(c.t, nf.ret)
}

func (ev *constEval) step() {
	ev.steps++
	if ev.steps > constEvalSteps {
		ev.fail()
	}
}

func (ev *constEval) stmt(s stmt, fr *constFrame) int {
	ev.step()
	switch s := s.(type) {
	case nil, *emptyStmt:
		return ceNext
	case *block:
		for _, x := range s.list {
			if r := ev.stmt(x, fr); r != ceNext {
				return r
			}
		}
		return ceNext
	case *exprStmt:
		ev.expr(s.x, fr)
		return ceNext
	case *declStmt:
		for _, sym := range s.syms {
			if sym.kind != symVar || sym.global || !sym.t.isInteger() || is128(sym.t) {
				ev.fail()
			}
			var v int64
			if sym.init != nil {
				if sym.init.scalar == nil {
					ev.fail()
				}
				v = ev.expr(sym.init.scalar, fr)
			}
			fr.vals[sym] = wrap(sym.t, v)
		}
		return ceNext
	case *returnStmt:
		if s.x == nil {
			ev.fail()
		}
		fr.ret = ev.expr(s.x, fr)
		return ceReturn
	case *ifStmt:
		if ev.expr(s.c, fr) != 0 {
			return ev.stmt(s.then, fr)
		}
		return ev.stmt(s.els, fr)
	case *whileStmt:
		for ev.expr(s.c, fr) != 0 {
			switch r := ev.stmt(s.body, fr); r {
			case ceBreak:
				return ceNext
			case ceReturn:
				return r
			}
		}
		return ceNext
	case *doStmt:
		for {
			switch r := ev.stmt(s.body, fr); r {
			case ceBreak:
				return ceNext
			case ceReturn:
				return r
			}
			if ev.expr(s.c, fr) == 0 {
				return ceNext
			}
		}
	case *forStmt:
		if r := ev.stmt(s.init, fr); r != ceNext {
			ev.fail()
		}
		for s.c == nil || ev.expr(s.c, fr) != 0 {
			switch r := ev.stmt(s.body, fr); r {
			case ceBreak:
				return ceNext
			case ceReturn:
				return r
			}
			if s.post != nil {
				ev.expr(s.post, fr)
			}
		}
		return ceNext
	case *breakStmt:
		if s.label != "" {
			ev.fail()
		}
		return ceBreak
	case *continueStmt:
		if s.label != "" {
			ev.fail()
		}
		return ceContinue
	}
	ev.fail()
	return 0
}

// local is the symbol of the local or parameter of the frame that e
// names, and nil if it names none.
func (ev *constEval) local(e expr, fr *constFrame) *symbol {
	r, ok := e.(*varRef)
	if !ok || fr == nil {
		return nil
	}
	if _, in := fr.vals[r.sym]; !in {
		return nil
	}
	return r.sym
}

func (ev *constEval) expr(e expr, fr *constFrame) int64 {
	ev.step()
	t := e.ty()
	if !t.isInteger() || is128(t) {
		ev.fail()
	}
	if s := ev.local(e, fr); s != nil {
		return fr.vals[s]
	}
	switch e := e.(type) {
	case *intConst:
		return e.v
	case *cast:
		if !e.x.ty().isInteger() {
			ev.fail()
		}
		return wrap(t, ev.expr(e.x, fr))
	case *unary:
		x := ev.expr(e.x, fr)
		switch e.op {
		case "-":
			return wrap(t, -x)
		case "~":
			return wrap(t, ^x)
		case "!":
			return b2i(x == 0)
		case "+":
			return x
		}
	case *binary:
		switch e.op {
		case "&&":
			return b2i(ev.expr(e.x, fr) != 0 && ev.expr(e.y, fr) != 0)
		case "||":
			return b2i(ev.expr(e.x, fr) != 0 || ev.expr(e.y, fr) != 0)
		case ",":
			ev.expr(e.x, fr)
			return ev.expr(e.y, fr)
		}
		if !e.y.ty().isInteger() {
			ev.fail()
		}
		x, y := ev.expr(e.x, fr), ev.expr(e.y, fr)
		v, ok := foldOp(e.op, e.x.ty(), x, y)
		if !ok {
			ev.fail()
		}
		return wrap(t, v)
	case *cond:
		if ev.expr(e.c, fr) != 0 {
			return wrap(t, ev.expr(e.x, fr))
		}
		return wrap(t, ev.expr(e.y, fr))
	case *assign:
		s := ev.local(e.x, fr)
		if s == nil || e.ct == nil || !e.ct.isInteger() {
			ev.fail()
		}
		y := ev.expr(e.y, fr)
		v := y
		if e.op != "=" {
			op := e.op[:len(e.op)-1]
			var ok bool
			v, ok = foldOp(op, e.ct, wrap(e.ct, fr.vals[s]), wrap(e.ct, y))
			if !ok {
				ev.fail()
			}
			v = wrap(e.ct, v)
		}
		fr.vals[s] = wrap(s.t, v)
		return fr.vals[s]
	case *incDec:
		s := ev.local(e.x, fr)
		if s == nil {
			ev.fail()
		}
		old := fr.vals[s]
		d := int64(1)
		if e.op == "--" {
			d = -1
		}
		fr.vals[s] = wrap(s.t, old+d)
		if e.post {
			return old
		}
		return fr.vals[s]
	case *call:
		return ev.call(e, fr)
	}
	// A constant that is not a literal: a constexpr variable, an
	// enumerator.
	if v, ok := ev.p.foldInt(e); ok {
		return v
	}
	ev.fail()
	return 0
}

// hasCall reports whether e, a constant expression, has a call in it:
// one that consteval.go evaluated.
func hasCall(e expr) bool {
	switch e := e.(type) {
	case *call:
		return true
	case *cast:
		return hasCall(e.x)
	case *unary:
		return hasCall(e.x)
	case *binary:
		return hasCall(e.x) || hasCall(e.y)
	case *cond:
		return hasCall(e.c) || hasCall(e.x) || hasCall(e.y)
	}
	return false
}
