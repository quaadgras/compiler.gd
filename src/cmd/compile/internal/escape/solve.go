// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package escape

import (
	"cmd/compile/internal/ir"
	"cmd/compile/internal/logopt"
	"cmd/internal/src"
	"fmt"
	"math/bits"
	"strings"
)

// walkAll computes the minimal dereferences between all pairs of
// locations.
func (b *batch) walkAll() {
	// We use a work queue to keep track of locations that we need
	// to visit, and repeatedly walk until we reach a fixed point.
	//
	// We walk once from each location (including the heap), and
	// then re-enqueue each location on its transition from
	// !persists->persists and !escapes->escapes, which can each
	// happen at most once. So we take Θ(len(e.allLocs)) walks.

	// Queue of locations to walk. Has enough room for b.allLocs
	// plus b.heapLoc, b.mutatorLoc, b.calleeLoc, and b.candidateLoc.
	todo := newQueue(len(b.allLocs) + 5)

	enqueue := func(loc *location) {
		if !loc.queuedWalkAll {
			loc.queuedWalkAll = true
			if loc.hasAttr(attrEscapes) {
				// Favor locations that escape to the heap,
				// which in some cases allows attrEscape to
				// propagate faster.
				todo.pushFront(loc)
			} else {
				todo.pushBack(loc)
			}
		}
	}

	b.settleDynRoots()
	for _, loc := range b.allLocs {
		todo.pushFront(loc)
		// TODO(thepudds): clean up setting queuedWalkAll.
		loc.queuedWalkAll = true
	}
	todo.pushFront(&b.mutatorLoc)
	todo.pushFront(&b.shareLoc)
	todo.pushFront(&b.convShareLoc)
	todo.pushFront(&b.calleeLoc)
	todo.pushFront(&b.candidateLoc)
	todo.pushFront(&b.ownHeapLoc)
	todo.pushFront(&b.moveLoc)
	todo.pushFront(&b.checkLoc)
	todo.pushFront(&b.recvLoc)
	todo.pushFront(&b.recvHeldLoc)
	todo.pushFront(&b.recvAnyLoc)
	todo.pushFront(&b.heapLoc)

	b.mutatorLoc.queuedWalkAll = true
	b.shareLoc.queuedWalkAll = true
	b.convShareLoc.queuedWalkAll = true
	b.calleeLoc.queuedWalkAll = true
	b.candidateLoc.queuedWalkAll = true
	b.heapLoc.queuedWalkAll = true
	b.ownHeapLoc.queuedWalkAll = true
	b.moveLoc.queuedWalkAll = true
	b.checkLoc.queuedWalkAll = true
	b.recvLoc.queuedWalkAll = true
	b.recvHeldLoc.queuedWalkAll = true
	b.recvAnyLoc.queuedWalkAll = true

	var walkgen uint32
	for todo.len() > 0 {
		root := todo.popFront()
		root.queuedWalkAll = false
		walkgen++
		b.walkOne(root, walkgen, enqueue)
	}
}

