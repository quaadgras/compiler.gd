// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package escape

import (
	"cmd/compile/internal/ir"
	"cmd/compile/internal/types"
)

// Sharing by type (doc/gd/sharing.md).
//
// A function that shares what its parameter holds said so by depth
// only: the scanner's methods share the scanner's file, which has a
// mutex, and so everything that is stored in a scanner was shared, the
// closure that it reports errors to with it. A local object may point
// to shared ones, and what else it points to is not shared by that.
//
// So a value that is shared because a function says so of its
// parameter flows to a share root of the parameter's type, and in the
// walk from that root a location whose value cannot be of that type,
// nor hold one, is not shared, and the walk does not go on through it
// for sharing (filterBlocked). Once the walk has an object whose
// address is the shared value, all that is in the object is shared,
// whatever its type (filterOff): a shared object points to no local
// one. A location that is shared only by such a walk is shared for
// that type only (location.shareOnly), which the walk from it goes by
// in turn. A function says the same of its parameters
// (ir.ParamShare.ShareTyped).
//
// The types are those of the variables and the expressions, which is
// no ground in a function that converts pointers through
// unsafe.Pointer: its locations have no type for this.
//
// NOT SOUND AS IT IS, and off unless -d=localtyped=1. A flow without
// dereference is taken to be of the same value, so that a location of
// another type is not the shared one. But the graph has such flows for
// what is stored in the object that a value points to, where it does
// not see the object: a parameter that a function stores in what it
// returns flows to the result. The body of a request, which
// http.NewRequest stores in the request, was local in a request that
// was shared (net/http's tests, with GODEBUG=sharestats=2). The walk
// has to know whether it is at the shared pointer, where a value of
// another type that flows in is stored in the object and shared with
// it, or among what a holder has, where one is another value.

const (
	filterOff     int8 = iota // everything is shared
	filterOn                  // only what may be of the walk's type
	filterBlocked             // nothing: behind a value of another type
)

func (b *batch) typedOn() bool {
	return b.gd.Debug.LocalTyped != 0 && b.shareTagsOn()
}

// shareHoleOf returns the hole for a value of type t that another
// goroutine may reach, as shareHole, or convShareHole with conv.
func (e *escape) shareHoleOf(t *types.Type, conv bool) hole {
	if !e.typedOn() || !filterType(t) {
		if conv {
			return e.convShareHole()
		}
		return e.shareHole()
	}
	type key struct {
		t    *types.Type
		conv bool
	}
	if e.typedShares == nil {
		e.typedShares = make(map[any]*location)
	}
	for k, r := range e.typedShares {
		if k := k.(key); k.conv == conv && types.Identical(k.t, t) {
			return r.asHole()
		}
	}
	r := &location{attrs: attrShares, shareType: t, shareConv: conv, loopDepth: 1}
	e.typedShares[key{t, conv}] = r
	e.typedShareList = append(e.typedShareList, r)
	return r.asHole()
}

// filterType reports whether sharing can be told apart for values of
// type t: a pointer, a slice, a map, a channel or a function. An
// interface holds anything.
func filterType(t *types.Type) bool {
	if t == nil || t.HasShape() {
		return false
	}
	switch t.Kind() {
	case types.TPTR, types.TSLICE:
		return !t.Elem().HasShape()
	case types.TMAP, types.TCHAN, types.TFUNC:
		return true
	}
	return false
}

// isShareRoot reports whether l is where another goroutine reaches,
// for all values or for those of one type; conv, as convShareLoc.
func (b *batch) isShareRoot(l *location) (yes, conv bool) {
	switch {
	case l == &b.shareLoc:
		return true, false
	case l == &b.convShareLoc:
		return true, true
	case l.shareType != nil:
		return true, l.shareConv
	}
	return false, false
}

// typedAs reports, of the location l in a walk that shares the values
// of type t, whether l's value (or its address, with addr) may be such
// a value, and whether it may lead to one.
func (b *batch) typedAs(l *location, addr bool, t *types.Type) (is, leads bool) {
	if l.n == nil || l.curfn == nil || b.unsafeFns[l.curfn] {
		return true, true
	}
	lt := l.n.Type()
	if lt == nil {
		return true, true
	}
	if addr && l.n.Op() == ir.ONAME {
		// The address of a variable. (An expression that
		// allocates has the type of the pointer.)
		lt = types.NewPtr(lt)
	}
	if mayBe(lt, t) {
		return true, true
	}
	return false, b.leadsTo(lt, t)
}

// leadsTo reports whether a value of type a may lead to one of type t:
// be one, or point to memory that has one, however far.
func (b *batch) leadsTo(a, t *types.Type) bool {
	type key struct{ a, t *types.Type }
	if v, ok := b.leads[key{a, t}]; ok {
		return v
	}
	seen := make(map[*types.Type]bool)
	var walk func(x *types.Type) bool
	walk = func(x *types.Type) bool {
		if x == nil || x.HasShape() {
			return true
		}
		if !x.HasPointers() || seen[x] {
			return false
		}
		seen[x] = true
		if mayBe(x, t) {
			return true
		}
		switch x.Kind() {
		case types.TPTR, types.TSLICE, types.TARRAY, types.TCHAN:
			return walk(x.Elem())
		case types.TMAP:
			return walk(x.Key()) || walk(x.Elem())
		case types.TSTRUCT:
			for _, f := range x.Fields() {
				if walk(f.Type) {
					return true
				}
			}
			return false
		case types.TSTRING:
			return false
		}
		// A function (what it has captured), an interface.
		return true
	}
	v := walk(a)
	if b.leads == nil {
		b.leads = make(map[any]bool)
	}
	b.leads[key{a, t}] = v
	return v
}

