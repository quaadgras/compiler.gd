// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package escape

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/logopt"
	"cmd/compile/internal/types"
	"fmt"
)

// Below we implement the methods for walking the AST and recording
// data flow edges. Note that because a sub-expression might have
// side-effects, it's important to always visit the entire AST.
//
// For example, write either:
//
//     if x {
//         e.discard(n.Left)
//     } else {
//         e.value(k, n.Left)
//     }
//
// or
//
//     if x {
//         k = e.discardHole()
//     }
//     e.value(k, n.Left)
//
// Do NOT write:
//
//    // BAD: possibly loses side-effects within n.Left
//    if !x {
//        e.value(k, n.Left)
//    }

// A location represents an abstract location that stores a Go
// variable.
type location struct {
	n         ir.Node  // represented variable or expression, if any
	curfn     *ir.Func // enclosing function
	edges     []edge   // incoming edges
	loopDepth int      // loopDepth at declaration

	// resultIndex records the tuple index (starting at 1) for
	// PPARAMOUT variables within their function's result type.
	// For non-PPARAMOUT variables it's 0.
	resultIndex int

	// derefs and walkgen are used during walkOne to track the
	// minimal dereferences from the walk root.
	derefs  int // >= -1
	walkgen uint32

	// dst and dstEdgeindex track the next immediate assignment
	// destination location during walkone, along with the index
	// of the edge pointing back to this location.
	dst        *location
	dstEdgeIdx int

	// queuedWalkAll is used by walkAll to track whether this location is
	// in its work queue.
	queuedWalkAll bool

	// queuedWalkOne is used by walkOne to track whether this location is
	// in its work queue. The value is the walkgen when this location was
	// last queued for walkOne, or 0 if it's not currently queued.
	queuedWalkOne uint32

	// attrs is a bitset of location attributes.
	attrs locAttr

	// paramEsc records the represented parameter's leak set.
	paramEsc leaks

	heap       bool          // finish: the location is in the heap
	local      uint8         // finish: how its object is local (localStatic, localTagged), if it is
	shares     ir.ParamShare // for a parameter: see ir.Func.ParamShares
	captured   bool          // has a closure captured this variable?
	stmt       int32         // the statement the location was made in, or a variable declared in (shares.go)
	rawgen     uint32        // walkOne: the walk that reached the location on a path with no read-only edge
	rawDerefs  int           // walkOne: the least dereferences of such a path
	dynRoot    *dynRoot      // the location is a container known at run time (shares.go)
	isDynRoot  bool          // the location was made as one, even if it is not one any more
	dyn        []*location   // the dynamic roots that the location, or what it leads to, flows to
	owners     []*location   // for the contents of tracked variables: those variables (shares.go)
	reassigned bool          // has this variable been reassigned?
	addrtaken  bool          // has this variable's address been taken?
	param      bool          // is this variable a parameter (ONAME of class ir.PPARAM)?
	paramOut   bool          // is this variable an out parameter (ONAME of class ir.PPARAMOUT)?

	// gd Phase G: assignedInBody reports whether this result
	// variable is assigned other than by a return statement (p =
	// x, by its function or a closure of it), after which the
	// function goes on: in a loop, it then holds what an earlier
	// iteration made while a later one runs. See phaseg.go.
	assignedInBody bool

	// gd escape-bits: addrHeld reports whether this location's
	// address flows into a location that holds it (see holdsAddr):
	// a variable, a closure, a range temporary, not just a callee's
	// parameter; or may be held in a temporary of a statement that
	// also passes it as a candidate argument (see noteAddrTaken).
	// Walk's wrap can only re-home an escape candidate whose address
	// the function keeps nowhere but in its box: a pointer to the
	// stack copy kept elsewhere would go stale when the wrap moves
	// the variable to the heap.
	addrHeld bool

	// transient marks the temporaries of teeHole, which forward a
	// value to other holes instead of holding it.
	transient bool

	// moveCand marks the contents of the tracked variables of which
	// one is sent on a channel and not used again; moveHeld, that
	// something else than those variables holds a pointer to those
	// contents (move.go).
	moveCand, moveHeld bool
	// retCand marks such contents that the function returns; deferred,
	// that a deferred call hands them over.
	retCand, deferred bool
	// later marks the temporaries of the hole of that name, which
	// forward a value some time later.
	later  bool
	heldBy string
	// kept marks a parameter whose pointer something keeps that is
	// not a call that checks (ir.Func.KeptChecked); keptCheck is, for
	// the dynamic root of a call's argument, the check that the call
	// can make (move.go).
	kept      bool
	keptCheck *keptCheck

	// escapedInGraph is set when src's address flowed to a location
	// that already escaped (the heap) while the graph was built: flow
	// marks src as escaping and records no edge. gd Phase G's walks
	// (phaseg.go), which can't see that flow, start from it.
	escapedInGraph bool
}

