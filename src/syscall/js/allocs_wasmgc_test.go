// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js && wasm && wasm.gc

package js_test

// manyArgAllocs is the number of allocations of a call with more than
// 16 arguments (see allocTests): with GOWASM=gc, the result's object and
// the slice of the numbers of the arguments. Go's own port also has a
// slice of the arguments' Values, kept alive through the call; here the
// host has them from before the call (js_gc.go).
const manyArgAllocs = 2
