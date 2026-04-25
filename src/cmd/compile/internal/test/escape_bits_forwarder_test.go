// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"io"
	"testing"
)

// Phase F3: trivial-forwarder detector. The detector tags
// matching ir.Funcs with ir.Func.GdForwarder so F4's compute-fn
// synthesis can target them. F3 is observation-only — no
// behaviour change. These tests exist to sanity-check that the
// compiler accepts a representative spectrum of forwarder shapes
// without breaking anything else.
//
// Verification of the detector's match decisions for the
// stdlib (and rejection of nil-checked pointer-wrappers,
// multi-stmt bodies, etc.) is done out-of-band via
// `-gcflags=all=-d=gdforwarder=2` during development.

// fwdForwarder is the canonical io.Reader-wrapper forwarder.
// The detector should match (*fwdForwarder).Read.
type fwdForwarder struct{ inner io.Reader }

func (w *fwdForwarder) Read(p []byte) (int, error) { return w.inner.Read(p) }

// fwdNotForwarder rejects: extra work after the inner call.
type fwdNotForwarder struct{ inner io.Reader }

func (w *fwdNotForwarder) Read(p []byte) (int, error) {
	n, err := w.inner.Read(p)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// fwdReorderedArgs rejects: arg reordering breaks 1:1 mapping.
type fwdReorderedArgs struct{ inner func(a, b *int) }

func (w *fwdReorderedArgs) Call(a, b *int) { w.inner(b, a) }

// TestForwarderShapesCompile covers a spectrum of forwarder and
// non-forwarder shapes. The test passes simply if the package
// compiles and the methods behave as expected — the detector
// tags F3 sets are inert until F4 lands. Behavioural regression
// (rather than detector regression) is what this guards against.
func TestForwarderShapesCompile(t *testing.T) {
	r := &fwdForwarder{inner: bytesReader{data: []byte("hello")}}
	buf := make([]byte, 5)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("forwarder Read: %v", err)
	}
	if n != 5 || string(buf) != "hello" {
		t.Errorf("forwarder Read: got %q (n=%d), want %q (n=5)", buf, n, "hello")
	}

	nr := &fwdNotForwarder{inner: bytesReader{data: []byte("world")}}
	buf2 := make([]byte, 5)
	if _, err := nr.Read(buf2); err != nil {
		t.Fatalf("not-forwarder Read: %v", err)
	}
	if string(buf2) != "world" {
		t.Errorf("not-forwarder Read: got %q, want %q", buf2, "world")
	}

	var ax, bx int
	called := false
	rr := &fwdReorderedArgs{inner: func(a, b *int) {
		called = true
		*a = 1
		*b = 2
	}}
	rr.Call(&ax, &bx)
	if !called {
		t.Fatal("reordered-args Call: inner not invoked")
	}
	// Args were swapped: the inner sees (b, a), so ax=*b=2 and bx=*a=1.
	if ax != 2 || bx != 1 {
		t.Errorf("reordered-args Call: got ax=%d bx=%d, want ax=2 bx=1", ax, bx)
	}
}

// bytesReader is a tiny io.Reader implementation so the test
// doesn't depend on bytes.Buffer (which has its own forwarders
// that would muddy test isolation).
type bytesReader struct {
	data []byte
	pos  int
}

func (b bytesReader) Read(p []byte) (int, error) {
	if b.pos >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.pos:])
	return n, nil
}
