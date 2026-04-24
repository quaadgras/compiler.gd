// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"sync"

	"cmd/internal/src"
)

// OutBufNamePrefix is the Sym.Name prefix every gd Phase G
// synthesised outBufK param carries. Only used for virtual fields
// generated on demand by VirtualParams / VirtualRecvParams.
const OutBufNamePrefix = ".outBuf"

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
// *T fields. Virtual fields produced by VirtualParams have the
// naming convention baked in.
func (f *Field) IsOutBufParam() bool {
	if f == nil || f.Sym == nil {
		return false
	}
	return strings.HasPrefix(f.Sym.Name, OutBufNamePrefix)
}

// NumOutBufs returns the number of synthesised outBuf params the
// Virtual view of t exposes. Non-func types and unmarked func types
// return 0.
func (t *Type) NumOutBufs() int {
	if t == nil || t.Kind() != TFUNC || !t.GdReturnOutBuf() {
		return 0
	}
	n := 0
	for _, r := range t.Results() {
		if r.Type != nil && r.Type.IsPtr() {
			n++
		}
	}
	return n
}

// VirtualParams returns t.Params() extended with one synthesised
// outBufK *T field for each pointer-typed result. When t is not
// flagged with GdReturnOutBuf (or isn't a func type), returns
// t.Params() unchanged.
//
// The returned slice is freshly built each call; callers can modify
// the slice header but the underlying fields are shared with t.
//
// This is a projection, not a mutation: t.Params() keeps its stock
// length for reflect, type identity, and shape checks. Only
// ABI-level and call-emission consumers that explicitly ask for the
// virtual view see the extended form.
func (t *Type) VirtualParams() []*Field {
	params := t.Params()
	n := t.NumOutBufs()
	if n == 0 {
		return params
	}
	out := make([]*Field, 0, len(params)+n)
	out = append(out, params...)
	k := 0
	for _, r := range t.Results() {
		if r.Type == nil || !r.Type.IsPtr() {
			continue
		}
		out = append(out, NewField(src.NoXPos, outBufSym(k), r.Type))
		k++
	}
	return out
}

// VirtualRecvParams is VirtualParams prefixed by the receiver (if
// any), matching the layout of RecvParams().
func (t *Type) VirtualRecvParams() []*Field {
	if t.NumOutBufs() == 0 {
		return t.RecvParams()
	}
	recvs := t.Recvs()
	params := t.VirtualParams()
	out := make([]*Field, 0, len(recvs)+len(params))
	out = append(out, recvs...)
	out = append(out, params...)
	return out
}

// NumVirtualParams is len(VirtualParams()).
func (t *Type) NumVirtualParams() int {
	return t.NumParams() + t.NumOutBufs()
}
