// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"

	"cmd/internal/src"
)

// OutBufNamePrefix is the Sym.Name prefix every gd Phase G
// synthesised outBufK param carries. Only used for virtual fields
// generated on demand by VirtualParams / VirtualRecvParams.
const OutBufNamePrefix = ".outBuf"

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
		sym := LocalPkg.LookupNum(OutBufNamePrefix, k)
		out = append(out, NewField(src.NoXPos, sym, r.Type))
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
