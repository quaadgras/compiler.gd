// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package apegen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

var fset = token.NewFileSet()

// A hostPkg is a package as one host builds it.
type hostPkg struct {
	host  string
	ctxt  build.Context
	bp    *build.Package
	files map[string]*ast.File // all Go files
	pkg   *types.Package
	info  *types.Info
	only  map[string]bool // Go files only some hosts build
	tests bool            // files include the package's own tests

	// The external tests (package p_test), type-checked against the
	// package with its own tests.
	xfiles map[string]*ast.File
	xinfo  *types.Info
	imp    types.ImporterFrom // the importer the package was checked with
	sfiles []string           // assembly files only some hosts build

	// decls are the package-level objects the host files declare.
	decls map[string]types.Object
	// rename gives the host copy's name of an object.
	rename map[types.Object]string
	// hoist is the fields moved out of a host type into the ape type.
	hoist map[types.Object]bool
	// drop is the declarations the ape file makes once for every host.
	drop map[types.Object]bool

	parents map[ast.Node]ast.Node

	// orig is the shared files that are split, as parsed.
	orig map[string]*ast.File

	inits []string // the host's init functions, renamed
}

// A pkgGen generates a package's host files.
type pkgGen struct {
	u      *universe
	path   string
	dir    string
	hs     []*hostPkg
	shared map[string]bool // file names every host builds
	// sharedTests is the package's own test files every host builds.
	sharedTests map[string]bool
	report      []string

	prepared, preparing bool

	testOff map[string]bool // shared tests that do not build for ape
	byValue map[string]bool // host types shared code or the ape API holds by value
	multi   map[string]bool // differing untyped constants shared code uses as several types
	xoff    map[string]bool // external tests: whether each does not

	// moved is, for each split file, the declarations its host part has.
	moved map[string]map[int]bool

	dedup map[string]bool // types and variables every host declares the same way
	// sharedVar is the variables of the same type that hosts
	// initialize differently: one variable, which host copies use.
	sharedVar map[string]bool
	union map[string]bool // host types shared structs embed
	canon map[string]bool // types that are the first host's, converted for others
	api   map[string]*iface

	// pushes maps a linkname target in another package (time.now) to
	// the function each host pushes to it.
	pushes map[string]map[string]*types.Func
	// asmDispatch maps a function a shared file declares and host
	// assembly implements to each host's ABI selector.
	asmDispatch map[string]map[string]string

	needApeValue bool // host copies use apeValue

	decls   bytes.Buffer      // ape declarations
	init    bytes.Buffer      // body of apeinitHost (runtime)
	imports map[string]string // of the dispatch file: path -> name
	outputs map[string][]byte // file name -> contents
	// sharedOut is the shared parts of the split files, by name.
	sharedOut map[string][]byte
}

// iface is something the ape package has for every host.
type iface struct {
	kind string // "func", "method", "const", "type", "var"
	name string // for a method, "T.m"
	objs map[string]types.Object
	uses []ast.Expr // in shared files (of the first host)
}

func (g *pkgGen) reportf(format string, args ...any) {
	g.report = append(g.report, fmt.Sprintf(format, args...))
}

func (g *pkgGen) first() *hostPkg { return g.hs[0] }

func (hp *hostPkg) obj(ic *iface) types.Object { return ic.objs[hp.host] }

func newPkgGen(u *universe, path string) *pkgGen {
	g := &pkgGen{u: u, path: path, dir: goroot(path), imports: map[string]string{}, outputs: map[string][]byte{}, sharedOut: map[string][]byte{}}
	for _, h := range u.hosts {
		ctxt := build.Default
		ctxt.GOROOT = gorootDir
		ctxt.GOOS, ctxt.GOARCH, ctxt.CgoEnabled = h, *arch, false
		bp, err := ctxt.ImportDir(g.dir, 0)
		if _, ok := err.(*build.NoGoError); ok {
			// The host does not build it: it has no files of it.
			bp = &build.Package{Name: bp.Name, ImportPath: path, Dir: g.dir}
		} else if err != nil {
			fatalf("%s: %s: %v", path, h, err)
		}
		g.hs = append(g.hs, &hostPkg{host: h, ctxt: ctxt, bp: bp})
	}
	count := map[string]int{}
	for _, hp := range g.hs {
		for _, f := range slices.Concat(hp.bp.GoFiles, hp.bp.SFiles) {
			count[f]++
		}
	}
	g.shared = map[string]bool{}
	for f, n := range count {
		if n == len(g.hs) {
			g.shared[f] = true
		}
	}
	for _, hp := range g.hs {
		hp.only = map[string]bool{}
		for _, f := range hp.bp.GoFiles {
			if !g.shared[f] {
				hp.only[f] = true
			}
		}
		for _, f := range hp.bp.SFiles {
			if !g.shared[f] {
				hp.sfiles = append(hp.sfiles, f)
			}
		}
		hp.check(g.dir)
	}
	for _, hp := range g.hs {
		if hp.tests {
			hp.checkXTests(g.dir)
		}
	}
	g.sharedTests = map[string]bool{}
	tcount := map[string]int{}
	for _, hp := range g.hs {
		if hp.tests {
			for _, f := range hp.bp.TestGoFiles {
				tcount[f]++
			}
		}
	}
	for f, n := range tcount {
		if n == len(g.hs) {
			g.sharedTests[f] = true
		}
	}
	return g
}

