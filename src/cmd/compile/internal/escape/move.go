// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package escape

import (
	"cmd/compile/internal/ir"
	"cmd/compile/internal/types"
)

// Handing over (doc/gd/sharing.md).
//
// A pointer sent on a channel is, to the rest of this package, in the
// hands of every goroutine: its object is shared from then on, and every
// store of a slice, a string or an interface in it takes a lock, though
// the goroutine that receives it is as a rule the only one that has it.
//
// A send hands its object over, and does not share it, if the sender
// provably has no way left to it:
//
//   - the value sent is a tracked variable (shares.go): every pointer it
//     ever holds is to an allocation of this function;
//   - neither it nor a variable that may hold the same pointer, or one
//     into the same object, is used after the send (unusedAfter);
//   - nothing else holds a pointer to the object: when the graph is
//     solved, the address of the variables' contents has flowed to those
//     variables only (holdsMoved). A callee's parameters hold it for the
//     call; what a callee keeps flows to the heap, to a result or to
//     another argument's object, which are other holders.
//
// What the object points to is shared by the send as before (the flow of
// the contents to shareLoc, and runtime.publishContents): the sender may
// well have other ways to that.
//
// If a send that was a candidate turns out not to hand over, it is an
// ordinary send, which shares the object when it runs. The variables say
// until then that the object is not shared, which is so: they are not
// used after the send.

// A moveSend is a send, or a call of (*sync.Pool).Put or of a function
// that consumes its argument (ir.Func.Consumes), that hands over the
// objects that x points to, unless c.moveHeld. c is the contents of x's
// class of tracked variables, or the location of the parameter whose
// pointer x holds: then the function only consumes that parameter, for
// its callers to say whether the object is theirs to hand over.
type moveSend struct {
	n     ir.Node
	x     *ir.Name
	c     *location
	arg   int      // for a call, the index of the argument
	param *ir.Name // the parameter, if x holds one's pointer
	fn    *ir.Func
}

// returned counts the return statements of a function with one result,
// and has the contents of those that return a tracked variable.
type returned struct {
	n  int
	cs []*location
}

// moveCandidate reports whether the send n may hand its object over.
func (e *escape) moveCandidate(n *ir.SendStmt) bool {
	return e.handOver(n, n.Value, []ir.Node{n.Chan}, 0, false)
}

// handOverHole is where a pointer that n may hand over flows: the heap,
// and what it points to, to where another goroutine reaches it.
func (e *escape) handOverHole(n ir.Node) hole {
	return e.teeHole(e.moveHole().note(e.gd, n, "handed over"), e.shareHole().deref(e.gd, n, "handed over (contents)"))
}

// handOver reports whether the statement n, which gives away the
// pointer val, may hand its object over: val is a tracked variable, or
// with params a variable that holds the pointer of a parameter, and no
// variable that may hold the same pointer is used once n has run. The
// statement also evaluates others.
func (e *escape) handOver(n, val ir.Node, others []ir.Node, arg int, params bool) bool {
	if e.gd.Debug.NoShare != 0 || e.gd.Debug.NoMove != 0 {
		return false
	}
	for val.Op() == ir.OCONVIFACE || val.Op() == ir.OCONVNOP || val.Op() == ir.OPAREN {
		switch v := val.(type) {
		case *ir.ConvExpr:
			val = v.X
		case *ir.ParenExpr:
			val = v.X
		}
	}
	x, ok := val.(*ir.Name)
	if !ok || x.Op() != ir.ONAME || !x.Type().IsPtr() {
		return false
	}
	// In the function that a defer statement calls, which only makes
	// the call n, the variable is the one of the function around it,
	// and n runs when that function ends.
	fn := e.curfn
	stmt := n
	wrap := e.deferWraps[fn]
	if wrap != nil {
		if x.Curfn != fn || !params {
			return false
		}
		if !x.IsClosureVar() {
			// A variable of an inlined call there, which holds
			// the pointer of one: nothing after n may use it.
			cv := wrap.alias[x]
			if cv == nil {
				return false
			}
			inner := make(map[*ir.Name]bool)
			inner[cv] = true
			for name, v := range wrap.alias {
				if v == cv {
					inner[name] = true
				}
			}
			if deferred, ok := unusedAfter(fn, n, inner); !ok || deferred {
				return false
			}
			x = cv
		}
		x = x.Canonical()
		fn = wrap.outer
		stmt = wrap.stmt.Call
	}
	if x.Curfn != fn || x.IsClosureVar() {
		return false
	}
	class := make(map[*ir.Name]bool)
	var c *location
	var param *ir.Name
	if c = e.contents[x]; c != nil {
		for name, nc := range e.contents {
			if nc == c {
				if name.Curfn != fn {
					return false
				}
				class[name] = true
			}
		}
	} else if param = e.paramClass[x]; param != nil && params {
		c = e.oldLoc(param)
		for name, p := range e.paramClass {
			if p == param {
				class[name] = true
			}
		}
	} else {
		return false
	}
	for _, other := range others {
		if refers(other, class) {
			return false
		}
	}
	deferred, ok := unusedAfter(fn, stmt, class)
	if !ok || deferred != (wrap != nil) {
		return false
	}
	if deferred {
		c.deferred = true
	}
	e.moves = append(e.moves, moveSend{n: n, x: x, c: c, arg: arg, param: param, fn: fn})
	c.moveCand = true
	return true
}

