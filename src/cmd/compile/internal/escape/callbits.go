// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package escape

import (
	"fmt"
	"go/constant"

	"cmd/compile/internal/ir"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
)

// The bits of a call (runtime/shared.go, doc/gd/sharing.md).
//
// Whether an object is local is decided where it is allocated, and the
// function that allocates it does not know what its caller does with
// what it returns, nor with what it stores in a variable of the
// caller's: on a stack, where an address tells nothing. The caller
// knows, and says it to the function it calls directly:
//
//   - bit 0: nothing that is reached from the results of this call will
//     be shared. An allocation that the function only returns (shares.go:
//     attrReturned and nothing else) is local if the bit is set.
//   - bit k+1: nothing that is stored through argument k, the receiver
//     first, will be shared: the argument is the address of a variable,
//     or a pointer that only points to allocations of the caller's, that
//     nothing shared is reached from. A store through a parameter that
//     is never assigned is in a local container if the bit is set, or if
//     the parameter points to a local object.
//
// A caller that returns the results, or that passes on a parameter of
// its own, says what it was told.
//
// The function has the bits in a variable (ir.Func.LocalBitsVar), which
// the code generator sets when the function starts. A function that is
// not called directly, or by a caller that knows nothing, has zero.

// maxCallBits is the number of bits of a call.
const maxCallBits = 16

// callSite is a direct call of a function that may take the bits.
type callSite struct {
	call  *ir.CallExpr
	fn    *ir.Name // the function that is called
	curfn *ir.Func
	stmt  int32
	res   []*location // what the results flow through
	args  []callArg
}

type callArg struct {
	bit  int       // the bit of the argument
	kind int       // argAddr, argParam or argTracked
	name *ir.Name  // the variable
	loc  *location // its location
	own  int       // argParam: the bit of the parameter
}

const (
	argAddr    = iota // &x
	argParam          // a parameter of the caller's
	argTracked        // a tracked variable (shares.go)
)

func (b *batch) callBitsOn() bool {
	d := &b.gd.Debug
	if d.NoShare != 0 || d.NoLocal != 0 || d.NoLocalAlloc != 0 || d.NoLocalDyn != 0 || d.NoLocalBits != 0 {
		return false
	}
	_, ok := splitHeap(b.gd)
	return ok
}

// bitsVar returns the variable of fn that has the bits of the call.
func (b *batch) bitsVar(fn *ir.Func) *ir.Name {
	if fn.LocalBitsVar == nil {
		v := typecheck.TempAt(b.gd, fn.Pos(), fn, types.Types[types.TUINTPTR])
		fn.LocalBitsVar = v
		fn.TakesLocalBits = true
	}
	return fn.LocalBitsVar
}

// paramBit returns the bit of fn's parameter name, or 0.
func paramBit(fn *ir.Func, name *ir.Name) int {
	for i, f := range fn.Type().RecvParams() {
		if f.Nname == name {
			if i+1 < maxCallBits {
				return i + 1
			}
			return 0
		}
	}
	return 0
}

// noteCall records the direct call of fn, and returns the holes for its
// results: each flows through a location of its own, which says where
// the result goes.
func (e *escape) noteCall(ks []hole, call *ir.CallExpr, fn *ir.Name) (*callSite, []hole) {
	if call.GoDefer || fn.Func == nil || !e.callBitsOn() {
		return nil, ks
	}
	if !fn.Func.TakesLocalBits && !e.inMutualBatch(fn) {
		return nil, ks
	}
	if pkg := e.curfn.Sym().Pkg; pkg == nil || ir.UncheckedPkg(pkg.Path) {
		return nil, ks
	}
	cs := &callSite{call: call, fn: fn, curfn: e.curfn, stmt: e.curStmt}
	e.calls = append(e.calls, cs)
	if ks == nil {
		return cs, ks
	}
	nks := make([]hole, len(ks))
	for i, k := range ks {
		nks[i] = k
		if i >= fn.Type().NumResults() || !fn.Type().Result(i).Type.HasPointers() || k.derefs < 0 {
			continue
		}
		r := e.newLoc(nil, false)
		r.transient = true
		r.stmt = e.curStmt
		e.flow(k, r)
		cs.res = append(cs.res, r)
		nks[i] = r.asHole()
	}
	return cs, nks
}