// check type-checks the package as the host builds it.
func (hp *hostPkg) check(dir string) {
	if *tests && hp.checkFiles(dir, slices.Concat(hp.bp.GoFiles, hp.bp.TestGoFiles), true) {
		hp.tests = true
		return
	}
	hp.checkFiles(dir, hp.bp.GoFiles, false)
}

// checkXTests type-checks the package's external tests, if they do.
func (hp *hostPkg) checkXTests(dir string) {
	if len(hp.bp.XTestGoFiles) == 0 {
		return
	}
	files := map[string]*ast.File{}
	var list []*ast.File
	for _, name := range hp.bp.XTestGoFiles {
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return
		}
		files[name] = f
		list = append(list, f)
	}
	saved := build.Default
	build.Default = hp.ctxt
	defer func() { build.Default = saved }()
	src := hp.imp
	conf := types.Config{
		Importer: importerFunc(func(path, dir string, mode types.ImportMode) (*types.Package, error) {
			if path == hp.bp.ImportPath {
				return hp.pkg, nil
			}
			return src.ImportFrom(path, dir, mode)
		}),
		Sizes: types.SizesFor("gc", hp.ctxt.GOARCH),
		Error: func(err error) {},
	}
	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	// Type errors are expected: other packages the tests import import
	// the package again, without its tests. What the tests use is still
	// known.
	conf.Check(hp.bp.ImportPath+"_test", fset, list, info)
	hp.xfiles, hp.xinfo = files, info
}

type importerFunc func(path, dir string, mode types.ImportMode) (*types.Package, error)

func (f importerFunc) Import(path string) (*types.Package, error) { return f(path, "", 0) }
func (f importerFunc) ImportFrom(path, dir string, mode types.ImportMode) (*types.Package, error) {
	return f(path, dir, mode)
}

// checkFiles type-checks the files; with mayFail, it reports whether
// they type-check.
func (hp *hostPkg) checkFiles(dir string, names []string, mayFail bool) bool {
	hp.files = map[string]*ast.File{}
	var files []*ast.File
	for _, name := range names {
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			fatalf("%v", err)
		}
		hp.files[name] = f
		files = append(files, f)
	}
	saved := build.Default
	build.Default = hp.ctxt
	defer func() { build.Default = saved }()
	var errs []error
	hp.imp = importer.ForCompiler(fset, "source", nil).(types.ImporterFrom)
	conf := types.Config{
		Importer: hp.imp,
		Sizes:    types.SizesFor("gc", hp.ctxt.GOARCH),
		Error:    func(err error) { errs = append(errs, err) },
	}
	hp.info = &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	hp.pkg, _ = conf.Check(hp.bp.ImportPath, fset, files, hp.info)
	if len(errs) > 0 && mayFail {
		return false
	}
	if len(errs) > 0 {
		for _, e := range errs[:min(len(errs), 10)] {
			log.Printf("%s: %v", hp.host, e)
		}
		fatalf("%s: %d type errors", hp.host, len(errs))
	}
	return true
}

// sharedUse reports whether pos is in a file every host builds that is
// not copied per host: a shared file or a shared test that builds for ape.
func (g *pkgGen) sharedUse(pos token.Pos) bool {
	name := fileName(pos)
	return g.shared[name] || g.sharedTests[name] && !g.testOff[name]
}

func (hp *hostPkg) inOnly(pos token.Pos) bool {
	return hp.only[fileName(pos)]
}

// hostName is the name a host copy gives a package-level name.
func hostName(host, name string) string {
	if token.IsExported(name) {
		return strings.ToUpper(host[:1]) + host[1:] + "_" + name
	}
	return host + "_" + name
}

func title(s string) string { return strings.ToUpper(s[:1]) + s[1:] }

// hostNamed returns the host type t is or points to, or nil. Types the
// ape file declares for every host are not host types.
func (g *pkgGen) hostNamed(hp *hostPkg, t types.Type) *types.Named {
	t = types.Unalias(t)
	if p, ok := t.(*types.Pointer); ok {
		t = types.Unalias(p.Elem())
	}
	n, ok := t.(*types.Named)
	if !ok || n.Obj().Pkg() != hp.pkg || !hp.inOnly(n.Obj().Pos()) || g.dedup[n.Obj().Name()] {
		return nil
	}
	return n
}