type locAttr uint16

const (
	// attrEscapes indicates whether the represented variable's address
	// escapes; that is, whether the variable must be heap allocated.
	attrEscapes locAttr = 1 << iota

	// attrPersists indicates whether the represented expression's
	// address outlives the statement; that is, whether its storage
	// cannot be immediately reused.
	attrPersists

	// attrMutates indicates whether pointers that are reachable from
	// this location may have their addressed memory mutated. This is
	// used to detect string->[]byte conversions that can be safely
	// optimized away.
	attrMutates

	// attrCalls indicates whether closures that are reachable from this
	// location may be called without tracking their results. This is
	// used to better optimize indirect closure calls.
	attrCalls

	// attrCandidateEscape indicates the value flows into an indirect
	// call whose callee's per-arg escape mask will decide at runtime
	// whether the value actually needs heap storage. At allocation
	// time, such values stay on the stack; walk wraps them with a
	// runtime.maybeEscape* helper that materialises to the heap only
	// when the mask bit is set. When a value also reaches a normal
	// attrEscapes location, attrEscapes wins and the value is
	// unconditionally heap-allocated — attrCandidateEscape is only
	// honoured when it's the sole escape path. See
	// doc/gd/escape-bits.md.
	attrCandidateEscape

	// attrShares indicates that the location's address may flow to
	// where another goroutine can reach it: the heap (a global, memory
	// stored in through a pointer, a channel, a go statement, a callee
	// that keeps it) or a location that does. A returned value does
	// not: the caller is the same goroutine. See shares.go.
	attrShares

	// attrSharesValue indicates that the value held at the location
	// may flow there: what it points to is shared, the location itself
	// need not be.
	attrSharesValue

	// attrSharesDeep indicates that something that is reached from
	// the location may flow there, however far it is: the location's
	// value, what that points to, and so on. An object that is local
	// has none of the three marks (shares.go).
	attrSharesDeep

	// attrReturned indicates that the location, or something that is
	// reached from it, may flow to a result of a function: what the
	// caller does with it is not known here.
	attrReturned

	// attrDynShares and attrDynSharesValue are attrShares and
	// attrSharesValue for the containers that are only known at run
	// time (dynamic roots, shares.go): the location's address, or its
	// value, may flow to one. For what is decided when compiling
	// (markUnshared) such a container is shared.
	attrDynShares
	attrDynSharesValue

	// attrReturnedOther indicates that the location, or something
	// that is reached from it, flows to a result of another function
	// than its own (callbits.go).
	attrReturnedOther
)

func (l *location) hasAttr(attr locAttr) bool { return l.attrs&attr != 0 }

// An edge represents an assignment edge between two Go variables.
type edge struct {
	src    *location
	derefs int // >= -1
	notes  *note

	// ro is set for an edge that only a string flows along: what a
	// string points to is never written, so who reaches the string
	// says nothing of who may write what it was loaded from (shares.go:
	// a value that is only read is not shared). The edge counts for
	// escaping and not for sharing.
	ro bool

	// share is set for an edge that is in the graph only to find what
	// other goroutines may reach (shares.go): one that the graph did
	// not have before, because its source escapes anyway. Only the
	// solver follows it.
	share bool
}