// noteCallArg records the argument for the bit'th parameter of a call.
func (e *escape) noteCallArg(cs *callSite, bit int, arg ir.Node) {
	if cs == nil || bit+1 >= maxCallBits {
		return
	}
	if t := arg.Type(); t == nil || !t.IsPtr() && !t.IsSlice() {
		return
	}
	for {
		switch x := arg.(type) {
		case *ir.ParenExpr:
			arg = x.X
			continue
		case *ir.ConvExpr:
			if x.Op() == ir.OCONVNOP && x.X.Type().HasPointers() {
				arg = x.X
				continue
			}
		}
		break
	}
	local := func(n ir.Node) *ir.Name {
		x, ok := n.(*ir.Name)
		if !ok || x.Op() != ir.ONAME || x.Curfn != e.curfn || x.IsClosureVar() || x.Canonical() != x {
			return nil
		}
		if x.Class != ir.PAUTO && x.Class != ir.PPARAM {
			return nil
		}
		return x
	}
	a := callArg{bit: bit + 1}
	switch x := arg.(type) {
	case *ir.AddrExpr:
		if x.Op() != ir.OADDR {
			return
		}
		name := local(x.X)
		if name == nil {
			return
		}
		a.kind, a.name = argAddr, name
	case *ir.Name:
		name := local(x)
		if name == nil {
			return
		}
		a.name = name
		if name.Class == ir.PPARAM {
			a.kind = argParam
			if a.own = paramBit(e.curfn, name); a.own == 0 {
				return
			}
		} else if e.contents[name] != nil {
			a.kind = argTracked
		} else {
			return
		}
	default:
		return
	}
	a.loc = e.oldLoc(a.name)
	cs.args = append(cs.args, a)
}

// markReturnedOther marks the locations that flow, or lead, to a result
// of another function than their own otherwise than through a result of
// their own: the bit of a call of their function says nothing of those.
func (b *batch) markReturnedOther() {
	if !b.callBitsOn() {
		return
	}
	byFn := make(map[*ir.Func][]*location)
	for _, l := range b.allLocs {
		if l.paramOut {
			byFn[l.curfn] = append(byFn[l.curfn], l)
		}
	}
	for fn, roots := range byFn {
		seen := make(map[*location]bool)
		todo := append([]*location(nil), roots...)
		for _, r := range roots {
			seen[r] = true
		}
		visit := func(l *location) {
			if seen[l] {
				return
			}
			seen[l] = true
			if l.paramOut && l.curfn != fn {
				// What flows there is that function's result,
				// which a call's own location stands for.
				return
			}
			if l.curfn != fn {
				l.attrs |= attrReturnedOther
			}
			todo = append(todo, l)
		}
		for len(todo) > 0 {
			l := todo[len(todo)-1]
			todo = todo[:len(todo)-1]
			for _, e := range l.edges {
				visit(e.src)
			}
			for _, o := range l.owners {
				visit(o)
			}
		}
	}
}

// attrNotLocal are the attributes of a location that nothing local is
// reached from, whatever the caller says.
const attrNotLocal = attrShares | attrSharesValue | attrSharesDeep | attrReturnedOther

// stays reports whether nothing that is reached from the variable or the
// allocation of l is shared: always (ok), or if the results of l's
// function are not (ret).
func (b *batch) stays(l *location) (ok, ret bool) {
	if l == nil || l.hasAttr(attrNotLocal|attrDynShares|attrDynSharesValue|attrCandidateEscape) || len(l.dyn) != 0 {
		return false, false
	}
	ret = l.hasAttr(attrReturned)
	if l.heap {
		// It is in the heap: where it was decided to be.
		switch {
		case l.local == localStatic && !ret, l.local == localTagged && ret:
		default:
			return false, false
		}
	}
	return true, ret
}

