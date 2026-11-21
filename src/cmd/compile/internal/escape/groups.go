// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package escape

import (
	"fmt"
	"go/constant"
	"slices"
	"strings"

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
	bad bool
	why string
	own uint16 // the bits of the function's call that must be set
	// links are the arguments of the classes of functions that are
	// only known when the program runs to which a member is passed:
	// none may share it, nor store in what it reaches.
	links []ir.ShareLink
	// stores are those of which only what they store matters.
	stores []ir.ShareLink
	// mixes are the mix roots that a member flows to.
	mixes []*location
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

// pendingDirt returns the membership root of the current function for
// what it passes as the j'th argument to fn, a function of its batch:
// dirt, if fn turns out to store in what that parameter reaches.
func (e *escape) pendingDirt(fn *ir.Name, j int) *location {
	type key struct {
		cur *ir.Func
		fn  *ir.Name
		j   int
	}
	if e.pending == nil {
		e.pending = make(map[any]*location)
	}
	k := key{e.curfn, fn, j}
	d := e.pending[k]
	if d == nil {
		d = e.memberRoot(e.curfn)
		d.dynRoot.dirt = true
		d.dynRoot.pendFn, d.dynRoot.pendArg = fn, j
		e.pending[k] = d
	}
	return d
}

// classMarker returns the membership root of the current function for
// what it passes as argument arg to a function of the class key.
func (e *escape) classMarker(key string, arg int) *location {
	type ckey struct {
		cur *ir.Func
		key string
		arg int
	}
	if e.pending == nil {
		e.pending = make(map[any]*location)
	}
	k := ckey{e.curfn, key, arg}
	d := e.pending[k]
	if d == nil {
		d = e.memberRoot(e.curfn)
		d.dynRoot.classKey, d.dynRoot.classArg = key, arg
		e.pending[k] = d
	}
	return d
}

// mixUnknown is the key of a mix root for a call that has no class:
// nobody can ask, and the function is taken to mix its arguments.
const mixUnknown = "?"

// mixRoot returns the dynamic root of the current function for what is
// passed as the argument at place arg, with other arguments, to a
// function of the class key: see dynRoot.mixKey. It is no holder. With
// any, it returns the membership root for what that argument reaches
// (dynRoot.mixAny): it matters if a function of the class stores
// something in what that argument reaches.
func (e *escape) mixRoot(key string, arg uint8, anyArg bool, other *location, mask uint16, bad bool) *location {
	type mkey struct {
		cur    *ir.Func
		key    string
		arg    uint8
		anyArg bool
		other  *location
		mask   uint16
		bad    bool
	}
	if e.pending == nil {
		e.pending = make(map[any]*location)
	}
	k := mkey{e.curfn, key, arg, anyArg, other, mask, bad}
	r := e.pending[k]
	if r == nil {
		d := &dynRoot{mixKey: key, mixArg: arg, mixAny: anyArg, mixOther: other, mixMask: mask, mixBad: bad, mixFn: e.curfn}
		if anyArg {
			r = e.memberRoot(e.curfn)
			d.group = true
			r.dynRoot = d
		} else {
			r = &location{
				curfn:     e.curfn,
				loopDepth: 1,
				attrs:     attrDynShares,
				dynRoot:   d,
				isDynRoot: true,
				marker:    true,
			}
			e.allLocs = append(e.allLocs, r)
		}
		e.pending[k] = r
	}
	return r
}

// mixTerms returns the expression that is 1 if, for each of the mix
// roots and of those that their others' members flow to in turn, no
// function of the root's class stores one argument in what another
// reaches, or what the root's others reach is local by itself. (A root
// that is only reached through one whose class does not mix is asked
// all the same: the expression is one term for each root, not one in
// another, which grew without bound.)
func (b *batch) mixTerms(roots []*location, pos src.XPos) ir.Node {
	uintptrType := types.Types[types.TUINTPTR]
	binary := func(op ir.Op, x, y ir.Node) ir.Node {
		n := ir.NewBinaryExpr(b.gd, pos, op, x, y)
		n.SetType(uintptrType)
		n.SetTypecheck(1)
		return n
	}
	lit := func(v int64) ir.Node {
		return ir.NewBasicLit(b.gd, pos, uintptrType, constant.MakeInt64(v))
	}
	seen := make(map[*location]bool)
	todo := append([]*location(nil), roots...)
	var tag ir.Node = lit(1)
	for len(todo) > 0 {
		m := todo[0]
		todo = todo[1:]
		if seen[m] {
			continue
		}
		seen[m] = true
		d := m.dynRoot
		// 1 if no function of the class stores this argument, or
		// for what it reaches any argument, in what another
		// reaches.
		var apart ir.Node = lit(0)
		if d.mixKey != mixUnknown {
			bit := ir.ShareWordMix
			switch {
			case d.mixAny && d.mixArg == ir.MixRecv:
				bit = ir.ShareWordMixDstRecv
			case d.mixAny && int(d.mixArg) < ir.EscMaskArgs:
				bit = ir.ShareWordMixDst + int(d.mixArg)
			case d.mixAny:
			case d.mixArg == ir.MixRecv:
				bit = ir.ShareWordMixRecv
			case int(d.mixArg) < ir.EscMaskArgs:
				bit = ir.ShareWordMixArg + int(d.mixArg)
			}
			apart = b.linkBit(pos, d.mixKey, bit, true)
		}
		var rest ir.Node
		ok := !d.mixBad
		if ok && d.mixOther != nil {
			c := b.cond(d.mixOther)
			if c.bad {
				ok = false
			} else {
				rest = b.groupAtoms(d.mixOther, c, pos)
				todo = append(todo, c.mixes...)
			}
		}
		if ok && d.mixMask != 0 {
			own := b.ownBits(d.mixFn, d.mixMask<<1, pos)
			if rest == nil {
				rest = own
			} else {
				rest = binary(ir.OAND, rest, own)
			}
		}
		term := apart
		if ok {
			if rest == nil {
				continue
			}
			term = binary(ir.OOR, apart, rest)
		}
		tag = binary(ir.OAND, tag, term)
	}
	return tag
}

// groupAtoms returns the expression of what the group g needs by
// itself, without the mix roots that its members flow to.
func (b *batch) groupAtoms(g *location, c *groupCond, pos src.XPos) ir.Node {
	tag := b.ownBits(g.curfn, c.own, pos)
	and := func(t ir.Node) {
		n := ir.NewBinaryExpr(b.gd, pos, ir.OAND, tag, t)
		n.SetType(types.Types[types.TUINTPTR])
		n.SetTypecheck(1)
		tag = n
	}
	for _, l := range c.links {
		and(b.linkTagOf(pos, l.Key, int(l.Arg), true, true))
	}
	for _, l := range c.stores {
		and(b.linkTagOf(pos, l.Key, int(l.Arg), false, true))
	}
	return tag
}

// storedBy returns the membership root of the current function for
// what it passes to a function that stores in what the argument
// reaches.
func (e *escape) storedBy() *location {
	if e.storedRoots == nil {
		e.storedRoots = make(map[*ir.Func]*location)
	}
	d := e.storedRoots[e.curfn]
	if d == nil {
		d = e.memberRoot(e.curfn)
		d.dynRoot.stored = true
		e.storedRoots[e.curfn] = d
	}
	return d
}

// calleeStores returns the parameters of fn in whose structures it may
// store.
func calleeStores(fn *ir.Name) uint16 {
	if fn.Func == nil || len(fn.Func.ParamShares) == 0 {
		return ^uint16(0)
	}
	return fn.Func.StoresInto
}

// isDirt reports whether the membership root x is dirt: what flows to
// it may have had stored in it what nobody here knows.
func isDirt(x *location) bool {
	d := x.dynRoot
	if !d.dirt {
		return false
	}
	if d.pendFn != nil {
		f := d.pendFn.Func
		if f == nil || d.pendArg >= 16 || f.StoresInto&(1<<d.pendArg) != 0 {
			return true
		}
		// (What it passes on to a function that is only known when
		// the program runs, it may store in too.)
		params := d.pendFn.Type().RecvParams()
		if d.pendArg < len(params) && params[d.pendArg].Nname != nil {
			if l, _ := params[d.pendArg].Nname.(*ir.Name).Opt.(*location); l != nil {
				for _, g := range l.groups {
					if g.dynRoot.classKey != "" {
						return true
					}
				}
				for _, r := range l.dyn {
					if r.dynRoot != nil && r.dynRoot.link != "" {
						return true
					}
				}
			}
		}
		return false
	}
	return true
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
		// What it does is not known yet: when the graph is solved,
		// whether it stores in what the parameter reaches is, and
		// that is all that its caller makes of it.
		for j, arg := range all {
			if pointsToPointers(arg.Type()) {
				set(j, e.pendingDirt(fn, j))
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
			if e.gd.Flag.LowerM >= 4 {
				fmt.Printf("%s: argument %d of %v is dirt: the callee says so\n", e.gd.FmtPos(call.Pos()), j, fn)
			}
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

	// The parameters that are passed on as receivers.
	for _, fn := range fns {
		for i, f := range fn.Type().RecvParams() {
			if i >= 16 || f.Nname == nil {
				continue
			}
			l := b.oldLoc(f.Nname.(*ir.Name))
			for _, g := range l.groups {
				if g.dynRoot.classKey != "" {
					l.shares.StoreLinks = addShareLink(l.shares.StoreLinks, g.dynRoot.classKey, g.dynRoot.classArg)
				}
			}
		}
	}

	// In which parameters' structures a function stores: through a
	// variable that the parameter flows to, or by a function that it
	// passes it to; for one of the batch, until nothing changes.
	for _, fn := range fns {
		fn.StoresInto = 0
	}
	for changed := true; changed; {
		changed = false
		for _, fn := range fns {
			if st := b.storesInto(fn); st != fn.StoresInto {
				fn.StoresInto = st
				changed = true
			}
		}
	}

	// Whether a function's results are as local as it is told
	// depends on the functions of the batch whose results it
	// returns: all are, until one is found that is not.
	for _, fn := range fns {
		fn.ResultExact = true
	}
	b.storeRoot = make(map[*location]bool)
	for _, sr := range b.storeRoots {
		for _, r := range []*location{sr.q, sr.s, sr.qBad, sr.sBad} {
			b.storeRoot[r] = true
		}
	}
	for changed := true; changed; {
		changed = false
		b.solveConds()
		for _, fn := range fns {
			if !fn.ResultExact {
				continue
			}
			if g := b.resGroups[fn]; g != nil {
				if c := b.cond(g); c.bad || c.own&^1 != 0 || len(c.links) != 0 || len(c.stores) != 0 || len(c.mixes) != 0 {
					fn.ResultExact = false
					changed = true
				}
			}
		}
	}

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

func (c *groupCond) addLink(key string, arg int) {
	c.links = addShareLink(c.links, key, arg)
}

// addShareLink returns links with the argument arg of the class key, in
// an order that does not depend on how they came.
func addShareLink(links []ir.ShareLink, key string, arg int) []ir.ShareLink {
	for _, l := range links {
		if l.Key == key && int(l.Arg) == arg {
			return links
		}
	}
	links = append(links, ir.ShareLink{Key: key, Arg: uint8(arg), Derefs: 1})
	slices.SortFunc(links, func(a, b ir.ShareLink) int {
		if x := strings.Compare(a.Key, b.Key); x != 0 {
			return x
		}
		return int(a.Arg) - int(b.Arg)
	})
	return links
}

// cond returns the condition of the group g.
func (b *batch) cond(g *location) *groupCond {
	c := b.conds[g]
	if c == nil {
		// (Not one of the groups that solveConds knows: by itself.)
		c = b.condOnce(g)
		b.conds[g] = c
	}
	return c
}

// solveConds computes the conditions of all the groups. One may need
// another's (what is stored in a structure is in another too), and that
// one the first's: each is computed from what the others are so far, in
// the order of the locations, until nothing changes, so that the result
// does not depend on the order in which anything else asks.
func (b *batch) solveConds() {
	var groups []*location
	for _, l := range b.allLocs {
		if d := l.dynRoot; d != nil && d.group && !d.dirt && !d.stored && d.classKey == "" && !d.mixAny && !b.storeRoot[l] {
			groups = append(groups, l)
		}
	}
	b.conds = make(map[*location]*groupCond, len(groups))
	for i, g := range groups {
		g.index = i
		b.conds[g] = new(groupCond)
	}
	n := len(groups)
	for _, l := range b.allLocs {
		if l.dynRoot != nil && l.dynRoot.mixKey != "" {
			l.index = n
			n++
		}
	}
	for changed := true; changed; {
		changed = false
		for _, g := range groups {
			c := b.condOnce(g)
			old := b.conds[g]
			if c.bad != old.bad || c.own != old.own || len(c.links) != len(old.links) || len(c.stores) != len(old.stores) || len(c.mixes) != len(old.mixes) {
				changed = true
			}
			b.conds[g] = c
		}
	}
	if b.gd.Flag.LowerM >= 4 {
		for _, g := range groups {
			b.printCond(g, b.conds[g])
		}
	}
}

// condOnce computes the condition of g from the conditions that the
// other groups have so far.
// A groupInfo is what a group's condition is made of: what its members
// say by themselves, the groups whose conditions it needs, and the
// functions of the batch whose results it has.
type groupInfo struct {
	static  groupCond
	conds   []*location
	callees []*ir.Name
}

// condOnce computes the condition of g from the conditions that the
// other groups have so far, and from what is known so far of the
// results of the functions of the batch.
func (b *batch) condOnce(g *location) *groupCond {
	in := b.groupInfoOf(g)
	c := new(groupCond)
	*c = in.static
	if c.bad {
		return c
	}
	c.links = slices.Clone(in.static.links)
	c.stores = slices.Clone(in.static.stores)
	c.mixes = slices.Clone(in.static.mixes)
	for _, callee := range in.callees {
		if !callee.Func.ResultExact {
			c.bad = true
			c.why = fmt.Sprintf("the result of %v, which does not return what is as local as it is told", callee)
			return c
		}
	}
	for _, g2 := range in.conds {
		c2 := b.conds[g2]
		if c2 == nil {
			continue
		}
		if c2.bad {
			c.bad = true
			c.why = "stored in a structure that is not local: " + c2.why
			return c
		}
		c.own |= c2.own
		for _, l := range c2.links {
			c.addLink(l.Key, int(l.Arg))
		}
		for _, l := range c2.stores {
			c.stores = addShareLink(c.stores, l.Key, int(l.Arg))
		}
		for _, m := range c2.mixes {
			if m.dynRoot.mixOther != g && !slices.Contains(c.mixes, m) {
				c.mixes = append(c.mixes, m)
			}
		}
	}
	// (In an order that does not depend on how the members came.)
	slices.SortFunc(c.mixes, func(x, y *location) int { return x.index - y.index })
	return c
}

// groupInfoOf returns what the group g's condition is made of.
func (b *batch) groupInfoOf(g *location) *groupInfo {
	if in := b.infos[g]; in != nil {
		return in
	}
	in := new(groupInfo)
	if b.infos == nil {
		b.infos = make(map[*location]*groupInfo)
	}
	b.infos[g] = in
	c := &in.static
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
			if isDirt(x) {
				fail("passed to what may store in it", m)
			}
			if x.dynRoot.classKey != "" {
				c.stores = addShareLink(c.stores, x.dynRoot.classKey, x.dynRoot.classArg)
			}
			if x.dynRoot.mixAny && x.dynRoot.mixOther != g && !slices.Contains(c.mixes, x) {
				c.mixes = append(c.mixes, x)
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
			case callee.Func == nil:
				fail("the result of a function that nobody knows", m)
			case b.inMutualBatch(callee):
				// Known when the batch is (solveGroups).
				in.callees = append(in.callees, callee)
			case !callee.Func.ResultExact:
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
			case d.mixKey != "":
				if d.mixOther != g && r.curfn == fn && !slices.Contains(c.mixes, r) {
					c.mixes = append(c.mixes, r)
				}
			case d.cond != nil:
				if d.cond != g && !slices.Contains(in.conds, d.cond) {
					in.conds = append(in.conds, d.cond)
				}
			case d.link != "":
				// None of the functions that it may be passed to
				// may share it, nor store in what it reaches: a
				// call of one passes no bits, so that what it
				// allocates is shared.
				c.addLink(d.link, d.arg)
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
	// (In an order that does not depend on how the members came.)
	slices.SortFunc(in.conds, func(x, y *location) int { return x.index - y.index })
	return in
}

// printCond prints the condition c of the group g.
func (b *batch) printCond(g *location, c *groupCond) {
	if g.dynRoot.result {
		return
	}
	fn := g.curfn
	what, pos := "", fn.Pos()
	switch {
	case g.dynRoot.call != nil:
		what, pos = fmt.Sprintf("argument %d", g.dynRoot.arg), g.dynRoot.call.Pos()
	case g.dynRoot.name != nil:
		what, pos = fmt.Sprint(g.dynRoot.name), g.dynRoot.name.Pos()
	}
	if what == "" {
		return
	}
	if c.bad {
		fmt.Printf("%s: what %s reaches is not known to be local: %s\n", b.gd.FmtPos(pos), what, c.why)
	} else {
		fmt.Printf("%s: what %s reaches is local if the bits %b of the call of %v are set, and %d kinds of functions that are only known when the program runs neither share it nor store in it\n", b.gd.FmtPos(pos), what, c.own, fn, len(c.links)+len(c.stores))
	}
}

// storesInto returns the parameters of fn in whose structures it may
// store anything, as far as is known of the functions of its batch.
func (b *batch) storesInto(fn *ir.Func) uint16 {
	if b.lost[fn] {
		return ^uint16(0)
	}
	var st uint16
	sr := b.storeRoots[fn]
	for i, f := range fn.Type().RecvParams() {
		if i >= 16 || f.Nname == nil || !f.Type.HasPointers() {
			continue
		}
		l := b.oldLoc(f.Nname.(*ir.Name))
		if sr != nil && (hasGroup(l, sr.q) || hasGroup(l, sr.qBad)) {
			st |= 1 << i
			continue
		}
		for _, g := range l.groups {
			if g.curfn == fn && (g.dynRoot.stored || isDirt(g)) {
				st |= 1 << i
				break
			}
		}
	}
	return st
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
				if b.gd.Flag.LowerM >= 4 {
					pos := fn.Pos()
					if m.n != nil {
						pos = m.n.Pos()
					}
					fmt.Printf("%s: %v stores what its caller does not know: %v (unknown %v, of %v, a call's result %v, escapes %v, attributes %#x, %d roots)\n", b.gd.FmtPos(pos), fn, m.n, m.unknown, m.curfn, m.callRes != nil, m.hasAttr(attrEscapes), uint(m.attrs), len(m.dyn))
				}
				break
			}
		}
		// Through a variable that is neither a parameter nor
		// derived from some, the function says nothing of what it
		// stores: only what is not an object of its own, or a
		// pointer from elsewhere, is harmless.
		if sr.s.dynRoot.sawUnknown || sr.s.dynRoot.crossed {
			if b.gd.Flag.LowerM >= 4 && !bad {
				fmt.Printf("%s: %v stores what its caller does not know: saw unknown %v, crossed %v\n", b.gd.FmtPos(fn.Pos()), fn, sr.s.dynRoot.sawUnknown, sr.s.dynRoot.crossed)
			}
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
			if g.curfn == fn && isDirt(g) {
				// Passed on to what may.
				dirty |= 1 << i
				if b.gd.Flag.LowerM >= 4 {
					fmt.Printf("%s: %v passes its parameter %d on to what may store in it (a function of its batch: %v)\n", b.gd.FmtPos(fn.Pos()), fn, i, g.dynRoot.pendFn)
				}
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
	tag := b.groupAtoms(g, c, pos)
	if len(c.mixes) != 0 {
		n := ir.NewBinaryExpr(b.gd, pos, ir.OAND, tag, b.mixTerms(c.mixes, pos))
		n.SetType(types.Types[types.TUINTPTR])
		n.SetTypecheck(1)
		tag = n
	}
	return tag
}

// mixParam adds to ps, of the parameter at l, the mix roots that it
// flows to: what the parameter is stored in if a function of the root's
// class stores it in what another argument reaches (MixLinks), and what
// it reaches may have gained if one stores any (MixGroups).
func (b *batch) mixParam(ps *ir.ParamShare, l *location) {
	if !b.deepOn() {
		return
	}
	link := func(d *dynRoot) ir.MixLink {
		ml := ir.MixLink{Key: d.mixKey, Arg: d.mixArg, Into: d.mixMask, Bad: d.mixBad}
		if o := d.mixOther; o != nil && !ml.Bad {
			c := b.cond(o)
			if c.bad || c.own&1 != 0 || len(c.links) != 0 || len(c.stores) != 0 || len(c.mixes) != 0 {
				ml.Bad = true
			} else {
				ml.Into |= c.own >> 1
			}
		}
		return ml
	}
	add := func(list []ir.MixLink, ml ir.MixLink) []ir.MixLink {
		for i := range list {
			if x := &list[i]; x.Key == ml.Key && x.Arg == ml.Arg {
				x.Into |= ml.Into
				x.Bad = x.Bad || ml.Bad
				return list
			}
		}
		list = append(list, ml)
		slices.SortFunc(list, func(x, y ir.MixLink) int {
			if c := strings.Compare(x.Key, y.Key); c != 0 {
				return c
			}
			return int(x.Arg) - int(y.Arg)
		})
		return list
	}
	for _, r := range l.dyn {
		if d := r.dynRoot; d != nil && d.mixKey != "" && !d.mixAny && r.curfn == l.curfn {
			ps.MixLinks = add(ps.MixLinks, link(d))
		}
	}
	for _, r := range l.groups {
		if d := r.dynRoot; d.mixAny && r.curfn == l.curfn {
			ps.MixGroups = add(ps.MixGroups, link(d))
		}
	}
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
		if c.bad || len(c.mixes) != 0 {
			// (A structure of the function's that may be
			// mixed with others: not said to the caller yet.)
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
		for _, l := range c.links {
			// The caller's argument is passed to those too:
			// its own groups then know. (The function reads
			// their words for what it allocates.)
			found := false
			for _, x := range ps.Links {
				if x.Key == l.Key && x.Arg == l.Arg {
					found = true
				}
			}
			switch {
			case found:
			case len(ps.Links) < ir.MaxShareLinks:
				ps.Links = append(ps.Links, l)
			default:
				ps.LinksFull = true
			}
		}
		for _, l := range c.stores {
			ps.StoreLinks = addShareLink(ps.StoreLinks, l.Key, int(l.Arg))
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
