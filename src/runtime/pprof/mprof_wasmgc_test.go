// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package pprof

// The tests of memory profiles (mprof_test.go) are left out with
// GOWASM=gc, where the runtime has none: the host allocates and
// collects. (They are left out by the file, and not skipped, as
// TestMemoryProfiler expects the lines of that file to be what they
// are.) This is what the other tests use of it.
var memSink any
