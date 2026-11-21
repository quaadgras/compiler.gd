// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package escape

import (
	"fmt"
	"slices"
	"strings"

	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/types"
)

// The census of fields (-d=fieldcensus, doc/gd/sharing.md): a
// measurement for a design in which each field that holds a pointer has
// one class for the whole program. It prints, for every store of a
// pointer in a field, an element or a map, where the value comes from,
// as far as one look at the function says, without flow in time: a
// variable has had all that is ever assigned to it. Nothing here
// decides anything.

type cenCat uint16

const (
	cenFresh   cenCat = 1 << iota // allocated in the function
	cenResult                     // the result of a call
	cenParam                      // a parameter, or what one is reached from
	cenShared                     // a package's variable, or received
	cenStatic                     // data that is only read
	cenUnknown                    // nothing is known
	cenLoad                       // loaded from the fields of deps
)

// A cenVal is where a value may come from.
type cenVal struct {
	cat  cenCat
	deps []string // the keys of the fields that it may be loaded from
	ops  []string // the operations that nothing is known of
}

func (v *cenVal) merge(w cenVal) bool {
	changed := v.cat|w.cat != v.cat
	v.cat |= w.cat
	for _, d := range w.deps {
		if !slices.Contains(v.deps, d) {
			v.deps = append(v.deps, d)
			changed = true
		}
	}
	for _, d := range w.ops {
		if !slices.Contains(v.ops, d) {
			v.ops = append(v.ops, d)
		}
	}
	return changed
}

type census struct {
	gd      *base.Invocation
	vars    map[*ir.Name]*cenVal
	changed bool
	emit    bool
	lit     map[ir.Node]ir.Node
	curfn   *ir.Func
}

// fieldCensus prints the census of the functions fns.
func fieldCensus(gd *base.Invocation, fns []*ir.Func) {
	c := &census{gd: gd, vars: make(map[*ir.Name]*cenVal)}
	for pass := 0; pass < 20; pass++ {
		c.changed = false
		for _, fn := range fns {
			c.curfn = fn
			ir.VisitList(fn.Body, c.visit)
		}
		if !c.changed {
			break
		}
	}
	c.emit = true
	for _, fn := range fns {
		c.curfn = fn
		if fn.Type().Recv() != nil && fn.Sym() != nil {
			name := fn.Sym().Name
			if i := strings.LastIndex(name, "."); i >= 0 {
				name = name[i+1:]
			}
			c.line("def", cenFuncKey(fn.Nname), 0, cenVal{ops: []string{"meth:" + name}})
		}
		ir.VisitList(fn.Body, c.visit)
		// What the function returns by its named results.
		for i, f := range fn.Type().Results() {
			if n, ok := f.Nname.(*ir.Name); ok && n != nil && cenPointer(f.Type) {
				if v := c.vars[n.Canonical()]; v != nil {
					c.line("ret", cenFuncKey(fn.Nname), i, *v)
				}
			}
		}
	}
}

// cenPointer reports whether a value of type t is a pointer to what may
// hold pointers, which is all that the census is about.
func cenPointer(t *types.Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind() {
	case types.TPTR, types.TSLICE:
		return t.Elem().HasPointers()
	case types.TMAP, types.TINTER, types.TFUNC, types.TCHAN, types.TUNSAFEPTR:
		return true
	}
	return false
}

func cenFieldKey(n *ir.SelectorExpr) string {
	t := n.X.Type()
	if t.IsPtr() {
		t = t.Elem()
	}
	return t.LinkString() + "." + n.Sel.Name
}

func cenElemKey(t *types.Type) string {
	if t.IsPtr() && t.Elem().IsArray() {
		t = t.Elem()
	}
	return "elem " + t.LinkString()
}

func (c *census) of(n *ir.Name) *cenVal {
	n = n.Canonical()
	v := c.vars[n]
	if v == nil {
		v = new(cenVal)
		if n.Class == ir.PPARAM {
			v.cat = cenParam
			if n.Curfn != nil {
				for i, f := range n.Curfn.Type().RecvParams() {
					if f.Nname == n {
						v.deps = []string{fmt.Sprintf("p:%s#%d", cenFuncKey(n.Curfn.Nname), i)}
					}
				}
			}
		}
		c.vars[n] = v
	}
	return v
}

