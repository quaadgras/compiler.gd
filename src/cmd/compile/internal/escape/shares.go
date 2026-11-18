// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package escape

import (
	"fmt"
	"go/constant"

	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
	"cmd/internal/objabi"
	"cmd/internal/sys"
)

// Goroutine sharing, statically (doc/gd/sharing.md, runtime/shared.go).
//
// The solver marks with attrShares every location whose address may flow
// to where another goroutine can reach it. An allocation without the mark
// is reached, while the function that made it runs, by that function's
// goroutine only: what the function returns is the caller's, which is the
// same goroutine.
//
// markUnshared turns that into facts about variables, which is what the
// code generator has when it stores or loads through one:
//
//   - ir.UnsharedStorage: the variable's own memory is not shared (it
//     matters for one that is moved to the heap);
//   - ir.UnsharedPointee: every pointer the variable ever holds is to
//     such an allocation, or nil.
//
// The stores and loads in unshared memory need no lock, no publication
// and no validation.
//
// The graph knows where an address flows, but not everything a variable
// may be assigned (a call's result, a load from the heap). So the
// variables that count are the tracked ones: those whose every
// assignment, found in the syntax, has a source that is plainly a local
// allocation or another tracked variable (trackPointers).
//
// A store through a pointer is, to escape analysis, a store in the heap,
// which would make everything that a function links together shared. For
// a tracked variable x the graph has instead a location for what x's
// allocations hold, with x = &contents: a store through x flows there, a
// load through x flows from there, and the contents are shared exactly
// when x's value is. For escaping, the contents are the heap, as before.

// trackPointers finds fn's tracked variables and gives each the location
// of its contents. It is called before fn's graph is built.
func (b *batch) trackPointers(fn *ir.Func) {
	if b.gd.Debug.NoShare != 0 || b.gd.Debug.NoLocal != 0 {
		return
	}
	cand, srcs := b.pointerSources(fn)
	if len(cand) == 0 {
		return
	}
	dropUnfresh(cand, srcs, func(n ir.Node) bool { return true })
	if b.contents == nil {
		b.contents = make(map[*ir.Name]*location)
		b.sources = make(map[*ir.Name][]ir.Node)
	}

	// Two variables that may hold the same pointer have the same
	// contents: a store through one is loaded through the other. A
	// tracked variable gets a pointer another holds only from it.
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
	for name := range cand {
		root := find(name)
		c := b.contents[root]
		if c == nil {
			c = &location{
				curfn:     fn,
				loopDepth: 1,
				attrs:     attrEscapes | attrPersists | attrMutates | attrCalls,
			}
			b.allLocs = append(b.allLocs, c)
			b.contents[root] = c
		}
		b.contents[name] = c
		b.sources[name] = srcs[name]
		// name = &contents
		x := b.oldLoc(name)
		x.edges = append(x.edges, edge{src: c, derefs: -1, share: true})
		c.owners = append(c.owners, x)
	}
}

// pointerVars calls f for the variables whose pointer the fresh
// expression n may be: see dropUnfresh.
func pointerVars(n ir.Node, f func(v *ir.Name)) {
	switch n.Op() {
	case ir.OCONVNOP:
		pointerVars(n.(*ir.ConvExpr).X, f)
	case ir.OPAREN:
		pointerVars(n.(*ir.ParenExpr).X, f)
	case ir.OADDR:
		if p := addrThrough(n.(*ir.AddrExpr)); p != nil {
			pointerVars(p, f)
		}
	case ir.OAPPEND:
		pointerVars(n.(*ir.CallExpr).Args[0], f)
	case ir.OSLICE, ir.OSLICE3:
		pointerVars(n.(*ir.SliceExpr).X, f)
	case ir.ONAME:
		f(n.(*ir.Name))
	case ir.OINLCALL:
		if n := n.(*ir.InlinedCallExpr); len(n.ReturnVars) == 1 {
			pointerVars(n.ReturnVars[0], f)
		}
	}
}

// addrThrough returns p if n is the address of a part of what the
// pointer or slice p points to (&p.f, &p[i], &*p), and nil otherwise.
func addrThrough(n *ir.AddrExpr) ir.Node {
	switch x := ir.OuterValue(n.X).(type) {
	case *ir.SelectorExpr:
		if x.Op() == ir.ODOTPTR {
			return x.X
		}
	case *ir.StarExpr:
		return x.X
	case *ir.IndexExpr:
		if x.Op() == ir.OINDEX && x.X.Type().IsSlice() {
			return x.X
		}
	}
	return nil
}