// mayBe reports whether a value of type a may be one of type t, point
// into one or to what one points into, or hold one.
func mayBe(a, t *types.Type) bool {
	if a == nil || t == nil || a.HasShape() || t.HasShape() {
		return true
	}
	if !a.HasPointers() {
		return false
	}
	switch a.Kind() {
	case types.TINTER, types.TUNSAFEPTR:
		return true
	case types.TSTRUCT:
		for _, f := range a.Fields() {
			if mayBe(f.Type, t) {
				return true
			}
		}
		return false
	case types.TARRAY:
		return mayBe(a.Elem(), t)
	}
	switch t.Kind() {
	case types.TINTER, types.TUNSAFEPTR:
		return true
	}
	pointer := func(x *types.Type) bool { return x.Kind() == types.TPTR || x.Kind() == types.TSLICE }
	switch {
	case pointer(a) && pointer(t):
		// The same object, or one inside the other (&x.f, &s[i]).
		x, y := a.Elem(), t.Elem()
		return sameUnder(x, y) || holds(x, y) || holds(y, x)
	case pointer(a) || pointer(t):
		return false
	}
	// Maps, channels, functions, strings.
	return sameUnder(a, t)
}

func sameUnder(x, y *types.Type) bool {
	return types.Identical(x.Underlying(), y.Underlying())
}

// holds reports whether a value of type s has one of type f in it.
func holds(s, f *types.Type) bool {
	switch s.Kind() {
	case types.TSTRUCT:
		for _, x := range s.Fields() {
			if sameUnder(x.Type, f) || holds(x.Type, f) {
				return true
			}
		}
	case types.TARRAY:
		return sameUnder(s.Elem(), f) || holds(s.Elem(), f)
	}
	return false
}

// usesUnsafe reports whether fn converts a pointer through
// unsafe.Pointer, or makes one with the functions of unsafe.
func usesUnsafe(fn *ir.Func) bool {
	found := false
	ir.VisitList(fn.Body, func(n ir.Node) {
		switch n.Op() {
		case ir.OCONVNOP, ir.OCONV:
			n := n.(*ir.ConvExpr)
			if n.Type().IsUnsafePtr() || n.X.Type() != nil && n.X.Type().IsUnsafePtr() {
				found = true
			}
		case ir.OUNSAFEADD, ir.OUNSAFESLICE, ir.OUNSAFESTRING, ir.OUNSAFESLICEDATA, ir.OUNSAFESTRINGDATA:
			found = true
		}
	})
	return found
}

// maxShareField is the number of steps of ir.ParamShare.ShareField.
const maxShareField = 4

// shareFieldOf returns the steps from a parameter's type p to the
// field that a shared value of type t is in, where the parameter points
// to a struct and the value is what a field of the struct holds (the
// parameter is shared when it is dereferenced once): the fields whose
// values may be of type t, or point into one, must all have one type,
// which is then the type that the caller goes by. It holds every value
// that the function shares, since the struct has no other place for
// one.
func shareFieldOf(p, t *types.Type) ([]uint8, bool) {
	if !p.IsPtr() || p.Elem().Kind() != types.TSTRUCT {
		return nil, false
	}
	var found *types.Type
	var path []uint8
	ok := true
	var walk func(x *types.Type, at []uint8)
	walk = func(x *types.Type, at []uint8) {
		for x.Kind() == types.TARRAY {
			x = x.Elem()
		}
		if x.Kind() != types.TSTRUCT {
			if !mayBe(x, t) {
				return
			}
			switch {
			case !filterType(x):
				ok = false
			case found == nil:
				found, path = x, append([]uint8(nil), at...)
			case !types.Identical(found, x):
				ok = false
			}
			return
		}
		fields := x.Fields()
		if len(fields) > 255 || len(at) >= maxShareField {
			if mayBe(x, t) {
				ok = false
			}
			return
		}
		for i, f := range fields {
			walk(f.Type, append(at, uint8(i)))
		}
	}
	walk(p.Elem(), nil)
	if !ok || found == nil {
		return nil, false
	}
	return path, true
}

// shareTypeOf returns the type that the steps path lead to from the
// parameter's type p, or nil.
func shareTypeOf(p *types.Type, path []uint8) *types.Type {
	if len(path) == 0 || !p.IsPtr() {
		return nil
	}
	x := p.Elem()
	for _, i := range path {
		for x.Kind() == types.TARRAY {
			x = x.Elem()
		}
		if x.Kind() != types.TSTRUCT || int(i) >= x.NumFields() {
			return nil
		}
		x = x.Field(int(i)).Type
	}
	if len(path) == 0 {
		return nil
	}
	return x
}

// noteShare records that the parameter of ps, dereferenced derefs
// times, is where another goroutine may reach it: only for values of
// type t, if t is not nil.
func noteShare(ps *ir.ParamShare, derefs int, t *types.Type) {
	setMinDerefs(&ps.Share, derefs)
	if derefs < 1 {
		return
	}
	switch {
	case t == nil:
		ps.ShareAny = true
	case ps.ShareType == nil:
		ps.ShareType = t
	case !types.Identical(ps.ShareType, t):
		ps.ShareAny = true
	}
}

// shareHoleFor returns the hole for an argument of the parameter
// param, of which the function called says ps: where another goroutine
// reaches what it shares of the argument.
func (e *escape) shareHoleFor(param *types.Field, ps *ir.ParamShare, conv bool) hole {
	var t *types.Type
	switch {
	case ps.Share == 1:
		t = param.Type
	case ps.ShareTyped:
		t = shareTypeOf(param.Type, ps.ShareField)
	}
	var k hole
	if t != nil {
		k = e.shareHoleOf(t, conv)
	} else if conv {
		k = e.convShareHole()
	} else {
		k = e.shareHole()
	}
	return k.shift(e.gd, int(ps.Share)-1)
}