// A deferWrap is the function of a defer statement of outer that only
// calls a function with outer's variables as arguments: defer f(x).
type deferWrap struct {
	stmt  *ir.GoDeferStmt
	outer *ir.Func
	// alias has, if the call was inlined, the variables of the
	// inlined function that only hold the pointer of one of outer's
	// variables, and that variable as the wrapping function has it.
	alias map[*ir.Name]*ir.Name
}

// noteDeferWraps finds the defer statements of fn that only call a
// function with fn's variables.
func (b *batch) noteDeferWraps(fn *ir.Func) {
	if b.gd.Debug.NoShare != 0 || b.gd.Debug.NoMove != 0 {
		return
	}
	anyBody(fn, func(n ir.Node) bool {
		d, ok := n.(*ir.GoDeferStmt)
		if !ok || n.Op() != ir.ODEFER {
			return false
		}
		call, ok := d.Call.(*ir.CallExpr)
		if !ok || len(call.Args) != 0 {
			return false
		}
		clo, ok := call.Fun.(*ir.ClosureExpr)
		if !ok || clo.Func == nil || len(clo.Func.Body) != 1 {
			return false
		}
		if b.deferWraps[clo.Func] != nil {
			return false
		}
		w := &deferWrap{stmt: d, outer: fn}
		switch inner := clo.Func.Body[0].(type) {
		case *ir.CallExpr:
			if inner.Op() != ir.OCALLFUNC || len(inner.Init()) != 0 {
				return false
			}
			for _, arg := range inner.Args {
				for arg.Op() == ir.OCONVIFACE || arg.Op() == ir.OCONVNOP {
					arg = arg.(*ir.ConvExpr).X
				}
				switch arg.Op() {
				case ir.ONAME, ir.OLITERAL, ir.ONIL:
				default:
					return false
				}
			}
		case *ir.InlinedCallExpr:
			// The call was inlined: its variables that are only
			// assigned one of the wrapping function's, or one
			// another, hold that one's pointer. Nothing there
			// assigns to the wrapping function's own.
			if ir.Any(inner, func(n ir.Node) bool { return n.Op() == ir.OCLOSURE || n.Op() == ir.ODEFER || n.Op() == ir.OGO }) {
				return false
			}
			cand, srcs := b.pointerSources(clo.Func)
			w.alias = make(map[*ir.Name]*ir.Name)
			for changed := true; changed; {
				changed = false
				for name := range cand {
					if w.alias[name] != nil || len(srcs[name]) != 1 {
						continue
					}
					src, ok := srcs[name][0].(*ir.Name)
					if !ok || src.Op() != ir.ONAME || !name.Type().IsPtr() {
						continue
					}
					if src.IsClosureVar() && src.Curfn == clo.Func {
						w.alias[name] = src
						changed = true
					} else if a := w.alias[src]; a != nil {
						w.alias[name] = a
						changed = true
					}
				}
			}
		default:
			return false
		}
		if b.deferWraps == nil {
			b.deferWraps = make(map[*ir.Func]*deferWrap)
		}
		b.deferWraps[clo.Func] = w
		return false
	})
}

