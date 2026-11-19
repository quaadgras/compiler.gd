// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Apegen writes the host files of a package for GOOS=ape.
//
// A GOOS=ape program runs on several host systems. A file of a package
// that builds for every host is shared: it is built once, as it is. A
// file that builds only for some hosts is copied once per host, with
// every package-level name it declares prefixed by the host
// ("newosproc" becomes "linux_newosproc"), and the generated
// zape_dispatch_$GOARCH.go gives the shared files what they use of the
// host files:
//
//   - a function becomes a function that calls the copy of the host the
//     program runs on (apehost, set at startup);
//   - a method of a host type becomes a method of the ape type that
//     calls the host's method on the same memory;
//   - a constant with the same value everywhere stays a constant; one
//     that differs becomes a variable, set by apeinitHost;
//   - a type with the same definition everywhere is declared once; a
//     type that differs becomes an opaque type as large as the largest
//     host's, which dispatched functions convert by pointer; a host type
//     that a shared struct embeds (the runtime's mOS) becomes a struct of
//     every host's type, and the fields shared code uses are hoisted out
//     of the hosts' types into it;
//   - a variable becomes a variable set by apeinitHost (a slice of the
//     host's array, if the hosts' arrays differ in length).
//
// What it cannot do is reported, for the package's hand-written ape files.
//
// Usage:
//
//	go run cmd/apegen [-w] [-hosts linux,freebsd] [-arch amd64] pkgdir
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/build"
	"go/constant"
	"go/format"
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

var (
	write = flag.Bool("w", false, "write the files")
	hosts = flag.String("hosts", "linux,freebsd", "host systems")
	arch  = flag.String("arch", "amd64", "architecture")
	// osdep lists the packages that differ between hosts. A host file
	// that imports one imports the host's variant, ape/HOST/PATH.
	constTypes = flag.String("types", "_SIGURG=uint32", "types of constants that differ between hosts and that shared code does not use")
	maxConsts = flag.String("max", "_NSIG", "constants that are the largest host's value")
	osdep = flag.String("osdep", "internal/runtime/syscall/linux,internal/runtime/syscall/windows,internal/runtime/cgroup,internal/syscall/unix,internal/syscall/windows,syscall", "packages that differ between hosts")
)

type hostPkg struct {
	host   string
	ctxt   build.Context
	bp     *build.Package
	files  map[string]*ast.File // all Go files
	pkg    *types.Package
	info   *types.Info
	only   map[string]bool // Go files only some hosts build
	sfiles []string        // assembly files only some hosts build
	rename map[types.Object]string
	hoist  map[types.Object]bool // fields moved into the ape type

	parents map[ast.Node]ast.Node
}

var fset = token.NewFileSet()

func main() {
	log.SetFlags(0)
	log.SetPrefix("apegen: ")
	flag.Parse()
	if flag.NArg() != 1 {
		log.Fatal("usage: apegen [-w] [-hosts h1,h2] [-arch a] pkgdir")
	}
	dir, err := filepath.Abs(flag.Arg(0))
	if err != nil {
		log.Fatal(err)
	}

	var hs []*hostPkg
	for _, h := range strings.Split(*hosts, ",") {
		ctxt := build.Default
		ctxt.GOOS, ctxt.GOARCH, ctxt.CgoEnabled = h, *arch, false
		bp, err := ctxt.ImportDir(dir, 0)
		if err != nil {
			log.Fatalf("%s: %v", h, err)
		}
		hs = append(hs, &hostPkg{host: h, ctxt: ctxt, bp: bp})
	}

	// Files every host builds are shared.
	count := map[string]int{}
	for _, hp := range hs {
		for _, f := range slices.Concat(hp.bp.GoFiles, hp.bp.SFiles) {
			count[f]++
		}
	}
	shared := map[string]bool{}
	for f, n := range count {
		if n == len(hs) {
			shared[f] = true
		}
	}
	for _, hp := range hs {
		hp.only = map[string]bool{}
		for _, f := range hp.bp.GoFiles {
			if !shared[f] {
				hp.only[f] = true
			}
		}
		for _, f := range hp.bp.SFiles {
			if !shared[f] {
				hp.sfiles = append(hp.sfiles, f)
			}
		}
		hp.check(dir)
	}

	g := &generator{dir: dir, hs: hs, shared: shared}
	g.run()
	for _, r := range g.report {
		fmt.Println(r)
	}
}

// check type-checks the package as the host builds it.
func (hp *hostPkg) check(dir string) {
	hp.files = map[string]*ast.File{}
	var files []*ast.File
	for _, name := range hp.bp.GoFiles {
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			log.Fatal(err)
		}
		hp.files[name] = f
		files = append(files, f)
	}
	saved := build.Default
	build.Default = hp.ctxt
	defer func() { build.Default = saved }()
	var errs []error
	conf := types.Config{
		Importer: importer.ForCompiler(fset, "source", nil).(types.ImporterFrom),
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
	if len(errs) > 0 {
		for _, e := range errs[:min(len(errs), 10)] {
			log.Printf("%s: %v", hp.host, e)
		}
		log.Fatalf("%s: %d type errors", hp.host, len(errs))
	}
}

func (hp *hostPkg) inOnly(pos token.Pos) bool {
	return hp.only[filepath.Base(fset.Position(pos).Filename)]
}

type generator struct {
	dir    string
	hs     []*hostPkg
	shared map[string]bool
	report []string

	canon map[string]bool // host types whose fields shared code uses

	// pushes maps a linkname target in another package (time.now) to
	// the function each host pushes to it.
	pushes map[string]map[string]*types.Func

	// asmDispatch maps a function a shared file declares and host
	// assembly implements to each host's ABI selector.
	asmDispatch map[string]map[string]string

	decls bytes.Buffer // ape declarations
	init  bytes.Buffer // body of apeinitHost
}

// iface is something the shared files use that the host files declare.
type iface struct {
	kind string // "func", "method", "const", "type", "var", "field"
	name string // for a method, "T.m"
	objs map[string]types.Object
	uses []ast.Expr // in shared files (of the first host)
}

func (g *generator) reportf(format string, args ...any) {
	g.report = append(g.report, fmt.Sprintf(format, args...))
}

