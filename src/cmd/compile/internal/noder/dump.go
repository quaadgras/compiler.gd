// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package noder

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/syntax"
	"cmd/compile/internal/types2"
)

// MatchASTDump returns true if the fn matches the value
// of the astdump debug flag.
func MatchASTDump(gd *base.Invocation, fn *syntax.FuncDecl) bool {
	if len(gd.Debug.AstDump) == 0 {
		return false
	}
	if fn.Name == nil {
		return false
	}
	return matchForDump(gd, fn, gd.Ctxt.Pkgpath)
}

// matchForDump is marked noinline to ensure that the exported
// function MatchAstDump IS inlineable and is also small, because
// common case is AstDump is not set.
//
//go:noinline
func matchForDump(gd *base.Invocation, fn *syntax.FuncDecl, pkgPath string) bool {
	return ir.MatchPkgFn(pkgPath, fn.Name.Value, gd.Debug.AstDump)
}

func escapedFileName(gd *base.Invocation, fn *syntax.FuncDecl, suffix string) string {
	return ir.EscapedFileName(gd.Ctxt.Pkgpath+"."+fn.Name.Value, suffix)
}

// DumpNodeHTML dumps the node n to the HTML writer for fn.
//
// The open writers live on gd (NoderHTMLWriters / NoderHTMLOrderedFuncs)
// rather than in package globals so concurrent in-process compile
// invocations each keep their own set.
func DumpNodeHTML(gd *base.Invocation, pkg *types2.Package, file *syntax.File, info *types2.Info, fn *syntax.FuncDecl, why string, n syntax.Node) {
	gd.AstDumpMu.Lock()
	defer gd.AstDumpMu.Unlock()
	htmlWriters, _ := gd.NoderHTMLWriters.(map[*syntax.FuncDecl]*HTMLWriter)
	if htmlWriters == nil {
		htmlWriters = make(map[*syntax.FuncDecl]*HTMLWriter)
		gd.NoderHTMLWriters = htmlWriters
	}
	w, ok := htmlWriters[fn]
	if !ok {
		name := escapedFileName(gd, fn, ".syntax.html")
		w = NewHTMLWriter(gd, pkg, file, info, name, fn, "")
		htmlWriters[fn] = w
		orderedFuncs, _ := gd.NoderHTMLOrderedFuncs.([]*syntax.FuncDecl)
		gd.NoderHTMLOrderedFuncs = append(orderedFuncs, fn)
	}
	w.WritePhase(why, why)
}

// CloseHTMLWriters closes every open syntax HTML writer of gd, if any exist.
func CloseHTMLWriters(gd *base.Invocation) {
	gd.AstDumpMu.Lock()
	defer gd.AstDumpMu.Unlock()
	htmlWriters, _ := gd.NoderHTMLWriters.(map[*syntax.FuncDecl]*HTMLWriter)
	orderedFuncs, _ := gd.NoderHTMLOrderedFuncs.([]*syntax.FuncDecl)
	for _, fn := range orderedFuncs {
		if w, ok := htmlWriters[fn]; ok {
			w.Close("Writing html syntax output for %s to %s\n", w.pkgFuncName(), w.Path())
			delete(htmlWriters, fn)
		}
	}
	gd.NoderHTMLOrderedFuncs = nil
}