// deferWrapLoc reports whether root is the function value of a defer
// statement that only passes variables on (noteDeferWraps): it has
// them until it has run.
func (b *batch) deferWrapLoc(root *location) bool {
	clo, ok := root.n.(*ir.ClosureExpr)
	return ok && clo != nil && b.deferWraps[clo.Func] != nil
}

// poolPut reports whether the call, of fns, puts a variable in a
// sync.Pool and may hand its object over.
func (e *escape) poolPut(call *ir.CallExpr, fns []*ir.Name, recv ir.Node, args []ir.Node) bool {
	if len(fns) != 1 || len(args) != 1 || recv == nil || !isPoolMethod(fns[0], "Put") {
		return false
	}
	return e.handOver(call, args[0], []ir.Node{recv}, 1, true)
}

// consumedArg reports whether the call, of fns, gives its argument arg,
// the i'th with the receiver, to a function that consumes it, and may
// hand its object over.
func (e *escape) consumedArg(call *ir.CallExpr, fns []*ir.Name, i int, arg ir.Node) bool {
	if len(fns) != 1 || fns[0].Func == nil || i >= 16 || fns[0].Func.Consumes&(1<<i) == 0 || call.Op() != ir.OCALLFUNC || i >= len(call.Args) {
		return false
	}
	others := make([]ir.Node, 0, len(call.Args))
	others = append(others, call.Args[:i]...)
	others = append(others, call.Args[i+1:]...)
	return e.handOver(call, arg, others, i, true)
}

// noteReturn notes, for a function with one result that has no name,
// what n returns: if it is always a tracked variable whose objects
// nothing else holds, the caller is the only one that has what it gets
// (ir.Func.OwnedResult).
func (e *escape) noteReturn(n *ir.ReturnStmt) {
	fn := e.curfn
	if e.gd.Debug.NoShare != 0 || e.gd.Debug.NoMove != 0 || fn.Type().NumResults() != 1 || len(n.Results) != 1 {
		return
	}
	res := fn.Type().Result(0)
	if res.Nname == nil || !res.Type.IsPtr() || res.Sym != nil && !res.Sym.IsBlank() && !isAutoResult(res.Sym.Name) {
		return
	}
	if e.returns == nil {
		e.returns = make(map[*ir.Func]*returned)
	}
	r := e.returns[fn]
	if r == nil {
		r = new(returned)
		e.returns[fn] = r
	}
	r.n++
	if x, ok := n.Results[0].(*ir.Name); ok && x.Op() == ir.ONAME && x.Curfn == fn {
		if c := e.contents[x]; c != nil {
			c.retCand = true
			c.moveCand = true // its holders count
			r.cs = append(r.cs, c)
		}
	}
}

func isAutoResult(name string) bool {
	return len(name) >= 2 && name[0] == '~' && name[1] == 'r'
}

