// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package escape

import (
	"fmt"

	"cmd/compile/internal/ir"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
)

// Receivers of interface calls (doc/gd/sharing.md).
//
// The method that an interface call runs is only known when the program
// runs, so its receiver went to the heap, where other goroutines reach:
// no object that was ever called through an interface was local. But an
// object is local or not from where it is allocated, and what is known
// there is its type, which says what the methods are: the object can stay
// local if none of the methods that may be called on it, with that type,
// lets another goroutine reach its receiver.
//
// So the receiver of an interface call flows to a receiver root: the
// heap, for escaping, with the interface type and the method. And a
// conversion of a pointer to an interface type has a location of its
// own, which the pointer flows through, with the pointer's type. When the
// graph is solved, a conversion that reaches a receiver root without
// going through another conversion (a "raw" path: solve.go) is checked:
// the method of its type must keep its receiver to the goroutine
// (convOK), or what was converted is shared. What flows into the
// conversion is not concerned by the root otherwise.
//
// Everything else that reaches a receiver root on a raw path holds, or
// leads to, an interface whose type is not known:
//
//   - a parameter of an interface type that is itself the receiver, of a
//     method of that type: the function says so (ir.ParamShare.Recv),
//     and its callers treat the argument as the receiver of any method
//     of the parameter's type;
//   - anything else (a parameter that leads to the receiver, an object
//     that the receiver is loaded from, the result of a call) may lead
//     to what is shared, as if the receiver went to the heap.

// A recvRoot is the receiver of a call of a method of an interface type.
type recvRoot struct {
	iface *types.Type
	sym   *types.Sym // the method, or nil: any method of iface
}

type recvKey struct {
	fn    *ir.Func
	iface *types.Type
	sym   *types.Sym
}

type convKey struct {
	t   *types.Type
	sym *types.Sym
}

func (b *batch) recvOn() bool {
	return b.gd.Debug.NoLocalRecv == 0 && b.callBitsOn()
}

// recvHole returns the hole for the receiver of a call of the method sym
// of the interface type iface, or of any of its methods if sym is nil.
func (e *escape) recvHole(iface *types.Type, sym *types.Sym) hole {
	if !e.recvOn() || iface == nil || !iface.IsInterface() || iface.HasShape() {
		return e.heapHole()
	}
	if pkg := e.curfn.Sym().Pkg; pkg == nil || ir.UncheckedPkg(pkg.Path) {
		return e.heapHole()
	}
	key := recvKey{e.curfn, iface, sym}
	if r := e.recvRoots[key]; r != nil {
		return r.asHole()
	}
	r := &location{
		curfn:     e.curfn,
		loopDepth: 1,
		attrs:     attrEscapes | attrPersists | attrMutates | attrCalls,
		recv:      &recvRoot{iface: iface, sym: sym},
	}
	e.allLocs = append(e.allLocs, r)
	if e.recvRoots == nil {
		e.recvRoots = make(map[recvKey]*location)
	}
	e.recvRoots[key] = r
	return r.asHole()
}

// convHole returns the hole for the operand of a conversion to an
// interface type of a value of type t, whose result goes to k: through
// a location of the conversion if t is a pointer type.
func (e *escape) convHole(k hole, t *types.Type) hole {
	if !e.recvOn() || k.derefs < 0 || k.dst == &e.blankLoc || !t.IsPtr() || t.HasShape() {
		return k
	}
	c := e.newLoc(nil, false)
	c.transient = true
	c.conv = t
	e.flow(k, c)
	return c.asHole()
}

// assertHole returns the hole for the operand of an assertion that an
// interface holds a value of type t, whose result goes to k: through a
// location of the assertion if t is not an interface type. What is
// reached from such a value is reached from the interface without its
// methods: if a receiver of an interface call is among it, on a raw
// path, the interface's object leads to what may be shared.
func (e *escape) assertHole(k hole, t *types.Type) hole {
	if !e.recvOn() || k.derefs < 0 || k.dst == &e.blankLoc || t == nil || t.IsInterface() || !t.HasPointers() {
		return k
	}
	a := e.newLoc(nil, false)
	a.transient = true
	a.assert = true
	e.flow(k, a)
	return a.asHole()
}

// hasRecv reports whether l, or what it leads to, is the receiver of a
// method that is not known, by a path that no conversion is on.
func (l *location) hasRecv() bool {
	return len(l.recvs) != 0 || l.recvOver
}

// tracksRecv reports whether a walk from root looks for the raw paths.
func tracksRecv(root *location) bool {
	return root.recv != nil || len(root.recvs) != 0
}

// wantsRecv reports whether l, which reaches root on a raw path, would
// learn of a receiver root from it.
func (b *batch) wantsRecv(l, root *location) bool {
	if l.recvOver || l.recv != nil || root.conv != nil {
		return false
	}
	if l.assert {
		return !l.hasAttr(attrSharesValue)
	}
	if root.recv != nil && !hasDyn2(l.recvs, root) {
		return true
	}
	for _, r := range root.recvs {
		if !hasDyn2(l.recvs, r) {
			return true
		}
	}
	return false
}

