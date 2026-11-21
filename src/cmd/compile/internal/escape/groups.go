// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package escape

import (
	"fmt"
	"go/constant"

	"cmd/compile/internal/ir"
	"cmd/compile/internal/types"
	"cmd/internal/src"
)

// The structures of a call's arguments (doc/gd/sharing.md, deep.go).
//
// A function that stores in an object which it reaches from a parameter
// needs to know that every object which the argument reaches is local.
// The caller answers with a bit of the call, and for that it must know
// everything that the argument reaches: a group.
//
// A membership root is a location that things flow to only so that the
// solver records, on each location that flows to it, that it does
// (location.groups). The walk from it goes on past what escapes, so it
// finds what an object holds, and what that holds, as the walks that
// say what an object leads to do. There are five kinds:
//
//   - the group of an argument: the argument flows to it;
//   - the group of a function's results, for whether they are exactly
//     as local as the caller says (ir.Func.ResultExact);
//   - for a store through a variable (q.f = y) that is not tracked, one
//     that q flows to and one that y flows to: y is then in whatever
//     object q points to, which is one that a source of q is or holds,
//     so y is a member of every group that has a source of q;
//   - the dirt of a function: what flows to it is passed to something
//     that may store in it what nobody here knows.
//
// A group's condition is a dynamic root, which the argument that the
// function called stores in the group's structure flows to: it is local
// only if the group is.

// A strayStore is a store through a variable that is not tracked: what
// is stored flows to s.
type strayStore struct {
	fn   *ir.Func
	q, s *location
	// param is whether q is a parameter and the store is in what it
	// points to itself; deep, the parameters that q is derived from.
	param bool
	deep  uint16
}

// The membership roots of a function for its stores through variables
// that are not tracked: what the variables are, and what is stored,
// for those that are parameters or derived from them and for the
// others. Their walks stay in the function.
type storeRoots struct {
	q, s       *location
	qBad, sBad *location
}

// A groupCond is what a group needs to be local as a whole.
type groupCond struct {
	busy, done bool
	bad        bool
	why        string
	own        uint16 // the bits of the function's call that must be set
}

func (b *batch) memberRoot(fn *ir.Func) *location {
	r := &location{
		curfn:     fn,
		loopDepth: 1,
		dynRoot:   &dynRoot{group: true},
		marker:    true,
	}
	b.allLocs = append(b.allLocs, r)
	return r
}

// pointsToPointers reports whether a value of type t may point to
// memory that holds pointers: only such an argument has a structure
// that something may be stored in.
func pointsToPointers(t *types.Type) bool {
	if t == nil {
		return false
	}
	switch {
	case t.IsPtr(), t.IsSlice():
		return t.Elem().HasPointers()
	case t.IsString():
		return false
	}
	return t.HasPointers()
}

// dirtOf returns the dirt of the current function.
func (e *escape) dirtOf() *location {
	if e.dirt == nil {
		e.dirt = make(map[*ir.Func]*location)
	}
	d := e.dirt[e.curfn]
	if d == nil {
		d = e.memberRoot(e.curfn)
		d.dynRoot.dirt = true
		e.dirt[e.curfn] = d
	}
	return d
}

// dirtyHole returns k, or k and the dirt of the current function if arg
// has a structure: it is an argument of a call that may store in it
// what nobody here knows.
func (e *escape) dirtyHole(k hole, arg ir.Node) hole {
	if !e.deepOn() || k.dst == &e.blankLoc || !pointsToPointers(arg.Type()) {
		return k
	}
	return e.teeHole(k, e.dirtOf().asHole())
}

// calleeDirty returns the parameters of fn in whose structures it may
// store what its caller does not know.
func calleeDirty(fn *ir.Name) uint16 {
	if fn.Func == nil || len(fn.Func.ParamShares) == 0 {
		return ^uint16(0)
	}
	return fn.Func.DirtyInto
}