// typeString prints t as the ape file names it: this package's types by
// their original names.
func (g *pkgGen) typeString(hp *hostPkg, t types.Type) string {
	return types.TypeString(t, func(p *types.Package) string {
		if p == hp.pkg {
			return ""
		}
		g.imports[p.Path()] = p.Name()
		return p.Name()
	})
}

// fieldOwner names the package-level struct type that declares f.
func (hp *hostPkg) fieldOwner(f *types.Var) string {
	scope := hp.pkg.Scope()
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok {
			continue
		}
		if st, ok := tn.Type().Underlying().(*types.Struct); ok {
			for sf := range st.Fields() {
				if sf == f {
					return name
				}
			}
		}
	}
	return ""
}

// namedRefs returns the named types of this package that t refers to.
func (hp *hostPkg) namedRefs(t types.Type, seen map[types.Type]bool, out *[]*types.Named) {
	if seen[t] {
		return
	}
	seen[t] = true
	switch t := types.Unalias(t).(type) {
	case *types.Named:
		if t.Obj().Pkg() == hp.pkg {
			*out = append(*out, t)
		}
		for a := range t.TypeArgs().Types() {
			hp.namedRefs(a, seen, out)
		}
	case *types.Pointer:
		hp.namedRefs(t.Elem(), seen, out)
	case *types.Slice:
		hp.namedRefs(t.Elem(), seen, out)
	case *types.Array:
		hp.namedRefs(t.Elem(), seen, out)
	case *types.Map:
		hp.namedRefs(t.Key(), seen, out)
		hp.namedRefs(t.Elem(), seen, out)
	case *types.Chan:
		hp.namedRefs(t.Elem(), seen, out)
	case *types.Struct:
		for f := range t.Fields() {
			hp.namedRefs(f.Type(), seen, out)
		}
	case *types.Signature:
		for v := range t.Params().Variables() {
			hp.namedRefs(v.Type(), seen, out)
		}
		for v := range t.Results().Variables() {
			hp.namedRefs(v.Type(), seen, out)
		}
	case *types.Interface:
		for m := range t.Methods() {
			hp.namedRefs(m.Type(), seen, out)
		}
	}
}

