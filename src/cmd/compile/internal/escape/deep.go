// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package escape

import (
	"fmt"

	"cmd/compile/internal/ir"
)

// What a function does with its arguments' structures (doc/gd/sharing.md).
//
// A store through a parameter that is never assigned (p.f = x) is a
// store in what the caller's argument points to, which the caller
// knows (ir.ParamShare.Via). A store that goes deeper (p.f.g = x), or
// through a variable that was loaded from what a parameter reaches (for
// _, q := range p.s { q.f = x }), is a store in an object that is
// reached from the parameter's argument, and nobody knows which: x is
// shared unless every object that the argument reaches is local. The
// function says which parameters those are (ir.ParamShare.Deep), and
// the caller answers.

func (b *batch) deepOn() bool {
	return b.gd.Debug.NoLocalDeep == 0 && b.linksOn()
}

// deriveFromParams finds the variables of fn that are derived from its
// parameters: all that such a variable is ever assigned is a path from
// a parameter that is never assigned, or from another derived variable.
// What it points to is reached from those parameters' arguments, whose
// bits b.derived has (the bit of fn's k'th parameter, the receiver
// first, is 1<<k). A parameter that is never assigned has its own bit.
// It is called before fn's graph is built.
func (b *batch) deriveFromParams(fn *ir.Func) {
	if !b.deepOn() {
		return
	}
	paramBit := make(map[*ir.Name]uint16)
	for i, f := range fn.Type().RecvParams() {
		if i >= 16 || f.Nname == nil {
			continue
		}
		name := f.Nname.(*ir.Name)
		if name.Addrtaken() || name.IsClosureVar() || !name.Type().HasPointers() {
			continue
		}
		paramBit[name] = 1 << i
	}
	if len(paramBit) == 0 {
		return
	}
	cand := make(map[*ir.Name]bool)
	for _, n := range fn.Dcl {
		if n.Class != ir.PAUTO || n.IsClosureVar() || n.Addrtaken() || !n.Type().HasPointers() {
			continue
		}
		cand[n] = true
	}
	srcs := make(map[*ir.Name][]ir.Node)
	drop := func(n ir.Node) {
		if name, ok := n.(*ir.Name); ok {
			delete(cand, name)
			delete(paramBit, name)
		}
	}
	assign := func(l, r ir.Node) {
		name, ok := l.(*ir.Name)
		if !ok {
			return
		}
		if _, isParam := paramBit[name]; isParam {
			// A parameter that is assigned is not its argument.
			delete(paramBit, name)
			return
		}
		if cand[name] && r != nil {
			srcs[name] = append(srcs[name], r)
		}
	}
	ir.VisitFuncAndClosures(fn, func(n ir.Node) {
		switch n := n.(type) {
		case *ir.AssignStmt:
			if n.Op() != ir.OAS {
				drop(n.X)
				break
			}
			assign(n.X, n.Y)
		case *ir.AssignListStmt:
			switch {
			case (n.Op() == ir.OAS2DOTTYPE || n.Op() == ir.OAS2MAPR) && len(n.Lhs) == 2 && len(n.Rhs) == 1:
				assign(n.Lhs[0], n.Rhs[0])
				drop(n.Lhs[1])
			case n.Op() == ir.OAS2 && len(n.Lhs) == len(n.Rhs):
				for i, l := range n.Lhs {
					assign(l, n.Rhs[i])
				}
			default:
				for _, l := range n.Lhs {
					drop(l)
				}
			}
		case *ir.AssignOpStmt:
			drop(n.X)
		case *ir.RangeStmt:
			// The elements of what is ranged over, and the keys of
			// a map, are reached from it.
			if n.X != nil && (n.X.Type().IsSlice() || n.X.Type().IsArray() || n.X.Type().IsPtr() || n.X.Type().IsMap()) {
				if n.Key != nil {
					assign(n.Key, n.X)
				}
				if n.Value != nil {
					assign(n.Value, n.X)
				}
			} else {
				drop(n.Key)
				drop(n.Value)
			}
		case *ir.SwitchStmt:
			if g, ok := n.Tag.(*ir.TypeSwitchGuard); ok {
				for _, c := range n.Cases {
					if c.Var != nil {
						assign(c.Var, g.X)
					}
				}
			}
		case *ir.ClosureExpr:
			for _, cv := range n.Func.ClosureVars {
				drop(cv.Canonical())
			}
		}
	})
	// A variable that the syntax above does not assign and that is not
	// declared by an assignment may get its value elsewhere.
	for name := range cand {
		switch name.Defn.(type) {
		case nil, *ir.AssignStmt, *ir.AssignListStmt, *ir.RangeStmt, *ir.TypeSwitchGuard, *ir.CaseClause, *ir.SwitchStmt:
		default:
			delete(cand, name)
		}
	}

	mask := make(map[*ir.Name]uint16)
	// maskOf returns the parameters that the value of n is reached
	// from, and whether it is known to be reached from parameters
	// only (nil is).
	var maskOf func(n ir.Node) (uint16, bool)
	maskOf = func(n ir.Node) (uint16, bool) {
		for {
			switch x := n.(type) {
			case *ir.ParenExpr:
				n = x.X
				continue
			case *ir.ConvExpr:
				if (x.Op() == ir.OCONVNOP || x.Op() == ir.OCONVIFACE) && x.X.Type() != nil && x.X.Type().HasPointers() && !x.X.Type().IsUintptr() && !x.Type().IsUintptr() {
					n = x.X
					continue
				}
			case *ir.SelectorExpr:
				if x.Op() == ir.ODOT || x.Op() == ir.ODOTPTR {
					n = x.X
					continue
				}
			case *ir.StarExpr:
				n = x.X
				continue
			case *ir.IndexExpr:
				if (x.Op() == ir.OINDEX || x.Op() == ir.OINDEXMAP) && x.X.Type() != nil && !x.X.Type().IsString() {
					n = x.X
					continue
				}
			case *ir.SliceExpr:
				if x.X.Type() != nil && !x.X.Type().IsString() {
					n = x.X
					continue
				}
			case *ir.TypeAssertExpr:
				n = x.X
				continue
			case *ir.AddrExpr:
				if x.Op() == ir.OADDR {
					if p := addrThrough(x); p != nil {
						n = p
						continue
					}
				}
			case *ir.NilExpr:
				return 0, true
			case *ir.Name:
				if x.Op() != ir.ONAME {
					return 0, false
				}
				if bit, ok := paramBit[x]; ok {
					return bit, true
				}
				if cand[x] {
					return mask[x], true
				}
			}
			return 0, false
		}
	}
	for changed := true; changed; {
		changed = false
		for name := range cand {
			m := mask[name]
			for _, src := range srcs[name] {
				sm, ok := maskOf(src)
				if !ok {
					delete(cand, name)
					delete(mask, name)
					changed = true
					m = 0
					break
				}
				m |= sm
			}
			if cand[name] && m != mask[name] {
				mask[name] = m
				changed = true
			}
		}
	}
	// The variables that are copies of one parameter: all that such a
	// variable is ever assigned is that parameter, or another copy
	// of it (the receiver of a method that is inlined). An argument
	// that is one is the parameter (noteCallArg, ownParam).
	copyOf := make(map[*ir.Name]*ir.Name)
	paramOf := func(n ir.Node) *ir.Name {
		for {
			switch x := n.(type) {
			case *ir.ParenExpr:
				n = x.X
				continue
			case *ir.ConvExpr:
				if x.Op() == ir.OCONVNOP && x.X.Type() != nil && x.X.Type().HasPointers() && !x.X.Type().IsUnsafePtr() && !x.Type().IsUnsafePtr() {
					n = x.X
					continue
				}
			case *ir.Name:
				if x.Op() != ir.ONAME {
					return nil
				}
				if _, ok := paramBit[x]; ok {
					return x
				}
				return copyOf[x]
			}
			return nil
		}
	}
	for changed := true; changed; {
		changed = false
		for name := range cand {
			if copyOf[name] != nil || len(srcs[name]) == 0 {
				continue
			}
			var p *ir.Name
			for _, src := range srcs[name] {
				q := paramOf(src)
				if q == nil || p != nil && q != p {
					p = nil
					break
				}
				p = q
			}
			if p != nil {
				copyOf[name] = p
				changed = true
			}
		}
	}
	if len(copyOf) != 0 {
		if b.copyOf == nil {
			b.copyOf = make(map[*ir.Name]*ir.Name)
		}
		for name, p := range copyOf {
			b.copyOf[name] = p
		}
	}
	if b.derived == nil {
		b.derived = make(map[*ir.Name]uint16)
	}
	for name, bit := range paramBit {
		b.derived[name] = bit
	}
	for name := range cand {
		if m := mask[name]; m != 0 {
			b.derived[name] = m
			if b.gd.Flag.LowerM >= 4 {
				fmt.Printf("%s: %v is reached from the parameters %b of %v\n", b.gd.FmtPos(name.Pos()), name, m, fn)
			}
		}
	}
	b.derivedMask = maskOf
	if b.derivedOf == nil {
		b.derivedOf = make(map[*ir.Func]func(ir.Node) (uint16, bool))
	}
	b.derivedOf[fn] = maskOf
}

