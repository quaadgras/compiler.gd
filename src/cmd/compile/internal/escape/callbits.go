// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package escape

import (
	"fmt"
	"go/constant"
	"internal/abi"

	"cmd/compile/internal/ir"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
	"cmd/internal/obj"
	"cmd/internal/src"
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

// callSite is a call of a function that may take the bits.
type callSite struct {
	call  *ir.CallExpr
	fn    *ir.Name // the function that is called, or nil if it is not known
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

// noteCall records the call of fn, or of a function that is only known
// when the program runs if fn is nil, and returns the holes for its
// results: each flows through a location of its own, which says where
// the result goes.
func (e *escape) noteCall(ks []hole, call *ir.CallExpr, fn *ir.Name, fntype *types.Type) (*callSite, []hole) {
	if call.GoDefer || !e.callBitsOn() {
		return nil, ks
	}
	if fn != nil && (fn.Func == nil || !fn.Func.TakesLocalBits && !e.inMutualBatch(fn)) {
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
		if i >= fntype.NumResults() || !fntype.Result(i).Type.HasPointers() || k.derefs < 0 {
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
	if t := arg.Type(); t == nil || !t.IsPtr() && !t.IsSlice() && !(t.IsMap() && e.mapsOn()) {
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
				if !e.ro {
					visit(e.src)
				}
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
		case ir.ONEW, ir.OPTRLIT, ir.OMAKESLICE, ir.OSLICELIT, ir.OMAKEMAP, ir.OMAPLIT:
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
		if cs.fn != nil && (cs.fn.Func == nil || !cs.fn.Func.TakesLocalBits) {
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
	if d := r.dynRoot; d != nil && d.link != "" {
		// An argument of a function that is only known when the
		// program runs: the caller reads what the linker knows.
		v := derefs + 1
		if v > 255 {
			v = 255
		}
		for i := range ps.Links {
			if l := &ps.Links[i]; l.Key == d.link && int(l.Arg) == d.arg {
				if uint8(v) < l.Derefs {
					l.Derefs = uint8(v)
				}
				return
			}
		}
		if len(ps.Links) < ir.MaxShareLinks {
			ps.Links = append(ps.Links, ir.ShareLink{Key: d.link, Arg: uint8(d.arg), Derefs: uint8(v)})
			return
		}
		setMinDerefs(&ps.Share, derefs)
		return
	}
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

// viaParam reports whether root is the dynamic root of a store through
// a parameter of fn that is never assigned, and returns that
// parameter's bit.
func viaParam(fn *ir.Func, root *location) int {
	if d := root.dynRoot; d != nil && root.curfn == fn {
		if v := d.loc; d.plain && v != nil && v.param && !v.reassigned && !v.captured && !v.addrtaken {
			return paramBit(fn, d.name)
		}
	}
	return 0
}

// noteParamLeak records that the parameter l, dereferenced derefs
// times, flows to root, which outlives it: unless root is a result that
// the escape tag has, or a store through a parameter, what flows must
// be in the heap.
func (b *batch) noteParamLeak(l, root *location, derefs int) {
	if !b.shareTagsOn() {
		return
	}
	if !root.hasAttr(attrEscapes) && root.isName(ir.PPARAMOUT) && root.curfn == l.curfn && root.resultIndex-1 < numEscResults {
		return
	}
	if viaParam(l.curfn, root) != 0 {
		return
	}
	setMinDerefs(&l.shares.Heap, derefs)
}

// paramShares records on fn what its parameters say.
func (b *batch) paramShares(fn *ir.Func) {
	if !b.shareTagsOn() || len(fn.Body) == 0 {
		return
	}
	// Its integers are pointers that its callers keep in the heap, as
	// the tags say.
	if fn.Pragma&(ir.UintptrEscapes|ir.UintptrKeepAlive) != 0 {
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
			setMinDerefs(&ps[i].Heap, 0)
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
	// A function that keeps the parameter only by calls that check
	// (ir.Func.KeptChecked) puts it in the heap, where anyone may
	// reach it, but for an object that is handed over later.
	checked := x == 0 && e.checkedArg
	if ps == nil {
		if checked {
			// Nothing says where it goes for sharing: as if a
			// call checked.
			calls := e.checkCallsArg
			e.checkCallsArg = true
			hs := e.checkHoles()
			e.checkCallsArg = calls
			return hs
		}
		return []hole{e.heapHole().shift(e.gd, x)}
	}
	// What the heap of the tag stood for.
	tagKs := []hole{e.mutatorHole().shift(e.gd, x), e.calleeHole().shift(e.gd, x)}
	if checked {
		tagKs = append(tagKs, e.checkHoles()...)
	}
	if !checked || !e.checkCallsArg && e.recvOn() {
		if ps.Heap != 0 && !checked {
			tagKs = append(tagKs, e.ownHeapHole().shift(e.gd, int(ps.Heap)-1))
		}
		if ps.Share != 0 {
			tagKs = append(tagKs, e.shareHole().shift(e.gd, int(ps.Share)-1))
		}
		for _, l := range ps.Links {
			// (Whether it must be in the heap, the tag and Heap say.)
			if r := e.linkRootOf(l.Key, int(l.Arg), false); r != nil {
				tagKs = append(tagKs, r.asHole().shift(e.gd, int(l.Derefs)-1))
			} else {
				tagKs = append(tagKs, e.shareHole().shift(e.gd, int(l.Derefs)-1))
			}
		}
	}
	if ps.Via != 0 {
		d := int(ps.ViaDerefs) - 1
		for k := 0; k < 16; k++ {
			if ps.Via&(1<<k) == 0 {
				continue
			}
			if k >= len(e.callArgs) {
				tagKs = append(tagKs, e.heapHole().shift(e.gd, d))
				break
			}
			// A store through the argument: the heap, unless
			// the argument is the address of a variable, which
			// then holds what is stored for as long as it lives.
			tagKs = append(tagKs, e.storeThroughArg(e.callArgs[k]).shift(e.gd, d))
		}
	}
	for i := range ks {
		all := ps.Results == 0xff
		if (all || i < 8 && ps.Results&(1<<i) != 0) && (i >= numEscResults || esc.Result(i) < 0) {
			k := ks[i].shift(e.gd, x)
			k.recvRes = checked
			tagKs = append(tagKs, k)
		}
	}
	return tagKs
}

// shareOnlyHoles returns the holes for an argument of the function fn's
// parameter param that the function does not put in the heap: where
// another goroutine may reach it, as the parameter says.
func (e *escape) shareOnlyHoles(fn *ir.Name, param *types.Field) []hole {
	if fn.Func == nil || fn.Func.ParamShares == nil || !e.shareTagsOn() {
		return nil
	}
	var ps *ir.ParamShare
	for i, f := range fn.Type().RecvParams() {
		if f == param && i < len(fn.Func.ParamShares) {
			ps = &fn.Func.ParamShares[i]
		}
	}
	if ps == nil {
		return nil
	}
	// (Nothing keeps the pointer: it is shared as what is converted
	// to an interface is, and no holder for a hand-over.)
	var hs []hole
	if ps.Share != 0 {
		hs = append(hs, e.convShareHole().shift(e.gd, int(ps.Share)-1))
	}
	for _, l := range ps.Links {
		if r := e.linkRootOf(l.Key, int(l.Arg), false); r != nil {
			hs = append(hs, r.asHole().shift(e.gd, int(l.Derefs)-1))
		} else {
			hs = append(hs, e.convShareHole().shift(e.gd, int(l.Derefs)-1))
		}
	}
	return hs
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
	if t := p.Type(); t == nil || !t.IsPtr() && !t.IsSlice() && !(t.IsMap() && e.mapsOn()) {
		return e.heapHole()
	}
	return e.storeThrough(p)
}

// shareNote returns the escape note of fn's i'th parameter, whose leaks
// are esc, with what the parameter's ParamShare says for the escape
// masks (ir.EscMaskShared): the callers of a function that is only known
// when the program runs read it there.
func (b *batch) shareNote(fn *ir.Func, i int, esc leaks) string {
	note := esc.Encode()
	if fl := b.shareFlags(fn, i, esc); fl != 0 {
		return "esc:" + string(esc[:]) + string([]byte{fl})
	}
	return note
}

// shareFlags returns the byte that follows the leaks in the note, or 0
// if the note has none.
func (b *batch) shareFlags(fn *ir.Func, i int, esc leaks) byte {
	// Kept only by calls that check, which the call that tests for
	// it reads.
	// Kept by a method of its own is said of a parameter of an
	// interface type only, whose methods the caller knows: of a
	// pointer that the function converts, it knows nothing.
	var checked byte
	if i < 16 && fn.KeptChecked&^fn.KeptByAny&(1<<i) != 0 && esc.Heap() == 0 && !esc.anyResult() {
		if params := fn.Type().RecvParams(); i < len(params) && (params[i].Type.IsInterface() || fn.KeptByRecv&(1<<i) == 0) {
			checked = ir.EscNoteChecked
		}
	}
	if fn.ParamShares == nil || i >= len(fn.ParamShares) || esc.Heap() < 0 && !esc.anyResult() {
		return checked
	}
	ps := fn.ParamShares[i]
	// (What is stored through the parameter itself is where it is.)
	via := ps.Via &^ (1 << i)
	fl := byte(ir.EscNoteStays)
	if fn.Type().Recv() != nil && i != 0 && via&1 != 0 {
		via &^= 1
		fl |= ir.EscNoteViaRecv
	}
	if ps.Share != 0 || ps.Results != 0 || via != 0 || esc.anyResult() {
		return checked
	}
	if len(ps.Links) != 0 {
		// Not by itself, but the functions that it is passed on to
		// may share it: a mask says that it is shared, and the
		// linker starts from one that does not (ir.EscMaskBaseOf).
		return fl&^1 | ir.EscNoteLinked | checked
	}
	return fl | checked
}

func (l leaks) anyResult() bool {
	for i := range numEscResults {
		if l.Result(i) >= 0 {
			return true
		}
	}
	return false
}

// A call of a function that is only known when the program runs.
//
// An argument of such a call flows to the heap, and what it points to
// is shared: the function may do anything with it. But the function's
// escape mask, which the closure or the method table has, says what it
// does (ir.EscMaskShared), and an object that is allocated in the
// arguments of the call, in the call's statement, is allocated when the
// function is known: it is local if the function does not keep the
// argument, or keeps it where no other goroutine reaches, which may be
// what its receiver points to if that is a local object.
//
// So an argument flows to a dynamic root of its own (shares.go), whose
// tag is read from the mask. What was allocated before, or flows
// elsewhere, cannot tell and is shared.

// callArgRoot returns the dynamic root of the idx'th argument of call,
// or nil if the function that is called cannot be read where the
// arguments are evaluated: it must be a local variable, or the method of
// an interface that is one.
func (e *escape) callArgRoot(call *ir.CallExpr, idx int) *location {
	if !e.callBitsOn() || call.GoDefer || idx >= ir.EscMaskArgs {
		return nil
	}
	fun := call.Fun
	if call.Op() == ir.OCALLINTER {
		sel, ok := fun.(*ir.SelectorExpr)
		if !ok || sel.Op() != ir.ODOTINTER {
			return nil
		}
		fun = sel.X
		if !fun.Type().IsInterface() {
			return nil
		}
	}
	name, plain := e.purePath(fun)
	if name == nil || !plain {
		return nil
	}
	key := dynKey{e.curStmt, fmt.Sprintf("call %p %d", call, idx)}
	if r := e.dynRoots[key]; r != nil {
		return r
	}
	r := &location{
		curfn:     e.curfn,
		loopDepth: 1,
		attrs:     attrEscapes | attrPersists | attrMutates | attrCalls | attrDynShares,
		dynRoot:   &dynRoot{path: fun, name: name, stmt: key.stmt, call: call, arg: idx, link: e.dynKey},
		isDynRoot: true,
	}
	e.allLocs = append(e.allLocs, r)
	if e.dynRoots == nil {
		e.dynRoots = make(map[dynKey]*location)
	}
	e.dynRoots[key] = r
	return r
}

// callArgTag returns the expression of the tag of an object that is
// allocated in the argument d.arg of the call d.call: 1 if the function
// that is called keeps it local.
func (b *batch) callArgTag(pos src.XPos, d *dynRoot) ir.Node {
	return b.callArgBits(pos, d, false, false)
}

// callArgKept returns the expression that is 1 if the function that
// d.call calls does not keep its argument d.arg, as far as its escape
// mask says, and 0 if it may. With own, a function that keeps it only
// by calls that check, or by methods of its own, does not
// (ir.EscMaskHeld).
func (b *batch) callArgKept(pos src.XPos, d *dynRoot, own bool) ir.Node {
	return b.callArgBits(pos, d, true, own)
}

func (b *batch) callArgBits(pos src.XPos, d *dynRoot, kept, own bool) ir.Node {
	uintptrType := types.Types[types.TUINTPTR]
	unsafePtr := types.Types[types.TUNSAFEPTR]
	lit := func(v int64) ir.Node {
		return ir.NewBasicLit(b.gd, pos, uintptrType, constant.MakeInt64(v))
	}
	binary := func(op ir.Op, x, y ir.Node) ir.Node {
		n := ir.NewBinaryExpr(b.gd, pos, op, x, y)
		n.SetType(uintptrType)
		n.SetTypecheck(1)
		return n
	}
	// An integer of a pointer, and back: not for checkptr to look at.
	toInt := func(p ir.Node) ir.Node {
		up := ir.NewConvExpr(b.gd, pos, ir.OCONVNOP, unsafePtr, p)
		up.SetTypecheck(1)
		u := ir.NewConvExpr(b.gd, pos, ir.OCONVNOP, uintptrType, up)
		u.SetTypecheck(1)
		u.SetCheckPtr(true)
		return u
	}
	unary := func(op ir.Op, x ir.Node) ir.Node {
		n := ir.NewUnaryExpr(b.gd, pos, op, x)
		n.SetType(unsafePtr)
		n.SetTypecheck(1)
		return n
	}
	not := func(x ir.Node) ir.Node { return binary(ir.OXOR, x, lit(1)) }

	// The word that has the mask: in the closure, or in the method
	// table of the interface.
	fun := ir.DeepCopy(pos, d.path)
	var carrier, recv ir.Node
	var off int64
	if d.call.Op() == ir.OCALLINTER {
		sel := d.call.Fun.(*ir.SelectorExpr)
		t := sel.X.Type()
		carrier = toInt(unary(ir.OITAB, fun))
		recv = toInt(unary(ir.OIDATA, ir.DeepCopy(pos, d.path)))
		off = int64(abi.ITabEscMaskOff(types.PtrSize, len(t.AllMethods()), int(sel.Offset()/int64(types.PtrSize))))
	} else {
		carrier = toInt(fun)
		off = types.FuncMaskOffset
	}
	// If there is no function (the call will panic, after the
	// arguments are evaluated), a word that says that all is shared.
	all := ir.NewLinksymExpr(b.gd, pos, typecheck.LookupRuntimeVar(b.gd, "escMaskAll"), types.Types[types.TUINT64])
	allAddr := toInt(typecheck.NodAddr(b.gd, all))
	nz := binary(ir.ORSH, binary(ir.OOR, carrier, binary(ir.OSUB, lit(0), ir.DeepCopy(pos, carrier))), lit(63))
	addr := binary(ir.OOR,
		binary(ir.OAND, binary(ir.OADD, ir.DeepCopy(pos, carrier), lit(off)), binary(ir.OSUB, lit(0), nz)),
		binary(ir.OAND, allAddr, binary(ir.OSUB, ir.DeepCopy(pos, nz), lit(1))))
	ap := ir.NewConvExpr(b.gd, pos, ir.OCONVNOP, unsafePtr, addr)
	ap.SetTypecheck(1)
	ap.SetCheckPtr(true)
	wp := ir.NewConvExpr(b.gd, pos, ir.OCONVNOP, types.NewPtr(types.Types[types.TUINT64]), ap)
	wp.SetTypecheck(1)
	wp.SetCheckPtr(true)
	word := ir.NewStarExpr(b.gd, pos, wp)
	word.SetType(types.Types[types.TUINT64])
	word.SetTypecheck(1)
	word.SetBounded(true)
	raw := func() ir.Node {
		c := ir.NewConvExpr(b.gd, pos, ir.OCONVNOP, uintptrType, ir.DeepCopy(pos, word))
		c.SetTypecheck(1)
		return c
	}
	bit := func(n int) ir.Node {
		if n == 0 {
			return binary(ir.OAND, raw(), lit(1))
		}
		return binary(ir.OAND, binary(ir.ORSH, raw(), lit(int64(n))), lit(1))
	}
	// Not a mask that is computed (bit 0), and: not kept; or not
	// shared and not stored through the receiver, or the receiver
	// points to a local object.
	if kept {
		free := not(bit(1 + d.arg))
		if own {
			free = binary(ir.OOR, free, not(bit(ir.EscMaskHeld+d.arg)))
		}
		return binary(ir.OAND, not(bit(0)), free)
	}
	stays := not(bit(ir.EscMaskViaRecv + d.arg))
	if recv != nil {
		stays = binary(ir.OOR, stays, b.localTest(pos, recv))
	}
	stays = binary(ir.OAND, not(bit(ir.EscMaskShared+d.arg)), stays)
	return binary(ir.OAND, not(bit(0)), binary(ir.OOR, not(bit(1+d.arg)), stays))
}

// What the linker knows of the functions that are only known when the
// program runs (ir/sharelink.go).
//
// The argument of a call of such a function flows to a link root: a
// dynamic root whose tag is read from the word of the class of functions
// that the call may be of, which the linker fills in. It can be read
// anywhere, so an object that was allocated long before the call, and in
// another function, is local if none of those functions shares it: a
// parameter that flows to a link root says so (ir.ParamShare.Links), and
// the caller's argument flows to a link root of its own.

func (b *batch) linksOn() bool {
	return b.gd.Debug.NoLocalLink == 0 && b.callBitsOn()
}

// linkKey returns the key of the class of functions that call, of a
// function that is only known when the program runs, may be of, or "".
func (e *escape) linkKey(call *ir.CallExpr) string {
	if !e.linksOn() || call.GoDefer {
		return ""
	}
	var key string
	var wild bool
	switch call.Op() {
	case ir.OCALLINTER:
		sel, ok := call.Fun.(*ir.SelectorExpr)
		if !ok || sel.Op() != ir.ODOTINTER {
			return ""
		}
		key, wild = ir.ShareKeyMethod(sel.Sel.Name, sel.Type())
	case ir.OCALLFUNC:
		if t := call.Fun.Type(); t != nil && t.Recv() == nil {
			key, wild = ir.ShareKeyFunc(t)
		}
	}
	if wild {
		return ""
	}
	return key
}

// linkRoot returns the link root of the current function for the
// argument arg of the functions of the class key, or nil.
func (e *escape) linkRoot(key string, arg int) *location {
	return e.linkRootOf(key, arg, true)
}

// linkRootOf is linkRoot; without heap, what flows to the root need not
// be in the heap for it: the root only says who reaches it, as for the
// receiver of a method that some call may be of (recv.go).
func (e *escape) linkRootOf(key string, arg int, heap bool) *location {
	if key == "" || arg < 0 || arg >= ir.EscMaskArgs || !e.linksOn() {
		return nil
	}
	if pkg := e.curfn.Sym().Pkg; pkg == nil || ir.UncheckedPkg(pkg.Path) {
		return nil
	}
	k := dynKey{0, fmt.Sprintf("link %p %d %v %s", e.curfn, arg, heap, key)}
	if r := e.dynRoots[k]; r != nil {
		return r
	}
	attrs := attrDynShares
	if heap {
		attrs |= attrEscapes | attrPersists | attrMutates | attrCalls
	}
	r := &location{
		curfn:     e.curfn,
		loopDepth: 1,
		attrs:     attrs,
		dynRoot:   &dynRoot{link: key, arg: arg},
		isDynRoot: true,
	}
	e.allLocs = append(e.allLocs, r)
	if e.dynRoots == nil {
		e.dynRoots = make(map[dynKey]*location)
	}
	e.dynRoots[k] = r
	return r
}

// linkTag returns the expression that is 1 if no function of the class
// key shares its argument arg: it does not keep it, or keeps it where
// no other goroutine reaches, and not in what its receiver points to,
// which nobody knows here.
func (b *batch) linkTag(pos src.XPos, key string, arg int) ir.Node {
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
	// Until the linker writes it, all is shared. (It is not read-only,
	// which the compiler would take for a constant.)
	sym := b.gd.Ctxt.LookupInit(ir.ShareWordPrefix+key, func(s *obj.LSym) {
		s.WriteInt(b.gd.Ctxt, 0, 8, -1)
		b.gd.Ctxt.Globl(s, 8, obj.DUPOK|obj.NOPTR)
	})
	bit := func(n int) ir.Node {
		var w ir.Node = ir.NewLinksymExpr(b.gd, pos, sym, types.Types[types.TUINT64])
		c := ir.NewConvExpr(b.gd, pos, ir.OCONVNOP, uintptrType, w)
		c.SetTypecheck(1)
		if n == 0 {
			return binary(ir.OAND, c, lit(1))
		}
		return binary(ir.OAND, binary(ir.ORSH, c, lit(int64(n))), lit(1))
	}
	not := func(x ir.Node) ir.Node { return binary(ir.OXOR, x, lit(1)) }
	stays := binary(ir.OAND, not(bit(ir.EscMaskShared+arg)), not(bit(ir.EscMaskViaRecv+arg)))
	return binary(ir.OAND, not(bit(0)), binary(ir.OOR, not(bit(1+arg)), stays))
}