// walkOne computes the minimal number of dereferences from root to
// all other locations.
func (b *batch) walkOne(root *location, walkgen uint32, enqueue func(*location)) {
	// The data flow graph has negative edges (from addressing
	// operations), so we use the Bellman-Ford algorithm. However,
	// we don't have to worry about infinite negative cycles since
	// we bound intermediate dereference counts to 0.

	root.walkgen = walkgen
	root.derefs = 0
	root.dst = nil
	// The paths that have no read-only edge: sharing follows those
	// only (edge.ro).
	root.rawgen = walkgen
	root.rawDerefs = 0
	// The paths that have no edge from a receiver to its call's
	// result: who holds a pointer is found along those (edge.recvRes).
	root.plaingen = walkgen
	root.plainDerefs = 0

	if root.hasAttr(attrCalls) {
		if clo, ok := root.n.(*ir.ClosureExpr); ok {
			if fn := clo.Func; b.inMutualBatch(fn.Nname) && !fn.ClosureResultsLost() {
				fn.SetClosureResultsLost(true)

				// Re-flow from the closure's results, now that we're aware
				// we lost track of them.
				for _, result := range fn.Type().Results() {
					enqueue(b.oldLoc(result.Nname.(*ir.Name)))
				}
			}
		}
	}

	todo := newQueue(1)
	todo.pushFront(root)

	for todo.len() > 0 {
		l := todo.popFront()
		l.queuedWalkOne = 0 // no longer queued for walkOne

		derefs := l.derefs
		var newAttrs locAttr

		lplain := l.plaingen == walkgen
		if l.param && lplain && l.plainDerefs == 0 {
			// For the callers that would hand it over
			// (ir.Func.KeptChecked).
			if root == &b.recvHeldLoc {
				l.keptRecv = true
			} else if root == &b.recvAnyLoc {
				l.keptAny = true
			} else if !l.kept && b.keepsParam(root, l) {
				l.kept = true
			}
		}
		if l.param && l.moveCand && lplain && l.plainDerefs == 0 && !l.moveHeld && b.holdsMovedParam(root, l) {
			// root keeps the pointer of a parameter that the
			// function would hand over.
			l.moveHeld = true
			l.heldBy = b.explainLoc(root)
		}

		// If l.derefs < 0, then l's address flows to root.
		addressOf := derefs < 0
		if addressOf {
			// For a flow path like "root = &l; l = x",
			// l's address flows to root, but x's does
			// not. We recognize this by lower bounding
			// derefs at 0.
			derefs = 0

			// gd escape-bits: if root keeps a pointer to l, l
			// can't be an escape candidate (see addrHeld).
			if b.holdsAddr(root, l) {
				l.addrHeld = true
			}

			// If l's address flows somewhere that
			// outlives it, then l needs to be heap
			// allocated.
			if b.outlives(root, l) {
				if !l.hasAttr(attrEscapes) && (logopt.Enabled() || b.gd.Flag.LowerM >= 2) {
					if b.gd.Flag.LowerM >= 2 {
						fmt.Printf("%s: %v escapes to heap in %v:\n", b.gd.FmtPos(l.n.Pos()), l.n, ir.FuncName(l.curfn))
					}
					explanation := b.explainPath(root, l)
					if logopt.Enabled() {
						var e_curfn *ir.Func // TODO(mdempsky): Fix.
						logopt.LogOpt(b.gd, l.n.Pos(), "escape", "escape", ir.FuncName(e_curfn), fmt.Sprintf("%v escapes to heap", l.n), explanation)
					}
				}
				// gd escape-bits: when the sole outliving root is
				// a candidate-only location, propagate
				// attrCandidateEscape in place of attrEscapes.
				// See doc/gd/escape-bits.md.
				if root.hasAttr(attrCandidateEscape) && !root.hasAttr(attrEscapes) {
					newAttrs |= attrCandidateEscape | attrPersists | attrMutates | attrCalls
				} else {
					newAttrs |= attrEscapes | attrPersists | attrMutates | attrCalls
				}
			} else
			// If l's address flows to a persistent location, then l needs
			// to persist too.
			if root.hasAttr(attrPersists) {
				newAttrs |= attrPersists
			}
		}

		if derefs == 0 {
			newAttrs |= root.attrs & (attrMutates | attrCalls)
		}

		// For which goroutines reach l, only the paths without a
		// read-only edge count.
		lraw := l.rawgen == walkgen
		rawAddr := lraw && l.rawDerefs < 0
		rawDerefs := max(l.rawDerefs, 0)
		if lraw && rawDerefs == 0 {
			// What root holds, another goroutine may reach: l
			// itself if that is l's address, what l points to if
			// it is l's value.
			if root.attrs&(attrShares|attrSharesValue) != 0 {
				if rawAddr {
					newAttrs |= attrShares
				} else {
					newAttrs |= attrSharesValue
				}
			}
			if root.attrs&(attrDynShares|attrDynSharesValue) != 0 {
				if rawAddr {
					newAttrs |= attrDynShares
				} else {
					newAttrs |= attrDynSharesValue
				}
			}
		}

		// Whatever of l flows to root, and however deep, something
		// that is reached from l is where root is: an object is
		// local only if nothing that it leads to is shared, since
		// what is stored in it is allocated as it is (shares.go).
		if lraw && root.attrs&(attrShares|attrSharesValue|attrSharesDeep) != 0 {
			newAttrs |= attrSharesDeep
		}
		if lraw && (root.paramOut || root.hasAttr(attrReturned)) {
			newAttrs |= attrReturned
		}

		if b.gd.Flag.LowerM >= 2 && l.n != nil && newAttrs&^l.attrs&(attrShares|attrSharesValue|attrSharesDeep) != 0 {
			// Why an object is not local.
			fmt.Printf("%s: %v is shared by %s for %v with derefs=%d:\n", b.gd.FmtPos(l.n.Pos()), l.n, b.explainLoc(root), ir.FuncName(l.curfn), rawDerefs)
			b.explainPath(root, l)
		}

		// l's value flows to root. If l is a function
		// parameter and root is the heap or a
		// corresponding result parameter, then record
		// that value flow for tagging the function
		// later.
		if l.param {
			if b.outlives(root, l) {
				if !l.hasAttr(attrEscapes) && (logopt.Enabled() || b.gd.Flag.LowerM >= 2) {
					if b.gd.Flag.LowerM >= 2 {
						fmt.Printf("%s: parameter %v leaks to %s for %v with derefs=%d:\n", b.gd.FmtPos(l.n.Pos()), l.n, b.explainLoc(root), ir.FuncName(l.curfn), derefs)
					}
					explanation := b.explainPath(root, l)
					if logopt.Enabled() {
						var e_curfn *ir.Func // TODO(mdempsky): Fix.
						logopt.LogOpt(b.gd, l.n.Pos(), "leak", "escape", ir.FuncName(e_curfn),
							fmt.Sprintf("parameter %v leaks to %s with derefs=%d", l.n, b.explainLoc(root), derefs), explanation)
					}
				}
				l.leakTo(b.gd, root, derefs)
				b.noteParamLeak(l, root, derefs)
			}
			if lraw {
				b.noteParamShare(l, root, rawDerefs)
			}
			if root.hasAttr(attrMutates) {
				l.paramEsc.AddMutator(b.gd, derefs)
			}
			if root.hasAttr(attrCalls) {
				l.paramEsc.AddCallee(b.gd, derefs)
			}
		}

		if lraw && b.addDyn(l, root) {
			enqueue(l)
			for _, o := range l.owners {
				if b.addDyn(o, l) {
					enqueue(o)
				}
			}
		}

		if newAttrs&^l.attrs != 0 {
			l.attrs |= newAttrs
			enqueue(l)
			// What is loaded from the contents of a tracked
			// variable's objects is reached from those objects.
			if deep := l.attrs & (attrSharesDeep | attrReturned); deep != 0 {
				for _, o := range l.owners {
					if deep&^o.attrs != 0 {
						o.attrs |= deep
						enqueue(o)
					}
				}
			}
			if l.attrs&attrEscapes != 0 {
				continue
			}
		}

		for i, edge := range l.edges {
			if edge.recvOK && root == &b.recvHeldLoc || edge.recvAllOK && root == &b.recvAnyLoc {
				// No method of what flows here keeps it.
				continue
			}
			d := derefs + edge.derefs
			srcPlain := lplain && !edge.recvRes
			pd := max(l.plainDerefs, 0) + edge.derefs
			if srcPlain && pd < 0 && edge.src.moveCand && !edge.src.moveHeld && b.holdsMoved(root, edge.src) {
				// root keeps a pointer to an object that a send
				// would hand over: it is not handed over.
				edge.src.moveHeld = true
				edge.src.heldBy = b.explainLoc(root)
			}
			srcRaw := lraw && !edge.ro
			rd := rawDerefs + edge.derefs
			if edge.src.hasAttr(attrEscapes) && !srcRaw {
				// Nothing to learn: it escapes, and this path says
				// nothing of who reaches it.
				continue
			}
			if edge.src.hasAttr(attrEscapes) {
				// A location that escapes is a root itself, with
				// nothing left to learn here but whether another
				// goroutine may reach it or what it holds.
				var want locAttr
				if root.attrs&(attrShares|attrSharesValue|attrSharesDeep) != 0 {
					want |= attrSharesDeep
				}
				if root.attrs&(attrShares|attrSharesValue) != 0 {
					if rd < 0 {
						want |= attrShares
					} else if rd == 0 {
						want |= attrSharesValue
					}
				}
				if root.paramOut || root.hasAttr(attrReturned) {
					want |= attrReturned
				}
				if root.attrs&(attrDynShares|attrDynSharesValue) != 0 {
					if rd < 0 {
						want |= attrDynShares
					} else if rd == 0 {
						want |= attrDynSharesValue
					}
				}
				if want&^edge.src.attrs == 0 && !b.wantsDyn(edge.src, root) {
					continue
				}
			}
			closer := edge.src.walkgen != walkgen || edge.src.derefs > d
			if closer {
				edge.src.walkgen = walkgen
				edge.src.derefs = d
				edge.src.dst = l
				edge.src.dstEdgeIdx = i
			}
			if srcRaw && (edge.src.rawgen != walkgen || edge.src.rawDerefs > rd) {
				edge.src.rawgen = walkgen
				edge.src.rawDerefs = rd
				closer = true
			}
			if srcPlain && (edge.src.plaingen != walkgen || edge.src.plainDerefs > pd) {
				edge.src.plaingen = walkgen
				edge.src.plainDerefs = pd
				closer = true
			}
			if closer {
				// Check if already queued in todo.
				if edge.src.queuedWalkOne != walkgen {
					edge.src.queuedWalkOne = walkgen // Mark queued for this walkgen.

					// Place at the back to possibly give time for
					// other possible attribute changes to src.
					todo.pushBack(edge.src)
				}
			}
		}
	}
}