func (g *generator) run() {
	g.canon = map[string]bool{}
	ifaces := map[string]*iface{}
	union := map[string]bool{} // host types embedded in shared structs
	for _, hp := range g.hs {
		hp.rename = map[types.Object]string{}
		hp.hoist = map[types.Object]bool{}
		scope := hp.pkg.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			if hp.inOnly(obj.Pos()) {
				hp.rename[obj] = hp.host + "_" + name
			}
		}
		for _, name := range scope.Names() {
			tn, ok := scope.Lookup(name).(*types.TypeName)
			if !ok || hp.inOnly(tn.Pos()) {
				continue
			}
			nt, ok := tn.Type().(*types.Named)
			if !ok {
				continue
			}
			// Methods the host files declare on shared types.
			for m := range nt.Methods() {
				if hp.inOnly(m.Pos()) {
					hp.rename[m] = hp.host + "_" + m.Name()
				}
			}
			// Host types embedded in shared structs.
			if st, ok := nt.Underlying().(*types.Struct); ok {
				for f := range st.Fields() {
					if en := hp.hostNamed(f.Type()); f.Embedded() && en != nil {
						union[en.Obj().Name()] = true
					}
				}
			}
		}
	}
	// Fields of union types: shared code's are hoisted, the rest prefixed.
	for _, hp := range g.hs {
		for id, obj := range hp.info.Uses {
			v, ok := obj.(*types.Var)
			if !ok || !v.IsField() || hp.inOnly(id.Pos()) {
				continue
			}
			if ut := hp.fieldOwner(v); ut != "" && union[ut] {
				hp.hoist[v] = true
			}
		}
	}
	// A field shared code uses in one host is hoisted in all.
	hoistNames := map[string]bool{}
	for _, hp := range g.hs {
		for f := range hp.hoist {
			hoistNames[hp.fieldOwner(f.(*types.Var))+"."+f.Name()] = true
		}
	}
	for _, hp := range g.hs {
		for name := range union {
			tn := hp.pkg.Scope().Lookup(name)
			for f := range tn.Type().Underlying().(*types.Struct).Fields() {
				if hoistNames[name+"."+f.Name()] {
					hp.hoist[f] = true
				} else {
					hp.rename[f] = hp.host + "_" + f.Name()
				}
			}
		}
	}
	// What the shared files use.
	for _, hp := range g.hs {
		for id, obj := range hp.info.Uses {
			if hp.inOnly(id.Pos()) {
				continue
			}
			kind := ""
			name := obj.Name()
			switch o := obj.(type) {
			case *types.Func:
				kind = "func"
				if recv := o.Signature().Recv(); recv != nil {
					kind = "method"
					n := hp.hostNamed(recv.Type())
					if n != nil && !hp.inOnly(o.Pos()) {
						// Declared by a shared file on a host type.
						continue
					}
					if n == nil {
						if _, ok := hp.rename[o]; ok {
							g.reportf("%s: method %s declared by a host file on a shared type", hp.host, o.FullName())
						}
						continue
					}
					name = n.Obj().Name() + "." + name
				} else if _, ok := hp.rename[o]; !ok {
					continue
				}
			case *types.Var:
				if o.IsField() {
					if owner := hp.fieldOwner(o); owner != "" && hp.inOnly(hp.pkg.Scope().Lookup(owner).Pos()) && !union[owner] {
						// The first host's layout is the ape type's; other
						// hosts' are converted field by field.
						g.canon[owner] = true
					}
					continue
				}
				if _, ok := hp.rename[o]; !ok {
					continue
				}
				kind = "var"
			case *types.Const:
				if _, ok := hp.rename[o]; !ok {
					continue
				}
				kind = "const"
			case *types.TypeName:
				if _, ok := hp.rename[o]; !ok {
					continue
				}
				kind = "type"
			default:
				continue
			}
			key := kind + " " + name
			ic := ifaces[key]
			if ic == nil {
				ic = &iface{kind: kind, name: name, objs: map[string]types.Object{}}
				ifaces[key] = ic
			}
			ic.objs[hp.host] = obj
			if hp == g.hs[0] {
				ic.uses = append(ic.uses, id)
			}
		}
	}
	// Canonical types' field types are canonical too.
	for changed := true; changed; {
		changed = false
		for name := range g.canon {
			hp := g.first()
			st, ok := hp.pkg.Scope().Lookup(name).Type().Underlying().(*types.Struct)
			if !ok {
				continue
			}
			for f := range st.Fields() {
				if n := hp.hostNamed(f.Type()); n != nil && !g.canon[n.Obj().Name()] {
					g.canon[n.Obj().Name()] = true
					changed = true
				}
			}
		}
	}
	for name := range g.canon {
		key := "type " + name
		if ifaces[key] == nil {
			ifaces[key] = &iface{kind: "type", name: name, objs: map[string]types.Object{}}
		}
		for _, hp := range g.hs {
			if o := hp.pkg.Scope().Lookup(name); o != nil {
				ifaces[key].objs[hp.host] = o
			}
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
				log.Fatal(err)
			}
			for _, m := range asmSym.FindAllSubmatch(src, -1) {
				fn, ok := hp.pkg.Scope().Lookup(string(m[2])).(*types.Func)
				if !ok || !hp.inOnly(fn.Pos()) {
					continue
				}
				key := "func " + fn.Name()
				if ifaces[key] == nil {
					ifaces[key] = &iface{kind: "func", name: fn.Name(), objs: map[string]types.Object{}}
				}
				ifaces[key].objs[hp.host] = fn
			}
		}
	}

	// A type a dispatched signature mentions must be declared too.
	for _, ic := range ifaces {
		if ic.kind != "func" && ic.kind != "method" {
			continue
		}
		for _, hp := range g.hs {
			obj := ic.objs[hp.host]
			if obj == nil {
				continue
			}
			s := obj.Type().(*types.Signature)
			ts := hp.sigTypes(s)
			if s.Recv() != nil {
				ts = append(ts, s.Recv().Type())
			}
			for _, t := range ts {
				if n := hp.hostNamed(t); n != nil {
					key := "type " + n.Obj().Name()
					if ifaces[key] == nil {
						ifaces[key] = &iface{kind: "type", name: n.Obj().Name(), objs: map[string]types.Object{}}
					}
					ifaces[key].objs[hp.host] = n.Obj()
				}
			}
		}
	}
	for name := range union {
		key := "type " + name
		if ifaces[key] == nil {
			ifaces[key] = &iface{kind: "type", name: name, objs: map[string]types.Object{}}
		}
		for _, hp := range g.hs {
			ifaces[key].objs[hp.host] = hp.pkg.Scope().Lookup(name)
		}
	}

	var keys []string
	for k := range ifaces {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		ic := ifaces[k]
		if len(ic.objs) != len(g.hs) {
			var have []string
			for h := range ic.objs {
				have = append(have, h)
			}
			sort.Strings(have)
			g.reportf("%s %s: declared only by %v", ic.kind, ic.name, have)
			continue
		}
		switch ic.kind {
		case "func", "method":
			g.dispatchFunc(ic)
		case "const":
			g.declConst(ic)
		case "type":
			if union[ic.name] {
				g.declUnion(ic)
			} else {
				g.declType(ic)
			}
		case "var":
			g.declVar(ic)
		}
	}

	g.findPushes()
	g.asmDispatch = map[string]map[string]string{}

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
	g.writeDispatch()
	g.writeAsmDispatch()
	g.auditShared()
}