// argGroupsOf returns, for the arguments all of a direct call of fn, the
// receiver first, the membership root of each that needs one: a group,
// if fn stores in its structure or reads its bit for that, or the dirt.
func (e *escape) argGroupsOf(call *ir.CallExpr, fn *ir.Name, all []ir.Node) []*location {
	if !e.deepOn() || call.GoDefer {
		return nil
	}
	if pkg := e.curfn.Sym().Pkg; pkg == nil || ir.UncheckedPkg(pkg.Path) {
		return nil
	}
	var groups []*location
	set := func(j int, g *location) {
		if groups == nil {
			groups = make([]*location, len(all))
		}
		groups[j] = g
	}
	if e.inMutualBatch(fn) {
		// What it does is not known yet.
		for j, arg := range all {
			if pointsToPointers(arg.Type()) {
				set(j, e.dirtOf())
			}
		}
		return groups
	}
	dirty := calleeDirty(fn)
	var need uint16
	if fn.Func != nil {
		need = fn.Func.DeepBits
		for _, ps := range fn.Func.ParamShares {
			need |= ps.Deep
		}
	}
	for j, arg := range all {
		if j >= 16 || !pointsToPointers(arg.Type()) {
			if j >= 16 && pointsToPointers(arg.Type()) {
				set(j, e.dirtOf())
			}
			continue
		}
		switch {
		case dirty&(1<<j) != 0:
			set(j, e.dirtOf())
		case need&(1<<j) != 0 && e.deepMaskOf(arg) == 0:
			g := e.memberRoot(e.curfn)
			g.dynRoot.call, g.dynRoot.arg = call, j
			set(j, g)
		}
	}
	return groups
}

// varGroup returns the group of the variable name, of the current
// function, for a store through a path from it: everything that the
// variable reaches. It returns nil for a parameter or a variable that is
// derived from parameters, whose structure is the caller's, and in a
// package whose pointers the graph does not follow.
func (e *escape) varGroup(name *ir.Name) *location {
	if e.derived[name] != 0 || name.Class == ir.PPARAM {
		return nil
	}
	if pkg := e.curfn.Sym().Pkg; pkg == nil || ir.UncheckedPkg(pkg.Path) {
		return nil
	}
	if e.varGroups == nil {
		e.varGroups = make(map[*ir.Name]*location)
	}
	g := e.varGroups[name]
	if g == nil {
		g = e.memberRoot(e.curfn)
		g.dynRoot.name = name
		g.edges = append(g.edges, edge{src: e.oldLoc(name)})
		e.varGroups[name] = g
	}
	return g
}

// condRoot returns the condition of the group g.
func (e *escape) condRoot(g *location) *location {
	if c := g.dynRoot.condOf; c != nil {
		return c
	}
	c := &location{
		curfn:     g.curfn,
		loopDepth: 1,
		attrs:     attrDynShares,
		dynRoot:   &dynRoot{cond: g},
		isDynRoot: true,
	}
	e.allLocs = append(e.allLocs, c)
	g.dynRoot.condOf = c
	return c
}

// noteStray records a store, through a path from the variable name
// that is not tracked, of what flows to the hole that it returns with
// k.
func (e *escape) noteStray(k hole, name *ir.Name, plain bool) hole {
	if !e.deepOn() || k.dst == &e.blankLoc {
		return k
	}
	q := e.oldLoc(name)
	st := &strayStore{fn: e.curfn, q: q, s: &location{curfn: e.curfn, loopDepth: 1, marker: true}}
	e.allLocs = append(e.allLocs, st.s)
	st.param = plain && name.Class == ir.PPARAM && !name.IsClosureVar()
	st.deep = e.derived[name]
	e.strays = append(e.strays, st)

	if e.storeRoots == nil {
		e.storeRoots = make(map[*ir.Func]*storeRoots)
	}
	sr := e.storeRoots[e.curfn]
	if sr == nil {
		sr = new(storeRoots)
		for _, r := range []**location{&sr.q, &sr.s, &sr.qBad, &sr.sBad} {
			*r = e.memberRoot(e.curfn)
		}
		e.storeRoots[e.curfn] = sr
	}
	rq, rs := sr.q, sr.s
	if !st.param && st.deep == 0 {
		rq, rs = sr.qBad, sr.sBad
	}
	rq.edges = append(rq.edges, edge{src: q})
	rs.edges = append(rs.edges, edge{src: st.s})
	return e.teeHole(k, st.s.asHole())
}

