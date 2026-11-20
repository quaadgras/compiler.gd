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

// Interfaces (doc/gd/sharing.md).
//
// The method that an interface call runs is only known when the program
// runs, so its receiver went to the heap, where other goroutines reach:
// nothing that was ever called through an interface was local. But an
// interface value is made by a conversion, of a value whose type says
// what the methods are. So the rule is on the conversion, once for all
// the calls there will be:
//
//   - A value of type T converted to the interface type I is shared
//     unless every method of I, as T has it, keeps its receiver to the
//     goroutine: it does not let another goroutine reach it, or what it
//     leads to, nor store it through another parameter (convOK). What
//     the method returns is the call's affair.
//   - A call i.M() then says nothing about which goroutines reach its
//     receiver: whatever i holds was converted to an interface type
//     with M, somewhere, and answered for M there. The receiver must be
//     in the heap, as before, and what the call returns may be the
//     receiver or what it leads to, as for a function that returns a
//     field of its argument.
//   - An interface asserted to hold a value of another interface type J
//     may have methods called that its conversion did not answer for:
//     unless the interface's own type has all of J's methods, what it
//     holds is shared (assertHole).
//
// A value in an interface that is not a pointer is in a box that is
// only read: the box is not what another goroutine may write, and
// needs no place of its own in this.

type convKey struct {
	t, iface *types.Type
}

func (b *batch) recvOn() bool {
	return b.gd.Debug.NoLocalRecv == 0 && b.callBitsOn()
}

// recvHole returns the hole for the receiver of a call of a method of an
// interface, whose results go to ks: the heap, where no other goroutine
// reaches by itself, and the results.
func (e *escape) recvHole(ks []hole, call *ir.CallExpr) hole {
	if !e.recvOn() {
		return e.heapHole()
	}
	if pkg := e.curfn.Sym().Pkg; pkg == nil || ir.UncheckedPkg(pkg.Path) {
		return e.heapHole()
	}
	hs := []hole{e.recvLoc.asHole(), e.recvHeldHole()}
	results := call.Fun.Type().Results()
	for i, k := range ks {
		if i >= len(results) || !results[i].Type.HasPointers() || k.dst == &e.blankLoc {
			continue
		}
		if k.derefs < 0 {
			return e.heapHole()
		}
		k = k.note(e.gd, call, "receiver in result")
		k.recvRes = true
		hs = append(hs, k)
	}
	return e.teeHole(hs...)
}

// convHole returns the hole for the operand of a conversion of a value
// of type t to the interface type iface, whose result goes to k.
func (e *escape) convHole(k hole, t, iface *types.Type) hole {
	if k.dst != &e.blankLoc && k.derefs >= 0 {
		k.recvAllOK = e.recvKeepsNothing(t, nil)
		k.recvOK = k.recvAllOK || e.recvKeepsNothing(t, iface)
	}
	if !e.recvOn() || k.dst == &e.blankLoc || k.derefs < 0 || !t.HasPointers() {
		return k
	}
	if e.convOK(t, iface) {
		return k
	}
	// Not known here: a type with a shape, a method that is not
	// analysed yet. The linker knows what the methods with those names
	// do with their receivers, of all the types that the program has.
	hs := e.recvLinkHoles(iface)
	if hs == nil {
		hs = []hole{e.convShareHole()}
	}
	k2 := e.teeHole(append([]hole{k}, hs...)...)
	k2.recvOK, k2.recvAllOK = k.recvOK, k.recvAllOK
	return k2
}

// maxRecvLinks is the number of methods of an interface type over which
// what is converted to it is shared, where the type that is converted
// does not say: each method is a place that the value goes to.
const maxRecvLinks = 4

// recvLinkHoles returns the holes for a value that is converted to the
// interface type iface, or asserted to be of it, by something that does
// not know its type: the link roots of the receivers of iface's methods
// (callbits.go), or nil if there are none.
func (e *escape) recvLinkHoles(iface *types.Type) []hole {
	if !e.linksOn() || !iface.IsInterface() || iface.HasShape() {
		return nil
	}
	methods := iface.AllMethods()
	if len(methods) == 0 || len(methods) > maxRecvLinks {
		return nil
	}
	var hs []hole
	for _, m := range methods {
		if m.Sym == nil {
			return nil
		}
		key, wild := ir.ShareKeyRecv(m.Sym.Name, m.Type)
		if key == "" || wild {
			return nil
		}
		r := e.linkRootOf(key, 0, false)
		if r == nil {
			return nil
		}
		hs = append(hs, r.asHole())
	}
	return hs
}

