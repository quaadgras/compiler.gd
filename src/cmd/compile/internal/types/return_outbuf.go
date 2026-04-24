// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"sync"

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
//
// Known blockers to flipping (2026-04-25):
//   1. Runtime-special sigs are stock but non-runtime readers
//      construct fresh FuncTypes via NewSignature that extend
//      them. Cross-package generic/shape reshape assertions then
//      see (stock vs extended) forms of the same sig and fail
//      (e.g. runtime.FuncForPC typed as both func(uintptr)
//      *runtime.Func and func(uintptr, *runtime.Func)
//      *runtime.Func in the same compile).
//   2. Asm-backed decls like runtime.newobject need their .s
//      argsize bumped to match the extended ABI, else vet /
//      asmdecl screams.
//   3. cgo-generated wrappers need the trailing outBuf arg added
//      by cmd/cgo/out.go.
//
// Option-A fix for (1): universal extension — remove the
// CompilingRuntime gate from PhaseGApplies so every Go program's
// pointer-returning sigs are uniformly extended. Requires
// completing (2) and (3) first.
const PhaseGActive = false

// OutBufNamePrefix is the Sym.Name prefix every gd Phase G
// synthesised outBufK param carries. Used as a marker — consumers
// that need to tell "real user param" from "synthesised outBuf"
// match on this prefix (Field.IsOutBufParam).
const OutBufNamePrefix = ".outBuf"

// PhaseGApplies reports whether NewSignature should extend a sig
// with the given params/results. True when the gate is on and at
// least one result is a pointer type. Variadic sigs are supported —
// NewSignature inserts outBufs BEFORE the variadic slice so the
// slice stays at the tail (matching the Go ABI convention).
//
// Universal extension: we do NOT gate on CompilingRuntime. Every
// pointer-returning func Type in the universe is uniformly extended
// — runtime's Go code, asm decls, and cgo wrappers all see the
// extended ABI. That eliminates the cross-package stock-vs-extended
// shape mismatch that plagued the per-package approach. Asm and
// cgo sources have been updated in tandem to accept the extra arg.
func PhaseGApplies(params, results []*Field) bool {
	if !PhaseGActive {
		return false
	}
	_ = params // reserved; variadic handled by positioning, not skip.
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

// paramsAlreadyExtended reports whether params already contains any
// .outBufK field. Used by NewSignature to stay idempotent when the
// reader passes back already-extended params from pkgbits — and to
// tolerate the variadic layout where outBufs sit BEFORE the trailing
// variadic slice, so the last-param check isn't sufficient.
func paramsAlreadyExtended(params []*Field) bool {
	for _, p := range params {
		if p != nil && p.Sym != nil && strings.HasPrefix(p.Sym.Name, OutBufNamePrefix) {
			return true
		}
	}
	return false
}

// BuildOutBufFields returns a fresh slice of K outBufK Fields, one
// per pointer-typed result. Used by NewSignature to extend the
// param list, and by the pkgbits reader to extend already-written
// sigs whose origin applied the rewrite.
//
// Field type: unsafe.Pointer, not *T.
//
// Using a concrete unsafe.Pointer (rather than copying the result's
// *T type) keeps the outBuf fields out of SubstAny's way when the
// compiler-side builtin sig descriptors use `any` as a placeholder
// (runtime's mapaccess1(*byte, map[any]any, *any) *any etc.). An
// `*any` outBuf would double-count in the any-substitution loop;
// a plain unsafe.Pointer doesn't participate at all.
//
// ABI-wise this is fine — runtime.maybeInPlace and the caller's
// stack-buffer materialisation both operate on raw pointers anyway,
// and the callee casts back to the concrete type via the result's
// declared type.
func BuildOutBufFields(results []*Field) []*Field {
	n := countPointerResults(results)
	if n == 0 {
		return nil
	}
	out := make([]*Field, 0, n)
	k := 0
	unsafePtr := Types[TUNSAFEPTR]
	for _, r := range results {
		if r == nil || r.Type == nil || !r.Type.IsPtr() {
			continue
		}
		out = append(out, NewField(src.NoXPos, outBufSym(k), unsafePtr))
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

// StockifyCgoSig returns a stock-ABI copy of t, with outBuf params
// removed. Used by the noder's funcExt when it sees a
// //go:cgo_unsafe_args pragma — those wrappers treat the Go arg
// frame as a raw contiguous region that must match the C callee's
// layout, so they keep the unextended sig regardless of Phase G.
//
// Callers sharing a Type identity after stockification see the
// stock view too; Go's type system will accept assignments as long
// as the structural shape (user-visible) still matches.
func StockifyCgoSig(t *Type) *Type {
	if t == nil || t.Kind() != TFUNC || !t.GdReturnOutBuf() {
		return t
	}
	// Find the outBuf start. outBufs are contiguous within Params
	// (possibly followed by the variadic slice).
	params := t.Params()
	userParams := make([]*Field, 0, len(params))
	for _, p := range params {
		if p != nil && p.IsOutBufParam() {
			continue
		}
		userParams = append(userParams, p)
	}
	// Copy results as-is; Recv preserved.
	var recv *Field
	if t.Recv() != nil {
		recv = t.Recv()
	}
	results := t.Results()
	return NewSignatureAsIs(recv, userParams, results, false)
}