func hasDyn2(list []*location, r *location) bool {
	for _, x := range list {
		if x == r {
			return true
		}
	}
	return false
}

// addRecv records on l, which reaches root on a raw path, the receiver
// roots of root, and reports whether l changed.
func (b *batch) addRecv(l, root *location) bool {
	if l == root || l.recvOver || l.recv != nil || l.assert || root.conv != nil {
		return false
	}
	changed := false
	add := func(r *location) {
		if !hasDyn2(l.recvs, r) {
			l.recvs = append(l.recvs, r)
			changed = true
		}
	}
	if root.recv != nil {
		add(root)
	}
	for _, r := range root.recvs {
		add(r)
	}
	if root.recvOver || len(l.recvs) > maxDyn {
		l.recvs = nil
		l.recvOver = true
		changed = true
	}
	return changed
}

// recvMethods calls f for the methods that r is the receiver of.
func recvMethods(r *recvRoot, f func(sym *types.Sym) bool) bool {
	if r.sym != nil {
		return f(r.sym)
	}
	for _, m := range r.iface.AllMethods() {
		if m.Sym == nil || !f(m.Sym) {
			return false
		}
	}
	return true
}

// convOK reports whether what the conversion c converts stays with the
// goroutine through all the calls that c is the receiver of.
func (b *batch) convOK(c *location) bool {
	if c.recvOver {
		return false
	}
	for _, r := range c.recvs {
		ok := recvMethods(r.recv, func(sym *types.Sym) bool {
			key := convKey{c.conv, sym}
			v, done := b.convOKs[key]
			if !done {
				v = b.methodKeepsLocal(c.conv, sym)
				if b.convOKs == nil {
					b.convOKs = make(map[convKey]bool)
				}
				b.convOKs[key] = v
				if b.gd.Flag.LowerM >= 2 {
					s := "keeps its receiver to the goroutine"
					if !v {
						s = "may share its receiver, or is not known"
					}
					fmt.Printf("%s: (%v).%v %s\n", b.gd.FmtPos(c.curfn.Pos()), c.conv, sym.Name, s)
				}
			}
			return v
		})
		if !ok {
			return false
		}
	}
	return true
}

// methodKeepsLocal reports whether the method sym of the pointer type t
// is known not to let another goroutine reach its receiver, or anything
// that is reached from it, and not to return it or store it through
// another parameter.
func (b *batch) methodKeepsLocal(t *types.Type, sym *types.Sym) bool {
	mt := types.ReceiverBaseType(t)
	if mt == nil || mt.HasShape() {
		return false
	}
	typecheck.CalcMethods(mt)
	for _, f := range mt.AllMethods() {
		if f.Sym != sym {
			continue
		}
		if !f.IsMethod() || f.Nointerface() || !types.IsMethodApplicable(t, f) {
			return false
		}
		name, _ := f.Nname.(*ir.Name)
		if name == nil || name.Func == nil || len(name.Func.ParamShares) == 0 {
			// Not analysed (yet: ParamShares is set when the
			// function's batch is done), or not Go.
			return false
		}
		recv := name.Type().Recv()
		if recv == nil {
			return false
		}
		if !recv.Type.HasPointers() {
			return true
		}
		ps := name.Func.ParamShares[0]
		if ps.Share != 0 || ps.Results != 0 || ps.Via&^1 != 0 || ps.Recv {
			return false
		}
		esc := parseLeaks(b.gd, recv.Note)
		return !esc.anyResult()
	}
	return false
}

// noteParamRecvs records that the parameter l, dereferenced derefs
// times, reaches on a raw path the receiver roots of root. Only the walk
// from a receiver root itself knows how far the parameter is from the
// receiver.
func (b *batch) noteParamRecvs(l, root *location, derefs int) {
	if root.conv != nil || root.assert {
		return
	}
	if root.recv != nil {
		if derefs == 0 && b.paramIsRecv(l, root.recv) {
			l.shares.Recv = true
		} else {
			setMinDerefs(&l.shares.Share, derefs)
		}
	}
	if len(root.recvs) != 0 || root.recvOver {
		setMinDerefs(&l.shares.Share, derefs)
	}
}

// paramIsRecv reports whether the parameter l is of an interface type
// that has the methods that r is the receiver of.
func (b *batch) paramIsRecv(l *location, r *recvRoot) bool {
	if l.n == nil {
		return false
	}
	t := l.n.Type()
	if t == nil || !t.IsInterface() || t.HasShape() {
		return false
	}
	if t == r.iface {
		return true
	}
	return recvMethods(r, func(sym *types.Sym) bool {
		for _, m := range t.AllMethods() {
			if m.Sym == sym {
				return true
			}
		}
		return false
	})
}