// recvKeepsNothing reports whether t is a pointer type of which no
// method that the interface type iface has, or no method at all if
// iface is nil, keeps its receiver beyond the call, as far as is known
// here: each is analysed, and what it does with the receiver's pointer
// is at most to pass it to calls that check (ir.Func.KeptChecked),
// which is also what a call of a method of its own is, the type being
// this one. The methods of iface are those that can be called of what
// is converted to it, until it is asserted to have others (assertHole).
func (b *batch) recvKeepsNothing(t, iface *types.Type) bool {
	if moveOff(b.gd) || !t.IsPtr() || t.HasShape() {
		return false
	}
	if iface != nil && (!iface.IsInterface() || iface.HasShape()) {
		return false
	}
	key := convKey{t, iface}
	if v, ok := b.recvNothing[key]; ok {
		return v
	}
	var want map[*types.Sym]bool
	if iface != nil {
		want = make(map[*types.Sym]bool)
		for _, m := range iface.AllMethods() {
			if m.Sym == nil {
				return false
			}
			want[m.Sym] = true
		}
	}
	v := func() bool {
		mt := types.ReceiverBaseType(t)
		if mt == nil || mt.HasShape() {
			return false
		}
		typecheck.CalcMethods(mt)
		for _, f := range mt.AllMethods() {
			if want != nil && !want[f.Sym] {
				continue
			}
			if !f.IsMethod() || f.Nointerface() || !types.IsMethodApplicable(t, f) {
				if want != nil {
					return false
				}
				continue
			}
			name, _ := f.Nname.(*ir.Name)
			if name == nil || name.Func == nil || len(name.Func.ParamShares) == 0 {
				// Not analysed (yet), or not Go.
				return false
			}
			recv := name.Type().Recv()
			if recv == nil {
				return false
			}
			if !recv.Type.IsPtr() {
				// A copy of what the pointer points to.
				continue
			}
			esc := parseLeaks(b.gd, recv.Note)
			if esc.Heap() == 0 && name.Func.KeptChecked&1 == 0 {
				return false
			}
			// Nor may it return it.
			for i := range numEscResults {
				if esc.Result(i) == 0 {
					return false
				}
			}
		}
		return true
	}()
	if b.recvNothing == nil {
		b.recvNothing = make(map[convKey]bool)
	}
	b.recvNothing[key] = v
	return v
}

// assertHole returns the hole for the operand of an assertion that an
// interface of type from holds a value of type t, whose result goes to
// k.
func (e *escape) assertHole(k hole, t, from *types.Type) hole {
	k = e.assertHole1(k, t, from)
	if k.dst != &e.blankLoc && t != nil && from != nil && t.IsInterface() && !(from.IsInterface() && !t.HasShape() && !from.HasShape() && hasMethods(from, t)) {
		// Methods may be called that the interface's type does
		// not have: any of what it holds (recvKeepsNothing).
		k2 := e.teeHole(k, e.recvAnyHole())
		k2.ro = k.ro
		return k2
	}
	return k
}

func (e *escape) assertHole1(k hole, t, from *types.Type) hole {
	if !e.recvOn() || k.dst == &e.blankLoc || k.derefs < 0 || t == nil || from == nil {
		return k
	}
	if !t.IsInterface() && !t.HasShape() {
		return k
	}
	if !t.HasShape() && !from.HasShape() && from.IsInterface() && hasMethods(from, t) {
		return k
	}
	if hs := e.recvLinkHoles(t); hs != nil {
		return e.teeHole(append([]hole{k}, hs...)...)
	}
	return e.teeHole(k, e.convShareHole())
}

// hasMethods reports whether the interface type t has all the methods
// of the interface type of.
func hasMethods(t, of *types.Type) bool {
	if t == of {
		return true
	}
	have := t.AllMethods()
	for _, m := range of.AllMethods() {
		found := false
		for _, h := range have {
			if h.Sym == m.Sym {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// convOK reports whether every method of the interface type iface, as
// the type t has it, keeps its receiver to the goroutine.
func (b *batch) convOK(t, iface *types.Type) bool {
	if t.HasShape() || iface.HasShape() || !iface.IsInterface() {
		return false
	}
	methods := iface.AllMethods()
	if len(methods) == 0 {
		return true
	}
	key := convKey{t, iface}
	if v, ok := b.convOKs[key]; ok {
		return v
	}
	v := true
	for _, m := range methods {
		if m.Sym == nil || !b.methodKeepsLocal(t, m.Sym) {
			if b.gd.Flag.LowerM >= 2 {
				fmt.Printf("%v as %v: method %v may share its receiver, or is not known\n", t, iface, m.Sym)
			}
			v = false
			break
		}
	}
	if b.convOKs == nil {
		b.convOKs = make(map[convKey]bool)
	}
	b.convOKs[key] = v
	return v
}

// convMethod returns the function that the method sym of the type t is,
// or nil.
func convMethod(t *types.Type, sym *types.Sym) *ir.Name {
	mt := types.ReceiverBaseType(t)
	if mt == nil || mt.HasShape() {
		return nil
	}
	typecheck.CalcMethods(mt)
	for _, f := range mt.AllMethods() {
		if f.Sym != sym {
			continue
		}
		if !f.IsMethod() || f.Nointerface() || !types.IsMethodApplicable(t, f) {
			return nil
		}
		name, _ := f.Nname.(*ir.Name)
		return name
	}
	return nil
}

// methodKeepsLocal reports whether the method sym of the type t is known
// not to let another goroutine reach its receiver, or anything that is
// reached from it, and not to store it through another parameter.
func (b *batch) methodKeepsLocal(t *types.Type, sym *types.Sym) bool {
	name := convMethod(t, sym)
	if name == nil || name.Func == nil || len(name.Func.ParamShares) == 0 {
		// Not analysed (yet: ParamShares is set when the function's
		// batch is done), or not Go.
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
	return ps.Share == 0 && ps.Via&^1 == 0
}