// contentsOf returns the location of what the pointer or slice n's
// allocations hold, if n is a tracked variable, and nil otherwise.
func (b *batch) contentsOf(n ir.Node) *location {
	if b.contents == nil {
		return nil
	}
	for {
		switch x := n.(type) {
		case *ir.ParenExpr:
			n = x.X
			continue
		case *ir.ConvExpr:
			if x.Op() == ir.OCONVNOP && x.X.Type().HasPointers() {
				n = x.X
				continue
			}
		case *ir.Name:
			if x.Op() == ir.ONAME {
				return b.contents[x.Canonical()]
			}
		}
		return nil
	}
}

// pointerSources returns the local variables of fn that hold pointers,
// whose address is not taken, that no closure has, and that are only
// assigned by plain assignments, with the sources of those.
func (b *batch) pointerSources(fn *ir.Func) (cand map[*ir.Name]bool, srcs map[*ir.Name][]ir.Node) {
	cand = make(map[*ir.Name]bool)
	srcs = make(map[*ir.Name][]ir.Node)
	for _, n := range fn.Dcl {
		if n.Class != ir.PAUTO || n.IsClosureVar() || n.Addrtaken() || !n.Type().HasPointers() {
			continue
		}
		if !n.Type().IsPtr() && !n.Type().IsSlice() && !n.Type().IsUnsafePtr() {
			continue
		}
		switch n.Defn.(type) {
		case nil, *ir.AssignStmt, *ir.AssignListStmt:
			cand[n] = true
		}
	}
	if len(cand) == 0 {
		return
	}
	drop := func(n ir.Node) {
		if name, ok := n.(*ir.Name); ok {
			delete(cand, name)
		}
	}
	ir.VisitFuncAndClosures(fn, func(n ir.Node) {
		switch n := n.(type) {
		case *ir.AssignStmt:
			if n.Op() != ir.OAS {
				drop(n.X)
				break
			}
			if name, ok := n.X.(*ir.Name); ok && cand[name] && n.Y != nil {
				srcs[name] = append(srcs[name], n.Y)
			}
		case *ir.AssignListStmt:
			if n.Op() == ir.OAS2 && len(n.Lhs) == len(n.Rhs) {
				for i, l := range n.Lhs {
					if name, ok := l.(*ir.Name); ok && cand[name] {
						srcs[name] = append(srcs[name], n.Rhs[i])
					}
				}
				break
			}
			for _, l := range n.Lhs {
				drop(l)
			}
		case *ir.AssignOpStmt:
			drop(n.X)
		case *ir.RangeStmt:
			drop(n.Key)
			drop(n.Value)
		case *ir.CaseClause:
			if n.Var != nil {
				drop(n.Var)
			}
		case *ir.ClosureExpr:
			for _, cv := range n.Func.ClosureVars {
				drop(cv.Canonical())
			}
		}
	})
	return
}

// dropUnfresh removes from cand the variables with a source that is not
// fresh: an allocation that alloc accepts, nil, a variable that stays in
// cand, or a slice or the address of a part of what one of those points
// to.
func dropUnfresh(cand map[*ir.Name]bool, srcs map[*ir.Name][]ir.Node, alloc func(n ir.Node) bool) {
	var fresh func(n ir.Node) bool
	fresh = func(n ir.Node) bool {
		switch n.Op() {
		case ir.ONIL:
			return true
		case ir.OCONVNOP, ir.OPAREN:
			switch n := n.(type) {
			case *ir.ConvExpr:
				// Not an integer made a pointer.
				return n.X.Type().HasPointers() && fresh(n.X)
			case *ir.ParenExpr:
				return fresh(n.X)
			}
		case ir.ONEW, ir.OPTRLIT, ir.OMAKESLICE, ir.OSLICELIT:
			return alloc(n)
		case ir.OADDR:
			n := n.(*ir.AddrExpr)
			// A part of what a fresh pointer points to. (Not the
			// address of a variable: what is stored through it
			// would be loaded from the variable.)
			if p := addrThrough(n); p != nil {
				return fresh(p)
			}
		case ir.OAPPEND:
			n := n.(*ir.CallExpr)
			return alloc(n) && fresh(n.Args[0])
		case ir.OSLICE, ir.OSLICE3:
			n := n.(*ir.SliceExpr)
			return n.X.Type().IsSlice() && fresh(n.X)
		case ir.ONAME:
			name := n.(*ir.Name)
			return cand[name]
		case ir.OINLCALL:
			n := n.(*ir.InlinedCallExpr)
			if len(n.ReturnVars) == 1 {
				return fresh(n.ReturnVars[0])
			}
		}
		return false
	}
	for changed := true; changed; {
		changed = false
		for name := range cand {
			for _, src := range srcs[name] {
				if !fresh(src) {
					delete(cand, name)
					changed = true
					break
				}
			}
		}
	}
}

