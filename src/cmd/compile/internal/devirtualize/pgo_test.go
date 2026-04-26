// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package devirtualize

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/pgoir"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
	"cmd/internal/obj"
	"cmd/internal/pgo"
	"cmd/internal/src"
	"cmd/internal/sys"
	"testing"
)

// testGd is the *base.Invocation passed to devirtualize's exported
// functions in tests. We populate just the bits the package reads —
// Ctxt and Debug.PGODebug — so we don't have to stand up a full
// compiler invocation.
var testGd = func() *base.Invocation {
	gd := &base.Invocation{
		Ctxt: &obj.Link{Arch: &obj.LinkArch{Arch: &sys.Arch{Alignment: 1, CanMergeLoads: true}}},
	}
	gd.Debug.PGODebug = 3
	return gd
}()

func init() {
	// These are the few constants that need to be initialized in order to use
	// the types package without using the typecheck package by calling
	// typecheck.InitUniverse() (the normal way to initialize the types package).
	types.PtrSize = 8
	types.RegSize = 8
	types.MaxWidth = 1 << 50
	typecheck.InitUniverse(testGd)
}

func makePos(b *src.PosBase, line, col uint) src.XPos {
	return testGd.Ctxt.PosTable.XPos(src.MakePos(b, line, col))
}

type profileBuilder struct {
	p *pgoir.Profile
}

func newProfileBuilder() *profileBuilder {
	// findHotConcreteCallee only uses pgoir.Profile.WeightedCG, so we're
	// going to take a shortcut and only construct that.
	return &profileBuilder{
		p: &pgoir.Profile{
			WeightedCG: &pgoir.IRGraph{
				IRNodes: make(map[string]*pgoir.IRNode),
			},
		},
	}
}

// Profile returns the constructed profile.
func (p *profileBuilder) Profile() *pgoir.Profile {
	return p.p
}

// NewNode creates a new IRNode and adds it to the profile.
//
// fn may be nil, in which case the node will set LinkerSymbolName.
func (p *profileBuilder) NewNode(name string, fn *ir.Func) *pgoir.IRNode {
	n := &pgoir.IRNode{
		OutEdges: make(map[pgo.NamedCallEdge]*pgoir.IREdge),
	}
	if fn != nil {
		n.AST = fn
	} else {
		n.LinkerSymbolName = name
	}
	p.p.WeightedCG.IRNodes[name] = n
	return n
}

// Add a new call edge from caller to callee.
func addEdge(caller, callee *pgoir.IRNode, offset int, weight int64) {
	namedEdge := pgo.NamedCallEdge{
		CallerName:     caller.Name(),
		CalleeName:     callee.Name(),
		CallSiteOffset: offset,
	}
	irEdge := &pgoir.IREdge{
		Src:            caller,
		Dst:            callee,
		CallSiteOffset: offset,
		Weight:         weight,
	}
	caller.OutEdges[namedEdge] = irEdge
}

// Create a new struct type named structName with a method named methName and
// return the method.
func makeStructWithMethod(gd *base.Invocation, pkg *types.Pkg, structName, methName string) *ir.Func {
	// type structName struct{}
	structType := types.NewStruct(nil)

	// func (structName) methodName()
	recv := types.NewField(src.NoXPos, typecheck.Lookup(testGd, structName), structType)
	sig := types.NewSignature(testGd, recv, nil, nil)
	fn := ir.NewFunc(gd, src.NoXPos, src.NoXPos, pkg.Lookup(structName+"."+methName), sig)

	// Add the method to the struct.
	structType.SetMethods([]*types.Field{types.NewField(src.NoXPos, typecheck.Lookup(testGd, methName), sig)})

	return fn
}