// explainPath prints an explanation of how src flows to the walk root.
func (b *batch) explainPath(root, src *location) []*logopt.LoggedOpt {
	visited := make(map[*location]bool)
	pos := b.gd.FmtPos(src.n.Pos())
	var explanation []*logopt.LoggedOpt
	for {
		// Prevent infinite loop.
		if visited[src] {
			if b.gd.Flag.LowerM >= 2 {
				fmt.Printf("%s:   warning: truncated explanation due to assignment cycle; see golang.org/issue/35518\n", pos)
			}
			break
		}
		visited[src] = true
		// src.dst points at the next hop toward root; root itself has
		// dst=nil (walkOne:79). When src == root (Phase F: a synthetic
		// param loc that gained attrCandidateEscape becomes its own
		// walkAll root, so outlives(root,root) is true and we still
		// enter the param branch), the flow is trivially empty.
		if src == root {
			break
		}
		dst := src.dst
		edge := &dst.edges[src.dstEdgeIdx]
		if edge.src != src {
			b.gd.Fatalf("path inconsistency: %v != %v", edge.src, src)
		}

		explanation = b.explainFlow(pos, dst, src, edge.derefs, edge.notes, explanation)

		if dst == root {
			break
		}
		src = dst
	}

	return explanation
}

func (b *batch) explainFlow(pos string, dst, srcloc *location, derefs int, notes *note, explanation []*logopt.LoggedOpt) []*logopt.LoggedOpt {
	ops := "&"
	if derefs >= 0 {
		ops = strings.Repeat("*", derefs)
	}
	print := b.gd.Flag.LowerM >= 2

	flow := fmt.Sprintf("   flow: %s ← %s%v:", b.explainLoc(dst), ops, b.explainLoc(srcloc))
	if print {
		fmt.Printf("%s:%s\n", pos, flow)
	}
	if logopt.Enabled() {
		var epos src.XPos
		if notes != nil {
			epos = notes.where.Pos()
		} else if srcloc != nil && srcloc.n != nil {
			epos = srcloc.n.Pos()
		}
		var e_curfn *ir.Func // TODO(mdempsky): Fix.
		explanation = append(explanation, logopt.NewLoggedOpt(epos, epos, "escflow", "escape", ir.FuncName(e_curfn), flow))
	}

	for note := notes; note != nil; note = note.next {
		if print {
			fmt.Printf("%s:     from %v (%v) at %s\n", pos, note.where, note.why, b.gd.FmtPos(note.where.Pos()))
		}
		if logopt.Enabled() {
			var e_curfn *ir.Func // TODO(mdempsky): Fix.
			notePos := note.where.Pos()
			explanation = append(explanation, logopt.NewLoggedOpt(notePos, notePos, "escflow", "escape", ir.FuncName(e_curfn),
				fmt.Sprintf("     from %v (%v)", note.where, note.why)))
		}
	}
	return explanation
}

