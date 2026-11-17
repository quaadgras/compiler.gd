// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc && !js

package runtime

// jssync reports whether the goroutine running is that of a call from
// JavaScript that has not returned (see jscall.go): never.
func jssync() bool { return false }

// jsbusy tells the host that the runtime's state is or is not (on) to be
// left as it is while the goroutine is suspended (see jscall.go): no.
func jsbusy(on int32) {}
