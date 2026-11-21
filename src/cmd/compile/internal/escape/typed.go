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
// parameter flows to a share root of the parameter's type
// (shareHoleOf), and the walk from that root goes by the types of the
// locations that it comes to with nothing dereferenced (typedAt):
//
//   - One whose value may be the shared pointer, or a struct that holds
//     it, is shared for that type (location.shareOnly), and the walk
//     from it goes by the type in turn.
//   - One whose value points to a larger object that the shared one may
//     be a part of: the walk goes on for the larger type.
//   - An object whose address is the shared value is shared with all
//     that is in it, whatever its type: a shared object points to no
//     local one. So is what flows to an interface or an
//     unsafe.Pointer, which say nothing, and to a location in a
//     function that makes pointers of unsafe.Pointer (usesUnsafe).
//   - One that can be neither, but may lead to such a value, leads to
//     what is shared and is not shared itself (filterDeep); the walk
//     goes on to mark what else may.
//   - One that cannot lead to such a value is not shared, and the walk
//     does not go on through it (filterBlocked).
//
// That is sound only where a flow without dereference is of the same
// value, or of one that the destination holds beside others. The graph
// has such flows for what a function that is called did with an
// argument, where it does not see the object: a parameter that the
// function stores in what it returns flows to the result, and so
// does one that it stores, however deep, in what another parameter
// points to. The body that http.NewRequest stores in the request was
// local in a request that was shared, before this was told apart. Such
// a flow is loose (hole.loose), and what comes over one is shared with
// everything behind it, unless the function says that it stores the
// parameter's own value as a field or an element of the object that
// the other parameter points to (ir.ParamShare.ViaDirect): the walk
// from the place of such a store finds whether a parameter comes to it
// by itself, through variables that only pass a value on (carrier).
//
// A function says of its parameters what a caller can go by
// (ir.ParamShare.ShareTyped): of one that it shares itself, that the
// value shared is the argument's, of its type; of one that is shared
// one dereference down, which field of the parameter's struct the
// shared value is in (shareFieldOf: the export data cannot name a
// type), if all the fields that could hold it have one type. Not if
// the function, or one that it calls and shares by, makes pointers of
// unsafe.Pointer (ir.Func.ShareOpaque): what such a function shares,
// types do not say.
//
// An allocation that is only shared for a type, and not by its
// address, holds values of that type or leads to them, and may be
// local itself (markLocal).
//
// Off with -d=nolocaltyped=1.

const (
	filterOff     int8 = iota // everything is shared
	filterOn                  // only what may be of the walk's type
	filterDeep                // nothing: it may lead to what is shared
	filterBlocked             // nothing: behind a value of another type
)