func (b *batch) explainLoc(l *location) string {
	if l == &b.heapLoc || l == &b.ownHeapLoc || l == &b.moveLoc || l == &b.checkLoc || l == &b.recvLoc {
		return "{heap}"
	}
	if l == &b.recvHeldLoc || l == &b.recvAnyLoc {
		return "a method of its own"
	}
	if l.n == nil {
		// TODO(mdempsky): Omit entirely.
		return "{temp}"
	}
	if l.n.Op() == ir.ONAME {
		return fmt.Sprintf("%v", l.n)
	}
	return fmt.Sprintf("{storage for %v}", l.n)
}

// outlives reports whether values stored in l may survive beyond
// other's lifetime if stack allocated.
func (b *batch) outlives(l, other *location) bool {
	// The heap outlives everything.
	if l.hasAttr(attrEscapes) {
		return true
	}

	// candidateLoc also outlives the callee's frame — values flowing
	// there either materialize to heap or stay stack-bound at the
	// runtime wrap. Treat as outliving so the solver propagates
	// attrCandidateEscape in sync.
	if l.hasAttr(attrCandidateEscape) {
		return true
	}

	// Pseudo-locations that don't really exist.
	if l == &b.mutatorLoc || l == &b.calleeLoc || l == &b.shareLoc || l == &b.convShareLoc || l == &b.recvHeldLoc || l == &b.recvAnyLoc {
		return false
	}

	// We don't know what callers do with returned values, so
	// pessimistically we need to assume they flow to the heap and
	// outlive everything too.
	if l.paramOut {
		// Exception: Closures can return locations allocated outside of
		// them without forcing them to the heap, if we can statically
		// identify all call sites. For example:
		//
		//	var u int  // okay to stack allocate
		//	fn := func() *int { return &u }()
		//	*fn() = 42
		if ir.ContainsClosure(other.curfn, l.curfn) && !l.curfn.ClosureResultsLost() {
			return false
		}

		return true
	}

	// If l and other are within the same function, then l
	// outlives other if it was declared outside other's loop
	// scope. For example:
	//
	//	var l *int
	//	for {
	//		l = new(int) // must heap allocate: outlives for loop
	//	}
	if l.curfn == other.curfn && l.loopDepth < other.loopDepth {
		return true
	}

	// If other is declared within a child closure of where l is
	// declared, then l outlives it. For example:
	//
	//	var l *int
	//	func() {
	//		l = new(int) // must heap allocate: outlives call frame (if not inlined)
	//	}()
	if ir.ContainsClosure(l.curfn, other.curfn) {
		return true
	}

	return false
}

