// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/constant"
	"go/format"
	"go/token"
	"go/types"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// sameAll reports whether f gives the same string for every host.
func (g *pkgGen) sameAll(ic *iface, f func(hp *hostPkg, obj types.Object) string) (string, bool) {
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

// hostTypeString prints t as a host copy names it.
func (g *pkgGen) hostTypeString(hp *hostPkg, t types.Type) string {
	s := g.typeString(hp, t)
	var refs []*types.Named
	hp.namedRefs(t, map[types.Type]bool{}, &refs)
	for _, n := range refs {
		if g.hostNamed(hp, n) != nil {
			s = replaceIdent(s, n.Obj().Name(), hostName(hp.host, n.Obj().Name()))
		}
	}
	return s
}

func replaceIdent(s, old, new string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, old)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		before := i == 0 || !isIdent(s[i-1])
		after := i+len(old) == len(s) || !isIdent(s[i+len(old)])
		b.WriteString(s[:i])
		if before && after {
			b.WriteString(new)
		} else {
			b.WriteString(old)
		}
		s = s[i+len(old):]
	}
}

func isIdent(c byte) bool {
	return c == '_' || c == '.' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
}

// sameRep reports whether a value of type a in hp and one of type b in
// the first host (the ape type's layout) have the same representation
// and the same Go type once written: neither is a host type.
func (g *pkgGen) sameRep(hp *hostPkg, a types.Type, b types.Type) bool {
	var ra, rb []*types.Named
	hp.namedRefs(a, map[types.Type]bool{}, &ra)
	g.first().namedRefs(b, map[types.Type]bool{}, &rb)
	for _, n := range ra {
		if g.hostNamed(hp, n) != nil {
			return false
		}
	}
	for _, n := range rb {
		if g.hostNamed(g.first(), n) != nil {
			return false
		}
	}
	return g.typeString(hp, a) == g.typeString(g.first(), b)
}

// convStmts converts src (of type st) into dst (of type dt). toHost
// tells whether dst is hp's type (else the ape type). The first host's
// layout is the ape type's.
func (g *pkgGen) convStmts(hp *hostPkg, dst string, dt types.Type, src string, st types.Type, toHost bool, depth int) ([]string, bool) {
	hpd, hps := hp, g.first()
	if !toHost {
		hpd, hps = g.first(), hp
	}
	tname := func() string {
		if toHost {
			return g.hostTypeString(hp, dt)
		}
		return g.typeString(g.first(), dt)
	}
	if g.sameRep(hps, st, dt) || (hp == g.first() && g.typeString(hpd, dt) == g.typeString(hps, st)) {
		if hp == g.first() && g.hostNamed(hp, dt) != nil {
			// The same layout: reinterpret.
			return []string{assign(dst, dt, fmt.Sprintf("*(*%s)(unsafe.Pointer(&%s))", tname(), src))}, true
		}
		return []string{assign(dst, dt, src)}, true
	}
	db, dok := dt.Underlying().(*types.Basic)
	sb, sok := st.Underlying().(*types.Basic)
	_, dptr := dt.Underlying().(*types.Pointer)
	_, sptr := st.Underlying().(*types.Pointer)
	switch {
	case dok && sok && db.Info()&types.IsNumeric != 0 && sb.Info()&types.IsNumeric != 0:
		return []string{fmt.Sprintf("%s = %s(%s)", dst, tname(), src)}, true
	case dok && sok && db.Kind() == sb.Kind():
		return []string{fmt.Sprintf("%s = %s(%s)", dst, tname(), src)}, true
	case dok && db.Kind() == types.Uintptr && sptr:
		return []string{fmt.Sprintf("%s = uintptr(unsafe.Pointer(%s))", dst, src)}, true
	case sok && sb.Kind() == types.Uintptr && dptr:
		return []string{fmt.Sprintf("*(*uintptr)(unsafe.Pointer(&%s)) = %s", dst, src)}, true
	case dptr && sptr:
		return []string{fmt.Sprintf("*(*uintptr)(unsafe.Pointer(&%s)) = uintptr(unsafe.Pointer(%s))", dst, src)}, true
	}
	if ds, ok := dt.Underlying().(*types.Struct); ok {
		ss, ok := st.Underlying().(*types.Struct)
		if !ok {
			return nil, false
		}
		var out []string
		for df := range ds.Fields() {
			if df.Name() == "_" || strings.HasPrefix(df.Name(), "pad_") || strings.HasPrefix(df.Name(), "Pad_") {
				continue
			}
			for sf := range ss.Fields() {
				if sf.Name() != df.Name() {
					continue
				}
				dn := g.fieldName(hpd, df, toHost)
				sn := g.fieldName(hps, sf, !toHost)
				s, ok := g.convStmts(hp, dst+"."+dn, df.Type(), src+"."+sn, sf.Type(), toHost, depth+1)
				if !ok {
					return nil, false
				}
				out = append(out, s...)
			}
		}
		return out, true
	}
	if da, ok := dt.Underlying().(*types.Array); ok {
		sa, ok := st.Underlying().(*types.Array)
		if !ok {
			return nil, false
		}
		if g.typeString(hpd, da.Elem()) == g.typeString(hps, sa.Elem()) && g.sameRep(hps, sa.Elem(), da.Elem()) {
			return []string{fmt.Sprintf("copy(%s[:], %s[:])", dst, src)}, true
		}
		i := fmt.Sprintf("i%d", depth)
		body, ok := g.convStmts(hp, dst+"["+i+"]", da.Elem(), src+"["+i+"]", sa.Elem(), toHost, depth+1)
		if !ok {
			return nil, false
		}
		return []string{fmt.Sprintf("for %s := 0; %s < %d; %s++ {\n%s\n}", i, i, min(da.Len(), sa.Len()), i, strings.Join(body, "\n"))}, true
	}
	return nil, false
}