// mayShare reports whether the location's address may flow to where
// another goroutine reaches it, as far as is known when compiling: a
// container that is only known at run time may be shared.
func (l *location) mayShare() bool {
	return l.hasAttr(attrShares | attrDynShares)
}

// markUnshared records on fns' variables what the solved graph says no
// other goroutine can reach.
func (b *batch) markUnshared(fns []*ir.Func) {
	if b.gd.Debug.NoShare != 0 || b.gd.Debug.NoLocal != 0 {
		return
	}
	// The allocations that are shared, by the expression or the variable
	// that makes them.
	shared := make(map[ir.Node]bool)
	alloc := make(map[ir.Node]bool)
	for _, loc := range b.allLocs {
		if loc.n == nil {
			continue
		}
		alloc[loc.n] = true
		if loc.mayShare() {
			shared[loc.n] = true
		}
	}
	unshared := func(n ir.Node) bool { return alloc[n] && !shared[n] }

	for _, fn := range fns {
		cand := make(map[*ir.Name]bool)
		for _, n := range fn.Dcl {
			if n.Class != ir.PAUTO || n.IsClosureVar() {
				continue
			}
			loc, ok := n.Opt.(*location)
			if !ok || loc == nil || loc.mayShare() {
				continue
			}
			n.Unshared |= ir.UnsharedStorage
			if b.contents[n] != nil && !loc.captured && !loc.addrtaken && !b.contents[n].mayShare() {
				cand[n] = true
			}
		}
		dropUnfresh(cand, b.sources, unshared)
		for name := range cand {
			name.Unshared |= ir.UnsharedPointee
		}
		if b.gd.Flag.LowerM >= 3 {
			for _, n := range fn.Dcl {
				if n.Class == ir.PAUTO && n.Type().HasPointers() {
					loc, _ := n.Opt.(*location)
					fmt.Printf("%s: %v unshared=%d shares=%v tracked=%v\n", b.gd.FmtPos(n.Pos()), n, n.Unshared, loc != nil && loc.mayShare(), b.contents[n] != nil)
				}
			}
		}
	}
}

// markLocal records on the expression or the variable of loc, which is
// allocated in the heap, that the object is local: no other goroutine
// will reach it, or anything that it will lead to, as long as it lives.
// The object is then allocated in the range of addresses of local
// memory, and what is stored in it later, by code that only has its
// address, is allocated as it is (runtime/shared.go).
//
// Nothing that is reached from a local object may be shared, however
// deep, and not by the caller either: no part of it is returned.
func (b *batch) markLocal(loc *location) {
	if b.gd.Debug.NoShare != 0 || b.gd.Debug.NoLocal != 0 || b.gd.Debug.NoLocalAlloc != 0 {
		return
	}
	if loc.hasAttr(attrShares | attrSharesValue | attrSharesDeep | attrReturned) {
		if b.gd.Flag.LowerM >= 2 && loc.n != nil {
			why := ""
			for i, name := range []string{"shares", "holds what is shared", "leads to what is shared", "is returned"} {
				if loc.attrs&(attrShares<<i) != 0 {
					why += ", " + name
				}
			}
			fmt.Printf("%s: %v is not local%s\n", b.gd.FmtPos(loc.n.Pos()), loc.n, why)
		}
		return
	}
	// The runtime's packages and reflect keep pointers as integers
	// and make them of memory, where the graph does not follow.
	if fn := loc.curfn; fn == nil || fn.Sym() == nil || fn.Sym().Pkg == nil || ir.UncheckedPkg(fn.Sym().Pkg.Path) {
		return
	}
	if len(loc.dyn) != 0 {
		tag := b.localTag(loc)
		if tag == nil {
			if b.gd.Flag.LowerM >= 2 && loc.n != nil {
				fmt.Printf("%s: %v is not local, stored where it cannot tell\n", b.gd.FmtPos(loc.n.Pos()), loc.n)
			}
			return
		}
		ir.SetLocalTag(b.gd, loc.n, tag)
		if b.gd.Flag.LowerM >= 2 {
			fmt.Printf("%s: %v is local if %v\n", b.gd.FmtPos(loc.n.Pos()), loc.n, tag)
		}
		return
	}
	switch n := loc.n.(type) {
	case *ir.Name:
		n.Unshared |= ir.LocalObject
	case interface{ SetLocalAlloc(bool) }:
		n.SetLocalAlloc(true)
	default:
		return
	}
	if b.gd.Flag.LowerM >= 2 {
		fmt.Printf("%s: %v is local\n", b.gd.FmtPos(loc.n.Pos()), loc.n)
	}
}

