// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !amd64

package runtime

// The direct C-ABI virtual-call entry thunk is amd64-only; see
// fastcb_entry_amd64.go.
const fastcbEntrySupported = false

func fastcbentry() {
	throw("fastcbentry: not implemented on this architecture")
}