// deepMaskOf returns the parameters of the current function that the
// value of n is reached from, or 0 if it is not known to be reached
// from parameters only.
func (e *escape) deepMaskOf(n ir.Node) uint16 {
	f := e.derivedOf[e.curfn]
	if f == nil {
		return 0
	}
	m, ok := f(n)
	if !ok {
		return 0
	}
	return m
}

// deepRoot returns the dynamic root of the current function for a
// store in what its parameters of the mask reach: it has no path.
func (e *escape) deepRoot(mask uint16) *location {
	return e.deepRootOf(mask, false)
}

// deepRootOf is deepRoot; with marker, the root is no holder of what
// flows to it: the function that may store it is only known when the
// program runs, and says then whether it keeps it.
func (e *escape) deepRootOf(mask uint16, marker bool) *location {
	if mask == 0 || !e.deepOn() {
		return nil
	}
	k := dynKey{0, fmt.Sprintf("deep %p %d %v", e.curfn, mask, marker)}
	if r := e.dynRoots[k]; r != nil {
		return r
	}
	r := &location{
		curfn:     e.curfn,
		loopDepth: 1,
		attrs:     attrDynShares,
		dynRoot:   &dynRoot{deep: mask},
		isDynRoot: true,
		marker:    marker,
	}
	e.allLocs = append(e.allLocs, r)
	if e.dynRoots == nil {
		e.dynRoots = make(map[dynKey]*location)
	}
	e.dynRoots[k] = r
	return r
}

