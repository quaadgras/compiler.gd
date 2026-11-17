// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc && !js

package runtime

// jssync reports whether the goroutine running is that of a call from
// JavaScript that has not returned (see js_js.go): never.
func jssync() bool { return false }