// fieldName is a field's name in a host copy (or the ape type).
func (g *pkgGen) fieldName(hp *hostPkg, f *types.Var, host bool) string {
	if host {
		if n, ok := hp.rename[f]; ok {
			return n
		}
	}
	return f.Name()
}

// assign stores x in dst, of type t; a pointer is stored as a uintptr,
// without a write barrier: signal handlers convert signal stacks.
func assign(dst string, t types.Type, x string) string {
	if _, ok := t.Underlying().(*types.Pointer); ok {
		return fmt.Sprintf("*(*uintptr)(unsafe.Pointer(&%s)) = uintptr(unsafe.Pointer(%s))", dst, x)
	}
	return fmt.Sprintf("%s = %s", dst, x)
}

// arg converts the ape-typed a for hp's parameter of type t: the
// statements before the call, the argument, and the statements after.
func (g *pkgGen) arg(hp *hostPkg, t types.Type, a string, j int) (pre []string, arg string, post []string, ok bool) {
	if sl, ok := types.Unalias(t).(*types.Slice); ok {
		if n := g.hostNamed(hp, sl.Elem()); n != nil {
			if _, isPtr := types.Unalias(sl.Elem()).(*types.Pointer); isPtr {
				return nil, "", nil, false
			}
			hostT := "[]" + hostName(hp.host, n.Obj().Name())
			if !g.canon[n.Obj().Name()] || hp == g.first() {
				return nil, fmt.Sprintf("*(*%s)(unsafe.Pointer(&%s))", hostT, a), nil, true
			}
			apeT := g.first().decls[n.Obj().Name()].Type()
			tmp := fmt.Sprintf("t%d", j)
			in, ok1 := g.convStmts(hp, tmp+"[i]", n, a+"[i]", apeT, true, 1)
			back, ok2 := g.convStmts(hp, a+"[i]", apeT, tmp+"[i]", n, false, 1)
			if !ok1 || !ok2 {
				return nil, "", nil, false
			}
			pre = []string{fmt.Sprintf("%s := make(%s, len(%s))\nfor i := range %s {\n%s\n}", tmp, hostT, a, a, strings.Join(in, "\n"))}
			post = []string{fmt.Sprintf("for i := range %s {\n%s\n}", a, strings.Join(back, "\n"))}
			return pre, tmp, post, true
		}
	}
	n := g.hostNamed(hp, t)
	if n == nil {
		return nil, a, nil, true
	}
	hostT := hostName(hp.host, n.Obj().Name())
	_, isPtr := types.Unalias(t).(*types.Pointer)
	if !g.canon[n.Obj().Name()] || hp == g.first() {
		// Opaque, or the same layout: reinterpret.
		if isPtr {
			return nil, fmt.Sprintf("(*%s)(unsafe.Pointer(%s))", hostT, a), nil, true
		}
		return nil, fmt.Sprintf("*(*%s)(unsafe.Pointer(&%s))", hostT, a), nil, true
	}
	apeT := g.first().decls[n.Obj().Name()].Type()
	tmp := fmt.Sprintf("t%d", j)
	src := a
	if isPtr {
		src = "(*" + a + ")"
	}
	in, ok1 := g.convStmts(hp, tmp, n, src, apeT, true, 0)
	back, ok2 := g.convStmts(hp, src, apeT, tmp, n, false, 0)
	if !ok1 || !ok2 {
		return nil, "", nil, false
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

// result converts hp's result x of type t to the ape type r.
func (g *pkgGen) result(hp *hostPkg, t types.Type, x string, r string) (stmts []string, expr string, ok bool) {
	if sl, ok := types.Unalias(t).(*types.Slice); ok {
		if n := g.hostNamed(hp, sl.Elem()); n != nil {
			if _, isPtr := types.Unalias(sl.Elem()).(*types.Pointer); isPtr {
				return nil, "", false
			}
			if !g.canon[n.Obj().Name()] || hp == g.first() {
				return nil, fmt.Sprintf("*(*%s)(unsafe.Pointer(&%s))", r, x), true
			}
			apeT := g.first().decls[n.Obj().Name()].Type()
			v := "r" + x
			s, ok := g.convStmts(hp, v+"[i]", apeT, x+"[i]", n, false, 1)
			if !ok {
				return nil, "", false
			}
			return []string{fmt.Sprintf("%s := make(%s, len(%s))\nfor i := range %s {\n%s\n}", v, r, x, x, strings.Join(s, "\n"))}, v, true
		}
	}
	n := g.hostNamed(hp, t)
	if n == nil {
		if r != g.typeString(hp, t) {
			return nil, fmt.Sprintf("%s(%s)", r, x), true
		}
		return nil, x, true
	}
	_, isPtr := types.Unalias(t).(*types.Pointer)
	if !g.canon[n.Obj().Name()] || hp == g.first() {
		if isPtr {
			return nil, fmt.Sprintf("(*%s)(unsafe.Pointer(%s))", n.Obj().Name(), x), true
		}
		return nil, fmt.Sprintf("*(*%s)(unsafe.Pointer(&%s))", n.Obj().Name(), x), true
	}
	apeT := g.first().decls[n.Obj().Name()].Type()
	v := "r" + x
	if isPtr {
		// A copy.
		s, ok := g.convStmts(hp, "(*"+v+")", apeT, "(*"+x+")", n, false, 0)
		if !ok {
			return nil, "", false
		}
		return []string{fmt.Sprintf("var %s *%s\nif %s != nil {\n%s = new(%s)\n%s\n}", v, n.Obj().Name(), x, v, n.Obj().Name(), strings.Join(s, "\n"))}, v, true
	}
	s, ok := g.convStmts(hp, v, apeT, x, n, false, 0)
	if !ok {
		return nil, "", false
	}
	return append([]string{fmt.Sprintf("var %s %s", v, n.Obj().Name())}, s...), v, true
}

// sigKey prints a signature as the ape package would declare it.
func (g *pkgGen) sigKey(hp *hostPkg, s *types.Signature, intResults bool) string {
	var b strings.Builder
	for v := range s.Params().Variables() {
		b.WriteString(g.typeString(hp, v.Type()) + ",")
	}
	if s.Variadic() {
		b.WriteString("...")
	}
	b.WriteString("->")
	for v := range s.Results().Variables() {
		if bt, ok := v.Type().Underlying().(*types.Basic); intResults && ok && bt.Info()&types.IsInteger != 0 && g.hostNamed(hp, v.Type()) == nil {
			b.WriteString("int,")
			continue
		}
		b.WriteString(g.typeString(hp, v.Type()) + ",")
	}
	return b.String()
}

func (g *pkgGen) pragmas(hp *hostPkg, obj types.Object) []string {
	var out []string
	// A one-argument linkname anywhere makes the name visible to other
	// packages: so it does the dispatch function's.
	if fn, ok := obj.(*types.Func); ok && fn.Signature().Recv() == nil {
		for _, f := range hp.files {
			for _, cg := range f.Comments {
				for _, c := range cg.List {
					if fs := strings.Fields(c.Text); len(fs) == 2 && fs[0] == "//go:linkname" && fs[1] == obj.Name() && !slices.Contains(out, c.Text) {
						out = append(out, c.Text)
					}
				}
			}
		}
	}
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
			if !strings.HasPrefix(c.Text, "//go:") || strings.HasPrefix(c.Text, "//go:linkname") || strings.HasPrefix(c.Text, "//go:noescape") || strings.HasPrefix(c.Text, "//go:build") {
				continue
			}
			out = append(out, c.Text)
		}
	}
	return out
}

