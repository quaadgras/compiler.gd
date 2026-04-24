// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import "strings"

// OutBufNamePrefix is the Sym.Name prefix the gd Phase G return-value
// rewrite uses for synthesised outBuf params. Kept as a constant here
// (rather than in typecheck) so that types-level helpers can recognise
// them without introducing a dependency cycle.
//
// See doc/gd/escape-bits-phase-g-plan.md.
const OutBufNamePrefix = ".outBuf"

// IsOutBufParam reports whether f is a gd Phase G synthesised
// outBufK *T param. Returns false for the zero Field, for fields
// without a Sym, and for fields whose Sym name doesn't match the
// prefix. Call sites are expected to use IsOutBufParam rather than
// re-checking the prefix inline — keeps the discriminator in one
// place.
func (f *Field) IsOutBufParam() bool {
	if f == nil || f.Sym == nil {
		return false
	}
	return strings.HasPrefix(f.Sym.Name, OutBufNamePrefix)
}

// NumOutBufs returns the number of trailing synthesised outBuf params
// in signature type t. 0 for non-func types and for sigs without any
// outBufs appended.
//
// Phase G appends outBufs to the END of the params list, so we scan
// backwards and stop at the first non-outBuf field.
func (t *Type) NumOutBufs() int {
	if t == nil || t.Kind() != TFUNC {
		return 0
	}
	params := t.Params()
	n := 0
	for i := len(params) - 1; i >= 0; i-- {
		if !params[i].IsOutBufParam() {
			break
		}
		n++
	}
	return n
}

// UserParams returns t.Params() with trailing synthesised outBufs
// stripped. Use at every site that wants to present a user-visible
// view of the signature — reflect's In/NumIn, diagnostic printers,
// arity matching against user-written call args. ABI-level
// consumers (abiutils, SSA-gen, walk) should use Params() directly.
func (t *Type) UserParams() []*Field {
	params := t.Params()
	return params[:len(params)-t.NumOutBufs()]
}

// NumUserParams is the len of UserParams.
func (t *Type) NumUserParams() int {
	return t.NumParams() - t.NumOutBufs()
}

// OutBufs returns just the trailing synthesised outBuf fields. Empty
// for sigs without outBufs.
func (t *Type) OutBufs() []*Field {
	params := t.Params()
	return params[len(params)-t.NumOutBufs():]
}

// UserRecvParams returns RecvParams() with trailing synthesised
// outBufs stripped. OutBufs are always at the tail of params (never
// before results or within recv), so the strip count is the same as
// NumOutBufs.
func (t *Type) UserRecvParams() []*Field {
	rp := t.RecvParams()
	return rp[:len(rp)-t.NumOutBufs()]
}
