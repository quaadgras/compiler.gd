// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"testing"
)

// Phase F4: synthesised compute fns for trivial forwarders.
// The detector tags wrappers like
//
//	func (w *W) M(args) { return w.inner.M(args) }
//
// with ir.Func.GdForwarder, the synthesizer emits a parallel
// compute fn that tail-calls runtime.resolveForwardedRecvFieldMask
// with the field-offset / method-idx baked in, and the install
// path puts the compute fn's funcsym (with bit 0 set) into the
// wrapper's itab mask slot.
//
// The end-to-end signal we test for: the synthesised compute fn
// actually fires at runtime when the wrapper is dispatched via
// iface. We can't easily count from inside the synth (no user-
// visible hooks), so we drive a wrapper whose inner is a custom
// type whose own escape profile we control, and verify the
// dispatch happens correctly. Allocs/run is a noisy proxy because
// it depends on the inner's mask being read precisely — the
// behavioural correctness check (Read returning the right bytes)
// is the load-bearing assertion.

// f4InnerProbe: counts Read calls so the test can confirm the
// dispatch made it through both the wrapper and the forwarded
// inner.
type f4InnerProbe struct {
	calls int
	src   string
}

func (p *f4InnerProbe) Read(b []byte) (int, error) {
	p.calls++
	n := copy(b, p.src)
	return n, nil
}

// f4Reader is the trivial-forwarder shape the F3 detector
// recognises: single-stmt return that delegates to an iface
// field with the same args. F4 must synthesise a compute fn
// for this method.
type f4Reader struct{ inner f4ReaderIface }

type f4ReaderIface interface {
	Read(b []byte) (int, error)
}

//go:noinline
func (r *f4Reader) Read(b []byte) (int, error) { return r.inner.Read(b) }

// TestF4ForwarderRoundtrip drives a wrapper-of-iface call
// through the synthesised compute-fn install. The pass condition
// is correctness: the wrapper returns the inner's bytes and the
// inner sees one call per outer call. If the install is broken
// (e.g. install wrote the entry address instead of the funcsym
// pointer), this test SIGSEGVs at the dispatch site.
func TestF4ForwarderRoundtrip(t *testing.T) {
	probe := &f4InnerProbe{src: "hello world"}
	w := &f4Reader{inner: probe}
	var iface f4ReaderIface = w
	for i := 0; i < 3; i++ {
		buf := make([]byte, 11)
		n, err := iface.Read(buf)
		if err != nil {
			t.Fatalf("iter %d: Read err=%v", i, err)
		}
		if got, want := string(buf[:n]), probe.src; got != want {
			t.Errorf("iter %d: got %q, want %q", i, got, want)
		}
	}
	if probe.calls != 3 {
		t.Errorf("inner probe calls: got %d, want 3", probe.calls)
	}
}