func (g *pkgGen) dispatchFunc(ic *iface) {
	sig, ok := g.sameAll(ic, func(hp *hostPkg, obj types.Object) string {
		return g.sigKey(hp, obj.Type().(*types.Signature), true)
	})
	if !ok {
		if len(ic.uses) > 0 || g.path == "runtime" {
			g.reportf("%s %s: signatures differ: %s", ic.kind, ic.name, sig)
		}
		return
	}
	hp0 := g.first()
	fn := hp0.obj(ic).(*types.Func)
	s := fn.Signature()
	// Another package's host types: only host copies can call it.
	for _, hp := range g.hs {
		var refs []*types.Named
		hs := hp.obj(ic).Type().(*types.Signature)
		refsOf(hs.Params(), map[types.Type]bool{}, &refs)
		refsOf(hs.Results(), map[types.Type]bool{}, &refs)
		for _, r := range refs {
			if r.Obj().Pkg() != nil && r.Obj().Pkg() != hp.pkg && g.u.hostDiffers(r.Obj().Pkg().Path(), hp.host, r.Obj()) {
				if len(ic.uses) > 0 || *verbose {
					g.reportf("%s %s: not dispatched: it takes %s.%s, a host type", ic.kind, ic.name, r.Obj().Pkg().Name(), r.Obj().Name())
				}
				return
			}
		}
	}
	if s.TypeParams().Len() > 0 || (s.Recv() != nil && s.RecvTypeParams().Len() > 0) {
		g.reportf("%s %s: generic", ic.kind, ic.name)
		return
	}
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
		t := g.typeString(hp0, s.Params().At(i).Type())
		a := fmt.Sprintf("a%d", i)
		if s.Variadic() && i == s.Params().Len()-1 {
			params = append(params, a+" ..."+strings.TrimPrefix(t, "[]"))
			args = append(args, a)
			continue
		}
		params = append(params, a+" "+t)
		args = append(args, a)
	}
	var results []string
	for i := 0; i < s.Results().Len(); i++ {
		results = append(results, g.typeString(hp0, s.Results().At(i).Type()))
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
		recv = fmt.Sprintf("(r %s) ", g.typeString(hp0, s.Recv().Type()))
	}
	fmt.Fprintf(&w, "func %s%s(%s) %s {\n", recv, fn.Name(), strings.Join(params, ", "), res)
	for i, hp := range g.hs {
		hs := hp.obj(ic).(*types.Func).Signature()
		var cargs, pre, post []string
		for j := 0; j < hs.Params().Len(); j++ {
			a, b, c, ok := g.arg(hp, hs.Params().At(j).Type(), args[j], j)
			if !ok {
				g.reportf("%s %s: cannot convert parameter %d", ic.kind, ic.name, j)
				return
			}
			pre = append(pre, a...)
			post = append(post, c...)
			if s.Variadic() && j == hs.Params().Len()-1 {
				b += "..."
			}
			cargs = append(cargs, b)
		}
		callee := hostName(hp.host, fn.Name())
		if ic.kind == "method" {
			rt := hs.Recv().Type()
			if g.hostNamed(hp, rt) == nil {
				// A shared type: the host's method has the host's name.
				callee = "r." + hostName(hp.host, fn.Name())
			} else {
				a, b, c, ok := g.arg(hp, rt, "r", -1)
				if _, isPtr := types.Unalias(rt).(*types.Pointer); !isPtr {
					a, b, c, ok = g.arg(hp, types.NewPointer(rt), "&r", -1)
				}
				if !ok {
					g.reportf("%s %s: cannot convert the receiver", ic.kind, ic.name)
					return
				}
				pre = append(a, pre...)
				post = append(post, c...)
				callee = "(" + strings.ReplaceAll(b, "p-1", "pr") + ")." + fn.Name()
				for k := range pre {
					pre[k] = strings.ReplaceAll(strings.ReplaceAll(pre[k], "t-1", "tr"), "p-1", "pr")
				}
				for k := range post {
					post[k] = strings.ReplaceAll(post[k], "t-1", "tr")
				}
			}
		}
		call := fmt.Sprintf("%s(%s)", callee, strings.Join(cargs, ", "))
		var body []string
		body = append(body, pre...)
		var rets []string
		if len(results) == 0 {
			body = append(body, call)
		} else {
			var xs []string
			for j := range results {
				xs = append(xs, fmt.Sprintf("x%d", j))
			}
			body = append(body, strings.Join(xs, ", ")+" := "+call)
			for j := range results {
				st, e, ok := g.result(hp, hs.Results().At(j).Type(), xs[j], results[j])
				if !ok {
					g.reportf("%s %s: cannot convert result %d", ic.kind, ic.name, j)
					return
				}
				body = append(body, st...)
				rets = append(rets, e)
			}
		}
		body = append(body, post...)
		if len(results) > 0 {
			body = append(body, "return "+strings.Join(rets, ", "))
		}
		text := strings.Join(body, "\n")
		if i == len(g.hs)-1 {
			fmt.Fprintf(&w, "%s\n", text)
		} else {
			fmt.Fprintf(&w, "if apehost == ape%s {\n%s\n", title(hp.host), text)
			if len(results) == 0 {
				fmt.Fprintf(&w, "return\n")
			}
			fmt.Fprintf(&w, "}\n")
		}
	}
	fmt.Fprintf(&w, "}\n")
	g.decls.WriteString(w.String())
}