// deepHoles returns the holes for an argument that the function called
// stores, dereferenced derefs times, in what its parameters of the mask
// reach: the arguments are e.callArgs. Where such an argument is
// reached from parameters of the current function, that is a store in
// what those reach; anything else is not known yet, and shared (the
// hole that share gives).
func (e *escape) deepHoles(mask uint16, derefs int, share func() hole) []hole {
	return e.deepHolesOf(mask, derefs, share, false)
}

// deepHolesOf is deepHoles; with marker, the roots are no holders of
// what flows to them (deepRootOf).
func (e *escape) deepHolesOf(mask uint16, derefs int, share func() hole, marker bool) []hole {
	var hs []hole
	shared := false
	for k := 0; k < 16; k++ {
		if mask&(1<<k) == 0 {
			continue
		}
		var m uint16
		if k < len(e.callArgs) {
			m = e.deepMaskOf(e.callArgs[k])
		}
		if r := e.deepRootOf(m, marker); r != nil {
			if e.gd.Flag.LowerM >= 4 {
				fmt.Printf("%s: an argument is stored in what argument %d reaches, which the parameters %b of %v reach\n", e.gd.FmtPos(e.gd.Pos), k, m, e.curfn)
			}
			hs = append(hs, r.asHole().shift(e.gd, derefs))
		} else if k < len(e.argGroups) && e.argGroups[k] != nil && !e.argGroups[k].dynRoot.dirt {
			// It is one of what the argument reaches from now
			// on, and local only if all of that is.
			g := e.argGroups[k]
			hs = append(hs, g.asHole().shift(e.gd, derefs), e.condRoot(g).asHole().shift(e.gd, derefs))
		} else if !shared && share != nil {
			if e.gd.Flag.LowerM >= 4 {
				fmt.Printf("%s: what is stored in what argument %d reaches is shared: nobody knows all that it reaches\n", e.gd.FmtPos(e.gd.Pos), k)
			}
			shared = true
			hs = append(hs, share().shift(e.gd, derefs))
		}
	}
	return hs
}

// deepOfPath returns the parameters that the object of a store through
// a path from the variable name is reached from, if it is not what a
// parameter itself points to (plain), which the caller knows better.
func (e *escape) deepOfPath(name *ir.Name, plain bool) uint16 {
	if !e.deepOn() {
		return 0
	}
	if plain && name.Class == ir.PPARAM {
		return 0
	}
	return e.derived[name]
}