// useType returns the type an untyped constant takes where e uses it:
// the type of the smallest enclosing expression that has one.
func (hp *hostPkg) useType(e ast.Expr) types.Type {
	if hp.parents == nil {
		hp.parents = map[ast.Node]ast.Node{}
		for _, f := range hp.files {
			var stack []ast.Node
			ast.Inspect(f, func(n ast.Node) bool {
				if n == nil {
					stack = stack[:len(stack)-1]
					return true
				}
				if len(stack) > 0 {
					hp.parents[n] = stack[len(stack)-1]
				}
				stack = append(stack, n)
				return true
			})
		}
	}
	var n ast.Node = e
	for n != nil {
		if x, ok := n.(ast.Expr); ok {
			if tv, ok := hp.info.Types[x]; ok && tv.Type != nil {
				if b, ok := tv.Type.(*types.Basic); !ok || b.Info()&types.IsUntyped == 0 {
					return tv.Type
				}
			}
		}
		switch p := hp.parents[n].(type) {
		case *ast.BinaryExpr, *ast.ParenExpr, *ast.UnaryExpr:
			n = p
		case *ast.CallExpr:
			// A conversion T(c), or an argument.
			if tv, ok := hp.info.Types[p.Fun]; ok && tv.IsType() {
				return tv.Type
			}
			if sig, ok := hp.info.Types[p.Fun].Type.(*types.Signature); ok {
				for i, a := range p.Args {
					if a == n && i < sig.Params().Len() {
						return sig.Params().At(i).Type()
					}
				}
			}
			return nil
		case *ast.ValueSpec:
			if p.Type != nil {
				return hp.info.Types[p.Type].Type
			}
			return nil
		default:
			return nil
		}
	}
	return nil
}

// hostNamed returns the host type t is or points to, or nil.
func (hp *hostPkg) hostNamed(t types.Type) *types.Named {
	t = types.Unalias(t)
	if p, ok := t.(*types.Pointer); ok {
		t = types.Unalias(p.Elem())
	}
	n, ok := t.(*types.Named)
	if !ok || n.Obj().Pkg() != hp.pkg || !hp.inOnly(n.Obj().Pos()) {
		return nil
	}
	return n
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

func (hp *hostPkg) sigTypes(s *types.Signature) []types.Type {
	var ts []types.Type
	for v := range s.Params().Variables() {
		ts = append(ts, v.Type())
	}
	for v := range s.Results().Variables() {
		ts = append(ts, v.Type())
	}
	return ts
}

// typeString prints t as the ape files name it: host types by their
// original names.
func (hp *hostPkg) typeString(t types.Type) string {
	return types.TypeString(t, func(p *types.Package) string {
		if p == hp.pkg {
			return ""
		}
		return p.Name()
	})
}

func (g *generator) first() *hostPkg { return g.hs[0] }

func (hp *hostPkg) obj(ic *iface) types.Object { return ic.objs[hp.host] }

// sameAll reports whether f gives the same string for every host.
func (g *generator) sameAll(ic *iface, f func(hp *hostPkg, obj types.Object) string) (string, bool) {
	var s string
	for i, hp := range g.hs {
		v := f(hp, hp.obj(ic))
		if i == 0 {
			s = v
		} else if v != s {
			return s + " vs " + v, false
		}
	}
	return s, true
}

func (g *generator) pragmas(hp *hostPkg, obj types.Object) []string {
	var out []string
	f := hp.files[filepath.Base(fset.Position(obj.Pos()).Filename)]
	if f == nil {
		return nil
	}
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Name.Pos() != obj.Pos() || fd.Doc == nil {
			continue
		}
		for _, c := range fd.Doc.List {
			if !strings.HasPrefix(c.Text, "//go:") {
				continue
			}
			if strings.HasPrefix(c.Text, "//go:linkname") || strings.HasPrefix(c.Text, "//go:noescape") {
				// A one-argument linkname makes the name visible: the
				// dispatch function keeps it. noescape is for bodyless
				// functions only.
				continue
			}
			out = append(out, c.Text)
		}
	}
	return out
}

// conv converts a from the ape type t to the host's type (or back, if
// out). It reports false if it cannot.
func (g *generator) conv(hp *hostPkg, t types.Type, a string, out bool) (string, bool) {
	n := hp.hostNamed(t)
	if n == nil {
		return a, true
	}
	to := hp.host + "_" + n.Obj().Name()
	if out {
		to = n.Obj().Name()
	}
	switch types.Unalias(t).(type) {
	case *types.Pointer:
		return fmt.Sprintf("(*%s)(unsafe.Pointer(%s))", to, a), true
	case *types.Named:
		return fmt.Sprintf("*(*%s)(unsafe.Pointer(&%s))", to, a), true
	}
	return "", false
}