func (g *pkgGen) run() {
	g.asmDispatch = map[string]map[string]string{}
	g.prepare()
	for _, hp := range g.hs {
		hp.rename = map[types.Object]string{}
		hp.hoist = map[types.Object]bool{}
		hp.drop = map[types.Object]bool{}
	}

	// Names in host copies.
	for _, hp := range g.hs {
		for name, obj := range hp.decls {
			if g.dedup[name] {
				hp.drop[obj] = true
				continue
			}
			hp.rename[obj] = hostName(hp.host, name)
		}
		scope := hp.pkg.Scope()
		for _, name := range scope.Names() {
			tn, ok := scope.Lookup(name).(*types.TypeName)
			if !ok || (hp.inOnly(tn.Pos()) && !g.dedup[name]) {
				continue
			}
			nt, ok := tn.Type().(*types.Named)
			if !ok {
				continue
			}
			// Methods the host files declare on shared types.
			for m := range nt.Methods() {
				if hp.inOnly(m.Pos()) {
					hp.rename[m] = hostName(hp.host, m.Name())
				}
			}
		}
	}
	// A host interface's method that host methods on shared types
	// implement has their name.
	for _, hp := range g.hs {
		renamed := map[string]bool{}
		for obj := range hp.rename {
			if fn, ok := obj.(*types.Func); ok && fn.Signature().Recv() != nil {
				renamed[fn.Name()] = true
			}
		}
		for name, obj := range hp.decls {
			tn, ok := obj.(*types.TypeName)
			if !ok || g.dedup[name] {
				continue
			}
			it, ok := tn.Type().Underlying().(*types.Interface)
			if !ok {
				continue
			}
			for m := range it.ExplicitMethods() {
				if renamed[m.Name()] {
					hp.rename[m] = hostName(hp.host, m.Name())
				}
			}
		}
	}
	g.hoistFields()

	g.api = map[string]*iface{}
	add := func(hp *hostPkg, kind, name string, obj types.Object, use ast.Expr) {
		key := kind + " " + name
		ic := g.api[key]
		if ic == nil {
			ic = &iface{kind: kind, name: name, objs: map[string]types.Object{}}
			g.api[key] = ic
		}
		ic.objs[hp.host] = obj
		if use != nil && hp == g.first() {
			ic.uses = append(ic.uses, use)
		}
	}
	g.canon = map[string]bool{}
	// What the shared files (and shared tests) use.
	for _, hp := range g.hs {
		for id, obj := range hp.info.Uses {
			if !g.sharedUse(id.Pos()) || obj.Pkg() != hp.pkg {
				continue
			}
			switch o := obj.(type) {
			case *types.Func:
				if recv := o.Signature().Recv(); recv != nil {
					if !hp.inOnly(o.Pos()) {
						continue // declared by a shared file
					}
					if n := g.hostNamed(hp, recv.Type()); n != nil {
						add(hp, "method", n.Obj().Name()+"."+o.Name(), o, id)
					} else {
						add(hp, "method", recvName(recv.Type())+"."+o.Name(), o, id)
					}
					continue
				}
				if _, ok := hp.rename[o]; ok {
					add(hp, "func", o.Name(), o, id)
				}
			case *types.Var:
				if o.IsField() {
					if owner := hp.fieldOwner(o); owner != "" && hp.inOnly(hp.pkg.Scope().Lookup(owner).Pos()) && !g.union[owner] && !g.dedup[owner] {
						// The first host's layout is the ape type's; other
						// hosts' are converted field by field.
						g.canon[owner] = true
					}
					continue
				}
				if _, ok := hp.rename[o]; ok {
					add(hp, "var", o.Name(), o, id)
				}
			case *types.Const:
				if _, ok := hp.rename[o]; ok {
					add(hp, "const", o.Name(), o, id)
				}
			case *types.TypeName:
				if _, ok := hp.rename[o]; ok {
					add(hp, "type", o.Name(), o, id)
				}
			}
		}
	}
	// Names a shared file marks with a one-argument linkname: the ape
	// package must have them.
	for _, hp := range g.hs {
		for name, f := range hp.files {
			if !g.shared[name] {
				continue
			}
			for _, cg := range f.Comments {
				for _, c := range cg.List {
					fs := strings.Fields(c.Text)
					if len(fs) != 2 || fs[0] != "//go:linkname" && fs[0] != "//go:linknamestd" {
						continue
					}
					o, ok := hp.decls[fs[1]]
					if !ok || g.dedup[fs[1]] {
						continue
					}
					switch o.(type) {
					case *types.Func:
						add(hp, "func", o.Name(), o, nil)
					case *types.Var:
						add(hp, "var", o.Name(), o, nil)
					}
				}
			}
		}
	}
	// What a package other than the runtime exports.
	if g.path != "runtime" {
		for _, hp := range g.hs {
			for name, obj := range hp.decls {
				if !token.IsExported(name) {
					continue
				}
				switch o := obj.(type) {
				case *types.Func:
					add(hp, "func", name, o, nil)
				case *types.Var:
					// (Not one of functions of host values: only host
					// copies can use it.)
					if !g.dedup[name] && !g.funcOfHost(hp, o.Type()) {
						add(hp, "var", name, o, nil)
					}
				case *types.Const:
					add(hp, "const", name, o, nil)
				case *types.TypeName:
					if !g.dedup[name] {
						add(hp, "type", name, o, nil)
						// A struct of data (exported fields only) is the
						// first host's, converted field by field; one with
						// state is not.
						if g.dataStructForAll(name) {
							g.canon[name] = true
						}
					}
				}
			}
			// Methods host files declare on shared types satisfy shared
			// interfaces: they are all dispatched.
			for obj := range hp.rename {
				if fn, ok := obj.(*types.Func); ok && fn.Signature().Recv() != nil {
					// (Not one of another package's host values: only
					// host code can call it.)
					if g.hostNamed(hp, fn.Signature().Recv().Type()) == nil && g.otherHostRef(hp, fn.Signature()) == nil {
						add(hp, "method", recvName(fn.Signature().Recv().Type())+"."+fn.Name(), fn, nil)
					}
				}
			}
			// Exported methods of exported host types.
			for name, obj := range hp.decls {
				tn, ok := obj.(*types.TypeName)
				if !ok || !token.IsExported(name) || g.dedup[name] {
					continue
				}
				if nt, ok := tn.Type().(*types.Named); ok {
					for m := range nt.Methods() {
						if token.IsExported(m.Name()) && hp.inOnly(m.Pos()) {
							add(hp, "method", name+"."+m.Name(), m, nil)
						}
					}
				}
			}
		}
	}
	// The variables host copies share are the ape package's.
	for name := range g.sharedVar {
		for _, hp := range g.hs {
			add(hp, "var", name, hp.decls[name], nil)
		}
	}
	// Functions shared assembly calls.
	for _, hp := range g.hs {
		for _, name := range hp.bp.SFiles {
			if !g.shared[name] {
				continue
			}
			src, err := os.ReadFile(filepath.Join(g.dir, name))
			if err != nil {
				fatalf("%v", err)
			}
			for _, m := range asmSym.FindAllSubmatch(src, -1) {
				if fn, ok := hp.decls[string(m[2])].(*types.Func); ok {
					add(hp, "func", fn.Name(), fn, nil)
				}
			}
		}
	}
	// Canonical types' field types are canonical too.
	for changed := true; changed; {
		changed = false
		for name := range g.canon {
			hp := g.first()
			o := hp.decls[name]
			if o == nil {
				continue
			}
			// (Only data structs held by value: a pointer to another
			// host type is a handle, converted by pointer.)
			var refs []*types.Named
			refsByValue(o.Type().Underlying(), map[types.Type]bool{}, &refs)
			for _, n := range refs {
				st, ok := n.Underlying().(*types.Struct)
				if g.hostNamed(hp, n) != nil && !g.canon[n.Obj().Name()] && (!ok || allExported(st)) {
					g.canon[n.Obj().Name()] = true
					changed = true
				}
			}
		}
	}
	for name := range g.canon {
		for _, hp := range g.hs {
			if o := hp.decls[name]; o != nil {
				add(hp, "type", name, o, nil)
			}
		}
	}
	// A type a dispatched signature mentions must be declared too.
	for _, ic := range g.api {
		if ic.kind != "func" && ic.kind != "method" {
			continue
		}
		for _, hp := range g.hs {
			obj := ic.objs[hp.host]
			if obj == nil {
				continue
			}
			s := obj.Type().(*types.Signature)
			var refs []*types.Named
			hp.namedRefs(s, map[types.Type]bool{}, &refs)
			if s.Recv() != nil {
				hp.namedRefs(s.Recv().Type(), map[types.Type]bool{}, &refs)
			}
			for _, n := range refs {
				if g.hostNamed(hp, n) != nil {
					add(hp, "type", n.Obj().Name(), n.Obj(), nil)
				}
			}
		}
	}
	for name := range g.union {
		for _, hp := range g.hs {
			add(hp, "type", name, hp.pkg.Scope().Lookup(name), nil)
		}
	}
	// The ape type of a host type has the methods every host's has, so
	// that it satisfies the interfaces shared code uses it as.
	var typeNames []string
	for _, ic := range g.api {
		if ic.kind == "type" && len(ic.objs) == len(g.hs) && !g.union[ic.name] {
			typeNames = append(typeNames, ic.name)
		}
	}
	for _, name := range typeNames {
		for _, hp := range g.hs {
			if nt, ok := hp.decls[name].Type().(*types.Named); ok {
				for m := range nt.Methods() {
					// (A shared file's method is the ape type's own.)
					if hp.inOnly(m.Pos()) {
						add(hp, "method", name+"."+m.Name(), m, nil)
					}
				}
			}
		}
	}
	g.findByValue()
	g.declDedup()

	var keys []string
	for k := range g.api {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		ic := g.api[k]
		if ic.kind == "type" && g.canon[ic.name] && ic.objs[g.first().host] != nil && len(ic.objs) != len(g.hs) {
			// Only some hosts have it, but a type every host has refers
			// to it: the first host's.
			g.declType(ic)
			continue
		}
		if len(ic.objs) != len(g.hs) {
			if len(ic.uses) > 0 {
				var have []string
				for h := range ic.objs {
					have = append(have, h)
				}
				sort.Strings(have)
				g.reportf("%s %s: shared code uses it, but only %v declare it", ic.kind, ic.name, have)
			}
			continue
		}
		switch ic.kind {
		case "func", "method":
			g.dispatchFunc(ic)
		case "const":
			g.declConst(ic)
		case "type":
			switch {
			case g.union[ic.name]:
				g.declUnion(ic)
			default:
				g.declType(ic)
			}
		case "var":
			g.declVar(ic)
		}
	}

	g.findPushes()
	for _, hp := range g.hs {
		var names []string
		for f := range hp.only {
			names = append(names, f)
		}
		sort.Strings(names)
		for _, f := range names {
			g.writeGo(hp, f)
		}
		for _, f := range hp.sfiles {
			g.writeAsm(hp, f)
		}
	}
	for _, name := range g.splitNames() {
		g.writeShared(name)
	}
	g.writeDispatch()
	g.writeAsmDispatch()
	g.xoff = g.xtestOff()
	g.auditShared()
}