func (b *batch) typedOn() bool {
	return b.gd.Debug.NoLocalTyped == 0 && b.shareTagsOn()
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

// valueType returns the type of the value of the location l, or of its
// address with addr: for an expression that allocates, the value is
// what the object holds and the address is the expression's. It is nil
// where nothing is known.
func (b *batch) valueType(l *location, addr bool) *types.Type {
	if l.n == nil || l.curfn == nil || b.unsafeFns[l.curfn] {
		return nil
	}
	t := l.n.Type()
	if t == nil {
		return nil
	}
	if l.n.Op() == ir.ONAME {
		if addr {
			return types.NewPtr(t)
		}
		return t
	}
	if addr {
		return t
	}
	switch l.n.Op() {
	case ir.OPTRLIT, ir.ONEW, ir.OMAKESLICE, ir.OSLICELIT, ir.OAPPEND:
		if t.IsPtr() || t.IsSlice() {
			return t.Elem()
		}
		return nil
	case ir.OMAKEMAP, ir.OMAPLIT, ir.OCLOSURE, ir.OMETHVALUE, ir.OCONVIFACE, ir.OMAKECHAN:
		return nil
	}
	return t
}

// typedAt returns, for the location l that a walk which shares the
// values of type t has reached with nothing dereferenced, what the walk
// shares of l (at) and of what flows to it (eff):
//
//   - filterOn, filterOn: l's value may be the shared one, or l holds
//     it beside others;
//   - filterOn, filterOff: l's value may be the shared one or be in
//     what it leads to, and what flows to l is in the shared object, or
//     nothing says what it is (an interface, a larger object that the
//     shared one may be a part of);
//   - filterOn, filterOn and a type: l's value points to a larger
//     object that the shared one may be a part of, and the walk goes
//     on for values of that type;
//   - filterDeep: l is not shared, and may lead to what is;
//   - filterBlocked: neither.
func (b *batch) typedAt(l *location, addr bool, t *types.Type) (at, eff int8, wider *types.Type) {
	v := b.valueType(l, addr)
	if v == nil {
		if addr {
			// An object that what is shared points to.
			return filterOn, filterOff, nil
		}
		return filterOn, filterOn, nil
	}
	if v.HasShape() || t.HasShape() {
		return filterOn, filterOff, nil
	}
	switch v.Kind() {
	case types.TINTER, types.TUNSAFEPTR, types.TUINTPTR:
		return filterOn, filterOff, nil
	case types.TSTRUCT, types.TARRAY:
		switch {
		case b.leadsTo(t, v):
			return filterOn, filterOff, nil
		case mayBe(v, t):
			return filterOn, filterOn, nil
		case b.leadsTo(v, t):
			return filterDeep, filterDeep, nil
		}
		return filterBlocked, filterBlocked, nil
	}
	if !v.HasPointers() {
		return filterBlocked, filterBlocked, nil
	}
	pointer := func(x *types.Type) bool { return x.Kind() == types.TPTR || x.Kind() == types.TSLICE }
	if pointer(v) && pointer(t) {
		x, y := v.Elem(), t.Elem()
		switch {
		case sameUnder(x, y) || holds(y, x):
			// The shared pointer, or one into its object.
			if addr {
				return filterOn, filterOff, nil
			}
			return filterOn, filterOn, nil
		case holds(x, y):
			// A larger object that the shared one is a part of: it
			// is the object that is shared, with all that it has.
			if addr || !filterType(v) {
				return filterOn, filterOff, nil
			}
			return filterOn, filterOn, v
		}
	} else if !pointer(v) && !pointer(t) && sameUnder(v, t) {
		if addr {
			return filterOn, filterOff, nil
		}
		return filterOn, filterOn, nil
	}
	switch {
	case b.leadsTo(t, v):
		// It may be in what the shared value leads to.
		return filterOn, filterOff, nil
	case b.leadsTo(v, t):
		return filterDeep, filterDeep, nil
	}
	return filterBlocked, filterBlocked, nil
}

// typedLeads reports whether the location l, which is not what is
// shared, may lead to a value of type t.
func (b *batch) typedLeads(l *location, addr bool, t *types.Type) bool {
	v := b.valueType(l, addr)
	if v == nil {
		return true
	}
	return b.leadsTo(v, t)
}

// carrier reports whether the location l only passes its value on: a
// variable that is no struct or array, or a temporary of the graph.
func carrier(l *location) bool {
	if l.n == nil {
		return l.transient && l.callRes == nil && !l.isDynRoot && len(l.owners) == 0
	}
	if l.n.Op() != ir.ONAME {
		return false
	}
	t := l.n.Type()
	return t != nil && !t.IsStruct() && !t.IsArray()
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
	if sameUnder(a, t) {
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

// usesUnsafe reports whether fn makes a pointer of an unsafe.Pointer or
// of an integer, or with the functions of unsafe.
func usesUnsafe(fn *ir.Func) bool {
	found := false
	ir.VisitList(fn.Body, func(n ir.Node) {
		switch n.Op() {
		case ir.OCONVNOP, ir.OCONV:
			// A pointer that is made of an unsafe.Pointer, or an
			// unsafe.Pointer of an integer. (A pointer that is
			// only converted to unsafe.Pointer, to be passed on,
			// keeps its type where it is, and the unsafe.Pointer
			// is taken for anything.)
			n := n.(*ir.ConvExpr)
			from, to := n.X.Type(), n.Type()
			if from == nil || to == nil {
				break
			}
			if from.IsUnsafePtr() && !to.IsUnsafePtr() || to.IsUnsafePtr() && !from.IsUnsafePtr() && !from.IsPtr() {
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
// param of the function fn, of which fn says ps: where another
// goroutine reaches what it shares of the argument.
func (e *escape) shareHoleFor(fn *ir.Name, param *types.Field, ps *ir.ParamShare, conv bool) hole {
	var t *types.Type
	if ps.ShareTyped {
		switch ps.Share {
		case 1:
			t = param.Type
		case 2:
			t = shareTypeOf(param.Type, ps.ShareField)
		}
	}
	if fn == nil || fn.Func == nil || fn.Func.ShareOpaque || fn.Sym().Pkg == nil || ir.UncheckedPkg(fn.Sym().Pkg.Path) {
		// What it shares, types do not say: nor of what its
		// caller shares by it.
		t = nil
		if e.opaqueFns == nil {
			e.opaqueFns = make(map[*ir.Func]bool)
		}
		e.opaqueFns[e.curfn] = true
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

// opaqueBatch reports whether a function of the batch does with
// pointers what their types do not say, or calls one that does and
// shares by it.
func (b *batch) opaqueBatch() bool {
	if b.opaque == 0 {
		b.opaque = 1
		for _, fn := range b.fns {
			if b.unsafeFns[fn] || b.opaqueFns[fn] {
				b.opaque = 2
			}
		}
	}
	return b.opaque == 2
}