// cenFuncKey returns the name of the function fn for the census.
func cenFuncKey(fn *ir.Name) string {
	s := fn.Sym()
	if s == nil {
		return "?"
	}
	if s.Pkg == nil {
		return s.Name
	}
	return s.Pkg.Path + "." + s.Name
}

// cenCallee returns the key of what call calls, or "" if only the
// program knows; first is the index of its first argument.
func cenCallee(call *ir.CallExpr) (key string, recv ir.Node) {
	switch call.Op() {
	case ir.OCALLFUNC, ir.OCALLMETH:
		if fn := ir.StaticCalleeName(call.Fun); fn != nil {
			return cenFuncKey(fn), nil
		}
	case ir.OCALLINTER:
		if sel, ok := call.Fun.(*ir.SelectorExpr); ok {
			return "meth:" + sel.Sel.Name, sel.X
		}
	}
	return "", nil
}

func (c *census) line(kind, key string, i int, v cenVal) {
	if !c.emit {
		return
	}
	fmt.Printf("fieldcensus-%s\t%s\t%d\t%d\t%s\t%s\t%s\n", kind, key, i, v.cat, strings.Join(v.deps, "|"), strings.Join(v.ops, "|"), c.pkg())
}

func (c *census) pkg() string {
	if s := c.curfn.Sym(); s != nil && s.Pkg != nil {
		return s.Pkg.Path
	}
	return ""
}

// alloc returns the value of an allocation at n.
func (c *census) alloc(n ir.Node) cenVal {
	if o := c.lit[n]; o != nil {
		// (A literal whose address is taken is that allocation.)
		n = o
	}
	return cenVal{cat: cenFresh, deps: []string{"a:" + c.gd.FmtPos(n.Pos()) + ":" + n.Op().String()}}
}

func load(key string) cenVal { return cenVal{cat: cenLoad, deps: []string{key}} }