// findByValue finds the host types that shared code or the ape API
// holds by value: shared structs' fields, shared variables, and dispatched
// functions' parameters and results.
func (g *pkgGen) findByValue() {
	g.byValue = map[string]bool{}
	hp := g.first()
	var ts []types.Type
	scope := hp.pkg.Scope()
	for _, name := range scope.Names() {
		o := scope.Lookup(name)
		file := fileName(o.Pos())
		if !g.shared[file] && !g.dedup[name] {
			continue
		}
		switch o := o.(type) {
		case *types.TypeName:
			ts = append(ts, o.Type().Underlying())
		case *types.Var:
			ts = append(ts, o.Type())
		}
	}
	for _, ic := range g.api {
		if ic.kind != "func" && ic.kind != "method" {
			continue
		}
		for _, h := range g.hs {
			if o := ic.objs[h.host]; o != nil {
				ts = append(ts, sigTypes(o.Type().(*types.Signature))...)
			}
		}
	}
	for _, t := range ts {
		var refs []*types.Named
		refsByValue(t, map[types.Type]bool{}, &refs)
		for _, n := range refs {
			if n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == g.path {
				g.byValue[n.Obj().Name()] = true
			}
		}
	}
}

// allExported reports whether every named field of st is exported.
func allExported(st *types.Struct) bool {
	for f := range st.Fields() {
		if f.Name() != "_" && !strings.HasPrefix(f.Name(), "pad_") && !strings.HasPrefix(f.Name(), "Pad_") && !f.Exported() {
			return false
		}
	}
	return true
}