func TestFindHotConcreteInterfaceCallee(t *testing.T) {
	p := newProfileBuilder()

	pkgFoo := types.NewPkg("example.com/foo", "foo")
	basePos := src.NewFileBase("foo.go", "/foo.go")

	const (
		// Caller start line.
		callerStart = 42

		// The line offset of the call we care about.
		callOffset = 1

		// The line offset of some other call we don't care about.
		wrongCallOffset = 2
	)

	// type IFace interface {
	//	Foo()
	// }
	fooSig := types.NewSignature(testGd, types.FakeRecv(), nil, nil)
	method := types.NewField(src.NoXPos, typecheck.Lookup(testGd, "Foo"), fooSig)
	iface := types.NewInterface([]*types.Field{method})

	callerFn := ir.NewFunc(testGd, makePos(basePos, callerStart, 1), src.NoXPos, pkgFoo.Lookup("Caller"), types.NewSignature(testGd, nil, nil, nil))

	hotCalleeFn := makeStructWithMethod(testGd, pkgFoo, "HotCallee", "Foo")
	coldCalleeFn := makeStructWithMethod(testGd, pkgFoo, "ColdCallee", "Foo")
	wrongLineCalleeFn := makeStructWithMethod(testGd, pkgFoo, "WrongLineCallee", "Foo")
	wrongMethodCalleeFn := makeStructWithMethod(testGd, pkgFoo, "WrongMethodCallee", "Bar")

	callerNode := p.NewNode("example.com/foo.Caller", callerFn)
	hotCalleeNode := p.NewNode("example.com/foo.HotCallee.Foo", hotCalleeFn)
	coldCalleeNode := p.NewNode("example.com/foo.ColdCallee.Foo", coldCalleeFn)
	wrongLineCalleeNode := p.NewNode("example.com/foo.WrongCalleeLine.Foo", wrongLineCalleeFn)
	wrongMethodCalleeNode := p.NewNode("example.com/foo.WrongCalleeMethod.Foo", wrongMethodCalleeFn)

	hotMissingCalleeNode := p.NewNode("example.com/bar.HotMissingCallee.Foo", nil)

	addEdge(callerNode, wrongLineCalleeNode, wrongCallOffset, 100) // Really hot, but wrong line.
	addEdge(callerNode, wrongMethodCalleeNode, callOffset, 100)    // Really hot, but wrong method type.
	addEdge(callerNode, hotCalleeNode, callOffset, 10)
	addEdge(callerNode, coldCalleeNode, callOffset, 1)

	// Equal weight, but IR missing.
	//
	// N.B. example.com/bar sorts lexicographically before example.com/foo,
	// so if the IR availability of hotCalleeNode doesn't get precedence,
	// this would be mistakenly selected.
	addEdge(callerNode, hotMissingCalleeNode, callOffset, 10)

	// IFace.Foo()
	sel := typecheck.NewMethodExpr(testGd, src.NoXPos, iface, typecheck.Lookup(testGd, "Foo"))
	call := ir.NewCallExpr(testGd, makePos(basePos, callerStart+callOffset, 1), ir.OCALLINTER, sel, nil)

	gotFn, gotWeight := findHotConcreteInterfaceCallee(testGd, p.Profile(), callerFn, call)
	if gotFn != hotCalleeFn {
		t.Errorf("findHotConcreteInterfaceCallee func got %v want %v", gotFn, hotCalleeFn)
	}
	if gotWeight != 10 {
		t.Errorf("findHotConcreteInterfaceCallee weight got %v want 10", gotWeight)
	}
}

func TestFindHotConcreteFunctionCallee(t *testing.T) {
	// TestFindHotConcreteInterfaceCallee already covered basic weight
	// comparisons, which is shared logic. Here we just test type signature
	// disambiguation.

	p := newProfileBuilder()

	pkgFoo := types.NewPkg("example.com/foo", "foo")
	basePos := src.NewFileBase("foo.go", "/foo.go")

	const (
		// Caller start line.
		callerStart = 42

		// The line offset of the call we care about.
		callOffset = 1
	)

	callerFn := ir.NewFunc(testGd, makePos(basePos, callerStart, 1), src.NoXPos, pkgFoo.Lookup("Caller"), types.NewSignature(testGd, nil, nil, nil))

	// func HotCallee()
	hotCalleeFn := ir.NewFunc(testGd, src.NoXPos, src.NoXPos, pkgFoo.Lookup("HotCallee"), types.NewSignature(testGd, nil, nil, nil))

	// func WrongCallee() bool
	wrongCalleeFn := ir.NewFunc(testGd, src.NoXPos, src.NoXPos, pkgFoo.Lookup("WrongCallee"), types.NewSignature(testGd, nil, nil,
		[]*types.Field{
			types.NewField(src.NoXPos, nil, types.Types[types.TBOOL]),
		},
	))

	callerNode := p.NewNode("example.com/foo.Caller", callerFn)
	hotCalleeNode := p.NewNode("example.com/foo.HotCallee", hotCalleeFn)
	wrongCalleeNode := p.NewNode("example.com/foo.WrongCallee", wrongCalleeFn)

	addEdge(callerNode, wrongCalleeNode, callOffset, 100) // Really hot, but wrong function type.
	addEdge(callerNode, hotCalleeNode, callOffset, 10)

	// var fn func()
	name := ir.NewNameAt(testGd, src.NoXPos, typecheck.Lookup(testGd, "fn"), types.NewSignature(testGd, nil, nil, nil))
	// fn()
	call := ir.NewCallExpr(testGd, makePos(basePos, callerStart+callOffset, 1), ir.OCALL, name, nil)

	gotFn, gotWeight := findHotConcreteFunctionCallee(testGd, p.Profile(), callerFn, call)
	if gotFn != hotCalleeFn {
		t.Errorf("findHotConcreteFunctionCallee func got %v want %v", gotFn, hotCalleeFn)
	}
	if gotWeight != 10 {
		t.Errorf("findHotConcreteFunctionCallee weight got %v want 10", gotWeight)
	}
}