// Dynamic roots.
//
// A store through a pointer that is not a tracked variable is a store
// in a container that is only known when the program runs. If the
// pointer is a pure path P from a local variable (p, p.f, *p.f: no call,
// no index), the store has a location of its own, the dynamic root of P
// in that statement, that is the heap for escaping but that no other
// goroutine reaches by itself: it is local if what P points to is, which
// is a compare of P (runtime.isLocal). The solver records on every
// location the roots that it, or something reached from it, flows to
// (location.dyn).
//
// An allocation that only flows to dynamic roots is local if all of them
// are, provided each can be evaluated where the object is allocated and
// has the value there that it has at the store:
//
//   - the root's statement is the allocation's, a plain assignment, and
//     P is a variable or the statement has no call: nothing between the
//     allocation and the store changes P;
//   - or P is a parameter that is never assigned. (A variable that is
//     assigned once and in scope at the allocation would do as well;
//     the graph has no scopes yet.)
//
// markLocal then gives the allocation the expression of its tag.

type dynKey struct {
	stmt int32
	path string
}

type dynRoot struct {
	path  ir.Node   // P
	name  *ir.Name  // the variable P starts from
	plain bool      // P is name
	loc   *location // name's
	stmt  int32
}

// maxDyn is the number of dynamic roots of a location over which it
// counts as shared.
const maxDyn = 8

func (e *escape) noteSimpleStmt() {
	if e.simpleStmts == nil {
		e.simpleStmts = make(map[int32]bool)
	}
	e.simpleStmts[e.curStmt] = true
}

func (e *escape) noteStmtCall() {
	if e.stmtCalls == nil {
		e.stmtCalls = make(map[int32]bool)
	}
	e.stmtCalls[e.curStmt] = true
}

// splitHeap reports whether the target has the two ranges of addresses
// (runtime.heapSplit), and returns the logarithm of the size of the
// local one.
func splitHeap(gd *base.Invocation) (shift int64, ok bool) {
	if gd.Flag.Race || gd.Flag.MSan || gd.Flag.ASan {
		return 0, false
	}
	switch gd.Ctxt.Headtype {
	case objabi.Hlinux, objabi.Hdarwin, objabi.Hfreebsd, objabi.Hwindows:
	default:
		return 0, false
	}
	switch gd.Ctxt.Arch.Family {
	case sys.AMD64, sys.ARM64:
		return 36, true
	}
	return 0, false
}

// purePath returns the variable that the path n starts from, and
// whether n is a path: a local variable, or a field of or what is
// pointed to by a path.
func (e *escape) purePath(n ir.Node) (name *ir.Name, plain bool) {
	plain = true
	for {
		switch x := n.(type) {
		case *ir.ParenExpr:
			n = x.X
			continue
		case *ir.SelectorExpr:
			if x.Op() == ir.ODOT || x.Op() == ir.ODOTPTR {
				n, plain = x.X, false
				continue
			}
		case *ir.StarExpr:
			n, plain = x.X, false
			continue
		case *ir.Name:
			if x.Op() == ir.ONAME && (x.Class == ir.PAUTO || x.Class == ir.PPARAM) && x.Curfn == e.curfn && !x.IsClosureVar() && x.Canonical() == x {
				return x, plain
			}
		}
		return nil, false
	}
}

// dynRootOf returns the dynamic root of a store through the pointer or
// slice p in the current statement, or nil if p is not a path.
func (e *escape) dynRootOf(p ir.Node) *location {
	if e.gd.Debug.NoShare != 0 || e.gd.Debug.NoLocal != 0 || e.gd.Debug.NoLocalAlloc != 0 || e.gd.Debug.NoLocalDyn != 0 {
		return nil
	}
	if _, ok := splitHeap(e.gd); !ok {
		return nil
	}
	if t := p.Type(); t == nil || !t.IsPtr() && !t.IsSlice() {
		return nil
	}
	name, plain := e.purePath(p)
	if name == nil {
		return nil
	}
	key := dynKey{e.curStmt, fmt.Sprintf("%p %v", name, p)}
	if plain && name.Class == ir.PPARAM {
		key.stmt = 0
	}
	if r := e.dynRoots[key]; r != nil {
		return r
	}
	r := &location{
		curfn:     e.curfn,
		loopDepth: 1,
		attrs:     attrEscapes | attrPersists | attrMutates | attrCalls | attrDynShares,
		dynRoot:   &dynRoot{path: p, name: name, plain: plain, stmt: key.stmt},
		isDynRoot: true,
	}
	e.allLocs = append(e.allLocs, r)
	if e.dynRoots == nil {
		e.dynRoots = make(map[dynKey]*location)
	}
	e.dynRoots[key] = r
	return r
}