func recvName(t types.Type) string {
	t = types.Unalias(t)
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	if n, ok := types.Unalias(t).(*types.Named); ok {
		return n.Obj().Name()
	}
	return "?"
}

// findDedup finds the types every host declares the same way.
func (g *pkgGen) findDedup() {
	g.dedup = map[string]bool{}
	hp0 := g.first()
	for name, obj := range hp0.decls {
		tn, ok := obj.(*types.TypeName)
		if !ok {
			continue
		}
		if n, ok := tn.Type().(*types.Named); ok && n.TypeParams().Len() > 0 {
			continue
		}
		same := true
		var shape string
		for i, hp := range g.hs {
			o, ok := hp.decls[name].(*types.TypeName)
			if !ok || o.IsAlias() != tn.IsAlias() {
				same = false
				break
			}
			s := g.shape(hp, o)
			if i == 0 {
				shape = s
			} else if s != shape {
				same = false
			}
		}
		// (Written the same way, but of another package's host types.)
		for _, hp := range g.hs {
			if o, ok := hp.decls[name]; ok && g.otherHostRef(hp, o.Type().Underlying()) != nil {
				same = false
			}
		}
		if same {
			g.dedup[name] = true
		}
	}
	// Variables every host declares with the same type and value. A
	// variable linked to another symbol (_cgo_mmap) is declared once even
	// if only some hosts declare it.
	names := map[string]bool{}
	for _, hp := range g.hs {
		for name, obj := range hp.decls {
			if _, ok := obj.(*types.Var); ok {
				names[name] = true
			}
		}
	}
	for name := range names {
		same := true
		var t string
		n := 0
		for _, hp := range g.hs {
			o, ok := hp.decls[name].(*types.Var)
			if !ok {
				continue
			}
			i := n
			n++
			spec, value := hp.varSpec(o)
			if spec == nil || value != nil && (g.dependsOnHost(hp, value) != "" || g.callsHostOnly(hp, value)) {
				same = false
				break
			}
			s := types.TypeString(o.Type(), func(p *types.Package) string { return p.Path() }) + " " + hp.linkname(o) + " = " + hp.text(value)
			if i == 0 {
				t = s
			} else if s != t {
				same = false
			}
		}
		if same && n < len(g.hs) && (!strings.HasSuffix(t, " = ") || !strings.Contains(t, "//go:linkname")) {
			same = false // only some hosts have it: only a linked zero variable is one
		}
		if same && n > 0 {
			g.dedup[name] = true
		}
	}
	// A type is the same only if the host types it refers to are.
	for changed := true; changed; {
		changed = false
		for name := range g.dedup {
			for _, hp := range g.hs {
				if hp.decls[name] == nil {
					continue
				}
				var refs []*types.Named
				hp.namedRefs(hp.decls[name].Type(), map[types.Type]bool{}, &refs)
				if tn, ok := hp.decls[name].(*types.TypeName); ok {
					refs = nil
					hp.namedRefs(tn.Type().Underlying(), map[types.Type]bool{}, &refs)
				}
				for _, n := range refs {
					if hp.inOnly(n.Obj().Pos()) && !g.dedup[n.Obj().Name()] {
						delete(g.dedup, name)
						changed = true
					}
				}
				// Or another package's that differ, held by value (a
				// pointer to one is the ape type, which host copies
				// convert).
				var all []*types.Named
				refsByValue(hp.decls[name].Type().Underlying(), map[types.Type]bool{}, &all)
				for _, n := range all {
					if n.Obj().Pkg() != nil && n.Obj().Pkg() != hp.pkg && g.u.hostDiffers(n.Obj().Pkg().Path(), hp.host, n.Obj()) {
						if g.dedup[name] {
							delete(g.dedup, name)
							changed = true
						}
					}
				}
			}
		}
	}
}

// varSpec returns the declaration of v, if it declares v alone, and
// its value, if any.
func (hp *hostPkg) varSpec(v *types.Var) (*ast.ValueSpec, ast.Expr) {
	f := hp.files[fileName(v.Pos())]
	if f == nil {
		return nil, nil
	}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, s := range gd.Specs {
			vs := s.(*ast.ValueSpec)
			for _, id := range vs.Names {
				if hp.info.Defs[id] != v {
					continue
				}
				switch {
				case len(vs.Names) != 1:
					return nil, nil
				case len(vs.Values) == 1:
					return vs, vs.Values[0]
				case len(vs.Values) == 0:
					return vs, nil
				}
				return nil, nil
			}
		}
	}
	return nil, nil
}