// trackParams finds, for each parameter of fn that is a pointer and is
// never assigned, the local variables that only ever hold that pointer,
// or one into its object (paramClass). It is called before fn's graph
// is built.
func (b *batch) trackParams(fn *ir.Func) {
	if b.gd.Debug.NoShare != 0 || b.gd.Debug.NoMove != 0 {
		return
	}
	params := make(map[*ir.Name]bool)
	for _, n := range fn.Dcl {
		if n.Class == ir.PPARAM && !n.IsClosureVar() && !n.Addrtaken() && n.Type().IsPtr() {
			params[n] = true
		}
	}
	if len(params) == 0 {
		return
	}
	cand, srcs := b.pointerSources(fn)
	drop := func(n ir.Node) {
		if name, ok := n.(*ir.Name); ok {
			delete(params, name)
		}
	}
	ir.VisitFuncAndClosures(fn, func(n ir.Node) {
		switch n := n.(type) {
		case *ir.AssignStmt:
			drop(n.X)
		case *ir.AssignListStmt:
			for _, l := range n.Lhs {
				drop(l)
			}
		case *ir.AssignOpStmt:
			drop(n.X)
		case *ir.RangeStmt:
			drop(n.Key)
			drop(n.Value)
		case *ir.ClosureExpr:
			for _, cv := range n.Func.ClosureVars {
				drop(cv.Canonical())
			}
		}
	})
	if len(params) == 0 {
		return
	}
	for p := range params {
		cand[p] = true
	}
	// Nothing is allocated for these: every source is a parameter, a
	// variable that holds one's pointer, or nil.
	dropUnfresh(cand, srcs, func(n ir.Node) bool { return false }, false)

	class := make(map[*ir.Name]*ir.Name)
	var find func(n *ir.Name) *ir.Name
	find = func(n *ir.Name) *ir.Name {
		if p, ok := class[n]; ok && p != n {
			r := find(p)
			class[n] = r
			return r
		}
		return n
	}
	for name := range cand {
		for _, src := range srcs[name] {
			pointerVars(src, func(v *ir.Name) {
				if cand[v] {
					class[find(name)] = find(v)
				}
			})
		}
	}
	// A class is a parameter's if it has exactly one.
	owner := make(map[*ir.Name]*ir.Name)
	mixed := make(map[*ir.Name]bool)
	for p := range params {
		if !cand[p] {
			continue
		}
		root := find(p)
		if owner[root] != nil {
			mixed[root] = true
		}
		owner[root] = p
	}
	if b.paramClass == nil {
		b.paramClass = make(map[*ir.Name]*ir.Name)
	}
	for name := range cand {
		root := find(name)
		if p := owner[root]; p != nil && !mixed[root] {
			b.paramClass[name] = p
		}
	}
}

// holdsMovedParam reports whether root, to which the pointer of the
// parameter at l flows, is another holder of it than the variables that
// hold that parameter's pointer.
func (b *batch) holdsMovedParam(root, l *location) bool {
	switch root {
	case l, &b.moveLoc, &b.mutatorLoc, &b.calleeLoc, &b.blankLoc:
		return false
	case &b.heapLoc, &b.ownHeapLoc, &b.candidateLoc, &b.shareLoc:
		return true
	}
	if root.transient || root.later || root.curfn != l.curfn {
		return false
	}
	if name, ok := root.n.(*ir.Name); ok && name != nil && root.n.Op() == ir.ONAME && l.n != nil && b.paramClass[name] == l.n {
		return false
	}
	return !b.deferWrapLoc(root)
}

func isPoolMethod(fn *ir.Name, name string) bool {
	sym := fn.Sym()
	return sym != nil && sym.Pkg != nil && sym.Pkg.Path == "sync" && sym.Name == "(*Pool)."+name
}

// poolGot reports whether n is what a sync.Pool returned, as a pointer
// (p.Get().(*T)), or the result of a function that returns such a
// pointer or an allocation of its own that nothing else holds
// (ir.Func.OwnedResult): an object that nobody else has, or that is
// shared.
func poolGot(n ir.Node) bool {
	for n.Op() == ir.OCONVNOP || n.Op() == ir.OPAREN {
		switch v := n.(type) {
		case *ir.ConvExpr:
			n = v.X
		case *ir.ParenExpr:
			n = v.X
		}
	}
	if call, ok := n.(*ir.CallExpr); ok && call.Op() == ir.OCALLFUNC && n.Type() != nil && n.Type().IsPtr() {
		fn := ir.StaticCalleeName(call.Fun)
		return fn != nil && fn.Func != nil && (fn.Func.OwnedResult || fn.Func.FreshResult)
	}
	if n.Op() != ir.ODOTTYPE && n.Op() != ir.ODOTTYPE2 {
		return false
	}
	ta := n.(*ir.TypeAssertExpr)
	if !ta.Type().IsPtr() {
		return false
	}
	call, ok := ta.X.(*ir.CallExpr)
	if !ok || call.Op() != ir.OCALLFUNC {
		return false
	}
	fn := ir.StaticCalleeName(call.Fun)
	return fn != nil && isPoolMethod(fn, "Get")
}