func (g *pkgGen) isRuntime() bool { return g.path == "runtime" }

func flagHas(flagValue, pkg, name string) (string, bool) {
	for _, kv := range strings.Split(flagValue, ",") {
		k, v, _ := strings.Cut(kv, "=")
		if k == pkg+"."+name {
			return v, true
		}
	}
	return "", false
}

func (g *pkgGen) declConst(ic *iface) {
	val, same := g.sameAll(ic, func(hp *hostPkg, obj types.Object) string {
		c := obj.(*types.Const)
		return g.typeString(hp, c.Type()) + " " + c.Val().ExactString()
	})
	c := g.first().obj(ic).(*types.Const)
	untyped := false
	if b, ok := c.Type().(*types.Basic); ok && b.Info()&types.IsUntyped != 0 {
		untyped = true
	}
	tstr := g.typeString(g.first(), c.Type())
	if same {
		if untyped {
			fmt.Fprintf(&g.decls, "\nconst %s = %s\n", ic.name, c.Val().ExactString())
		} else {
			fmt.Fprintf(&g.decls, "\nconst %s %s = %s\n", ic.name, tstr, c.Val().ExactString())
		}
		return
	}
	if _, ok := flagHas(*maxConsts, g.path, ic.name); ok {
		var best *types.Const
		for _, hp := range g.hs {
			o := hp.obj(ic).(*types.Const)
			if best == nil || constant.Compare(o.Val(), token.GTR, best.Val()) {
				best = o
			}
		}
		fmt.Fprintf(&g.decls, "\n// %s is the largest host's.\nconst %s = %s\n", ic.name, ic.name, best.Val().ExactString())
		return
	}
	// A variable, of the type shared code uses it as.
	t := tstr
	if untyped {
		used := map[string]bool{}
		for _, u := range ic.uses {
			if ut := g.first().useType(u); ut != nil {
				used[g.typeString(g.first(), ut)] = true
			}
		}
		var ts []string
		for k := range used {
			ts = append(ts, k)
		}
		sort.Strings(ts)
		if v, ok := flagHas(*constTypes, g.path, ic.name); ok {
			ts = []string{v}
		}
		t = types.Default(c.Type()).String()
		if len(ts) == 1 && !strings.HasPrefix(ts[0], "untyped") {
			t = ts[0]
		} else if len(ts) > 0 {
			g.reportf("const %s: differs (%s); shared code uses it as %v", ic.name, val, ts)
		}
	}
	g.declVarInit(ic.name, t, func(hp *hostPkg) string {
		v := hostName(hp.host, ic.name)
		if untyped {
			return t + "(" + v + ")"
		}
		return v
	}, "differs between hosts")
}

