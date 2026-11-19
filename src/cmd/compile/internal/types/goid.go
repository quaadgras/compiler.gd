// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !compiler_bootstrap

package types

import _ "unsafe" // for go:linkname

// calcSizeGoroutineID returns the id of the goroutine: CalcSize is
// called for every type that has no size yet, and when the go command
// compiles many packages at once, each in its goroutines, what
// runtime.Stack costs (a traceback under the one lock of the runtime's
// prints) was a quarter of a build.
func calcSizeGoroutineID() int64 { return runtime_goid() }

//go:linkname runtime_goid runtime.compilerGoid
func runtime_goid() int64