func (g *generator) dispatchFunc(ic *iface) {
	sig, ok := g.sameAll(ic, func(hp *hostPkg, obj types.Object) string {
		s := obj.Type().(*types.Signature)
		var b strings.Builder
		for v := range s.Params().Variables() {
			b.WriteString(hp.typeString(v.Type()) + ",")
		}
		b.WriteString("->")
		for v := range s.Results().Variables() {
			b.WriteString(hp.typeString(v.Type()) + ",")
		}
		return b.String()
	})
	if !ok && !g.intResultsOnly(ic) {
		g.reportf("%s %s: signatures differ: %s", ic.kind, ic.name, sig)
		return
	}
	hp0 := g.first()
	fn := hp0.obj(ic).(*types.Func)
	s := fn.Signature()
	var pragmas []string
	seen := map[string]bool{}
	for _, hp := range g.hs {
		for _, p := range g.pragmas(hp, hp.obj(ic)) {
			if !seen[p] {
				seen[p] = true
				pragmas = append(pragmas, p)
			}
		}
	}
	var params, args []string
	for i := 0; i < s.Params().Len(); i++ {
		t := hp0.typeString(s.Params().At(i).Type())
		a := fmt.Sprintf("a%d", i)
		if s.Variadic() && i == s.Params().Len()-1 {
			params = append(params, a+" ..."+strings.TrimPrefix(t, "[]"))
			args = append(args, a+"...")
			continue
		}
		params = append(params, a+" "+t)
		args = append(args, a)
	}
	var results []string
	for i := 0; i < s.Results().Len(); i++ {
		results = append(results, hp0.typeString(s.Results().At(i).Type()))
	}
	res := strings.Join(results, ", ")
	if len(results) > 1 {
		res = "(" + res + ")"
	}
	var w strings.Builder
	fmt.Fprintln(&w)
	for _, p := range pragmas {
		fmt.Fprintln(&w, p)
	}
	recv := ""
	if ic.kind == "method" {
		recv = fmt.Sprintf("(r %s) ", hp0.typeString(s.Recv().Type()))
	}
	fmt.Fprintf(&w, "func %s%s(%s) %s {\n", recv, fn.Name(), strings.Join(params, ", "), res)
	for i, hp := range g.hs {
		hs := hp.obj(ic).(*types.Func).Signature()
		var cargs, pre, post []string
		for j := 0; j < hs.Params().Len(); j++ {
			pt := hs.Params().At(j).Type()
			if n := hp.hostNamed(pt); n != nil && g.canon[n.Obj().Name()] && hp != hp0 {
				a, b, c, ok := g.fieldConv(hp, pt, args[j], j)
				if !ok {
					g.reportf("%s %s: cannot convert parameter %d field by field", ic.kind, ic.name, j)
					return
				}
				pre = append(pre, a...)
				post = append(post, c...)
				cargs = append(cargs, b)
				continue
			}
			c, ok := g.conv(hp, pt, args[j], false)
			if !ok {
				g.reportf("%s %s: cannot convert parameter %d", ic.kind, ic.name, j)
				return
			}
			cargs = append(cargs, c)
		}
		for j := 0; j < hs.Results().Len(); j++ {
			if n := hp.hostNamed(hs.Results().At(j).Type()); n != nil && g.canon[n.Obj().Name()] && hp != hp0 {
				g.reportf("%s %s: cannot convert a result field by field", ic.kind, ic.name)
				return
			}
		}
		callee := hp.host + "_" + fn.Name()
		if ic.kind == "method" {
			rt := hs.Recv().Type()
			if _, ok := types.Unalias(rt).(*types.Pointer); ok {
				r, _ := g.conv(hp, rt, "r", false)
				callee = r + "." + fn.Name()
			} else {
				callee = fmt.Sprintf("(*%s_%s)(unsafe.Pointer(&r)).%s", hp.host, hp.hostNamed(rt).Obj().Name(), fn.Name())
			}
		}
		call := fmt.Sprintf("%s(%s)", callee, strings.Join(cargs, ", "))
		if len(post) > 0 && len(results) > 0 {
			g.reportf("%s %s: cannot convert parameters back after a call with results", ic.kind, ic.name)
			return
		}
		var body string
		switch len(results) {
		case 0:
			body = call
		case 1:
			c, ok := g.conv(hp, hs.Results().At(0).Type(), "x", true)
			if !ok {
				g.reportf("%s %s: cannot convert result", ic.kind, ic.name)
				return
			}
			if r0 := hp0.typeString(s.Results().At(0).Type()); c == "x" && hp.typeString(hs.Results().At(0).Type()) != r0 {
				body = fmt.Sprintf("return %s(%s)", r0, call)
			} else if c == "x" {
				body = "return " + call
			} else {
				body = fmt.Sprintf("x := %s\nreturn %s", call, c)
			}
		default:
			for j := 0; j < hs.Results().Len(); j++ {
				if hp.hostNamed(hs.Results().At(j).Type()) != nil {
					g.reportf("%s %s: cannot convert results", ic.kind, ic.name)
					return
				}
			}
			body = "return " + call
		}
		body = strings.Join(slices.Concat(pre, []string{body}, post), "\n")
		if i == len(g.hs)-1 {
			fmt.Fprintf(&w, "%s\n", body)
		} else {
			fmt.Fprintf(&w, "if apehost == ape%s {\n%s\n", title(hp.host), body)
			if len(results) == 0 {
				fmt.Fprintf(&w, "return\n")
			}
			fmt.Fprintf(&w, "}\n")
		}
	}
	fmt.Fprintf(&w, "}\n")
	g.decls.WriteString(w.String())
}

var linknameRE = regexp.MustCompile(`(?m)^//go:linkname (\S+) (\S+)\s*$`)