// queue implements a queue of locations for use in WalkAll and WalkOne.
// It supports pushing to front & back, and popping from front.
// TODO(thepudds): does cmd/compile have a deque or similar somewhere?
type queue struct {
	locs  []*location
	head  int // index of front element
	tail  int // next back element
	elems int
}

func newQueue(capacity int) *queue {
	capacity = max(capacity, 2)
	capacity = 1 << bits.Len64(uint64(capacity-1)) // round up to a power of 2
	return &queue{locs: make([]*location, capacity)}
}

// pushFront adds an element to the front of the queue.
func (q *queue) pushFront(loc *location) {
	if q.elems == len(q.locs) {
		q.grow()
	}
	q.head = q.wrap(q.head - 1)
	q.locs[q.head] = loc
	q.elems++
}

// pushBack adds an element to the back of the queue.
func (q *queue) pushBack(loc *location) {
	if q.elems == len(q.locs) {
		q.grow()
	}
	q.locs[q.tail] = loc
	q.tail = q.wrap(q.tail + 1)
	q.elems++
}

// popFront removes the front of the queue.
func (q *queue) popFront() *location {
	if q.elems == 0 {
		return nil
	}
	loc := q.locs[q.head]
	q.head = q.wrap(q.head + 1)
	q.elems--
	return loc
}

// grow doubles the capacity.
func (q *queue) grow() {
	newLocs := make([]*location, len(q.locs)*2)
	for i := range q.elems {
		// Copy over our elements in order.
		newLocs[i] = q.locs[q.wrap(q.head+i)]
	}
	q.locs = newLocs
	q.head = 0
	q.tail = q.elems
}

func (q *queue) len() int       { return q.elems }
func (q *queue) wrap(i int) int { return i & (len(q.locs) - 1) }