// staysPointee is stays for all that the tracked variable name may point
// to, and what is stored through it.
func (b *batch) staysPointee(name *ir.Name, locOf map[ir.Node]*location, seen map[*ir.Name]bool) (ok, ret bool) {
	if seen[name] {
		return true, false
	}
	seen[name] = true
	c := b.contents[name]
	if c == nil || c.hasAttr(attrNotLocal|attrDynShares|attrDynSharesValue) || len(c.dyn) != 0 {
		return false, false
	}
	ret = c.hasAttr(attrReturned)
	var fresh func(n ir.Node) bool
	alloc := func(n ir.Node) bool {
		ok, r := b.stays(locOf[n])
		ret = ret || r
		return ok
	}
	fresh = func(n ir.Node) bool {
		switch n.Op() {
		case ir.ONIL:
			return true
		case ir.OCONVNOP, ir.OPAREN:
			switch n := n.(type) {
			case *ir.ConvExpr:
				return n.X.Type().HasPointers() && fresh(n.X)
			case *ir.ParenExpr:
				return fresh(n.X)
			}
		case ir.ONEW, ir.OPTRLIT, ir.OMAKESLICE, ir.OSLICELIT:
			return alloc(n)
		case ir.OADDR:
			if p := addrThrough(n.(*ir.AddrExpr)); p != nil {
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
			if b.contents[name] == nil {
				return false
			}
			ok, r := b.staysPointee(name, locOf, seen)
			ret = ret || r
			return ok
		case ir.OINLCALL:
			n := n.(*ir.InlinedCallExpr)
			if len(n.ReturnVars) == 1 {
				return fresh(n.ReturnVars[0])
			}
		}
		return false
	}
	for _, src := range b.sources[name] {
		if !fresh(src) {
			return false, false
		}
	}
	return true, ret
}

// callBits gives every recorded call the expression of its bits. It is
// called when the allocations are decided.
func (b *batch) callBits() {
	if len(b.calls) == 0 {
		return
	}
	locOf := make(map[ir.Node]*location)
	for _, l := range b.allLocs {
		if l.n != nil {
			locOf[l.n] = l
		}
	}
	uintptrType := types.Types[types.TUINTPTR]
	for _, cs := range b.calls {
		if cs.fn.Func == nil || !cs.fn.Func.TakesLocalBits {
			continue
		}
		pos := cs.call.Pos()
		lit := func(v int64) ir.Node {
			return ir.NewBasicLit(b.gd, pos, uintptrType, constant.MakeInt64(v))
		}
		binary := func(op ir.Op, x, y ir.Node) ir.Node {
			n := ir.NewBinaryExpr(b.gd, pos, op, x, y)
			n.SetType(uintptrType)
			n.SetTypecheck(1)
			return n
		}
		// own returns the caller's bit from, as the bit to.
		own := func(from, to int) ir.Node {
			var x ir.Node = b.bitsVar(cs.curfn)
			if from != 0 {
				x = binary(ir.ORSH, x, lit(int64(from)))
			}
			x = binary(ir.OAND, x, lit(1))
			if to != 0 {
				x = binary(ir.OLSH, x, lit(int64(to)))
			}
			return x
		}
		var static int64
		var dyn []ir.Node

		// The results.
		ok, ret := true, false
		var tag ir.Node
		for _, r := range cs.res {
			if r.hasAttr(attrNotLocal) {
				ok = false
				break
			}
			ret = ret || r.hasAttr(attrReturned)
			if len(r.dyn) != 0 {
				t := b.localTag(r, pos, false, true)
				if t == nil {
					ok = false
					break
				}
				if tag == nil {
					tag = t
				} else {
					tag = binary(ir.OAND, tag, t)
				}
			} else if r.hasAttr(attrDynShares | attrDynSharesValue) {
				ok = false
				break
			}
		}
		switch {
		case !ok:
		case !ret && tag == nil:
			static |= 1
		default:
			if ret {
				t := own(0, 0)
				if tag == nil {
					tag = t
				} else {
					tag = binary(ir.OAND, tag, t)
				}
			}
			dyn = append(dyn, tag)
		}

		// The arguments.
		for _, a := range cs.args {
			var ok, ret bool
			switch a.kind {
			case argAddr:
				ok, ret = b.stays(a.loc)
			case argTracked:
				ok, ret = b.staysPointee(a.name, locOf, make(map[*ir.Name]bool))
			case argParam:
				if l := a.loc; !l.reassigned && !l.addrtaken && !l.captured {
					dyn = append(dyn, own(a.own, a.bit))
				}
				continue
			}
			switch {
			case !ok:
			case ret:
				dyn = append(dyn, own(0, a.bit))
			default:
				static |= 1 << a.bit
			}
		}

		if static == 0 && len(dyn) == 0 {
			continue
		}
		var bits ir.Node
		if static != 0 {
			bits = lit(static)
		}
		for _, d := range dyn {
			if bits == nil {
				bits = d
			} else {
				bits = binary(ir.OOR, bits, d)
			}
		}
		cs.call.LocalBits = bits
		if b.gd.Flag.LowerM >= 3 {
			fmt.Printf("%s: call of %v has the bits %v\n", b.gd.FmtPos(pos), cs.fn, bits)
		}
	}
}

// Where a function's parameters go.
//
// The escape tag of a parameter says that what it points to is put in
// the heap, not whether another goroutine reaches it there: a function
// that stores an argument in what another argument points to, or in an
// object that it returns, would make everything shared that its caller
// passes. So a function also says, of each parameter (ir.ParamShare):
// the least number of dereferences that give what flows to where
// another goroutine may reach it, and the parameters through which it
// is stored. The caller's argument then flows to the heap that nobody
// reaches by itself (ownHeapLoc), for escaping, to shareLoc, and to
// what those arguments point to: as for a store of its own.
//
// Only a store through a parameter that is never assigned, and through
// it alone (p.f = x, *p = x), counts as such: what is deeper (p.f.g = x)
// may be reached from elsewhere than the caller's argument.

func (b *batch) shareTagsOn() bool {
	d := &b.gd.Debug
	return d.NoShare == 0 && d.NoLocal == 0 && d.NoLocalAlloc == 0 && d.NoLocalBits == 0
}

// appendHeapHole is where the elements of an appended slice flow, for
// the array that append may allocate.
func (e *escape) appendHeapHole() hole {
	if e.shareTagsOn() {
		return e.ownHeapHole()
	}
	return e.heapHole()
}

func setMinDerefs(p *uint8, derefs int) {
	v := derefs + 1
	if v > 255 {
		v = 255
	}
	if *p == 0 || uint8(v) < *p {
		*p = uint8(v)
	}
}

// noteParamRoot records that the parameter l's shares, dereferenced
// derefs times, is stored in the dynamic root r: through a parameter,
// or where another goroutine may reach it.
func (b *batch) noteParamRoot(fn *ir.Func, ps *ir.ParamShare, r *location, derefs int) {
	if d := r.dynRoot; d != nil && r.curfn == fn {
		if v := d.loc; d.plain && v != nil && v.param && !v.reassigned && !v.captured && !v.addrtaken {
			if k := paramBit(fn, d.name); k != 0 {
				ps.Via |= 1 << (k - 1)
				setMinDerefs(&ps.ViaDerefs, derefs)
				return
			}
		}
	}
	setMinDerefs(&ps.Share, derefs)
}

// noteParamHeap records what a location in the heap that the parameter
// flows to, dereferenced derefs times, says of where it goes: the walks
// from where that location flows stop there.
func (b *batch) noteParamHeap(fn *ir.Func, ps *ir.ParamShare, root *location, derefs int) {
	if root.hasAttr(attrSharesValue | attrSharesDeep) {
		setMinDerefs(&ps.Share, derefs)
	}
	if root.hasAttr(attrReturned) || root.paramOut {
		ps.Results = 0xff
	}
	for _, r := range root.dyn {
		b.noteParamRoot(fn, ps, r, derefs)
	}
}

// noteParamShare records that the parameter l, dereferenced derefs
// times, flows to root.
func (b *batch) noteParamShare(l, root *location, derefs int) {
	if !b.shareTagsOn() {
		return
	}
	ps := &l.shares
	switch {
	case root.isDynRoot:
		b.noteParamRoot(l.curfn, ps, root, derefs)
	case root.hasAttr(attrShares):
		setMinDerefs(&ps.Share, derefs)
	case root.paramOut && root.curfn == l.curfn && !root.hasAttr(attrEscapes):
		if ri := root.resultIndex - 1; ri < numEscResults && ri < 8 {
			ps.Results |= 1 << ri
		} else {
			// The escape tag says the heap.
			ps.Results = 0xff
		}
	case root.hasAttr(attrEscapes):
		b.noteParamHeap(l.curfn, ps, root, derefs)
	}
}

// paramShares records on fn what its parameters say.
func (b *batch) paramShares(fn *ir.Func) {
	if !b.shareTagsOn() || len(fn.Body) == 0 {
		return
	}
	params := fn.Type().RecvParams()
	ps := make([]ir.ParamShare, len(params))
	for i, f := range params {
		if f.Nname == nil || !f.Type.HasPointers() {
			continue
		}
		loc := b.oldLoc(f.Nname.(*ir.Name))
		ps[i] = loc.shares
		if loc.hasAttr(attrShares | attrSharesValue) {
			// What the parameter holds is reached.
			setMinDerefs(&ps[i].Share, 0)
		}
		if loc.hasAttr(attrEscapes) {
			// The variable is in the heap.
			b.noteParamHeap(fn, &ps[i], loc, 0)
		}
	}
	fn.ParamShares = ps
}

// sharedTagHoles returns the holes for an argument of the function fn's
// parameter param, which the function puts in the heap dereferenced x
// times, as its tag esc says.
func (e *escape) sharedTagHoles(ks []hole, fn *ir.Name, param *types.Field, esc leaks, x int) []hole {
	var ps *ir.ParamShare
	if fn.Func != nil && fn.Func.ParamShares != nil && e.shareTagsOn() {
		for i, f := range fn.Type().RecvParams() {
			if f == param && i < len(fn.Func.ParamShares) {
				ps = &fn.Func.ParamShares[i]
			}
		}
	}
	if ps == nil {
		return []hole{e.heapHole().shift(e.gd, x)}
	}
	tagKs := []hole{e.ownHeapHole().shift(e.gd, x)}
	if ps.Share != 0 {
		tagKs = append(tagKs, e.shareHole().shift(e.gd, int(ps.Share)-1))
	}
	if ps.Via != 0 {
		d := int(ps.ViaDerefs) - 1
		for k := 0; k < 16; k++ {
			if ps.Via&(1<<k) == 0 {
				continue
			}
			if k >= len(e.callArgs) {
				tagKs = append(tagKs, e.shareHole().shift(e.gd, d))
				break
			}
			tagKs = append(tagKs, e.storeThroughArg(e.callArgs[k]).shift(e.gd, d))
		}
	}
	if ks != nil {
		for i := 0; i < numEscResults && i < len(ks); i++ {
			if ps.Results&(1<<i) != 0 && esc.Result(i) < 0 {
				tagKs = append(tagKs, ks[i].shift(e.gd, x))
			}
		}
	}
	return tagKs
}

// storeThroughArg returns the hole for what a function stores through
// its parameter, for the argument p: see storeThrough.
func (e *escape) storeThroughArg(p ir.Node) hole {
	for {
		switch x := p.(type) {
		case *ir.ParenExpr:
			p = x.X
			continue
		case *ir.ConvExpr:
			if x.Op() == ir.OCONVNOP && x.X.Type().HasPointers() {
				p = x.X
				continue
			}
		case *ir.AddrExpr:
			// A store in the variable.
			if name, ok := x.X.(*ir.Name); ok && x.Op() == ir.OADDR && name.Op() == ir.ONAME && name.Curfn == e.curfn &&
				(name.Class == ir.PAUTO || name.Class == ir.PPARAM) && !name.IsClosureVar() && name.Canonical() == name {
				return e.oldLoc(name).asHole()
			}
		}
		break
	}
	if t := p.Type(); t == nil || !t.IsPtr() && !t.IsSlice() {
		return e.heapHole()
	}
	return e.storeThrough(p)
}