// text returns the source text of n.
func (hp *hostPkg) text(n ast.Node) string {
	if n == nil {
		return ""
	}
	p, e := fset.Position(n.Pos()), fset.Position(n.End())
	src, err := os.ReadFile(p.Filename)
	if err != nil {
		return ""
	}
	return string(src[p.Offset:e.Offset])
}

// callsHostOnly reports whether e uses an unexported function or a
// variable of this package that a host file declares.
func (g *pkgGen) callsHostOnly(hp *hostPkg, e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			switch o := hp.info.Uses[id].(type) {
			case *types.Func:
				if o.Pkg() == hp.pkg && hp.inOnly(o.Pos()) && !token.IsExported(o.Name()) {
					found = true
				}
			case *types.Var:
				if o.Pkg() == hp.pkg && hp.inOnly(o.Pos()) && !o.IsField() {
					found = true
				}
			case *types.Const:
				if o.Pkg() == hp.pkg && hp.inOnly(o.Pos()) {
					found = true // renamed in host copies
				}
			case *types.TypeName:
				if o.Pkg() == hp.pkg && hp.inOnly(o.Pos()) && !g.dedup[o.Name()] {
					found = true
				}
			}
		}
		return true
	})
	return found
}

// asmDefines reports whether hp's assembly defines the symbol pkg.name.
func (g *pkgGen) asmDefines(hp *hostPkg, sym string) bool {
	pkg, name, _ := cutLast(sym, ".")
	text := "TEXT " + strings.ReplaceAll(pkg, "/", "∕") + "·" + name
	for _, f := range hp.sfiles {
		src, err := os.ReadFile(filepath.Join(g.dir, f))
		if err == nil && strings.Contains(string(src), text) {
			return true
		}
	}
	return false
}

// hasBody reports whether the function has a Go body.
func (hp *hostPkg) hasBody(fn *types.Func) bool {
	f := hp.files[fileName(fn.Pos())]
	if f == nil {
		return false
	}
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Pos() == fn.Pos() {
			return fd.Body != nil
		}
	}
	return false
}

// linkname returns the //go:linkname directive naming obj, or "".
func (hp *hostPkg) linkname(obj types.Object) string {
	f := hp.files[fileName(obj.Pos())]
	if f == nil {
		return ""
	}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if fs := strings.Fields(c.Text); len(fs) >= 2 && (fs[0] == "//go:linkname" || fs[0] == "//go:linknamestd") && fs[1] == obj.Name() {
				return c.Text
			}
		}
	}
	return ""
}

// shape prints a host's type declaration for comparison.
func (g *pkgGen) shape(hp *hostPkg, tn *types.TypeName) string {
	q := func(p *types.Package) string {
		if p == hp.pkg {
			return ""
		}
		return p.Path()
	}
	if tn.IsAlias() {
		return "= " + types.TypeString(types.Unalias(tn.Type()), q)
	}
	// (A local alias names what it stands for, which may differ.)
	out := types.TypeString(tn.Type().Underlying(), q)
	var aliases []string
	var walk func(t types.Type, depth int)
	walk = func(t types.Type, depth int) {
		if depth > 8 {
			return
		}
		switch t := t.(type) {
		case *types.Alias:
			if t.Obj().Pkg() == hp.pkg {
				aliases = append(aliases, t.Obj().Name()+"="+types.TypeString(types.Unalias(t), q))
			}
		case *types.Pointer:
			walk(t.Elem(), depth+1)
		case *types.Slice:
			walk(t.Elem(), depth+1)
		case *types.Array:
			walk(t.Elem(), depth+1)
		case *types.Map:
			walk(t.Key(), depth+1)
			walk(t.Elem(), depth+1)
		case *types.Chan:
			walk(t.Elem(), depth+1)
		case *types.Struct:
			for f := range t.Fields() {
				walk(f.Type(), depth+1)
			}
		case *types.Signature:
			for v := range t.Params().Variables() {
				walk(v.Type(), depth+1)
			}
			for v := range t.Results().Variables() {
				walk(v.Type(), depth+1)
			}
		}
	}
	walk(tn.Type().Underlying(), 0)
	if len(aliases) > 0 {
		out += " " + strings.Join(aliases, " ")
	}
	return out
}