// markFresh notes whether fn does nothing but return the address of an
// object that it allocates (ir.Func.FreshResult).
func markFresh(fn *ir.Func) {
	if fn.Type().NumResults() != 1 || len(fn.Body) != 1 {
		return
	}
	ret, ok := fn.Body[0].(*ir.ReturnStmt)
	if !ok || len(ret.Results) != 1 || len(ret.Init()) != 0 {
		return
	}
	n := ret.Results[0]
	for n.Op() == ir.OCONVIFACE || n.Op() == ir.OCONVNOP || n.Op() == ir.OPAREN {
		switch v := n.(type) {
		case *ir.ConvExpr:
			n = v.X
		case *ir.ParenExpr:
			n = v.X
		}
	}
	switch n.Op() {
	case ir.ONEW, ir.OPTRLIT:
		fn.FreshResult = true
		fn.OwnedResult = true
	}
}

// holdsMoved reports whether root, to which the address of the contents
// c of a class of tracked variables flows, is another holder of their
// objects than those variables.
func (b *batch) holdsMoved(root, c *location) bool {
	switch root {
	case c, &b.moveLoc, &b.mutatorLoc, &b.calleeLoc, &b.blankLoc:
		// (The object may point to itself.)
		return false
	case &b.heapLoc, &b.ownHeapLoc, &b.candidateLoc, &b.shareLoc:
		return true
	}
	if root.transient || root.later || root.curfn != c.curfn {
		// A temporary that forwards; a callee's parameter or
		// variable, which has it for the call.
		return false
	}
	if name, ok := root.n.(*ir.Name); ok && name != nil && root.n.Op() == ir.ONAME && b.contents[name] == c {
		return false
	}
	if b.deferWrapLoc(root) {
		return false
	}
	if root.paramOut && c.retCand {
		// Returned: the caller has it then (noteReturn).
		return false
	}
	return true
}

// markMoves records the sends and the calls that hand over, the
// parameters that the functions consume and the results that are their
// callers' alone.
func (b *batch) markMoves() {
	for _, m := range b.moves {
		if m.c.retCand && m.c.deferred {
			// Returned, and handed over by a deferred call after
			// that: neither.
			m.c.moveHeld = true
		}
	}
	for _, m := range b.moves {
		if m.c.moveHeld {
			if b.gd.Flag.LowerM >= 3 {
				b.gd.WarnfAt(m.n.Pos(), "%v is not handed over: held by %s", m.x, m.c.heldBy)
			}
			continue
		}
		if m.param != nil {
			if i := paramBit(m.fn, m.param); i > 0 {
				m.fn.Consumes |= 1 << (i - 1)
			}
			continue
		}
		switch n := m.n.(type) {
		case *ir.SendStmt:
			n.Move = true
			if b.gd.Flag.LowerM != 0 {
				b.gd.WarnfAt(n.Pos(), "%v is handed over by the send", m.x)
			}
		case *ir.CallExpr:
			n.HandOver = m.arg + 1
			if b.gd.Flag.LowerM != 0 {
				b.gd.WarnfAt(n.Pos(), "%v is handed over to the pool", m.x)
			}
		}
	}
	for fn, r := range b.returns {
		if fn.FreshResult {
			continue
		}
		ok := r.n > 0 && len(r.cs) == r.n
		for _, c := range r.cs {
			if c.moveHeld || c.deferred {
				ok = false
			}
		}
		if ok {
			fn.OwnedResult = true
			if b.gd.Flag.LowerM >= 3 {
				b.gd.WarnfAt(fn.Pos(), "%v returns an object that is its caller's alone", fn)
			}
		}
	}
	if b.gd.Flag.LowerM >= 3 {
		seen := make(map[*ir.Func]bool)
		for _, m := range b.moves {
			if m.param != nil && m.fn.Consumes != 0 && !seen[m.fn] {
				seen[m.fn] = true
				b.gd.WarnfAt(m.fn.Pos(), "%v hands over its parameters %b", m.fn, m.fn.Consumes)
			}
		}
	}
}

// refers reports whether n mentions a variable of class.
func refers(n ir.Node, class map[*ir.Name]bool) bool {
	if n == nil {
		return false
	}
	return ir.Any(n, func(n ir.Node) bool {
		name, ok := n.(*ir.Name)
		return ok && n.Op() == ir.ONAME && (class[name] || name.IsClosureVar() && class[name.Canonical()])
	})
}