// findPushes finds the functions host files push to other packages by
// linkname, and makes a function that dispatches for each symbol more
// than one host pushes.
func (g *generator) findPushes() {
	g.pushes = map[string]map[string]*types.Func{}
	for _, hp := range g.hs {
		for name := range hp.only {
			for _, cg := range hp.files[name].Comments {
				for _, c := range cg.List {
					m := linknameRE.FindStringSubmatch(c.Text)
					if m == nil {
						continue
					}
					pkg, _, ok := cutLast(m[2], ".")
					if !ok || pkg == hp.bp.ImportPath {
						continue
					}
					fn, ok := hp.pkg.Scope().Lookup(m[1]).(*types.Func)
					if !ok {
						continue
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
		if len(by) != len(g.hs) {
			g.reportf("linkname %s: pushed by only some hosts", t)
			continue
		}
		ic := &iface{kind: "func", name: t, objs: map[string]types.Object{}}
		for h, fn := range by {
			ic.objs[h] = fn
		}
		// The dispatch function is named for the target and pushes it.
		var b bytes.Buffer
		saved := g.decls
		g.decls = b
		g.dispatchFunc(ic)
		out := g.decls.String()
		g.decls = saved
		fn := by[g.first().host]
		wrapper := "ape_" + strings.NewReplacer("/", "_", ".", "_").Replace(t)
		out = strings.Replace(out, "func "+fn.Name()+"(", "//go:linkname "+wrapper+" "+t+"\nfunc "+wrapper+"(", 1)
		g.decls.WriteString(out)
	}
}

// fieldConv converts parameter a of the ape type t (the first host's
// layout) to hp's layout field by field, and back after the call.
func (g *generator) fieldConv(hp *hostPkg, t types.Type, a string, j int) (pre []string, arg string, post []string, ok bool) {
	n := hp.hostNamed(t)
	hst := n.Underlying().(*types.Struct)
	ast := g.first().pkg.Scope().Lookup(n.Obj().Name()).Type().Underlying().(*types.Struct)
	hostT := hp.host + "_" + n.Obj().Name()
	tmp := fmt.Sprintf("t%d", j)
	_, isPtr := types.Unalias(t).(*types.Pointer)
	src := a
	var in, back []string
	for af := range ast.Fields() {
		if af.Name() == "_" || strings.HasPrefix(af.Name(), "pad_") {
			continue
		}
		var hf *types.Var
		for f := range hst.Fields() {
			if f.Name() == af.Name() {
				hf = f
			}
		}
		if hf == nil {
			continue
		}
		to, ok1 := fieldExpr(hp, hf.Type(), af.Type(), src+"."+af.Name(), true)
		from, ok2 := fieldExpr(hp, af.Type(), hf.Type(), tmp+"."+af.Name(), false)
		if !ok1 || !ok2 {
			return nil, "", nil, false
		}
		in = append(in, assign(tmp+"."+af.Name(), hf.Type(), to))
		back = append(back, assign(src+"."+af.Name(), af.Type(), from))
	}
	pre = append(pre, fmt.Sprintf("var %s %s", tmp, hostT))
	if !isPtr {
		pre = append(pre, in...)
		return pre, tmp, nil, true
	}
	p := fmt.Sprintf("p%d", j)
	pre = append(pre, fmt.Sprintf("var %s *%s", p, hostT))
	pre = append(pre, fmt.Sprintf("if %s != nil {\n%s\n%s = &%s\n}", a, strings.Join(in, "\n"), p, tmp))
	post = append(post, fmt.Sprintf("if %s != nil {\n%s\n}", a, strings.Join(back, "\n")))
	return pre, p, post, true
}

// assign stores x in dst, of type t; a pointer is stored as a uintptr,
// without a write barrier: signal handlers convert signal stacks.
func assign(dst string, t types.Type, x string) string {
	if _, ok := t.Underlying().(*types.Pointer); ok {
		if strings.HasPrefix(x, "(") {
			if i := strings.Index(x, ")(unsafe.Pointer("); i > 0 {
				x = "uintptr(" + x[i+len(")(unsafe.Pointer("):len(x)-2] + ")"
			}
		}
		return fmt.Sprintf("*(*uintptr)(unsafe.Pointer(&%s)) = %s", dst, x)
	}
	return fmt.Sprintf("%s = %s", dst, x)
}

// fieldExpr converts x, of type from, to type to: to hp's type if
// toHost, else to the ape type.
func fieldExpr(hp *hostPkg, to, from types.Type, x string, toHost bool) (string, bool) {
	if types.Identical(to, from) {
		return x, true
	}
	if ts, ok := to.Underlying().(*types.Struct); ok {
		fs, ok := from.Underlying().(*types.Struct)
		tn, ok2 := types.Unalias(to).(*types.Named)
		if !ok || !ok2 {
			return "", false
		}
		name := tn.Obj().Name()
		if toHost && hp.inOnly(tn.Obj().Pos()) {
			name = hp.host + "_" + name
		}
		var elems []string
		for tf := range ts.Fields() {
			if tf.Name() == "_" || strings.HasPrefix(tf.Name(), "pad_") {
				continue
			}
			for ff := range fs.Fields() {
				if ff.Name() == tf.Name() {
					e, ok := fieldExpr(hp, tf.Type(), ff.Type(), x+"."+tf.Name(), toHost)
					if !ok {
						return "", false
					}
					elems = append(elems, tf.Name()+": "+e)
				}
			}
		}
		return name + "{" + strings.Join(elems, ", ") + "}", true
	}
	tb, tok := to.Underlying().(*types.Basic)
	fb, fok := from.Underlying().(*types.Basic)
	_, tptr := to.Underlying().(*types.Pointer)
	_, fptr := from.Underlying().(*types.Pointer)
	switch {
	case tok && fok && tb.Info()&types.IsNumeric != 0 && fb.Info()&types.IsNumeric != 0:
		return fmt.Sprintf("%s(%s)", hp.typeString(to), x), true
	case tok && tb.Kind() == types.Uintptr && fptr:
		return fmt.Sprintf("uintptr(unsafe.Pointer(%s))", x), true
	case fok && fb.Kind() == types.Uintptr && tptr:
		return fmt.Sprintf("(%s)(unsafe.Pointer(%s))", hp.typeString(to), x), true
	}
	return "", false
}

// intResultsOnly reports whether the hosts' signatures differ only in
// the integer types of single results.
func (g *generator) intResultsOnly(ic *iface) bool {
	_, ok := g.sameAll(ic, func(hp *hostPkg, obj types.Object) string {
		s := obj.Type().(*types.Signature)
		var b strings.Builder
		for v := range s.Params().Variables() {
			b.WriteString(hp.typeString(v.Type()) + ",")
		}
		if s.Results().Len() != 1 {
			b.WriteString("results")
		} else if t, ok := s.Results().At(0).Type().Underlying().(*types.Basic); !ok || t.Info()&types.IsInteger == 0 {
			b.WriteString(hp.typeString(t))
		}
		return b.String()
	})
	return ok
}

func title(s string) string { return strings.ToUpper(s[:1]) + s[1:] }

func (g *generator) declConst(ic *iface) {
	val, same := g.sameAll(ic, func(hp *hostPkg, obj types.Object) string {
		c := obj.(*types.Const)
		return hp.typeString(c.Type()) + " " + c.Val().ExactString()
	})
	c := g.first().obj(ic).(*types.Const)
	untyped := false
	if b, ok := c.Type().(*types.Basic); ok && b.Info()&types.IsUntyped != 0 {
		untyped = true
	}
	if same {
		if untyped {
			fmt.Fprintf(&g.decls, "\nconst %s = %s\n", ic.name, c.Val().ExactString())
		} else {
			fmt.Fprintf(&g.decls, "\nconst %s %s = %s\n", ic.name, g.first().typeString(c.Type()), c.Val().ExactString())
		}
		return
	}
	for _, m := range strings.Split(*maxConsts, ",") {
		if m == ic.name {
			var best types.Object
			for _, hp := range g.hs {
				o := hp.obj(ic).(*types.Const)
				if best == nil || constant.Compare(o.Val(), token.GTR, best.(*types.Const).Val()) {
					best = o
				}
			}
			fmt.Fprintf(&g.decls, "\n// %s is the largest host's.\nconst %s = %s\n", ic.name, ic.name, best.(*types.Const).Val().ExactString())
			return
		}
	}
	// A variable, of the type shared code uses it as.
	t := g.first().typeString(c.Type())
	if untyped {
		used := map[string]bool{}
		for _, u := range ic.uses {
			if t := g.first().useType(u); t != nil {
				used[g.first().typeString(t)] = true
			}
		}
		var ts []string
		for k := range used {
			ts = append(ts, k)
		}
		sort.Strings(ts)
		t = "int"
		for _, kv := range strings.Split(*constTypes, ",") {
			if k, v, ok := strings.Cut(kv, "="); ok && k == ic.name {
				ts = []string{v}
			}
		}
		if len(ts) == 1 && !strings.HasPrefix(ts[0], "untyped") {
			t = ts[0]
		} else {
			g.reportf("const %s: differs (%s); shared code uses it as %v", ic.name, val, ts)
		}
	}
	fmt.Fprintf(&g.decls, "\n// %s differs between hosts.\nvar %s %s\n", ic.name, ic.name, t)
	g.setInit(ic.name, func(hp *hostPkg) string { return hp.host + "_" + ic.name })
}

func (g *generator) setInit(name string, value func(hp *hostPkg) string) {
	g.setInitStmt(func(hp *hostPkg) string { return name + " = " + value(hp) })
}

func (g *generator) setInitStmt(stmt func(hp *hostPkg) string) {
	for i, hp := range g.hs {
		switch {
		case len(g.hs) == 1:
			fmt.Fprintf(&g.init, "\t%s\n", stmt(hp))
		case i == 0:
			fmt.Fprintf(&g.init, "\tif apehost == ape%s {\n\t\t%s\n", title(hp.host), stmt(hp))
		case i < len(g.hs)-1:
			fmt.Fprintf(&g.init, "\t} else if apehost == ape%s {\n\t\t%s\n", title(hp.host), stmt(hp))
		default:
			fmt.Fprintf(&g.init, "\t} else {\n\t\t%s\n\t}\n", stmt(hp))
		}
	}
}

func (g *generator) declType(ic *iface) {
	under, same := g.sameAll(ic, func(hp *hostPkg, obj types.Object) string {
		return hp.typeString(obj.Type().Underlying())
	})
	if same {
		fmt.Fprintf(&g.decls, "\ntype %s %s\n", ic.name, under)
		return
	}
	if g.canon[ic.name] {
		fmt.Fprintf(&g.decls, "\n// %s is %s's: shared code uses its fields. Functions\n// convert other hosts' field by field.\ntype %s %s\n", ic.name, g.first().host, ic.name, g.first().typeString(g.first().obj(ic).Type().Underlying()))
		return
	}
	var size, align int64
	sz := types.SizesFor("gc", *arch)
	for _, hp := range g.hs {
		t := hp.obj(ic).Type()
		size = max(size, sz.Sizeof(t))
		align = max(align, sz.Alignof(t))
	}
	elem := map[int64]string{1: "uint8", 2: "uint16", 4: "uint32", 8: "uint64"}[align]
	fmt.Fprintf(&g.decls, "\n// %s differs between hosts: functions convert it by pointer.\ntype %s struct{ _ [%d]%s }\n", ic.name, ic.name, (size+align-1)/align, elem)
	g.reportf("type %s: differs between hosts, declared opaque (%s)", ic.name, under)
}

func (g *generator) declUnion(ic *iface) {
	var b strings.Builder
	fmt.Fprintf(&b, "\n// %s is every host's.\ntype %s struct {\n", ic.name, ic.name)
	hoisted := map[string]string{}
	for _, hp := range g.hs {
		st := hp.obj(ic).Type().Underlying().(*types.Struct)
		for f := range st.Fields() {
			if hp.hoist[f] {
				t := hp.typeString(f.Type())
				if prev, ok := hoisted[f.Name()]; ok && prev != t {
					g.reportf("type %s: hoisted field %s differs: %s vs %s", ic.name, f.Name(), prev, t)
				}
				hoisted[f.Name()] = t
			}
		}
	}
	var names []string
	for n := range hoisted {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&b, "\t%s %s\n", n, hoisted[n])
	}
	for _, hp := range g.hs {
		fmt.Fprintf(&b, "\t%s_%s\n", hp.host, ic.name)
	}
	b.WriteString("}\n")
	g.decls.WriteString(b.String())
}

func (g *generator) declVar(ic *iface) {
	t, same := g.sameAll(ic, func(hp *hostPkg, obj types.Object) string {
		return hp.typeString(obj.Type())
	})
	if same {
		fmt.Fprintf(&g.decls, "\nvar %s %s\n", ic.name, t)
		g.setInit(ic.name, func(hp *hostPkg) string {
			if hp.hostNamed(hp.obj(ic).Type()) != nil {
				return fmt.Sprintf("*(*%s)(unsafe.Pointer(&%s_%s))", t, hp.host, ic.name)
			}
			return hp.host + "_" + ic.name
		})
		return
	}
	// Arrays that differ in length are as long as the longest, copied.
	var n int64
	elem, same := g.sameAll(ic, func(hp *hostPkg, obj types.Object) string {
		if a, ok := obj.Type().Underlying().(*types.Array); ok {
			n = max(n, a.Len())
			return hp.typeString(a.Elem())
		}
		return "?" + hp.host
	})
	if same {
		fmt.Fprintf(&g.decls, "\n// %s is as long as the longest host's.\nvar %s [%d]%s\n", ic.name, ic.name, n, elem)
		g.setInitStmt(func(hp *hostPkg) string { return fmt.Sprintf("copy(%s[:], %s_%s[:])", ic.name, hp.host, ic.name) })
		return
	}
	g.reportf("var %s: types differ (%s)", ic.name, t)
}

type edit struct {
	start, end int
	text       string
}

func applyEdits(src []byte, edits []edit) []byte {
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	var out bytes.Buffer
	last := 0
	for _, e := range edits {
		if e.start < last {
			continue // inside an earlier edit
		}
		out.Write(src[last:e.start])
		out.WriteString(e.text)
		last = e.end
	}
	out.Write(src[last:])
	return out.Bytes()
}

var buildLine = regexp.MustCompile(`(?m)^//go:build .*\n|^// \+build .*\n`)

// outName names a host file so that no _GOOS or _GOARCH suffix selects it.
func outName(host, name string) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	return "zape_" + host + "_" + strings.ReplaceAll(base, "_", "-") + ext
}

const header = "// Code generated by cmd/apegen from %s; DO NOT EDIT.\n\n//go:build ape && %s\n\n"

func osdeps() map[string]bool {
	dep := map[string]bool{}
	for _, p := range strings.Split(*osdep, ",") {
		dep[p] = true
	}
	return dep
}

func (g *generator) writeGo(hp *hostPkg, name string) {
	src, err := os.ReadFile(filepath.Join(g.dir, name))
	if err != nil {
		log.Fatal(err)
	}
	f := hp.files[name]
	tf := fset.File(f.Pos())
	off := func(p token.Pos) int { return tf.Offset(p) }
	var edits []edit
	needUnsafe := false

	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Ident:
			obj := hp.info.Defs[n]
			if _, ok := hp.rename[obj]; !ok {
				// An embedded field's name is its type's.
				if u := hp.info.Uses[n]; u != nil {
					obj = u
				}
			}
			if newName, ok := hp.rename[obj]; ok {
				edits = append(edits, edit{off(n.Pos()), off(n.End()), newName})
			}
			// The runtime's own GOOS constant is known in a host file.
			if n.Name == "GOOS" {
				if c, ok := hp.info.Uses[n].(*types.Const); ok && c.Pkg() == hp.pkg {
					edits = append(edits, edit{off(n.Pos()), off(n.End()), fmt.Sprintf("%q", hp.host)})
				}
			}
		case *ast.SelectorExpr:
			// So are goos.IsLinux and goos.GOOS.
			if x, ok := n.X.(*ast.Ident); ok {
				if pn, ok := hp.info.Uses[x].(*types.PkgName); ok && pn.Imported().Path() == "internal/goos" {
					if v := goosValue(hp.host, n.Sel.Name); v != "" {
						edits = append(edits, edit{off(n.Pos()), off(n.End()), v})
						return false
					}
				}
			}
		case *ast.CallExpr:
			// A shared function takes the ape type where the host file
			// passes its own.
			id, ok := n.Fun.(*ast.Ident)
			if !ok {
				break
			}
			fn, ok := hp.info.Uses[id].(*types.Func)
			if !ok || fn.Pkg() != hp.pkg || hp.inOnly(fn.Pos()) {
				break
			}
			for _, arg := range n.Args {
				t := hp.info.Types[arg].Type
				nt := hp.hostNamed(t)
				if nt == nil {
					continue
				}
				if g.canon[nt.Obj().Name()] && hp != g.first() {
					g.reportf("%s: %s: cannot pass a %s to a shared function", hp.host, relPos(arg.Pos()), nt.Obj().Name())
					continue
				}
				if _, isPtr := types.Unalias(t).(*types.Pointer); !isPtr {
					g.reportf("%s: %s: cannot pass a %s by value to a shared function", hp.host, relPos(arg.Pos()), nt.Obj().Name())
					continue
				}
				needUnsafe = true
				edits = append(edits, edit{off(arg.Pos()), off(arg.Pos()), "(*" + nt.Obj().Name() + ")(unsafe.Pointer("})
				edits = append(edits, edit{off(arg.End()), off(arg.End()), "))"})
			}
		case *ast.StructType:
			// Fields hoisted into the ape type leave the host's.
			for _, fl := range n.Fields.List {
				if len(fl.Names) == 1 && hp.hoist[hp.info.Defs[fl.Names[0]]] {
					start := off(fl.Pos())
					if fl.Doc != nil {
						start = off(fl.Doc.Pos())
					}
					end := off(fl.End())
					if fl.Comment != nil {
						end = off(fl.Comment.End())
					}
					edits = append(edits, edit{start, end, ""})
				}
			}
		}
		return true
	})

	if needUnsafe && !slices.ContainsFunc(f.Imports, func(s *ast.ImportSpec) bool { return s.Path.Value == `"unsafe"` }) {
		edits = append(edits, edit{off(f.Name.End()), off(f.Name.End()), "\n\nimport \"unsafe\""})
	}

	// Imports of packages that differ between hosts.
	dep := osdeps()
	for _, imp := range f.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		if dep[p] {
			text := fmt.Sprintf("%q", "ape/"+hp.host+"/"+p)
			if imp.Name == nil {
				text = filepath.Base(p) + " " + text
			}
			edits = append(edits, edit{off(imp.Path.Pos()), off(imp.Path.End()), text})
		}
	}
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, `"`) == "internal/goos" {
			// Its uses may all be constants now.
			end := off(f.Imports[len(f.Imports)-1].End())
			for _, d := range f.Decls {
				if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.IMPORT {
					end = max(end, off(gd.End()))
				}
			}
			edits = append(edits, edit{end, end, "\n\nvar _ = goos.GOOS"})
		}
	}

	// Directives: //go:linkname local [target].
	byName := map[string]string{}
	for obj, n := range hp.rename {
		if obj.Parent() == hp.pkg.Scope() {
			byName[obj.Name()] = n
		}
	}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if !strings.HasPrefix(c.Text, "//go:linkname ") {
				continue
			}
			fs := strings.Fields(c.Text)
			if newName, ok := byName[fs[1]]; ok {
				fs[1] = newName
			}
			if len(fs) == 3 && g.pushes[fs[2]] != nil {
				edits = append(edits, edit{off(c.Pos()), off(c.End()), "// (apegen: zape_dispatch pushes " + fs[2] + ")"})
				continue
			}
			edits = append(edits, edit{off(c.Pos()), off(c.End()), strings.Join(fs, " ")})
		}
	}

	out := applyEdits(src, edits)
	out = buildLine.ReplaceAll(out, nil)
	out = append([]byte(fmt.Sprintf(header, name, *arch)), out...)
	if formatted, err := format.Source(out); err == nil {
		out = formatted
	} else {
		g.reportf("%s: %v", outName(hp.host, name), err)
	}
	g.emit(outName(hp.host, name), out)
}