func (l *location) asHole() hole {
	return hole{dst: l}
}

// leak records that parameter l leaks to sink.
func (l *location) leakTo(gd *base.Invocation, sink *location, derefs int) {
	// If sink is a result parameter that doesn't escape (#44614)
	// and we can fit return bits into the escape analysis tag,
	// then record as a result leak.
	if !sink.hasAttr(attrEscapes) && sink.isName(ir.PPARAMOUT) && sink.curfn == l.curfn {
		ri := sink.resultIndex - 1
		if ri < numEscResults {
			// Leak to result parameter.
			l.paramEsc.AddResult(gd, ri, derefs)
			return
		}
	}

	// Otherwise, record as heap leak.
	l.paramEsc.AddHeap(gd, derefs)
}

func (l *location) isName(c ir.Class) bool {
	return l.n != nil && l.n.Op() == ir.ONAME && l.n.(*ir.Name).Class == c
}

// A hole represents a context for evaluation of a Go
// expression. E.g., when evaluating p in "x = **p", we'd have a hole
// with dst==x and derefs==2.
type hole struct {
	dst    *location
	derefs int // >= -1
	notes  *note

	// addrtaken indicates whether this context is taking the address of
	// the expression, independent of whether the address will actually
	// be stored into a variable.
	addrtaken bool

	// ro says that the expression is, or is a part of what gives, a
	// string: see edge.ro.
	ro bool
}

type note struct {
	next  *note
	where ir.Node
	why   string
}

func (k hole) note(gd *base.Invocation, where ir.Node, why string) hole {
	if where == nil || why == "" {
		gd.Fatalf("note: missing where/why")
	}
	if gd.Flag.LowerM >= 2 || logopt.Enabled() {
		k.notes = &note{
			next:  k.notes,
			where: where,
			why:   why,
		}
	}
	return k
}

func (k hole) shift(gd *base.Invocation, delta int) hole {
	k.derefs += delta
	if k.derefs < -1 {
		gd.Fatalf("derefs underflow: %v", k.derefs)
	}
	k.addrtaken = delta < 0
	return k
}

func (k hole) deref(gd *base.Invocation, where ir.Node, why string) hole {
	return k.shift(gd, 1).note(gd, where, why)
}
func (k hole) addr(gd *base.Invocation, where ir.Node, why string) hole {
	return k.shift(gd, -1).note(gd, where, why)
}

func (k hole) dotType(gd *base.Invocation, t *types.Type, where ir.Node, why string) hole {
	if !t.IsInterface() && !types.IsDirectIface(t) {
		k = k.shift(gd, 1)
	}
	return k.note(gd, where, why)
}

func (b *batch) flow(k hole, src *location) {
	if k.addrtaken {
		src.addrtaken = true
		b.noteAddrTaken(src, k.dst == &b.candidateLoc)
	}

	dst := k.dst
	if dst == &b.blankLoc {
		return
	}
	if dst == src && k.derefs >= 0 { // dst = dst, dst = *dst, ...
		return
	}
	if dst.hasAttr(attrEscapes) && k.derefs < 0 { // dst = &src
		if b.gd.Flag.LowerM >= 2 || logopt.Enabled() {
			pos := b.gd.FmtPos(src.n.Pos())
			if b.gd.Flag.LowerM >= 2 {
				fmt.Printf("%s: %v escapes to heap in %v:\n", pos, src.n, ir.FuncName(src.curfn))
			}
			explanation := b.explainFlow(pos, dst, src, k.derefs, k.notes, []*logopt.LoggedOpt{})
			if logopt.Enabled() {
				var e_curfn *ir.Func // TODO(mdempsky): Fix.
				logopt.LogOpt(b.gd, src.n.Pos(), "escapes", "escape", ir.FuncName(e_curfn), fmt.Sprintf("%v escapes to heap", src.n), explanation)
			}

		}
		src.attrs |= attrEscapes | attrPersists | attrMutates | attrCalls
		src.escapedInGraph = true
		// The edge is still needed to find, when the graph is solved,
		// whether another goroutine may reach src: dst may only learn
		// then that one may reach it (shares.go). For escaping, the
		// solver does not follow an edge from a location that escapes.
		if src.n != nil || src.curfn != nil {
			dst.edges = append(dst.edges, edge{src: src, derefs: k.derefs, notes: k.notes, share: true, ro: k.ro})
		}
		return
	}

	// TODO(mdempsky): Deduplicate edges?
	dst.edges = append(dst.edges, edge{src: src, derefs: k.derefs, notes: k.notes, ro: k.ro})
}