// pathBase returns the variable that the path p starts from, whether p
// is that variable, and whether p is a path from a variable of the
// program at all (a global's structure is nothing that is local).
func (e *escape) pathBase(p ir.Node) (name *ir.Name, plain, global bool) {
	plain = true
	for {
		switch x := p.(type) {
		case *ir.ParenExpr:
			p = x.X
			continue
		case *ir.ConvExpr:
			if x.Op() == ir.OCONVNOP || x.Op() == ir.OCONVIFACE {
				p = x.X
				continue
			}
		case *ir.SelectorExpr:
			if x.Op() == ir.ODOT || x.Op() == ir.ODOTPTR {
				p, plain = x.X, false
				continue
			}
		case *ir.StarExpr:
			p, plain = x.X, false
			continue
		case *ir.IndexExpr:
			p, plain = x.X, false
			continue
		case *ir.SliceExpr:
			p = x.X
			continue
		case *ir.TypeAssertExpr:
			p = x.X
			continue
		case *ir.AddrExpr:
			if x.Op() == ir.OADDR {
				if q := addrThrough(x); q != nil {
					p, plain = q, false
					continue
				}
			}
		case *ir.Name:
			if x.Op() != ir.ONAME {
				return nil, false, false
			}
			switch x.Class {
			case ir.PEXTERN:
				return nil, false, true
			case ir.PAUTO, ir.PPARAM, ir.PPARAMOUT:
				return x.Canonical(), plain, false
			}
		}
		return nil, false, false
	}
}

// prepareGroups gives each function the group of its results. It is
// called when the graph is built.
func (b *batch) prepareGroups(fns []*ir.Func) {
	if !b.deepOn() {
		return
	}
	b.resGroups = make(map[*ir.Func]*location)
	for _, fn := range fns {
		var g *location
		for _, f := range fn.Type().Results() {
			if f.Nname == nil || !f.Type.HasPointers() {
				continue
			}
			if g == nil {
				g = b.memberRoot(fn)
				g.dynRoot.result = true
				b.resGroups[fn] = g
			}
			g.edges = append(g.edges, edge{src: b.oldLoc(f.Nname.(*ir.Name))})
		}
	}
}

func hasGroup(l, g *location) bool {
	for _, x := range l.groups {
		if x == g {
			return true
		}
	}
	return false
}

// addGroups records on l, which flows to root, the membership roots of
// root, and reports whether l changed. A root has the locations of its
// own function only: what comes from another function of the batch is
// the result of a call, which answers for it (ir.Func.ResultExact), or
// makes the root one that does not know all (dynRoot.crossed).
func addGroups(l, root *location) bool {
	changed := false
	add := func(g *location) {
		if g == l {
			return
		}
		if l.unknown {
			g.dynRoot.sawUnknown = true
			return
		}
		if l.curfn == g.curfn && !hasGroup(l, g) {
			l.groups = append(l.groups, g)
			changed = true
		}
	}
	if root.dynRoot != nil && root.dynRoot.group {
		add(root)
	}
	for _, g := range root.groups {
		add(g)
	}
	return changed
}

func wantsGroups(l, root *location) bool {
	want := func(g *location) bool {
		if g == l {
			return false
		}
		if l.unknown {
			return !g.dynRoot.sawUnknown
		}
		return l.curfn == g.curfn && !hasGroup(l, g)
	}
	if root.dynRoot != nil && root.dynRoot.group && want(root) {
		return true
	}
	for _, g := range root.groups {
		if want(g) {
			return true
		}
	}
	return false
}

// solveGroups finds, when the graph is solved, the members of the
// membership roots and what the functions say of their parameters'
// structures and of their results.
func (b *batch) solveGroups(fns []*ir.Func) {
	if !b.deepOn() {
		return
	}
	for _, l := range b.allLocs {
		for _, g := range l.groups {
			g.members = append(g.members, l)
		}
	}
	b.memberSets = make(map[*location]map[*location]bool)
	b.memberBad = make(map[*location]bool)

	// Whether a function's results are as local as it is told
	// depends on the functions of the batch whose results it
	// returns: all are, until one is found that is not.
	for _, fn := range fns {
		fn.ResultExact = true
	}
	for changed := true; changed; {
		changed = false
		b.conds = make(map[*location]*groupCond)
		for _, fn := range fns {
			if !fn.ResultExact {
				continue
			}
			if g := b.resGroups[fn]; g != nil {
				if c := b.cond(g); c.bad || c.own&^1 != 0 {
					fn.ResultExact = false
					changed = true
				}
			}
		}
	}
	b.conds = make(map[*location]*groupCond)

	// What each function may store in its parameters' structures that
	// its callers do not know.
	for _, fn := range fns {
		fn.DirtyInto = b.dirtyInto(fn)
		if b.gd.Flag.LowerM >= 4 {
			fmt.Printf("%s: %v may store what its caller does not know in the structures of its parameters %b; its results are exact: %v\n", b.gd.FmtPos(fn.Pos()), fn, fn.DirtyInto, fn.ResultExact)
		}
	}
}