func cutLast(s, sep string) (before, after string, ok bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

func goosValue(host, name string) string {
	if name == "GOOS" {
		return fmt.Sprintf("%q", host)
	}
	if strings.HasPrefix(name, "Is") {
		if strings.EqualFold(name[2:], host) {
			return "1"
		}
		return "0"
	}
	return ""
}

var (
	asmSym   = regexp.MustCompile(`(runtime)?·([A-Za-z_][A-Za-z0-9_]*)`)
	asmTextABI = regexp.MustCompile(`TEXT\s+(?:runtime)?·([A-Za-z_][A-Za-z0-9_]*)(<[A-Za-z0-9]+>)?`)
	asmIdent = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]*\b`)
)

func (g *generator) writeAsm(hp *hostPkg, name string) {
	src, err := os.ReadFile(filepath.Join(g.dir, name))
	if err != nil {
		log.Fatal(err)
	}
	funcs := map[string]string{}
	typeNames := map[string]string{}
	consts := map[string]string{}
	for obj, n := range hp.rename {
		if obj.Parent() != hp.pkg.Scope() {
			continue
		}
		switch obj.(type) {
		case *types.Func, *types.Var:
			funcs[obj.Name()] = n
		case *types.TypeName:
			typeNames[obj.Name()] = n
		case *types.Const:
			consts[obj.Name()] = n
		}
	}
	// Functions only assembly declares are the host's too, and so are
	// the bodies of functions shared files declare: an assembly
	// function dispatches to them.
	for _, m := range asmTextABI.FindAllSubmatch(src, -1) {
		s := string(m[1])
		if _, ok := funcs[s]; ok {
			continue
		}
		obj := hp.pkg.Scope().Lookup(s)
		if obj == nil {
			funcs[s] = hp.host + "_" + s
		} else if !hp.inOnly(obj.Pos()) {
			funcs[s] = hp.host + "_" + s
			if g.asmDispatch[s] == nil {
				g.asmDispatch[s] = map[string]string{}
			}
			g.asmDispatch[s][hp.host] = string(m[2])
		}
	}
	for t, by := range g.pushes {
		if fn := by[hp.host]; fn != nil {
			pkg, sym, _ := cutLast(t, ".")
			asmName := strings.ReplaceAll(pkg, "/", "∕") + "·" + sym
			src = bytes.ReplaceAll(src, []byte(asmName), []byte("runtime·"+hp.rename[fn]))
		}
	}
	out := asmSym.ReplaceAllFunc(src, func(b []byte) []byte {
		m := asmSym.FindSubmatch(b)
		if n, ok := funcs[string(m[2])]; ok {
			return []byte(string(m[1]) + "·" + n)
		}
		return b
	})
	// go_asm.h names: T__field, T__size, const_NAME.
	out = asmIdent.ReplaceAllFunc(out, func(b []byte) []byte {
		s := string(b)
		if c, ok := strings.CutPrefix(s, "const_"); ok {
			if n, ok := consts[c]; ok {
				return []byte("const_" + n)
			}
		}
		if t, rest, ok := strings.Cut(s, "__"); ok {
			if n, ok := typeNames[t]; ok {
				return []byte(n + "__" + rest)
			}
		}
		return b
	})
	out = buildLine.ReplaceAll(out, nil)
	out = append([]byte(fmt.Sprintf(header, name, *arch)+"#undef GOOS_ape\n#define GOOS_"+hp.host+"\n"), out...)
	g.emit(outName(hp.host, name), out)
}

func (g *generator) writeDispatch() {
	var b bytes.Buffer
	fmt.Fprintf(&b, "// Code generated by cmd/apegen; DO NOT EDIT.\n\n//go:build ape && %s\n\npackage %s\n\nimport \"unsafe\"\n\nvar _ unsafe.Pointer\n", *arch, g.first().pkg.Name())
	b.Write(g.decls.Bytes())
	fmt.Fprintf(&b, "\n// apeinitHost sets what differs between hosts. It runs first.\nfunc apeinitHost() {\n%s}\n", g.init.Bytes())
	out := b.Bytes()
	if formatted, err := format.Source(out); err == nil {
		out = formatted
	} else {
		g.reportf("dispatch: %v", err)
	}
	g.emit("zape_dispatch_"+*arch+".go", out)
}

// writeAsmDispatch writes the assembly functions that jump to the
// host's body of a function a shared file declares.
func (g *generator) writeAsmDispatch() {
	var names []string
	for n, by := range g.asmDispatch {
		if len(by) != len(g.hs) {
			g.reportf("asm %s: implemented by only some hosts", n)
			continue
		}
		names = append(names, n)
	}
	if len(names) == 0 {
		return
	}
	sort.Strings(names)
	var b bytes.Buffer
	fmt.Fprintf(&b, "// Code generated by cmd/apegen; DO NOT EDIT.\n\n//go:build ape && %s\n\n#include \"go_asm.h\"\n#include \"textflag.h\"\n", *arch)
	for _, n := range names {
		abi := g.asmDispatch[n][g.first().host]
		fmt.Fprintf(&b, "\nTEXT ·%s%s(SB),NOSPLIT|NOFRAME,$0\n", n, abi)
		for i, hp := range g.hs {
			if i < len(g.hs)-1 {
				fmt.Fprintf(&b, "\tCMPB\truntime·apehost(SB), $const_ape%s\n\tJNE\t2(PC)\n", title(hp.host))
			}
			fmt.Fprintf(&b, "\tJMP\t·%s_%s%s(SB)\n", hp.host, n, g.asmDispatch[n][hp.host])
		}
	}
	g.emit("zape_dispatch_"+*arch+".s", b.Bytes())
}

// auditShared reports shared code that asks which system it runs on:
// under GOOS=ape that is a run-time question.
func (g *generator) auditShared() {
	hp := g.first()
	var names []string
	for name := range hp.files {
		if g.shared[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		ast.Inspect(hp.files[name], func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.SelectorExpr:
				if x, ok := n.X.(*ast.Ident); ok {
					if pn, ok := hp.info.Uses[x].(*types.PkgName); ok && pn.Imported().Path() == "internal/goos" && n.Sel.Name != "GOOS" {
						g.reportf("audit: %s: goos.%s", relPos(n.Pos()), n.Sel.Name)
					}
				}
			case *ast.Ident:
				if n.Name == "GOOS" {
					if c, ok := hp.info.Uses[n].(*types.Const); ok && c.Pkg() == hp.pkg {
						g.reportf("audit: %s: GOOS", relPos(n.Pos()))
					}
				}
			}
			return true
		})
	}
}

func relPos(p token.Pos) string {
	pos := fset.Position(p)
	return fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line)
}

func (g *generator) emit(name string, src []byte) {
	if !*write {
		fmt.Printf("would write %s (%d bytes)\n", name, len(src))
		return
	}
	if err := os.WriteFile(filepath.Join(g.dir, name), src, 0o666); err != nil {
		log.Fatal(err)
	}
}