// holdsAddr reports whether root, whose walk found that l's address
// flows into it, keeps a pointer to l beyond a statement of l's
// function (see location.addrHeld): any location of that function
// but the pseudo-locations standing for the heap and for callees and
// teeHole's temporaries, which only forward values.
func (b *batch) holdsAddr(root, l *location) bool {
	switch root {
	case &b.heapLoc, &b.ownHeapLoc, &b.moveLoc, &b.checkLoc, &b.mutatorLoc, &b.calleeLoc, &b.blankLoc, &b.candidateLoc, &b.shareLoc, &b.convShareLoc:
		return false
	}
	// Locations of other functions in the batch, the parameters
	// and locals of a callee, only hold it for the call: what
	// outlives the call flows out through its results to the
	// caller's holes.
	return root.curfn == l.curfn && !root.transient
}

// An addrStmtKey identifies a location and a statement taking its
// address.
type addrStmtKey struct {
	loc  *location
	stmt int32
}

// noteAddrTaken records that the statement being analyzed takes
// src's address, as an argument of a dynamic call that routes it to
// the candidate location (see dynamicArgHole) or otherwise.
//
// gd escape-bits: order and walk evaluate parts of a statement into
// temporaries ahead of others, so a pointer to src computed elsewhere
// in the statement than a candidate argument &src can sit in a
// temporary while the wrap at the candidate argument's call moves src
// to the heap: use(src[:], f(&src)), use(id(&src), f(&src)). Such a
// src isn't a candidate (addrHeld). Pointers kept beyond a statement
// are in locations, which walkOne checks.
func (b *batch) noteAddrTaken(src *location, asCandidateArg bool) {
	if src.addrHeld || !isCandidateLoc(src.n) {
		return
	}
	bit := uint8(2)
	if asCandidateArg {
		bit = 1
	}
	key := addrStmtKey{src, b.curStmt}
	bits := b.addrStmts[key] | bit
	if bits == 3 {
		src.addrHeld = true
		return
	}
	if b.addrStmts == nil {
		b.addrStmts = make(map[addrStmtKey]uint8)
	}
	b.addrStmts[key] = bits
}

func (b *batch) heapHole() hole      { return b.heapLoc.asHole() }
func (b *batch) shareHole() hole     { return b.shareLoc.asHole() }
func (b *batch) convShareHole() hole { return b.convShareLoc.asHole() }
func (b *batch) ownHeapHole() hole   { return b.ownHeapLoc.asHole() }
func (b *batch) moveHole() hole      { return b.moveLoc.asHole() }
func (b *batch) checkHole() hole     { return b.checkLoc.asHole() }
func (b *batch) mutatorHole() hole   { return b.mutatorLoc.asHole() }
func (b *batch) calleeHole() hole    { return b.calleeLoc.asHole() }
func (b *batch) discardHole() hole   { return b.blankLoc.asHole() }
func (b *batch) candidateHole() hole { return b.candidateLoc.asHole() }

