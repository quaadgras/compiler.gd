// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"sync"

	"cmd/compile/internal/base"
	"cmd/internal/src"
)

// PhaseGActive is the master gate for the gd Phase G return-value
// outBuf rewrite. When true, NewSignature automatically appends one
// outBufK *T field per pointer-typed result and flips
// typeGdReturnOutBuf — so every structurally-compatible func Type is
// uniformly extended, avoiding the caller/callee ABI mismatch that
// plagued the per-function pragma approach.
//
// Runtime-special compiles (CompilingRuntime) opt out entirely so
// the runtime's stock ABI is preserved end-to-end. Non-runtime Go
// code, asm, and cgo wrappers all see the extended form; asm/cgo
// need to be updated in tandem to accept the extra arg.
//
// Keep false until asm + cgo updates land, then flip to true
// behind make.bash to rebuild the fork compile tool with the
// extended ABI applied to its own code.
const PhaseGActive = false

// OutBufNamePrefix is the Sym.Name prefix every gd Phase G
// synthesised outBufK param carries. Used as a marker — consumers
// that need to tell "real user param" from "synthesised outBuf"
// match on this prefix (Field.IsOutBufParam).
const OutBufNamePrefix = ".outBuf"

// PhaseGApplies reports whether NewSignature should append outBuf
// params to a sig with the given results. True when the gate is on,
// we're not compiling runtime-special code, and at least one result
// is a pointer type.
func PhaseGApplies(results []*Field) bool {
	if !PhaseGActive {
		return false
	}
	if base.Flag.CompilingRuntime {
		return false
	}
	for _, r := range results {
		if r != nil && r.Type != nil && r.Type.IsPtr() {
			return true
		}
	}
	return false
}

// countPointerResults returns the number of pointer-typed results.
// NewSignature uses this to size the outBuf tail.
func countPointerResults(results []*Field) int {
	n := 0
	for _, r := range results {
		if r != nil && r.Type != nil && r.Type.IsPtr() {
			n++
		}
	}
	return n
}

// paramsAlreadyExtended reports whether params already contains a
// trailing .outBufK field. Used by NewSignature to stay idempotent
// when the reader passes back already-extended params from pkgbits.
func paramsAlreadyExtended(params []*Field) bool {
	if len(params) == 0 {
		return false
	}
	last := params[len(params)-1]
	return last != nil && last.Sym != nil && strings.HasPrefix(last.Sym.Name, OutBufNamePrefix)
}

// BuildOutBufFields returns a fresh slice of K outBufK *T Fields,
// one per pointer-typed result. Used by NewSignature to extend the
// param list, and by the pkgbits reader to extend already-written
// sigs whose origin applied the rewrite.
func BuildOutBufFields(results []*Field) []*Field {
	n := countPointerResults(results)
	if n == 0 {
		return nil
	}
	out := make([]*Field, 0, n)
	k := 0
	for _, r := range results {
		if r == nil || r.Type == nil || !r.Type.IsPtr() {
			continue
		}
		out = append(out, NewField(src.NoXPos, outBufSym(k), r.Type))
		k++
	}
	return out
}

// outBufSymInit guards one-time initialisation of outBufSyms.
var outBufSymInit sync.Once

// outBufSyms caches the gd Phase G `.outBufK` symbols so
// VirtualParams can build its field list without touching the
// unsynchronised LocalPkg.Syms map during parallel SSA generation.
//
// The Syms here are DELIBERATELY not registered in LocalPkg.Syms.
// staticdata.FuncLinksym holds funcsymsmu when doing LookupOK on
// package symbol maps during the backend; we have no access to
// that mutex from the types package, and the stock contract is
// that nothing else writes those maps concurrently. Since these
// synthesised syms are only used internally for marker identity
// (FieldIsOutBufParam matches by name prefix, not by Sym pointer),
// not publishing them to LocalPkg.Syms avoids the contention
// entirely — LookupOK for ".outBufK" returns a FRESH Sym to any
// caller that asks, which is fine because nothing in the codebase
// looks them up that way.
//
// Cap is larger than any realistic number of pointer results on a
// single function; overflow is handled by building a Sym on the fly.
var outBufSyms [32]*Sym

func initOutBufSyms() {
	for i := range outBufSyms {
		outBufSyms[i] = &Sym{
			Name: OutBufNamePrefix + itoa(i),
			Pkg:  LocalPkg,
		}
	}
}

// itoa is a local copy of strconv.Itoa to avoid importing strconv
// from the types package (which some build tags restrict).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// outBufSym returns the cached `.outBufK` Sym for k.
func outBufSym(k int) *Sym {
	outBufSymInit.Do(initOutBufSyms)
	if k < len(outBufSyms) {
		return outBufSyms[k]
	}
	// Overflow fallback — build a fresh Sym. No identity
	// guarantees across calls, but nothing depends on that.
	return &Sym{Name: OutBufNamePrefix + itoa(k), Pkg: LocalPkg}
}

// IsOutBufParam reports whether f is one of gd's synthesised outBufK
// *T fields. Matches by the Sym.Name prefix NewSignature bakes in.
func (f *Field) IsOutBufParam() bool {
	if f == nil || f.Sym == nil {
		return false
	}
	return strings.HasPrefix(f.Sym.Name, OutBufNamePrefix)
}

// NumOutBufs returns the number of synthesised outBuf params in t's
// param list. Non-func types and unflagged func types return 0.
// With Phase G.2.1 the outBufs are real params; this is a plain
// count, not a projection.
func (t *Type) NumOutBufs() int {
	if t == nil || t.Kind() != TFUNC || !t.GdReturnOutBuf() {
		return 0
	}
	return countPointerResults(t.Results())
}

// NumUserParams returns the param count excluding synthesised
// outBufs, i.e. the count the user source sees.
func (t *Type) NumUserParams() int {
	if t == nil || t.Kind() != TFUNC {
		return 0
	}
	return t.NumParams() - t.NumOutBufs()
}

// UserParams returns t.Params() with the synthesised outBufs
// filtered out, i.e. the params the user source sees. For stock
// sigs this is just t.Params().
func (t *Type) UserParams() []*Field {
	params := t.Params()
	nOut := t.NumOutBufs()
	if nOut == 0 {
		return params
	}
	return params[:len(params)-nOut]
}