// settleDynRoots makes the heap of the dynamic roots whose variable
// something else than the function's own statements may change. It is
// called when the graph is built.
func (b *batch) settleDynRoots() {
	for _, r := range b.dynRoots {
		loc, _ := r.dynRoot.name.Opt.(*location)
		if loc == nil || loc.captured || loc.addrtaken {
			r.dynRoot = nil
			r.attrs |= attrShares
			continue
		}
		r.dynRoot.loc = loc
	}
}

// dynOf returns the dynamic roots that what flows to root flows to.
func dynOf(root *location) (self *location, more []*location) {
	if root.dynRoot != nil {
		self = root
	}
	return self, root.dyn
}

// wantsDyn reports whether l would learn of a dynamic root from root.
func (b *batch) wantsDyn(l, root *location) bool {
	if l.hasAttr(attrSharesDeep) {
		return false
	}
	self, more := dynOf(root)
	if self != nil && !hasDyn(l, self) {
		return true
	}
	for _, r := range more {
		if !hasDyn(l, r) {
			return true
		}
	}
	return false
}

func hasDyn(l, r *location) bool {
	for _, x := range l.dyn {
		if x == r {
			return true
		}
	}
	return false
}

// addDyn records on l, which flows to root, the dynamic roots of root,
// and reports whether l changed.
func (b *batch) addDyn(l, root *location) bool {
	if l == root || l.hasAttr(attrSharesDeep) {
		return false
	}
	self, more := dynOf(root)
	changed := false
	add := func(r *location) {
		if r != nil && r != l && !hasDyn(l, r) {
			l.dyn = append(l.dyn, r)
			changed = true
		}
	}
	add(self)
	for _, r := range more {
		add(r)
	}
	if len(l.dyn) > maxDyn {
		l.dyn = nil
		l.attrs |= attrSharesDeep
	}
	return changed
}

// localTag returns the expression of the tag of the allocation of loc,
// which only flows to dynamic roots, or nil if one of them cannot be
// evaluated there.
func (b *batch) localTag(loc *location) ir.Node {
	shift, ok := splitHeap(b.gd)
	if !ok || loc.n == nil {
		return nil
	}
	pos := loc.n.Pos()
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
	// The first address of the local range.
	base := func() ir.Node {
		return ir.NewLinksymExpr(b.gd, pos, typecheck.LookupRuntimeVar(b.gd, "localHeapBase"), uintptrType)
	}
	var tag ir.Node
	seen := make(map[string]bool)
	for _, r := range loc.dyn {
		d := r.dynRoot
		if d == nil {
			return nil
		}
		v := d.loc
		if v == nil || v.captured || v.addrtaken || r.curfn != loc.curfn {
			return nil
		}
		switch {
		case d.plain && !v.reassigned && v.param:
		case d.stmt != 0 && d.stmt == loc.stmt && b.simpleStmts[d.stmt] && (d.plain || !b.stmtCalls[d.stmt]):
		default:
			return nil
		}
		key := fmt.Sprint(d.path)
		if seen[key] {
			continue
		}
		seen[key] = true

		// (((uintptr(P) - base) >> shift) - 1) >> 63: 1 if P is in
		// the local range, 0 if not.
		var p ir.Node = ir.DeepCopy(pos, d.path)
		if p.Type().IsSlice() {
			sp := ir.NewUnaryExpr(b.gd, pos, ir.OSPTR, p)
			sp.SetType(types.NewPtr(p.Type().Elem()))
			sp.SetTypecheck(1)
			p = sp
		}
		up := ir.NewConvExpr(b.gd, pos, ir.OCONVNOP, types.Types[types.TUNSAFEPTR], p)
		up.SetTypecheck(1)
		u := ir.NewConvExpr(b.gd, pos, ir.OCONVNOP, uintptrType, up)
		u.SetTypecheck(1)
		u.SetCheckPtr(true) // not kept: see walkConv
		one := binary(ir.ORSH, binary(ir.OSUB, binary(ir.ORSH, binary(ir.OSUB, u, base()), lit(shift)), lit(1)), lit(63))
		if tag == nil {
			tag = one
		} else {
			tag = binary(ir.OAND, tag, one)
		}
	}
	return tag
}