// reachTo returns everything of l's function that flows to l, and l,
// and whether that is all: not if something flows to it from another
// function of the batch that is neither an argument nor a recorded
// result.
func (b *batch) reachTo(l *location) (map[*location]bool, bool) {
	if m := b.reach[l]; m != nil {
		return m, !b.reachCrossed[l]
	}
	m := map[*location]bool{l: true}
	crossed := false
	todo := []*location{l}
	for len(todo) > 0 {
		x := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		for _, e := range x.edges {
			if e.ro || m[e.src] {
				continue
			}
			if e.src.curfn != nil && e.src.curfn != l.curfn {
				if !x.param && x.callRes == nil {
					crossed = true
				}
				continue
			}
			m[e.src] = true
			todo = append(todo, e.src)
		}
	}
	if b.reach == nil {
		b.reach = make(map[*location]map[*location]bool)
		b.reachCrossed = make(map[*location]bool)
	}
	b.reach[l] = m
	b.reachCrossed[l] = crossed
	return m, !crossed
}

// membersOf returns everything that the group g reaches: what flows to
// it, and what its function stores, through a variable that is not
// tracked, in an object that a member is or holds.
func (b *batch) membersOf(g *location) map[*location]bool {
	if m := b.memberSets[g]; m != nil {
		return m
	}
	m := make(map[*location]bool, len(g.members))
	for _, l := range g.members {
		m[l] = true
	}
	var strays []*strayStore
	for _, st := range b.strays {
		if st.fn == g.curfn {
			strays = append(strays, st)
		}
	}
	in := make([]bool, len(strays))
	for changed := true; changed; {
		changed = false
		for i, st := range strays {
			if in[i] {
				continue
			}
			from, all := b.reachTo(st.q)
			hit := !all
			if !hit {
				for l := range from {
					if m[l] {
						hit = true
						break
					}
				}
			}
			if !hit {
				continue
			}
			in[i] = true
			changed = true
			stored, all := b.reachTo(st.s)
			if !all {
				b.memberBad[g] = true
			}
			for l := range stored {
				m[l] = true
			}
		}
	}
	b.memberSets[g] = m
	return m
}

// isAlloc reports whether l is the location of something that is
// allocated, and not of a variable or of a value on its way.
func isAlloc(l *location) bool {
	if l.n == nil {
		return false
	}
	switch l.n.Op() {
	case ir.ONAME:
		return false
	}
	return true
}