// eval returns where the value of n may come from.
func (c *census) eval(n ir.Node) cenVal {
	if n == nil {
		return cenVal{}
	}
	switch n.Op() {
	case ir.ONIL:
		return cenVal{}
	case ir.OLITERAL, ir.OMETHEXPR, ir.OLINKSYMOFFSET:
		return cenVal{cat: cenStatic}
	case ir.ONAME:
		n := n.(*ir.Name)
		switch n.Class {
		case ir.PEXTERN:
			return cenVal{cat: cenShared}
		case ir.PFUNC:
			c.line("esc", cenFuncKey(n), 0, cenVal{})
			return cenVal{cat: cenStatic}
		}
		if ir.IsBlank(n) {
			return cenVal{}
		}
		return *c.of(n)
	case ir.OADDR:
		return c.evalAddr(n.(*ir.AddrExpr).X)
	case ir.OCLOSURE:
		if f := n.(*ir.ClosureExpr).Func; f != nil && f.Nname != nil {
			c.line("esc", cenFuncKey(f.Nname), 0, cenVal{})
		}
		return c.alloc(n)
	case ir.OMETHVALUE:
		c.line("esc", "meth:"+n.(*ir.SelectorExpr).Sel.Name, 0, cenVal{})
		return c.alloc(n)
	case ir.OPTRLIT, ir.ONEW, ir.OMAKESLICE, ir.OMAKEMAP, ir.OMAKECHAN, ir.OSLICELIT, ir.OARRAYLIT, ir.OSTRUCTLIT, ir.OMAPLIT,
		ir.OADDSTR, ir.OBYTES2STR, ir.OSTR2BYTES, ir.OSTR2RUNES, ir.ORUNES2STR, ir.ORUNESTR, ir.OMAKESLICECOPY:
		return c.alloc(n)
	case ir.OCONVIFACE:
		n := n.(*ir.ConvExpr)
		if n.X.Type().IsInterface() || types.IsDirectIface(n.X.Type()) {
			return c.eval(n.X)
		}
		if n.X.Op() == ir.OLITERAL {
			return cenVal{cat: cenStatic}
		}
		return c.alloc(n)
	case ir.OCONV, ir.OCONVNOP:
		n := n.(*ir.ConvExpr)
		if !n.X.Type().HasPointers() {
			return cenVal{cat: cenUnknown, ops: []string{"uintptr"}}
		}
		return c.eval(n.X)
	case ir.OPAREN:
		return c.eval(n.(*ir.ParenExpr).X)
	case ir.ODOTTYPE, ir.ODOTTYPE2:
		return c.eval(n.(*ir.TypeAssertExpr).X)
	case ir.ODYNAMICDOTTYPE, ir.ODYNAMICDOTTYPE2:
		return c.eval(n.(*ir.DynamicTypeAssertExpr).X)
	case ir.OSLICE, ir.OSLICEARR, ir.OSLICE3, ir.OSLICE3ARR, ir.OSLICESTR:
		return c.eval(n.(*ir.SliceExpr).X)
	case ir.OSPTR, ir.OITAB, ir.OIDATA, ir.OCHECKNIL:
		return c.eval(n.(*ir.UnaryExpr).X)
	case ir.ODOT, ir.ODOTPTR:
		return load(cenFieldKey(n.(*ir.SelectorExpr)))
	case ir.OINDEX:
		n := n.(*ir.IndexExpr)
		if n.X.Type().IsString() {
			return cenVal{}
		}
		return load(cenElemKey(n.X.Type()))
	case ir.OINDEXMAP:
		return load(cenElemKey(n.(*ir.IndexExpr).X.Type()))
	case ir.ODEREF:
		return load(cenElemKey(n.(*ir.StarExpr).X.Type()))
	case ir.OCALLFUNC, ir.OCALLMETH, ir.OCALLINTER, ir.OCALL:
		return c.result(n, 0)
	case ir.OINLCALL:
		n := n.(*ir.InlinedCallExpr)
		var v cenVal
		for _, r := range n.ReturnVars {
			v.merge(c.eval(r))
		}
		return v
	case ir.OAPPEND:
		n := n.(*ir.CallExpr)
		v := c.alloc(n)
		if len(n.Args) > 0 {
			v.merge(c.eval(n.Args[0]))
		}
		return v
	case ir.ORECV:
		return cenVal{cat: cenShared}
	case ir.OUNSAFEADD, ir.OUNSAFESLICE, ir.OUNSAFESTRING:
		return c.eval(n.(*ir.BinaryExpr).X)
	case ir.OUNSAFESLICEDATA, ir.OUNSAFESTRINGDATA:
		return c.eval(n.(*ir.UnaryExpr).X)
	}
	return cenVal{cat: cenUnknown, ops: []string{n.Op().String()}}
}

// result returns where the i'th result of the call n comes from.
func (c *census) result(n ir.Node, i int) cenVal {
	if call, ok := n.(*ir.CallExpr); ok {
		if key, _ := cenCallee(call); key != "" {
			return cenVal{cat: cenResult, deps: []string{fmt.Sprintf("r:%s#%d", key, i)}}
		}
	}
	return cenVal{cat: cenResult, ops: []string{"dynamic result"}}
}

// evalAddr returns where the address of x comes from.
func (c *census) evalAddr(x ir.Node) cenVal {
	for {
		switch x.Op() {
		case ir.OPAREN:
			x = x.(*ir.ParenExpr).X
			continue
		case ir.ODOT:
			x = x.(*ir.SelectorExpr).X
			continue
		case ir.OINDEX:
			ix := x.(*ir.IndexExpr)
			if ix.X.Type().IsArray() {
				x = ix.X
				continue
			}
			return c.eval(ix.X)
		case ir.ODOTPTR:
			return c.eval(x.(*ir.SelectorExpr).X)
		case ir.ODEREF:
			return c.eval(x.(*ir.StarExpr).X)
		case ir.ONAME:
			if x.(*ir.Name).Class == ir.PEXTERN {
				return cenVal{cat: cenShared}
			}
			return c.alloc(x.(*ir.Name).Canonical())
		case ir.OSTRUCTLIT, ir.OARRAYLIT, ir.OSLICELIT, ir.OMAPLIT:
			return c.alloc(x)
		}
		return cenVal{cat: cenUnknown, ops: []string{"&" + x.Op().String()}}
	}
}

