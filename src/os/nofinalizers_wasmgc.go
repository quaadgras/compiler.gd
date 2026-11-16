// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package os

// noFinalizers reports that runtime.SetFinalizer does nothing (GOWASM=gc:
// a finalizer needs its object, which the host only reports collected
// once it is gone). Descriptors are closed by cleanups instead.
const noFinalizers = true