// cond returns the condition of the group g.
func (b *batch) cond(g *location) *groupCond {
	c := b.conds[g]
	if c == nil {
		c = new(groupCond)
		b.conds[g] = c
	}
	if c.done || c.busy {
		return c
	}
	c.busy = true
	fail := func(why string, l *location) {
		if !c.bad {
			c.bad = true
			c.why = why
			if l != nil && l.n != nil {
				c.why = fmt.Sprintf("%s: %v (%s)", why, l.n, b.gd.FmtPos(l.n.Pos()))
			}
		}
	}
	fn := g.curfn
	if b.lost[fn] {
		fail("the function stores through something that is not a variable", nil)
	}
	members := b.membersOf(g)
	if g.dynRoot.sawUnknown {
		fail("a pointer from where the graph does not follow", nil)
	}
	if g.dynRoot.crossed || b.memberBad[g] {
		fail("a value from a function of the batch that nothing records", nil)
	}
	for m := range members {
		if c.bad {
			break
		}
		if m.unknown {
			fail("a pointer from where the graph does not follow", nil)
			break
		}
		for _, x := range m.groups {
			if x.dynRoot.dirt {
				fail("passed to what may store in it", m)
			}
		}
		if m.hasAttr(attrShares | attrSharesValue | attrSharesDeep | attrReturnedOther) {
			fail("shared", m)
			break
		}
		if m.curfn != fn {
			fail("of another function", m)
			break
		}
		if m.callRes != nil {
			callee := m.callRes.fn
			switch {
			case callee == nil:
				fail("the result of a function that is only known when the program runs", m)
			case callee.Func == nil || !callee.Func.ResultExact:
				fail("the result of a function that does not return what is as local as it is told", m)
			}
		}
		if name, ok := m.n.(*ir.Name); ok && name.Op() == ir.ONAME && !g.dynRoot.result {
			// (What a function returns of its parameters, its
			// caller sees.)
			if m.param {
				k := paramBit(fn, name)
				if k == 0 || m.reassigned || m.addrtaken || m.captured {
					fail("a parameter that is assigned, or has no bit", m)
				} else {
					c.own |= 1 << k
				}
			} else if d := b.derived[name]; d != 0 {
				c.own |= d << 1
			}
		}
		for _, r := range m.dyn {
			d := r.dynRoot
			switch {
			case d == nil:
				fail("stored where it cannot tell", m)
			case d.cond != nil:
				if d.cond != g {
					c2 := b.cond(d.cond)
					if c2.bad {
						fail("stored in a structure that is not local: "+c2.why, m)
					}
					c.own |= c2.own
				}
			case d.link != "":
				// (Until a call of such a function passes its
				// bits, what it allocates and stores is shared.)
				fail("passed to a function that is only known when the program runs", m)
			case d.deep != 0 && r.curfn == fn:
				c.own |= d.deep << 1
			case d.plain && d.loc != nil && d.loc.param && r.curfn == fn && !d.loc.reassigned && !d.loc.captured && !d.loc.addrtaken:
				if k := paramBit(fn, d.name); k != 0 {
					c.own |= 1 << k
				} else {
					fail("stored through a parameter that has no bit", m)
				}
			default:
				fail("stored where only its statement can tell", m)
			}
		}
		if m.hasAttr(attrReturned) {
			c.own |= 1
		}
	}
	c.busy = false
	c.done = true
	if b.gd.Flag.LowerM >= 4 && !g.dynRoot.result {
		what, pos := "", fn.Pos()
		switch {
		case g.dynRoot.call != nil:
			what, pos = fmt.Sprintf("argument %d", g.dynRoot.arg), g.dynRoot.call.Pos()
		case g.dynRoot.name != nil:
			what, pos = fmt.Sprint(g.dynRoot.name), g.dynRoot.name.Pos()
		}
		if what != "" {
			if c.bad {
				fmt.Printf("%s: what %s reaches is not known to be local: %s\n", b.gd.FmtPos(pos), what, c.why)
			} else {
				fmt.Printf("%s: what %s reaches is local if the bits %b of the call of %v are set\n", b.gd.FmtPos(pos), what, c.own, fn)
			}
		}
	}
	return c
}

// dirtyInto returns the parameters of fn in whose structures it may
// store what its callers do not know.
func (b *batch) dirtyInto(fn *ir.Func) uint16 {
	if b.lost[fn] {
		return ^uint16(0)
	}
	var dirty uint16
	sr := b.storeRoots[fn]
	bad, badQ := false, false
	if sr != nil {
		for _, m := range sr.s.members {
			if !b.knownStored(fn, m) {
				bad = true
				break
			}
		}
		// Through a variable that is neither a parameter nor
		// derived from some, the function says nothing of what it
		// stores: only what is not an object of its own, or a
		// pointer from elsewhere, is harmless.
		if sr.s.dynRoot.sawUnknown || sr.s.dynRoot.crossed {
			bad = true
		}
		badQ = sr.sBad.dynRoot.sawUnknown || sr.sBad.dynRoot.crossed
		for _, m := range sr.sBad.members {
			if m.callRes != nil || isAlloc(m) && m.hasAttr(attrEscapes) {
				badQ = true
				break
			}
		}
	}
	for i, f := range fn.Type().RecvParams() {
		if i >= 16 || f.Nname == nil || !f.Type.HasPointers() {
			continue
		}
		l := b.oldLoc(f.Nname.(*ir.Name))
		for _, g := range l.groups {
			if g.dynRoot.dirt && g.curfn == fn {
				// Passed on to what may.
				dirty |= 1 << i
			}
		}
		if sr == nil {
			continue
		}
		if bad && hasGroup(l, sr.q) || badQ && hasGroup(l, sr.qBad) {
			dirty |= 1 << i
		}
	}
	return dirty
}