// unusedAfter reports whether no variable of class is used once the
// statement send of fn has run, in the syntax: no statement that may
// follow it, up to a return, mentions one. It is false whenever that is
// not plain.
//
// If send is the call of a defer statement, it runs when the function
// ends, after every statement: deferred is true, and what is asked is
// that no other deferred call has the variables, and that the statement
// is in no loop, which would defer it twice.
func unusedAfter(fn *ir.Func, send ir.Node, class map[*ir.Name]bool) (deferred, ok bool) {
	// A jump may lead anywhere, unless it is the return of an inlined
	// call, to the label that ends it; a deferred call runs after the
	// send.
	ends := make(map[*types.Sym]bool)
	anyBody(fn, func(n ir.Node) bool {
		if call, ok := n.(*ir.InlinedCallExpr); ok && len(call.Body) > 0 {
			if l, ok := call.Body[len(call.Body)-1].(*ir.LabelStmt); ok {
				ends[l.Label] = true
			}
		}
		return false
	})
	odd := anyBody(fn, func(n ir.Node) bool {
		switch n.Op() {
		case ir.OGOTO:
			return !ends[n.(*ir.BranchStmt).Label]
		case ir.OFALL:
			return true
		case ir.ODEFER, ir.OGO:
			if n.Op() == ir.ODEFER && n.(*ir.GoDeferStmt).Call == send {
				deferred = true
				return false
			}
			return refers(n, class)
		}
		return false
	})
	if odd {
		return false, false
	}

	declaredIn := func(body ir.Nodes) bool {
		if deferred {
			return false
		}
		n := 0
		for _, stmt := range body {
			ir.Any(stmt, func(x ir.Node) bool {
				if d, ok := x.(*ir.Decl); ok && x.Op() == ir.ODCL && class[d.X] {
					n++
				}
				return false
			})
		}
		return n == len(class)
	}

	// list reports whether send is in the list, whether what may run
	// after it there is free of the variables, and whether the function
	// returns before anything after the list runs.
	var list func(l ir.Nodes) (found, ok, done bool)
	stmt := func(n ir.Node) (found, ok, done bool) {
		if n == nil {
			return false, true, false
		}
		if n == send {
			return true, true, false
		}
		switch n := n.(type) {
		case *ir.GoDeferStmt:
			if deferred && n.Call == send {
				// What follows runs before it.
				return true, true, true
			}
		case *ir.BlockStmt:
			return list(n.List)
		case *ir.IfStmt:
			if found, ok, done := list(n.Body); found {
				return true, ok, done
			}
			return list(n.Else)
		case *ir.ForStmt:
			// The loop runs again: its variables are those of
			// one turn only if they are declared in its body.
			if found, ok, done := list(n.Body); found {
				if deferred {
					return true, false, false
				}
				return true, ok && (done || declaredIn(n.Body)), done
			}
		case *ir.RangeStmt:
			if found, ok, done := list(n.Body); found {
				if deferred {
					return true, false, false
				}
				return true, ok && (done || declaredIn(n.Body)), done
			}
		case *ir.SwitchStmt:
			for _, cas := range n.Cases {
				if found, ok, done := list(cas.Body); found {
					return true, ok, done
				}
			}
		case *ir.SelectStmt:
			for _, cas := range n.Cases {
				if found, ok, done := list(cas.Body); found {
					return true, ok, done
				}
			}
		case *ir.InlinedCallExpr:
			// (Its returns are jumps to its end.)
			if deferred {
				// A defer statement of an inlined function
				// runs when the function around it ends.
				return false, true, false
			}
			return list(n.Body)
		}
		return false, true, false
	}
	list = func(l ir.Nodes) (bool, bool, bool) {
		for i, n := range l {
			found, ok, done := stmt(n)
			if !found {
				continue
			}
			if !ok || done {
				return true, ok, done
			}
			for _, rest := range l[i+1:] {
				if refers(rest, class) {
					return true, false, false
				}
				if rest.Op() == ir.ORETURN {
					return true, true, true
				}
			}
			return true, true, false
		}
		return false, true, false
	}
	found, ok, _ := list(fn.Body)
	return deferred, found && ok
}

// anyBody is ir.Any for the statements of fn.
func anyBody(fn *ir.Func, cond func(ir.Node) bool) bool {
	for _, n := range fn.Body {
		if ir.Any(n, cond) {
			return true
		}
	}
	return false
}