// store notes that v is stored at key in a container that comes from
// where in says.
func (c *census) store(key string, in, v cenVal, pos ir.Node) {
	if !c.emit {
		return
	}
	fmt.Printf("fieldcensus\t%s\t%d\t%d\t%s\t%s\t%s\t%s\t%s\n", key, v.cat, in.cat, strings.Join(v.deps, "|"), strings.Join(v.ops, "|"), c.gd.FmtPos(pos.Pos()), strings.Join(in.deps, "|"), c.pkg())
}

// assign notes the assignment of v to lhs.
func (c *census) assign(lhs ir.Node, v cenVal) {
	if lhs == nil || ir.IsBlank(lhs) || !cenPointer(lhs.Type()) {
		return
	}
	switch lhs.Op() {
	case ir.ONAME:
		n := lhs.(*ir.Name)
		if n.Class == ir.PEXTERN {
			c.store("<shared>", cenVal{cat: cenShared}, v, lhs)
			return
		}
		if c.of(n).merge(v) {
			c.changed = true
		}
	case ir.ODOT, ir.ODOTPTR:
		n := lhs.(*ir.SelectorExpr)
		c.store(cenFieldKey(n), c.evalAddr(lhs), v, lhs)
	case ir.OINDEX, ir.OINDEXMAP:
		n := lhs.(*ir.IndexExpr)
		c.store(cenElemKey(n.X.Type()), c.evalAddr(lhs), v, lhs)
	case ir.ODEREF:
		n := lhs.(*ir.StarExpr)
		c.store(cenElemKey(n.X.Type()), c.eval(n.X), v, lhs)
	case ir.OPAREN:
		c.assign(lhs.(*ir.ParenExpr).X, v)
	}
}