func (b *batch) oldLoc(n *ir.Name) *location {
	if n.Canonical().Opt == nil {
		b.gd.FatalfAt(n.Pos(), "%v has no location", n)
	}
	return n.Canonical().Opt.(*location)
}

func (e *escape) newLoc(n ir.Node, persists bool) *location {
	if e.curfn == nil {
		e.gd.Fatalf("e.curfn isn't set")
	}
	if n != nil && n.Type() != nil && n.Type().NotInHeap() {
		e.gd.ErrorfAt(n.Pos(), 0, "%v is incomplete (or unallocatable); stack allocation disallowed", n.Type())
	}

	if n != nil && n.Op() == ir.ONAME {
		if canon := n.(*ir.Name).Canonical(); n != canon {
			e.gd.FatalfAt(n.Pos(), "newLoc on non-canonical %v (canonical is %v)", n, canon)
		}
	}
	loc := &location{
		n:         n,
		curfn:     e.curfn,
		loopDepth: e.loopDepth,
	}
	if loc.isName(ir.PPARAM) {
		loc.param = true
	} else if loc.isName(ir.PPARAMOUT) {
		loc.paramOut = true
	}

	if persists {
		loc.attrs |= attrPersists
	}
	loc.stmt = e.curStmt
	e.allLocs = append(e.allLocs, loc)
	if n != nil {
		if n.Op() == ir.ONAME {
			n := n.(*ir.Name)
			if n.Class == ir.PPARAM && n.Curfn == nil {
				// ok; hidden parameter
			} else if n.Curfn != e.curfn {
				e.gd.FatalfAt(n.Pos(), "curfn mismatch: %v != %v for %v", n.Curfn, e.curfn, n)
			}

			if n.Opt != nil {
				e.gd.FatalfAt(n.Pos(), "%v already has a location", n)
			}
			n.Opt = loc
		}
	}
	return loc
}

// teeHole returns a new hole that flows into each hole of ks,
// similar to the Unix tee(1) command.
func (e *escape) teeHole(ks ...hole) hole {
	if len(ks) == 0 {
		return e.discardHole()
	}
	if len(ks) == 1 {
		return ks[0]
	}
	// TODO(mdempsky): Optimize if there's only one non-discard hole?

	// Given holes "l1 = _", "l2 = **_", "l3 = *_", ..., create a
	// new temporary location ltmp, wire it into place, and return
	// a hole for "ltmp = _".
	loc := e.newLoc(nil, false)
	loc.transient = true
	for _, k := range ks {
		// N.B., "p = &q" and "p = &tmp; tmp = q" are not
		// semantically equivalent. To combine holes like "l1
		// = _" and "l2 = &_", we'd need to wire them as "l1 =
		// *ltmp" and "l2 = ltmp" and return "ltmp = &_"
		// instead.
		if k.derefs < 0 {
			e.gd.Fatalf("teeHole: negative derefs")
		}

		e.flow(k, loc)
	}
	return loc.asHole()
}

// later returns a new hole that flows into k, but some time later.
// Its main effect is to prevent immediate reuse of temporary
// variables introduced during Order.
func (e *escape) later(k hole) hole {
	loc := e.newLoc(nil, true)
	loc.later = true
	e.flow(k, loc)
	return loc.asHole()
}

// Fmt is called from node printing to print information about escape analysis results.
func Fmt(n ir.Node) string {
	text := ""
	switch n.Esc() {
	case ir.EscUnknown:
		break

	case ir.EscHeap:
		text = "esc(h)"

	case ir.EscNone:
		text = "esc(no)"

	case ir.EscNever:
		text = "esc(N)"

	default:
		text = fmt.Sprintf("esc(%d)", n.Esc())
	}

	if n.Op() == ir.ONAME {
		n := n.(*ir.Name)
		if loc, ok := n.Opt.(*location); ok && loc.loopDepth != 0 {
			if text != "" {
				text += " "
			}
			text += fmt.Sprintf("ld(%d)", loc.loopDepth)
		}
	}

	return text
}