// declVarInit declares a variable holding each host's value.
func (g *pkgGen) declVarInit(name, t string, value func(hp *hostPkg) string, why string) {
	if g.isRuntime() {
		// The runtime sets them first, in apeinitHost.
		fmt.Fprintf(&g.decls, "\n// %s %s.\nvar %s %s\n", name, why, name, t)
		g.setInitStmt(func(hp *hostPkg) string { return name + " = " + value(hp) })
		return
	}
	fmt.Fprintf(&g.decls, "\n// %s %s.\nvar %s %s = func() %s {\n", name, why, name, t, t)
	for i, hp := range g.hs {
		if i == len(g.hs)-1 {
			fmt.Fprintf(&g.decls, "return %s\n", value(hp))
		} else {
			fmt.Fprintf(&g.decls, "if apehost == ape%s {\nreturn %s\n}\n", title(hp.host), value(hp))
		}
	}
	fmt.Fprintf(&g.decls, "}()\n")
}

func (g *pkgGen) setInitStmt(stmt func(hp *hostPkg) string) {
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

func (g *pkgGen) declType(ic *iface) {
	if len(ic.objs) != len(g.hs) {
		fmt.Fprintf(&g.decls, "\n// %s is %s's.\ntype %s %s\n", ic.name, g.first().host, ic.name, g.typeString(g.first(), g.first().obj(ic).Type().Underlying()))
		return
	}
	under, same := g.sameAll(ic, func(hp *hostPkg, obj types.Object) string {
		return g.typeString(hp, obj.Type().Underlying())
	})
	if same && g.canon[ic.name] {
		// The same layout written the same way, but of host types:
		// the first host's.
		same = false
	}
	if same {
		fmt.Fprintf(&g.decls, "\ntype %s %s\n", ic.name, under)
		return
	}
	if g.canon[ic.name] {
		fmt.Fprintf(&g.decls, "\n// %s is %s's. Functions convert other hosts' field by field.\ntype %s %s\n", ic.name, g.first().host, ic.name, g.typeString(g.first(), g.first().obj(ic).Type().Underlying()))
		if _, ok := g.first().obj(ic).Type().Underlying().(*types.Struct); ok && token.IsExported(ic.name) && !g.isRuntime() {
			g.fromAPEFuncs(ic)
		}
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
}

func (g *pkgGen) declUnion(ic *iface) {
	var b strings.Builder
	fmt.Fprintf(&b, "\n// %s is every host's.\ntype %s struct {\n", ic.name, ic.name)
	hoisted := map[string]string{}
	for _, hp := range g.hs {
		st := hp.obj(ic).Type().Underlying().(*types.Struct)
		for f := range st.Fields() {
			if hp.hoist[f] {
				t := g.typeString(hp, f.Type())
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
		fmt.Fprintf(&b, "\t%s\n", hostName(hp.host, ic.name))
	}
	b.WriteString("}\n")
	g.decls.WriteString(b.String())
}

func (g *pkgGen) declVar(ic *iface) {
	t, same := g.sameAll(ic, func(hp *hostPkg, obj types.Object) string {
		return g.typeString(hp, obj.Type())
	})
	if same {
		hostTyped := g.hostNamed(g.first(), g.first().obj(ic).Type()) != nil
		if g.isRuntime() {
			fmt.Fprintf(&g.decls, "\nvar %s %s\n", ic.name, t)
			g.setInitStmt(func(hp *hostPkg) string {
				if hostTyped {
					return fmt.Sprintf("%s = *(*%s)(unsafe.Pointer(&%s))", ic.name, t, hostName(hp.host, ic.name))
				}
				return ic.name + " = " + hostName(hp.host, ic.name)
			})
			return
		}
		if hostTyped {
			g.reportf("var %s: of a host type", ic.name)
			return
		}
		// A variable other packages may change: point at the host's.
		fmt.Fprintf(&g.decls, "\n// %s is the host's.\nvar %s = func() *%s {\n", "ape_"+ic.name, "ape_"+ic.name, t)
		for i, hp := range g.hs {
			if i == len(g.hs)-1 {
				fmt.Fprintf(&g.decls, "return &%s\n", hostName(hp.host, ic.name))
			} else {
				fmt.Fprintf(&g.decls, "if apehost == ape%s {\nreturn &%s\n}\n", title(hp.host), hostName(hp.host, ic.name))
			}
		}
		fmt.Fprintf(&g.decls, "}()\n")
		g.declVarInit(ic.name, t, func(hp *hostPkg) string { return hostName(hp.host, ic.name) }, "is the host's at startup")
		return
	}
	// Arrays that differ in length are as long as the longest, copied.
	var n int64
	elem, same := g.sameAll(ic, func(hp *hostPkg, obj types.Object) string {
		if a, ok := obj.Type().Underlying().(*types.Array); ok {
			n = max(n, a.Len())
			return g.typeString(hp, a.Elem())
		}
		return "?" + hp.host
	})
	if same && g.isRuntime() {
		fmt.Fprintf(&g.decls, "\n// %s is as long as the longest host's.\nvar %s [%d]%s\n", ic.name, ic.name, n, elem)
		g.setInitStmt(func(hp *hostPkg) string {
			return fmt.Sprintf("copy(%s[:], %s[:])", ic.name, hostName(hp.host, ic.name))
		})
		return
	}
	if same {
		g.declVarInit(ic.name, "[]"+elem, func(hp *hostPkg) string { return hostName(hp.host, ic.name) + "[:]" }, "is the host's")
		return
	}
	if len(ic.uses) > 0 || g.isRuntime() {
		g.reportf("var %s: types differ (%s)", ic.name, t)
	}
}

func (g *pkgGen) writeDispatch() {
	var b bytes.Buffer
	fmt.Fprintf(&b, "// Code generated by cmd/apegen; DO NOT EDIT.\n\n//go:build ape && %s\n\npackage %s\n\n", *arch, g.first().pkg.Name())
	imports := map[string]string{"unsafe": "unsafe"}
	for p, n := range g.imports {
		imports[p] = n
	}
	var paths []string
	for p := range imports {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	b.WriteString("import (\n")
	for _, p := range paths {
		if p == "unsafe" || regexp.MustCompile(`\b`+regexp.QuoteMeta(imports[p])+`\.`).Match(g.decls.Bytes()) {
			fmt.Fprintf(&b, "\t%q\n", p)
		}
	}
	b.WriteString(")\n\nvar _ unsafe.Pointer\n")
	if !g.isRuntime() {
		b.WriteString("\n//go:linkname apehost runtime.apehost\nvar apehost uint8\n\nconst (\n")
		for i, h := range g.u.hosts {
			if i == 0 {
				fmt.Fprintf(&b, "\tape%s = 1 + iota\n", title(h))
			} else {
				fmt.Fprintf(&b, "\tape%s\n", title(h))
			}
		}
		b.WriteString(")\n")
	}
	b.Write(g.decls.Bytes())
	if g.isRuntime() {
		fmt.Fprintf(&b, "\n// apeinitHost sets what differs between hosts. It runs first.\nfunc apeinitHost() {\n%s}\n", g.init.Bytes())
	}
	out := b.Bytes()
	if formatted, err := format.Source(out); err == nil {
		out = formatted
	} else {
		g.reportf("dispatch: %v", err)
	}
	g.outputs["zape_dispatch_"+*arch+".go"] = out
}

// writeAsmDispatch writes the assembly functions that jump to the
// host's body of a function a shared file declares.
func (g *pkgGen) writeAsmDispatch() {
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
	fmt.Fprintf(&b, "// Code generated by cmd/apegen; DO NOT EDIT.\n\n//go:build ape && %s\n\n#include \"textflag.h\"\n", *arch)
	if g.isRuntime() {
		b.WriteString("#include \"go_asm.h\"\n")
	}
	for _, n := range names {
		abi := g.asmDispatch[n][g.first().host]
		fmt.Fprintf(&b, "\nTEXT ·%s%s(SB),NOSPLIT|NOFRAME,$0\n", n, abi)
		for i, hp := range g.hs {
			if i < len(g.hs)-1 {
				if g.isRuntime() {
					fmt.Fprintf(&b, "\tCMPB\truntime·apehost(SB), $const_ape%s\n", title(hp.host))
				} else {
					fmt.Fprintf(&b, "\tCMPB\truntime·apehost(SB), $%d\n", i+1)
				}
				fmt.Fprintf(&b, "\tJNE\t2(PC)\n")
			}
			fmt.Fprintf(&b, "\tJMP\t·%s%s(SB)\n", hostName(hp.host, n), g.asmDispatch[n][hp.host])
		}
	}
	g.outputs["zape_dispatch_"+*arch+".s"] = b.Bytes()
}

// auditShared reports shared code that asks which system it runs on:
// under GOOS=ape that is a run-time question.
func (g *pkgGen) auditShared() {
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
					if pn, ok := hp.info.Uses[x].(*types.PkgName); ok && pn.Imported().Path() == "runtime" && n.Sel.Name == "GOOS" {
						g.reportf("audit: %s: runtime.GOOS", relPos(n.Pos()))
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

var _ = slices.Contains[[]string]

// fromAPEFuncs writes, for each host, the function that returns the
// host's copy of a *T (the ape type): other packages' host copies take
// it from shared structs.
func (g *pkgGen) fromAPEFuncs(ic *iface) {
	for _, hp := range g.hs {
		hostT := hostName(hp.host, ic.name)
		fn := hostName(hp.host, "FromAPE_"+ic.name)
		fmt.Fprintf(&g.decls, "\n// %s returns the host's copy of p.\nfunc %s(p *%s) *%s {\nif p == nil {\nreturn nil\n}\n", fn, fn, ic.name, hostT)
		if hp == g.first() {
			fmt.Fprintf(&g.decls, "return (*%s)(unsafe.Pointer(p))\n}\n", hostT)
			continue
		}
		stmts, ok := g.convStmts(hp, "t", hp.obj(ic).Type(), "(*p)", g.first().obj(ic).Type(), true, 0)
		if !ok {
			fmt.Fprintf(&g.decls, "panic(\"cannot convert %s\")\n}\n", ic.name)
			g.reportf("type %s: cannot convert to %s's", ic.name, hp.host)
			continue
		}
		fmt.Fprintf(&g.decls, "var t %s\n%s\nreturn &t\n}\n", hostT, strings.Join(stmts, "\n"))
	}
}