func (c *census) visit(n ir.Node) {
	fresh := cenVal{cat: cenFresh}
	_ = fresh
	switch n.Op() {
	case ir.OPTRLIT, ir.OADDR:
		if x := n.(*ir.AddrExpr).X; x.Op() == ir.OSTRUCTLIT || x.Op() == ir.OARRAYLIT {
			if c.lit == nil {
				c.lit = make(map[ir.Node]ir.Node)
			}
			c.lit[x] = n
		}
	case ir.OCALLFUNC, ir.OCALLMETH, ir.OCALLINTER:
		if !c.emit {
			break
		}
		call := n.(*ir.CallExpr)
		key, recv := cenCallee(call)
		first := 0
		if recv != nil {
			first = 1
			if cenPointer(recv.Type()) {
				c.line("arg", key, 0, c.eval(recv))
			}
		}
		for i, a := range call.Args {
			if !cenPointer(a.Type()) {
				continue
			}
			v := c.eval(a)
			if key == "" {
				// (Evaluated for the functions that it names.)
				continue
			}
			c.line("arg", key, first+i, v)
		}
	case ir.OSEND:
		n := n.(*ir.SendStmt)
		if cenPointer(n.Value.Type()) {
			c.store("<shared>", cenVal{cat: cenShared}, c.eval(n.Value), n)
		}
	case ir.OGO:
		if call, ok := n.(*ir.GoDeferStmt).Call.(*ir.CallExpr); ok {
			for _, a := range call.Args {
				if cenPointer(a.Type()) {
					c.store("<shared>", cenVal{cat: cenShared}, c.eval(a), n)
				}
			}
			if cenPointer(call.Fun.Type()) && call.Fun.Op() != ir.ONAME {
				c.store("<shared>", cenVal{cat: cenShared}, c.eval(call.Fun), n)
			}
		}
	case ir.ORETURN:
		if !c.emit {
			break
		}
		for i, r := range n.(*ir.ReturnStmt).Results {
			if cenPointer(r.Type()) {
				c.line("ret", cenFuncKey(c.curfn.Nname), i, c.eval(r))
			}
		}
	case ir.OAS:
		n := n.(*ir.AssignStmt)
		if n.Y != nil {
			c.assign(n.X, c.eval(n.Y))
		}
	case ir.OAS2:
		n := n.(*ir.AssignListStmt)
		for i, l := range n.Lhs {
			if i < len(n.Rhs) {
				c.assign(l, c.eval(n.Rhs[i]))
			}
		}
	case ir.OAS2FUNC:
		n := n.(*ir.AssignListStmt)
		if inl, ok := n.Rhs[0].(*ir.InlinedCallExpr); ok {
			for i, l := range n.Lhs {
				if i < len(inl.ReturnVars) {
					c.assign(l, c.eval(inl.ReturnVars[i]))
				}
			}
			break
		}
		for i, l := range n.Lhs {
			c.assign(l, c.result(n.Rhs[0], i))
		}
	case ir.OAS2DOTTYPE, ir.OAS2MAPR:
		n := n.(*ir.AssignListStmt)
		c.assign(n.Lhs[0], c.eval(n.Rhs[0]))
	case ir.OAS2RECV:
		n := n.(*ir.AssignListStmt)
		c.assign(n.Lhs[0], cenVal{cat: cenShared})
	case ir.ORANGE:
		n := n.(*ir.RangeStmt)
		t := n.X.Type()
		switch {
		case t == nil:
		case t.IsSlice() || t.IsArray() || t.IsPtr() && t.Elem().IsArray():
			c.assign(n.Value, load(cenElemKey(t)))
		case t.IsMap():
			c.assign(n.Key, load("key "+t.LinkString()))
			c.assign(n.Value, load(cenElemKey(t)))
		case t.IsChan():
			c.assign(n.Key, cenVal{cat: cenShared})
		case t.Kind() == types.TFUNC:
			c.assign(n.Key, cenVal{cat: cenUnknown, ops: []string{"range func"}})
			c.assign(n.Value, cenVal{cat: cenUnknown, ops: []string{"range func"}})
		}
	case ir.OSWITCH:
		n := n.(*ir.SwitchStmt)
		if g, ok := n.Tag.(*ir.TypeSwitchGuard); ok {
			v := c.eval(g.X)
			for _, cc := range n.Cases {
				if cc.Var != nil {
					c.assign(cc.Var, v)
				}
			}
		}
	case ir.OAPPEND:
		n := n.(*ir.CallExpr)
		if len(n.Args) < 2 || !cenPointer(n.Type().Elem()) {
			break
		}
		key := cenElemKey(n.Type())
		in := c.eval(n.Args[0])
		in.cat |= cenFresh
		if n.IsDDD {
			c.store(key, in, load(cenElemKey(n.Args[1].Type())), n)
			break
		}
		for _, a := range n.Args[1:] {
			c.store(key, in, c.eval(a), n)
		}
	case ir.OSTRUCTLIT:
		n := n.(*ir.CompLitExpr)
		for _, e := range n.List {
			if kv, ok := e.(*ir.StructKeyExpr); ok && cenPointer(kv.Field.Type) {
				c.store(n.Type().LinkString()+"."+kv.Field.Sym.Name, c.alloc(n), c.eval(kv.Value), kv)
			}
		}
	case ir.OARRAYLIT, ir.OSLICELIT:
		n := n.(*ir.CompLitExpr)
		if !cenPointer(n.Type().Elem()) {
			break
		}
		for _, e := range n.List {
			if kv, ok := e.(*ir.KeyExpr); ok {
				e = kv.Value
			}
			c.store(cenElemKey(n.Type()), c.alloc(n), c.eval(e), e)
		}
	case ir.OMAPLIT:
		n := n.(*ir.CompLitExpr)
		for _, e := range n.List {
			kv, ok := e.(*ir.KeyExpr)
			if !ok {
				continue
			}
			if cenPointer(n.Type().Key()) {
				c.store("key "+n.Type().LinkString(), c.alloc(n), c.eval(kv.Key), kv)
			}
			if cenPointer(n.Type().Elem()) {
				c.store(cenElemKey(n.Type()), c.alloc(n), c.eval(kv.Value), kv)
			}
		}
	}
}
