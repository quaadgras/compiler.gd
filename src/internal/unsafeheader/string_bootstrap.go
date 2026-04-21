// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build compiler_bootstrap

package unsafeheader

import "unsafe"

// String is the runtime representation of a string for bootstrap
// compilation: 2-word stock layout matching host Go 1.x.
//
// The bootstrap-built cmd/compile binary runs on host runtime; its
// `var s string` allocations are 2 words wide. Any `(*unsafeheader.String)
// (unsafe.Pointer(&s))` access within compile must therefore see a
// 2-word view — writing through a 3-word view would spill past the
// allocated string and corrupt adjacent stack memory.
//
// The non-bootstrap variant (string_gd.go, //go:build !compiler_bootstrap)
// is 3-word to match the gd fork's small-string-optimization layout
// (see doc/gd/sso-string.md).
type String struct {
	Data unsafe.Pointer
	Len  int
}