// declDedup declares the types every host declares the same way.
func (g *pkgGen) declDedup() {
	var names []string
	for n := range g.dedup {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		hp := g.first()
		for _, h := range g.hs {
			if h.decls[n] != nil {
				hp = h
				break
			}
		}
		if v, ok := hp.decls[n].(*types.Var); ok {
			if l := hp.linkname(v); l != "" {
				fmt.Fprintf(&g.decls, "\n%s", l)
			}
			if _, value := hp.varSpec(v); value != nil {
				ast.Inspect(value, func(n ast.Node) bool {
					if id, ok := n.(*ast.Ident); ok {
						if pn, ok := hp.info.Uses[id].(*types.PkgName); ok {
							g.imports[pn.Imported().Path()] = pn.Name()
						}
					}
					return true
				})
				fmt.Fprintf(&g.decls, "\nvar %s %s = %s\n", n, g.typeString(hp, v.Type()), hp.text(value))
				continue
			}
			fmt.Fprintf(&g.decls, "\nvar %s %s\n", n, g.typeString(hp, v.Type()))
			continue
		}
		tn := hp.decls[n].(*types.TypeName)
		if tn.IsAlias() {
			fmt.Fprintf(&g.decls, "\ntype %s = %s\n", n, g.typeString(hp, types.Unalias(tn.Type())))
		} else {
			fmt.Fprintf(&g.decls, "\ntype %s %s\n", n, g.typeString(hp, tn.Type().Underlying()))
		}
	}
}

// hoistFields hoists the fields shared code uses out of union types.
func (g *pkgGen) hoistFields() {
	names := map[string]bool{}
	for _, hp := range g.hs {
		for id, obj := range hp.info.Uses {
			v, ok := obj.(*types.Var)
			if !ok || !v.IsField() || !g.sharedUse(id.Pos()) {
				continue
			}
			if ut := hp.fieldOwner(v); ut != "" && g.union[ut] {
				names[ut+"."+v.Name()] = true
			}
		}
	}
	for _, hp := range g.hs {
		for name := range g.union {
			tn := hp.pkg.Scope().Lookup(name)
			if tn == nil {
				continue
			}
			for f := range tn.Type().Underlying().(*types.Struct).Fields() {
				if names[name+"."+f.Name()] {
					hp.hoist[f] = true
					delete(hp.rename, f)
				} else {
					hp.rename[f] = hostName(hp.host, f.Name())
				}
			}
		}
	}
}

var linknameRE = regexp.MustCompile(`(?m)^//go:linkname(?:std)? (\S+) (\S+)\s*$`)

// findPushes finds the functions host files push to other packages by
// linkname, and makes a function that dispatches for each symbol more
// than one host pushes.
func (g *pkgGen) findPushes() {
	g.pushes = map[string]map[string]*types.Func{}
	for _, hp := range g.hs {
		for name := range hp.only {
			for _, cg := range hp.files[name].Comments {
				for _, c := range cg.List {
					m := linknameRE.FindStringSubmatch(c.Text)
					if m == nil {
						continue
					}
					// (A push to the package's own name is one too: time
					// gives its legacy functions their old names.)
					if _, _, ok := cutLast(m[2], "."); !ok || m[2] == hp.bp.ImportPath+"."+m[1] {
						continue
					}
					fn, ok := hp.pkg.Scope().Lookup(m[1]).(*types.Func)
					if !ok || !hp.hasBody(fn) && !g.asmDefines(hp, m[2]) {
						continue // a pull
					}
					if g.pushes[m[2]] == nil {
						g.pushes[m[2]] = map[string]*types.Func{}
					}
					g.pushes[m[2]][hp.host] = fn
				}
			}
		}
	}
	var targets []string
	for t, by := range g.pushes {
		if len(by) < 2 {
			delete(g.pushes, t)
			continue
		}
		targets = append(targets, t)
	}
	sort.Strings(targets)
	for _, t := range targets {
		by := g.pushes[t]
		// Pushed by only some hosts: the others do not use it.
		savedHosts := g.hs
		var present []*hostPkg
		for _, hp := range g.hs {
			if by[hp.host] != nil {
				present = append(present, hp)
			}
		}
		g.hs = present
		ic := &iface{kind: "func", name: t, objs: map[string]types.Object{}}
		for h, fn := range by {
			ic.objs[h] = fn
		}
		fn := by[g.first().host]
		wrapper := "ape_" + strings.NewReplacer("/", "_", ".", "_").Replace(t)
		saved := g.decls
		g.decls = bytes.Buffer{}
		g.dispatchFunc(ic)
		out := g.decls.String()
		g.decls = saved
		out = strings.Replace(out, "func "+fn.Name()+"(", "//go:linkname "+wrapper+" "+t+"\nfunc "+wrapper+"(", 1)
		g.decls.WriteString(out)
		g.hs = savedHosts
	}
}

func cutLast(s, sep string) (before, after string, ok bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

// dataStructForAll reports whether every host that declares the type
// name declares a struct of data: one the ape package can have as the
// first host's, converted field by field.
func (g *pkgGen) dataStructForAll(name string) bool {
	for _, hp := range g.hs {
		o, ok := hp.decls[name]
		if !ok {
			continue
		}
		st, ok := o.Type().Underlying().(*types.Struct)
		if !ok || !allExported(st) {
			return false
		}
	}
	return true
}