// knownStored reports whether the caller of fn knows of m, which fn
// stores in a parameter's structure: a parameter, of which the function
// says where it is stored; or an object that fn allocates exactly as
// its caller says of that structure.
func (b *batch) knownStored(fn *ir.Func, m *location) bool {
	if m.unknown || m.curfn != fn || m.callRes != nil {
		return false
	}
	if !isAlloc(m) {
		return true
	}
	if !m.hasAttr(attrEscapes) {
		return true
	}
	if m.hasAttr(attrNotLocal | attrReturned | attrCandidateEscape) {
		return false
	}
	for _, r := range m.dyn {
		d := r.dynRoot
		if d == nil || d.call != nil || d.link != "" || d.cond != nil || r.curfn != fn {
			return false
		}
		if d.deep != 0 {
			continue
		}
		if d.plain && d.loc != nil && d.loc.param {
			continue
		}
		return false
	}
	return true
}

// ownBits returns the expression that is 1 if the bits of fn's call
// that the mask has are all set.
func (b *batch) ownBits(fn *ir.Func, mask uint16, pos src.XPos) ir.Node {
	uintptrType := types.Types[types.TUINTPTR]
	lit := func(v int64) ir.Node {
		return ir.NewBasicLit(b.gd, pos, uintptrType, constant.MakeInt64(v))
	}
	binary := func(op ir.Op, x, y ir.Node) ir.Node {
		n := ir.NewBinaryExpr(b.gd, pos, op, x, y)
		n.SetType(uintptrType)
		n.SetTypecheck(1)
		return n
	}
	if mask == 0 {
		return lit(1)
	}
	fn.DeepBits |= mask >> 1
	// (bits & mask) == mask, as (bits &^ ... ) would need a compare:
	// ((bits & mask) ^ mask) - 1 has its top bit set only for zero.
	x := binary(ir.OXOR, binary(ir.OAND, b.bitsVar(fn), lit(int64(mask))), lit(int64(mask)))
	return binary(ir.ORSH, binary(ir.OSUB, x, lit(1)), lit(63))
}

// groupTag returns the expression that is 1 if the group g is local as
// a whole, or nil if nobody knows.
func (b *batch) groupTag(g *location, pos src.XPos) ir.Node {
	c := b.cond(g)
	if c.bad {
		return nil
	}
	return b.ownBits(g.curfn, c.own, pos)
}

// groupParam adds to ps, of the parameter at l, what the conditions
// that it flows to need: it is stored in a structure by a function that
// is called.
func (b *batch) groupParam(ps *ir.ParamShare, l *location) {
	if !b.deepOn() {
		return
	}
	for _, r := range l.dyn {
		d := r.dynRoot
		if d == nil || d.cond == nil {
			continue
		}
		c := b.cond(d.cond)
		if c.bad {
			setMinDerefs(&ps.Share, 0)
			continue
		}
		if c.own&1 != 0 {
			ps.Results = 0xff
		}
		if deep := c.own >> 1; deep != 0 {
			ps.Deep |= deep
			setMinDerefs(&ps.DeepDerefs, 0)
		}
	}
}

// unknown records that a pointer which the graph does not follow, the
// value of n, flows to k (a group that it reaches is not known to be
// local).
func (e *escape) unknown(k hole, n ir.Node) {
	e.unknownOf(k, n.Type())
}

// unknownOf is unknown for a value of type t.
func (e *escape) unknownOf(k hole, t *types.Type) {
	if !e.deepOn() || k.dst == &e.blankLoc || t == nil || !t.HasPointers() {
		return
	}
	if k.derefs < 0 {
		k.derefs = 0
	}
	k.addrtaken = false
	e.flow(k, &e.unknownSrc)
}
