// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

import (
	"internal/coverage/rtcov"
	_ "unsafe" // for go:linkname
)

// A Pinner is a set of Go objects each pinned to a fixed location in
// memory. With GOWASM=gc the engine's collector does not move objects,
// and an address of one cannot be passed to the host: pinning does
// nothing (natively it only keeps the object where it is).
type Pinner struct {
	_ [0]func()
}

// Pin pins a Go object (see the native runtime's Pinner.Pin).
func (p *Pinner) Pin(pointer any) {}

// Unpin unpins all pinned objects of the Pinner.
func (p *Pinner) Unpin() {}

// blockevent records a blocking event for the block profile, which is
// empty here.
func blockevent(cycles int64, skip int) {}

//go:linkname cgroup_throw internal/runtime/cgroup.throw
func cgroup_throw(s string) { throw(s) }

// getCovCounterList: programs built with GOWASM=gc are not
// instrumented for coverage.
//
//go:linkname getCovCounterList internal/coverage/cfile.getCovCounterList
func getCovCounterList() []rtcov.CovCounterBlob { return nil }
