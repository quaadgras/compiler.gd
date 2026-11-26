// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package noder

import (
	"internal/pkgbits"

	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/types"
)

// Generic instantiations that an imported package provides
// (-d=skipextinst=3, gd/extinst.go, golang.org/issue/56718).

var (
	// ExtInstFunc, if set, returns the summary that a direct import
	// recorded of the instantiation fn, whose code that package
	// provides (FuncSummaryText's), and whether there is one: if
	// not, fn is compiled by this package.
	ExtInstFunc func(gd *base.Invocation, fn *ir.Func) (summary string, ok bool)

	// ExtInstWrapper, if set, reports whether a direct import
	// provides the code of the wrapper with the given linker name.
	ExtInstWrapper func(gd *base.Invocation, name string) bool
)

// FuncSummaryText returns what export data would record of the
// function name, were it one of the package's own, as text.
func FuncSummaryText(name *ir.Name) string {
	var w textSummary
	writeFuncSummary(&w, name)
	return w.b.String()
}

// importExtInst reports whether an imported package provides the code
// of the instantiation name, and if so sets it up as an imported
// function is, whose body is only read in order to inline it (see
// reader.funcExt).
func (r *reader) importExtInst(name *ir.Name, method *types.Sym) bool {
	if ExtInstFunc == nil || name.Sym().Pkg == types.LocalPkg(r.gd) {
		return false
	}
	fn := name.Func
	text, ok := ExtInstFunc(r.gd, fn)
	if !ok {
		return false
	}
	words := splitSummary(text)
	if words == nil {
		return false
	}

	// What is read is the function's only if all of it is.
	saved := *fn
	params := name.Type().RecvParams()
	notes := make([]string, len(params))
	for i, f := range params {
		notes[i] = f.Note
	}
	sr := &summaryText{words: words}
	readFuncSummary(sr, name)
	if sr.bad || len(sr.words) != 0 {
		*fn = saved
		for i, f := range params {
			f.Note = notes[i]
		}
		return false
	}

	name.Defn = nil
	importBodyReader(r.gd)[name.Sym()] = pkgReaderIndex{pr: r.p, idx: r.Reloc(pkgbits.SectionBody), dict: r.dict, methodSym: method, gd: r.gd}
	return true
}

// extInstWrapper reports whether an imported package provides the
// code of the wrapper sym, so that this package need not generate it.
func extInstWrapper(gd *base.Invocation, sym *types.Sym) bool {
	return ExtInstWrapper != nil && !gd.Ctxt.Flag_linkshared && ExtInstWrapper(gd, sym.Pkg.Prefix+"."+sym.Name)
}
