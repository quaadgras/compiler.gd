// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package escape

import (
	"cmd/compile/internal/ir"
)

// addr evaluates an addressable expression n and returns a hole
// that represents storing into the represented location.
func (e *escape) addr(n ir.Node) hole {
	if n == nil || ir.IsBlank(n) {
		// Can happen in select case, range, maybe others.
		return e.discardHole()
	}

	k := e.heapHole()

	switch n.Op() {
	default:
		e.gd.Fatalf("unexpected addr: %v", n)
	case ir.ONAME:
		n := n.(*ir.Name)
		if n.Class == ir.PEXTERN {
			break
		}
		loc := e.oldLoc(n)
		if loc.paramOut && !e.returning {
			loc.assignedInBody = true
		}
		k = loc.asHole()
	case ir.OLINKSYMOFFSET:
		break
	case ir.ODOT:
		n := n.(*ir.SelectorExpr)
		k = e.addr(n.X)
	case ir.OINDEX:
		n := n.(*ir.IndexExpr)
		e.discard(n.Index)
		if n.X.Type().IsArray() {
			k = e.addr(n.X)
		} else {
			e.mutate(n.X)
			k = e.storeThrough(n.X)
		}
	case ir.ODEREF:
		n := n.(*ir.StarExpr)
		e.mutate(n.X)
		k = e.storeThrough(n.X)
	case ir.ODOTPTR:
		n := n.(*ir.SelectorExpr)
		e.mutate(n.X)
		k = e.storeThrough(n.X)
	case ir.OINDEXMAP:
		n := n.(*ir.IndexExpr)
		if !e.mapsOn() {
			e.discard(n.X)
			// Keys used in map assignments must escape.
			// See "Hashing Pointers" doc in internal/runtime/maps/map.go.
			e.assignHeap(n.Index, "key of map put", n)
			break
		}
		// The key and the value are stored in what the map
		// allocates, which is where the map is: the heap, for
		// escaping, and local if the map is (shares.go).
		e.mutate(n.X)
		k = e.storeThrough(n.X)
		e.expr(k.note(e.gd, n, "key of map put"), n.Index)
	}

	return k
}

func (e *escape) mutate(n ir.Node) {
	e.expr(e.mutatorHole(), n)
}

func (e *escape) addrs(l ir.Nodes) []hole {
	var ks []hole
	for _, n := range l {
		ks = append(ks, e.addr(n))
	}
	return ks
}

func (e *escape) assignHeap(src ir.Node, why string, where ir.Node) {
	e.expr(e.heapHole().note(e.gd, where, why), src)
}

// assignList evaluates the assignment dsts... = srcs....
func (e *escape) assignList(dsts, srcs []ir.Node, why string, where ir.Node) {
	e.assignTo(e.addrs(dsts), dsts, srcs, why, where)
}

// assignTo evaluates the assignment dsts... = srcs..., given the
// holes ks of dsts.
func (e *escape) assignTo(ks []hole, dsts, srcs []ir.Node, why string, where ir.Node) {
	for i, k := range ks {
		var src ir.Node
		if i < len(srcs) {
			src = srcs[i]
		}

		if dst := dsts[i]; dst != nil {
			// Detect implicit conversion of uintptr to unsafe.Pointer when
			// storing into reflect.{Slice,String}Header.
			if dst.Op() == ir.ODOTPTR && ir.IsReflectHeaderDataField(dst) {
				e.unsafeValue(e.heapHole().note(e.gd, where, why), src)
				continue
			}

			// Filter out some no-op assignments for escape analysis.
			if src != nil && isSelfAssign(dst, src) {
				if e.gd.Flag.LowerM != 0 {
					e.gd.WarnfAt(where.Pos(), "%v ignoring self-assignment in %v", e.curfn, where)
				}
				k = e.discardHole()
			}
		}

		e.expr(k.note(e.gd, where, why), src)
	}

	e.reassigned(ks, where)
}

// reassigned marks the locations associated with the given holes as
// reassigned, unless the location represents a variable declared and
// assigned exactly once by where.
func (e *escape) reassigned(ks []hole, where ir.Node) {
	if as, ok := where.(*ir.AssignStmt); ok && as.Op() == ir.OAS && as.Y == nil {
		if dst, ok := as.X.(*ir.Name); ok && dst.Op() == ir.ONAME && dst.Defn == nil {
			// Zero-value assignment for variable declared without an
			// explicit initial value. Assume this is its initialization
			// statement.
			return
		}
	}

	for _, k := range ks {
		loc := k.dst
		// Variables declared by range statements are assigned on every iteration.
		if n, ok := loc.n.(*ir.Name); ok && n.Defn == where && where.Op() != ir.ORANGE {
			continue
		}
		loc.reassigned = true
	}
}

// storeThrough returns the hole for a store through the pointer or slice
// p: what p's allocations hold if p is a tracked variable (shares.go),
// and the heap otherwise.
func (e *escape) storeThrough(p ir.Node) hole {
	if c := e.contentsOf(p); c != nil {
		return c.asHole()
	}
	if !e.deepOn() {
		if r := e.dynRootOf(p); r != nil {
			return r.asHole()
		}
		return e.heapHole()
	}
	// What is stored is, from now on, in an object that the variable
	// which the path starts from points to or reaches: the groups
	// that have that object need to know (groups.go).
	name, plain, global := e.pathBase(p)
	// The store is in what the parameters that p is derived from reach
	// (storeset.go).
	target := storeTarget(p.Type())
	e.noteStoreType(e.deepMaskOf(p), target)
	if name != nil && name.Opt != nil {
		if g := e.varGroup(name); g != nil {
			if !e.skipStoreTypes {
				g.dynRoot.need.add(target)
			}
			// Not a parameter's structure, of which the caller
			// knows: the function's own. What is stored is one
			// of what the variable reaches, and local only if
			// all of that is.
			k := e.teeHole(e.ownHeapHole(), g.asHole(), e.condRoot(g).asHole())
			return e.noteStray(k, name, plain, true)
		}
	}
	k := e.heapHole()
	if r := e.dynRootOf(p); r != nil {
		k = r.asHole()
	}
	switch {
	case name != nil && name.Opt != nil:
		return e.noteStray(k, name, plain, false)
	case global:
	default:
		if e.lost == nil {
			e.lost = make(map[*ir.Func]bool)
		}
		e.lost[e.curfn] = true
	}
	return k
}
